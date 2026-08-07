// Copyright 2026 The a2a-cli Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package poll

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ghchinoy/a2a-cli/internal/clierr"
	"github.com/ghchinoy/a2a-cli/internal/envelope"
)

func strptr(s string) *string { return &s }

func TestWait_ImmediateTerminal_NoPolling(t *testing.T) {
	called := false
	get := func(context.Context) (*envelope.TaskResult, error) {
		called = true
		return nil, errors.New("should not be called")
	}
	initial := &envelope.TaskResult{TaskID: strptr("t1"), State: envelope.StateCompleted}
	tr, err := Wait(context.Background(), initial, get, Options{Interval: time.Millisecond})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Error("get should not be called for an already-terminal task")
	}
	if tr.State != envelope.StateCompleted {
		t.Errorf("state = %q", tr.State)
	}
}

func TestWait_ImmediateInterrupted_NoPolling(t *testing.T) {
	get := func(context.Context) (*envelope.TaskResult, error) {
		t.Fatal("get should not be called")
		return nil, nil
	}
	initial := &envelope.TaskResult{TaskID: strptr("t1"), State: envelope.StateInputRequired}
	tr, err := Wait(context.Background(), initial, get, Options{Interval: time.Millisecond})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !envelope.IsInterrupted(tr.State) {
		t.Errorf("expected interrupted state, got %q", tr.State)
	}
}

func TestWait_PollsToTerminal(t *testing.T) {
	states := []string{envelope.StateWorking, envelope.StateWorking, envelope.StateCompleted}
	i := 0
	get := func(context.Context) (*envelope.TaskResult, error) {
		s := states[i]
		if i < len(states)-1 {
			i++
		}
		return &envelope.TaskResult{TaskID: strptr("t1"), State: s}, nil
	}
	initial := &envelope.TaskResult{TaskID: strptr("t1"), State: envelope.StateSubmitted}
	tr, err := Wait(context.Background(), initial, get, Options{Interval: time.Millisecond})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tr.State != envelope.StateCompleted {
		t.Errorf("final state = %q, want completed", tr.State)
	}
}

func TestWait_TimeoutPreservesTaskID(t *testing.T) {
	// Task never becomes terminal; timeout must fire and the taskId survives.
	get := func(context.Context) (*envelope.TaskResult, error) {
		return &envelope.TaskResult{TaskID: strptr("t1"), State: envelope.StateWorking}, nil
	}
	initial := &envelope.TaskResult{TaskID: strptr("t1"), State: envelope.StateSubmitted}
	tr, err := Wait(context.Background(), initial, get, Options{Interval: 5 * time.Millisecond, Timeout: 20 * time.Millisecond})

	if err == nil {
		t.Fatal("expected a timeout error")
	}
	if clierr.ExitCode(err) != 7 {
		t.Errorf("timeout exit code = %d, want 7", clierr.ExitCode(err))
	}
	if tr == nil || tr.TaskID == nil || *tr.TaskID != "t1" {
		t.Errorf("taskId must be preserved on timeout, got %+v", tr)
	}
}

func TestWait_ContextCancel_PreservesTaskID(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	get := func(context.Context) (*envelope.TaskResult, error) {
		return &envelope.TaskResult{TaskID: strptr("t1"), State: envelope.StateWorking}, nil
	}
	initial := &envelope.TaskResult{TaskID: strptr("t1"), State: envelope.StateSubmitted}

	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()
	tr, err := Wait(ctx, initial, get, Options{Interval: 5 * time.Millisecond})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
	if tr == nil || tr.TaskID == nil || *tr.TaskID != "t1" {
		t.Errorf("taskId must be preserved on cancel, got %+v", tr)
	}
}

// E. A context already canceled before Wait is entered (the SIGINT-before-first-
// poll edge) must still return the known task with its taskId intact, never lost
// (§7.3). Complements TestWait_ContextCancel_PreservesTaskID (mid-poll cancel).
func TestWait_PreCanceledContext_PreservesTaskID(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // canceled before Wait runs

	get := func(context.Context) (*envelope.TaskResult, error) {
		t.Fatal("get must not be called once the context is already canceled")
		return nil, nil
	}
	initial := &envelope.TaskResult{TaskID: strptr("t1"), State: envelope.StateWorking}

	// A long interval guarantees the ticker is never ready, so the already-ready
	// ctx.Done() is selected deterministically (no race with a poll tick).
	tr, err := Wait(ctx, initial, get, Options{Interval: time.Hour})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("expected context.Canceled, got %v", err)
	}
	if tr == nil || tr.TaskID == nil || *tr.TaskID != "t1" {
		t.Errorf("taskId must be preserved on a pre-canceled context, got %+v", tr)
	}
}

func TestWait_GetErrorPropagates(t *testing.T) {
	sentinel := errors.New("boom")
	get := func(context.Context) (*envelope.TaskResult, error) { return nil, sentinel }
	initial := &envelope.TaskResult{TaskID: strptr("t1"), State: envelope.StateSubmitted}
	tr, err := Wait(context.Background(), initial, get, Options{Interval: time.Millisecond})
	if !errors.Is(err, sentinel) {
		t.Errorf("expected sentinel error, got %v", err)
	}
	if tr == nil || tr.TaskID == nil {
		t.Error("taskId must be preserved on get error")
	}
}
