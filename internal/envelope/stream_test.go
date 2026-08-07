// Copyright 2026 The A2A Authors
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

// FromEvent must translate each SDK streaming event concrete type to the right
// normalized StreamEvent — this is the SDK->envelope seam the import boundary
// depends on (design §3.2). Failing this means internal/cli would render the wrong
// thing (or the wrong type would leak).
func TestFromEvent_TranslatesEachConcreteType(t *testing.T) {
	t.Run("task is first-class task event", func(t *testing.T) {
		task := &a2a.Task{
			ID:        a2a.TaskID("t1"),
			ContextID: "c1",
			Status:    a2a.TaskStatus{State: a2a.TaskStateWorking},
		}
		se := FromEvent(task)
		if se.Type != StreamTypeTask {
			t.Fatalf("type = %q, want %q", se.Type, StreamTypeTask)
		}
		if se.TaskID == nil || *se.TaskID != "t1" {
			t.Errorf("taskId = %v, want t1", se.TaskID)
		}
		if se.State != StateWorking {
			t.Errorf("state = %q, want %q", se.State, StateWorking)
		}
	})

	t.Run("status update", func(t *testing.T) {
		ev := &a2a.TaskStatusUpdateEvent{
			TaskID:    a2a.TaskID("t1"),
			ContextID: "c1",
			Status:    a2a.TaskStatus{State: a2a.TaskStateInputRequired},
		}
		se := FromEvent(ev)
		if se.Type != StreamTypeStatus {
			t.Fatalf("type = %q, want %q", se.Type, StreamTypeStatus)
		}
		if se.State != StateInputRequired {
			t.Errorf("state = %q, want %q", se.State, StateInputRequired)
		}
		if se.ContextID == nil || *se.ContextID != "c1" {
			t.Errorf("contextId = %v, want c1", se.ContextID)
		}
	})

	t.Run("artifact update", func(t *testing.T) {
		ev := &a2a.TaskArtifactUpdateEvent{
			TaskID:    a2a.TaskID("t1"),
			ContextID: "c1",
			Artifact: &a2a.Artifact{
				ID:    a2a.ArtifactID("a1"),
				Name:  "result",
				Parts: a2a.ContentParts{a2a.NewTextPart("hello artifact")},
			},
		}
		se := FromEvent(ev)
		if se.Type != StreamTypeArtifact {
			t.Fatalf("type = %q, want %q", se.Type, StreamTypeArtifact)
		}
		if se.Artifact == nil || se.Artifact.Name != "result" {
			t.Fatalf("artifact not translated: %+v", se.Artifact)
		}
		if len(se.Artifact.Parts) != 1 || se.Artifact.Parts[0].Text != "hello artifact" {
			t.Errorf("artifact parts = %+v, want one text part", se.Artifact.Parts)
		}
	})

	t.Run("bare message", func(t *testing.T) {
		ev := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("Hello, world!"))
		se := FromEvent(ev)
		if se.Type != StreamTypeMessage {
			t.Fatalf("type = %q, want %q", se.Type, StreamTypeMessage)
		}
		if se.Message == nil || len(se.Message.Parts) != 1 || se.Message.Parts[0].Text != "Hello, world!" {
			t.Errorf("message not translated: %+v", se.Message)
		}
	})
}

// G9 — an unrecognized event maps to StreamTypeUnknown (the FromEvent default
// branch), so a future/unknown SDK event type is never silently mis-typed. a2a.Event
// is a sealed union, so a nil event is the only "unrecognized" value constructible
// outside the handled concrete types; it must land in the default branch.
func TestFromEvent_UnrecognizedEvent_MapsToUnknown(t *testing.T) {
	se := FromEvent(nil)
	if se.Type != StreamTypeUnknown {
		t.Errorf("type = %q, want %q", se.Type, StreamTypeUnknown)
	}
}

// A terminal NDJSON record must carry the Appendix B task-operation fields and a
// type discriminator, all on one line — that is the shape `-o json --stream`
// promises consumers (spec §9.1).
func TestFinalStreamEvent_CarriesAppendixBFields(t *testing.T) {
	id, ctx := "task-9", "ctx-9"
	tr := &TaskResult{TaskID: &id, ContextID: &ctx, State: StateCompleted}
	b, err := json.Marshal(FinalStreamEvent(tr))
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	line := string(b)
	if strings.Contains(line, "\n") {
		t.Errorf("an NDJSON record must be single-line, got %q", line)
	}
	var got map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("not valid json: %v", err)
	}
	if got["type"] != StreamTypeFinal {
		t.Errorf("type = %v, want %q", got["type"], StreamTypeFinal)
	}
	if got["taskId"] != "task-9" || got["contextId"] != "ctx-9" || got["state"] != StateCompleted {
		t.Errorf("final record missing Appendix B fields: %v", got)
	}
}
