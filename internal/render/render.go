// Copyright 2026 The a2a-cli Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

// Package render writes normalized envelope types to output, enforcing the
// stdout/stderr discipline of spec §9.1: in json mode ONLY valid JSON reaches
// stdout; every diagnostic, warning, and human hint goes to stderr (design §3.8).
package render

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/ghchinoy/a2a-cli/internal/envelope"
)

// Mode selects the output format.
type Mode string

const (
	ModeText Mode = "text"
	ModeJSON Mode = "json"
)

// Renderer serializes envelope types. Out is the machine/primary stream (stdout);
// Err is the diagnostics stream (stderr). In json mode nothing but JSON is ever
// written to Out.
type Renderer struct {
	Mode Mode
	Out  io.Writer
	Err  io.Writer
}

// New builds a Renderer for the given mode and streams.
func New(mode Mode, out, err io.Writer) *Renderer {
	return &Renderer{Mode: mode, Out: out, Err: err}
}

// Warn writes a diagnostic to stderr only (never pollutes json stdout).
func (r *Renderer) Warn(format string, args ...any) {
	fmt.Fprintf(r.Err, format+"\n", args...)
}

// RenderTask writes a TaskResult. In json mode it emits the Appendix B envelope
// to stdout and nothing else. In text mode it prints minimal labeled fields plus,
// when a taskId exists, a copy-pasteable resume command (spec §6.3).
func (r *Renderer) RenderTask(tr *envelope.TaskResult) error {
	if r.Mode == ModeJSON {
		return writeJSON(r.Out, tr)
	}
	return r.renderTaskText(tr)
}

func (r *Renderer) renderTaskText(tr *envelope.TaskResult) error {
	w := r.Out
	fmt.Fprintf(w, "State:     %s\n", nonEmpty(tr.State))
	fmt.Fprintf(w, "Task ID:   %s\n", ptr(tr.TaskID))
	fmt.Fprintf(w, "Context:   %s\n", ptr(tr.ContextID))
	if tr.Message != nil {
		if text := joinParts(tr.Message.Parts); text != "" {
			fmt.Fprintf(w, "Message:   %s\n", text)
		}
	}
	for _, a := range tr.Artifacts {
		label := a.Name
		if label == "" {
			label = a.ArtifactID
		}
		if text := joinParts(a.Parts); text != "" {
			fmt.Fprintf(w, "Artifact %s: %s\n", label, text)
		}
	}
	if tr.TaskID != nil {
		fmt.Fprintf(w, "\nResume:    a2a-cli send --task-id %s \"<reply>\"\n", *tr.TaskID)
	}
	return nil
}

// RenderCard writes a FullCard (design §8.1). In json mode it emits only the
// normalized FullCard to stdout; in text mode it prints every card section with
// copy-pasteable identifiers and shows which transport the client would select.
func (r *Renderer) RenderCard(c *envelope.FullCard) error {
	if c == nil {
		return nil
	}
	if r.Mode == ModeJSON {
		return writeJSON(r.Out, c)
	}
	return r.renderCardText(c)
}

// renderCardText prints a FullCard for a TTY. EVERY card-derived string is passed
// through sanitizeTerminal first: an agent card is untrusted input (discover's
// whole purpose is inspecting an agent before trusting it), so control/escape
// bytes must never reach the terminal (audit F-1). Structural labels and boolean
// capabilities are tool-controlled and printed as-is.
func (r *Renderer) renderCardText(c *envelope.FullCard) error {
	w := r.Out
	fmt.Fprintf(w, "Name:        %s\n", sanitizeTerminal(nonEmpty(c.Name)))
	if c.Description != "" {
		fmt.Fprintf(w, "Description: %s\n", sanitizeTerminal(c.Description))
	}
	if c.Version != "" {
		fmt.Fprintf(w, "Version:     %s\n", sanitizeTerminal(c.Version))
	}
	if c.Provider != nil && (c.Provider.Organization != "" || c.Provider.URL != "") {
		fmt.Fprintf(w, "Provider:    %s\n", joinNonEmpty(" — ", sanitizeTerminal(c.Provider.Organization), sanitizeTerminal(c.Provider.URL)))
	}
	if c.DocumentationURL != "" {
		fmt.Fprintf(w, "Docs:        %s\n", sanitizeTerminal(c.DocumentationURL))
	}

	fmt.Fprintf(w, "\nCapabilities:\n")
	fmt.Fprintf(w, "  streaming:         %t\n", c.Capabilities.Streaming)
	fmt.Fprintf(w, "  pushNotifications: %t\n", c.Capabilities.PushNotifications)
	fmt.Fprintf(w, "  extendedAgentCard: %t\n", c.Capabilities.ExtendedAgentCard)
	for _, ext := range c.Capabilities.Extensions {
		req := ""
		if ext.Required {
			req = " (required)"
		}
		fmt.Fprintf(w, "  extension: %s%s\n", sanitizeTerminal(ext.URI), req)
	}

	fmt.Fprintf(w, "\nInterfaces:\n")
	if len(c.Interfaces) == 0 {
		fmt.Fprintf(w, "  (none declared)\n")
	}
	for _, iface := range c.Interfaces {
		fmt.Fprintf(w, "  - %-8s %s", sanitizeTerminal(iface.Transport), sanitizeTerminal(iface.URL))
		if iface.ProtocolVersion != "" {
			fmt.Fprintf(w, " [v%s]", sanitizeTerminal(iface.ProtocolVersion))
		}
		if iface.RoutingID != "" {
			fmt.Fprintf(w, " routingId=%s", sanitizeTerminal(iface.RoutingID))
		}
		fmt.Fprintf(w, "\n")
	}

	fmt.Fprintf(w, "\nSecurity schemes:\n")
	if len(c.SecuritySchemes) == 0 {
		fmt.Fprintf(w, "  (none — no authentication required)\n")
	}
	for _, s := range c.SecuritySchemes {
		fmt.Fprintf(w, "  - %s: %s", sanitizeTerminal(s.Name), sanitizeTerminal(s.Type))
		if s.Detail != "" {
			fmt.Fprintf(w, " (%s)", sanitizeTerminal(s.Detail))
		}
		fmt.Fprintf(w, "\n")
	}

	fmt.Fprintf(w, "\nSkills:\n")
	if len(c.Skills) == 0 {
		fmt.Fprintf(w, "  (none declared)\n")
	}
	for _, sk := range c.Skills {
		fmt.Fprintf(w, "  - %s", sanitizeTerminal(nonEmpty(sk.ID)))
		if sk.Name != "" {
			fmt.Fprintf(w, " (%s)", sanitizeTerminal(sk.Name))
		}
		fmt.Fprintf(w, "\n")
		if sk.Description != "" {
			fmt.Fprintf(w, "      %s\n", sanitizeTerminal(sk.Description))
		}
		if len(sk.Tags) > 0 {
			fmt.Fprintf(w, "      tags: %s\n", sanitizeTerminal(strings.Join(sk.Tags, ", ")))
		}
	}

	fmt.Fprintf(w, "\nSelected transport: %s -> %s\n", sanitizeTerminal(nonEmpty(c.Selection.Transport)), sanitizeTerminal(nonEmpty(c.Selection.URL)))
	fmt.Fprintf(w, "  reason: %s\n", sanitizeTerminal(c.Selection.Reason))
	if c.Selection.RoutingID != "" {
		fmt.Fprintf(w, "  routing identifier: %s\n", sanitizeTerminal(c.Selection.RoutingID))
	}
	return nil
}

// RenderError writes a normalized error. In json mode the Appendix B error object
// goes to stdout (still valid JSON); in text mode a human message goes to stderr.
func (r *Renderer) RenderError(ce envelope.CLIError) error {
	if r.Mode == ModeJSON {
		return writeJSON(r.Out, ce)
	}
	fmt.Fprintf(r.Err, "Error [%s]: %s\n", ce.Code, ce.Message)
	return nil
}

// ResumeHint writes a copy-pasteable resume command to stderr. Used on
// interruption/timeout so the taskId is surfaced without polluting json stdout.
func (r *Renderer) ResumeHint(taskID *string) {
	if taskID == nil || *taskID == "" {
		return
	}
	r.Warn("resume with: a2a-cli send --task-id %s \"<reply>\"", *taskID)
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func joinParts(parts []envelope.Part) string {
	out := ""
	for _, p := range parts {
		if p.Text != "" {
			if out != "" {
				out += " "
			}
			out += p.Text
		}
	}
	return out
}

func ptr(s *string) string {
	if s == nil {
		return "(none)"
	}
	return *s
}

// joinNonEmpty joins the non-empty arguments with sep.
func joinNonEmpty(sep string, parts ...string) string {
	var kept []string
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, sep)
}

func nonEmpty(s string) string {
	if s == "" {
		return "(unknown)"
	}
	return s
}

// sanitizeTerminal makes an untrusted string safe to print to a terminal by
// escaping bytes that a hostile agent card could use to drive the TTY: C0 control
// characters (incl. ESC 0x1b and CR 0x0d), DEL (0x7f), and the C1 range
// (0x80–0x9f, which some terminals treat as control introducers). Tab (0x09) is
// preserved as-is; everything else — including all printable multi-byte UTF-8 —
// passes through unchanged. Offending runes are rendered as \xNN so the operator
// still sees that a byte was there without letting it execute (audit F-1).
//
// This is the render-seam sanitizer: any command whose renderer prints
// server-derived strings in text mode should route them through here so terminal
// safety is enforced in one place rather than per-command.
func sanitizeTerminal(s string) string {
	// Fast path: most strings are clean, so avoid allocating a builder.
	if !needsSanitize(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 8)
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			// Invalid UTF-8 byte: escape the raw byte so it can never reach
			// the terminal (and never gets re-interpreted downstream).
			fmt.Fprintf(&b, "\\x%02x", s[i])
			i++
			continue
		}
		if isSafeRune(r) {
			b.WriteRune(r)
		} else {
			fmt.Fprintf(&b, "\\x%02x", r)
		}
		i += size
	}
	return b.String()
}

// isSafeRune reports whether r may be printed to a terminal unescaped: tab and
// any rune that is not a C0/C1 control or DEL.
func isSafeRune(r rune) bool {
	return r == '\t' || (r >= 0x20 && r != 0x7f && !(r >= 0x80 && r <= 0x9f))
}

// needsSanitize reports whether s contains any byte sanitizeTerminal would escape.
func needsSanitize(s string) bool {
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			return true
		}
		if !isSafeRune(r) {
			return true
		}
		i += size
	}
	return false
}
