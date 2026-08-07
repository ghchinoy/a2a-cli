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
	"testing"
)

func TestCallerSuppliedProvider_Headers(t *testing.T) {
	p := &CallerSuppliedProvider{
		Bearer: "tok",
		APIKey: "key",
		Extra:  map[string]string{"X-Trace": "1"},
	}
	h, err := p.Headers(context.Background(), Target{URL: "http://x", Transport: "jsonrpc"})
	if err != nil {
		t.Fatal(err)
	}
	if h["Authorization"] != "Bearer tok" {
		t.Errorf("Authorization = %q", h["Authorization"])
	}
	if h["X-API-Key"] != "key" {
		t.Errorf("X-API-Key = %q", h["X-API-Key"])
	}
	if h["X-Trace"] != "1" {
		t.Errorf("X-Trace = %q", h["X-Trace"])
	}
}

func TestCallerSuppliedProvider_Empty(t *testing.T) {
	p := &CallerSuppliedProvider{}
	h, err := p.Headers(context.Background(), Target{})
	if err != nil {
		t.Fatal(err)
	}
	if len(h) != 0 {
		t.Errorf("expected no headers, got %v", h)
	}
}
