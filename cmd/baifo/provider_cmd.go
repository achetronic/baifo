// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

package main

import (
	"fmt"
	"os"

	"github.com/achetronic/baifo/internal/providers/anthropic"
)

// runProviderCommand handles `baifo provider <verb> <name>`.
func runProviderCommand(dir string, args []string) exitCode {
	if len(args) < 2 {
		fmt.Fprintln(os.Stderr, "Usage: baifo provider auth <name>")
		return exitError
	}

	verb, name := args[0], args[1]
	if verb != "auth" {
		fmt.Fprintf(os.Stderr, "baifo: unknown provider verb %q\n", verb)
		return exitError
	}

	switch name {
	case "anthropic":
		if err := anthropic.RunOAuthFlow(dir); err != nil {
			fmt.Fprintf(os.Stderr, "baifo: %v\n", err)
			return exitError
		}
		return exitOK
	default:
		fmt.Fprintf(os.Stderr, "baifo: provider %q does not support auth\n", name)
		return exitError
	}
}
