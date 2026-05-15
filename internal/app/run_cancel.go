// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

package app

import (
	"context"
	"iter"
	"slices"

	"github.com/a2aproject/a2a-go/a2asrv"
	"google.golang.org/adk/agent"
	"google.golang.org/adk/plugin"
	"google.golang.org/adk/runner"
	"google.golang.org/adk/server/adka2a"
	"google.golang.org/adk/session"
	"google.golang.org/genai"
)

// run_cancel.go restores caller-side cancellation across the A2A
// execution boundary.
//
// The a2a-go request handler runs every execution in a DETACHED context
// (taskexec.LocalManager uses context.WithoutCancel) so an HTTP client
// disconnect cannot abort a server-side task. That is the right default
// for a network server, but baifo's TUI drives the same handler
// in-process: when the user hits Esc, SendMessage's context is
// cancelled, the TUI stops listening — and the agent run kept going in
// the background, burning tokens and writing late events into the
// session. A new message sent afterwards then interleaved with the
// zombie run, which surfaced as a stuck/garbled chat.
//
// context.WithoutCancel preserves VALUES, only the cancellation is
// dropped. So we smuggle the caller's cancellable context through as a
// value and re-attach its cancellation inside the runner, on the other
// side of the detachment.

// callerCtxKey is the context key under which SendMessage stores its
// own cancellable context so the runner can observe its cancellation.
type callerCtxKey struct{}

// withCallerCancel stores ctx inside itself as a value, so the
// cancellation signal survives the executor's context detachment.
func withCallerCancel(ctx context.Context) context.Context {
	return context.WithValue(ctx, callerCtxKey{}, ctx)
}

// cancellableRunnerProvider mirrors adka2a's default runner provider
// (runner.New over the base config with the executor plugin appended)
// but wraps the runner so Run honours the caller context stored by
// withCallerCancel. Executions without a stored caller context (e.g.
// remote A2A clients over HTTP) behave exactly like the default.
func cancellableRunnerProvider(base runner.Config) adka2a.RunnerProvider {
	return func(ctx context.Context, reqCtx *a2asrv.RequestContext, p *plugin.Plugin) (adka2a.RunnerConfig, adka2a.Runner, error) {
		cfg := base
		cfg.PluginConfig.Plugins = append(slices.Clone(cfg.PluginConfig.Plugins), p)
		r, err := runner.New(cfg)
		if err != nil {
			return adka2a.RunnerConfig{}, nil, err
		}
		rc := adka2a.RunnerConfig{
			AppName:        cfg.AppName,
			Agent:          cfg.Agent,
			SessionService: cfg.SessionService,
		}
		return rc, &cancellableRunner{inner: adkRunnerAdapter{r}}, nil
	}
}

// adkRunnerAdapter narrows *runner.Runner (whose Run takes variadic
// options) to the adka2a.Runner interface, mirroring the unexported
// defaultRunner adapter inside adka2a.
type adkRunnerAdapter struct {
	r *runner.Runner
}

func (a adkRunnerAdapter) Run(ctx context.Context, userID, sessionID string, msg *genai.Content, cfg agent.RunConfig) iter.Seq2[*session.Event, error] {
	return a.r.Run(ctx, userID, sessionID, msg, cfg)
}

// cancellableRunner wraps an ADK runner and merges the cancellation of
// the caller context (when present) into the run context.
type cancellableRunner struct {
	inner adka2a.Runner
}

func (c *cancellableRunner) Run(ctx context.Context, userID, sessionID string, msg *genai.Content, cfg agent.RunConfig) iter.Seq2[*session.Event, error] {
	caller, ok := ctx.Value(callerCtxKey{}).(context.Context)
	if !ok {
		return c.inner.Run(ctx, userID, sessionID, msg, cfg)
	}
	return func(yield func(*session.Event, error) bool) {
		runCtx, cancel := context.WithCancel(ctx)
		defer cancel()
		stop := context.AfterFunc(caller, cancel)
		defer stop()
		for ev, err := range c.inner.Run(runCtx, userID, sessionID, msg, cfg) {
			if !yield(ev, err) {
				return
			}
		}
	}
}
