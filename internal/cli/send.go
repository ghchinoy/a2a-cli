// Copyright 2026 The a2a-cli Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package cli

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/ghchinoy/a2a-cli/internal/client"
	"github.com/ghchinoy/a2a-cli/internal/clierr"
	"github.com/ghchinoy/a2a-cli/internal/config"
	"github.com/ghchinoy/a2a-cli/internal/envelope"
	"github.com/ghchinoy/a2a-cli/internal/poll"
	"github.com/ghchinoy/a2a-cli/internal/render"
	"github.com/ghchinoy/a2a-cli/internal/session"
)

func newSendCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "send <text>",
		Short: "Send a message to an agent and wait for the result",
		Long: "Send a message to an A2A agent over HTTP+JSON (blocking by default) and " +
			"print the normalized result. Use -o json for machine-readable output.",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 1 {
				return clierr.New(clierr.KindUsage, "send requires exactly one <text> argument")
			}
			return nil
		},
		RunE: runSend,
	}
}

func runSend(cmd *cobra.Command, args []string) error {
	text := args[0]
	flags := cmd.Flags()

	// Load session for the lowest-but-one precedence layer (design §3.7).
	sess, _ := session.Load()
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

	// Resolve output mode: -n forces json; otherwise -o value.
	mode := render.ModeText
	noTUI, _ := flags.GetBool(flagNoTUI)
	switch {
	case noTUI:
		mode = render.ModeJSON
	case strings.EqualFold(cfg.String(flagOutput), "json"):
		mode = render.ModeJSON
	}
	r := render.New(mode, os.Stdout, os.Stderr)

	serviceURL := cfg.String(flagServiceURL)
	if serviceURL == "" {
		return usageError(r, "a service URL is required (-u/--service-url)")
	}

	headers, err := parseHeaders(mustStringArray(flags, flagHeader))
	if err != nil {
		return usageError(r, err.Error())
	}

	// SIGINT cancels the context but never loses an already-known taskId (§7.3).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	cl, err := client.New(ctx, client.Options{
		ServiceURL: serviceURL,
		CardURL:    cfg.String(flagCardURL),
		Transport:  cfg.String(flagTransport),
		A2AVersion: cfg.String(flagA2AVersion),
		Insecure:   mustBool(flags, flagInsecure),
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

	tr, err := cl.Send(ctx, client.SendRequest{
		Text:      text,
		ContextID: cfg.String(flagContextID),
		TaskID:    cfg.String(flagTaskID),
	})
	if err != nil {
		return renderAndReturn(r, err)
	}

	// Surface the taskId to stderr the moment it exists so it survives any later
	// timeout/interruption (§7.3). Never fabricate ids (§6.1).
	if tr.TaskID != nil {
		r.Warn("task %s created (context %s)", *tr.TaskID, ptrOr(tr.ContextID, "-"))
	}
	captureSession(sess, tr, serviceURL, cl.Transport())

	// Blocking wait unless the first result is already terminal/interrupted (the
	// Go hello-world server returns a terminal Message, so no polling occurs).
	if !envelope.IsTerminal(tr.State) && !envelope.IsInterrupted(tr.State) {
		final, perr := poll.Wait(ctx, tr, func(c context.Context) (*envelope.TaskResult, error) {
			return cl.GetTask(c, ptrOr(tr.TaskID, ""))
		}, poll.Options{
			Interval: mustDuration(flags, flagPollInterval),
			Timeout:  mustDuration(flags, flagTimeout),
		})
		if final != nil {
			tr = final
		}
		if perr != nil {
			// taskId already emitted to stderr above; surface a resume hint too.
			r.ResumeHint(tr.TaskID)
			return renderAndReturn(r, perr)
		}
	}

	if err := r.RenderTask(tr); err != nil {
		return err
	}

	// Map the final state to an exit code (design §3.5). Interrupted states also
	// get a resume hint on stderr.
	if stateErr := clierr.FromState(tr.State); stateErr != nil {
		if envelope.IsInterrupted(tr.State) {
			r.ResumeHint(tr.TaskID)
		}
		return stateErr
	}
	return nil
}

// captureSession persists the latest conversation identifiers (spec §6.4).
func captureSession(prev *session.Session, tr *envelope.TaskResult, serviceURL, transport string) {
	s := &session.Session{ServiceURL: serviceURL, Transport: transport}
	if prev != nil {
		s.ContextID = prev.ContextID
	}
	if tr.ContextID != nil {
		s.ContextID = *tr.ContextID
	}
	if tr.TaskID != nil {
		s.LatestTaskID = *tr.TaskID
	}
	_ = session.Save(s)
}

// renderAndReturn renders a clierr.Error (or generic error) as an Appendix B
// error object and returns it for exit-code mapping.
func renderAndReturn(r *render.Renderer, err error) error {
	var ce *clierr.Error
	if errors.As(err, &ce) {
		_ = r.RenderError(ce.ToEnvelope())
	} else {
		_ = r.RenderError(envelope.CLIError{Code: string(clierr.KindGeneric), Message: err.Error()})
	}
	return err
}

func usageError(r *render.Renderer, msg string) error {
	e := clierr.New(clierr.KindUsage, msg)
	_ = r.RenderError(e.ToEnvelope())
	return e
}

func parseHeaders(hs []string) (map[string]string, error) {
	if len(hs) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(hs))
	for _, h := range hs {
		name, val, ok := strings.Cut(h, ":")
		if !ok {
			return nil, errString("invalid header (want 'Name: Value'): " + h)
		}
		out[strings.TrimSpace(name)] = strings.TrimSpace(val)
	}
	return out, nil
}

func ptrOr(s *string, fallback string) string {
	if s == nil {
		return fallback
	}
	return *s
}

// --- small flag/error helpers -------------------------------------------------

type errString string

func (e errString) Error() string { return string(e) }

func mustBool(f *pflag.FlagSet, name string) bool {
	v, _ := f.GetBool(name)
	return v
}

func mustDuration(f *pflag.FlagSet, name string) time.Duration {
	v, _ := f.GetDuration(name)
	return v
}

func mustStringArray(f *pflag.FlagSet, name string) []string {
	v, _ := f.GetStringArray(name)
	return v
}
