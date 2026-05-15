// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	tea "charm.land/bubbletea/v2"

	"github.com/achetronic/baifo/internal/app"
	"github.com/achetronic/baifo/internal/config"
	_ "github.com/achetronic/baifo/internal/providers/allproviders"
	"github.com/achetronic/baifo/internal/server"
	"github.com/achetronic/baifo/internal/tui"
	"github.com/achetronic/baifo/internal/version"
)

// runTUI is the interactive entry point. It loads the config, builds
// the App facade, and hands a tea.Model to BubbleTea. Errors during
// boot are printed to stderr; if the App boots without a usable root
// agent, the TUI still launches and shows the configuration-required
// state.
func runTUI(dir string) exitCode {
	cfg, err := config.Load(config.FilePath(dir))
	if err != nil {
		fmt.Fprintf(os.Stderr, "baifo: load config: %v\n", err)
		return exitError
	}

	a, err := app.New(context.Background(), cfg, dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "baifo: %v\n", err)
		return exitError
	}
	defer func() { _ = a.Close() }()

	// When A2A is enabled in baifo.yaml, host the server in-process
	// alongside the TUI so a single `baifo` invocation both chats and
	// serves. The server runs in its own goroutine and is shut down
	// when the TUI exits (stopServer cancels its context). `baifo
	// server` remains the headless-only path; here it is an opt-in
	// extra driven purely by config.
	stopServer := func() {}
	if cfg.A2A.Enabled {
		// Resolve the optional bearer token (literal or ${secret:NAME})
		// the same way the headless daemon does. A resolution error is
		// fatal: silently serving unauthenticated when the operator
		// asked for auth would be worse than refusing to launch.
		authToken, err := a.ExpandSecretString(cfg.A2A.Credentials.Token)
		if err != nil {
			fmt.Fprintf(os.Stderr, "baifo: resolve a2a token: %v\n", err)
			return exitError
		}
		srvCtx, cancel := context.WithCancel(context.Background())
		srv := server.New(a, server.Config{
			Host:      cfg.A2A.Host,
			Port:      cfg.A2A.Port,
			PublicURL: cfg.A2A.PublicURL,
			AuthToken: authToken,
		})
		go func() {
			// The TUI owns the screen, so we route any server error
			// through slog (the app already configured the sink)
			// rather than stderr, which would corrupt the display.
			if err := srv.Run(srvCtx); err != nil {
				slog.Error("a2a server stopped", "error", err)
			}
		}()
		stopServer = cancel
	}
	defer stopServer()

	model := tui.NewModelWithAutoScroll(a, cfg.Theme.NerdFonts, version.Tag(), cfg.Runtime.ChatAutoScrollEnabled(), cfg.Runtime.ChatKeepToolsExpandedEnabled())
	p := tea.NewProgram(model)
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "baifo: tui: %v\n", err)
		return exitError
	}
	return exitOK
}
