// Copyright 2026 The a2a-cli Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package client

import (
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/ghchinoy/a2a-cli/internal/clierr"
)

func card(bindings ...a2a.TransportProtocol) *a2a.AgentCard {
	c := &a2a.AgentCard{}
	for i, b := range bindings {
		c.SupportedInterfaces = append(c.SupportedInterfaces, a2a.NewAgentInterface("http://host/"+string(rune('a'+i)), b))
	}
	return c
}

func TestSelectInterface_CardDeclaredPreference(t *testing.T) {
	// The Go hello-world card advertises JSONRPC; selection must pick it.
	c := card(a2a.TransportProtocolJSONRPC)
	iface, name, err := selectInterface(c, "", "http://svc")
	if err != nil {
		t.Fatal(err)
	}
	if name != TransportJSONRPC || iface.ProtocolBinding != a2a.TransportProtocolJSONRPC {
		t.Errorf("expected jsonrpc from card, got %q (%s)", name, iface.ProtocolBinding)
	}
}

func TestSelectInterface_FirstSupportedWins(t *testing.T) {
	// gRPC is unsupported at Tier 1; selection skips it to the next supported one.
	c := card(a2a.TransportProtocolGRPC, a2a.TransportProtocolHTTPJSON)
	_, name, err := selectInterface(c, "", "http://svc")
	if err != nil {
		t.Fatal(err)
	}
	if name != TransportHTTPJSON {
		t.Errorf("expected http-json, got %q", name)
	}
}

func TestSelectInterface_ExplicitOverride(t *testing.T) {
	c := card(a2a.TransportProtocolJSONRPC, a2a.TransportProtocolHTTPJSON)
	iface, name, err := selectInterface(c, TransportHTTPJSON, "http://svc")
	if err != nil {
		t.Fatal(err)
	}
	if name != TransportHTTPJSON || iface.ProtocolBinding != a2a.TransportProtocolHTTPJSON {
		t.Errorf("explicit override failed: got %q (%s)", name, iface.ProtocolBinding)
	}
}

func TestSelectInterface_ExplicitNotOffered(t *testing.T) {
	c := card(a2a.TransportProtocolJSONRPC)
	_, _, err := selectInterface(c, TransportHTTPJSON, "http://svc")
	if err == nil {
		t.Fatal("expected error when card lacks requested transport")
	}
	if clierr.ExitCode(err) != 3 {
		t.Errorf("want unreachable(3), got exit %d", clierr.ExitCode(err))
	}
}

func TestSelectInterface_ExplicitGRPCRejected(t *testing.T) {
	c := card(a2a.TransportProtocolGRPC)
	_, _, err := selectInterface(c, TransportGRPC, "http://svc")
	if err == nil || clierr.ExitCode(err) != 2 {
		t.Errorf("gRPC should be a usage error(2), got %v", err)
	}
}

func TestSelectInterface_DefaultsToHTTPJSONWhenSilent(t *testing.T) {
	// Card offers nothing we speak (only gRPC) and no explicit transport:
	// default to HTTP+JSON synthesized at the service URL (spec §4.5).
	c := card(a2a.TransportProtocolGRPC)
	iface, name, err := selectInterface(c, "", "http://svc:9001")
	if err != nil {
		t.Fatal(err)
	}
	if name != TransportHTTPJSON || iface.URL != "http://svc:9001" || iface.ProtocolBinding != a2a.TransportProtocolHTTPJSON {
		t.Errorf("silent-card default failed: got %q %+v", name, iface)
	}
}

func TestSelectInterface_UnknownTransport(t *testing.T) {
	_, _, err := selectInterface(card(a2a.TransportProtocolJSONRPC), "smoke-signals", "http://svc")
	if err == nil || clierr.ExitCode(err) != 2 {
		t.Errorf("unknown transport should be usage error(2), got %v", err)
	}
}
