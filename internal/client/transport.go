// Copyright 2026 The a2a-cli Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package client

import (
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

// selectInterface performs card-driven transport selection (design §3.3,
// resolves findings Q4). It deliberately does NOT use the SDK's JSON-RPC-first
// NewFromCard default; the precedence is:
//
//  1. an explicit --transport request wins (error if the card doesn't offer it);
//  2. otherwise the card's declared preference order (first supported interface);
//  3. otherwise, when the card offers nothing we can speak, default to HTTP+JSON
//     synthesized at serviceURL (spec §4.5).
//
// It returns the chosen AgentInterface and the CLI transport name.
func selectInterface(card *a2a.AgentCard, requested, serviceURL string) (*a2a.AgentInterface, string, error) {
	var interfaces []*a2a.AgentInterface
	if card != nil {
		interfaces = card.SupportedInterfaces
	}

	// 1. Explicit --transport request.
	if requested != "" {
		binding, ok := cliToBinding[requested]
		if !ok {
			return nil, "", clierr.New(clierr.KindUsage, "unknown transport: "+requested)
		}
		if !supported[binding] {
			return nil, "", clierr.New(clierr.KindUsage, "transport not supported at Tier 1: "+requested)
		}
		for _, iface := range interfaces {
			if iface != nil && iface.ProtocolBinding == binding {
				return iface, requested, nil
			}
		}
		// A local request/card mismatch is a usage error (exit 2), consistent with
		// unknown/unsupported transports above — no dial has been attempted, so it
		// is not "unreachable" (review O1, EM decision).
		return nil, "", clierr.New(clierr.KindUsage, "agent card does not offer transport "+requested)
	}

	// 2. Card's declared preference order: first interface we support.
	for _, iface := range interfaces {
		if iface != nil && supported[iface.ProtocolBinding] {
			return iface, bindingToCLI[iface.ProtocolBinding], nil
		}
	}

	// 3. Card silent / offers nothing we speak: default to HTTP+JSON.
	if serviceURL == "" {
		return nil, "", clierr.New(clierr.KindUnreachable, "no compatible transport in agent card and no service URL to default to")
	}
	iface := a2a.NewAgentInterface(serviceURL, a2a.TransportProtocolHTTPJSON)
	return iface, TransportHTTPJSON, nil
}

// selectionReason explains, in one human-readable line, why selectInterface
// chose the transport it did (design §11.1, surfaced by `discover`). It mirrors
// selectInterface's precedence without re-deciding it: it is called with the
// same inputs and the already-chosen transport name.
func selectionReason(card *a2a.AgentCard, requested, selected string) string {
	if requested != "" {
		return "explicit --transport=" + requested
	}
	if card != nil {
		for _, iface := range card.SupportedInterfaces {
			if iface != nil && supported[iface.ProtocolBinding] {
				return "card-declared preference (first supported interface: " + selected + ")"
			}
		}
	}
	return "default HTTP+JSON (card declared no transport this client speaks)"
}
