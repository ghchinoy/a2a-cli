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
	"sync/atomic"
	"testing"
)

// taskEndpoint drives a raw-JSON A2A task server. Handlers return an HTTP status
// and a JSON-serializable body; keeping them as Go maps means these tests never
// import SDK/proto types, preserving the import boundary (design §3.2).
type taskEndpoint struct {
	getFn    func(id, historyLength string) (int, any)
	cancelFn func(id string) (int, any)
}

// newTaskServer serves the well-known card (whose single HTTP+JSON interface
// points back at the server itself, so data-plane calls land here and pass the B2
// same-origin check) plus GET /tasks/{id} and POST /tasks/{id}:cancel.
func newTaskServer(t *testing.T, ep taskEndpoint) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == wellKnownCardPath {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(taskCardJSON("http://" + r.Host))
			return
		}
		if strings.HasPrefix(r.URL.Path, "/tasks/") {
			rest := strings.TrimPrefix(r.URL.Path, "/tasks/")
			if strings.HasSuffix(rest, ":cancel") {
				id := strings.TrimSuffix(rest, ":cancel")
				status, body := ep.cancelFn(id)
				writeJSONStatus(w, status, body)
				return
			}
			status, body := ep.getFn(rest, r.URL.Query().Get("historyLength"))
			writeJSONStatus(w, status, body)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func writeJSONStatus(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// taskCardJSON is a minimal HTTP+JSON agent card whose interface URL is the
// server's own base, so get/cancel data-plane requests are routed back to it.
func taskCardJSON(url string) map[string]any {
	return map[string]any{
		"name":               "Task Test Agent",
		"description":        "serves tasks for get/cancel tests",
		"version":            "1.0.0",
		"capabilities":       map[string]any{"streaming": false, "pushNotifications": false},
		"defaultInputModes":  []string{"text"},
		"defaultOutputModes": []string{"text"},
		"supportedInterfaces": []map[string]any{
			{"url": url, "protocolBinding": "HTTP+JSON", "protocolVersion": "1.0"},
		},
		"skills": []map[string]any{
			{"id": "echo", "name": "Echo", "description": "echoes", "tags": []string{"test"}},
		},
	}
}

// taskDoc builds a raw-JSON a2a.Task body.
func taskDoc(id, ctxID, state string) map[string]any {
	return map[string]any{
		"id":        id,
		"contextId": ctxID,
		"status":    map[string]any{"state": state},
	}
}

// textPart / textArtifact / historyMessage build raw-JSON A2A sub-objects.
func textArtifact(id, name, text string) map[string]any {
	return map[string]any{
		"artifactId": id,
		"name":       name,
		"parts":      []map[string]any{{"text": text}},
	}
}

func historyMessage(role, text string) map[string]any {
	return map[string]any{
		"messageId": "m-" + role,
		"role":      role,
		"parts":     []map[string]any{{"text": text}},
	}
}

// notFoundBody is a google.rpc.Status error body whose ErrorInfo reason resolves
// to a2a.ErrTaskNotFound on both the JSON-RPC and REST client transports (§9.4).
func notFoundBody() map[string]any {
	return errorBody(404, "NOT_FOUND", "task not found", "TASK_NOT_FOUND")
}

func notCancelableBody() map[string]any {
	return errorBody(409, "FAILED_PRECONDITION", "task cannot be canceled", "TASK_NOT_CANCELABLE")
}

func errorBody(code int, status, message, reason string) map[string]any {
	return map[string]any{
		"error": map[string]any{
			"code":    code,
			"status":  status,
			"message": message,
			"details": []map[string]any{
				{
					"@type":  "type.googleapis.com/google.rpc.ErrorInfo",
					"reason": reason,
					"domain": "a2a-protocol.org",
				},
			},
		},
	}
}

func TestGet_OneShot_Text(t *testing.T) {
	cleanConfigDir(t)
	srv := newTaskServer(t, taskEndpoint{
		getFn: func(id, _ string) (int, any) {
			return 200, taskDoc(id, "ctx-1", "TASK_STATE_COMPLETED")
		},
	})

	out, errOut, code := runCLI(t, "get", "task-42", "-u", srv.URL)
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, errOut)
	}
	for _, want := range []string{"State:     TASK_STATE_COMPLETED", "Task ID:   task-42", "Context:   ctx-1"} {
		if !strings.Contains(out, want) {
			t.Errorf("get text output missing %q\n---\n%s", want, out)
		}
	}
}

func TestGet_OneShot_JSON_EnvelopeOnly(t *testing.T) {
	cleanConfigDir(t)
	srv := newTaskServer(t, taskEndpoint{
		getFn: func(id, _ string) (int, any) {
			return 200, taskDoc(id, "ctx-1", "TASK_STATE_COMPLETED")
		},
	})

	out, errOut, code := runCLI(t, "get", "task-42", "-u", srv.URL, "-o", "json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, errOut)
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("stdout is not a single valid JSON document: %v\n%s", err, out)
	}
	if env["taskId"] != "task-42" || env["contextId"] != "ctx-1" || env["state"] != "TASK_STATE_COMPLETED" {
		t.Errorf("envelope missing §6.3 identifiers: %v", env)
	}
	if strings.Contains(errOut, "{") {
		t.Errorf("stderr must not carry JSON, got %q", errOut)
	}
}

func TestGet_IncludeArtifacts(t *testing.T) {
	cleanConfigDir(t)
	srv := newTaskServer(t, taskEndpoint{
		getFn: func(id, _ string) (int, any) {
			doc := taskDoc(id, "ctx-1", "TASK_STATE_COMPLETED")
			doc["artifacts"] = []map[string]any{textArtifact("a1", "result", "the answer is 42")}
			return 200, doc
		},
	})

	// Default: artifacts are summarized — no part contents in json.
	out, _, code := runCLI(t, "get", "task-42", "-u", srv.URL, "-o", "json")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("invalid json: %v\n%s", err, out)
	}
	arts, _ := env["artifacts"].([]any)
	if len(arts) != 1 {
		t.Fatalf("want 1 artifact summary, got %v", env["artifacts"])
	}
	if a0, _ := arts[0].(map[string]any); a0["parts"] != nil {
		t.Errorf("default get should summarize (no parts), got %v", a0)
	}

	// --include-artifacts: full contents rendered.
	out, _, code = runCLI(t, "get", "task-42", "-u", srv.URL, "-o", "json", "--include-artifacts")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(out, "the answer is 42") {
		t.Errorf("--include-artifacts should render artifact contents, got %s", out)
	}
}

func TestGet_History_ThreadsHistoryLength(t *testing.T) {
	cleanConfigDir(t)
	var gotHistoryLen atomic.Value
	gotHistoryLen.Store("")
	srv := newTaskServer(t, taskEndpoint{
		getFn: func(id, historyLength string) (int, any) {
			gotHistoryLen.Store(historyLength)
			doc := taskDoc(id, "ctx-1", "TASK_STATE_COMPLETED")
			if historyLength != "" {
				doc["history"] = []map[string]any{
					historyMessage("ROLE_USER", "hello"),
					historyMessage("ROLE_AGENT", "hi there"),
				}
			}
			return 200, doc
		},
	})

	out, _, code := runCLI(t, "get", "task-42", "-u", srv.URL, "-o", "json", "--history", "5")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if got := gotHistoryLen.Load().(string); got != "5" {
		t.Errorf("historyLength forwarded to server = %q, want %q", got, "5")
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("invalid json: %v\n%s", err, out)
	}
	hist, _ := env["history"].([]any)
	if len(hist) != 2 {
		t.Errorf("want 2 history entries, got %v", env["history"])
	}
}

// B. --history edge values: n=0 is valid and forwarded verbatim; a negative n is
// a USAGE error (exit 2, text stderr diagnostic, empty stdout) per the EM
// decision; a large-but-parseable n is forwarded unclamped (upper-clamp is a
// Phase-6 item). Asserts the forwarded query value where the existing --history
// test does.
func TestGet_History_EdgeValues(t *testing.T) {
	t.Run("zero_forwarded", func(t *testing.T) {
		cleanConfigDir(t)
		var got atomic.Value
		got.Store("")
		srv := newTaskServer(t, taskEndpoint{
			getFn: func(id, historyLength string) (int, any) {
				got.Store(historyLength)
				return 200, taskDoc(id, "ctx-1", "TASK_STATE_COMPLETED")
			},
		})
		_, errOut, code := runCLI(t, "get", "task-42", "-u", srv.URL, "--history", "0")
		if code != 0 {
			t.Fatalf("exit = %d, want 0\nstderr: %s", code, errOut)
		}
		if v := got.Load().(string); v != "0" {
			t.Errorf("historyLength forwarded = %q, want %q (n=0 is valid and threaded)", v, "0")
		}
	})

	t.Run("negative_usage", func(t *testing.T) {
		cleanConfigDir(t)
		var reached atomic.Bool
		srv := newTaskServer(t, taskEndpoint{
			getFn: func(id, _ string) (int, any) {
				reached.Store(true)
				return 200, taskDoc(id, "ctx-1", "TASK_STATE_COMPLETED")
			},
		})
		out, errOut, code := runCLI(t, "get", "task-42", "-u", srv.URL, "--history", "-1")
		if code != 2 {
			t.Fatalf("exit = %d, want 2 (negative --history is a USAGE error)\nstderr: %s", code, errOut)
		}
		if !strings.Contains(errOut, "USAGE") {
			t.Errorf("negative --history should emit a USAGE diagnostic on stderr, got %q", errOut)
		}
		if out != "" {
			t.Errorf("stdout should be empty for a usage error, got %q", out)
		}
		if reached.Load() {
			t.Error("a negative --history must be rejected before any server call")
		}
	})

	t.Run("large_forwarded", func(t *testing.T) {
		cleanConfigDir(t)
		var got atomic.Value
		got.Store("")
		srv := newTaskServer(t, taskEndpoint{
			getFn: func(id, historyLength string) (int, any) {
				got.Store(historyLength)
				return 200, taskDoc(id, "ctx-1", "TASK_STATE_COMPLETED")
			},
		})
		_, errOut, code := runCLI(t, "get", "task-42", "-u", srv.URL, "--history", "1000000")
		if code != 0 {
			t.Fatalf("exit = %d, want 0\nstderr: %s", code, errOut)
		}
		if v := got.Load().(string); v != "1000000" {
			t.Errorf("large --history forwarded = %q, want %q (no client-side clamp)", v, "1000000")
		}
	})
}

// C. get one-shot terminal-state exit mapping (design §3.5): a FAILED task exits
// 5; an INPUT_REQUIRED task exits 6 with a resume hint on stderr. Previously only
// COMPLETED->0 was covered.
func TestGet_OneShot_TerminalExitMapping(t *testing.T) {
	t.Run("failed_exit5", func(t *testing.T) {
		cleanConfigDir(t)
		srv := newTaskServer(t, taskEndpoint{
			getFn: func(id, _ string) (int, any) {
				return 200, taskDoc(id, "ctx-1", "TASK_STATE_FAILED")
			},
		})
		out, errOut, code := runCLI(t, "get", "task-42", "-u", srv.URL)
		if code != 5 {
			t.Fatalf("exit = %d, want 5 (FAILED)\nstdout: %s\nstderr: %s", code, out, errOut)
		}
	})

	t.Run("input_required_exit6_resume_hint", func(t *testing.T) {
		cleanConfigDir(t)
		srv := newTaskServer(t, taskEndpoint{
			getFn: func(id, _ string) (int, any) {
				return 200, taskDoc(id, "ctx-1", "TASK_STATE_INPUT_REQUIRED")
			},
		})
		_, errOut, code := runCLI(t, "get", "task-42", "-u", srv.URL)
		if code != 6 {
			t.Fatalf("exit = %d, want 6 (INPUT_REQUIRED)\nstderr: %s", code, errOut)
		}
		if !strings.Contains(errOut, "task-42") {
			t.Errorf("INPUT_REQUIRED should print a resume hint carrying the taskId, got %q", errOut)
		}
	})
}

// D. get --wait stops on an interrupted state (WORKING -> INPUT_REQUIRED) as well
// as a terminal one: the loop halts, exit = 6 with the resume hint, and json
// stdout stays a single clean envelope (transitions never leak onto stdout).
func TestGet_Wait_StopsOnInterrupted(t *testing.T) {
	cleanConfigDir(t)
	var calls atomic.Int32
	srv := newTaskServer(t, taskEndpoint{
		getFn: func(id, _ string) (int, any) {
			if calls.Add(1) >= 3 {
				return 200, taskDoc(id, "ctx-1", "TASK_STATE_INPUT_REQUIRED")
			}
			return 200, taskDoc(id, "ctx-1", "TASK_STATE_WORKING")
		},
	})

	out, errOut, code := runCLI(t, "get", "task-42", "-u", srv.URL, "-o", "json", "--wait", "--poll-interval", "10ms", "--timeout", "5s")
	if code != 6 {
		t.Fatalf("exit = %d, want 6 (interrupted stops the wait)\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	if !strings.Contains(errOut, "task-42") {
		t.Errorf("interrupted --wait should surface a resume hint with the taskId, got %q", errOut)
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("--wait json stdout must stay a single valid envelope: %v\n%s", err, out)
	}
	if env["state"] != "TASK_STATE_INPUT_REQUIRED" {
		t.Errorf("final envelope state = %v, want TASK_STATE_INPUT_REQUIRED", env["state"])
	}
}

// G. --include-artifacts in text mode: by default artifacts are summarized (no
// content line), and the flag renders the full artifact contents.
func TestGet_IncludeArtifacts_TextMode(t *testing.T) {
	cleanConfigDir(t)
	srv := newTaskServer(t, taskEndpoint{
		getFn: func(id, _ string) (int, any) {
			doc := taskDoc(id, "ctx-1", "TASK_STATE_COMPLETED")
			doc["artifacts"] = []map[string]any{textArtifact("a1", "result", "the answer is 42")}
			return 200, doc
		},
	})

	// Default: summarized — the artifact content must NOT appear in text output.
	out, _, code := runCLI(t, "get", "task-42", "-u", srv.URL)
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if strings.Contains(out, "the answer is 42") {
		t.Errorf("default text get should summarize (no artifact content), got:\n%s", out)
	}
	if !strings.Contains(out, "result") {
		t.Errorf("default text get should still name the artifact, got:\n%s", out)
	}

	// --include-artifacts: full content rendered.
	out, _, code = runCLI(t, "get", "task-42", "-u", srv.URL, "--include-artifacts")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	if !strings.Contains(out, "the answer is 42") {
		t.Errorf("--include-artifacts text mode should render artifact content, got:\n%s", out)
	}
}

func TestGet_Wait_PollsToTerminal(t *testing.T) {
	cleanConfigDir(t)
	var calls atomic.Int32
	srv := newTaskServer(t, taskEndpoint{
		getFn: func(id, _ string) (int, any) {
			// First few observations are WORKING; then the task completes.
			if calls.Add(1) >= 3 {
				return 200, taskDoc(id, "ctx-1", "TASK_STATE_COMPLETED")
			}
			return 200, taskDoc(id, "ctx-1", "TASK_STATE_WORKING")
		},
	})

	out, errOut, code := runCLI(t, "get", "task-42", "-u", srv.URL, "--wait", "--poll-interval", "10ms", "--timeout", "5s")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, errOut)
	}
	if !strings.Contains(out, "TASK_STATE_COMPLETED") {
		t.Errorf("--wait should poll to terminal, final output: %s", out)
	}
}

func TestGet_Wait_TimeoutPreservesTaskID(t *testing.T) {
	cleanConfigDir(t)
	srv := newTaskServer(t, taskEndpoint{
		getFn: func(id, _ string) (int, any) {
			return 200, taskDoc(id, "ctx-1", "TASK_STATE_WORKING") // never terminal
		},
	})

	out, errOut, code := runCLI(t, "get", "task-42", "-u", srv.URL, "-o", "json", "--wait", "--poll-interval", "10ms", "--timeout", "60ms")
	if code != 7 {
		t.Fatalf("exit = %d, want 7 (timeout)\nstdout: %s\nstderr: %s", code, out, errOut)
	}
	// The taskId is the argument, so it must survive the timeout: it is echoed on
	// stderr via the resume hint, never lost (§7.3).
	if !strings.Contains(errOut, "task-42") {
		t.Errorf("timeout must preserve the taskId on stderr, got %q", errOut)
	}
}

func TestGet_Watch_ReportsTransitionsToStderr(t *testing.T) {
	cleanConfigDir(t)
	var calls atomic.Int32
	srv := newTaskServer(t, taskEndpoint{
		getFn: func(id, _ string) (int, any) {
			if calls.Add(1) >= 3 {
				return 200, taskDoc(id, "ctx-1", "TASK_STATE_COMPLETED")
			}
			return 200, taskDoc(id, "ctx-1", "TASK_STATE_WORKING")
		},
	})

	out, errOut, code := runCLI(t, "get", "task-42", "-u", srv.URL, "-o", "json", "--watch", "--poll-interval", "10ms", "--timeout", "5s")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, errOut)
	}
	// Transitions go to stderr only; json stdout must remain a single valid envelope.
	if !strings.Contains(errOut, "state:") {
		t.Errorf("--watch should report transitions on stderr, got %q", errOut)
	}
	var env map[string]any
	if err := json.Unmarshal([]byte(out), &env); err != nil {
		t.Fatalf("--watch json stdout must stay a single valid envelope: %v\n%s", err, out)
	}
}

// CO-2: get against an unknown task surfaces a NORMALIZED Appendix B error object
// with code NOT_FOUND (never a raw GENERIC, never a silent success), in both text
// and json, and exits with the GENERIC/1 default (§3.5 has no NOT_FOUND slot).
func TestGet_Missing_NotFound_JSON(t *testing.T) {
	cleanConfigDir(t)
	srv := newTaskServer(t, taskEndpoint{
		getFn: func(_, _ string) (int, any) { return 404, notFoundBody() },
	})

	out, errOut, code := runCLI(t, "get", "ghost", "-u", srv.URL, "-o", "json")
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
}

func TestGet_Missing_NotFound_Text(t *testing.T) {
	cleanConfigDir(t)
	srv := newTaskServer(t, taskEndpoint{
		getFn: func(_, _ string) (int, any) { return 404, notFoundBody() },
	})

	out, errOut, code := runCLI(t, "get", "ghost", "-u", srv.URL)
	if code != 1 {
		t.Fatalf("exit = %d, want 1\nstderr: %s", code, errOut)
	}
	if !strings.Contains(errOut, "NOT_FOUND") {
		t.Errorf("text-mode diagnostic should carry NOT_FOUND on stderr, got %q", errOut)
	}
	if out != "" {
		t.Errorf("stdout should be empty in text-mode error, got %q", out)
	}
}

func TestGet_MissingArg_Usage(t *testing.T) {
	cleanConfigDir(t)
	_, errOut, code := runCLI(t, "get", "-u", "http://x")
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (usage)", code)
	}
	if !strings.Contains(errOut, "USAGE") {
		t.Errorf("expected a USAGE diagnostic on stderr, got %q", errOut)
	}
}

func TestGet_MissingURL_Usage(t *testing.T) {
	cleanConfigDir(t)
	_, errOut, code := runCLI(t, "get", "task-1")
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (usage)", code)
	}
	if !strings.Contains(errOut, "USAGE") {
		t.Errorf("expected a USAGE diagnostic on stderr, got %q", errOut)
	}
}
