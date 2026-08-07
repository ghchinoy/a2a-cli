// Copyright 2026 The A2A Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

// Package urlutil holds small URL helpers shared across packages so credential
// handling stays consistent (design §3.7 / audit R-a/F-4). It has no internal
// dependencies to avoid import cycles between session, client, and envelope.
package urlutil

import "net/url"

// Sanitize removes any userinfo (user:password@) from a URL so URL-embedded
// credentials are never persisted or presented. Unparseable input is returned
// unchanged (best-effort: never make a bad string worse).
func Sanitize(s string) string {
	if s == "" {
		return s
	}
	u, err := url.Parse(s)
	if err != nil {
		return s
	}
	u.User = nil
	return u.String()
}
