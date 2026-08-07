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

// flagValidate is the discover-local flag that turns on card-schema validation.
const flagValidate = "validate"

func newDiscoverCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "discover",
		Short: "Fetch and present an agent's card",
		Long: "Resolve an A2A agent's card (from the well-known path or --card-url), " +
			"present every section (identity, capabilities, interfaces, security schemes, " +
			"skills), and show which transport the client would select. Use --validate to " +
			"check the card against the A2A card schema, and -o json for machine-readable output.",
		Args: func(_ *cobra.Command, args []string) error {
			if len(args) != 0 {
				return clierr.New(clierr.KindUsage, "discover takes no positional arguments")
			}
			return nil
		},
		RunE: runDiscover,
	}
	// F-5 (accepted for Tier 1): --validate is a STRUCTURAL conformance aid
	// (required fields / shape), NOT a full JSON-Schema validation and NOT a
	// security check — it does not vet URLs, credentials, or trust.
	cmd.Flags().Bool(flagValidate, false, "check the card's required-field structure (a conformance aid, not a security check; non-zero exit if invalid)")
	return cmd
}

func runDiscover(cmd *cobra.Command, _ []string) error {
	flags := cmd.Flags()

	// Session supplies the lowest-but-one precedence layer for the service URL and
	// transport, matching the send path (design §3.7). A missing session is not an
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
	cardURL := cfg.String(flagCardURL)
	if serviceURL == "" && cardURL == "" {
		return usageError(r, "a service URL (-u/--service-url) or --card-url is required")
	}

	headers, err := parseHeaders(mustStringArray(flags, flagHeader))
	if err != nil {
		return usageError(r, err.Error())
	}

	// SIGINT cancels the context; card fetch and transport selection are the only
	// network work discover performs.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	cl, err := client.New(ctx, client.Options{
		ServiceURL: serviceURL,
		CardURL:    cardURL,
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
		// Card-fetch/connection failures classify as unreachable (exit 3), matching
		// Phase 1 (design §3.5).
		return renderAndReturn(r, err)
	}

	// --validate: a malformed/invalid card is a non-zero exit with actionable
	// detail; a valid card still presents normally below (design §8.1).
	if mustBool(flags, flagValidate) {
		if verr := cl.ValidateCard(); verr != nil {
			return renderAndReturn(r, verr)
		}
		r.Warn("card is valid against the A2A card schema")
	}

	return r.RenderCard(cl.FullCard())
}
