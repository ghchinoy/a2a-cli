// Copyright 2026 The a2a-cli Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package cli

import (
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
)

// A successful cancel reports the resulting state (CANCELED) and exits 0 — a
// successful cancel is NOT a failure (deliberate exit-code decision, spec §8.4).
func TestCancel_Success_ReportsState(t *testing.T) {
	cleanConfigDir(t)
	srv := newTaskServer(t, taskEndpoint{
		cancelFn: func(id string) (int, any) {
			return 200, taskDoc(id, "ctx-1", "TASK_STATE_CANCELED")
		},
	})

	out, errOut, code := runCLI(t, "cancel", "task-42", "-u", srv.URL)
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (successful cancel is not a failure)\nstderr: %s", code, errOut)
	}
	if !strings.Contains(out, "TASK_STATE_CANCELED") || !strings.Contains(out, "task-42") {
		t.Errorf("cancel should report the resulting state and taskId, got: %s", out)
	}
}

func TestCancel_Success_JSON(t *testing.T) {
	cleanConfigDir(t)
	srv := newTaskServer(t, taskEndpoint{
		cancelFn: func(id string) (int, any) {
			return 200, taskDoc(id, "ctx-1", "TASK_STATE_CANCELED")
		},
	})

	out, _, code := runCLI(t, "cancel", "task-42", "-u", srv.URL, "-o", "json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("stdout is not a single valid JSON envelope: %v\n%s", err, out)
	}
	if env["state"] != "TASK_STATE_CANCELED" || env["taskId"] != "task-42" {
		t.Errorf("envelope missing resulting state/taskId: %v", env)
	}
}

// Idempotency: a repeat cancel of an already-terminal task returns not-cancelable
// from the server; the wrapper absorbs it, reports the resulting state via a
// follow-up get, and does NOT error spuriously (spec §8.4).
func TestCancel_Idempotent_Repeat(t *testing.T) {
	cleanConfigDir(t)
	var canceled atomic.Bool
	srv := newTaskServer(t, taskEndpoint{
		cancelFn: func(id string) (int, any) {
			if canceled.Swap(true) {
				// Second cancel: task is already terminal.
				return 409, notCancelableBody()
			}
			return 200, taskDoc(id, "ctx-1", "TASK_STATE_CANCELED")
		},
		getFn: func(id, _ string) (int, any) {
			// Follow-up get after a not-cancelable response reports the resting state.
			return 200, taskDoc(id, "ctx-1", "TASK_STATE_CANCELED")
		},
	})

	// First cancel succeeds.
	_, _, code := runCLI(t, "cancel", "task-42", "-u", srv.URL)
	if code != 0 {
		t.Fatalf("first cancel exit = %d, want 0", code)
	}

	// Repeat cancel must not error spuriously and must still report the state.
	out, errOut, code := runCLI(t, "cancel", "task-42", "-u", srv.URL)
	if code != 0 {
		t.Fatalf("repeat cancel exit = %d, want 0 (idempotent)\nstderr: %s", code, errOut)
	}
	if !strings.Contains(out, "TASK_STATE_CANCELED") {
		t.Errorf("repeat cancel should still report the resulting state, got: %s", out)
	}
}

// CO-2: cancel of an unknown task surfaces the normalized NOT_FOUND envelope.
func TestCancel_Missing_NotFound(t *testing.T) {
	cleanConfigDir(t)
	srv := newTaskServer(t, taskEndpoint{
		cancelFn: func(_ string) (int, any) { return 404, notFoundBody() },
	})

	out, _, code := runCLI(t, "cancel", "ghost", "-u", srv.URL, "-o", "json")
	if code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("invalid json error object: %v\n%s", err, out)
	}
	if env["code"] != "NOT_FOUND" {
		t.Errorf("envelope code = %v, want NOT_FOUND", env["code"])
	}
}

// H. not-cancelable then follow-up-get-ALSO-fails: the idempotency fallback tries
// a get to report the resting state, but when that get itself fails the ORIGINAL
// error must surface (non-zero exit, no panic) rather than being swallowed. Closes
// the uncovered CancelTask/classify fallback-failure branch.
func TestCancel_NotCancelable_FollowupGetFails(t *testing.T) {
	cleanConfigDir(t)
	srv := newTaskServer(t, taskEndpoint{
		cancelFn: func(_ string) (int, any) { return 409, notCancelableBody() },
		getFn:    func(_, _ string) (int, any) { return 500, errorBody(500, "INTERNAL", "backend down", "INTERNAL") },
	})

	out, errOut, code := runCLI(t, "cancel", "task-42", "-u", srv.URL, "-o", "json")
	if code == 0 {
		t.Fatalf("exit = %d, want non-zero (both cancel and fallback get failed)\nstderr: %s", code, errOut)
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("stdout is not a single valid JSON error object: %v\n%s", err, out)
	}
	// The surfaced error is the ORIGINAL cancel failure, not the fallback get's.
	msg, _ := env["message"].(string)
	if !strings.Contains(msg, "cancel") {
		t.Errorf("surfaced error should be the original cancel failure, got message %q", msg)
	}
}

func TestCancel_MissingArg_Usage(t *testing.T) {
	cleanConfigDir(t)
	_, errOut, code := runCLI(t, "cancel", "-u", "http://x")
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (usage)", code)
	}
	if !strings.Contains(errOut, "USAGE") {
		t.Errorf("expected a USAGE diagnostic on stderr, got %q", errOut)
	}
}
