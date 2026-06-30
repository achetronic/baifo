// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package agent

import (
	"fmt"
	"log/slog"
	"time"

	"google.golang.org/adk/agent"
	"google.golang.org/adk/tool"
)

// resilientToolsetTimeout bounds how long the agent waits for one
// external MCP toolset to enumerate its tools at the start of a turn.
// The HTTP transport already caps dial/TLS/header phases, so this is
// a belt-and-braces ceiling that also covers a stdio MCP whose process
// wedges. Generous enough not to trip a healthy-but-slow server.
const resilientToolsetTimeout = 30 * time.Second

// resilientToolset wraps an external MCP tool.Toolset so that a slow,
// unreachable or misbehaving server degrades to "this MCP contributes
// no tools this turn" instead of failing the whole agent turn.
//
// Why this exists: the root agent sees every registered MCP by default
// (mcps: []). Before this wrapper, a single bad entry in baifo.yaml — a
// dead HTTP endpoint, an MCP awaiting OAuth, a crashed stdio process —
// made ListTools error or hang, and the coordinator produced no reply
// at all. With the wrapper the coordinator still answers using the
// tools that did load, and the failure is logged (and visible via
// /mcps test) rather than silently swallowing the user's turn.
type resilientToolset struct {
	inner tool.Toolset
	name  string
}

// newResilientToolset wraps inner. name is the baifo MCP name (for log
// context); inner.Name() is used as a fallback.
func newResilientToolset(name string, inner tool.Toolset) *resilientToolset {
	return &resilientToolset{inner: inner, name: name}
}

func (r *resilientToolset) Name() string { return r.inner.Name() }

// Tools calls the wrapped toolset with a hard timeout and a panic
// guard. On any failure it logs a warning and returns an empty slice
// with a nil error, so the agent keeps the rest of its tools and the
// turn proceeds.
func (r *resilientToolset) Tools(ctx agent.ReadonlyContext) ([]tool.Tool, error) {
	type result struct {
		tools []tool.Tool
		err   error
	}
	done := make(chan result, 1)

	go func() {
		defer func() {
			if rec := recover(); rec != nil {
				done <- result{err: fmt.Errorf("panic: %v", rec)}
			}
		}()
		ts, err := r.inner.Tools(ctx)
		done <- result{tools: ts, err: err}
	}()

	timer := time.NewTimer(resilientToolsetTimeout)
	defer timer.Stop()

	select {
	case res := <-done:
		if res.err != nil {
			slog.Warn("MCP toolset unavailable this turn; skipping its tools",
				"mcp", r.name, "error", res.err)
			return nil, nil
		}
		return res.tools, nil
	case <-timer.C:
		slog.Warn("MCP toolset timed out listing tools; skipping it this turn",
			"mcp", r.name, "timeout", resilientToolsetTimeout)
		return nil, nil
	case <-ctx.Done():
		// The whole invocation was cancelled (user hit Esc, etc.):
		// propagate nothing, just bow out cleanly.
		return nil, nil
	}
}

// Ensure resilientToolset satisfies the interface.
var _ tool.Toolset = (*resilientToolset)(nil)
