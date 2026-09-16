// Copyright (c) 2025 Silver Blueprints LLC
// SPDX-License-Identifier: MIT

// Command dxctl is the CLI for the Denarix accounting API.
package main

import (
	"os"

	"github.com/silverbp/denarix/internal/dxctl/cmd"
)

func main() {
	if err := cmd.NewRootCmd().Execute(); err != nil {
		os.Exit(1)
	}
}
