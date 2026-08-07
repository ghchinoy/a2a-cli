// Copyright 2026 The a2a-cli Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package render

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/ghchinoy/a2a-cli/internal/envelope"
)

func sampleFullCard() *envelope.FullCard {
	return &envelope.FullCard{
		Name:        "Full Agent",
		Description: "an agent with all sections",
		Version:     "2.1.0",
		Provider:    &envelope.CardProvider{Organization: "Acme", URL: "http://acme.example"},
		Capabilities: envelope.CardCapabilities{
			Streaming:  true,
			Extensions: []envelope.CardExtension{{URI: "urn:ext:foo", Required: true}},
		},
		Interfaces: []envelope.CardInterface{
			{Transport: "HTTP+JSON", URL: "http://host/rest", ProtocolVersion: "1.0", RoutingID: "tenant-7"},
		},
		SecuritySchemes: []envelope.CardSecurity{
			{Name: "apiKeyScheme", Type: "apiKey", Detail: `in header as "X-API-Key"`},
		},
		Skills: []envelope.CardSkill{
			{ID: "hello", Name: "Hello", Description: "says hi", Tags: []string{"greeting"}},
		},
		Selection: envelope.CardSelection{Transport: "http-json", URL: "http://host/rest", Reason: "card-declared preference", RoutingID: "tenant-7"},
	}
}

func TestRenderCard_JSON_OnlyValidJSONOnStdout(t *testing.T) {
	var out, errb bytes.Buffer
	r := New(ModeJSON, &out, &errb)
	if err := r.RenderCard(sampleFullCard()); err != nil {
		t.Fatal(err)
	}
	var decoded envelope.FullCard
	dec := json.NewDecoder(&out)
	if err := dec.Decode(&decoded); err != nil {
		t.Fatalf("stdout is not valid JSON: %v", err)
	}
	if dec.More() {
		t.Fatal("stdout has trailing content after the JSON document")
	}
	if decoded.Name != "Full Agent" || len(decoded.Skills) != 1 || decoded.Selection.Transport != "http-json" {
		t.Errorf("round-trip mismatch: %+v", decoded)
	}
	if errb.Len() != 0 {
		t.Errorf("stderr should be empty in json card path, got %q", errb.String())
	}
}

func TestRenderCard_Text_PresentsAllSections(t *testing.T) {
	var out, errb bytes.Buffer
	r := New(ModeText, &out, &errb)
	if err := r.RenderCard(sampleFullCard()); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	// Every section must be present (design §8.1 acceptance).
	for _, want := range []string{
		"Name:        Full Agent",
		"Provider:    Acme — http://acme.example",
		"Capabilities:",
		"streaming:         true",
		"extension: urn:ext:foo (required)",
		"Interfaces:",
		"HTTP+JSON http://host/rest [v1.0] routingId=tenant-7",
		"Security schemes:",
		"apiKeyScheme: apiKey",
		"Skills:",
		"hello (Hello)",
		"tags: greeting",
		"Selected transport: http-json -> http://host/rest",
		"reason: card-declared preference",
		"routing identifier: tenant-7",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("text output missing %q\n---\n%s", want, got)
		}
	}
}

func TestRenderCard_Text_EmptySections(t *testing.T) {
	var out, errb bytes.Buffer
	r := New(ModeText, &out, &errb)
	c := &envelope.FullCard{
		Name:      "Bare",
		Selection: envelope.CardSelection{Transport: "http-json", URL: "http://x", Reason: "default"},
	}
	if err := r.RenderCard(c); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"Security schemes:",
		"(none — no authentication required)",
		"Skills:",
		"(none declared)",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("empty-section output missing %q\n---\n%s", want, got)
		}
	}
}
