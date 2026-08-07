// Copyright 2026 The A2A Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package cli

import (
	"strings"
	"testing"
)

// CO-1: an invalid -o/--output VALUE is a USAGE error (exit 2), rendered as a
// TEXT stderr diagnostic (never json/yaml), with empty stdout. Validated in one
// central place so it holds for every command (send AND discover).
func TestInvalidOutputValue_IsUsageError(t *testing.T) {
	cases := []struct {
		name string
		args []string
	}{
		{"send", []string{"send", "-u", "http://127.0.0.1:1", "-o", "yaml", "hi"}},
		{"discover", []string{"discover", "-u", "http://127.0.0.1:1", "-o", "yaml"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cleanConfigDir(t)
			out, errOut, code := runCLI(t, tc.args...)
			if code != 2 {
				t.Fatalf("exit = %d, want 2 (usage)", code)
			}
			if out != "" {
				t.Errorf("stdout must be empty for an invalid output mode, got %q", out)
			}
			if !strings.Contains(errOut, "USAGE") || !strings.Contains(errOut, "output") {
				t.Errorf("expected a USAGE output diagnostic on stderr, got %q", errOut)
			}
			// R-e: the diagnostic must name the full accepted set (tui included),
			// not just text|json.
			if !strings.Contains(errOut, "text") || !strings.Contains(errOut, "json") || !strings.Contains(errOut, "tui") {
				t.Errorf("diagnostic should list the accepted values text/json/tui, got %q", errOut)
			}
		})
	}
}
