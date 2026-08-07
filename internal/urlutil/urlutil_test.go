// Copyright 2026 The A2A Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package urlutil

import "testing"

func TestSanitize(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"empty", "", ""},
		{"no-userinfo", "https://host/path", "https://host/path"},
		{"user-pass", "https://user:secret@host/path", "https://host/path"},
		{"user-only", "https://user@host/path", "https://host/path"},
		{"keeps-query", "https://user:p@host/p?q=1", "https://host/p?q=1"},
		{"unparseable-returned-asis", "://not a url", "://not a url"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Sanitize(tc.in); got != tc.want {
				t.Errorf("Sanitize(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
