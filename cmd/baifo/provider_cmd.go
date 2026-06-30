// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"
	"os"

	"github.com/achetronic/baifo/internal/config"
	"github.com/achetronic/baifo/internal/providers"
)

// runProviderCommand handles `baifo provider <verb> <name>`. The name
// refers to a providers[] entry in baifo.yaml; the auth flow is picked
// by the entry's type, so several providers of the same type keep
// separate credentials.
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

	cfg, err := config.Load(config.FilePath(dir))
	if err != nil {
		fmt.Fprintf(os.Stderr, "baifo: load config: %v\n", err)
		return exitError
	}

	var entry *config.ProviderEntry
	for i := range cfg.Providers {
		if cfg.Providers[i].Name == name {
			entry = &cfg.Providers[i]
			break
		}
	}
	if entry == nil {
		fmt.Fprintf(os.Stderr, "baifo: no provider named %q in baifo.yaml\n", name)
		return exitError
	}

	flow, ok := providers.AuthFlowFor(entry.Type)
	if !ok {
		fmt.Fprintf(os.Stderr, "baifo: provider %q is of type %q, which has no auth flow\n", name, entry.Type)
		return exitError
	}

	if entry.Auth != "oauth" {
		fmt.Fprintf(os.Stderr, "note: provider %q has auth %q in baifo.yaml; set `auth: oauth` for the login to be used\n", name, entry.Auth)
	}

	if err := flow(dir, name); err != nil {
		fmt.Fprintf(os.Stderr, "baifo: %v\n", err)
		return exitError
	}
	return exitOK
}
