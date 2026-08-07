// Copyright 2026 The a2a-cli Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package envelope

import (
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
)

// fullSDKCard builds an AgentCard exercising every section discover presents.
func fullSDKCard() *a2a.AgentCard {
	iface := a2a.NewAgentInterface("http://host/rest", a2a.TransportProtocolHTTPJSON)
	iface.ProtocolVersion = "1.0"
	iface.Tenant = "tenant-7"
	return &a2a.AgentCard{
		Name:               "Full Agent",
		Description:        "an agent with all sections",
		Version:            "2.1.0",
		DocumentationURL:   "http://host/docs",
		IconURL:            "http://host/icon.png",
		Provider:           &a2a.AgentProvider{Org: "Acme", URL: "http://acme.example"},
		DefaultInputModes:  []string{"text"},
		DefaultOutputModes: []string{"text"},
		Capabilities: a2a.AgentCapabilities{
			Streaming:         true,
			PushNotifications: true,
			ExtendedAgentCard: true,
			Extensions: []a2a.AgentExtension{
				{URI: "urn:ext:foo", Description: "foo ext", Required: true},
			},
		},
		SupportedInterfaces: []*a2a.AgentInterface{iface},
		SecuritySchemes: a2a.NamedSecuritySchemes{
			"apiKeyScheme": a2a.APIKeySecurityScheme{Location: "header", Name: "X-API-Key", Description: "key auth"},
			"httpScheme":   a2a.HTTPAuthSecurityScheme{Scheme: "Bearer", BearerFormat: "JWT"},
		},
		Skills: []a2a.AgentSkill{
			{ID: "hello", Name: "Hello", Description: "says hi", Tags: []string{"greeting"}, Examples: []string{"hi"}},
		},
	}
}

func TestFromFullCard_AllSections(t *testing.T) {
	sel := CardSelection{Transport: "http-json", URL: "http://host/rest", Reason: "test", RoutingID: "tenant-7"}
	fc := FromFullCard(fullSDKCard(), sel)
	if fc == nil {
		t.Fatal("FromFullCard returned nil")
	}
	if fc.Name != "Full Agent" || fc.Version != "2.1.0" || fc.Description == "" {
		t.Errorf("identity not carried: %+v", fc)
	}
	if fc.Provider == nil || fc.Provider.Organization != "Acme" || fc.Provider.URL != "http://acme.example" {
		t.Errorf("provider not carried: %+v", fc.Provider)
	}
	if fc.DocumentationURL != "http://host/docs" || fc.IconURL != "http://host/icon.png" {
		t.Errorf("doc/icon not carried: %+v", fc)
	}
	if !fc.Capabilities.Streaming || !fc.Capabilities.PushNotifications || !fc.Capabilities.ExtendedAgentCard {
		t.Errorf("capabilities not carried: %+v", fc.Capabilities)
	}
	if len(fc.Capabilities.Extensions) != 1 || fc.Capabilities.Extensions[0].URI != "urn:ext:foo" {
		t.Errorf("extensions not carried: %+v", fc.Capabilities.Extensions)
	}
	if len(fc.Interfaces) != 1 {
		t.Fatalf("interfaces = %d, want 1", len(fc.Interfaces))
	}
	iface := fc.Interfaces[0]
	if iface.Transport != "HTTP+JSON" || iface.URL != "http://host/rest" || iface.ProtocolVersion != "1.0" || iface.RoutingID != "tenant-7" {
		t.Errorf("interface not carried: %+v", iface)
	}
	if len(fc.SecuritySchemes) != 2 {
		t.Fatalf("security schemes = %d, want 2", len(fc.SecuritySchemes))
	}
	// Deterministic order (sorted by name): apiKeyScheme before httpScheme.
	if fc.SecuritySchemes[0].Name != "apiKeyScheme" || fc.SecuritySchemes[0].Type != "apiKey" {
		t.Errorf("first security scheme = %+v", fc.SecuritySchemes[0])
	}
	if fc.SecuritySchemes[1].Name != "httpScheme" || fc.SecuritySchemes[1].Type != "http" {
		t.Errorf("second security scheme = %+v", fc.SecuritySchemes[1])
	}
	if len(fc.Skills) != 1 || fc.Skills[0].ID != "hello" || fc.Skills[0].Name != "Hello" {
		t.Errorf("skills not carried: %+v", fc.Skills)
	}
	if fc.Selection.Transport != "http-json" || fc.Selection.RoutingID != "tenant-7" {
		t.Errorf("selection not carried: %+v", fc.Selection)
	}
}

func TestFromFullCard_Nil(t *testing.T) {
	if FromFullCard(nil, CardSelection{}) != nil {
		t.Error("FromFullCard(nil) should be nil")
	}
}

func TestValidateCard_Valid(t *testing.T) {
	if p := ValidateCard(fullSDKCard()); len(p) != 0 {
		t.Errorf("valid card reported problems: %v", p)
	}
}

func TestValidateCard_Malformed(t *testing.T) {
	// Missing name, description, no interfaces, a skill missing its id.
	c := &a2a.AgentCard{
		Skills: []a2a.AgentSkill{{Name: "unnamed-id"}},
	}
	problems := ValidateCard(c)
	if len(problems) == 0 {
		t.Fatal("expected validation problems for a malformed card")
	}
	joined := ""
	for _, p := range problems {
		joined += p + "\n"
	}
	for _, want := range []string{"name", "description", "supportedInterfaces", "id"} {
		if !contains(joined, want) {
			t.Errorf("problems missing mention of %q; got:\n%s", want, joined)
		}
	}
}

func TestValidateCard_Nil(t *testing.T) {
	if p := ValidateCard(nil); len(p) != 1 {
		t.Errorf("nil card should yield one problem, got %v", p)
	}
}

// TestValidateCard_InterfaceAndSkillBranches exercises the per-interface and
// per-skill validation branches individually (coverage R-d).
func TestValidateCard_InterfaceAndSkillBranches(t *testing.T) {
	base := func() *a2a.AgentCard {
		return &a2a.AgentCard{
			Name:                "A",
			Description:         "d",
			SupportedInterfaces: []*a2a.AgentInterface{a2a.NewAgentInterface("http://h", a2a.TransportProtocolHTTPJSON)},
		}
	}
	cases := []struct {
		name    string
		mutate  func(c *a2a.AgentCard)
		wantSub string
	}{
		{
			name:    "nil interface",
			mutate:  func(c *a2a.AgentCard) { c.SupportedInterfaces = []*a2a.AgentInterface{nil} },
			wantSub: "is null",
		},
		{
			name: "interface missing url",
			mutate: func(c *a2a.AgentCard) {
				c.SupportedInterfaces = []*a2a.AgentInterface{{ProtocolBinding: a2a.TransportProtocolHTTPJSON}}
			},
			wantSub: "missing url",
		},
		{
			name:    "interface missing protocolBinding",
			mutate:  func(c *a2a.AgentCard) { c.SupportedInterfaces = []*a2a.AgentInterface{{URL: "http://h"}} },
			wantSub: "missing protocolBinding",
		},
		{
			name:    "skill missing name",
			mutate:  func(c *a2a.AgentCard) { c.Skills = []a2a.AgentSkill{{ID: "s1"}} },
			wantSub: "missing required field name",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := base()
			tc.mutate(c)
			problems := ValidateCard(c)
			joined := ""
			for _, p := range problems {
				joined += p + "\n"
			}
			if !contains(joined, tc.wantSub) {
				t.Errorf("expected a problem mentioning %q; got:\n%s", tc.wantSub, joined)
			}
		})
	}
}

// TestFromFullCard_StripsUserinfo is the R-b regression: URL-embedded credentials
// must never survive into the presented FullCard (selection URL or interface URL).
func TestFromFullCard_StripsUserinfo(t *testing.T) {
	c := &a2a.AgentCard{
		Name:                "A",
		SupportedInterfaces: []*a2a.AgentInterface{a2a.NewAgentInterface("https://user:secret@host/rest", a2a.TransportProtocolHTTPJSON)},
	}
	fc := FromFullCard(c, CardSelection{Transport: "http-json", URL: "https://user:secret@host/rest"})
	if contains(fc.Selection.URL, "secret") || contains(fc.Selection.URL, "user:") {
		t.Errorf("selection URL leaked credentials: %q", fc.Selection.URL)
	}
	if len(fc.Interfaces) != 1 || contains(fc.Interfaces[0].URL, "secret") {
		t.Errorf("interface URL leaked credentials: %+v", fc.Interfaces)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
