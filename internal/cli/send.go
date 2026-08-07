// Copyright 2026 The A2A Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package cli

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/ghchinoy/a2a-cli/internal/client"
	"github.com/ghchinoy/a2a-cli/internal/clierr"
	"github.com/ghchinoy/a2a-cli/internal/config"
	"github.com/ghchinoy/a2a-cli/internal/envelope"
	"github.com/ghchinoy/a2a-cli/internal/poll"
	"github.com/ghchinoy/a2a-cli/internal/render"
	"github.com/ghchinoy/a2a-cli/internal/session"
)

func newSendCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "send <text>",
		Short: "Send a message to an agent and wait for the result",
		Long: "Send a message to an A2A agent over HTTP+JSON (blocking by default) and " +
			"print the normalized result. Use -o json for machine-readable output. " +
			"With --stream the result is streamed via SSE and reconciled with a get; " +
			"if the card does not advertise streaming, or the stream fails, it falls " +
			"back to the blocking poll path.",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return clierr.New(clierr.KindUsage, "send requires exactly one <text> argument")
			}
			return nil
		},
		RunE: runSend,
	}
	// --stream is send-specific (spec §7.2). It never silently changes behavior: an
	// agent that does not advertise streaming, or a stream that fails, falls back to
	// the blocking poll path.
	cmd.Flags().Bool(flagStream, false, "stream results via SSE, falling back to polling on failure")
	return cmd
}

func runSend(cmd *cobra.Command, args []string) error {
	text := args[0]
	flags := cmd.Flags()

	// Load session for the lowest-but-one precedence layer (design §3.7). A
	// non-existent session is not an error; any other Load failure is surfaced as
	// a stderr warning once the renderer exists (never on json stdout).
	sess, loadErr := session.Load()
	sessionDefaults := map[string]string{}
	if sess != nil {
		if sess.ServiceURL != "" {
			sessionDefaults[flagServiceURL] = sess.ServiceURL
		}
		if sess.Transport != "" {
			sessionDefaults[flagTransport] = sess.Transport
		}
	}

	// Resume prior conversation ids ONLY under an explicit --continue/--last (spec
	// §6.2/§6.4). Resuming is opt-in so a bare `send` never silently attaches to a
	// stale task; the serviceURL/transport auto-resume above stays unconditional.
	//   --continue: resume the stored contextId -> a NEW task within the same
	//               conversation (§6.2: only --context-id starts a new task). Does
	//               NOT bind the stored taskId.
	//   --last:     also resume the stored latestTaskId -> the message is sent
	//               AGAINST that task (§6.2: --task-id continues an existing task,
	//               e.g. replying to an INPUT_REQUIRED task).
	// These land in sessionDefaults, so config precedence (explicit flag >
	// sessionDefault) makes an explicit --context-id/--task-id override the stored
	// value automatically (§6.4 line 168). Final order: explicit flag >
	// --continue/--last stored id > session serviceURL/transport default > built-in
	// default. The no-session error is deferred until the renderer exists (below).
	resumeContinue := mustBool(flags, flagContinue)
	resumeLast := mustBool(flags, flagLast)
	if (resumeContinue || resumeLast) && sess != nil {
		if sess.ContextID != "" {
			sessionDefaults[flagContextID] = sess.ContextID
		}
		if resumeLast && sess.LatestTaskID != "" {
			sessionDefaults[flagTaskID] = sess.LatestTaskID
		}
	}

	defaults := map[string]string{
		flagOutput:     "text",
		flagA2AVersion: client.DefaultA2AVersion,
	}
	cfg := config.New(flags, sessionDefaults, defaults)

	// Resolve output mode: -n forces json; otherwise -o value.
	mode := render.ModeText
	noTUI, _ := flags.GetBool(flagNoTUI)
	switch {
	case noTUI:
		mode = render.ModeJSON
	case strings.EqualFold(cfg.String(flagOutput), "json"):
		mode = render.ModeJSON
	}
	r := render.New(mode, os.Stdout, os.Stderr)
	if loadErr != nil {
		r.Warn("WARNING: could not load session: %v", loadErr)
	}

	// --continue/--last with no stored session is a missing precondition, not a
	// transport/task failure: report it as a USAGE error (exit 2) — a stderr text
	// diagnostic in text mode, the Appendix B envelope on stdout in json mode
	// (usageError routes through the renderer). Exit-code choice flagged for review.
	if (resumeContinue || resumeLast) && sess == nil {
		return usageError(r, "no stored session to resume (--continue/--last); run a send first or pass --context-id/--task-id")
	}
	// --last with a session that never recorded a task (e.g. the hello-world server
	// returns a Message, not a Task, so latestTaskId is empty) has nothing to target:
	// warn on stderr and fall through to a new send rather than fail.
	if resumeLast && sess != nil && sess.LatestTaskID == "" {
		r.Warn("WARNING: --last: stored session has no latest task id; sending a new message")
	}

	serviceURL := cfg.String(flagServiceURL)
	if serviceURL == "" {
		return usageError(r, "a service URL is required (-u/--service-url)")
	}

	headers, err := parseHeaders(mustStringArray(flags, flagHeader))
	if err != nil {
		return usageError(r, err.Error())
	}

	// SIGINT cancels the context but never loses an already-known taskId (§7.3).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	cl, err := client.New(ctx, client.Options{
		ServiceURL:            serviceURL,
		CardURL:               cfg.String(flagCardURL),
		Transport:             cfg.String(flagTransport),
		A2AVersion:            cfg.String(flagA2AVersion),
		Insecure:              mustBool(flags, flagInsecure),
		Timeout:               mustDuration(flags, flagTimeout),
		Creds:                 resolveCredentials(flags, headers),
		AllowCrossOriginCreds: mustBool(flags, flagAllowXOrigin),
		Warnf:                 r.Warn,
	})
	if err != nil {
		return renderAndReturn(r, err)
	}

	req := client.SendRequest{
		Text:      text,
		ContextID: cfg.String(flagContextID),
		TaskID:    cfg.String(flagTaskID),
	}

	// --stream prefers SSE (spec §7.2) but degrades safely: if the card does not
	// advertise streaming it never attempts a stream (§11.3), and any stream failure
	// falls back to the blocking poll path (§7.3). Both routes reconcile the final
	// state with a get before reporting, share the exit-code mapping, and honor
	// --continue/--last (req is built identically).
	if mustBool(flags, flagStream) {
		return runSendStream(ctx, flags, r, cl, sess, req, serviceURL)
	}
	return runSendBlocking(ctx, flags, r, cl, sess, req, serviceURL)
}

// runSendBlocking is the non-streaming send path (spec §7): send, surface the
// taskId, persist the session, poll to a terminal/interrupted state, render, and
// map the state to an exit code. It is also the fallback target for --stream when
// streaming is unsupported or fails before a task materializes (§7.3), so there is a
// single implementation of the blocking behavior.
func runSendBlocking(ctx context.Context, flags *pflag.FlagSet, r *render.Renderer, cl *client.Client, sess *session.Session, req client.SendRequest, serviceURL string) error {
	tr, err := cl.Send(ctx, req)
	if err != nil {
		return renderAndReturn(r, err)
	}

	// Surface the taskId to stderr the moment it exists so it survives any later
	// timeout/interruption (§7.3). Never fabricate ids (§6.1).
	if tr.TaskID != nil {
		r.Warn("task %s created (context %s)", *tr.TaskID, ptrOr(tr.ContextID, "-"))
	}
	if err := captureSession(sess, tr, serviceURL, cl.Transport()); err != nil {
		r.Warn("WARNING: could not persist session: %v", err)
	}

	// Blocking wait unless the first result is already terminal/interrupted (the
	// Go hello-world server returns a terminal Message, so no polling occurs).
	if !envelope.IsTerminal(tr.State) && !envelope.IsInterrupted(tr.State) {
		final, perr := poll.Wait(ctx, tr, func(c context.Context) (*envelope.TaskResult, error) {
			// Poll with full artifacts so send still renders produced artifacts
			// (§8.2) — the get-options default summarizes them.
			return cl.GetTask(c, ptrOr(tr.TaskID, ""), client.GetOpts{IncludeArtifacts: true})
		}, poll.Options{
			Interval: mustDuration(flags, flagPollInterval),
			Timeout:  mustDuration(flags, flagTimeout),
		})
		if final != nil {
			tr = final
		}
		if perr != nil {
			// taskId already emitted to stderr above; surface a resume hint too.
			r.ResumeHint(tr.TaskID)
			return renderAndReturn(r, perr)
		}
	}

	if err := r.RenderTask(tr); err != nil {
		return err
	}
	return exitForState(r, tr)
}

// runSendStream is the --stream send path (spec §7.2/§7.3). It gates on the card's
// streaming capability, renders events as they arrive, reconciles the final state
// with a get, and falls back to the blocking path on any failure — never hanging.
func runSendStream(ctx context.Context, flags *pflag.FlagSet, r *render.Renderer, cl *client.Client, sess *session.Session, req client.SendRequest, serviceURL string) error {
	// Capability gate (§11.3): if the card does not advertise streaming, do NOT
	// attempt a stream — use the blocking poll path instead of hanging.
	if !cl.SupportsStreaming() {
		r.Warn("agent card does not advertise streaming; using the blocking request path")
		return runSendBlocking(ctx, flags, r, cl, sess, req, serviceURL)
	}

	// Render each event as it arrives (§7.2). The handler consumes envelope types
	// only (import boundary §3.2) and routes every server-derived value through the
	// render chokepoint (CO-5).
	handler := func(ev envelope.StreamEvent) error { return r.RenderStreamEvent(ev) }

	snap, serr := cl.SendStream(ctx, req, handler)
	if errors.Is(serr, client.ErrStreamingUnsupported) {
		// Defensive: SupportsStreaming already gated, but keep the contract that this
		// sentinel always means "fall back", never "fail".
		r.Warn("agent card does not advertise streaming; using the blocking request path")
		return runSendBlocking(ctx, flags, r, cl, sess, req, serviceURL)
	}

	// Surface the taskId to stderr the moment it exists so it survives a later
	// drop/timeout/SIGINT (§7.3). Never fabricate ids (§6.1).
	if snap != nil && snap.TaskID != nil {
		r.Warn("task %s created (context %s)", *snap.TaskID, ptrOr(snap.ContextID, "-"))
	}
	if snap != nil {
		if err := captureSession(sess, snap, serviceURL, cl.Transport()); err != nil {
			r.Warn("WARNING: could not persist session: %v", err)
		}
	}

	// No task materialized: either a bare Message reply (hello-world) or a failure
	// before any task existed. On failure fall back to a blocking send (the stream
	// may never have reached the server); on a clean Message stream, report it.
	if snap == nil || snap.TaskID == nil {
		if serr != nil {
			if errors.Is(serr, context.Canceled) {
				// Cancel before any task: emit a typed error record on the NDJSON path
				// (R2-1). No taskId exists yet; carry the contextId if the stream had one.
				var cid *string
				if snap != nil {
					cid = snap.ContextID
				}
				return streamErrorReturn(r, serr, nil, cid)
			}
			r.Warn("stream failed before a task was created; falling back to a blocking request")
			return runSendBlocking(ctx, flags, r, cl, sess, req, serviceURL)
		}
		tr := snap
		if tr == nil {
			tr = &envelope.TaskResult{State: envelope.StateUnspecified}
		}
		// R2-2: for a bare-Message stream the handler already rendered the message text
		// as it arrived, so re-rendering it via the final task view in TEXT mode is
		// redundant. Emit the terminal record only in NDJSON mode, where Appendix B
		// still requires the `final` object; text mode keeps just the streamed line.
		if r.Mode == render.ModeJSON {
			if err := r.RenderStreamFinal(tr); err != nil {
				return err
			}
		}
		return exitForState(r, tr)
	}

	// A task exists. Reconcile the authoritative final state with a get before
	// reporting (§7.2/§7.3: the reconciled get is authoritative, not the last
	// streamed event), then poll to terminal if the stream ended early (drop).
	tr := snap
	if serr != nil {
		if errors.Is(serr, context.Canceled) {
			// SIGINT: the taskId is already on stderr; surface a resume hint and stop.
			// On the NDJSON path emit a TYPED error record carrying the taskId (R2-1).
			r.ResumeHint(tr.TaskID)
			return streamErrorReturn(r, serr, tr.TaskID, tr.ContextID)
		}
		r.Warn("stream interrupted (%v); reconciling final state via get", serr)
	}

	getFn := func(c context.Context) (*envelope.TaskResult, error) {
		return cl.GetTask(c, ptrOr(tr.TaskID, ""), client.GetOpts{IncludeArtifacts: true})
	}
	if reconciled, gerr := getFn(ctx); gerr == nil && reconciled != nil {
		tr = reconciled
	} else if gerr != nil {
		// A terminal streamed snapshot whose get now fails (e.g. task already expired)
		// is still reportable from the stream; a non-terminal one is not, so surface
		// the error with a resume hint.
		if !envelope.IsTerminal(tr.State) && !envelope.IsInterrupted(tr.State) {
			r.ResumeHint(tr.TaskID)
			return streamErrorReturn(r, gerr, tr.TaskID, tr.ContextID)
		}
		r.Warn("could not reconcile final state via get (%v); reporting the last streamed state", gerr)
	}

	if !envelope.IsTerminal(tr.State) && !envelope.IsInterrupted(tr.State) {
		final, perr := poll.Wait(ctx, tr, getFn, poll.Options{
			Interval: mustDuration(flags, flagPollInterval),
			Timeout:  mustDuration(flags, flagTimeout),
		})
		if final != nil {
			tr = final
		}
		if perr != nil {
			r.ResumeHint(tr.TaskID)
			return streamErrorReturn(r, perr, tr.TaskID, tr.ContextID)
		}
	}

	if err := r.RenderStreamFinal(tr); err != nil {
		return err
	}
	return exitForState(r, tr)
}

// exitForState maps a final task state to the exit-code-carrying error (design
// §3.5). Interrupted states also get a resume hint on stderr. The task has already
// been rendered, so the error is marked rendered to avoid a duplicate diagnostic.
func exitForState(r *render.Renderer, tr *envelope.TaskResult) error {
	stateErr := clierr.FromState(tr.State)
	if stateErr == nil {
		return nil
	}
	if envelope.IsInterrupted(tr.State) {
		r.ResumeHint(tr.TaskID)
	}
	stateErr.MarkRendered()
	return stateErr
}

// captureSession persists the latest conversation identifiers (spec §6.4).
func captureSession(prev *session.Session, tr *envelope.TaskResult, serviceURL, transport string) error {
	s := &session.Session{ServiceURL: serviceURL, Transport: transport}
	if prev != nil {
		s.ContextID = prev.ContextID
	}
	if tr.ContextID != nil {
		s.ContextID = *tr.ContextID
	}
	if tr.TaskID != nil {
		s.LatestTaskID = *tr.TaskID
	}
	return session.Save(s)
}

// renderAndReturn renders an error as an Appendix B error object, marks it
// rendered (so cli.Execute does not surface it a second time), and returns it for
// exit-code mapping. A non-clierr error is wrapped as GENERIC, preserving its
// cause for errors.Is/As.
func renderAndReturn(r *render.Renderer, err error) error {
	var ce *clierr.Error
	if !errors.As(err, &ce) {
		ce = clierr.Wrap(clierr.KindGeneric, err.Error(), err)
	}
	_ = r.RenderError(ce.ToEnvelope())
	ce.MarkRendered()
	return ce
}

// streamErrorReturn is renderAndReturn for the `send --stream` terminal-error/cancel
// paths (R2-1). It normalizes err to a clierr.Error (wrapping a non-clierr as
// GENERIC, preserving the cause) exactly as renderAndReturn does, but renders through
// RenderStreamError so that in `-o json --stream` the error is a TYPED NDJSON record
// (carrying the Appendix B fields plus taskId/contextId when known) rather than the
// untyped one-shot envelope — keeping stdout one-object-per-line (spec §9.1). In text
// mode it is identical to renderAndReturn. The exit-code mapping is unchanged.
func streamErrorReturn(r *render.Renderer, err error, taskID, contextID *string) error {
	var ce *clierr.Error
	if !errors.As(err, &ce) {
		ce = clierr.Wrap(clierr.KindGeneric, err.Error(), err)
	}
	_ = r.RenderStreamError(ce.ToEnvelope(), taskID, contextID)
	ce.MarkRendered()
	return ce
}

func usageError(r *render.Renderer, msg string) error {
	e := clierr.New(clierr.KindUsage, msg)
	_ = r.RenderError(e.ToEnvelope())
	e.MarkRendered()
	return e
}

// resolveCredentials builds the caller-supplied credential provider with the
// §10.1 precedence explicit flag > environment variable > unset: --bearer pairs
// with A2A_BEARER and --api-key with A2A_API_KEY. -H/--header stays flag-only.
// Credentials are never persisted (design §191): this only reads flags/env.
func resolveCredentials(flags *pflag.FlagSet, headers map[string]string) *client.CallerSuppliedProvider {
	return &client.CallerSuppliedProvider{
		Bearer: flagOrEnv(flags, flagBearer, envBearer),
		APIKey: flagOrEnv(flags, flagAPIKey, envAPIKey),
		Extra:  headers,
	}
}

// flagOrEnv returns the string flag value when the user set it explicitly, else
// the environment variable when present, else the flag's built-in default. This
// keeps the credential precedence (flag > env > unset) independent of the viper
// config chain, which uses the A2A_CLI_ prefix rather than the §10.1 names.
func flagOrEnv(flags *pflag.FlagSet, flagName, envName string) string {
	if flags.Changed(flagName) {
		v, _ := flags.GetString(flagName)
		return v
	}
	if v, ok := os.LookupEnv(envName); ok {
		return v
	}
	v, _ := flags.GetString(flagName)
	return v
}

// parseHeaders parses repeatable -H "Name: Value" flags into a map, validating
// each header (audit L-2): the name must be a non-empty RFC 7230 token and the
// value must not contain CR or LF. Malformed input yields a usage error.
func parseHeaders(hs []string) (map[string]string, error) {
	if len(hs) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(hs))
	for _, h := range hs {
		name, val, ok := strings.Cut(h, ":")
		if !ok {
			return nil, errString("invalid header (want 'Name: Value'): " + h)
		}
		name = strings.TrimSpace(name)
		val = strings.TrimSpace(val)
		if name == "" {
			return nil, errString("invalid header (empty name): " + h)
		}
		if !isValidHeaderName(name) {
			return nil, errString("invalid header name: " + name)
		}
		if strings.ContainsAny(val, "\r\n") {
			return nil, errString("invalid header value (contains CR/LF) for: " + name)
		}
		out[name] = val
	}
	return out, nil
}

// isValidHeaderName reports whether name is a valid RFC 7230 header field-name
// (a non-empty token).
func isValidHeaderName(name string) bool {
	for _, c := range name {
		if !isTokenChar(c) {
			return false
		}
	}
	return true
}

// isTokenChar reports whether c is a valid RFC 7230 token character.
func isTokenChar(c rune) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	return strings.ContainsRune("!#$%&'*+-.^_`|~", c)
}

func ptrOr(s *string, fallback string) string {
	if s == nil {
		return fallback
	}
	return *s
}

// --- small flag/error helpers -------------------------------------------------

type errString string

func (e errString) Error() string { return string(e) }

func mustBool(f *pflag.FlagSet, name string) bool {
	v, _ := f.GetBool(name)
	return v
}

func mustDuration(f *pflag.FlagSet, name string) time.Duration {
	v, _ := f.GetDuration(name)
	return v
}

func mustStringArray(f *pflag.FlagSet, name string) []string {
	v, _ := f.GetStringArray(name)
	return v
}
