// Copyright 2026 The a2a-cli Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

// Package poll implements the blocking wait loop (spec §7 / design §3.6): it
// polls until a terminal state, stops immediately on an interrupted state, honors
// --poll-interval/--timeout, and never busy-loops. The already-known taskId is
// always preserved in the returned result so callers never lose it on timeout or
// SIGINT.
package poll

import (
	"context"
	"time"

	"github.com/ghchinoy/a2a-cli/internal/clierr"
	"github.com/ghchinoy/a2a-cli/internal/envelope"
)

// GetFunc fetches the current TaskResult for the task being awaited.
type GetFunc func(ctx context.Context) (*envelope.TaskResult, error)

// Options configures the wait loop.
type Options struct {
	// Interval is the delay between polls. Values <= 0 default to 2s (spec §7).
	Interval time.Duration
	// Timeout bounds the total wait. Zero means no timeout.
	Timeout time.Duration
}

const defaultInterval = 2 * time.Second

// Wait blocks until the task reaches a terminal or interrupted state, the timeout
// expires, the context is canceled, or get returns an error. The returned
// TaskResult is always the latest known state (never nil once initial is set), so
// the caller retains the taskId even on timeout/cancellation. On timeout Wait
// returns a clierr.KindTimeout error; on context cancellation it returns
// ctx.Err(); on a terminal/interrupted state it returns a nil error and the
// caller inspects the state.
func Wait(ctx context.Context, initial *envelope.TaskResult, get GetFunc, opts Options) (*envelope.TaskResult, error) {
	interval := opts.Interval
	if interval <= 0 {
		interval = defaultInterval
	}

	var deadline <-chan time.Time
	if opts.Timeout > 0 {
		t := time.NewTimer(opts.Timeout)
		defer t.Stop()
		deadline = t.C
	}

	last := initial
	if done(last) {
		return last, nil
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return last, ctx.Err()
		case <-deadline:
			return last, clierr.New(clierr.KindTimeout, "timed out waiting for task to reach a terminal state")
		case <-ticker.C:
			next, err := get(ctx)
			if err != nil {
				return last, err
			}
			if next != nil {
				last = next
			}
			if done(last) {
				return last, nil
			}
		}
	}
}

func done(tr *envelope.TaskResult) bool {
	return tr != nil && (envelope.IsTerminal(tr.State) || envelope.IsInterrupted(tr.State))
}
