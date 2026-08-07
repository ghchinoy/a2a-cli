// Copyright 2026 The A2A Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

// Package config resolves configuration values with the precedence mandated by
// spec §4.5 (design §3.7): explicit flag > env var > profile > session > built-in
// default. Phase 1 implements flag > env > session > default (profiles are a
// Tier 2 feature; the session layer already sits below env so --continue works).
package config

import (
	"strings"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// EnvPrefix is the environment-variable prefix, e.g. A2A_CLI_SERVICE_URL.
const EnvPrefix = "A2A_CLI"

// Resolver resolves values across the precedence layers.
type Resolver struct {
	v *viper.Viper
}

// New builds a Resolver.
//
//   - flags provides the highest-precedence layer (a flag counts only when the
//     user actually set it — viper honors pflag.Changed).
//   - env vars (prefixed EnvPrefix, with '-' mapped to '_') sit below flags.
//   - sessionDefaults sit below env and above the built-in defaults; a key present
//     in sessionDefaults shadows the built-in default of the same key.
//   - defaults are the built-in fallbacks.
func New(flags *pflag.FlagSet, sessionDefaults, defaults map[string]string) *Resolver {
	v := viper.New()
	v.SetEnvPrefix(EnvPrefix)
	v.SetEnvKeyReplacer(strings.NewReplacer("-", "_"))
	v.AutomaticEnv()
	if flags != nil {
		_ = v.BindPFlags(flags)
	}
	for k, def := range defaults {
		if sv, ok := sessionDefaults[k]; ok && sv != "" {
			v.SetDefault(k, sv)
		} else {
			v.SetDefault(k, def)
		}
	}
	// Session values for keys without a built-in default still take effect.
	for k, sv := range sessionDefaults {
		if _, ok := defaults[k]; !ok && sv != "" {
			v.SetDefault(k, sv)
		}
	}
	return &Resolver{v: v}
}

// String returns the resolved string value for key.
func (r *Resolver) String(key string) string {
	return r.v.GetString(key)
}

// Bool returns the resolved bool value for key.
func (r *Resolver) Bool(key string) bool {
	return r.v.GetBool(key)
}
