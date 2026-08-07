// Copyright 2026 The A2A Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package client

import "context"

// Target identifies the request destination for credential selection.
type Target struct {
	URL       string
	Transport string
}

// CredentialProvider supplies per-request auth headers/metadata (design §3.3).
// This is the seam that keeps auth layered across tiers: Tier 1 ships
// CallerSuppliedProvider; Tier 2's OAuth token store implements the same
// interface with no call-site change.
type CredentialProvider interface {
	// Headers returns per-request auth headers for the given target. An empty
	// map (or nil) means "attach nothing".
	Headers(ctx context.Context, target Target) (map[string]string, error)
}

// CallerSuppliedProvider attaches caller-supplied credentials: a bearer token,
// an API key, and arbitrary extra headers. It is wired in Phase 1 but the Go
// hello-world server requires no auth, so it is typically a no-op there.
type CallerSuppliedProvider struct {
	Bearer string
	APIKey string
	Extra  map[string]string
}

// Headers implements CredentialProvider.
func (p *CallerSuppliedProvider) Headers(_ context.Context, _ Target) (map[string]string, error) {
	if p == nil {
		return nil, nil
	}
	h := make(map[string]string)
	if p.Bearer != "" {
		h["Authorization"] = "Bearer " + p.Bearer
	}
	if p.APIKey != "" {
		h["X-API-Key"] = p.APIKey
	}
	for k, v := range p.Extra {
		h[k] = v
	}
	return h, nil
}
