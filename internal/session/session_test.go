// Copyright 2026 The a2a-cli Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSaveLoad_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	in := &Session{ContextID: "ctx-1", LatestTaskID: "task-1", ServiceURL: "http://x", Transport: "jsonrpc"}
	if err := Save(in); err != nil {
		t.Fatal(err)
	}

	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("expected a session, got nil")
	}
	if got.ContextID != "ctx-1" || got.LatestTaskID != "task-1" || got.ServiceURL != "http://x" || got.Transport != "jsonrpc" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
	if got.SchemaVersion != SchemaVersion {
		t.Errorf("schema version = %d, want %d", got.SchemaVersion, SchemaVersion)
	}
	if got.UpdatedAt.IsZero() {
		t.Error("updatedAt should be set")
	}
}

func TestSave_FileMode0600(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := Save(&Session{ContextID: "c"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "a2a-cli", "session.json"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("session file mode = %o, want 600", perm)
	}
}

func TestLoad_NoSessionReturnsNil(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	got, err := Load()
	if err != nil {
		t.Fatalf("expected nil error for missing session, got %v", err)
	}
	if got != nil {
		t.Errorf("expected nil session, got %+v", got)
	}
}

func TestSave_StripsURLUserinfo(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	if err := Save(&Session{ServiceURL: "https://user:token@agent.example.com/a2a"}); err != nil {
		t.Fatal(err)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got.ServiceURL, "token") || strings.Contains(got.ServiceURL, "user:") {
		t.Errorf("URL userinfo must not be persisted, got %q", got.ServiceURL)
	}
	if got.ServiceURL != "https://agent.example.com/a2a" {
		t.Errorf("serviceUrl = %q, want stripped host URL", got.ServiceURL)
	}
}

func TestSave_AtomicOverwriteKeeps0600(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := Save(&Session{ContextID: "first"}); err != nil {
		t.Fatal(err)
	}
	// Overwrite; the rename-based write must still leave a 0600 file.
	if err := Save(&Session{ContextID: "second"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "a2a-cli", "session.json"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("session file mode after overwrite = %o, want 600", perm)
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.ContextID != "second" {
		t.Errorf("expected overwrite to persist latest, got %q", got.ContextID)
	}
}

func TestDelete_RemovesFileAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := Save(&Session{ContextID: "c"}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "a2a-cli", "session.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("precondition: session should exist: %v", err)
	}
	if err := Delete(); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("session file should be gone, stat err = %v", err)
	}
	// Idempotent: deleting a non-existent session is not an error.
	if err := Delete(); err != nil {
		t.Errorf("Delete on missing session should be a no-op, got %v", err)
	}
}

func TestSanitizeURL_StripsUserinfo(t *testing.T) {
	if got := SanitizeURL("https://user:token@agent.example/a2a"); got != "https://agent.example/a2a" {
		t.Errorf("SanitizeURL = %q, want stripped host URL", got)
	}
	if got := SanitizeURL(""); got != "" {
		t.Errorf("SanitizeURL(\"\") = %q, want empty", got)
	}
}

func TestDir_UsesXDG(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/tmp/xdgtest")
	dir, err := Dir()
	if err != nil {
		t.Fatal(err)
	}
	if dir != filepath.Join("/tmp/xdgtest", "a2a-cli") {
		t.Errorf("Dir() = %q", dir)
	}
}
