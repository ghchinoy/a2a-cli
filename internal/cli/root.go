// Copyright 2026 The A2A Authors
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
	"fmt"
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
	flagContinue     = "continue"
	flagLast         = "last"
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

	// Classify an unknown subcommand as a usage error (exit 2) here rather than
	// letting cobra return its untyped error at the Find stage. A non-nil Args
	// validator makes Find succeed, so global flags (notably -o/-n) are parsed
	// before validation runs — that lets the Execute-level renderer honor the
	// requested output mode. Any positional arg on the root is an unknown command
	// (design §3.5 exit table); an empty arg list is a bare invocation.
	root.Args = func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return nil
		}
		return clierr.New(clierr.KindUsage, fmt.Sprintf("unknown command %q for %q", args[0], cmd.CommandPath()))
	}
	// A RunE makes the root Runnable, so execution reaches Args validation instead
	// of short-circuiting to help for a non-runnable command (cobra returns
	// flag.ErrHelp before validating args otherwise). It is reached only for a bare
	// `a2a-cli` invocation (Args passed with no positionals), where showing help and
	// exiting 0 is the desired behavior.
	root.RunE = func(cmd *cobra.Command, _ []string) error {
		return cmd.Help()
	}

	// Validate the -o/--output VALUE in ONE central place so it covers every
	// command (design §3.5 / spec §9): a bad value (e.g. -o yaml) is a USAGE error
	// (exit 2), not a silent fallback to text. Because the requested output mode is
	// itself unusable, the diagnostic is emitted as TEXT on stderr (never json/yaml)
	// and marked rendered so the Execute-level handler does not re-render it.
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error {
		return validateOutputFlag(cmd.Flags())
	}

	pf := root.PersistentFlags()
	pf.StringP(flagServiceURL, "u", "", "base URL of the A2A agent")
	pf.String(flagContextID, "", "context ID to continue a conversation")
	pf.String(flagTaskID, "", "task ID to send against an existing task")
	// Resume the stored conversation without re-supplying identifiers (spec §6.4).
	// --continue resumes the stored contextId (a new task in the same context);
	// --last additionally resumes the stored latest taskId (send against that task).
	// Both are opt-in so a bare `send` never silently attaches to a stale task;
	// explicit --context-id/--task-id override the stored value (§6.4 line 168).
	pf.Bool(flagContinue, false, "resume the stored conversation (contextId) for the next turn")
	pf.Bool(flagLast, false, "resume the stored last task (latest taskId) for the next turn")
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

	root.AddCommand(newDiscoverCommand())
	root.AddCommand(newSendCommand())
	root.AddCommand(newGetCommand())
	root.AddCommand(newCancelCommand())
	root.AddCommand(newSessionCommand())
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

// validOutputValues is the set of accepted -o/--output values. "tui" is accepted
// but degrades to text at Tier 1 (design §3.8); "text"/"json" are the real modes.
var validOutputValues = map[string]bool{"text": true, "json": true, "tui": true}

// validateOutputFlag rejects an unknown -o/--output value as a USAGE error
// (exit 2, addendum CO-1). The diagnostic is rendered as TEXT on stderr — the
// requested mode is invalid, so we must not try to emit json — and marked
// rendered so cli.Execute does not surface it a second time.
func validateOutputFlag(flags *pflag.FlagSet) error {
	o, err := flags.GetString(flagOutput)
	if err != nil || validOutputValues[strings.ToLower(o)] {
		return nil
	}
	e := clierr.New(clierr.KindUsage, fmt.Sprintf("invalid --output value %q (want one of: text, json, tui)", o))
	r := render.New(render.ModeText, os.Stdout, os.Stderr)
	_ = r.RenderError(e.ToEnvelope())
	e.MarkRendered()
	return e
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
