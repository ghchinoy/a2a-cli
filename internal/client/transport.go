// Copyright 2026 The A2A Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package client

import (
	"net/url"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/ghchinoy/a2a-cli/internal/clierr"
)

// Transport CLI flag values (spec §5.2).
const (
	TransportHTTPJSON = "http-json"
	TransportJSONRPC  = "jsonrpc"
	TransportGRPC     = "grpc"
)

// cliToBinding maps a --transport flag value to an SDK TransportProtocol.
var cliToBinding = map[string]a2a.TransportProtocol{
	TransportHTTPJSON: a2a.TransportProtocolHTTPJSON,
	TransportJSONRPC:  a2a.TransportProtocolJSONRPC,
	TransportGRPC:     a2a.TransportProtocolGRPC,
}

// bindingToCLI maps an SDK TransportProtocol back to a --transport flag value.
var bindingToCLI = map[a2a.TransportProtocol]string{
	a2a.TransportProtocolHTTPJSON: TransportHTTPJSON,
	a2a.TransportProtocolJSONRPC:  TransportJSONRPC,
	a2a.TransportProtocolGRPC:     TransportGRPC,
}

// supported is the set of bindings this Phase-1 client can actually speak.
// gRPC selection is intentionally left out until a later phase.
var supported = map[a2a.TransportProtocol]bool{
	a2a.TransportProtocolHTTPJSON: true,
	a2a.TransportProtocolJSONRPC:  true,
}

// selection is the outcome of card-driven transport selection: the chosen
// interface, its CLI transport name, and a one-line human-readable reason.
type selection struct {
	iface     *a2a.AgentInterface
	transport string
	reason    string
}

// selectInterface performs card-driven transport selection (design §3.3,
// resolves findings Q4). It deliberately does NOT use the SDK's JSON-RPC-first
// NewFromCard default; the precedence is:
//
//  1. an explicit --transport request wins (error if the card doesn't offer it);
//  2. otherwise the card's declared preference order (first supported interface);
//  3. otherwise, when the card offers nothing we can speak, default to HTTP+JSON
//     synthesized at serviceURL (spec §4.5).
//
// The reason is derived here — the single source of truth for both the chosen
// interface and the explanation surfaced by `discover` (review R-f).
//
// Every card-declared interface URL that is actually selected is validated before
// return (audit F-2, security fix B2): the URL must be http(s); a card fetched
// over https may not silently downgrade the selected interface to http (unless
// --insecure); and a cross-origin interface host is surfaced as a warning. Because
// this seam feeds both `discover` (presentation) and `send` (the credential-bearing
// connect + auth Target), every command that resolves a card inherits the check.
// fetchURL is the URL the card was fetched from (--card-url or --service-url).
func selectInterface(card *a2a.AgentCard, requested, serviceURL, fetchURL string, insecure bool, warnf func(string, ...any)) (*selection, error) {
	var interfaces []*a2a.AgentInterface
	if card != nil {
		interfaces = card.SupportedInterfaces
	}

	// 1. Explicit --transport request.
	if requested != "" {
		binding, ok := cliToBinding[requested]
		if !ok {
			return nil, clierr.New(clierr.KindUsage, "unknown transport: "+requested)
		}
		if !supported[binding] {
			return nil, clierr.New(clierr.KindUsage, "transport not supported at Tier 1: "+requested)
		}
		for _, iface := range interfaces {
			if iface != nil && iface.ProtocolBinding == binding {
				if err := validateInterfaceURL(iface.URL, fetchURL, insecure, warnf); err != nil {
					return nil, err
				}
				return &selection{iface: iface, transport: requested, reason: "explicit --transport=" + requested}, nil
			}
		}
		// A local request/card mismatch is a usage error (exit 2), consistent with
		// unknown/unsupported transports above — no dial has been attempted, so it
		// is not "unreachable" (review O1, EM decision).
		return nil, clierr.New(clierr.KindUsage, "agent card does not offer transport "+requested)
	}

	// 2. Card's declared preference order: first interface we support.
	for _, iface := range interfaces {
		if iface != nil && supported[iface.ProtocolBinding] {
			if err := validateInterfaceURL(iface.URL, fetchURL, insecure, warnf); err != nil {
				return nil, err
			}
			name := bindingToCLI[iface.ProtocolBinding]
			return &selection{
				iface:     iface,
				transport: name,
				reason:    "card-declared preference (first supported interface: " + name + ")",
			}, nil
		}
	}

	// 3. Card silent / offers nothing we speak: default to HTTP+JSON at the
	// user-supplied serviceURL. This URL is caller-controlled (not card-derived),
	// so it is not subject to the card-downgrade/cross-origin checks above.
	if serviceURL == "" {
		return nil, clierr.New(clierr.KindUnreachable, "no compatible transport in agent card and no service URL to default to")
	}
	iface := a2a.NewAgentInterface(serviceURL, a2a.TransportProtocolHTTPJSON)
	return &selection{
		iface:     iface,
		transport: TransportHTTPJSON,
		reason:    "default HTTP+JSON (card declared no transport this client speaks)",
	}, nil
}

// validateInterfaceURL enforces the Tier-1 transport-selection safety checks on a
// card-declared interface URL (audit F-2 / security fix B2). warnf may be nil.
func validateInterfaceURL(selected, fetchURL string, insecure bool, warnf func(string, ...any)) error {
	u, err := url.Parse(selected)
	if err != nil {
		return clierr.New(clierr.KindGeneric, "agent card interface URL is not a valid URL: "+selected)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return clierr.New(clierr.KindGeneric, "agent card interface URL uses unsupported scheme "+quote(u.Scheme)+" (only http/https are allowed): "+selected)
	}

	if fetchURL != "" {
		if f, ferr := url.Parse(fetchURL); ferr == nil && f.Host != "" {
			// Reject a TLS downgrade: a card fetched over https must not steer us
			// to a plaintext http interface unless the operator opts in.
			if strings.EqualFold(f.Scheme, "https") && scheme == "http" && !insecure {
				return clierr.New(clierr.KindGeneric,
					"agent card fetched over https declares an insecure http interface ("+selected+"); refusing to downgrade (use --insecure to override)")
			}
			// Surface (but allow) a cross-origin interface: a card may legitimately
			// point elsewhere, but the operator must see it.
			if !strings.EqualFold(f.Host, u.Host) && warnf != nil {
				warnf("WARNING: agent card selects a cross-origin interface: card fetched from %s but interface host is %s", f.Host, u.Host)
			}
		}
	}

	// TODO(phase6): when auth wiring lands, refuse to attach caller credentials to
	// a cross-origin or downgraded target without an explicit per-target opt-in.
	// The Tier-1 mitigation is the scheme-reject + downgrade-reject + cross-origin
	// warn above.
	return nil
}

// quote wraps s in double quotes for diagnostics without pulling in fmt.
func quote(s string) string { return "\"" + s + "\"" }
