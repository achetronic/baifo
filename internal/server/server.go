// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

// Package server hosts the HTTP daemon that exposes baifo's agents
// over A2A and the rest of the system (workers, sessions, secrets,
// config) over a tiny REST surface. The TUI talks to this daemon
// regardless of whether they share a process or live on different
// machines.
//
// PR-1 scope: just the A2A endpoint for the root agent and a health
// probe. Static / dynamic worker exposure and the /api/core REST
// surface land in follow-up PRs.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"google.golang.org/adk/v2/memory"
	"google.golang.org/adk/v2/session"

	"github.com/achetronic/baifo/internal/server/a2a"
)

// Core is the slice of an baifo core (`*app.App`) the HTTP daemon
// actually needs. Declared here, not in internal/app, so the server
// package does not depend on the composition-root package: the
// dependency flows app → server.Core (implicit), not server → app.
// Any sufficiently-rich type that implements these methods can be
// served — useful for tests and for a future split where the daemon
// and core live in different processes.
type Core interface {
	// Agents returns the live ADK agents this core wants exposed
	// over A2A. The slice ordering controls listing order in
	// /api/a2a/.well-known/agent-card.json; today it is just the
	// root, but it will grow to include static templates and any
	// dynamic worker promoted to a stable endpoint.
	Agents() []a2a.AgentEntry

	// SessionService and MemoryService are the persistence layers
	// the A2A executor wires into each agent's runner so the
	// daemon's view matches the in-process Facade's.
	SessionService() session.Service
	MemoryService() memory.Service

	// RootName and RootBuildError power the /healthz response.
	// RootBuildError returns nil when the root is healthy.
	RootName() string
	RootBuildError() error
}

// Config bundles the knobs Server reads at construction. Future
// extensions (TLS, auth tokens, CORS allowlist) slot in here.
type Config struct {
	// Host is the bind address. Defaults to 127.0.0.1 when empty so
	// the daemon stays local unless someone explicitly opts in.
	Host string

	// Port is the TCP port. Defaults to 7777 when zero, matching the
	// config schema.
	Port int

	// PublicURL is the URL embedded in Agent Cards. When empty the
	// server derives it from Host/Port. Override when running behind
	// a reverse proxy.
	PublicURL string

	// AuthToken, when non-empty, turns on bearer authentication: every
	// request must carry `Authorization: Bearer <AuthToken>`. Empty
	// means the server is unauthenticated (the historical default).
	// The caller resolves any ${secret:NAME} reference before passing
	// it here, so this is always the literal expected token.
	AuthToken string
}

// Server is the HTTP daemon. Construct with New, start with Run.
type Server struct {
	cfg   Config
	core  Core
	mux   *http.ServeMux
	a2aH  *a2a.Handler
	httpS *http.Server
}

// New wires the daemon. It does not bind a port yet; that happens in
// Run so unit tests can inspect routing without taking sockets.
func New(core Core, cfg Config) *Server {
	if cfg.Host == "" {
		cfg.Host = "127.0.0.1"
	}
	if cfg.Port == 0 {
		cfg.Port = 7777
	}
	if cfg.PublicURL == "" {
		cfg.PublicURL = fmt.Sprintf("http://%s:%d", cfg.Host, cfg.Port)
	}

	a2aH := a2a.NewHandler(cfg.PublicURL, cfg.AuthToken != "")

	mux := http.NewServeMux()
	// Bearer auth (when configured) guards the A2A surface only.
	// /healthz stays open so load balancers and `baifo`'s own probe
	// can check liveness without a credential.
	mux.Handle(a2a.PathPrefix, withBearerAuth(cfg.AuthToken, a2aH))
	mux.HandleFunc("/healthz", healthHandler(core, a2aH))

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	return &Server{
		cfg:  cfg,
		core: core,
		mux:  mux,
		a2aH: a2aH,
		httpS: &http.Server{
			Addr:         addr,
			Handler:      mux,
			ReadTimeout:  30 * time.Second,
			WriteTimeout: 0, // streaming responses; do not enforce
			IdleTimeout:  120 * time.Second,
		},
	}
}

// Rebuild syncs the A2A handlers to the core's current state.
// Called once from Run and on demand if the core ever notifies that
// the agent topology changed (config reload, dynamic spawn / kill).
func (s *Server) Rebuild() {
	s.a2aH.Rebuild(s.core.Agents(), s.core.SessionService(), s.core.MemoryService())
	slog.Info("a2a handlers rebuilt", "agents", s.a2aH.AgentIDs())
}

// Run blocks listening for HTTP connections until ctx is cancelled
// or ListenAndServe returns. Shutdown drains in-flight requests with
// a 5s grace period.
func (s *Server) Run(ctx context.Context) error {
	s.Rebuild()

	errCh := make(chan error, 1)
	go func() {
		slog.Info("baifo server listening", "addr", s.httpS.Addr, "public_url", s.cfg.PublicURL)
		if err := s.httpS.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.httpS.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
		return nil
	case err, ok := <-errCh:
		if !ok || err == nil {
			return nil
		}
		return fmt.Errorf("serve: %w", err)
	}
}

// Mux exposes the underlying mux so tests can call ServeHTTP directly
// without binding a port. Not part of the public API outside tests.
func (s *Server) Mux() *http.ServeMux { return s.mux }

// healthHandler returns a JSON snapshot describing the daemon and its
// exposed agents. Always returns 200 when the binary is alive.
func healthHandler(core Core, a2aH *a2a.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		payload := map[string]any{
			"status":         "ok",
			"root_name":      core.RootName(),
			"exposed_agents": a2aH.AgentIDs(),
		}
		if buildErr := core.RootBuildError(); buildErr != nil {
			payload["root_build_error"] = buildErr.Error()
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(payload)
	}
}
