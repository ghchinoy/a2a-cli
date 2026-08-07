// Copyright 2026 The A2A Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package cli

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/ghchinoy/a2a-cli/internal/clierr"
	"github.com/ghchinoy/a2a-cli/internal/session"
)

// runCLI drives the exact top-level path as cli.Execute — root.Execute() followed
// by top-level rendering of any unrendered error — while capturing stdout/stderr
// (the renderers write to os.Stdout/os.Stderr) and the mapped exit code.
func runCLI(t *testing.T, args ...string) (stdout, stderr string, exit int) {
	t.Helper()

	oldOut, oldErr := os.Stdout, os.Stderr
	rOut, wOut, _ := os.Pipe()
	rErr, wErr, _ := os.Pipe()
	os.Stdout, os.Stderr = wOut, wErr

	root := NewRootCommand()
	root.SetArgs(args)
	err := root.Execute()
	if err != nil {
		renderTopLevelError(root.Flags(), err)
	}

	_ = wOut.Close()
	_ = wErr.Close()
	os.Stdout, os.Stderr = oldOut, oldErr
	outBytes, _ := io.ReadAll(rOut)
	errBytes, _ := io.ReadAll(rErr)
	return string(outBytes), string(errBytes), clierr.ExitCode(err)
}

// cleanConfigDir points XDG_CONFIG_HOME at a temp dir and clears the service-URL
// env var so config resolution starts from a known-empty state.
func cleanConfigDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("A2A_CLI_SERVICE_URL", "")
	return dir
}

// B1: a missing <text> argument must exit 2 AND surface a diagnostic — in text
// mode to stderr, in json mode as the Appendix B error object on stdout.
func TestExecute_MissingArg_RendersOutput(t *testing.T) {
	t.Run("text", func(t *testing.T) {
		cleanConfigDir(t)
		out, errOut, code := runCLI(t, "send")
		if code != 2 {
			t.Fatalf("exit = %d, want 2", code)
		}
		if !strings.Contains(errOut, "USAGE") {
			t.Errorf("stderr should carry a USAGE diagnostic, got %q", errOut)
		}
		if out != "" {
			t.Errorf("stdout should be empty in text mode, got %q", out)
		}
	})
	t.Run("json", func(t *testing.T) {
		cleanConfigDir(t)
		out, _, code := runCLI(t, "send", "-o", "json")
		if code != 2 {
			t.Fatalf("exit = %d, want 2", code)
		}
		if !strings.Contains(out, `"code": "USAGE"`) {
			t.Errorf("stdout should carry the Appendix B USAGE envelope, got %q", out)
		}
	})
}

// B1: an unknown flag must exit 2 and surface a diagnostic (not fail silently).
func TestExecute_UnknownFlag_RendersOutput(t *testing.T) {
	cleanConfigDir(t)
	out, errOut, code := runCLI(t, "send", "--bogus", "x", "hi")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if errOut == "" && out == "" {
		t.Error("an unknown flag must produce a diagnostic on some stream, got none")
	}
}

// C1: an unknown subcommand is a USAGE error (exit 2), rendered through the same
// Execute-level default path as flag/arg errors — the Appendix B USAGE envelope on
// stdout in json mode, a stderr diagnostic in text mode. It must not fall through
// to GENERIC/exit 1 or ignore -o json.
func TestExecute_UnknownCommand_RendersUsage(t *testing.T) {
	t.Run("text", func(t *testing.T) {
		cleanConfigDir(t)
		out, errOut, code := runCLI(t, "bogus", "hi")
		if code != 2 {
			t.Fatalf("exit = %d, want 2", code)
		}
		if !strings.Contains(errOut, "USAGE") {
			t.Errorf("stderr should carry a USAGE diagnostic, got %q", errOut)
		}
		if out != "" {
			t.Errorf("stdout should be empty in text mode, got %q", out)
		}
	})
	t.Run("json", func(t *testing.T) {
		cleanConfigDir(t)
		out, errOut, code := runCLI(t, "bogus", "-o", "json", "hi")
		if code != 2 {
			t.Fatalf("exit = %d, want 2", code)
		}
		if !strings.Contains(out, `"code": "USAGE"`) {
			t.Errorf("stdout should carry the Appendix B USAGE envelope, got %q", out)
		}
		if errOut != "" {
			t.Errorf("stderr should be empty in json mode (envelope goes to stdout), got %q", errOut)
		}
	})
}

// R-f: a missing service URL (no -u, no session, no env) is a rendered usage error.
func TestRunSend_MissingURL_Usage(t *testing.T) {
	cleanConfigDir(t)
	out, _, code := runCLI(t, "send", "-o", "json", "hi")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(out, `"code": "USAGE"`) {
		t.Errorf("expected USAGE envelope on stdout, got %q", out)
	}
}

// R-b/R-f: an invalid -H header is rejected as a usage error before any network.
func TestRunSend_BadHeader_Usage(t *testing.T) {
	cleanConfigDir(t)
	// Empty header name.
	_, errOut, code := runCLI(t, "send", "-u", "http://127.0.0.1:1", "-H", ": value", "hi")
	if code != 2 {
		t.Fatalf("empty-name header: exit = %d, want 2", code)
	}
	if !strings.Contains(strings.ToLower(errOut), "header") {
		t.Errorf("expected a header diagnostic on stderr, got %q", errOut)
	}

	// CRLF in value.
	_, _, code = runCLI(t, "send", "-u", "http://127.0.0.1:1", "-H", "X-Evil: a\r\nb", "hi")
	if code != 2 {
		t.Fatalf("CRLF header: exit = %d, want 2", code)
	}
}

// R-f: a saved session replays its serviceUrl when -u is omitted. Pointing the
// session at a closed port yields UNREACHABLE (exit 3) rather than USAGE (exit 2),
// which proves the URL was sourced from the session rather than treated as missing.
func TestRunSend_SessionReplay(t *testing.T) {
	cleanConfigDir(t)
	if err := session.Save(&session.Session{ServiceURL: "http://127.0.0.1:1", Transport: "jsonrpc"}); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	_, _, code := runCLI(t, "send", "-o", "json", "--timeout", "2s", "hi")
	if code == 2 {
		t.Fatal("exit 2 (usage) means the session serviceUrl was not replayed")
	}
	if code != 3 {
		t.Errorf("exit = %d, want 3 (unreachable via replayed session URL)", code)
	}
}
