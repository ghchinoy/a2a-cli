// Copyright 2026 The A2A Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package envelope

import (
	"fmt"

	"github.com/a2aproject/a2a-go/v2/a2a"

	"github.com/ghchinoy/a2a-cli/internal/urlutil"
)

// Card is a normalized, minimal Agent Card view used by the Phase-1 send path:
// identity plus the transports the client can see and the one it selected. Full
// card presentation for `discover` lives in FullCard below.
type Card struct {
	Name              string   `json:"name"`
	Description       string   `json:"description,omitempty"`
	Version           string   `json:"version,omitempty"`
	Transports        []string `json:"transports,omitempty"`
	SelectedTransport string   `json:"selectedTransport,omitempty"`
	SelectedURL       string   `json:"selectedUrl,omitempty"`
}

// FromCard normalizes an a2a.AgentCard into a Card, recording the transport the
// client selected and the URL it will connect to.
func FromCard(c *a2a.AgentCard, selectedTransport, selectedURL string) *Card {
	if c == nil {
		return nil
	}
	out := &Card{
		Name:              c.Name,
		Description:       c.Description,
		Version:           c.Version,
		SelectedTransport: selectedTransport,
		SelectedURL:       selectedURL,
	}
	for _, iface := range c.SupportedInterfaces {
		if iface != nil {
			out.Transports = append(out.Transports, string(iface.ProtocolBinding))
		}
	}
	return out
}

// FullCard is the complete normalized Agent Card presented by `discover`
// (design §8.1). It carries every card section as normalized envelope types so
// no SDK/proto type leaks above internal/client (import boundary, design §3.2).
// It is the only card view the render layer serializes in json mode.
type FullCard struct {
	Name               string           `json:"name"`
	Description        string           `json:"description,omitempty"`
	Version            string           `json:"version,omitempty"`
	Provider           *CardProvider    `json:"provider,omitempty"`
	DocumentationURL   string           `json:"documentationUrl,omitempty"`
	IconURL            string           `json:"iconUrl,omitempty"`
	Capabilities       CardCapabilities `json:"capabilities"`
	DefaultInputModes  []string         `json:"defaultInputModes,omitempty"`
	DefaultOutputModes []string         `json:"defaultOutputModes,omitempty"`
	Interfaces         []CardInterface  `json:"interfaces,omitempty"`
	SecuritySchemes    []CardSecurity   `json:"securitySchemes,omitempty"`
	Skills             []CardSkill      `json:"skills,omitempty"`
	// Selection describes the transport the client would use for this card
	// (design §11.1): which binding/URL was chosen and why.
	Selection CardSelection `json:"selection"`
}

// CardProvider is the agent's service provider (identity section).
type CardProvider struct {
	Organization string `json:"organization,omitempty"`
	URL          string `json:"url,omitempty"`
}

// CardCapabilities is the normalized capabilities section.
type CardCapabilities struct {
	Streaming         bool            `json:"streaming"`
	PushNotifications bool            `json:"pushNotifications"`
	ExtendedAgentCard bool            `json:"extendedAgentCard"`
	Extensions        []CardExtension `json:"extensions,omitempty"`
}

// CardExtension is a declared protocol extension.
type CardExtension struct {
	URI         string `json:"uri,omitempty"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required,omitempty"`
}

// CardInterface is one transport binding declared by the card.
type CardInterface struct {
	Transport       string `json:"transport"`
	URL             string `json:"url"`
	ProtocolVersion string `json:"protocolVersion,omitempty"`
	// RoutingID is the interface's routing identifier (SDK tenant), echoed on
	// every request when declared (design §11.1).
	RoutingID string `json:"routingId,omitempty"`
}

// CardSecurity is a normalized security scheme. Type is the scheme kind
// (apiKey|http|oauth2|openIdConnect|mutualTLS); Detail carries the salient,
// human-readable specifics for that kind.
type CardSecurity struct {
	Name        string `json:"name"`
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
	Detail      string `json:"detail,omitempty"`
}

// CardSkill is a normalized agent skill.
type CardSkill struct {
	ID          string   `json:"id"`
	Name        string   `json:"name,omitempty"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Examples    []string `json:"examples,omitempty"`
	InputModes  []string `json:"inputModes,omitempty"`
	OutputModes []string `json:"outputModes,omitempty"`
}

// CardSelection records the transport the client would pick for this card and
// the reason (explicit --transport, card-declared preference, or HTTP+JSON
// default), plus any routing identifier declared on the chosen interface.
type CardSelection struct {
	Transport string `json:"transport"`
	URL       string `json:"url"`
	Reason    string `json:"reason"`
	RoutingID string `json:"routingId,omitempty"`
}

// FromFullCard normalizes an a2a.AgentCard into a FullCard, attaching the
// client's transport selection (chosen binding/URL, reason, routing id).
func FromFullCard(c *a2a.AgentCard, sel CardSelection) *FullCard {
	if c == nil {
		return nil
	}
	// Strip any user:pass@ credential embedded in URLs before presenting them
	// (audit F-4). Phase-1 stripped userinfo on persistence; this closes the
	// presentation path so `discover` never echoes a credential a hostile or
	// careless card/URL carries.
	sel.URL = urlutil.Sanitize(sel.URL)
	out := &FullCard{
		Name:               c.Name,
		Description:        c.Description,
		Version:            c.Version,
		DocumentationURL:   c.DocumentationURL,
		IconURL:            c.IconURL,
		DefaultInputModes:  c.DefaultInputModes,
		DefaultOutputModes: c.DefaultOutputModes,
		Selection:          sel,
		Capabilities: CardCapabilities{
			Streaming:         c.Capabilities.Streaming,
			PushNotifications: c.Capabilities.PushNotifications,
			ExtendedAgentCard: c.Capabilities.ExtendedAgentCard,
		},
	}
	if c.Provider != nil {
		out.Provider = &CardProvider{Organization: c.Provider.Org, URL: c.Provider.URL}
	}
	for _, ext := range c.Capabilities.Extensions {
		out.Capabilities.Extensions = append(out.Capabilities.Extensions, CardExtension{
			URI:         ext.URI,
			Description: ext.Description,
			Required:    ext.Required,
		})
	}
	for _, iface := range c.SupportedInterfaces {
		if iface == nil {
			continue
		}
		out.Interfaces = append(out.Interfaces, CardInterface{
			Transport:       string(iface.ProtocolBinding),
			URL:             urlutil.Sanitize(iface.URL),
			ProtocolVersion: string(iface.ProtocolVersion),
			RoutingID:       iface.Tenant,
		})
	}
	for name, scheme := range c.SecuritySchemes {
		out.SecuritySchemes = append(out.SecuritySchemes, securityFromSDK(string(name), scheme))
	}
	sortSecurity(out.SecuritySchemes)
	for _, sk := range c.Skills {
		out.Skills = append(out.Skills, CardSkill{
			ID:          sk.ID,
			Name:        sk.Name,
			Description: sk.Description,
			Tags:        sk.Tags,
			Examples:    sk.Examples,
			InputModes:  sk.InputModes,
			OutputModes: sk.OutputModes,
		})
	}
	return out
}

// securityFromSDK normalizes one SDK security scheme (a sealed union) into a
// CardSecurity. Both value and pointer concrete forms are handled.
func securityFromSDK(name string, scheme a2a.SecurityScheme) CardSecurity {
	out := CardSecurity{Name: name, Type: "unknown"}
	switch s := scheme.(type) {
	case *a2a.APIKeySecurityScheme:
		apiKey(&out, s)
	case a2a.APIKeySecurityScheme:
		apiKey(&out, &s)
	case *a2a.HTTPAuthSecurityScheme:
		httpAuth(&out, s)
	case a2a.HTTPAuthSecurityScheme:
		httpAuth(&out, &s)
	case *a2a.OAuth2SecurityScheme:
		oauth2(&out, s)
	case a2a.OAuth2SecurityScheme:
		oauth2(&out, &s)
	case *a2a.OpenIDConnectSecurityScheme:
		openID(&out, s)
	case a2a.OpenIDConnectSecurityScheme:
		openID(&out, &s)
	case *a2a.MutualTLSSecurityScheme:
		mutualTLS(&out, s)
	case a2a.MutualTLSSecurityScheme:
		mutualTLS(&out, &s)
	}
	return out
}

func apiKey(out *CardSecurity, s *a2a.APIKeySecurityScheme) {
	out.Type = "apiKey"
	out.Description = s.Description
	out.Detail = fmt.Sprintf("in %s as %q", s.Location, s.Name)
}

func httpAuth(out *CardSecurity, s *a2a.HTTPAuthSecurityScheme) {
	out.Type = "http"
	out.Description = s.Description
	out.Detail = "scheme " + s.Scheme
	if s.BearerFormat != "" {
		out.Detail += " (bearer format " + s.BearerFormat + ")"
	}
}

func oauth2(out *CardSecurity, s *a2a.OAuth2SecurityScheme) {
	out.Type = "oauth2"
	out.Description = s.Description
	if s.Oauth2MetadataURL != "" {
		out.Detail = "metadata " + s.Oauth2MetadataURL
	}
}

func openID(out *CardSecurity, s *a2a.OpenIDConnectSecurityScheme) {
	out.Type = "openIdConnect"
	out.Description = s.Description
	out.Detail = s.OpenIDConnectURL
}

func mutualTLS(out *CardSecurity, s *a2a.MutualTLSSecurityScheme) {
	out.Type = "mutualTLS"
	out.Description = s.Description
}

// sortSecurity orders schemes by name for deterministic output (map iteration is
// otherwise random), which keeps text/json rendering stable for golden tests.
func sortSecurity(s []CardSecurity) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1].Name > s[j].Name; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

// ValidateCard checks an a2a.AgentCard against the A2A card schema's required
// structure (design §8.1 --validate). It returns a list of human-readable
// problems; an empty list means the card is valid. Validation is intentionally
// pure (no clierr dependency) so the client layer maps the result to an exit
// code and the envelope layer stays free of the error taxonomy.
func ValidateCard(c *a2a.AgentCard) []string {
	if c == nil {
		return []string{"agent card is nil"}
	}
	var problems []string
	if c.Name == "" {
		problems = append(problems, "missing required field: name")
	}
	if c.Description == "" {
		problems = append(problems, "missing required field: description")
	}
	if len(c.SupportedInterfaces) == 0 {
		problems = append(problems, "missing required field: supportedInterfaces (at least one interface required)")
	}
	for i, iface := range c.SupportedInterfaces {
		if iface == nil {
			problems = append(problems, fmt.Sprintf("supportedInterfaces[%d]: is null", i))
			continue
		}
		if iface.URL == "" {
			problems = append(problems, fmt.Sprintf("supportedInterfaces[%d]: missing url", i))
		}
		if iface.ProtocolBinding == "" {
			problems = append(problems, fmt.Sprintf("supportedInterfaces[%d]: missing protocolBinding", i))
		}
	}
	for i, sk := range c.Skills {
		if sk.ID == "" {
			problems = append(problems, fmt.Sprintf("skills[%d]: missing required field id", i))
		}
		if sk.Name == "" {
			problems = append(problems, fmt.Sprintf("skills[%d] (%s): missing required field name", i, sk.ID))
		}
	}
	return problems
}
