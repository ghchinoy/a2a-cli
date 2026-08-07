// Copyright 2026 The a2a-cli Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

// Package cli defines the cobra command tree. Per the import boundary (design
// §3.2), commands operate only on envelope domain types and call internal/client
// and internal/poll — they never import a2apb/a2agrpc/raw SDK transport types.
package cli

import (
	"errors"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/ghchinoy/a2a-cli/internal/clierr"
	"github.com/ghchinoy/a2a-cli/internal/envelope"
	"github.com/ghchinoy/a2a-cli/internal/render"
)

// Global flag names (spec §5.2). Kept as constants so config keys and flag names
// stay in lockstep.
const (
	flagServiceURL   = "service-url"
	flagContextID    = "context-id"
	flagTaskID       = "task-id"
	flagOutput       = "output"
	flagNoTUI        = "no-tui"
	flagTransport    = "transport"
	flagPollInterval = "poll-interval"
	flagTimeout      = "timeout"
	flagVerbose      = "verbose"
	flagA2AVersion   = "a2a-version"
	flagCardURL      = "card-url"
	flagBearer       = "bearer"
	flagAPIKey       = "api-key"
	flagHeader       = "header"
	flagInsecure     = "insecure"
)

// NewRootCommand builds the root command with all global/persistent flags.
func NewRootCommand() *cobra.Command {
	root := &cobra.Command{
		Use:           "a2a-cli",
		Short:         "A conformant Tier-1 client for the A2A protocol",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	// Turn cobra flag-parsing errors into a usage-kind error (exit 2).
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error {
		return clierr.Wrap(clierr.KindUsage, err.Error(), err)
	})

	pf := root.PersistentFlags()
	pf.StringP(flagServiceURL, "u", "", "base URL of the A2A agent")
	pf.String(flagContextID, "", "context ID to continue a conversation")
	pf.String(flagTaskID, "", "task ID to send against an existing task")
	pf.StringP(flagOutput, "o", "text", "output format: text|json")
	pf.BoolP(flagNoTUI, "n", false, "shorthand for --output json")
	pf.String(flagTransport, "", "transport binding: http-json|jsonrpc|grpc (default: card-driven, HTTP+JSON)")
	pf.Duration(flagPollInterval, 2*time.Second, "polling interval while waiting for a task")
	pf.Duration(flagTimeout, 0, "maximum time to wait for a task (0 = no timeout)")
	pf.BoolP(flagVerbose, "v", false, "enable verbose diagnostics on stderr")
	pf.String(flagA2AVersion, "1.0", "A2A protocol version to signal")
	pf.String(flagCardURL, "", "explicit agent-card URL (overrides the well-known path)")

	// Auth flags: declared per spec §5.2 even though the Go hello-world server
	// requires no auth. Wired through the CredentialProvider seam (design §3.3).
	pf.String(flagBearer, "", "bearer token for Authorization header")
	pf.String(flagAPIKey, "", "API key (sent as X-API-Key)")
	pf.StringArrayP(flagHeader, "H", nil, "extra header in 'Name: Value' form (repeatable)")
	pf.Bool(flagInsecure, false, "skip TLS certificate verification (emits a warning)")

	root.AddCommand(newSendCommand())
	return root
}

// Execute builds and runs the root command, returning the error for exit mapping.
//
// Errors raised before a command builds its own render.Renderer — cobra-level
// usage errors such as an unknown flag or the wrong argument count — would
// otherwise exit non-zero while printing nothing to either stream (spec §9.1/§9.4).
// Any returned error that has not already been rendered is surfaced here through a
// default renderer (mode inferred from -o/-n, defaulting to text): json mode emits
// the Appendix B {code,message,a2aCode} object on stdout, text mode a stderr
// diagnostic. The exit-code mapping stays the single tail in main.
func Execute() error {
	root := NewRootCommand()
	err := root.Execute()
	if err != nil {
		renderTopLevelError(root.Flags(), err)
	}
	return err
}

// renderTopLevelError surfaces an error that no command renderer has handled yet.
func renderTopLevelError(flags *pflag.FlagSet, err error) {
	var ce *clierr.Error
	if errors.As(err, &ce) && ce.Rendered() {
		return // already surfaced by the command that produced it
	}
	r := render.New(modeFromFlags(flags), os.Stdout, os.Stderr)
	if errors.As(err, &ce) {
		_ = r.RenderError(ce.ToEnvelope())
		return
	}
	_ = r.RenderError(envelope.CLIError{Code: string(clierr.KindGeneric), Message: err.Error()})
}

// modeFromFlags infers the output mode from -n/--no-tui and -o/--output. On a
// flag-parse error the flag set may be only partially parsed; unresolved flags
// fall back to their defaults, i.e. text mode.
func modeFromFlags(flags *pflag.FlagSet) render.Mode {
	if b, err := flags.GetBool(flagNoTUI); err == nil && b {
		return render.ModeJSON
	}
	if o, err := flags.GetString(flagOutput); err == nil && strings.EqualFold(o, "json") {
		return render.ModeJSON
	}
	return render.ModeText
}
