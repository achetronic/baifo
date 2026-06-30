// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

// Command baifo is the entry point of the agent harness. See
// .agents/AGENTS.md for the high-level overview.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/achetronic/baifo/internal/config"
	"github.com/achetronic/baifo/internal/version"
)

// exitCode is what the program returns to the shell. We funnel every
// error through here to keep main() short and avoid scattered os.Exit
// calls that bypass deferred cleanup.
type exitCode int

const (
	exitOK    exitCode = 0
	exitError exitCode = 1
)

func main() {
	os.Exit(int(run(os.Args[1:])))
}

// run is the testable counterpart of main(). It returns exitOK on
// success and exitError on any handled failure.
func run(args []string) exitCode {
	fs := flag.NewFlagSet("baifo", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	configDirFlag := fs.String("config-dir", "", "Path to .baifo directory")
	versionFlag := fs.Bool("version", false, "Print version information and exit")

	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "baifo - multidisciplinary local assistant")
		fmt.Fprintln(os.Stderr, "\nUsage:")
		fmt.Fprintln(os.Stderr, "  baifo [flags]            Launch the interactive TUI (default)")
		fmt.Fprintln(os.Stderr, "  baifo <command> [flags]  Run a specific command in headless mode")
		fmt.Fprintln(os.Stderr, "\nCommands:")
		fmt.Fprintln(os.Stderr, "  chat      Start a headless REPL or send a one-shot message")
		fmt.Fprintln(os.Stderr, "  server    Run the A2A API server in the background")
		fmt.Fprintln(os.Stderr, "  secrets   Manage the encrypted secrets store")
		fmt.Fprintln(os.Stderr, "\nGlobal Flags:")
		fs.PrintDefaults()
		fmt.Fprintln(os.Stderr, "\nRun 'baifo <command> -h' for command-specific help.")
	}

	if err := fs.Parse(args); err != nil {
		return exitError
	}

	if *versionFlag {
		fmt.Printf("baifo %s (commit: %s, built at: %s)\n", version.Tag(), version.Commit(), version.Date())
		return exitOK
	}

	rest := fs.Args()

	// Subcommands need a config dir but must NOT trigger the first-run
	// wizard implicitly: a user typing `baifo secrets list` on a fresh
	// machine expects an error, not an interactive prompt.
	if len(rest) > 0 {
		dir, err := config.DiscoverDir(*configDirFlag)
		if err != nil {
			fmt.Fprintf(os.Stderr, "baifo: %v\n", err)
			fmt.Fprintln(os.Stderr, "Run `baifo` (no args) to initialise a configuration directory.")
			return exitError
		}
		return runSubcommand(dir, rest)
	}

	// No subcommand: this is the TUI entry point. If no config dir is
	// found we offer first-run initialisation.
	dir, err := config.DiscoverDir(*configDirFlag)
	if err != nil {
		if !errors.Is(err, config.ErrDirNotFound) {
			fmt.Fprintf(os.Stderr, "baifo: %v\n", err)
			return exitError
		}
		res, werr := runFirstRunWizard(*configDirFlag)
		if werr != nil {
			fmt.Fprintf(os.Stderr, "baifo: %v\n", werr)
			return exitError
		}
		if !res.launchTUI {
			// OAuth provider: there are no usable credentials until the
			// user logs in, so print the exact command and exit instead
			// of dropping them into a degraded TUI.
			fmt.Printf("\nProvider %q is set up to use OAuth.\n"+
				"Log in once, then start baifo:\n\n"+
				"    baifo provider auth %s\n\n", res.oauthName, res.oauthName)
			return exitOK
		}
		dir = res.dir
	}

	return runTUI(dir)
}
