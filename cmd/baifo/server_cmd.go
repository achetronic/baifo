// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/achetronic/baifo/internal/app"
	"github.com/achetronic/baifo/internal/config"
	"github.com/achetronic/baifo/internal/server"
)

// runServerCommand handles `baifo server [flags]`. It boots the App
// (config, providers, MCPs, workers, ...) exactly like the TUI does
// but instead of attaching BubbleTea it serves HTTP forever.
//
// The TUI can keep running in the same binary (no flag = TUI); this
// is the path you take when you want the daemon by itself, e.g. as
// a systemd unit or on a remote host the TUI connects to.
func runServerCommand(dir string, args []string) exitCode {
	fs := flag.NewFlagSet("baifo server", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		fmt.Fprintln(os.Stderr, "Usage: baifo server [flags]")
		fmt.Fprintln(os.Stderr, "\nRun the A2A API server in the background without starting the TUI.")
		fmt.Fprintln(os.Stderr, "\nFlags:")
		fs.PrintDefaults()
	}

	host := fs.String("host", "", "bind address (default from baifo.yaml or 127.0.0.1)")
	port := fs.Int("port", 0, "bind port (default from baifo.yaml or 7777)")
	publicURL := fs.String("public-url", "", "URL embedded in Agent Cards (default http://host:port)")

	if err := fs.Parse(args); err != nil {
		return exitError
	}

	cfg, err := config.Load(config.FilePath(dir))
	if err != nil {
		fmt.Fprintf(os.Stderr, "baifo server: load config: %v\n", err)
		return exitError
	}

	// Honour the master switch. `baifo server` exists to serve A2A, so
	// refusing to boot when it is disabled is the honest behaviour:
	// otherwise the daemon would silently serve endpoints the operator
	// turned off in baifo.yaml. Flip a2a.enabled: true to run it.
	if !cfg.A2A.Enabled {
		fmt.Fprintln(os.Stderr, "baifo server: a2a.enabled is false in baifo.yaml - nothing to serve. Set a2a.enabled: true to run the daemon.")
		return exitError
	}

	// Pull defaults from baifo.yaml when the flag is empty.
	if *host == "" {
		*host = cfg.A2A.Host
	}
	if *port == 0 {
		*port = cfg.A2A.Port
	}
	if *publicURL == "" {
		*publicURL = cfg.A2A.PublicURL
	}

	a, err := app.New(context.Background(), cfg, dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "baifo server: %v\n", err)
		return exitError
	}
	defer func() { _ = a.Close() }()

	// Resolve the optional bearer token: a literal value is used as-is,
	// a ${secret:NAME} reference is expanded from the secrets store so
	// the token never sits in baifo.yaml in plaintext. Empty = no auth.
	authToken, err := a.ExpandSecretString(cfg.A2A.Credentials.Token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "baifo server: resolve a2a token: %v\n", err)
		return exitError
	}

	srv := server.New(a, server.Config{
		Host:      *host,
		Port:      *port,
		PublicURL: *publicURL,
		AuthToken: authToken,
	})

	// Translate SIGINT / SIGTERM into context cancellation so the
	// HTTP shutdown gets a fair chance to drain in-flight requests.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	// Pick up baifo.yaml edits while the daemon runs: the App watcher
	// reloads config from disk and fires SubscribeReload; rebuild the
	// A2A handlers so the live daemon reflects the new topology.
	go func() {
		reloads := a.SubscribeReload()
		for {
			select {
			case <-ctx.Done():
				return
			case _, ok := <-reloads:
				if !ok {
					return
				}
				srv.Rebuild()
			}
		}
	}()

	if err := srv.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "baifo server: %v\n", err)
		return exitError
	}
	return exitOK
}
