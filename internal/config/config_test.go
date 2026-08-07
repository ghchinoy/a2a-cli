// Copyright 2026 The a2a-cli Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

package config

import (
	"testing"

	"github.com/spf13/pflag"
)

func newFlags() *pflag.FlagSet {
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	fs.String("service-url", "", "")
	return fs
}

func TestPrecedence_DefaultOnly(t *testing.T) {
	cfg := New(newFlags(), nil, map[string]string{"service-url": "http://default"})
	if got := cfg.String("service-url"); got != "http://default" {
		t.Errorf("default: got %q", got)
	}
}

func TestPrecedence_SessionBeatsDefault(t *testing.T) {
	cfg := New(newFlags(),
		map[string]string{"service-url": "http://session"},
		map[string]string{"service-url": "http://default"})
	if got := cfg.String("service-url"); got != "http://session" {
		t.Errorf("session should beat default: got %q", got)
	}
}

func TestPrecedence_EnvBeatsSession(t *testing.T) {
	t.Setenv("A2A_CLI_SERVICE_URL", "http://env")
	cfg := New(newFlags(),
		map[string]string{"service-url": "http://session"},
		map[string]string{"service-url": "http://default"})
	if got := cfg.String("service-url"); got != "http://env" {
		t.Errorf("env should beat session: got %q", got)
	}
}

func TestPrecedence_FlagBeatsEnv(t *testing.T) {
	t.Setenv("A2A_CLI_SERVICE_URL", "http://env")
	fs := newFlags()
	if err := fs.Set("service-url", "http://flag"); err != nil {
		t.Fatal(err)
	}
	cfg := New(fs,
		map[string]string{"service-url": "http://session"},
		map[string]string{"service-url": "http://default"})
	if got := cfg.String("service-url"); got != "http://flag" {
		t.Errorf("flag should beat env: got %q", got)
	}
}

func TestPrecedence_UnsetFlagFallsThrough(t *testing.T) {
	// A declared-but-unchanged flag must not shadow lower layers.
	cfg := New(newFlags(), nil, map[string]string{"service-url": "http://default"})
	if got := cfg.String("service-url"); got != "http://default" {
		t.Errorf("unset flag should fall through to default: got %q", got)
	}
}
