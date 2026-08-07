// Copyright 2026 The a2a-cli Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package envelope

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

func TestFromMessage_BareMessageResponse(t *testing.T) {
	// The Go hello-world server yields a Message and no Task.
	m := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("Hello, world!"))
	tr := FromMessage(m)

	if tr.TaskID != nil {
		t.Errorf("taskId should be nil for a bare message, got %q", *tr.TaskID)
	}
	if tr.ContextID != nil {
		t.Errorf("contextId should be nil for a bare message, got %q", *tr.ContextID)
	}
	if tr.State != StateCompleted {
		t.Errorf("bare message state = %q, want %q", tr.State, StateCompleted)
	}
	if tr.Message == nil || len(tr.Message.Parts) != 1 || tr.Message.Parts[0].Text != "Hello, world!" {
		t.Errorf("message not normalized correctly: %+v", tr.Message)
	}
}

func TestFromSendResult_MessageAndTask(t *testing.T) {
	msg := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("hi"))
	if got := FromSendResult(a2a.SendMessageResult(msg)); got.Message == nil {
		t.Error("expected message-normalized result from a Message send result")
	}

	task := &a2a.Task{ID: "t1", ContextID: "c1", Status: a2a.TaskStatus{State: a2a.TaskStateWorking}}
	got := FromSendResult(a2a.SendMessageResult(task))
	if got.TaskID == nil || *got.TaskID != "t1" || got.State != StateWorking {
		t.Errorf("task send result not normalized: %+v", got)
	}
}

func TestFromTask_AllStates(t *testing.T) {
	cases := []struct {
		sdk  a2a.TaskState
		want string
	}{
		{a2a.TaskStateSubmitted, StateSubmitted},
		{a2a.TaskStateWorking, StateWorking},
		{a2a.TaskStateCompleted, StateCompleted},
		{a2a.TaskStateFailed, StateFailed},
		{a2a.TaskStateCanceled, StateCanceled},
		{a2a.TaskStateInputRequired, StateInputRequired},
		{a2a.TaskStateRejected, StateRejected},
		{a2a.TaskStateAuthRequired, StateAuthRequired},
	}
	for _, tc := range cases {
		t.Run(string(tc.sdk), func(t *testing.T) {
			task := &a2a.Task{ID: "id", ContextID: "ctx", Status: a2a.TaskStatus{State: tc.sdk}}
			tr := FromTask(task)
			if tr.State != tc.want {
				t.Errorf("state = %q, want %q", tr.State, tc.want)
			}
			if tr.TaskID == nil || *tr.TaskID != "id" {
				t.Errorf("taskId not captured: %+v", tr.TaskID)
			}
			if tr.ContextID == nil || *tr.ContextID != "ctx" {
				t.Errorf("contextId not captured: %+v", tr.ContextID)
			}
		})
	}
}

func TestFromTask_ArtifactsAndStatusMessage(t *testing.T) {
	task := &a2a.Task{
		ID:        "t",
		ContextID: "c",
		Status: a2a.TaskStatus{
			State:   a2a.TaskStateCompleted,
			Message: a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("done")),
		},
		Artifacts: []*a2a.Artifact{
			{ID: "a1", Name: "result", Parts: a2a.ContentParts{a2a.NewTextPart("payload")}},
		},
	}
	tr := FromTask(task)
	if len(tr.Artifacts) != 1 || tr.Artifacts[0].Name != "result" || tr.Artifacts[0].Parts[0].Text != "payload" {
		t.Errorf("artifacts not normalized: %+v", tr.Artifacts)
	}
	if tr.Message == nil || tr.Message.Parts[0].Text != "done" {
		t.Errorf("status message not normalized: %+v", tr.Message)
	}
}

// TestFromTask_History confirms the additive History field is normalized from the
// SDK Task history (surfaced by `get --history <n>`) and stays absent otherwise.
func TestFromTask_History(t *testing.T) {
	task := &a2a.Task{
		ID:        "t",
		ContextID: "c",
		Status:    a2a.TaskStatus{State: a2a.TaskStateCompleted},
		History: []*a2a.Message{
			a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("hello")),
			a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("hi there")),
		},
	}
	tr := FromTask(task)
	if len(tr.History) != 2 {
		t.Fatalf("history len = %d, want 2: %+v", len(tr.History), tr.History)
	}
	if tr.History[0].Parts[0].Text != "hello" || tr.History[1].Parts[0].Text != "hi there" {
		t.Errorf("history not normalized: %+v", tr.History)
	}

	// A task without history yields no History field (omitempty stability).
	if tr := FromTask(&a2a.Task{ID: "t", Status: a2a.TaskStatus{State: a2a.TaskStateWorking}}); tr.History != nil {
		t.Errorf("History should be nil when absent, got %+v", tr.History)
	}
}

// F. The additive History field is omitempty: a TaskResult with no history must
// marshal WITHOUT a "history" key at all, so existing json consumers see the
// frozen envelope shape (design §3.8, only additive/omitempty fields).
func TestTaskResult_HistoryOmitemptyJSON(t *testing.T) {
	id := "t1"
	tr := TaskResult{TaskID: &id, State: StateCompleted}
	b, err := json.Marshal(tr)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(b), "history") {
		t.Errorf("no-history TaskResult must omit the history key, got %s", b)
	}

	// With history present, the key appears.
	tr.History = []Message{{Role: "ROLE_USER", Parts: []Part{{Text: "hi"}}}}
	b, err = json.Marshal(tr)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(b), "history") {
		t.Errorf("TaskResult with history must include the history key, got %s", b)
	}
}

func TestPartFromSDK_ContentKinds(t *testing.T) {
	if got := partFromSDK(a2a.NewTextPart("x")); got.Text != "x" {
		t.Errorf("text part = %+v", got)
	}
	if got := partFromSDK(a2a.NewDataPart(map[string]any{"k": "v"})); got.Data == nil {
		t.Errorf("data part = %+v", got)
	}
	if got := partFromSDK(a2a.NewFileURLPart("http://x/f", "text/plain")); got.FileURL != "http://x/f" {
		t.Errorf("url part = %+v", got)
	}
	if got := partFromSDK(a2a.NewRawPart([]byte("ab"))); got.Bytes != "YWI=" {
		t.Errorf("raw part = %+v", got)
	}
}

func TestTerminalAndInterrupted(t *testing.T) {
	terminal := []string{StateCompleted, StateFailed, StateCanceled, StateRejected}
	for _, s := range terminal {
		if !IsTerminal(s) {
			t.Errorf("%q should be terminal", s)
		}
		if IsInterrupted(s) {
			t.Errorf("%q should not be interrupted", s)
		}
	}
	interrupted := []string{StateInputRequired, StateAuthRequired}
	for _, s := range interrupted {
		if !IsInterrupted(s) {
			t.Errorf("%q should be interrupted", s)
		}
		if IsTerminal(s) {
			t.Errorf("%q should not be terminal", s)
		}
	}
	for _, s := range []string{StateSubmitted, StateWorking, StateUnspecified} {
		if IsTerminal(s) || IsInterrupted(s) {
			t.Errorf("%q should be neither terminal nor interrupted", s)
		}
	}
}
