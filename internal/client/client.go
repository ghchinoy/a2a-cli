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
	"net"
	"net/http"

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

// Options configures a Client.
type Options struct {
	ServiceURL string             // base URL of the agent
	CardURL    string             // optional explicit agent-card URL
	Transport  string             // "", "http-json", "jsonrpc", "grpc"
	A2AVersion string             // protocol version to signal; defaults to "1.0"
	Insecure   bool               // skip TLS verification (emits a warning)
	Creds      CredentialProvider // per-request credential seam
	Warnf      func(string, ...any)
}

// Client wraps an a2aclient.Client with envelope-returning methods.
type Client struct {
	sdk       *a2aclient.Client
	card      *a2a.AgentCard
	transport string
	url       string
	version   string
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

	httpClient := &http.Client{}
	if opts.Insecure {
		opts.warnf("WARNING: TLS certificate verification is disabled (--insecure)")
		httpClient.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	}

	card, err := resolveCard(ctx, opts, version, httpClient)
	if err != nil {
		return nil, clierr.Wrap(clierr.KindUnreachable, "failed to fetch agent card: "+err.Error(), err)
	}

	iface, transportName, err := selectInterface(card, opts.Transport, opts.ServiceURL)
	if err != nil {
		return nil, err
	}
	// Signal the requested/default protocol version by pinning the selected
	// interface's version; the SDK sets the A2A-Version header from it on every
	// request (spec §11.2). This guarantees a non-empty value.
	iface.ProtocolVersion = a2a.ProtocolVersion(version)

	factoryOpts := []a2aclient.FactoryOption{
		a2aclient.WithJSONRPCTransport(httpClient),
		a2aclient.WithRESTTransport(httpClient),
	}
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
		version:   version,
	}, nil
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

// GetTask fetches the current state of a task (used by the poll loop).
func (c *Client) GetTask(ctx context.Context, id string) (*envelope.TaskResult, error) {
	task, err := c.sdk.GetTask(ctx, &a2a.GetTaskRequest{ID: a2a.TaskID(id)})
	if err != nil {
		return nil, classify(err, "get task failed")
	}
	return envelope.FromTask(task), nil
}

// classify maps an SDK/transport error to a normalized clierr.Error. Connection
// failures become unreachable (exit 3); everything else is generic (exit 1).
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
