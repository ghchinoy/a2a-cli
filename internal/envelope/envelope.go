// Copyright 2026 The A2A Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

// Package envelope defines the normalized, spec-stable output types (a2a-cli
// spec Appendix B) and the conversion from a2a-go SDK/core types into them.
//
// These types are the ONLY thing the render layer serializes to stdout. Keeping
// normalization here — and forbidding the command layer from touching SDK/proto
// types (design §3.2) — is what makes Appendix B conformance structural rather
// than per-command discipline (design §3.4, resolves findings Q2).
package envelope

import (
	"encoding/base64"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

// Canonical TASK_STATE_* wire values (a2a-cli spec §7.1 / a2a.proto TaskState).
// Mirrors the SDK's a2a.TaskState* string values so the wire value is preserved
// verbatim in the envelope.
const (
	StateUnspecified   = ""
	StateSubmitted     = "TASK_STATE_SUBMITTED"
	StateWorking       = "TASK_STATE_WORKING"
	StateCompleted     = "TASK_STATE_COMPLETED"
	StateFailed        = "TASK_STATE_FAILED"
	StateCanceled      = "TASK_STATE_CANCELED"
	StateInputRequired = "TASK_STATE_INPUT_REQUIRED"
	StateRejected      = "TASK_STATE_REJECTED"
	StateAuthRequired  = "TASK_STATE_AUTH_REQUIRED"
)

// TaskResult is the Appendix B task-operation object — the stable §9.3/§6.3
// contract emitted on stdout in json mode. Pointer identifier fields are null
// (not empty string) when the server created no task, so callers can tell
// "no id" from "empty id" and the tool never fabricates ids (design §6.1).
type TaskResult struct {
	TaskID    *string    `json:"taskId"`
	ContextID *string    `json:"contextId"`
	State     string     `json:"state"`
	Artifacts []Artifact `json:"artifacts,omitempty"`
	Message   *Message   `json:"message"`
	// History is the task's message history, surfaced by `get --history <n>`. It is
	// an ADDITIVE, backward-compatible field (omitempty): it never appears unless a
	// server returns history, so the frozen send-path json shape (design §4.2/§6) is
	// unaffected.
	History []Message `json:"history,omitempty"`
}

// SessionView is the normalized, inspectable view of the local session store
// surfaced by `session show` (spec §6.4: persisted state MUST be inspectable). It
// is NOT part of the frozen Appendix B task/error contract — it is an
// inspection-only shape — but it is rendered through the same render seam so the
// stdout/stderr discipline (§9.1) and terminal sanitization (CO-5) still hold.
// Path is always present; when no session file exists, Exists is false and the
// identifier fields are empty.
type SessionView struct {
	Path         string `json:"path"`
	Exists       bool   `json:"exists"`
	ContextID    string `json:"contextId,omitempty"`
	LatestTaskID string `json:"latestTaskId,omitempty"`
	ServiceURL   string `json:"serviceUrl,omitempty"`
	Transport    string `json:"transport,omitempty"`
	UpdatedAt    string `json:"updatedAt,omitempty"`
}

// CLIError is the Appendix B error object — normalized across transports (§9.4).
type CLIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	A2ACode any    `json:"a2aCode"`
}

// Message is a normalized message (e.g. the agent's reply when the server
// returns a Message instead of a Task, as the Go hello-world server does).
type Message struct {
	Role  string `json:"role,omitempty"`
	Parts []Part `json:"parts,omitempty"`
}

// Artifact is a normalized artifact produced by a task.
type Artifact struct {
	ArtifactID string `json:"artifactId,omitempty"`
	Name       string `json:"name,omitempty"`
	Parts      []Part `json:"parts,omitempty"`
}

// Part is a normalized content part. Exactly one of the content fields is set.
type Part struct {
	Text      string `json:"text,omitempty"`
	Data      any    `json:"data,omitempty"`
	FileURL   string `json:"fileUrl,omitempty"`
	Bytes     string `json:"bytes,omitempty"` // base64 of raw bytes
	MediaType string `json:"mediaType,omitempty"`
}

// IsTerminal reports whether a task state is terminal (spec §7.1): the task is
// finished and will not change further.
func IsTerminal(state string) bool {
	switch state {
	case StateCompleted, StateFailed, StateCanceled, StateRejected:
		return true
	default:
		return false
	}
}

// IsInterrupted reports whether a task state is an interrupted state (spec §7.1):
// the task is paused awaiting caller action and polling must stop immediately.
func IsInterrupted(state string) bool {
	switch state {
	case StateInputRequired, StateAuthRequired:
		return true
	default:
		return false
	}
}

// FromSendResult normalizes a non-streaming SendMessage result, which may be
// either a Task or a bare Message (the Go hello-world server yields a Message).
func FromSendResult(res a2a.SendMessageResult) *TaskResult {
	switch v := res.(type) {
	case *a2a.Task:
		return FromTask(v)
	case *a2a.Message:
		return FromMessage(v)
	default:
		return &TaskResult{State: StateUnspecified}
	}
}

// FromTask normalizes an a2a.Task into a TaskResult.
func FromTask(t *a2a.Task) *TaskResult {
	if t == nil {
		return &TaskResult{State: StateUnspecified}
	}
	tr := &TaskResult{State: string(t.Status.State)}
	if id := string(t.ID); id != "" {
		tr.TaskID = &id
	}
	if t.ContextID != "" {
		cid := t.ContextID
		tr.ContextID = &cid
	}
	for _, a := range t.Artifacts {
		tr.Artifacts = append(tr.Artifacts, artifactFromSDK(a))
	}
	if t.Status.Message != nil {
		tr.Message = messageFromSDK(t.Status.Message)
	}
	// History is normalized when the server returns it (populated by `get
	// --history <n>`, which threads HistoryLength on the request). It stays absent
	// otherwise, so the field is additive and does not perturb the send-path shape.
	for _, m := range t.History {
		if nm := messageFromSDK(m); nm != nil {
			tr.History = append(tr.History, *nm)
		}
	}
	return tr
}

// FromMessage normalizes a bare Message response (no task). Per the brief, the
// Go hello-world executor yields a Message and no Task, so taskId/contextId stay
// null unless the message carries them. A direct message reply represents a
// completed interaction, so the normalized state is COMPLETED (this is a state,
// not a fabricated id — design §6.1 forbids only inventing ids).
func FromMessage(m *a2a.Message) *TaskResult {
	if m == nil {
		return &TaskResult{State: StateUnspecified}
	}
	tr := &TaskResult{State: StateCompleted, Message: messageFromSDK(m)}
	if id := string(m.TaskID); id != "" {
		tr.TaskID = &id
	}
	if m.ContextID != "" {
		cid := m.ContextID
		tr.ContextID = &cid
	}
	return tr
}

func messageFromSDK(m *a2a.Message) *Message {
	if m == nil {
		return nil
	}
	out := &Message{Role: string(m.Role)}
	for _, p := range m.Parts {
		out.Parts = append(out.Parts, partFromSDK(p))
	}
	return out
}

func artifactFromSDK(a *a2a.Artifact) Artifact {
	out := Artifact{Name: a.Name}
	if id := string(a.ID); id != "" {
		out.ArtifactID = id
	}
	for _, p := range a.Parts {
		out.Parts = append(out.Parts, partFromSDK(p))
	}
	return out
}

func partFromSDK(p *a2a.Part) Part {
	if p == nil {
		return Part{}
	}
	out := Part{MediaType: p.MediaType}
	switch c := p.Content.(type) {
	case a2a.Text:
		out.Text = string(c)
	case a2a.Data:
		out.Data = c.Value
	case a2a.URL:
		out.FileURL = string(c)
	case a2a.Raw:
		out.Bytes = base64.StdEncoding.EncodeToString([]byte(c))
	}
	return out
}
