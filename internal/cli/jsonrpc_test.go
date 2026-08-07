// Copyright 2026 The a2a-cli Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// jsonrpcCardJSON is a minimal agent card that advertises a single JSON-RPC
// interface pointing back at the serving host, so data-plane calls route to the
// same server and pass the B2 same-origin check. Kept as a raw Go map so these
// tests import no SDK/proto types (import boundary §3.2).
func jsonrpcCardJSON(url string) map[string]any {
	return map[string]any{
		"name":               "JSONRPC Task Test Agent",
		"description":        "serves JSON-RPC errors for binding-independence tests",
		"version":            "1.0.0",
		"capabilities":       map[string]any{"streaming": false, "pushNotifications": false},
		"defaultInputModes":  []string{"text"},
		"defaultOutputModes": []string{"text"},
		"supportedInterfaces": []map[string]any{
			{"url": url, "protocolBinding": "JSONRPC", "protocolVersion": "1.0"},
		},
		"skills": []map[string]any{
			{"id": "echo", "name": "Echo", "description": "echoes", "tags": []string{"test"}},
		},
	}
}

// newJSONRPCErrorServer serves the well-known card (advertising JSON-RPC) plus a
// data-plane endpoint that answers every JSON-RPC request with the configured
// error object at HTTP 200 (the JSON-RPC transport requires 200 and reads the
// error from the body). A rpcCode of -32001 resolves to a2a.ErrTaskNotFound on
// the client, exactly as a real JSON-RPC A2A server would report it.
func newJSONRPCErrorServer(t *testing.T, rpcCode int, message string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == wellKnownCardPath {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(jsonrpcCardJSON("http://" + r.Host))
			return
		}
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"jsonrpc": "2.0",
			"id":      req["id"],
			"error":   map[string]any{"code": rpcCode, "message": message},
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// A. CO-2 binding-independence: a -32001 JSON-RPC error resolves to the SAME
// normalized NOT_FOUND envelope (code + a2aCode TASK_NOT_FOUND + exit 1) as the
// REST/HTTP+JSON 404 path, in both text and json. This regression-locks the
// binding-independent a2a.ErrTaskNotFound seam so the JSON-RPC path is no longer
// live/prose-only (design §9.4).
func TestGet_Missing_NotFound_JSONRPC(t *testing.T) {
	t.Run("json", func(t *testing.T) {
		cleanConfigDir(t)
		srv := newJSONRPCErrorServer(t, -32001, "task not found")

		out, errOut, code := runCLI(t, "get", "ghost", "-u", srv.URL, "--transport", "jsonrpc", "-o", "json")
		if code != 1 {
			t.Fatalf("exit = %d, want 1 (GENERIC default for NOT_FOUND)\nstderr: %s", code, errOut)
		}
		var env map[string]any
		if err := json.Unmarshal([]byte(out), &env); err != nil {
			t.Fatalf("stdout is not a single valid JSON error object: %v\n%s", err, out)
		}
		if env["code"] != "NOT_FOUND" {
			t.Errorf("envelope code = %v, want NOT_FOUND (JSON-RPC binding)", env["code"])
		}
		if env["a2aCode"] != "TASK_NOT_FOUND" {
			t.Errorf("envelope a2aCode = %v, want TASK_NOT_FOUND (JSON-RPC binding)", env["a2aCode"])
		}
	})

	t.Run("text", func(t *testing.T) {
		cleanConfigDir(t)
		srv := newJSONRPCErrorServer(t, -32001, "task not found")

		out, errOut, code := runCLI(t, "get", "ghost", "-u", srv.URL, "--transport", "jsonrpc")
		if code != 1 {
			t.Fatalf("exit = %d, want 1\nstderr: %s", code, errOut)
		}
		if !strings.Contains(errOut, "NOT_FOUND") {
			t.Errorf("text-mode diagnostic should carry NOT_FOUND on stderr, got %q", errOut)
		}
		if out != "" {
			t.Errorf("stdout should be empty in text-mode error, got %q", out)
		}
	})
}

func TestCancel_Missing_NotFound_JSONRPC(t *testing.T) {
	cleanConfigDir(t)
	srv := newJSONRPCErrorServer(t, -32001, "task not found")

	out, errOut, code := runCLI(t, "cancel", "ghost", "-u", srv.URL, "--transport", "jsonrpc", "-o", "json")
	if code != 1 {
		t.Fatalf("exit = %d, want 1\nstderr: %s", code, errOut)
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("invalid json error object: %v\n%s", err, out)
	}
	if env["code"] != "NOT_FOUND" || env["a2aCode"] != "TASK_NOT_FOUND" {
		t.Errorf("cancel over JSON-RPC should normalize to NOT_FOUND/TASK_NOT_FOUND, got %v", env)
	}
}
