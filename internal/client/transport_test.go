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

// cardAt builds a card whose single interface has an explicit URL and binding.
func cardAt(binding a2a.TransportProtocol, url string) *a2a.AgentCard {
	return &a2a.AgentCard{SupportedInterfaces: []*a2a.AgentInterface{a2a.NewAgentInterface(url, binding)}}
}

func TestSelectInterface_CardDeclaredPreference(t *testing.T) {
	// The Go hello-world card advertises JSONRPC; selection must pick it.
	c := card(a2a.TransportProtocolJSONRPC)
	sel, err := selectInterface(c, "", "http://svc", "", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if sel.transport != TransportJSONRPC || sel.iface.ProtocolBinding != a2a.TransportProtocolJSONRPC {
		t.Errorf("expected jsonrpc from card, got %q (%s)", sel.transport, sel.iface.ProtocolBinding)
	}
	if sel.reason == "" {
		t.Error("expected a non-empty selection reason")
	}
}

func TestSelectInterface_FirstSupportedWins(t *testing.T) {
	// gRPC is unsupported at Tier 1; selection skips it to the next supported one.
	c := card(a2a.TransportProtocolGRPC, a2a.TransportProtocolHTTPJSON)
	sel, err := selectInterface(c, "", "http://svc", "", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if sel.transport != TransportHTTPJSON {
		t.Errorf("expected http-json, got %q", sel.transport)
	}
}

func TestSelectInterface_ExplicitOverride(t *testing.T) {
	c := card(a2a.TransportProtocolJSONRPC, a2a.TransportProtocolHTTPJSON)
	sel, err := selectInterface(c, TransportHTTPJSON, "http://svc", "", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if sel.transport != TransportHTTPJSON || sel.iface.ProtocolBinding != a2a.TransportProtocolHTTPJSON {
		t.Errorf("explicit override failed: got %q (%s)", sel.transport, sel.iface.ProtocolBinding)
	}
}

func TestSelectInterface_ExplicitNotOffered(t *testing.T) {
	// A local request/card mismatch is a usage error (exit 2), not unreachable —
	// no dial is attempted (review O1, EM decision).
	c := card(a2a.TransportProtocolJSONRPC)
	_, err := selectInterface(c, TransportHTTPJSON, "http://svc", "", false, nil)
	if err == nil {
		t.Fatal("expected error when card lacks requested transport")
	}
	if clierr.ExitCode(err) != 2 {
		t.Errorf("want usage(2), got exit %d", clierr.ExitCode(err))
	}
}

func TestSelectInterface_ExplicitGRPCRejected(t *testing.T) {
	c := card(a2a.TransportProtocolGRPC)
	_, err := selectInterface(c, TransportGRPC, "http://svc", "", false, nil)
	if err == nil || clierr.ExitCode(err) != 2 {
		t.Errorf("gRPC should be a usage error(2), got %v", err)
	}
}

func TestSelectInterface_DefaultsToHTTPJSONWhenSilent(t *testing.T) {
	// Card offers nothing we speak (only gRPC) and no explicit transport:
	// default to HTTP+JSON synthesized at the service URL (spec §4.5).
	c := card(a2a.TransportProtocolGRPC)
	sel, err := selectInterface(c, "", "http://svc:9001", "", false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if sel.transport != TransportHTTPJSON || sel.iface.URL != "http://svc:9001" || sel.iface.ProtocolBinding != a2a.TransportProtocolHTTPJSON {
		t.Errorf("silent-card default failed: got %q %+v", sel.transport, sel.iface)
	}
}

func TestSelectInterface_UnknownTransport(t *testing.T) {
	_, err := selectInterface(card(a2a.TransportProtocolJSONRPC), "smoke-signals", "http://svc", "", false, nil)
	if err == nil || clierr.ExitCode(err) != 2 {
		t.Errorf("unknown transport should be usage error(2), got %v", err)
	}
}

// --- B2 (audit F-2): interface URL scheme / downgrade / cross-origin ---

func TestSelectInterface_RejectsNonHTTPScheme(t *testing.T) {
	// A card-declared interface with a file:// (or any non-http) scheme must be
	// rejected before it can be dialed or used as a credential Target.
	c := cardAt(a2a.TransportProtocolHTTPJSON, "file:///etc/passwd")
	_, err := selectInterface(c, "", "http://svc", "http://svc", false, nil)
	if err == nil {
		t.Fatal("expected error for non-http(s) interface scheme")
	}
	if clierr.ExitCode(err) != 1 {
		t.Errorf("want generic(1), got exit %d", clierr.ExitCode(err))
	}
}

func TestSelectInterface_RejectsTLSDowngrade(t *testing.T) {
	// Card fetched over https but the selected interface is plaintext http:
	// refuse the downgrade unless --insecure.
	c := cardAt(a2a.TransportProtocolHTTPJSON, "http://host/rest")
	_, err := selectInterface(c, "", "", "https://host", false, nil)
	if err == nil {
		t.Fatal("expected downgrade error for https-fetched card with http interface")
	}
	if clierr.ExitCode(err) != 1 {
		t.Errorf("want generic(1), got exit %d", clierr.ExitCode(err))
	}
}

func TestSelectInterface_DowngradeAllowedWithInsecure(t *testing.T) {
	c := cardAt(a2a.TransportProtocolHTTPJSON, "http://host/rest")
	sel, err := selectInterface(c, "", "", "https://host", true, nil)
	if err != nil {
		t.Fatalf("downgrade should be allowed under --insecure, got %v", err)
	}
	if sel.iface.URL != "http://host/rest" {
		t.Errorf("unexpected interface URL: %q", sel.iface.URL)
	}
}

func TestSelectInterface_CrossOriginWarns(t *testing.T) {
	// A card fetched from one host that points its interface elsewhere is allowed
	// but MUST be surfaced to the operator.
	c := cardAt(a2a.TransportProtocolHTTPJSON, "https://other-host/rest")
	var warned string
	warnf := func(format string, args ...any) { warned = format }
	sel, err := selectInterface(c, "", "", "https://card-host", false, warnf)
	if err != nil {
		t.Fatalf("cross-origin should be allowed, got %v", err)
	}
	if warned == "" {
		t.Error("expected a cross-origin warning to be emitted")
	}
	if sel.iface.URL != "https://other-host/rest" {
		t.Errorf("unexpected interface URL: %q", sel.iface.URL)
	}
}

func TestSelectInterface_SameOriginNoWarn(t *testing.T) {
	c := cardAt(a2a.TransportProtocolHTTPJSON, "https://host/rest")
	warned := false
	warnf := func(string, ...any) { warned = true }
	if _, err := selectInterface(c, "", "", "https://host", false, warnf); err != nil {
		t.Fatal(err)
	}
	if warned {
		t.Error("same-origin interface should not warn")
	}
}
