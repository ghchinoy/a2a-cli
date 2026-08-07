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

// Renderer serializes envelope types. The stdout/stderr writers are private on
// purpose: render is the ONLY component that writes program output (design §3.8),
// and every text write goes through the emit chokepoint so untrusted
// (server/card-derived) content is sanitized by construction. The single
// exception is writeJSON, which emits the -o json envelope to RAW stdout. Nothing
// outside this package can reach the streams, so a new print seam cannot bypass
// sanitization.
type Renderer struct {
	Mode Mode
	out  io.Writer // primary/stdout — raw only via writeJSON; else via emit
	err  io.Writer // diagnostics/stderr — always via emit
}

// New builds a Renderer for the given mode and streams.
func New(mode Mode, out, err io.Writer) *Renderer {
	return &Renderer{Mode: mode, out: out, err: err}
}

// emit is the single text-output chokepoint (design §3.8: render is the only
// writer). `format` is a TRUSTED constant authored by the CLI; every string or
// error arg is treated as UNTRUSTED (server/card-derived) and passed through
// `clean` before interpolation, so a value's embedded ESC/CR/control bytes can
// never reach the terminal while the template's own newlines survive. Non-string
// args (bool, int) pass through untouched.
//
// INVARIANT — do NOT add another `fmt.Fprint*` to r.out/r.err anywhere in this
// package. Route text through emit so sanitization holds by construction; the
// ONLY sanctioned raw writers are writeJSON (the -o json stdout envelope) and
// writeStreamLine (one compact -o json --stream NDJSON record) — both hand off to
// encoding/json, which already single-escapes control bytes, so re-sanitizing
// would corrupt them. Pass untrusted (server/card-derived) content as a
// string/error arg, NEVER baked into the `format` constant and never via a
// non-string type through %v (CO-5). This holds for the streaming render paths
// (RenderStreamEvent/RenderStreamFinal) exactly as for the one-shot renderers.
func (r *Renderer) emit(w io.Writer, clean func(string) string, format string, args ...any) {
	for i, a := range args {
		switch v := a.(type) {
		case string:
			args[i] = clean(v)
		case error:
			args[i] = clean(v.Error())
		}
	}
	fmt.Fprintf(w, format, args...)
}

// Warn writes a diagnostic to stderr only (never pollutes json stdout). Warnings
// are single-line, so untrusted args are escaped with sanitizeTerminal (an
// embedded newline/CR in a value is a fake-line primitive and must not survive).
func (r *Renderer) Warn(format string, args ...any) {
	r.emit(r.err, sanitizeTerminal, format+"\n", args...)
}

// RenderTask writes a TaskResult. In json mode it emits the Appendix B envelope
// to stdout and nothing else. In text mode it prints minimal labeled fields plus,
// when a taskId exists, a copy-pasteable resume command (spec §6.3).
func (r *Renderer) RenderTask(tr *envelope.TaskResult) error {
	if r.Mode == ModeJSON {
		return writeJSON(r.out, tr)
	}
	return r.renderTaskText(tr)
}

// renderTaskText prints a TaskResult for a TTY. State/TaskID/Context/Message/
// Artifact text are all server-derived, so every value is routed through the emit
// chokepoint (sanitizeTerminal); only the CLI-authored labels and the '\n' in the
// Resume template are trusted.
func (r *Renderer) renderTaskText(tr *envelope.TaskResult) error {
	w := r.out
	r.emit(w, sanitizeTerminal, "State:     %s\n", nonEmpty(tr.State))
	r.emit(w, sanitizeTerminal, "Task ID:   %s\n", ptr(tr.TaskID))
	r.emit(w, sanitizeTerminal, "Context:   %s\n", ptr(tr.ContextID))
	if tr.Message != nil {
		if text := joinParts(tr.Message.Parts); text != "" {
			r.emit(w, sanitizeTerminal, "Message:   %s\n", text)
		}
	}
	for _, a := range tr.Artifacts {
		label := a.Name
		if label == "" {
			label = a.ArtifactID
		}
		// With --include-artifacts the client keeps artifact Parts, so contents are
		// rendered; without it the Parts are summarized away and only the identifier
		// line is shown (spec §8.3). Both routes go through the emit chokepoint.
		if text := joinParts(a.Parts); text != "" {
			r.emit(w, sanitizeTerminal, "Artifact %s: %s\n", label, text)
		} else {
			r.emit(w, sanitizeTerminal, "Artifact %s\n", nonEmpty(label))
		}
	}
	// Message history, surfaced by `get --history <n>`. Each entry is server-derived
	// and routed through the emit chokepoint (CO-5).
	for _, m := range tr.History {
		if text := joinParts(m.Parts); text != "" {
			r.emit(w, sanitizeTerminal, "History %s: %s\n", nonEmpty(m.Role), text)
		}
	}
	if tr.TaskID != nil {
		r.emit(w, sanitizeTerminal, "\nResume:    a2a-cli send --task-id %s \"<reply>\"\n", *tr.TaskID)
	}
	return nil
}

// RenderStreamEvent renders one streaming event as it arrives (spec §7.2). In json
// mode it emits the event as a single NDJSON record on stdout (one JSON object per
// line, each carrying a `type` field); in text mode it prints a human-readable,
// sanitized line per event through the emit chokepoint. Every server-derived value
// (state, ids, artifact/message text) is passed as a string arg so CO-5 sanitization
// holds by construction on the new streaming render path.
func (r *Renderer) RenderStreamEvent(ev envelope.StreamEvent) error {
	if r.Mode == ModeJSON {
		return writeStreamLine(r.out, ev)
	}
	return r.renderStreamEventText(ev)
}

// renderStreamEventText prints one streaming event for a TTY. All server-derived
// content routes through the emit chokepoint (sanitizeTerminal); only the
// CLI-authored labels are trusted (CO-5).
func (r *Renderer) renderStreamEventText(ev envelope.StreamEvent) error {
	w := r.out
	switch ev.Type {
	case envelope.StreamTypeTask:
		r.emit(w, sanitizeTerminal, "Task:      %s [%s]\n", ptr(ev.TaskID), nonEmpty(ev.State))
	case envelope.StreamTypeStatus:
		r.emit(w, sanitizeTerminal, "Status:    %s\n", nonEmpty(ev.State))
		if ev.Message != nil {
			if text := joinParts(ev.Message.Parts); text != "" {
				r.emit(w, sanitizeTerminal, "  message: %s\n", text)
			}
		}
	case envelope.StreamTypeArtifact:
		if ev.Artifact != nil {
			label := ev.Artifact.Name
			if label == "" {
				label = ev.Artifact.ArtifactID
			}
			if text := joinParts(ev.Artifact.Parts); text != "" {
				r.emit(w, sanitizeTerminal, "Artifact %s: %s\n", nonEmpty(label), text)
			} else {
				r.emit(w, sanitizeTerminal, "Artifact %s\n", nonEmpty(label))
			}
		}
	case envelope.StreamTypeMessage:
		if ev.Message != nil {
			if text := joinParts(ev.Message.Parts); text != "" {
				r.emit(w, sanitizeTerminal, "Message:   %s\n", text)
			}
		}
	}
	return nil
}

// RenderStreamFinal renders the reconciled terminal result of a stream (spec §7.3).
// In json mode it emits the `final` NDJSON record so stdout stays one-object-per-line;
// in text mode it prints the same labeled task view as the blocking path (including
// the resume hint), so `send --stream` and `send` converge on an identical terminal
// presentation.
func (r *Renderer) RenderStreamFinal(tr *envelope.TaskResult) error {
	if r.Mode == ModeJSON {
		return writeStreamLine(r.out, envelope.FinalStreamEvent(tr))
	}
	return r.renderTaskText(tr)
}

// RenderCard writes a FullCard (design §8.1). In json mode it emits only the
// normalized FullCard to stdout; in text mode it prints every card section with
// copy-pasteable identifiers and shows which transport the client would select.
func (r *Renderer) RenderCard(c *envelope.FullCard) error {
	if c == nil {
		return nil
	}
	if r.Mode == ModeJSON {
		return writeJSON(r.out, c)
	}
	return r.renderCardText(c)
}

// renderCardText prints a FullCard for a TTY. EVERY card-derived value reaches the
// terminal through the emit chokepoint, which sanitizes each string arg: an agent
// card is untrusted input (discover's whole purpose is inspecting an agent before
// trusting it), so control/escape bytes must never reach the terminal (audit F-1).
// Structural labels in the trusted format templates and boolean capabilities are
// tool-controlled and pass through as-is.
func (r *Renderer) renderCardText(c *envelope.FullCard) error {
	w := r.out
	r.emit(w, sanitizeTerminal, "Name:        %s\n", nonEmpty(c.Name))
	if c.Description != "" {
		r.emit(w, sanitizeTerminal, "Description: %s\n", c.Description)
	}
	if c.Version != "" {
		r.emit(w, sanitizeTerminal, "Version:     %s\n", c.Version)
	}
	if c.Provider != nil && (c.Provider.Organization != "" || c.Provider.URL != "") {
		r.emit(w, sanitizeTerminal, "Provider:    %s\n", joinNonEmpty(" — ", c.Provider.Organization, c.Provider.URL))
	}
	if c.DocumentationURL != "" {
		r.emit(w, sanitizeTerminal, "Docs:        %s\n", c.DocumentationURL)
	}

	r.emit(w, sanitizeTerminal, "\nCapabilities:\n")
	r.emit(w, sanitizeTerminal, "  streaming:         %t\n", c.Capabilities.Streaming)
	r.emit(w, sanitizeTerminal, "  pushNotifications: %t\n", c.Capabilities.PushNotifications)
	r.emit(w, sanitizeTerminal, "  extendedAgentCard: %t\n", c.Capabilities.ExtendedAgentCard)
	for _, ext := range c.Capabilities.Extensions {
		req := ""
		if ext.Required {
			req = " (required)"
		}
		r.emit(w, sanitizeTerminal, "  extension: %s%s\n", ext.URI, req)
	}

	r.emit(w, sanitizeTerminal, "\nInterfaces:\n")
	if len(c.Interfaces) == 0 {
		r.emit(w, sanitizeTerminal, "  (none declared)\n")
	}
	for _, iface := range c.Interfaces {
		r.emit(w, sanitizeTerminal, "  - %-8s %s", iface.Transport, iface.URL)
		if iface.ProtocolVersion != "" {
			r.emit(w, sanitizeTerminal, " [v%s]", iface.ProtocolVersion)
		}
		if iface.RoutingID != "" {
			r.emit(w, sanitizeTerminal, " routingId=%s", iface.RoutingID)
		}
		r.emit(w, sanitizeTerminal, "\n")
	}

	r.emit(w, sanitizeTerminal, "\nSecurity schemes:\n")
	if len(c.SecuritySchemes) == 0 {
		r.emit(w, sanitizeTerminal, "  (none — no authentication required)\n")
	}
	for _, s := range c.SecuritySchemes {
		r.emit(w, sanitizeTerminal, "  - %s: %s", s.Name, s.Type)
		if s.Detail != "" {
			r.emit(w, sanitizeTerminal, " (%s)", s.Detail)
		}
		r.emit(w, sanitizeTerminal, "\n")
	}

	r.emit(w, sanitizeTerminal, "\nSkills:\n")
	if len(c.Skills) == 0 {
		r.emit(w, sanitizeTerminal, "  (none declared)\n")
	}
	for _, sk := range c.Skills {
		r.emit(w, sanitizeTerminal, "  - %s", nonEmpty(sk.ID))
		if sk.Name != "" {
			r.emit(w, sanitizeTerminal, " (%s)", sk.Name)
		}
		r.emit(w, sanitizeTerminal, "\n")
		if sk.Description != "" {
			r.emit(w, sanitizeTerminal, "      %s\n", sk.Description)
		}
		if len(sk.Tags) > 0 {
			r.emit(w, sanitizeTerminal, "      tags: %s\n", strings.Join(sk.Tags, ", "))
		}
	}

	r.emit(w, sanitizeTerminal, "\nSelected transport: %s -> %s\n", nonEmpty(c.Selection.Transport), nonEmpty(c.Selection.URL))
	r.emit(w, sanitizeTerminal, "  reason: %s\n", c.Selection.Reason)
	if c.Selection.RoutingID != "" {
		r.emit(w, sanitizeTerminal, "  routing identifier: %s\n", c.Selection.RoutingID)
	}
	return nil
}

// RenderSession writes the local session view for `session show` (spec §6.4). In
// json mode it emits the SessionView object to stdout and nothing else; in text
// mode it prints the store path and, when a session exists, its labeled fields.
// Every stored value (serviceURL, ids) is server/user-derived, so each is routed
// through the emit chokepoint as a string arg (CO-5) — only the CLI-authored
// labels are trusted.
func (r *Renderer) RenderSession(sv *envelope.SessionView) error {
	if sv == nil {
		return nil
	}
	if r.Mode == ModeJSON {
		return writeJSON(r.out, sv)
	}
	w := r.out
	r.emit(w, sanitizeTerminal, "Path:      %s\n", sv.Path)
	if !sv.Exists {
		r.emit(w, sanitizeTerminal, "(no session stored)\n")
		return nil
	}
	r.emit(w, sanitizeTerminal, "Context:   %s\n", nonEmpty(sv.ContextID))
	r.emit(w, sanitizeTerminal, "Last Task: %s\n", nonEmpty(sv.LatestTaskID))
	r.emit(w, sanitizeTerminal, "Service:   %s\n", nonEmpty(sv.ServiceURL))
	r.emit(w, sanitizeTerminal, "Transport: %s\n", nonEmpty(sv.Transport))
	r.emit(w, sanitizeTerminal, "Updated:   %s\n", nonEmpty(sv.UpdatedAt))
	return nil
}

// RenderError writes a normalized error. In json mode the Appendix B error object
// goes to stdout (still valid JSON); in text mode a human message goes to stderr.
func (r *Renderer) RenderError(ce envelope.CLIError) error {
	if r.Mode == ModeJSON {
		// encoding/json already escapes control bytes; the stdout envelope must
		// stay single-escaped, so do NOT run the terminal sanitizer over it.
		return writeJSON(r.out, ce)
	}
	// Text diagnostic goes to a TTY: an error Message can embed card-derived,
	// attacker-controlled content (e.g. B2's rejected interface URL), so it routes
	// through the emit chokepoint with sanitizeDiagnostic (audit N-1 / review C-1).
	// sanitizeDiagnostic is per-line, so a CLI-authored multi-line Message keeps its
	// own '\n' while ESC/CR inside any line is escaped.
	r.emit(r.err, sanitizeDiagnostic, "Error [%s]: %s\n", ce.Code, ce.Message)
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

// writeStreamLine emits v as a single NDJSON record: compact (no indent) so the
// whole object stays on one line, with the trailing newline Encode already writes.
// It is a sanctioned raw stdout writer (see the emit INVARIANT): encoding/json
// single-escapes control bytes, so the CO-5 terminal sanitizer must NOT run over it
// (that would corrupt the JSON). Successive calls produce valid NDJSON on stdout.
func writeStreamLine(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
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

// sanitizeDiagnostic makes an untrusted diagnostic string (an error message or a
// warning that may embed card/server-derived content) safe for a terminal while
// preserving the CLI's own line structure. It splits on '\n' — the line
// separator the CLI itself authors in multi-line diagnostics (e.g. the
// --validate problem list) — and runs sanitizeTerminal over each line, so ESC,
// CR, and other control bytes inside any line are escaped but CLI-authored line
// breaks survive. This is the diagnostic-seam counterpart to the success-render
// seam; every command that reports errors/warnings inherits it centrally.
func sanitizeDiagnostic(s string) string {
	if !strings.Contains(s, "\n") {
		return sanitizeTerminal(s)
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		lines[i] = sanitizeTerminal(line)
	}
	return strings.Join(lines, "\n")
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
