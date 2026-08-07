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
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/ghchinoy/a2a-cli/internal/session"
)

// sendRecord captures the taskId/contextId a `send` request carried to the server,
// parsed from the raw JSON body — the tests never import SDK/proto types, so the
// import boundary (design §3.2) holds in the test layer too.
type sendRecord struct {
	taskID    string
	contextID string
}

// sendServer is an httptest server that serves the well-known card (HTTP+JSON,
// pointing back at itself so the B2 same-origin check passes) and answers
// POST /message:send with a caller-supplied handler, recording every request so a
// test can assert exactly what reached the wire and how many times.
type sendServer struct {
	srv   *httptest.Server
	mu    sync.Mutex
	calls []sendRecord
}

func (s *sendServer) URL() string { return s.srv.URL }

func (s *sendServer) records() []sendRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]sendRecord, len(s.calls))
	copy(out, s.calls)
	return out
}

// newSendServer builds a send server. handler receives the parsed request record
// and returns the HTTP status and a JSON-serializable body (a StreamResponse for
// success, or a google.rpc.Status error body).
func newSendServer(t *testing.T, handler func(rec sendRecord) (int, any)) *sendServer {
	t.Helper()
	ss := &sendServer{}
	ss.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == wellKnownCardPath {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(taskCardJSON("http://" + r.Host))
			return
		}
		if strings.HasSuffix(r.URL.Path, "/message:send") || r.URL.Path == "/message:send" {
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			rec := sendRecord{}
			if msg, ok := body["message"].(map[string]any); ok {
				rec.taskID, _ = msg["taskId"].(string)
				rec.contextID, _ = msg["contextId"].(string)
			}
			ss.mu.Lock()
			ss.calls = append(ss.calls, rec)
			ss.mu.Unlock()
			status, resp := handler(rec)
			writeJSONStatus(w, status, resp)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(ss.srv.Close)
	return ss
}

// taskResultBody wraps a Task as the StreamResponse the SDK expects from send.
func taskResultBody(id, ctxID, state string) map[string]any {
	return map[string]any{"task": taskDoc(id, ctxID, state)}
}

// stateConflictBody is a non-not-found send error (a state conflict): it does NOT
// resolve to a2a.ErrTaskNotFound, so classify normalizes it to GENERIC (exit 1)
// rather than NOT_FOUND — proving a state conflict is surfaced, not swallowed.
func stateConflictBody() map[string]any {
	return errorBody(409, "FAILED_PRECONDITION", "task is not in a resumable state", "TASK_INVALID_STATE")
}

// --- CO-2 (PRIMARY): §6.2 continuation rules on send ------------------------

// CO-2: `send --task-id <id>` against a task the server does not know surfaces a
// NORMALIZED NOT_FOUND error (never a silent new task), in both json and text, and
// issues exactly ONE send (no fallback "new task" retry).
func TestSend_TaskID_NotFound_SurfacesError(t *testing.T) {
	t.Run("json", func(t *testing.T) {
		cleanConfigDir(t)
		ss := newSendServer(t, func(sendRecord) (int, any) { return 404, notFoundBody() })

		out, errOut, code := runCLI(t, "send", "hi", "-u", ss.URL(), "--task-id", "ghost", "-o", "json")
		if code != 1 {
			t.Fatalf("exit = %d, want 1 (GENERIC default for NOT_FOUND)\nstderr: %s", code, errOut)
		}
		var env map[string]any
		if err := json.Unmarshal([]byte(out), &env); err != nil {
			t.Fatalf("stdout is not a single valid JSON error object: %v\n%s", err, out)
		}
		if env["code"] != "NOT_FOUND" {
			t.Errorf("envelope code = %v, want NOT_FOUND", env["code"])
		}
		if env["a2aCode"] != "TASK_NOT_FOUND" {
			t.Errorf("envelope a2aCode = %v, want TASK_NOT_FOUND", env["a2aCode"])
		}
		recs := ss.records()
		if len(recs) != 1 {
			t.Fatalf("send must issue exactly ONE request (never a silent new task), got %d: %+v", len(recs), recs)
		}
		if recs[0].taskID != "ghost" {
			t.Errorf("the send carried taskId = %q, want %q", recs[0].taskID, "ghost")
		}
	})

	t.Run("text", func(t *testing.T) {
		cleanConfigDir(t)
		ss := newSendServer(t, func(sendRecord) (int, any) { return 404, notFoundBody() })

		out, errOut, code := runCLI(t, "send", "hi", "-u", ss.URL(), "--task-id", "ghost")
		if code != 1 {
			t.Fatalf("exit = %d, want 1\nstderr: %s", code, errOut)
		}
		if !strings.Contains(errOut, "NOT_FOUND") {
			t.Errorf("text diagnostic should carry NOT_FOUND on stderr, got %q", errOut)
		}
		if out != "" {
			t.Errorf("stdout should be empty in text-mode error, got %q", out)
		}
		if n := len(ss.records()); n != 1 {
			t.Errorf("send must issue exactly ONE request, got %d", n)
		}
	})
}

// CO-2: a non-not-found server error (state conflict) on a --task-id send surfaces
// as a normalized error (GENERIC/exit 1), NOT a silent success and NOT a second
// "new task" send.
func TestSend_TaskID_StateConflict_SurfacesError(t *testing.T) {
	cleanConfigDir(t)
	ss := newSendServer(t, func(sendRecord) (int, any) { return 409, stateConflictBody() })

	out, errOut, code := runCLI(t, "send", "hi", "-u", ss.URL(), "--task-id", "t-1", "-o", "json")
	if code != 1 {
		t.Fatalf("exit = %d, want 1 (state conflict surfaces as a normalized error)\nstderr: %s", code, errOut)
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("stdout is not a single valid JSON error object: %v\n%s", err, out)
	}
	if env["code"] == nil || env["code"] == "" {
		t.Errorf("state conflict must surface a normalized error code, got %v", env)
	}
	if _, isErr := env["error"]; env["state"] != nil && !isErr {
		t.Errorf("a state conflict must NOT be reported as a successful task result: %v", env)
	}
	recs := ss.records()
	if len(recs) != 1 {
		t.Fatalf("send must issue exactly ONE request (no silent new-task retry), got %d: %+v", len(recs), recs)
	}
	if recs[0].taskID != "t-1" {
		t.Errorf("the send carried taskId = %q, want %q", recs[0].taskID, "t-1")
	}
}

// §6.2: only --context-id supplied starts a NEW task grouped under that context —
// the contextId reaches the server and no taskId is sent.
func TestSend_ContextID_Only_StartsNewTask(t *testing.T) {
	cleanConfigDir(t)
	ss := newSendServer(t, func(sendRecord) (int, any) {
		return 200, taskResultBody("srv-task", "ctx-9", "TASK_STATE_COMPLETED")
	})

	_, errOut, code := runCLI(t, "send", "hi", "-u", ss.URL(), "--context-id", "ctx-9", "-o", "json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, errOut)
	}
	recs := ss.records()
	if len(recs) != 1 {
		t.Fatalf("want exactly one send, got %d", len(recs))
	}
	if recs[0].contextID != "ctx-9" {
		t.Errorf("contextId reaching server = %q, want %q", recs[0].contextID, "ctx-9")
	}
	if recs[0].taskID != "" {
		t.Errorf("no taskId should be sent for a context-only send, got %q", recs[0].taskID)
	}
}

// §6.2: both --context-id and --task-id are passed through UNCHANGED.
func TestSend_BothIDs_PassedThrough(t *testing.T) {
	cleanConfigDir(t)
	ss := newSendServer(t, func(sendRecord) (int, any) {
		return 200, taskResultBody("t-1", "ctx-9", "TASK_STATE_COMPLETED")
	})

	_, errOut, code := runCLI(t, "send", "hi", "-u", ss.URL(), "--context-id", "ctx-9", "--task-id", "t-1", "-o", "json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, errOut)
	}
	recs := ss.records()
	if len(recs) != 1 {
		t.Fatalf("want exactly one send, got %d", len(recs))
	}
	if recs[0].contextID != "ctx-9" || recs[0].taskID != "t-1" {
		t.Errorf("both ids must pass through verbatim, got taskId=%q contextId=%q", recs[0].taskID, recs[0].contextID)
	}
}

// --- CO-3: --continue / --last resume from session.json ---------------------

// CO-3: --continue resumes the stored contextId (a new task in the same context)
// without re-supplying --context-id, and does NOT bind the stored taskId.
func TestSend_Continue_ResumesContext(t *testing.T) {
	cleanConfigDir(t)
	ss := newSendServer(t, func(sendRecord) (int, any) {
		return 200, taskResultBody("new-task", "ctx-stored", "TASK_STATE_COMPLETED")
	})
	if err := session.Save(&session.Session{ContextID: "ctx-stored", LatestTaskID: "task-stored", ServiceURL: ss.URL(), Transport: "http-json"}); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	_, errOut, code := runCLI(t, "send", "hi", "-u", ss.URL(), "--continue", "-o", "json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, errOut)
	}
	recs := ss.records()
	if len(recs) != 1 {
		t.Fatalf("want one send, got %d", len(recs))
	}
	if recs[0].contextID != "ctx-stored" {
		t.Errorf("--continue should resume stored contextId, got %q", recs[0].contextID)
	}
	if recs[0].taskID != "" {
		t.Errorf("--continue must NOT bind the stored taskId, got %q", recs[0].taskID)
	}
}

// CO-3: --last resumes the stored latest taskId (send AGAINST that task), plus the
// stored contextId.
func TestSend_Last_ResumesLatestTask(t *testing.T) {
	cleanConfigDir(t)
	ss := newSendServer(t, func(sendRecord) (int, any) {
		return 200, taskResultBody("task-stored", "ctx-stored", "TASK_STATE_COMPLETED")
	})
	if err := session.Save(&session.Session{ContextID: "ctx-stored", LatestTaskID: "task-stored", ServiceURL: ss.URL(), Transport: "http-json"}); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	_, errOut, code := runCLI(t, "send", "hi", "-u", ss.URL(), "--last", "-o", "json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, errOut)
	}
	recs := ss.records()
	if len(recs) != 1 {
		t.Fatalf("want one send, got %d", len(recs))
	}
	if recs[0].taskID != "task-stored" {
		t.Errorf("--last should target stored latest taskId, got %q", recs[0].taskID)
	}
	if recs[0].contextID != "ctx-stored" {
		t.Errorf("--last should also carry stored contextId, got %q", recs[0].contextID)
	}
}

// CO-3 precedence: explicit --context-id/--task-id MUST override the stored value
// even under --continue/--last (§6.4 line 168).
func TestSend_ExplicitOverridesStoredResume(t *testing.T) {
	t.Run("context_id_overrides_continue", func(t *testing.T) {
		cleanConfigDir(t)
		ss := newSendServer(t, func(sendRecord) (int, any) {
			return 200, taskResultBody("t", "ctx-explicit", "TASK_STATE_COMPLETED")
		})
		if err := session.Save(&session.Session{ContextID: "ctx-stored", ServiceURL: ss.URL(), Transport: "http-json"}); err != nil {
			t.Fatalf("seed session: %v", err)
		}
		_, errOut, code := runCLI(t, "send", "hi", "-u", ss.URL(), "--continue", "--context-id", "ctx-explicit", "-o", "json")
		if code != 0 {
			t.Fatalf("exit = %d, want 0\nstderr: %s", code, errOut)
		}
		recs := ss.records()
		if len(recs) != 1 || recs[0].contextID != "ctx-explicit" {
			t.Errorf("explicit --context-id must override stored contextId, got %+v", recs)
		}
	})

	t.Run("task_id_overrides_last", func(t *testing.T) {
		cleanConfigDir(t)
		ss := newSendServer(t, func(sendRecord) (int, any) {
			return 200, taskResultBody("t-explicit", "ctx-stored", "TASK_STATE_COMPLETED")
		})
		if err := session.Save(&session.Session{ContextID: "ctx-stored", LatestTaskID: "task-stored", ServiceURL: ss.URL(), Transport: "http-json"}); err != nil {
			t.Fatalf("seed session: %v", err)
		}
		_, errOut, code := runCLI(t, "send", "hi", "-u", ss.URL(), "--last", "--task-id", "t-explicit", "-o", "json")
		if code != 0 {
			t.Fatalf("exit = %d, want 0\nstderr: %s", code, errOut)
		}
		recs := ss.records()
		if len(recs) != 1 || recs[0].taskID != "t-explicit" {
			t.Errorf("explicit --task-id must override stored latest taskId, got %+v", recs)
		}
	})
}

// CO-3: --continue/--last with NO stored session is a USAGE error (exit 2), so a
// bare resume never silently starts a fresh conversation. Exit-code choice is
// flagged for the reviewer in the dev log.
func TestSend_Resume_NoSession_Usage(t *testing.T) {
	t.Run("continue_text", func(t *testing.T) {
		cleanConfigDir(t)
		out, errOut, code := runCLI(t, "send", "hi", "--continue")
		if code != 2 {
			t.Fatalf("exit = %d, want 2 (no session to resume is a usage error)\nstderr: %s", code, errOut)
		}
		if !strings.Contains(errOut, "USAGE") {
			t.Errorf("expected a USAGE diagnostic on stderr, got %q", errOut)
		}
		if out != "" {
			t.Errorf("stdout should be empty in text-mode usage error, got %q", out)
		}
	})

	t.Run("last_json", func(t *testing.T) {
		cleanConfigDir(t)
		out, _, code := runCLI(t, "send", "hi", "--last", "-o", "json")
		if code != 2 {
			t.Fatalf("exit = %d, want 2", code)
		}
		if !strings.Contains(out, `"code": "USAGE"`) {
			t.Errorf("expected the Appendix B USAGE envelope on stdout, got %q", out)
		}
	})
}

// §6.3 consistency: a send that yields a Task prints a copy-pasteable resume
// command in text mode, exactly as `get` does (deliverable 4).
func TestSend_TextMode_PrintsResumeHint(t *testing.T) {
	cleanConfigDir(t)
	ss := newSendServer(t, func(sendRecord) (int, any) {
		return 200, taskResultBody("t-77", "ctx-1", "TASK_STATE_COMPLETED")
	})

	out, errOut, code := runCLI(t, "send", "hi", "-u", ss.URL())
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, errOut)
	}
	if !strings.Contains(out, "Resume:") || !strings.Contains(out, "t-77") {
		t.Errorf("send text output should print a resume command carrying the taskId, got:\n%s", out)
	}
}
