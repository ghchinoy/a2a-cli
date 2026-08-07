// Copyright 2026 The a2a-cli Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package envelope

import "github.com/a2aproject/a2a-go/v2/a2a"

// Card is a normalized, minimal Agent Card view. Full card presentation is
// Phase 2 (discover); Phase 1 only needs identity plus the transports the client
// can see and the one it selected.
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
