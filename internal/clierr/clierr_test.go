// Copyright 2026 The a2a-cli Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package clierr

import (
	"errors"
	"fmt"
	"testing"

	"github.com/ghchinoy/a2a-cli/internal/envelope"
)

func TestExitCode_AllKinds(t *testing.T) {
	cases := []struct {
		kind Kind
		want int
	}{
		{KindSuccess, 0},
		{KindGeneric, 1},
		{KindUsage, 2},
		{KindUnreachable, 3},
		{KindAuth, 4},
		{KindTaskFailed, 5},
		{KindInputRequired, 6},
		{KindTimeout, 7},
	}
	for _, tc := range cases {
		t.Run(string(tc.kind), func(t *testing.T) {
			if got := ExitCode(New(tc.kind, "x")); got != tc.want {
				t.Errorf("ExitCode(%s) = %d, want %d", tc.kind, got, tc.want)
			}
		})
	}
}

func TestExitCode_NilAndUnknown(t *testing.T) {
	if got := ExitCode(nil); got != 0 {
		t.Errorf("ExitCode(nil) = %d, want 0", got)
	}
	if got := ExitCode(errors.New("plain")); got != 1 {
		t.Errorf("ExitCode(plain) = %d, want 1", got)
	}
}

// TestNotFound_EnvelopeCodeAndExit locks CO-2's dual contract: KindNotFound is a
// firm normalized envelope code ("NOT_FOUND") but has no dedicated §3.5 numeric
// slot, so ExitCode falls to the GENERIC default (1). Phase 6 finalizes the
// cross-binding numeric mapping.
func TestNotFound_EnvelopeCodeAndExit(t *testing.T) {
	e := New(KindNotFound, "task not found")
	if got := e.ToEnvelope().Code; got != "NOT_FOUND" {
		t.Errorf("ToEnvelope().Code = %q, want NOT_FOUND", got)
	}
	if got := ExitCode(e); got != 1 {
		t.Errorf("ExitCode(NOT_FOUND) = %d, want 1 (GENERIC default)", got)
	}
}

func TestExitCode_WrappedError(t *testing.T) {
	wrapped := fmt.Errorf("context: %w", New(KindTimeout, "boom"))
	if got := ExitCode(wrapped); got != 7 {
		t.Errorf("ExitCode(wrapped timeout) = %d, want 7", got)
	}
}

func TestFromState(t *testing.T) {
	cases := []struct {
		state    string
		wantNil  bool
		wantKind Kind
	}{
		{envelope.StateCompleted, true, ""},
		{envelope.StateFailed, false, KindTaskFailed},
		{envelope.StateRejected, false, KindTaskFailed},
		{envelope.StateCanceled, false, KindTaskFailed},
		{envelope.StateInputRequired, false, KindInputRequired},
		{envelope.StateAuthRequired, false, KindAuth},
		{envelope.StateWorking, true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.state, func(t *testing.T) {
			e := FromState(tc.state)
			if tc.wantNil {
				if e != nil {
					t.Errorf("FromState(%s) = %v, want nil", tc.state, e)
				}
				return
			}
			if e == nil || e.Kind != tc.wantKind {
				t.Errorf("FromState(%s) = %v, want kind %s", tc.state, e, tc.wantKind)
			}
		})
	}
}

func TestToEnvelope(t *testing.T) {
	e := &Error{Kind: KindUnreachable, Message: "no route", A2ACode: -32009}
	ce := e.ToEnvelope()
	if ce.Code != string(KindUnreachable) || ce.Message != "no route" || ce.A2ACode != -32009 {
		t.Errorf("ToEnvelope() = %+v", ce)
	}
}
