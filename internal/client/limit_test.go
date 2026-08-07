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
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/ghchinoy/a2a-cli/internal/clierr"
)

// TestLimitedRT_CapsBody verifies the R-a (audit F-3) size cap: a body larger
// than the limit yields errCardTooLarge instead of streaming unbounded bytes.
func TestLimitedRT_CapsBody(t *testing.T) {
	const limit = 1 << 10 // 1 KiB for a fast test
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Stream well past the limit.
		_, _ = io.WriteString(w, strings.Repeat("x", limit*4))
	}))
	t.Cleanup(srv.Close)

	client := &http.Client{Transport: limitedRT{base: http.DefaultTransport, limit: limit}}
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()

	_, err = io.ReadAll(resp.Body)
	if err != errCardTooLarge {
		t.Fatalf("reading an oversized body: got err=%v, want errCardTooLarge", err)
	}
}

// TestLimitedRT_AllowsSmallBody confirms a body within the limit reads cleanly.
func TestLimitedRT_AllowsSmallBody(t *testing.T) {
	const limit = 1 << 10
	const payload = "hello"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, payload)
	}))
	t.Cleanup(srv.Close)

	client := &http.Client{Transport: limitedRT{base: http.DefaultTransport, limit: limit}}
	resp, err := client.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("reading a small body: %v", err)
	}
	if string(body) != payload {
		t.Errorf("body = %q, want %q", body, payload)
	}
}

// TestClient_New_OversizedCardBody is the integration guard: an oversized card
// response surfaces as an unreachable error (exit 3) via the capped card-fetch
// client, not an OOM.
func TestClient_New_OversizedCardBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == a2asrv.WellKnownAgentCardPath {
			w.Header().Set("Content-Type", "application/json")
			// A syntactically-open JSON object padded well beyond maxCardBytes.
			_, _ = io.WriteString(w, `{"name":"`)
			_, _ = io.WriteString(w, strings.Repeat("A", maxCardBytes+(1<<10)))
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)

	_, err := New(context.Background(), Options{ServiceURL: srv.URL})
	if err == nil {
		t.Fatal("expected an error fetching an oversized card")
	}
	if clierr.ExitCode(err) != 3 {
		t.Errorf("oversized card should be unreachable(3), got exit %d: %v", clierr.ExitCode(err), err)
	}
}
