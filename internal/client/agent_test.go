// Copyright 2026 The A2A Authors
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
	"iter"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/ghchinoy/a2a-cli/internal/clierr"
)

// testExecutor is a minimal agent that replies with a bare Message, mirroring the
// Go hello-world server the CLI is validated against.
type testExecutor struct{}

func (*testExecutor) Execute(_ context.Context, _ *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		yield(a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("Hello from test")), nil)
	}
}

func (*testExecutor) Cancel(_ context.Context, _ *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {}
}

// headerRecorder captures the A2A-Version and Authorization headers (and path) of
// every request the client makes, so tests can assert wire-level invariants.
type headerRecorder struct {
	mu       sync.Mutex
	versions []string
	auths    []string
	paths    []string
}

func (h *headerRecorder) record(r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.versions = append(h.versions, r.Header.Get(a2a.SvcParamVersion))
	h.auths = append(h.auths, r.Header.Get("Authorization"))
	h.paths = append(h.paths, r.URL.Path)
}

func (h *headerRecorder) reset() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.versions, h.auths, h.paths = nil, nil, nil
}

func (h *headerRecorder) snapshot() (versions, auths, paths []string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string(nil), h.versions...), append([]string(nil), h.auths...), append([]string(nil), h.paths...)
}

// newTestAgent starts a protocol-correct JSONRPC A2A agent (built from the a2a-go
// server SDK) whose card advertises the server's own /invoke endpoint. Every
// request is recorded before being handled.
func newTestAgent(t *testing.T) (*httptest.Server, *headerRecorder) {
	t.Helper()
	rec := &headerRecorder{}
	var srv *httptest.Server

	mux := http.NewServeMux()
	mux.Handle("/invoke", a2asrv.NewJSONRPCHandler(a2asrv.NewHandler(&testExecutor{})))
	mux.HandleFunc(a2asrv.WellKnownAgentCardPath, func(w http.ResponseWriter, _ *http.Request) {
		card := &a2a.AgentCard{
			Name:        "Test Agent",
			Description: "test",
			SupportedInterfaces: []*a2a.AgentInterface{
				a2a.NewAgentInterface(srv.URL+"/invoke", a2a.TransportProtocolJSONRPC),
			},
			DefaultInputModes:  []string{"text"},
			DefaultOutputModes: []string{"text"},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(card)
	})

	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.record(r)
		mux.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

// TestClient_A2AVersionAlwaysSet is the automated regression guard for gate AC#5:
// the A2A-Version header must be present and non-empty on EVERY outbound request
// (card resolution and send), defaulting to "1.0" and honoring --a2a-version. An
// empty value makes servers assume legacy 0.3 (findings §B.7).
func TestClient_A2AVersionAlwaysSet(t *testing.T) {
	srv, rec := newTestAgent(t)

	cases := []struct {
		name    string
		version string
		want    string
	}{
		{"default", "", "1.0"},
		{"override", "1.5", "1.5"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec.reset()
			cl, err := New(context.Background(), Options{ServiceURL: srv.URL, A2AVersion: tc.version})
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			if _, err := cl.Send(context.Background(), SendRequest{Text: "hi"}); err != nil {
				t.Fatalf("Send: %v", err)
			}

			versions, _, paths := rec.snapshot()
			if len(versions) < 2 {
				t.Fatalf("expected at least card + invoke requests, got %d: %v", len(versions), paths)
			}
			sawInvoke := false
			for i, v := range versions {
				if v == "" {
					t.Errorf("request %d (%s) has EMPTY A2A-Version", i, paths[i])
				}
				if v != tc.want {
					t.Errorf("request %d (%s) A2A-Version = %q, want %q", i, paths[i], v, tc.want)
				}
				if paths[i] == "/invoke" {
					sawInvoke = true
				}
			}
			if !sawInvoke {
				t.Errorf("no /invoke request captured; paths=%v", paths)
			}
		})
	}
}

// TestClient_Send_AttachesCredentials confirms the CredentialProvider seam wires a
// bearer token onto the outbound request (design §3.3).
func TestClient_Send_AttachesCredentials(t *testing.T) {
	srv, rec := newTestAgent(t)

	cl, err := New(context.Background(), Options{
		ServiceURL: srv.URL,
		Creds:      &CallerSuppliedProvider{Bearer: "SECRET123"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := cl.Send(context.Background(), SendRequest{Text: "hi"}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	_, auths, paths := rec.snapshot()
	found := false
	for i, p := range paths {
		if p == "/invoke" && auths[i] == "Bearer SECRET123" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected 'Authorization: Bearer SECRET123' on /invoke; auths=%v paths=%v", auths, paths)
	}
}

// TestClient_Send_NormalizesBareMessage confirms a bare-Message response (no Task)
// is normalized to a completed TaskResult with null identifiers (design §3.4/§6.1).
func TestClient_Send_NormalizesBareMessage(t *testing.T) {
	srv, _ := newTestAgent(t)

	cl, err := New(context.Background(), Options{ServiceURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	tr, err := cl.Send(context.Background(), SendRequest{Text: "hi"})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}
	if tr.State != "TASK_STATE_COMPLETED" {
		t.Errorf("state = %q, want TASK_STATE_COMPLETED", tr.State)
	}
	if tr.TaskID != nil || tr.ContextID != nil {
		t.Errorf("ids must stay null for a bare Message; got taskId=%v contextId=%v", tr.TaskID, tr.ContextID)
	}
}

// TestClient_Classify_UnreachableExit3 (B4) asserts a dial/connection failure
// normalizes to KindUnreachable (exit 3).
func TestClient_Classify_UnreachableExit3(t *testing.T) {
	srv, _ := newTestAgent(t)
	cl, err := New(context.Background(), Options{ServiceURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	srv.Close() // endpoint now refuses connections (Close is idempotent vs. cleanup)

	_, err = cl.Send(context.Background(), SendRequest{Text: "hi"})
	if err == nil {
		t.Fatal("expected an error after the server closed")
	}
	if got := clierr.ExitCode(err); got != 3 {
		t.Errorf("connection failure exit code = %d, want 3 (unreachable)", got)
	}
}

// TestClient_New_TimeoutBoundsCardFetch (B2) asserts --timeout bounds the
// card-fetch/connect phase: against a server that never responds, New returns
// promptly rather than hanging.
func TestClient_New_TimeoutBoundsCardFetch(t *testing.T) {
	slow := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done() // block until the client gives up
	}))
	t.Cleanup(slow.Close)

	start := time.Now()
	_, err := New(context.Background(), Options{ServiceURL: slow.URL, Timeout: 200 * time.Millisecond})
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected a timeout error against a non-responding server")
	}
	if elapsed > 2*time.Second {
		t.Fatalf("New did not honor --timeout: took %v (want < 2s)", elapsed)
	}
}
