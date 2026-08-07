// Copyright 2026 The A2A Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package cli

import (
	"context"
	"os"
	"os/signal"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ghchinoy/a2a-cli/internal/client"
	"github.com/ghchinoy/a2a-cli/internal/clierr"
	"github.com/ghchinoy/a2a-cli/internal/config"
	"github.com/ghchinoy/a2a-cli/internal/render"
	"github.com/ghchinoy/a2a-cli/internal/session"
)

func newCancelCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "cancel <taskId>",
		Short: "Cancel an active task by identifier",
		Long: "Request cancellation of a task and report the resulting state (spec §8.4). " +
			"The operation is idempotent: cancelling an already-terminal task is a clean " +
			"no-op that reports the task's current state rather than erroring. Use -o json " +
			"for machine-readable output.",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return clierr.New(clierr.KindUsage, "cancel requires exactly one <taskId> argument")
			}
			return nil
		},
		RunE: runCancel,
	}
}

func runCancel(cmd *cobra.Command, args []string) error {
	taskID := args[0]
	flags := cmd.Flags()

	// Session supplies service URL/transport as the lowest-but-one precedence layer,
	// matching the other task commands (design §3.7).
	sess, loadErr := session.Load()
	sessionDefaults := map[string]string{}
	if sess != nil {
		if sess.ServiceURL != "" {
			sessionDefaults[flagServiceURL] = sess.ServiceURL
		}
		if sess.Transport != "" {
			sessionDefaults[flagTransport] = sess.Transport
		}
	}
	defaults := map[string]string{
		flagOutput:     "text",
		flagA2AVersion: client.DefaultA2AVersion,
	}
	cfg := config.New(flags, sessionDefaults, defaults)

	mode := render.ModeText
	noTUI, _ := flags.GetBool(flagNoTUI)
	switch {
	case noTUI:
		mode = render.ModeJSON
	case strings.EqualFold(cfg.String(flagOutput), "json"):
		mode = render.ModeJSON
	}
	r := render.New(mode, os.Stdout, os.Stderr)
	if loadErr != nil {
		r.Warn("WARNING: could not load session: %v", loadErr)
	}

	serviceURL := cfg.String(flagServiceURL)
	if serviceURL == "" {
		return usageError(r, "a service URL is required (-u/--service-url)")
	}

	headers, err := parseHeaders(mustStringArray(flags, flagHeader))
	if err != nil {
		return usageError(r, err.Error())
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	cl, err := client.New(ctx, client.Options{
		ServiceURL: serviceURL,
		CardURL:    cfg.String(flagCardURL),
		Transport:  cfg.String(flagTransport),
		A2AVersion: cfg.String(flagA2AVersion),
		Insecure:   mustBool(flags, flagInsecure),
		Timeout:    mustDuration(flags, flagTimeout),
		Creds: &client.CallerSuppliedProvider{
			Bearer: cfg.String(flagBearer),
			APIKey: cfg.String(flagAPIKey),
			Extra:  headers,
		},
		Warnf: r.Warn,
	})
	if err != nil {
		return renderAndReturn(r, err)
	}

	// CancelTask is idempotent: an already-terminal task's not-cancelable response is
	// absorbed inside the wrapper, which reports the resulting state instead (§8.4).
	tr, err := cl.CancelTask(ctx, taskID)
	if err != nil {
		return renderAndReturn(r, err)
	}

	if err := r.RenderTask(tr); err != nil {
		return err
	}

	// Cancel exit-code decision (flagged for reviewer): a cancel that succeeds —
	// whether the task moves to CANCELED now or was already terminal — is a SUCCESS
	// for the command and exits 0. We deliberately do NOT run clierr.FromState here,
	// which would map CANCELED to exit 5 (TASK_FAILED). Reaching CANCELED is the
	// intended outcome of `cancel`, not a failure. NOTE: the CANCELED→exit mapping is
	// a known open item (Phase-1 dev flagged CANCELED→5 for the general case); Phase 6
	// finalizes the cross-binding numeric mapping. Only transport/normalized-error
	// failures (unreachable=3, not-found=1, auth=4, generic=1) yield a non-zero exit.
	return nil
}
