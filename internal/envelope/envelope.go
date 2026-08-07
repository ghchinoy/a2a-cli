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

// Stream event type discriminators (spec §7.2). Each streamed event is normalized
// to a StreamEvent carrying one of these in its Type field; the value is also the
// `type` field emitted on every NDJSON line in `-o json --stream` (Appendix B).
const (
	StreamTypeTask     = "task"     // the initial Task snapshot (MUST be first, §7.2)
	StreamTypeStatus   = "status"   // a TaskStatusUpdateEvent
	StreamTypeArtifact = "artifact" // a TaskArtifactUpdateEvent
	StreamTypeMessage  = "message"  // a bare Message (e.g. the hello-world reply)
	StreamTypeFinal    = "final"    // the reconciled terminal result (§7.3)
	StreamTypeError    = "error"    // a mid-stream/terminal error or cancel (§9.1)
	StreamTypeUnknown  = "unknown"  // an event of an unrecognized concrete type
)

// StreamEvent is the normalized streaming event — the ONLY streaming type that
// crosses the internal/client -> internal/cli boundary (import boundary, design
// §3.2: cli never sees SDK a2a.Event types) AND the single object shape emitted as
// one NDJSON line in `-o json --stream` (spec §9.1 / Appendix B). Type discriminates
// the event; the task-operation fields (taskId, contextId, state) are always present
// (null/empty when the event does not carry them) so terminal/final events satisfy
// Appendix B without a separate shape. Artifact/Artifacts/Message are populated per
// type. This is additive to the frozen Appendix B contract (design §4.2).
type StreamEvent struct {
	Type      string     `json:"type"`
	TaskID    *string    `json:"taskId"`
	ContextID *string    `json:"contextId"`
	State     string     `json:"state"`
	Message   *Message   `json:"message,omitempty"`
	Artifact  *Artifact  `json:"artifact,omitempty"`  // set on an artifact event
	Artifacts []Artifact `json:"artifacts,omitempty"` // set on the reconciled final event
}

// FromEvent normalizes a single SDK streaming event (a2a.Event, a sealed union of
// *a2a.Task | *a2a.TaskStatusUpdateEvent | *a2a.TaskArtifactUpdateEvent |
// *a2a.Message) into a StreamEvent. This is the SDK -> envelope translation seam
// for streaming (design §3.2): it lives here, in internal/envelope, so internal/cli
// consumes StreamEvent only and never imports SDK types.
func FromEvent(ev a2a.Event) StreamEvent {
	switch v := ev.(type) {
	case *a2a.Task:
		tr := FromTask(v)
		return StreamEvent{
			Type:      StreamTypeTask,
			TaskID:    tr.TaskID,
			ContextID: tr.ContextID,
			State:     tr.State,
			Artifacts: tr.Artifacts,
			Message:   tr.Message,
		}
	case *a2a.TaskStatusUpdateEvent:
		se := StreamEvent{Type: StreamTypeStatus, State: string(v.Status.State)}
		setIDs(&se, string(v.TaskID), v.ContextID)
		if v.Status.Message != nil {
			se.Message = messageFromSDK(v.Status.Message)
		}
		return se
	case *a2a.TaskArtifactUpdateEvent:
		se := StreamEvent{Type: StreamTypeArtifact}
		setIDs(&se, string(v.TaskID), v.ContextID)
		if v.Artifact != nil {
			a := artifactFromSDK(v.Artifact)
			se.Artifact = &a
		}
		return se
	case *a2a.Message:
		se := StreamEvent{Type: StreamTypeMessage, Message: messageFromSDK(v)}
		setIDs(&se, string(v.TaskID), v.ContextID)
		return se
	default:
		return StreamEvent{Type: StreamTypeUnknown}
	}
}

// setIDs fills the pointer identifier fields of a StreamEvent, leaving them nil
// (JSON null) when the event carries no id — never fabricating ids (design §6.1).
func setIDs(se *StreamEvent, taskID, contextID string) {
	if taskID != "" {
		se.TaskID = &taskID
	}
	if contextID != "" {
		se.ContextID = &contextID
	}
}

// FinalStreamEvent builds the terminal `final` NDJSON record from the reconciled
// TaskResult (spec §7.3: the authoritative final state is the reconciled get). It
// carries the full Appendix B task-operation fields so the terminal line is a
// complete result, not just a delta.
func FinalStreamEvent(tr *TaskResult) StreamEvent {
	se := StreamEvent{Type: StreamTypeFinal}
	if tr != nil {
		se.TaskID = tr.TaskID
		se.ContextID = tr.ContextID
		se.State = tr.State
		se.Artifacts = tr.Artifacts
		se.Message = tr.Message
	}
	return se
}

// CLIError is the Appendix B error object — normalized across transports (§9.4).
type CLIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	A2ACode any    `json:"a2aCode"`
}

// StreamErrorEvent is the TYPED terminal error record on the `-o json --stream`
// NDJSON path (spec §9.1: every object MUST carry a `type`). When a stream ends in
// an error or cancel, event lines have already been written to stdout, so the
// terminal error must ALSO be a typed NDJSON record — not the untyped one-shot
// Appendix B error envelope — or the trailing object breaks the one-object-per-line
// contract. It carries the Appendix B error fields (code/message/a2aCode) plus the
// task-operation ids when known (null when the stream never surfaced a task), and a
// `type` of StreamTypeError. The non-streaming error envelope (CLIError) is
// unchanged; this shape is additive and used only on the streaming NDJSON path.
type StreamErrorEvent struct {
	Type      string  `json:"type"`
	Code      string  `json:"code"`
	Message   string  `json:"message"`
	A2ACode   any     `json:"a2aCode"`
	TaskID    *string `json:"taskId"`
	ContextID *string `json:"contextId"`
}

// ErrorStreamEvent builds the typed terminal error record from a normalized
// Appendix B error plus the task ids known at the point of failure (either may be
// nil — never fabricate ids, design §6.1).
func ErrorStreamEvent(ce CLIError, taskID, contextID *string) StreamErrorEvent {
	return StreamErrorEvent{
		Type:      StreamTypeError,
		Code:      ce.Code,
		Message:   ce.Message,
		A2ACode:   ce.A2ACode,
		TaskID:    taskID,
		ContextID: contextID,
	}
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
