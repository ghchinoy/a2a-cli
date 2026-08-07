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
	"github.com/ghchinoy/a2a-cli/internal/envelope"
	"github.com/ghchinoy/a2a-cli/internal/poll"
	"github.com/ghchinoy/a2a-cli/internal/render"
	"github.com/ghchinoy/a2a-cli/internal/session"
)

// get-local flag names (spec §8.3).
const (
	flagIncludeArtifacts = "include-artifacts"
	flagHistory          = "history"
	flagWait             = "wait"
	flagWatch            = "watch"
)

// maxHistoryLength is the client-side upper bound on --history <n> (R-3 / CO-8):
// an absurd value is clamped to this sane Tier-1 ceiling (with a stderr warning)
// rather than forwarded verbatim. Negative is already a usage error above.
const maxHistoryLength = 1000

func newGetCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "get <taskId>",
		Short: "Retrieve a task by identifier",
		Long: "Retrieve a task's state (and, with --include-artifacts, its artifact " +
			"contents; with --history <n>, its recent message history). One-shot by " +
			"default; --wait/--watch polls until a terminal or interrupted state " +
			"(§7.3). Use -o json for machine-readable output.",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return clierr.New(clierr.KindUsage, "get requires exactly one <taskId> argument")
			}
			return nil
		},
		RunE: runGet,
	}
	cmd.Flags().Bool(flagIncludeArtifacts, false, "fetch and render artifact contents (default: summarize artifacts)")
	cmd.Flags().Int(flagHistory, 0, "include up to <n> most recent history messages")
	cmd.Flags().Bool(flagWait, false, "poll until the task reaches a terminal or interrupted state")
	cmd.Flags().Bool(flagWatch, false, "like --wait, and print each state transition to stderr")
	return cmd
}

func runGet(cmd *cobra.Command, args []string) error {
	taskID := args[0]
	flags := cmd.Flags()

	// Session supplies the lowest-but-one precedence layer for service URL and
	// transport, matching send/discover (design §3.7). A missing session is not an
	// error; any other Load failure becomes a stderr warning once the renderer exists.
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

	// --history <n> threads HistoryLength only when the flag was set (a bare `get`
	// leaves it unset so the server applies its own default). A negative count is
	// nonsensical input, so it is a USAGE error (exit 2) rather than a silent drop —
	// consistent with the central -o value check (CO-1) and with pflag rejecting an
	// out-of-range integer at parse time. n == 0 stays valid (forwarded verbatim);
	// a value above maxHistoryLength is clamped client-side with a warning (CO-8).
	opts := client.GetOpts{IncludeArtifacts: mustBool(flags, flagIncludeArtifacts)}
	if flags.Changed(flagHistory) {
		n, herr := flags.GetInt(flagHistory)
		if herr != nil {
			return usageError(r, "invalid --history value")
		}
		if n < 0 {
			return usageError(r, "--history must be zero or a positive number")
		}
		// Upper clamp (CO-8 / R-3): bound an absurd value client-side. The ints in
		// the warning are CLI-authored args, not server-derived (CO-5 unaffected).
		if n > maxHistoryLength {
			r.Warn("--history %d exceeds the client maximum of %d; clamping to %d", n, maxHistoryLength, maxHistoryLength)
			n = maxHistoryLength
		}
		opts.HistoryLength = &n
	}

	// SIGINT cancels the context but never loses the taskId (it is the argument, §7.3).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	cl, err := client.New(ctx, client.Options{
		ServiceURL:            serviceURL,
		CardURL:               cfg.String(flagCardURL),
		Transport:             cfg.String(flagTransport),
		A2AVersion:            cfg.String(flagA2AVersion),
		Insecure:              mustBool(flags, flagInsecure),
		Timeout:               mustDuration(flags, flagTimeout),
		Creds:                 resolveCredentials(flags, headers),
		AllowCrossOriginCreds: mustBool(flags, flagAllowXOrigin),
		Warnf:                 r.Warn,
	})
	if err != nil {
		return renderAndReturn(r, err)
	}

	tr, err := cl.GetTask(ctx, taskID, opts)
	if err != nil {
		return renderAndReturn(r, err)
	}

	// --wait/--watch turn `get` into the shared poll loop (design §3.6, spec §7.3):
	// seed with the one-shot result and poll to a terminal OR interrupted state.
	// --watch additionally reports each state transition on stderr (never on json
	// stdout). Both reuse poll.Wait exactly as send does — no second poll loop.
	watch := mustBool(flags, flagWatch)
	if mustBool(flags, flagWait) || watch {
		lastState := tr.State
		get := func(c context.Context) (*envelope.TaskResult, error) {
			next, gerr := cl.GetTask(c, taskID, opts)
			if gerr != nil {
				return nil, gerr
			}
			if watch && next != nil && next.State != lastState {
				r.Warn("state: %s -> %s", lastState, next.State)
				lastState = next.State
			}
			return next, nil
		}
		final, perr := poll.Wait(ctx, tr, get, poll.Options{
			Interval: mustDuration(flags, flagPollInterval),
			Timeout:  mustDuration(flags, flagTimeout),
		})
		if final != nil {
			tr = final
		}
		if perr != nil {
			// The taskId is the argument, so it is never lost on timeout; still surface
			// a resume hint on stderr (§7.3).
			r.ResumeHint(tr.TaskID)
			return renderAndReturn(r, perr)
		}
	}

	if err := r.RenderTask(tr); err != nil {
		return err
	}

	// Map the reported state to an exit code (design §3.5): a terminal FAILED/
	// REJECTED task exits 5, INPUT_REQUIRED/AUTH_REQUIRED exit 6/4 with a resume
	// hint, COMPLETED and any non-final state exit 0. This keeps `get`'s exit codes
	// consistent with `send` and the §9.5 table.
	if stateErr := clierr.FromState(tr.State); stateErr != nil {
		if envelope.IsInterrupted(tr.State) {
			r.ResumeHint(tr.TaskID)
		}
		stateErr.MarkRendered()
		return stateErr
	}
	return nil
}
