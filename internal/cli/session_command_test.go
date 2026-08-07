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
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ghchinoy/a2a-cli/internal/session"
)

// §6.4: `session show` with no stored session is not an error — it reports the
// store path and "no session" cleanly and exits 0, in both text and json.
func TestSession_Show_NoSession(t *testing.T) {
	t.Run("text", func(t *testing.T) {
		cleanConfigDir(t)
		out, errOut, code := runCLI(t, "session", "show")
		if code != 0 {
			t.Fatalf("exit = %d, want 0\nstderr: %s", code, errOut)
		}
		if !strings.Contains(out, "Path:") {
			t.Errorf("show should print the store path, got:\n%s", out)
		}
		if !strings.Contains(strings.ToLower(out), "no session") {
			t.Errorf("show with no session should say so, got:\n%s", out)
		}
	})

	t.Run("json", func(t *testing.T) {
		cleanConfigDir(t)
		out, _, code := runCLI(t, "session", "show", "-o", "json")
		if code != 0 {
			t.Fatalf("exit = %d, want 0", code)
		}
		var sv map[string]any
		if err := json.Unmarshal([]byte(out), &sv); err != nil {
			t.Fatalf("stdout is not a single valid JSON object: %v\n%s", err, out)
		}
		if sv["exists"] != false {
			t.Errorf("exists = %v, want false", sv["exists"])
		}
		if sv["path"] == nil || sv["path"] == "" {
			t.Errorf("path must always be surfaced, got %v", sv["path"])
		}
	})
}

// §6.4: `session show` (and the bare `session` default) surfaces the stored
// contents — contextId, latest taskId, serviceURL, transport — and the XDG path.
func TestSession_Show_WithSession(t *testing.T) {
	t.Run("text_default_subcommand", func(t *testing.T) {
		dir := cleanConfigDir(t)
		if err := session.Save(&session.Session{ContextID: "ctx-1", LatestTaskID: "task-1", ServiceURL: "http://agent.example", Transport: "jsonrpc"}); err != nil {
			t.Fatalf("seed: %v", err)
		}
		// Bare `session` defaults to show.
		out, errOut, code := runCLI(t, "session")
		if code != 0 {
			t.Fatalf("exit = %d, want 0\nstderr: %s", code, errOut)
		}
		for _, want := range []string{"ctx-1", "task-1", "http://agent.example", "jsonrpc"} {
			if !strings.Contains(out, want) {
				t.Errorf("show output missing %q\n---\n%s", want, out)
			}
		}
		wantPath := filepath.Join(dir, "a2a-cli", "session.json")
		if !strings.Contains(out, wantPath) {
			t.Errorf("show should surface the XDG store path %q, got:\n%s", wantPath, out)
		}
	})

	t.Run("json", func(t *testing.T) {
		cleanConfigDir(t)
		if err := session.Save(&session.Session{ContextID: "ctx-1", LatestTaskID: "task-1", ServiceURL: "http://agent.example", Transport: "jsonrpc"}); err != nil {
			t.Fatalf("seed: %v", err)
		}
		out, _, code := runCLI(t, "session", "show", "-o", "json")
		if code != 0 {
			t.Fatalf("exit = %d, want 0", code)
		}
		var sv map[string]any
		if err := json.Unmarshal([]byte(out), &sv); err != nil {
			t.Fatalf("invalid json: %v\n%s", err, out)
		}
		if sv["exists"] != true || sv["contextId"] != "ctx-1" || sv["latestTaskId"] != "task-1" {
			t.Errorf("json view missing stored identifiers: %v", sv)
		}
	})
}

// §6.4 hardening: `session show` defensively strips any URL userinfo even if a
// pre-existing session.json was written out-of-band with credentials in the URL.
func TestSession_Show_SanitizesStoredURL(t *testing.T) {
	dir := cleanConfigDir(t)
	cfgDir := filepath.Join(dir, "a2a-cli")
	if err := os.MkdirAll(cfgDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// Write a raw session file with URL-embedded credentials, bypassing Save's
	// sanitize, to prove show re-sanitizes on read.
	raw := `{"schemaVersion":1,"serviceUrl":"https://user:secret@agent.example/a2a"}`
	if err := os.WriteFile(filepath.Join(cfgDir, "session.json"), []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}

	out, _, code := runCLI(t, "session", "show", "-o", "json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if strings.Contains(out, "secret") || strings.Contains(out, "user:") {
		t.Errorf("session show must not surface URL userinfo, got:\n%s", out)
	}
	if !strings.Contains(out, "agent.example") {
		t.Errorf("session show should still surface the sanitized host, got:\n%s", out)
	}
}

// §6.4: `session clear` deletes the stored file and is idempotent — clearing when
// none exists is a clean no-op (exit 0), with a confirmation on stderr.
func TestSession_Clear_RemovesFileAndIsIdempotent(t *testing.T) {
	dir := cleanConfigDir(t)
	if err := session.Save(&session.Session{ContextID: "ctx-1"}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	path := filepath.Join(dir, "a2a-cli", "session.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("precondition: session file should exist: %v", err)
	}

	_, errOut, code := runCLI(t, "session", "clear")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, errOut)
	}
	if !strings.Contains(errOut, "cleared") {
		t.Errorf("clear should confirm on stderr, got %q", errOut)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("session file should be gone after clear, stat err = %v", err)
	}

	// Idempotent: clearing again is not an error.
	_, _, code = runCLI(t, "session", "clear")
	if code != 0 {
		t.Fatalf("second clear exit = %d, want 0 (idempotent)", code)
	}
}
