// Copyright 2026 The a2a-cli Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"

	"github.com/ghchinoy/a2a-cli/internal/clierr"
	"github.com/ghchinoy/a2a-cli/internal/envelope"
)

// nonStreamingCard advertises HTTP+JSON but NOT streaming, so the capability gate
// must refuse to attempt a stream.
func nonStreamingCard(url string) *a2a.AgentCard {
	iface := a2a.NewAgentInterface(url, a2a.TransportProtocolHTTPJSON)
	iface.ProtocolVersion = "1.0"
	return &a2a.AgentCard{
		Name:                "No-Stream Agent",
		Description:         "does not advertise streaming",
		Capabilities:        a2a.AgentCapabilities{Streaming: false},
		DefaultInputModes:   []string{"text"},
		DefaultOutputModes:  []string{"text"},
		SupportedInterfaces: []*a2a.AgentInterface{iface},
	}
}

// The capability gate (spec §11.3) must return ErrStreamingUnsupported WITHOUT
// attempting a stream when the card does not advertise streaming, so the caller can
// fall back cleanly. SupportsStreaming must agree.
func TestSendStream_CapabilityGate(t *testing.T) {
	srv := newCardServer(t, nonStreamingCard("http://example/rest"))
	cl, err := New(context.Background(), Options{ServiceURL: srv.URL})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if cl.SupportsStreaming() {
		t.Error("SupportsStreaming = true for a card without the streaming capability")
	}

	called := false
	snap, serr := cl.SendStream(context.Background(), SendRequest{Text: "hi"}, func(envelope.StreamEvent) error {
		called = true
		return nil
	})
	if !errors.Is(serr, ErrStreamingUnsupported) {
		t.Fatalf("err = %v, want ErrStreamingUnsupported", serr)
	}
	if called {
		t.Error("handler was invoked even though streaming is unsupported (a stream was attempted)")
	}
	if snap != nil {
		t.Errorf("snapshot should be nil when no stream is attempted, got %+v", snap)
	}
}

// streamingCardJSON advertises a JSON-RPC interface with streaming enabled,
// pointing back at the serving host so the data plane stays same-origin.
func streamingCardJSON(url string) map[string]any {
	return map[string]any{
		"name":               "SSE Agent",
		"description":        "streams",
		"version":            "1.0.0",
		"capabilities":       map[string]any{"streaming": true},
		"defaultInputModes":  []string{"text"},
		"defaultOutputModes": []string{"text"},
		"supportedInterfaces": []map[string]any{
			{"url": url, "protocolBinding": "JSONRPC", "protocolVersion": "1.0"},
		},
	}
}

// SendStream must deliver the Task FIRST (spec §7.2), then subsequent events in
// order, translate them to envelope types (never SDK types cross the seam), stop
// consuming at a terminal state, and return a snapshot carrying the taskId for the
// caller's reconcile.
func TestSendStream_TaskFirstThenEventsInOrder(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == a2asrv.WellKnownAgentCardPath {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(streamingCardJSON("http://" + r.Host))
			return
		}
		flusher := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()
		events := []map[string]any{
			{"task": map[string]any{"id": "t1", "contextId": "c1", "status": map[string]any{"state": "TASK_STATE_WORKING"}}},
			{"artifactUpdate": map[string]any{"taskId": "t1", "contextId": "c1", "artifact": map[string]any{"artifactId": "a1", "name": "result", "parts": []map[string]any{{"text": "hi"}}}}},
			{"statusUpdate": map[string]any{"taskId": "t1", "contextId": "c1", "status": map[string]any{"state": "TASK_STATE_COMPLETED"}}},
			// A trailing event the client MUST NOT consume (it stops at terminal).
			{"statusUpdate": map[string]any{"taskId": "t1", "contextId": "c1", "status": map[string]any{"state": "TASK_STATE_WORKING"}}},
		}
		for _, ev := range events {
			b, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": "1", "result": ev})
			fmt.Fprintf(w, "data: %s\n\n", b)
			flusher.Flush()
		}
	}))
	t.Cleanup(srv.Close)

	cl, err := New(context.Background(), Options{ServiceURL: srv.URL, Transport: "jsonrpc"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !cl.SupportsStreaming() {
		t.Fatal("SupportsStreaming = false for a streaming card")
	}

	var types []string
	snap, serr := cl.SendStream(context.Background(), SendRequest{Text: "go"}, func(ev envelope.StreamEvent) error {
		types = append(types, ev.Type)
		return nil
	})
	if serr != nil {
		t.Fatalf("SendStream: %v", serr)
	}
	want := []string{envelope.StreamTypeTask, envelope.StreamTypeArtifact, envelope.StreamTypeStatus}
	if len(types) != len(want) {
		t.Fatalf("event types = %v, want %v (must stop at terminal, not consume the trailing event)", types, want)
	}
	for i := range want {
		if types[i] != want[i] {
			t.Errorf("event %d = %q, want %q", i, types[i], want[i])
		}
	}
	if types[0] != envelope.StreamTypeTask {
		t.Errorf("first event must be the Task (§7.2), got %q", types[0])
	}
	if snap == nil || snap.TaskID == nil || *snap.TaskID != "t1" {
		t.Fatalf("snapshot must carry the taskId for reconcile, got %+v", snap)
	}
	if snap.State != envelope.StateCompleted {
		t.Errorf("snapshot state = %q, want COMPLETED", snap.State)
	}
	if len(snap.Artifacts) != 1 || snap.Artifacts[0].Name != "result" {
		t.Errorf("snapshot should accumulate the streamed artifact, got %+v", snap.Artifacts)
	}
}

// G5 — applyStreamEvent must UNION artifacts: a Task snapshot that already carries an
// artifact, followed by a TaskArtifactUpdateEvent, must accumulate to two (the update
// appends, it does not replace). This is the incremental-artifact accumulation the
// reconcile/render paths depend on (spec §8.2).
func TestApplyStreamEvent_ArtifactAppend(t *testing.T) {
	snap := &envelope.TaskResult{State: envelope.StateUnspecified}
	tid, cid := "t1", "c1"

	applyStreamEvent(snap, envelope.StreamEvent{
		Type:      envelope.StreamTypeTask,
		TaskID:    &tid,
		ContextID: &cid,
		State:     envelope.StateWorking,
		Artifacts: []envelope.Artifact{{ArtifactID: "a1", Name: "first", Parts: []envelope.Part{{Text: "one"}}}},
	})
	if len(snap.Artifacts) != 1 {
		t.Fatalf("after the Task event, artifacts = %d, want 1", len(snap.Artifacts))
	}

	applyStreamEvent(snap, envelope.StreamEvent{
		Type:      envelope.StreamTypeArtifact,
		TaskID:    &tid,
		ContextID: &cid,
		Artifact:  &envelope.Artifact{ArtifactID: "a2", Name: "second", Parts: []envelope.Part{{Text: "two"}}},
	})
	if len(snap.Artifacts) != 2 {
		t.Fatalf("a subsequent artifactUpdate must APPEND (union), got %d artifacts", len(snap.Artifacts))
	}
	if snap.Artifacts[0].Name != "first" || snap.Artifacts[1].Name != "second" {
		t.Errorf("artifact order/content not preserved: %+v", snap.Artifacts)
	}
}

// classifyStream must give context signals priority so a stalled stream bounded by
// --timeout maps to a TIMEOUT (exit 7) and a SIGINT-style cancellation propagates
// as context.Canceled (so the caller keeps the already-surfaced taskId).
func TestClassifyStream_ContextSignals(t *testing.T) {
	t.Run("deadline exceeded -> TIMEOUT", func(t *testing.T) {
		dctx, dcancel := context.WithTimeout(context.Background(), 0)
		defer dcancel()
		<-dctx.Done()
		err := classifyStream(dctx, dctx.Err(), "stream failed")
		var ce *clierr.Error
		if !errors.As(err, &ce) || ce.Kind != clierr.KindTimeout {
			t.Fatalf("err = %v, want KindTimeout", err)
		}
		if clierr.ExitCode(err) != 7 {
			t.Errorf("exit = %d, want 7 (TIMEOUT)", clierr.ExitCode(err))
		}
	})

	t.Run("canceled -> propagated", func(t *testing.T) {
		cctx, ccancel := context.WithCancel(context.Background())
		ccancel()
		err := classifyStream(cctx, cctx.Err(), "stream failed")
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	})
}
