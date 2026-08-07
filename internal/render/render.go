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

func nonEmpty(s string) string {
	if s == "" {
		return "(unknown)"
	}
	return s
}
