// Copyright 2026 The a2a-cli Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package cli

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The import boundary (design §3.2, HARD): internal/cli MUST NOT import SDK/proto
// types. The a2a.Event -> envelope translation lives in internal/client, so the
// command layer consumes envelope types only. This test parses every production
// .go file in this package and fails if any imports an a2a-go SDK package — locking
// the invariant that the Phase-5 streaming path (and every future change) keeps
// internal/cli SDK-free.
func TestImportBoundary_NoSDKInCLI(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}
	fset := token.NewFileSet()
	checked := 0
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(".", name)
		f, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		checked++
		for _, imp := range f.Imports {
			p, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				t.Fatalf("bad import path in %s: %v", path, imp.Path.Value)
			}
			if strings.Contains(p, "a2aproject/a2a-go") {
				t.Errorf("%s imports SDK package %q — internal/cli must stay SDK-free (import boundary §3.2)", name, p)
			}
		}
	}
	if checked == 0 {
		t.Fatal("no production .go files were checked; the boundary test is not running")
	}
}
