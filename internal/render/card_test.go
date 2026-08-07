// Copyright 2026 The a2a-cli Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package render

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ghchinoy/a2a-cli/internal/envelope"
)

func sampleFullCard() *envelope.FullCard {
	return &envelope.FullCard{
		Name:        "Full Agent",
		Description: "an agent with all sections",
		Version:     "2.1.0",
		Provider:    &envelope.CardProvider{Organization: "Acme", URL: "http://acme.example"},
		Capabilities: envelope.CardCapabilities{
			Streaming:  true,
			Extensions: []envelope.CardExtension{{URI: "urn:ext:foo", Required: true}},
		},
		Interfaces: []envelope.CardInterface{
			{Transport: "HTTP+JSON", URL: "http://host/rest", ProtocolVersion: "1.0", RoutingID: "tenant-7"},
		},
		SecuritySchemes: []envelope.CardSecurity{
			{Name: "apiKeyScheme", Type: "apiKey", Detail: `in header as "X-API-Key"`},
		},
		Skills: []envelope.CardSkill{
			{ID: "hello", Name: "Hello", Description: "says hi", Tags: []string{"greeting"}},
		},
		Selection: envelope.CardSelection{Transport: "http-json", URL: "http://host/rest", Reason: "card-declared preference", RoutingID: "tenant-7"},
	}
}

func TestRenderCard_JSON_OnlyValidJSONOnStdout(t *testing.T) {
	var out, errb bytes.Buffer
	r := New(ModeJSON, &out, &errb)
	if err := r.RenderCard(sampleFullCard()); err != nil {
		t.Fatal(err)
	}
	var decoded envelope.FullCard
	dec := json.NewDecoder(&out)
	if err := dec.Decode(&decoded); err != nil {
		t.Fatalf("stdout is not valid JSON: %v", err)
	}
	if dec.More() {
		t.Fatal("stdout has trailing content after the JSON document")
	}
	if decoded.Name != "Full Agent" || len(decoded.Skills) != 1 || decoded.Selection.Transport != "http-json" {
		t.Errorf("round-trip mismatch: %+v", decoded)
	}
	if errb.Len() != 0 {
		t.Errorf("stderr should be empty in json card path, got %q", errb.String())
	}
}

func TestRenderCard_JSON_PresentsAllSections(t *testing.T) {
	// R-c: the json envelope must carry every section, not just name/skills.
	var out, errb bytes.Buffer
	r := New(ModeJSON, &out, &errb)
	if err := r.RenderCard(sampleFullCard()); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		`"provider"`,
		`"organization": "Acme"`,
		`"capabilities"`,
		`"streaming": true`,
		`"extensions"`,
		`"uri": "urn:ext:foo"`,
		`"interfaces"`,
		`"transport": "HTTP+JSON"`,
		`"securitySchemes"`,
		`"name": "apiKeyScheme"`,
		`"skills"`,
		`"selection"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("json output missing %q\n---\n%s", want, got)
		}
	}
}

// TestSanitizeTerminal_EscapesControlBytes is the unit-level guard for the B1
// render-seam sanitizer: it must escape ESC/CR/other control + C1 + DEL while
// preserving tab and printable UTF-8.
func TestSanitizeTerminal_EscapesControlBytes(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"plain", "hello world", "hello world"},
		{"utf8", "café — 日本語", "café — 日本語"},
		{"tab-kept", "a\tb", "a\tb"},
		{"esc", "a\x1b[31mred", `a\x1b[31mred`},
		{"cr", "line1\rline2", `line1\x0dline2`},
		{"lf", "line1\nline2", `line1\x0aline2`},
		{"del", "a\x7fb", `a\x7fb`},
		{"c1", "a\x9bb", `a\x9bb`},
		{"nul", "a\x00b", `a\x00b`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sanitizeTerminal(tc.in); got != tc.want {
				t.Errorf("sanitizeTerminal(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestRenderCard_Text_SanitizesHostileCard is the B1 regression test: a card
// whose fields carry ANSI escapes and carriage returns must render with NO raw
// control bytes surviving into text-mode stdout.
func TestRenderCard_Text_SanitizesHostileCard(t *testing.T) {
	hostile := "\x1b[2K\rSelected transport: evil -> http://attacker\x1b[0m"
	c := &envelope.FullCard{
		Name:        "Agent\x1b[31m",
		Description: hostile,
		Version:     "1.0\r\n",
		Provider:    &envelope.CardProvider{Organization: "Org\x1b[1m", URL: "http://x\x07"},
		Capabilities: envelope.CardCapabilities{
			Extensions: []envelope.CardExtension{{URI: "urn:\x1bevil"}},
		},
		Interfaces: []envelope.CardInterface{
			{Transport: "HTTP\x1b+JSON", URL: "http://h\rx", ProtocolVersion: "1\x1b", RoutingID: "t\x1b7"},
		},
		SecuritySchemes: []envelope.CardSecurity{
			{Name: "s\x1bk", Type: "apiKey\r", Detail: "d\x1be"},
		},
		Skills: []envelope.CardSkill{
			{ID: "id\x1b", Name: "n\r", Description: "de\x1bsc", Tags: []string{"t\x1ba", "t\rb"}},
		},
		Selection: envelope.CardSelection{Transport: "http-json\x1b", URL: "http://h\r", Reason: hostile, RoutingID: "r\x1b"},
	}
	var out, errb bytes.Buffer
	r := New(ModeText, &out, &errb)
	if err := r.RenderCard(c); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, b := range []byte{0x1b, 0x0d, 0x07, 0x00} {
		if bytes.IndexByte([]byte(got), b) >= 0 {
			t.Errorf("hostile card text output contains raw control byte 0x%02x\n---\n%q", b, got)
		}
	}
	// The escaped form must be present so the operator still sees the bytes.
	if !strings.Contains(got, `\x1b`) {
		t.Errorf("expected escaped ESC in output, got:\n%q", got)
	}
}

// TestRenderError_Text_SanitizesMessage is the C1/N-1 regression on the ERROR
// seam: an error whose Message carries card-derived ESC/CR (e.g. B2's rejected
// interface URL) must render escaped on stderr — no raw control bytes, no
// CRLF-fabricated line.
func TestRenderError_Text_SanitizesMessage(t *testing.T) {
	var out, errb bytes.Buffer
	r := New(ModeText, &out, &errb)
	ce := envelope.CLIError{
		Code:    "GENERIC",
		Message: "agent card interface URL uses unsupported scheme: http://h\x1b[2K\rError [SUCCESS]: agent verified as TRUSTED",
	}
	if err := r.RenderError(ce); err != nil {
		t.Fatal(err)
	}
	got := errb.String()
	for _, b := range []byte{0x1b, 0x0d} {
		if bytes.IndexByte([]byte(got), b) >= 0 {
			t.Errorf("error diagnostic contains raw control byte 0x%02x\n---\n%q", b, got)
		}
	}
	if !strings.Contains(got, `\x1b`) || !strings.Contains(got, `\x0d`) {
		t.Errorf("expected escaped ESC/CR in error output, got:\n%q", got)
	}
	if out.Len() != 0 {
		t.Errorf("text error must not write to stdout, got %q", out.String())
	}
}

// TestRenderError_JSON_NotDoubleEscaped confirms the -o json error envelope stays
// on stdout as valid, single-escaped JSON (the sanitizer must NOT touch it).
func TestRenderError_JSON_NotDoubleEscaped(t *testing.T) {
	var out, errb bytes.Buffer
	r := New(ModeJSON, &out, &errb)
	ce := envelope.CLIError{Code: "GENERIC", Message: "bad url http://h\x1b[0m\rspoof"}
	if err := r.RenderError(ce); err != nil {
		t.Fatal(err)
	}
	// stdout must be valid JSON that round-trips to the ORIGINAL message (proving
	// json escaping applied once, not the terminal sanitizer on top).
	var decoded envelope.CLIError
	if err := json.Unmarshal(out.Bytes(), &decoded); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%q", err, out.String())
	}
	if decoded.Message != ce.Message {
		t.Errorf("json message was altered (double-escaped?):\n got  %q\n want %q", decoded.Message, ce.Message)
	}
	if !bytes.Contains(out.Bytes(), []byte("\\u001b")) {
		t.Errorf("expected json to single-escape ESC as \\u001b, got:\n%q", out.String())
	}
}

// TestRenderError_Text_PreservesCLINewlines ensures the per-line sanitizer keeps
// the CLI's own multi-line structure (e.g. the --validate problem list).
func TestRenderError_Text_PreservesCLINewlines(t *testing.T) {
	var out, errb bytes.Buffer
	r := New(ModeText, &out, &errb)
	ce := envelope.CLIError{Code: "GENERIC", Message: "agent card failed validation:\n  - missing name\n  - skill\x1b bad"}
	if err := r.RenderError(ce); err != nil {
		t.Fatal(err)
	}
	got := errb.String()
	if !strings.Contains(got, "\n  - missing name\n  - skill") {
		t.Errorf("CLI-authored newlines were not preserved:\n%q", got)
	}
	if bytes.IndexByte([]byte(got), 0x1b) >= 0 {
		t.Errorf("raw ESC survived in a multi-line diagnostic:\n%q", got)
	}
}

// TestWarn_SanitizesUntrustedValue is the C1/N-1 regression on the WARN seam: a
// warning embedding a card-derived value with ESC/CR is sanitized on stderr.
func TestWarn_SanitizesUntrustedValue(t *testing.T) {
	var out, errb bytes.Buffer
	r := New(ModeText, &out, &errb)
	hostileHost := "other-host\x1b[2K\rWARNING: verified"
	r.Warn("WARNING: agent card selects a cross-origin interface: interface host is %s", hostileHost)
	got := errb.String()
	for _, b := range []byte{0x1b, 0x0d} {
		if bytes.IndexByte([]byte(got), b) >= 0 {
			t.Errorf("warn output contains raw control byte 0x%02x\n---\n%q", b, got)
		}
	}
	if !strings.Contains(got, `\x1b`) {
		t.Errorf("expected escaped ESC in warn output, got:\n%q", got)
	}
	// The single trailing newline Warn appends must remain a real newline.
	if !strings.HasSuffix(got, "\n") || strings.Count(got, "\n") != 1 {
		t.Errorf("warn should end with exactly one real newline, got:\n%q", got)
	}
}

// TestRenderTask_Text_SanitizesServerValues proves the structural chokepoint
// closed the latent task-text seam: State/Message/Artifact text are server-derived
// and now route through emit, so ESC/CR in a task result can no longer reach the
// TTY raw (previously renderTaskText printed them unsanitized).
func TestRenderTask_Text_SanitizesServerValues(t *testing.T) {
	var out, errb bytes.Buffer
	r := New(ModeText, &out, &errb)
	tid := "task\x1b[2K\r1"
	ctx := "ctx\x1b7"
	tr := &envelope.TaskResult{
		State:     "TASK_STATE_COMPLETED\x1b[31m",
		TaskID:    &tid,
		ContextID: &ctx,
		Message:   &envelope.Message{Parts: []envelope.Part{{Text: "reply\x1b[2K\rError [SUCCESS]: TRUSTED"}}},
		Artifacts: []envelope.Artifact{{Name: "art\x1b", Parts: []envelope.Part{{Text: "body\rfake"}}}},
	}
	if err := r.RenderTask(tr); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, b := range []byte{0x1b, 0x0d} {
		if bytes.IndexByte([]byte(got), b) >= 0 {
			t.Errorf("task text output contains raw control byte 0x%02x\n---\n%q", b, got)
		}
	}
	if !strings.Contains(got, `\x1b`) {
		t.Errorf("expected escaped ESC in task output, got:\n%q", got)
	}
}

func TestRenderCard_Text_PresentsAllSections(t *testing.T) {
	var out, errb bytes.Buffer
	r := New(ModeText, &out, &errb)
	if err := r.RenderCard(sampleFullCard()); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	// Every section must be present (design §8.1 acceptance).
	for _, want := range []string{
		"Name:        Full Agent",
		"Provider:    Acme — http://acme.example",
		"Capabilities:",
		"streaming:         true",
		"extension: urn:ext:foo (required)",
		"Interfaces:",
		"HTTP+JSON http://host/rest [v1.0] routingId=tenant-7",
		"Security schemes:",
		"apiKeyScheme: apiKey",
		"Skills:",
		"hello (Hello)",
		"tags: greeting",
		"Selected transport: http-json -> http://host/rest",
		"reason: card-declared preference",
		"routing identifier: tenant-7",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("text output missing %q\n---\n%s", want, got)
		}
	}
}

func TestRenderCard_Text_EmptySections(t *testing.T) {
	var out, errb bytes.Buffer
	r := New(ModeText, &out, &errb)
	c := &envelope.FullCard{
		Name:      "Bare",
		Selection: envelope.CardSelection{Transport: "http-json", URL: "http://x", Reason: "default"},
	}
	if err := r.RenderCard(c); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"Security schemes:",
		"(none — no authentication required)",
		"Skills:",
		"(none declared)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("empty-section output missing %q\n---\n%s", want, got)
		}
	}
}
