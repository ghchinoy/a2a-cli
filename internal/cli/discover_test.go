// Copyright 2026 The A2A Authors
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

// wellKnownCardPath is the standard agent-card location. Declared locally so this
// test (in internal/cli) keeps the import boundary (design §3.2): it must not
// import a2a SDK types, so the card is served as raw JSON.
const wellKnownCardPath = "/.well-known/agent-card.json"

// cardServer serves a raw-JSON agent card at the well-known path. The card body
// is provided as a Go map so tests never touch SDK types.
func cardServer(t *testing.T, card map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == wellKnownCardPath {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(card)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// fullCardJSON builds a raw-JSON card (HTTP+JSON) exercising every section.
func fullCardJSON(url string) map[string]any {
	return map[string]any{
		"name":        "REST Hello World Agent",
		"description": "Just a rest hello world agent",
		"version":     "1.0.0",
		"provider":    map[string]any{"organization": "Acme", "url": "http://acme.example"},
		"capabilities": map[string]any{
			"streaming":         true,
			"pushNotifications": false,
		},
		"defaultInputModes":  []string{"text"},
		"defaultOutputModes": []string{"text"},
		"supportedInterfaces": []map[string]any{
			{"url": url, "protocolBinding": "HTTP+JSON", "protocolVersion": "1.0"},
		},
		"skills": []map[string]any{
			{"id": "hello_world", "name": "REST Hello world!", "description": "Returns a hello", "tags": []string{"hello world"}, "examples": []string{"hi"}},
		},
	}
}

func TestDiscover_Text_PresentsAllSections(t *testing.T) {
	cleanConfigDir(t)
	srv := cardServer(t, fullCardJSON("http://example/rest"))

	out, _, code := runCLI(t, "discover", "-u", srv.URL)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	for _, want := range []string{
		"Name:        REST Hello World Agent",
		"Capabilities:",
		"streaming:         true",
		"Interfaces:",
		"HTTP+JSON http://example/rest",
		"Security schemes:",
		"Skills:",
		"hello_world",
		"Selected transport: http-json",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("discover text output missing %q\n---\n%s", want, out)
		}
	}
}

func TestDiscover_JSON_OnlyValidJSONOnStdout(t *testing.T) {
	cleanConfigDir(t)
	srv := cardServer(t, fullCardJSON("http://example/rest"))

	out, errOut, code := runCLI(t, "discover", "-u", srv.URL, "-o", "json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(out), &decoded); err != nil {
		t.Fatalf("stdout is not a single valid JSON document: %v\n%s", err, out)
	}
	if decoded["name"] != "REST Hello World Agent" {
		t.Errorf("json missing identity: %v", decoded)
	}
	if _, ok := decoded["selection"]; !ok {
		t.Errorf("json missing selection section: %v", decoded)
	}
	// Diagnostics (none expected here) must never reach stdout; nothing to assert on
	// stderr for a clean success, but it must not contain JSON.
	if strings.Contains(errOut, "{") {
		t.Errorf("stderr should not carry JSON, got %q", errOut)
	}
}

func TestDiscover_Validate_ValidCard(t *testing.T) {
	cleanConfigDir(t)
	srv := cardServer(t, fullCardJSON("http://example/rest"))

	out, errOut, code := runCLI(t, "discover", "-u", srv.URL, "--validate")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 for a valid card", code)
	}
	if !strings.Contains(errOut, "valid") {
		t.Errorf("expected a validity note on stderr, got %q", errOut)
	}
	// CO-6 / D4: the success message must scope what was checked to STRUCTURAL /
	// required-field conformance and must NOT overstate it as a full JSON-Schema or
	// security check (that would mislead operators into trusting an unvetted card).
	if !strings.Contains(errOut, "structural") {
		t.Errorf("validity note should describe a STRUCTURAL check, got %q", errOut)
	}
	if !strings.Contains(strings.ToLower(errOut), "not a full json-schema") {
		t.Errorf("validity note must not overstate the check (should disclaim full JSON-Schema), got %q", errOut)
	}
	if !strings.Contains(out, "Name:") {
		t.Errorf("valid card should still present normally, got %q", out)
	}
}

func TestDiscover_Validate_MalformedCard(t *testing.T) {
	cleanConfigDir(t)
	// Parses as JSON but is schema-invalid: no name/description, empty interfaces,
	// a skill missing its id. New() still succeeds (defaults HTTP+JSON at -u), so
	// --validate is what catches the problem.
	bad := map[string]any{
		"skills": []map[string]any{{"name": "no id"}},
	}
	srv := cardServer(t, bad)

	out, errOut, code := runCLI(t, "discover", "-u", srv.URL, "--validate")
	if code == 0 {
		t.Fatalf("exit = 0, want non-zero for a malformed card")
	}
	if !strings.Contains(strings.ToLower(errOut), "valid") && !strings.Contains(strings.ToLower(errOut), "missing") {
		t.Errorf("expected an actionable validation diagnostic on stderr, got %q", errOut)
	}
	if out != "" {
		t.Errorf("stdout should be empty in text-mode validation failure, got %q", out)
	}
}

func TestDiscover_Unreachable_Exit3(t *testing.T) {
	cleanConfigDir(t)
	// Point at a closed port: card fetch fails → unreachable (exit 3), consistent
	// with Phase 1 classify (design §3.5).
	_, _, code := runCLI(t, "discover", "-u", "http://127.0.0.1:1", "--timeout", "2s")
	if code != 3 {
		t.Errorf("exit = %d, want 3 (unreachable)", code)
	}
}

func TestDiscover_MissingURL_Usage(t *testing.T) {
	cleanConfigDir(t)
	_, errOut, code := runCLI(t, "discover")
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (usage)", code)
	}
	if !strings.Contains(errOut, "USAGE") {
		t.Errorf("expected a USAGE diagnostic on stderr, got %q", errOut)
	}
}

func TestDiscover_PositionalArg_Usage(t *testing.T) {
	cleanConfigDir(t)
	_, _, code := runCLI(t, "discover", "-u", "http://x", "extra-arg")
	if code != 2 {
		t.Errorf("exit = %d, want 2 (usage) for a positional arg", code)
	}
}
