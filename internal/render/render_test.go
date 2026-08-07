// Copyright 2026 The A2A Authors
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

func strptr(s string) *string { return &s }

func TestRenderTask_JSON_OnlyValidJSONOnStdout(t *testing.T) {
	var out, errb bytes.Buffer
	r := New(ModeJSON, &out, &errb)
	tr := &envelope.TaskResult{
		State:   envelope.StateCompleted,
		Message: &envelope.Message{Role: "ROLE_AGENT", Parts: []envelope.Part{{Text: "Hello, world!"}}},
	}
	if err := r.RenderTask(tr); err != nil {
		t.Fatal(err)
	}

	// stdout must be exactly one valid JSON document, nothing else.
	var decoded envelope.TaskResult
	dec := json.NewDecoder(&out)
	if err := dec.Decode(&decoded); err != nil {
		t.Fatalf("stdout is not valid JSON: %v", err)
	}
	if dec.More() {
		t.Fatal("stdout has trailing content after the JSON document")
	}
	if decoded.State != envelope.StateCompleted || decoded.Message == nil {
		t.Errorf("round-trip mismatch: %+v", decoded)
	}
}

func TestRenderTask_JSON_NullIdentifiers(t *testing.T) {
	var out, errb bytes.Buffer
	r := New(ModeJSON, &out, &errb)
	tr := &envelope.TaskResult{State: envelope.StateCompleted, Message: &envelope.Message{Parts: []envelope.Part{{Text: "hi"}}}}
	if err := r.RenderTask(tr); err != nil {
		t.Fatal(err)
	}
	s := out.String()
	// Appendix B: identifier fields present and null when no task exists.
	for _, want := range []string{`"taskId": null`, `"contextId": null`, `"state": "TASK_STATE_COMPLETED"`} {
		if !strings.Contains(s, want) {
			t.Errorf("json output missing %q\ngot: %s", want, s)
		}
	}
	if errb.Len() != 0 {
		t.Errorf("stderr should be empty in json success path, got %q", errb.String())
	}
}

func TestRenderTask_Text_Golden(t *testing.T) {
	var out, errb bytes.Buffer
	r := New(ModeText, &out, &errb)
	tr := &envelope.TaskResult{
		TaskID:    strptr("task-123"),
		ContextID: strptr("ctx-9"),
		State:     envelope.StateInputRequired,
		Message:   &envelope.Message{Parts: []envelope.Part{{Text: "need more"}}},
	}
	if err := r.RenderTask(tr); err != nil {
		t.Fatal(err)
	}
	want := "State:     TASK_STATE_INPUT_REQUIRED\n" +
		"Task ID:   task-123\n" +
		"Context:   ctx-9\n" +
		"Message:   need more\n" +
		"\nResume:    a2a-cli send --task-id task-123 \"<reply>\"\n"
	if out.String() != want {
		t.Errorf("text golden mismatch:\n got: %q\nwant: %q", out.String(), want)
	}
}

func TestRenderError_JSON_OnStdout(t *testing.T) {
	var out, errb bytes.Buffer
	r := New(ModeJSON, &out, &errb)
	if err := r.RenderError(envelope.CLIError{Code: "UNREACHABLE", Message: "no route", A2ACode: nil}); err != nil {
		t.Fatal(err)
	}
	var ce envelope.CLIError
	if err := json.Unmarshal(out.Bytes(), &ce); err != nil {
		t.Fatalf("error envelope not valid JSON: %v", err)
	}
	if ce.Code != "UNREACHABLE" {
		t.Errorf("code = %q", ce.Code)
	}
	if errb.Len() != 0 {
		t.Errorf("stderr should be empty in json error path, got %q", errb.String())
	}
}

func TestRenderError_Text_OnStderr(t *testing.T) {
	var out, errb bytes.Buffer
	r := New(ModeText, &out, &errb)
	if err := r.RenderError(envelope.CLIError{Code: "TIMEOUT", Message: "slow"}); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Errorf("stdout should be empty for text-mode error, got %q", out.String())
	}
	if !strings.Contains(errb.String(), "TIMEOUT") {
		t.Errorf("stderr missing error, got %q", errb.String())
	}
}

func TestWarnAndResumeHint_GoToStderr(t *testing.T) {
	var out, errb bytes.Buffer
	r := New(ModeJSON, &out, &errb)
	r.Warn("diagnostic %d", 1)
	r.ResumeHint(strptr("abc"))
	if out.Len() != 0 {
		t.Errorf("stdout must stay clean; got %q", out.String())
	}
	if !strings.Contains(errb.String(), "diagnostic 1") || !strings.Contains(errb.String(), "--task-id abc") {
		t.Errorf("stderr diagnostics missing: %q", errb.String())
	}
}
