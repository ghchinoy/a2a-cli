// Copyright 2026 The A2A Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

// Package clierr defines the normalized CLI error taxonomy and its mapping to
// process exit codes (a2a-cli spec §9.5 / design §3.5). The normalized code is
// the single source of truth for both the JSON error envelope and the exit code.
package clierr

import (
	"errors"

	"github.com/ghchinoy/a2a-cli/internal/envelope"
)

// Kind is a normalized, binding-independent error category.
type Kind string

const (
	KindSuccess       Kind = "SUCCESS"
	KindGeneric       Kind = "GENERIC"
	KindUsage         Kind = "USAGE"
	KindUnreachable   Kind = "UNREACHABLE"
	KindAuth          Kind = "AUTH_REQUIRED"
	KindTaskFailed    Kind = "TASK_FAILED"
	KindInputRequired Kind = "INPUT_REQUIRED"
	KindTimeout       Kind = "TIMEOUT"
	// KindNotFound is the normalized code for a task the server does not know
	// (CO-2). It is a firm envelope contract — `get <missing>` MUST surface
	// {code: "NOT_FOUND"} across every binding (§9.4) — but §3.5's numeric exit
	// table has NO dedicated slot for it, so KindNotFound is deliberately absent
	// from exitCodes below and ExitCode maps it to the GENERIC default (1). The
	// cross-binding numeric mapping is finalized in Phase 6; the normalized string
	// code is the stable deliverable here.
	KindNotFound Kind = "NOT_FOUND"
)

// exitCodes maps each Kind to its process exit code (spec §9.5).
var exitCodes = map[Kind]int{
	KindSuccess:       0,
	KindGeneric:       1,
	KindUsage:         2,
	KindUnreachable:   3,
	KindAuth:          4,
	KindTaskFailed:    5,
	KindInputRequired: 6,
	KindTimeout:       7,
}

// Error is a normalized CLI error carrying its Kind (which determines the exit
// code), a human message, and the underlying A2A/transport code for debugging.
type Error struct {
	Kind    Kind
	Message string
	A2ACode any
	cause   error

	// rendered records whether this error has already been surfaced to the user
	// through a render.Renderer. It lets the top-level handler (cli.Execute) emit
	// a diagnostic for errors that never reached a renderer — e.g. cobra-level
	// usage errors raised before a command builds one — without double-rendering
	// errors the command already surfaced.
	rendered bool
}

func (e *Error) Error() string { return e.Message }

func (e *Error) Unwrap() error { return e.cause }

// MarkRendered records that this error has already been surfaced to the user.
func (e *Error) MarkRendered() { e.rendered = true }

// Rendered reports whether this error has already been surfaced to the user.
func (e *Error) Rendered() bool { return e.rendered }

// New builds an Error of the given kind.
func New(kind Kind, msg string) *Error {
	return &Error{Kind: kind, Message: msg}
}

// Wrap builds an Error of the given kind wrapping an underlying cause.
func Wrap(kind Kind, msg string, cause error) *Error {
	return &Error{Kind: kind, Message: msg, cause: cause}
}

// ToEnvelope converts the error into the Appendix B error object (spec §9.4).
func (e *Error) ToEnvelope() envelope.CLIError {
	return envelope.CLIError{Code: string(e.Kind), Message: e.Message, A2ACode: e.A2ACode}
}

// ExitCode returns the process exit code for err (design §3.5). A nil error is
// success (0); an *Error uses its Kind's code; any other error is generic (1).
func ExitCode(err error) int {
	if err == nil {
		return 0
	}
	var e *Error
	if errors.As(err, &e) {
		if code, ok := exitCodes[e.Kind]; ok {
			return code
		}
		return 1
	}
	return 1
}

// FromState maps a terminal or interrupted task state to the error that should
// be returned to set the exit code. A COMPLETED task (or any non-final/unknown
// state) returns nil, meaning success.
func FromState(state string) *Error {
	switch state {
	case envelope.StateCompleted:
		return nil
	case envelope.StateFailed, envelope.StateRejected, envelope.StateCanceled:
		return &Error{Kind: KindTaskFailed, Message: "task ended in state " + state}
	case envelope.StateInputRequired:
		return &Error{Kind: KindInputRequired, Message: "task requires input to proceed"}
	case envelope.StateAuthRequired:
		return &Error{Kind: KindAuth, Message: "task requires authentication to proceed"}
	default:
		return nil
	}
}
