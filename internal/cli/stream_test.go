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
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ghchinoy/a2a-cli/internal/session"
)

// --- SSE test fixtures --------------------------------------------------------
//
// These tests exercise the full `send --stream` matrix (Task-first, artifacts as
// they arrive, mid-stream drop -> reconcile, INPUT_REQUIRED, NDJSON, never-hang,
// capability gate, connect-error fallback) against a raw httptest SSE server. They
// use raw JSON maps ONLY — never SDK/proto types — so the import boundary (§3.2)
// that keeps internal/cli SDK-free is respected by the tests as well.

// streamCard is a minimal JSON-RPC agent card whose streaming capability is
// configurable, pointing back at the serving host so data-plane calls stay
// same-origin (B2).
func streamCard(url string, streaming bool) map[string]any {
	return map[string]any{
		"name":               "Stream Test Agent",
		"description":        "serves SSE streams for phase-5 tests",
		"version":            "1.0.0",
		"capabilities":       map[string]any{"streaming": streaming, "pushNotifications": false},
		"defaultInputModes":  []string{"text"},
		"defaultOutputModes": []string{"text"},
		"supportedInterfaces": []map[string]any{
			{"url": url, "protocolBinding": "JSONRPC", "protocolVersion": "1.0"},
		},
		"skills": []map[string]any{
			{"id": "echo", "name": "Echo", "description": "echoes", "tags": []string{"t"}},
		},
	}
}

// --- StreamResponse event builders (the SSE `result` payload) ------------------

func taskEvent(id, ctx, state string, artifacts ...map[string]any) map[string]any {
	task := taskJSON(id, ctx, state, artifacts...)
	return map[string]any{"task": task}
}

func statusEvent(id, ctx, state string) map[string]any {
	return map[string]any{"statusUpdate": map[string]any{
		"taskId":    id,
		"contextId": ctx,
		"status":    map[string]any{"state": state},
	}}
}

func artifactEvent(id, ctx, name, text string) map[string]any {
	return map[string]any{"artifactUpdate": map[string]any{
		"taskId":    id,
		"contextId": ctx,
		"artifact": map[string]any{
			"artifactId": "art-" + name,
			"name":       name,
			"parts":      []map[string]any{{"text": text}},
		},
	}}
}

func messageEvent(text string) map[string]any {
	return map[string]any{"message": map[string]any{
		"messageId": "m1",
		"role":      "agent",
		"parts":     []map[string]any{{"text": text}},
	}}
}

// taskJSON is the raw a2a.Task wire shape used both inside a task event and as the
// GetTask (reconcile) result.
func taskJSON(id, ctx, state string, artifacts ...map[string]any) map[string]any {
	t := map[string]any{
		"id":        id,
		"contextId": ctx,
		"status":    map[string]any{"state": state},
	}
	if len(artifacts) > 0 {
		t["artifacts"] = artifacts
	}
	return t
}

func artifactJSON(name, text string) map[string]any {
	return map[string]any{
		"artifactId": "art-" + name,
		"name":       name,
		"parts":      []map[string]any{{"text": text}},
	}
}

// streamConfig configures the SSE test server's data plane.
type streamConfig struct {
	streaming    bool             // card advertises streaming
	events       []map[string]any // StreamResponse objects emitted as SSE data blocks
	streamStatus int              // non-zero -> return this HTTP status on the stream (connect error)
	streamHang   bool             // block the stream endpoint until the request context is done
	emitThenHang bool             // emit events, then block until the request context is done
	emitted      chan struct{}    // closed after events are flushed (with emitThenHang)
	getStatus    int              // non-zero -> return this HTTP status on GetTask (reconcile failure)
	getTask      map[string]any   // raw Task returned for GetTask (reconcile)
	sendResult   map[string]any   // StreamResponse returned for the blocking SendMessage fallback
	getCalls     *int32           // incremented on each GetTask
	sendCalls    *int32           // incremented on each (blocking) SendMessage
	gotContextID *string          // captured message.contextId from the stream request
	gotTaskID    *string          // captured message.taskId from the stream request
}

// newStreamServer builds an httptest server that serves the card and a JSON-RPC
// data plane routed by method: SendStreamingMessage -> SSE, GetTask -> a task,
// SendMessage -> a blocking result.
func newStreamServer(t *testing.T, cfg streamConfig) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == wellKnownCardPath {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(streamCard("http://"+r.Host, cfg.streaming))
			return
		}

		var req struct {
			ID     string `json:"id"`
			Method string `json:"method"`
			Params struct {
				Message struct {
					ContextID string `json:"contextId"`
					TaskID    string `json:"taskId"`
				} `json:"message"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)

		switch req.Method {
		case "SendStreamingMessage":
			if cfg.gotContextID != nil {
				*cfg.gotContextID = req.Params.Message.ContextID
			}
			if cfg.gotTaskID != nil {
				*cfg.gotTaskID = req.Params.Message.TaskID
			}
			serveStream(t, w, r, req.ID, cfg)
		case "GetTask":
			if cfg.getCalls != nil {
				atomic.AddInt32(cfg.getCalls, 1)
			}
			if cfg.getStatus != 0 {
				w.WriteHeader(cfg.getStatus)
				return
			}
			writeRPCResult(w, req.ID, cfg.getTask)
		case "SendMessage":
			if cfg.sendCalls != nil {
				atomic.AddInt32(cfg.sendCalls, 1)
			}
			writeRPCResult(w, req.ID, cfg.sendResult)
		default:
			writeRPCResult(w, req.ID, map[string]any{})
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// serveStream emits the configured SSE events, honoring the connect-error and
// stall knobs.
func serveStream(t *testing.T, w http.ResponseWriter, r *http.Request, id string, cfg streamConfig) {
	t.Helper()
	if cfg.streamStatus != 0 {
		w.WriteHeader(cfg.streamStatus)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		t.Fatal("test server does not support flushing")
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	if cfg.streamHang {
		// Never send an event; block until the client's context deadline cancels the
		// request. This is the "silent/stalled stream" the client must bound.
		<-r.Context().Done()
		return
	}

	for _, ev := range cfg.events {
		payload, err := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": id, "result": ev})
		if err != nil {
			t.Fatalf("marshal event: %v", err)
		}
		fmt.Fprintf(w, "data: %s\n\n", payload)
		flusher.Flush()
	}

	if cfg.emitThenHang {
		// Events are flushed; signal the test and then block until the client's context
		// is canceled. This lets a test drive a deterministic mid-stream cancel AFTER a
		// taskId has been surfaced (the client is parked reading the next event).
		if cfg.emitted != nil {
			close(cfg.emitted)
		}
		<-r.Context().Done()
	}
}

func writeRPCResult(w http.ResponseWriter, id string, result map[string]any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result})
}

// --- tests --------------------------------------------------------------------

// Task-first: the first streamed event is the Task and it drives the render — the
// first stdout line in text mode is the Task line (spec §7.2).
func TestStream_TaskFirst_DrivesRender(t *testing.T) {
	cleanConfigDir(t)
	srv := newStreamServer(t, streamConfig{
		streaming: true,
		events: []map[string]any{
			taskEvent("t1", "c1", "TASK_STATE_WORKING"),
			statusEvent("t1", "c1", "TASK_STATE_COMPLETED"),
		},
		getTask: taskJSON("t1", "c1", "TASK_STATE_COMPLETED"),
	})

	out, errOut, code := runCLI(t, "send", "--stream", "-u", srv.URL, "--transport", "jsonrpc", "hi")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, errOut)
	}
	firstLine := strings.SplitN(strings.TrimSpace(out), "\n", 2)[0]
	if !strings.HasPrefix(firstLine, "Task:") {
		t.Errorf("first rendered line should be the Task (task-first), got %q\nfull:\n%s", firstLine, out)
	}
	if !strings.Contains(out, "t1") {
		t.Errorf("stdout should carry the streamed taskId, got %q", out)
	}
}

// Artifacts render AS THEY ARRIVE: the reconcile GetTask returns NO artifacts, so
// the streamed artifact text can only appear on stdout if it was rendered from the
// stream event as it arrived (spec §8.2).
func TestStream_ArtifactsRenderAsTheyArrive(t *testing.T) {
	cleanConfigDir(t)
	const marker = "STREAMED-ARTIFACT-TEXT"
	srv := newStreamServer(t, streamConfig{
		streaming: true,
		events: []map[string]any{
			taskEvent("t1", "c1", "TASK_STATE_WORKING"),
			artifactEvent("t1", "c1", "result", marker),
			statusEvent("t1", "c1", "TASK_STATE_COMPLETED"),
		},
		// Reconciled task deliberately omits the artifact.
		getTask: taskJSON("t1", "c1", "TASK_STATE_COMPLETED"),
	})

	out, errOut, code := runCLI(t, "send", "--stream", "-u", srv.URL, "--transport", "jsonrpc", "hi")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, errOut)
	}
	if !strings.Contains(out, marker) {
		t.Errorf("streamed artifact text must be rendered as it arrives, got %q", out)
	}
}

// Mid-stream truncation: the stream ends before a terminal state; the CLI must
// reconcile the authoritative final state with a GetTask, and the REPORTED state
// must come from the reconcile get, not the truncated stream (spec §7.3).
func TestStream_TruncatedThenReconciledByGet(t *testing.T) {
	cleanConfigDir(t)
	var getCalls int32
	srv := newStreamServer(t, streamConfig{
		streaming: true,
		// Only a WORKING task, then EOF — never terminal on the stream.
		events:   []map[string]any{taskEvent("t1", "c1", "TASK_STATE_WORKING")},
		getTask:  taskJSON("t1", "c1", "TASK_STATE_COMPLETED"),
		getCalls: &getCalls,
	})

	out, errOut, code := runCLI(t, "send", "--stream", "-o", "json", "-u", srv.URL, "--transport", "jsonrpc", "hi")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, errOut)
	}
	if atomic.LoadInt32(&getCalls) == 0 {
		t.Error("expected a reconcile GetTask after the truncated stream, got none")
	}
	final := lastNDJSON(t, out)
	if final["type"] != "final" {
		t.Errorf("last NDJSON record type = %v, want final", final["type"])
	}
	if final["state"] != "TASK_STATE_COMPLETED" {
		t.Errorf("reported state = %v, want the reconciled COMPLETED (not the truncated WORKING)", final["state"])
	}
}

// Capability gate: a card WITHOUT streaming must not attempt a stream — it falls
// back to the blocking send+poll path and does not hang (spec §11.3).
func TestStream_NoCapability_FallsBackToBlocking(t *testing.T) {
	cleanConfigDir(t)
	var sendCalls int32
	srv := newStreamServer(t, streamConfig{
		streaming:  false, // card does NOT advertise streaming
		sendResult: map[string]any{"task": taskJSON("t1", "c1", "TASK_STATE_COMPLETED")},
		getTask:    taskJSON("t1", "c1", "TASK_STATE_COMPLETED"),
		sendCalls:  &sendCalls,
	})

	done := make(chan struct{})
	var out, errOut string
	var code int
	go func() {
		out, errOut, code = runCLI(t, "send", "--stream", "--timeout", "5s", "-u", srv.URL, "--transport", "jsonrpc", "hi")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("send --stream against a non-streaming card hung")
	}

	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, errOut)
	}
	if atomic.LoadInt32(&sendCalls) == 0 {
		t.Error("expected a blocking SendMessage fallback, got none (a stream may have been attempted)")
	}
	if !strings.Contains(errOut, "does not advertise streaming") {
		t.Errorf("expected a stderr diagnostic about the missing capability, got %q", errOut)
	}
	if !strings.Contains(out, "TASK_STATE_COMPLETED") {
		t.Errorf("blocking fallback should render the completed task, got %q", out)
	}
}

// INPUT_REQUIRED mid-stream: stop, report taskId/state with the resume hint, exit 6,
// and do not deadlock (spec §8.2). Rendered as a single clean task view in text mode.
func TestStream_InputRequired_Exit6WithResumeHint(t *testing.T) {
	cleanConfigDir(t)
	srv := newStreamServer(t, streamConfig{
		streaming: true,
		events: []map[string]any{
			taskEvent("t1", "c1", "TASK_STATE_WORKING"),
			statusEvent("t1", "c1", "TASK_STATE_INPUT_REQUIRED"),
		},
		getTask: taskJSON("t1", "c1", "TASK_STATE_INPUT_REQUIRED"),
	})

	out, errOut, code := runCLI(t, "send", "--stream", "-u", srv.URL, "--transport", "jsonrpc", "hi")
	if code != 6 {
		t.Fatalf("exit = %d, want 6 (INPUT_REQUIRED)\nstderr: %s", code, errOut)
	}
	if !strings.Contains(errOut, "--task-id t1") {
		t.Errorf("expected a resume hint carrying the taskId on stderr, got %q", errOut)
	}
	if !strings.Contains(out, "TASK_STATE_INPUT_REQUIRED") {
		t.Errorf("stdout should report the INPUT_REQUIRED state, got %q", out)
	}
}

// NDJSON discipline: `-o json --stream` emits one JSON object per line, each with a
// `type` field; the terminal object carries the Appendix B task-op fields; stdout
// stays valid NDJSON with NO stderr bleed onto stdout (spec §9.1, Appendix B).
func TestStream_NDJSON_OneObjectPerLine(t *testing.T) {
	cleanConfigDir(t)
	srv := newStreamServer(t, streamConfig{
		streaming: true,
		events: []map[string]any{
			taskEvent("t1", "c1", "TASK_STATE_WORKING"),
			artifactEvent("t1", "c1", "result", "art text"),
			statusEvent("t1", "c1", "TASK_STATE_COMPLETED"),
		},
		getTask: taskJSON("t1", "c1", "TASK_STATE_COMPLETED", artifactJSON("result", "art text")),
	})

	out, _, code := runCLI(t, "send", "--stream", "-o", "json", "-u", srv.URL, "--transport", "jsonrpc", "hi")
	if code != 0 {
		t.Fatalf("exit = %d, want 0", code)
	}
	lines := nonEmptyLines(out)
	if len(lines) < 2 {
		t.Fatalf("expected multiple NDJSON records, got %d:\n%s", len(lines), out)
	}
	for i, line := range lines {
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Fatalf("line %d is not a valid JSON object: %v\n%q", i, err, line)
		}
		if _, ok := obj["type"]; !ok {
			t.Errorf("line %d missing a type field: %q", i, line)
		}
	}
	final := lastNDJSON(t, out)
	if final["type"] != "final" {
		t.Errorf("terminal record type = %v, want final", final["type"])
	}
	if final["taskId"] != "t1" || final["state"] != "TASK_STATE_COMPLETED" {
		t.Errorf("terminal record missing Appendix B task-op fields: %v", final)
	}
}

// Never-hang: a silent/stalled stream must be bounded by --timeout and either exit
// non-zero or fall back cleanly — it must NEVER block. Here the blocking-send
// fallback answers, so the run completes cleanly and fast.
func TestStream_StalledStream_BoundedNeverHangs(t *testing.T) {
	cleanConfigDir(t)
	srv := newStreamServer(t, streamConfig{
		streaming:  true,
		streamHang: true, // the stream endpoint never emits an event
		sendResult: map[string]any{"task": taskJSON("t1", "c1", "TASK_STATE_COMPLETED")},
		getTask:    taskJSON("t1", "c1", "TASK_STATE_COMPLETED"),
	})

	done := make(chan struct{})
	var code int
	var errOut string
	go func() {
		_, errOut, code = runCLI(t, "send", "--stream", "--timeout", "1s", "-u", srv.URL, "--transport", "jsonrpc", "hi")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("stalled stream hung despite --timeout")
	}
	// Bounded: either a clean fallback (exit 0) or a timeout exit (7); never a hang.
	if code != 0 && code != 7 {
		t.Fatalf("exit = %d, want 0 (clean fallback) or 7 (timeout)\nstderr: %s", code, errOut)
	}
}

// Connect error: a stream that fails before any task exists (non-200 on the stream
// endpoint) falls back to the blocking send path (spec §7.3).
func TestStream_ConnectError_FallsBackToBlocking(t *testing.T) {
	cleanConfigDir(t)
	var sendCalls int32
	srv := newStreamServer(t, streamConfig{
		streaming:    true,
		streamStatus: http.StatusInternalServerError, // stream refused
		sendResult:   map[string]any{"task": taskJSON("t1", "c1", "TASK_STATE_COMPLETED")},
		getTask:      taskJSON("t1", "c1", "TASK_STATE_COMPLETED"),
		sendCalls:    &sendCalls,
	})

	out, errOut, code := runCLI(t, "send", "--stream", "--timeout", "5s", "-u", srv.URL, "--transport", "jsonrpc", "hi")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (clean blocking fallback)\nstderr: %s", code, errOut)
	}
	if atomic.LoadInt32(&sendCalls) == 0 {
		t.Error("expected a blocking SendMessage fallback after the stream connect error, got none")
	}
	if !strings.Contains(out, "TASK_STATE_COMPLETED") {
		t.Errorf("fallback should render the completed task, got %q", out)
	}
}

// Regression: the hello-world shape — a stream that yields only a bare Message (no
// Task) — completes cleanly (exit 0) and renders the message; no reconcile is
// possible without a taskId, and it must not hang.
func TestStream_BareMessageOnly_Completes(t *testing.T) {
	cleanConfigDir(t)
	srv := newStreamServer(t, streamConfig{
		streaming: true,
		events:    []map[string]any{messageEvent("Hello, world!")},
	})

	out, errOut, code := runCLI(t, "send", "--stream", "-u", srv.URL, "--transport", "jsonrpc", "hi")
	if code != 0 {
		t.Fatalf("exit = %d, want 0\nstderr: %s", code, errOut)
	}
	if !strings.Contains(out, "Hello, world!") {
		t.Errorf("stdout should render the streamed message, got %q", out)
	}
}

// G2(a) — reconcile GetTask FAILS after a TERMINAL streamed snapshot. The stream
// reaches COMPLETED, so the reconcile failure is non-fatal: the CLI reports the
// streamed terminal state (exit maps to it), with a warning on stderr (spec §7.3).
func TestStream_ReconcileGetFails_TerminalSnapshot_ReportsStreamedState(t *testing.T) {
	cleanConfigDir(t)
	var getCalls int32
	srv := newStreamServer(t, streamConfig{
		streaming: true,
		events: []map[string]any{
			taskEvent("t1", "c1", "TASK_STATE_WORKING"),
			statusEvent("t1", "c1", "TASK_STATE_COMPLETED"),
		},
		getStatus: http.StatusInternalServerError, // reconcile get fails
		getCalls:  &getCalls,
	})

	out, errOut, code := runCLI(t, "send", "--stream", "-u", srv.URL, "--transport", "jsonrpc", "hi")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (terminal streamed state reported despite reconcile failure)\nstderr: %s", code, errOut)
	}
	if atomic.LoadInt32(&getCalls) == 0 {
		t.Error("expected a reconcile GetTask attempt, got none")
	}
	if !strings.Contains(out, "TASK_STATE_COMPLETED") {
		t.Errorf("should report the streamed terminal state, got %q", out)
	}
	if !strings.Contains(errOut, "could not reconcile") {
		t.Errorf("expected a stderr warning about the failed reconcile, got %q", errOut)
	}
}

// G2(b) — reconcile GetTask FAILS after a NON-TERMINAL (WORKING) streamed snapshot.
// The stream ends before terminal, so the reconcile failure is fatal: the CLI cannot
// report an authoritative final state. It surfaces the error with a resume hint and a
// non-zero exit (spec §7.3).
func TestStream_ReconcileGetFails_NonTerminalSnapshot_ErrorsWithResumeHint(t *testing.T) {
	cleanConfigDir(t)
	srv := newStreamServer(t, streamConfig{
		streaming: true,
		events:    []map[string]any{taskEvent("t1", "c1", "TASK_STATE_WORKING")}, // never terminal
		getStatus: http.StatusInternalServerError,
	})

	_, errOut, code := runCLI(t, "send", "--stream", "-u", srv.URL, "--transport", "jsonrpc", "hi")
	if code == 0 {
		t.Fatalf("exit = %d, want non-zero (reconcile failed on a non-terminal task)\nstderr: %s", code, errOut)
	}
	if !strings.Contains(errOut, "--task-id t1") {
		t.Errorf("expected a resume hint carrying the taskId on stderr, got %q", errOut)
	}
}

// G3 — the typed NDJSON error record (R2-1). In `-o json --stream`, a terminal error
// (here a reconcile failure on a non-terminal snapshot) must be emitted as a TYPED
// NDJSON record carrying `type` plus the Appendix B error fields, so stdout stays
// valid one-object-per-line NDJSON with every object carrying a `type` (spec §9.1).
func TestStream_NDJSONError_TypedRecord(t *testing.T) {
	cleanConfigDir(t)
	srv := newStreamServer(t, streamConfig{
		streaming: true,
		events:    []map[string]any{taskEvent("t1", "c1", "TASK_STATE_WORKING")}, // non-terminal
		getStatus: http.StatusInternalServerError,                                // reconcile fails
	})

	out, _, code := runCLI(t, "send", "--stream", "-o", "json", "-u", srv.URL, "--transport", "jsonrpc", "hi")
	if code == 0 {
		t.Fatalf("exit = %d, want non-zero", code)
	}
	lines := nonEmptyLines(out)
	if len(lines) < 2 {
		t.Fatalf("expected at least the task line and the error line, got %d:\n%s", len(lines), out)
	}
	// Every stdout object must be valid JSON and carry a `type` — including the error.
	for i, line := range lines {
		var obj map[string]any
		if err := json.Unmarshal([]byte(line), &obj); err != nil {
			t.Fatalf("line %d is not a valid JSON object: %v\n%q", i, err, line)
		}
		if _, ok := obj["type"]; !ok {
			t.Errorf("line %d missing a type field: %q", i, line)
		}
	}
	last := lastNDJSON(t, out)
	if last["type"] != "error" {
		t.Fatalf("terminal record type = %v, want error", last["type"])
	}
	if _, ok := last["code"]; !ok {
		t.Errorf("error record missing the Appendix B code field: %v", last)
	}
	if _, ok := last["message"]; !ok {
		t.Errorf("error record missing the Appendix B message field: %v", last)
	}
	if last["taskId"] != "t1" {
		t.Errorf("error record should carry the known taskId, got %v", last["taskId"])
	}
}

// G4 — --continue over --stream forwards the stored contextId on the STREAM request
// (Phase-4 continuation not regressed through streaming, spec §6.2). --last also
// forwards the stored latestTaskId.
func TestStream_ContinueAndLast_ForwardStoredIdsOnStreamRequest(t *testing.T) {
	t.Run("continue forwards contextId", func(t *testing.T) {
		cleanConfigDir(t)
		var gotCtx, gotTask string
		srv := newStreamServer(t, streamConfig{
			streaming:    true,
			events:       []map[string]any{taskEvent("t2", "ctx-stored", "TASK_STATE_COMPLETED")},
			getTask:      taskJSON("t2", "ctx-stored", "TASK_STATE_COMPLETED"),
			gotContextID: &gotCtx,
			gotTaskID:    &gotTask,
		})
		if err := session.Save(&session.Session{ContextID: "ctx-stored", LatestTaskID: "task-stored", ServiceURL: srv.URL, Transport: "jsonrpc"}); err != nil {
			t.Fatalf("seed session: %v", err)
		}

		_, errOut, code := runCLI(t, "send", "--stream", "--continue", "-u", srv.URL, "--transport", "jsonrpc", "hi")
		if code != 0 {
			t.Fatalf("exit = %d, want 0\nstderr: %s", code, errOut)
		}
		if gotCtx != "ctx-stored" {
			t.Errorf("stream request contextId = %q, want the stored ctx-stored", gotCtx)
		}
		if gotTask != "" {
			t.Errorf("--continue must NOT forward the stored taskId, got %q", gotTask)
		}
	})

	t.Run("last also forwards taskId", func(t *testing.T) {
		cleanConfigDir(t)
		var gotCtx, gotTask string
		srv := newStreamServer(t, streamConfig{
			streaming:    true,
			events:       []map[string]any{taskEvent("task-stored", "ctx-stored", "TASK_STATE_COMPLETED")},
			getTask:      taskJSON("task-stored", "ctx-stored", "TASK_STATE_COMPLETED"),
			gotContextID: &gotCtx,
			gotTaskID:    &gotTask,
		})
		if err := session.Save(&session.Session{ContextID: "ctx-stored", LatestTaskID: "task-stored", ServiceURL: srv.URL, Transport: "jsonrpc"}); err != nil {
			t.Fatalf("seed session: %v", err)
		}

		_, errOut, code := runCLI(t, "send", "--stream", "--last", "-u", srv.URL, "--transport", "jsonrpc", "hi")
		if code != 0 {
			t.Fatalf("exit = %d, want 0\nstderr: %s", code, errOut)
		}
		if gotCtx != "ctx-stored" {
			t.Errorf("stream request contextId = %q, want ctx-stored", gotCtx)
		}
		if gotTask != "task-stored" {
			t.Errorf("--last must forward the stored taskId, got %q", gotTask)
		}
	})
}

// G7 — an empty stream (HTTP 200 then immediate EOF, no data) is the "no events"
// case: it must fall back to the blocking send path, NOT be surfaced as a timeout
// (spec §7.3 never hang / degrade safely).
func TestStream_EmptyStream_FallsBackNotTimeout(t *testing.T) {
	cleanConfigDir(t)
	var sendCalls int32
	srv := newStreamServer(t, streamConfig{
		streaming:  true,
		events:     nil, // 200 then EOF, no SSE data blocks
		sendResult: map[string]any{"task": taskJSON("t1", "c1", "TASK_STATE_COMPLETED")},
		getTask:    taskJSON("t1", "c1", "TASK_STATE_COMPLETED"),
		sendCalls:  &sendCalls,
	})

	out, errOut, code := runCLI(t, "send", "--stream", "--timeout", "5s", "-u", srv.URL, "--transport", "jsonrpc", "hi")
	if code == 7 {
		t.Fatalf("an empty stream must not be surfaced as a timeout (exit 7)\nstderr: %s", errOut)
	}
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (clean blocking fallback)\nstderr: %s", code, errOut)
	}
	if atomic.LoadInt32(&sendCalls) == 0 {
		t.Error("expected a blocking SendMessage fallback after the empty stream, got none")
	}
	if !strings.Contains(out, "TASK_STATE_COMPLETED") {
		t.Errorf("fallback should render the completed task, got %q", out)
	}
}

// G8 — a non-Task FIRST event (Task-first is the server's obligation, §7.2). The
// client must tolerate the violation gracefully: no crash, and it still reconciles
// the authoritative final state via get.
func TestStream_NonTaskFirstEvent_ToleratedAndReconciles(t *testing.T) {
	cleanConfigDir(t)
	var getCalls int32
	srv := newStreamServer(t, streamConfig{
		streaming: true,
		events: []map[string]any{
			statusEvent("t1", "c1", "TASK_STATE_WORKING"), // status arrives BEFORE any Task
			statusEvent("t1", "c1", "TASK_STATE_COMPLETED"),
		},
		getTask:  taskJSON("t1", "c1", "TASK_STATE_COMPLETED"),
		getCalls: &getCalls,
	})

	out, errOut, code := runCLI(t, "send", "--stream", "-u", srv.URL, "--transport", "jsonrpc", "hi")
	if code != 0 {
		t.Fatalf("exit = %d, want 0 (must tolerate a non-Task first event)\nstderr: %s", code, errOut)
	}
	if atomic.LoadInt32(&getCalls) == 0 {
		t.Error("expected a reconcile GetTask even when the first event was not a Task, got none")
	}
	if !strings.Contains(out, "TASK_STATE_COMPLETED") {
		t.Errorf("should report the reconciled COMPLETED state, got %q", out)
	}
}

// --- helpers ------------------------------------------------------------------

func nonEmptyLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

func lastNDJSON(t *testing.T, s string) map[string]any {
	t.Helper()
	lines := nonEmptyLines(s)
	if len(lines) == 0 {
		t.Fatalf("no NDJSON records in output")
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(lines[len(lines)-1]), &obj); err != nil {
		t.Fatalf("last line is not valid JSON: %v\n%q", err, lines[len(lines)-1])
	}
	return obj
}
