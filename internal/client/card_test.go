// Copyright 2026 The a2a-cli Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/ghchinoy/a2a-cli/internal/clierr"
)

// newCardServer serves the given card at the well-known path over HTTP+JSON.
func newCardServer(t *testing.T, card *a2a.AgentCard) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == a2asrv.WellKnownAgentCardPath {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(card)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func restCard(url string) *a2a.AgentCard {
	iface := a2a.NewAgentInterface(url, a2a.TransportProtocolHTTPJSON)
	iface.ProtocolVersion = "1.0"
	return &a2a.AgentCard{
		Name:                "REST Full Agent",
		Description:         "full sections",
		Version:             "1.2.3",
		Provider:            &a2a.AgentProvider{Org: "Acme", URL: "http://acme.example"},
		DefaultInputModes:   []string{"text"},
		DefaultOutputModes:  []string{"text"},
		Capabilities:        a2a.AgentCapabilities{Streaming: true},
		SupportedInterfaces: []*a2a.AgentInterface{iface},
		SecuritySchemes: a2a.NamedSecuritySchemes{
			"apiKey": a2a.APIKeySecurityScheme{Location: "header", Name: "X-API-Key"},
		},
		Skills: []a2a.AgentSkill{{ID: "hello", Name: "Hello", Description: "hi", Tags: []string{"greeting"}}},
	}
}

func TestClient_FullCard_AllSectionsAndSelection(t *testing.T) {
	srv := newCardServer(t, restCard("http://example/rest"))
	cl, err := New(context.Background(), Options{ServiceURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	fc := cl.FullCard()
	if fc == nil {
		t.Fatal("FullCard nil")
	}
	if fc.Name != "REST Full Agent" || fc.Version != "1.2.3" {
		t.Errorf("identity: %+v", fc)
	}
	if len(fc.Interfaces) != 1 || fc.Interfaces[0].Transport != "HTTP+JSON" {
		t.Errorf("interfaces: %+v", fc.Interfaces)
	}
	if len(fc.SecuritySchemes) != 1 || fc.SecuritySchemes[0].Type != "apiKey" {
		t.Errorf("security: %+v", fc.SecuritySchemes)
	}
	if len(fc.Skills) != 1 {
		t.Errorf("skills: %+v", fc.Skills)
	}
	// Card declares only HTTP+JSON, so selection honors the declared preference.
	if fc.Selection.Transport != TransportHTTPJSON {
		t.Errorf("selected transport = %q, want %q", fc.Selection.Transport, TransportHTTPJSON)
	}
	if fc.Selection.Reason == "" {
		t.Error("selection reason should be set")
	}
}

// TestClient_FullCard_SelectionReason covers the three selection paths (design §11.1).
func TestClient_FullCard_SelectionReason(t *testing.T) {
	// Card offers only gRPC (unsupported at Tier 1): selection defaults to HTTP+JSON.
	grpcOnly := &a2a.AgentCard{
		Name:                "gRPC agent",
		Description:         "grpc",
		SupportedInterfaces: []*a2a.AgentInterface{a2a.NewAgentInterface("http://x/grpc", a2a.TransportProtocolGRPC)},
	}
	srv := newCardServer(t, grpcOnly)
	cl, err := New(context.Background(), Options{ServiceURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := cl.FullCard().Selection.Transport; got != TransportHTTPJSON {
		t.Errorf("silent/unsupported card should default to http-json, got %q", got)
	}
	if r := cl.FullCard().Selection.Reason; !contains(r, "default HTTP+JSON") {
		t.Errorf("reason = %q, want default HTTP+JSON explanation", r)
	}
}

func TestClient_ValidateCard_Valid(t *testing.T) {
	srv := newCardServer(t, restCard("http://example/rest"))
	cl, err := New(context.Background(), Options{ServiceURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := cl.ValidateCard(); err != nil {
		t.Errorf("valid card should pass validation, got %v", err)
	}
}

func TestClient_ValidateCard_Malformed(t *testing.T) {
	// Card parses (valid JSON) but is schema-invalid: missing description and skill id.
	// It still declares a usable interface so New() succeeds and validation is what
	// catches the problem (design §8.1 --validate).
	iface := a2a.NewAgentInterface("http://example/rest", a2a.TransportProtocolHTTPJSON)
	bad := &a2a.AgentCard{
		Name:                "No Description Agent",
		SupportedInterfaces: []*a2a.AgentInterface{iface},
		Skills:              []a2a.AgentSkill{{Name: "no id"}},
	}
	srv := newCardServer(t, bad)
	cl, err := New(context.Background(), Options{ServiceURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	verr := cl.ValidateCard()
	if verr == nil {
		t.Fatal("expected validation error for malformed card")
	}
	if clierr.ExitCode(verr) == 0 {
		t.Errorf("validation failure must be a non-zero exit, got 0")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
