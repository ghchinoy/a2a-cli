// Copyright 2026 The A2A Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0

// Command a2a-cli is a conformant Tier-1 client for the A2A protocol.
package main

import (
	"github.com/ghchinoy/a2a-cli/internal/cli"
	"github.com/ghchinoy/a2a-cli/internal/clierr"

	"os"
)

func main() {
	err := cli.Execute()
	os.Exit(clierr.ExitCode(err))
}
