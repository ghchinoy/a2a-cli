// Copyright 2026 The a2a-cli Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package render

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The render chokepoint (CO-5, BINDING): the private stdout/stderr writers (r.out /
// r.err) may only be handed to the sanctioned sinks — emit (which sanitizes every
// untrusted string arg) and the raw JSON writers writeJSON / writeStreamLine (which
// defer to encoding/json's single-escaping). No other call may take r.out or r.err
// as an argument, which structurally prevents a new print seam from bypassing
// sanitization — including on the Phase-5 streaming render paths.
//
// This test parses every production .go file in the package and asserts that every
// use of an `r.out` / `r.err` selector as a CALL ARGUMENT targets a sanctioned sink.
func TestRenderChokepoint_OutErrOnlyReachSanctionedSinks(t *testing.T) {
	sanctioned := map[string]bool{
		"emit":            true, // r.emit(w, clean, ...)
		"writeJSON":       true, // raw -o json envelope
		"writeStreamLine": true, // raw -o json --stream NDJSON record
	}

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
		f, err := parser.ParseFile(fset, filepath.Join(".", name), nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		checked++
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			// Skip the argument check for calls TO a sanctioned sink: passing r.out to
			// emit/writeJSON/writeStreamLine is exactly the allowed hand-off.
			if isSanctionedCall(call, sanctioned) {
				return true
			}
			for _, arg := range call.Args {
				if isOutErrSelector(arg) {
					pos := fset.Position(arg.Pos())
					t.Errorf("%s:%d: r.out/r.err passed to a non-sanctioned call — route output through emit/writeJSON/writeStreamLine (CO-5)", name, pos.Line)
				}
			}
			return true
		})
	}
	if checked == 0 {
		t.Fatal("no production .go files were checked; the chokepoint test is not running")
	}
}

// isSanctionedCall reports whether call targets emit / writeJSON / writeStreamLine.
func isSanctionedCall(call *ast.CallExpr, sanctioned map[string]bool) bool {
	switch fn := call.Fun.(type) {
	case *ast.SelectorExpr: // r.emit(...)
		return sanctioned[fn.Sel.Name]
	case *ast.Ident: // writeJSON(...) / writeStreamLine(...)
		return sanctioned[fn.Name]
	}
	return false
}

// isOutErrSelector reports whether expr is a selector `<x>.out` or `<x>.err`
// (the Renderer's private stdout/stderr writers).
func isOutErrSelector(expr ast.Expr) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	return sel.Sel.Name == "out" || sel.Sel.Name == "err"
}
