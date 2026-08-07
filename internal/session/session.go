// Copyright 2026 The a2a-cli Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

// Package session persists the most recent conversation so that no state lives
// only in process memory (spec §6.4 / design §3.7). Phase 1 captures identifiers
// only. The file carries a schema version for forward-compatibility.
package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/ghchinoy/a2a-cli/internal/urlutil"
)

// SchemaVersion is the on-disk schema version for session.json.
const SchemaVersion = 1

// fileName is the session file name within the config dir.
const fileName = "session.json"

// Session is the most recent conversation record.
type Session struct {
	SchemaVersion int       `json:"schemaVersion"`
	ContextID     string    `json:"contextId,omitempty"`
	LatestTaskID  string    `json:"latestTaskId,omitempty"`
	ServiceURL    string    `json:"serviceUrl,omitempty"`
	Transport     string    `json:"transport,omitempty"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

// Dir returns the config directory: $XDG_CONFIG_HOME/a2a-cli, falling back to
// ~/.config/a2a-cli (or the OS equivalent from os.UserConfigDir elsewhere).
func Dir() (string, error) {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "a2a-cli"), nil
	}
	if runtime.GOOS != "windows" && runtime.GOOS != "darwin" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, ".config", "a2a-cli"), nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "a2a-cli"), nil
}

// Path returns the full path to session.json.
func Path() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, fileName), nil
}

// Save writes the session to disk, creating the config dir (0700) and writing the
// file with 0600 permissions (spec §6.4: secret-capable files are owner-only).
//
// The write is atomic (temp file in the same dir + rename) so a crash or a
// concurrent run cannot leave a truncated session.json, and 0600 is enforced
// explicitly even when the file/dir already exists with looser permissions
// (audit L-3). Any credentials embedded in the service URL are stripped before
// persistence (audit L-1) — Tier 1 persists no caller-supplied secrets.
func Save(s *Session) error {
	s.SchemaVersion = SchemaVersion
	s.UpdatedAt = time.Now().UTC()
	s.ServiceURL = urlutil.Sanitize(s.ServiceURL)

	dir, err := Dir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}

	final := filepath.Join(dir, fileName)
	tmp, err := os.CreateTemp(dir, ".session-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, final); err != nil {
		return err
	}
	// Enforce 0600 in case the destination pre-existed with looser perms.
	return os.Chmod(final, 0o600)
}

// Load reads the session from disk. It returns (nil, nil) when no session exists.
func Load() (*Session, error) {
	path, err := Path()
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var s Session
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}
