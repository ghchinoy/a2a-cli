// Copyright 2026 The a2a-cli Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

// Package client is the sole SDK wrapper (design §3.3). It owns card resolution,
// card-driven transport selection, A2A-Version signaling, and auth attachment,
// and exposes narrow methods that return envelope types — never SDK/proto types —
// so the command layer stays free of wire concerns (import boundary, design §3.2).
package client

import (
	"context"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2aclient/agentcard"

	"github.com/ghchinoy/a2a-cli/internal/clierr"
	"github.com/ghchinoy/a2a-cli/internal/envelope"
)

// DefaultA2AVersion is the protocol version signaled when none is requested
// (spec §11.2). Never send an empty version — an empty value makes servers assume
// legacy 0.3 (findings §B.7).
const DefaultA2AVersion = "1.0"

// Default HTTP deadlines bound card fetch/connect/send against a slow or hostile
// server even when --timeout is not set (audit M-1). --timeout, when > 0, bounds
// the whole operation more tightly via context deadlines.
const (
	defaultHTTPTimeout           = 30 * time.Second
	defaultTLSHandshakeTimeout   = 10 * time.Second
	defaultResponseHeaderTimeout = 30 * time.Second
)

// maxCardBytes caps the agent-card response body (audit F-3, retires Phase-1
// I-2). The SDK reads the card with io.ReadAll and applies no size limit, so a
// hostile or misbehaving server could stream an unbounded body and exhaust
// memory. 4 MiB is far larger than any legitimate card; an oversized body errors
// during the fetch and surfaces as the existing unreachable/parse failure.
const maxCardBytes = 4 << 20 // 4 MiB

// errCardTooLarge is returned when a card body exceeds maxCardBytes.
var errCardTooLarge = errors.New("agent card response exceeds size limit (4 MiB)")

// limitedRT wraps a RoundTripper and caps every response body at limit bytes so a
// single fetch cannot exhaust memory (audit F-3). It is installed only on the
// card-fetch client, not on the send/get data-plane transport.
type limitedRT struct {
	base  http.RoundTripper
	limit int64
}

func (l limitedRT) RoundTrip(req *http.Request) (*http.Response, error) {
	resp, err := l.base.RoundTrip(req)
	if err != nil {
		return resp, err
	}
	resp.Body = &limitedBody{r: resp.Body, remaining: l.limit}
	return resp, nil
}

// limitedBody caps the number of bytes readable from a response body. Once the
// cap is exceeded it returns errCardTooLarge instead of more data.
type limitedBody struct {
	r         io.ReadCloser
	remaining int64
}

func (b *limitedBody) Read(p []byte) (int, error) {
	if b.remaining < 0 {
		return 0, errCardTooLarge
	}
	n, err := b.r.Read(p)
	b.remaining -= int64(n)
	if b.remaining < 0 {
		return n, errCardTooLarge
	}
	return n, err
}

func (b *limitedBody) Close() error { return b.r.Close() }

// Options configures a Client.
type Options struct {
	ServiceURL string             // base URL of the agent
	CardURL    string             // optional explicit agent-card URL
	Transport  string             // "", "http-json", "jsonrpc", "grpc"
	A2AVersion string             // protocol version to signal; defaults to "1.0"
	Insecure   bool               // skip TLS verification (emits a warning)
	Timeout    time.Duration      // overall per-operation deadline (0 = default only)
	Creds      CredentialProvider // per-request credential seam
	Warnf      func(string, ...any)
}

// Client wraps an a2aclient.Client with envelope-returning methods.
type Client struct {
	sdk       *a2aclient.Client
	card      *a2a.AgentCard
	transport string
	url       string
	routingID string
	reason    string
	version   string
	timeout   time.Duration
}

func (o Options) warnf(format string, args ...any) {
	if o.Warnf != nil {
		o.Warnf(format, args...)
	}
}

// New resolves the agent card, selects a transport, and builds a connected
// client. Card-fetch and connection failures are reported as unreachable (exit 3).
func New(ctx context.Context, opts Options) (*Client, error) {
	version := opts.A2AVersion
	if version == "" {
		version = DefaultA2AVersion
	}

	// Bound card fetch/connect/send even without --timeout: the shared client gets
	// an overall timeout and the transport handshake/response-header deadlines, so
	// a slow or hostile server cannot hang the CLI indefinitely (audit M-1). TLS
	// verification stays on unless --insecure is set, and only on this transport.
	transport := &http.Transport{
		TLSHandshakeTimeout:   defaultTLSHandshakeTimeout,
		ResponseHeaderTimeout: defaultResponseHeaderTimeout,
	}
	if opts.Insecure {
		opts.warnf("WARNING: TLS certificate verification is disabled (--insecure)")
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	httpClient := &http.Client{
		Timeout:   defaultHTTPTimeout,
		Transport: transport,
	}
	// The card-fetch client wraps the same transport with a body-size cap (audit
	// F-3): only the card fetch is bounded, not the data-plane send/get responses.
	cardHTTPClient := &http.Client{
		Timeout:   defaultHTTPTimeout,
		Transport: limitedRT{base: transport, limit: maxCardBytes},
	}

	// When --timeout is set, bound the card-fetch/connect phase on the context too.
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}

	card, err := resolveCard(ctx, opts, version, cardHTTPClient)
	if err != nil {
		return nil, clierr.Wrap(clierr.KindUnreachable, "failed to fetch agent card: "+err.Error(), err)
	}

	// The card was fetched from --card-url when set, else --service-url; that origin
	// anchors the downgrade/cross-origin checks inside selectInterface (B2).
	fetchURL := opts.ServiceURL
	if opts.CardURL != "" {
		fetchURL = opts.CardURL
	}
	sel, err := selectInterface(card, opts.Transport, opts.ServiceURL, fetchURL, opts.Insecure, opts.warnf)
	if err != nil {
		return nil, err
	}
	iface := sel.iface
	transportName := sel.transport
	reason := sel.reason
	// Signal the requested/default protocol version by pinning the selected
	// interface's version; the SDK sets the A2A-Version header from it on every
	// request (spec §11.2). This guarantees a non-empty value.
	iface.ProtocolVersion = a2a.ProtocolVersion(version)

	// Register the JSONRPC and REST transports (backed by httpClient so the
	// timeouts and --insecure config above apply) at the requested version — not
	// only the SDK's built-in 1.0. Without this the SDK matches by major version
	// and normalizes the data-plane A2A-Version back to "1.0", silently discarding
	// a --a2a-version override on send/get. Registering an exact-version key makes
	// the version the user signals the one actually sent on every request (AC#5).
	factoryOpts := transportFactoryOptions(a2a.ProtocolVersion(version), httpClient)
	if opts.Creds != nil {
		factoryOpts = append(factoryOpts, a2aclient.WithCallInterceptors(&authInterceptor{
			provider: opts.Creds,
			target:   Target{URL: iface.URL, Transport: transportName},
		}))
	}

	sdk, err := a2aclient.NewFromEndpoints(ctx, []*a2a.AgentInterface{iface}, factoryOpts...)
	if err != nil {
		return nil, clierr.Wrap(clierr.KindUnreachable, "failed to connect to agent: "+err.Error(), err)
	}

	return &Client{
		sdk:       sdk,
		card:      card,
		transport: transportName,
		url:       iface.URL,
		routingID: iface.Tenant,
		reason:    reason,
		version:   version,
		timeout:   opts.Timeout,
	}, nil
}

// transportFactoryOptions registers the Tier-1 transports (JSONRPC + HTTP+JSON)
// for the given protocol version, backed by httpClient. Registering at the
// requested version (rather than relying on the SDK's default 1.0 registration
// and its major-version compatibility fallback) ensures the A2A-Version the SDK
// sends on the data plane matches what the user requested via --a2a-version.
func transportFactoryOptions(version a2a.ProtocolVersion, httpClient *http.Client) []a2aclient.FactoryOption {
	jsonrpc := a2aclient.TransportFactoryFn(func(_ context.Context, _ *a2a.AgentCard, iface *a2a.AgentInterface) (a2aclient.Transport, error) {
		return a2aclient.NewJSONRPCTransport(iface.URL, httpClient), nil
	})
	rest := a2aclient.TransportFactoryFn(func(_ context.Context, _ *a2a.AgentCard, iface *a2a.AgentInterface) (a2aclient.Transport, error) {
		u, err := url.Parse(iface.URL)
		if err != nil {
			return nil, err
		}
		return a2aclient.NewRESTTransport(u, httpClient), nil
	})
	return []a2aclient.FactoryOption{
		a2aclient.WithCompatTransport(version, a2a.TransportProtocolJSONRPC, jsonrpc),
		a2aclient.WithCompatTransport(version, a2a.TransportProtocolHTTPJSON, rest),
	}
}

func resolveCard(ctx context.Context, opts Options, version string, httpClient *http.Client) (*a2a.AgentCard, error) {
	resolver := agentcard.NewResolver(httpClient)
	resolver.CardParser = agentcard.DefaultCardParser
	resolveOpts := []agentcard.ResolveOption{
		agentcard.WithRequestHeader(a2a.SvcParamVersion, version),
	}
	base := opts.ServiceURL
	if opts.CardURL != "" {
		base = opts.CardURL
		resolveOpts = append(resolveOpts, agentcard.WithPath(""))
	}
	return resolver.Resolve(ctx, base, resolveOpts...)
}

// Transport returns the selected transport name (e.g. "jsonrpc").
func (c *Client) Transport() string { return c.transport }

// URL returns the endpoint URL the client is connected to.
func (c *Client) URL() string { return c.url }

// Card returns the normalized agent card.
func (c *Client) Card() *envelope.Card {
	return envelope.FromCard(c.card, c.transport, c.url)
}

// FullCard returns the complete normalized agent card for `discover`, with the
// transport selection (chosen binding/URL, reason, routing id) attached
// (design §8.1/§11.1).
func (c *Client) FullCard() *envelope.FullCard {
	return envelope.FromFullCard(c.card, envelope.CardSelection{
		Transport: c.transport,
		URL:       c.url,
		Reason:    c.reason,
		RoutingID: c.routingID,
	})
}

// ValidateCard validates the resolved card against the A2A card schema
// (design §8.1 --validate). A malformed/invalid card is returned as a generic
// error (non-zero exit) whose message lists every problem found.
func (c *Client) ValidateCard() error {
	problems := envelope.ValidateCard(c.card)
	if len(problems) == 0 {
		return nil
	}
	msg := "agent card failed validation:"
	for _, p := range problems {
		msg += "\n  - " + p
	}
	return clierr.New(clierr.KindGeneric, msg)
}

// SendRequest is the input to Send.
type SendRequest struct {
	Text      string
	ContextID string
	TaskID    string
}

// Send sends a message and returns the normalized result. The result may be a
// Task or a bare Message (the Go hello-world server returns a Message) — both are
// normalized to a TaskResult (design §3.4).
func (c *Client) Send(ctx context.Context, req SendRequest) (*envelope.TaskResult, error) {
	// Bound the send phase on the context when --timeout is set (audit M-1); the
	// per-request http.Client timeout is the always-on safety net.
	if c.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}
	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart(req.Text))
	if req.ContextID != "" {
		msg.ContextID = req.ContextID
	}
	if req.TaskID != "" {
		msg.TaskID = a2a.TaskID(req.TaskID)
	}

	res, err := c.sdk.SendMessage(ctx, &a2a.SendMessageRequest{Message: msg})
	if err != nil {
		return nil, classify(err, "send failed")
	}
	return envelope.FromSendResult(res), nil
}

// ErrStreamingUnsupported reports that the resolved agent card does not advertise
// the streaming capability (Capabilities.Streaming == false). SendStream returns it
// WITHOUT attempting a stream so the caller can fall back to the blocking poll path
// (spec §11.3 capability gate). It is a sentinel: callers use errors.Is to detect it.
var ErrStreamingUnsupported = errors.New("agent card does not advertise the streaming capability")

// StreamHandler is invoked with each normalized streaming event AS IT ARRIVES
// (spec §7.2). Returning a non-nil error stops consumption and is surfaced by
// SendStream. The handler receives envelope types only — never SDK types — so the
// command layer stays free of wire concerns (import boundary, design §3.2).
type StreamHandler func(envelope.StreamEvent) error

// SupportsStreaming reports whether the resolved agent card advertises the
// streaming capability (spec §11.3). The command layer checks this to gate the
// stream attempt and fall back to blocking without ever attempting a stream.
func (c *Client) SupportsStreaming() bool {
	return c.card != nil && c.card.Capabilities.Streaming
}

// SendStream sends a message and consumes the SDK streaming iterator
// (sdk.SendStreamingMessage -> iter.Seq2[a2a.Event, error]), translating each event
// to an envelope.StreamEvent and passing it to handler as it arrives (spec §7.2).
// It returns the running snapshot accumulated from the events (so the caller retains
// the taskId for reconcile/fallback even on a mid-stream drop) and an error:
//   - ErrStreamingUnsupported when the card does not advertise streaming (no attempt);
//   - a KindTimeout error when the context deadline expires mid-stream (never hangs);
//   - context.Canceled on SIGINT (the caller keeps the already-surfaced taskId);
//   - a classified error on any other stream failure (the caller falls back to poll).
//
// Consumption stops as soon as the snapshot reaches a terminal or interrupted state
// so a paused (INPUT_REQUIRED/AUTH_REQUIRED) or finished task never waits for events
// that will not come (spec §7.2/§8.2 "MUST NOT deadlock"). The A2A-Version header
// stays non-empty on the streaming request: it rides the same version-pinned
// interface as send/get/cancel (spec §11.2), so no extra wiring is needed here.
func (c *Client) SendStream(ctx context.Context, req SendRequest, handler StreamHandler) (*envelope.TaskResult, error) {
	if !c.SupportsStreaming() {
		return nil, ErrStreamingUnsupported
	}
	// Bound the whole stream on the context when --timeout is set (audit M-1 / spec
	// §7.2 never hang); the per-request http.Client timeout is the always-on net.
	if c.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}
	msg := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart(req.Text))
	if req.ContextID != "" {
		msg.ContextID = req.ContextID
	}
	if req.TaskID != "" {
		msg.TaskID = a2a.TaskID(req.TaskID)
	}

	snap := &envelope.TaskResult{State: envelope.StateUnspecified}
	var haveAny bool
	var streamErr error

	for ev, err := range c.sdk.SendStreamingMessage(ctx, &a2a.SendMessageRequest{Message: msg}) {
		if err != nil {
			streamErr = classifyStream(ctx, err, "stream failed")
			break
		}
		se := envelope.FromEvent(ev)
		haveAny = true
		applyStreamEvent(snap, se)
		if handler != nil {
			if herr := handler(se); herr != nil {
				streamErr = herr
				break
			}
		}
		if envelope.IsTerminal(snap.State) || envelope.IsInterrupted(snap.State) {
			break
		}
	}
	if !haveAny && streamErr == nil {
		// An empty stream (200 then immediate EOF, or a stall bounded by the context)
		// yields nothing to render or reconcile; report it so the caller falls back.
		streamErr = classifyStream(ctx, errors.New("stream produced no events"), "stream failed")
	}
	return snap, streamErr
}

// applyStreamEvent folds a normalized streaming event into the running snapshot so
// the accumulated TaskResult always reflects the latest known ids/state/content.
// It operates purely on envelope types.
func applyStreamEvent(snap *envelope.TaskResult, se envelope.StreamEvent) {
	if se.TaskID != nil {
		snap.TaskID = se.TaskID
	}
	if se.ContextID != nil {
		snap.ContextID = se.ContextID
	}
	switch se.Type {
	case envelope.StreamTypeTask:
		if se.State != "" {
			snap.State = se.State
		}
		snap.Artifacts = append([]envelope.Artifact(nil), se.Artifacts...)
		if se.Message != nil {
			snap.Message = se.Message
		}
	case envelope.StreamTypeStatus:
		if se.State != "" {
			snap.State = se.State
		}
		if se.Message != nil {
			snap.Message = se.Message
		}
	case envelope.StreamTypeArtifact:
		if se.Artifact != nil {
			snap.Artifacts = append(snap.Artifacts, *se.Artifact)
		}
	case envelope.StreamTypeMessage:
		if se.Message != nil {
			snap.Message = se.Message
		}
		// A bare Message reply represents a completed interaction (as FromMessage
		// does for the non-streaming path); do not overwrite a real task state.
		if snap.State == envelope.StateUnspecified {
			snap.State = envelope.StateCompleted
		}
	}
}

// classifyStream maps a streaming error to a normalized clierr.Error, giving the
// context signals priority: a deadline is a TIMEOUT (exit 7, spec §7.2 never hang),
// a cancellation (SIGINT) propagates as context.Canceled so the caller can keep the
// already-surfaced taskId, and everything else defers to the shared classify.
func classifyStream(ctx context.Context, err error, msg string) error {
	if err == nil {
		return nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		if errors.Is(ctxErr, context.DeadlineExceeded) {
			return clierr.Wrap(clierr.KindTimeout, msg+": timed out while streaming", err)
		}
		if errors.Is(ctxErr, context.Canceled) {
			return ctxErr
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return clierr.Wrap(clierr.KindTimeout, msg+": timed out while streaming", err)
	}
	if errors.Is(err, context.Canceled) {
		return err
	}
	return classify(err, msg)
}

// GetOpts configures a GetTask call (design §3.3). HistoryLength, when non-nil,
// is threaded to the SDK as the server-side history bound (`get --history <n>`).
// IncludeArtifacts is a client-side content control: the SDK always returns the
// full task, so when false the wrapper summarizes artifacts by dropping their
// Parts (identifiers/names are kept) — "contents vs summarized" per spec §8.3.
type GetOpts struct {
	IncludeArtifacts bool
	HistoryLength    *int
}

// GetTask fetches the current state of a task (used one-shot by `get` and by the
// poll loop). opts.HistoryLength is threaded to the request; opts.IncludeArtifacts
// controls whether artifact contents are returned or summarized.
func (c *Client) GetTask(ctx context.Context, id string, opts GetOpts) (*envelope.TaskResult, error) {
	req := &a2a.GetTaskRequest{ID: a2a.TaskID(id)}
	if opts.HistoryLength != nil {
		req.HistoryLength = opts.HistoryLength
	}
	task, err := c.sdk.GetTask(ctx, req)
	if err != nil {
		return nil, classify(err, "get task failed")
	}
	tr := envelope.FromTask(task)
	if !opts.IncludeArtifacts {
		summarizeArtifacts(tr)
	}
	return tr, nil
}

// CancelTask requests cancellation of a task and returns the resulting state
// (spec §8.4). Cancellation is idempotent: a server may reject cancelling an
// already-terminal task with a not-cancelable error, so the wrapper falls back to
// a GetTask to report the resulting state cleanly rather than surfacing a spurious
// error on a repeat cancel. A task-not-found is normalized to KindNotFound (CO-2).
func (c *Client) CancelTask(ctx context.Context, id string) (*envelope.TaskResult, error) {
	if c.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}
	task, err := c.sdk.CancelTask(ctx, &a2a.CancelTaskRequest{ID: a2a.TaskID(id)})
	if err != nil {
		// Already-terminal task: cancel is a no-op. Report the current state so the
		// command stays idempotent and still satisfies "MUST report the resulting
		// state" (§8.4). If the follow-up get also fails, surface the original error.
		if errors.Is(err, a2a.ErrTaskNotCancelable) {
			if tr, gerr := c.GetTask(ctx, id, GetOpts{IncludeArtifacts: true}); gerr == nil {
				return tr, nil
			}
		}
		return nil, classify(err, "cancel failed")
	}
	return envelope.FromTask(task), nil
}

// summarizeArtifacts drops artifact Parts (content) while keeping identifiers and
// names, implementing the default `get` behavior (artifacts summarized unless
// --include-artifacts is set, spec §8.3).
func summarizeArtifacts(tr *envelope.TaskResult) {
	for i := range tr.Artifacts {
		tr.Artifacts[i].Parts = nil
	}
}

// classify maps an SDK/transport error to a normalized clierr.Error. Connection
// failures become unreachable (exit 3); a task-not-found is normalized to
// NOT_FOUND (CO-2); everything else is generic (exit 1). The normalization keys
// off the SDK's binding-independent sentinels (a2a.ErrTaskNotFound), which both
// the JSON-RPC (-32001) and REST (404/TASK_NOT_FOUND) transports resolve to, so
// the same A2A error yields the same tool result regardless of binding (§9.4).
// Richer cross-binding error normalization is Phase 6.
func classify(err error, msg string) error {
	if err == nil {
		return nil
	}
	var netErr net.Error
	var opErr *net.OpError
	if errors.As(err, &opErr) || errors.As(err, &netErr) {
		return clierr.Wrap(clierr.KindUnreachable, msg+": "+err.Error(), err)
	}
	if errors.Is(err, a2a.ErrTaskNotFound) {
		e := clierr.Wrap(clierr.KindNotFound, msg+": "+err.Error(), err)
		// Preserve the underlying A2A reason for debugging (design §3.4).
		e.A2ACode = a2a.ErrorReason(err)
		return e
	}
	return clierr.Wrap(clierr.KindGeneric, msg+": "+err.Error(), err)
}

// authInterceptor attaches per-request credentials from a CredentialProvider to
// the SDK request as service params (design §3.3).
type authInterceptor struct {
	provider CredentialProvider
	target   Target
}

func (a *authInterceptor) Before(ctx context.Context, req *a2aclient.Request) (context.Context, any, error) {
	if a.provider == nil {
		return ctx, nil, nil
	}
	headers, err := a.provider.Headers(ctx, a.target)
	if err != nil {
		return ctx, nil, clierr.Wrap(clierr.KindAuth, "failed to obtain credentials: "+err.Error(), err)
	}
	if req.ServiceParams == nil {
		req.ServiceParams = a2aclient.ServiceParams{}
	}
	for k, v := range headers {
		req.ServiceParams.Append(k, v)
	}
	return ctx, nil, nil
}

func (a *authInterceptor) After(_ context.Context, _ *a2aclient.Response) error {
	return nil
}
