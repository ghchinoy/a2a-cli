// Copyright 2026 The A2A Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package cli

import (
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/ghchinoy/a2a-cli/internal/envelope"
	"github.com/ghchinoy/a2a-cli/internal/render"
	"github.com/ghchinoy/a2a-cli/internal/session"
)

// newSessionCommand builds the `session` command tree (spec §6.4: persisted state
// MUST be inspectable and clearable by the user). `session` / `session show`
// inspects the store; `session clear` deletes it. The command uses only
// internal/session for store access and internal/render for output, so it stays
// within the import boundary (design §3.2) and never touches the network.
func newSessionCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "session",
		Short: "Inspect or clear the local session store",
		Long: "Inspect or clear the locally persisted conversation state (spec §6.4). " +
			"With no subcommand this prints the store path and its current contents, " +
			"the same as `session show`. `session clear` deletes the stored session.",
		Args: cobra.NoArgs,
		RunE: runSessionShow,
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "show",
		Short: "Print the session store path and its current contents",
		Args:  cobra.NoArgs,
		RunE:  runSessionShow,
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "clear",
		Short: "Delete the stored session (idempotent)",
		Args:  cobra.NoArgs,
		RunE:  runSessionClear,
	})
	return cmd
}

// runSessionShow prints the store path and, when present, its contents. A missing
// session is NOT an error for an inspect command (exit 0) — it reports "no session"
// cleanly. The stored service URL is defensively re-sanitized (userinfo stripped)
// even though Save already strips it on write.
func runSessionShow(cmd *cobra.Command, _ []string) error {
	r := render.New(modeFromFlags(cmd.Flags()), os.Stdout, os.Stderr)

	path, err := session.Path()
	if err != nil {
		return renderAndReturn(r, err)
	}
	sess, err := session.Load()
	if err != nil {
		return renderAndReturn(r, err)
	}

	sv := &envelope.SessionView{Path: path, Exists: sess != nil}
	if sess != nil {
		sv.ContextID = sess.ContextID
		sv.LatestTaskID = sess.LatestTaskID
		sv.ServiceURL = session.SanitizeURL(sess.ServiceURL)
		sv.Transport = sess.Transport
		if !sess.UpdatedAt.IsZero() {
			sv.UpdatedAt = sess.UpdatedAt.Format(time.RFC3339)
		}
	}
	return r.RenderSession(sv)
}

// runSessionClear deletes the stored session. Deletion is idempotent (clearing
// when none exists is not an error), so this is always safe to run. The
// confirmation goes to stderr (a diagnostic, never json stdout).
func runSessionClear(cmd *cobra.Command, _ []string) error {
	r := render.New(modeFromFlags(cmd.Flags()), os.Stdout, os.Stderr)

	path, err := session.Path()
	if err != nil {
		return renderAndReturn(r, err)
	}
	if err := session.Delete(); err != nil {
		return renderAndReturn(r, err)
	}
	r.Warn("session cleared: %s", path)
	return nil
}
