// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package app

import (
	"google.golang.org/adk/memory"
	"google.golang.org/adk/session"

	"github.com/achetronic/baifo/internal/server/a2a"
)

// Agents implements server.Core. Returns the live ADK agents this
// App wants served over A2A. Today this is just the root; later
// PRs append the static worker templates and any dynamic agents
// currently registered.
//
// We return a2a.AgentEntry directly rather than a private DTO so
// the server package doesn't have to import internal/app for the
// type. This keeps the dependency direction app -> server (the
// composition root knows the server) instead of the inverse.
func (a *App) Agents() []a2a.AgentEntry {
	a.mu.RLock()
	defer a.mu.RUnlock()

	if a.root == nil || a.root.Agent == nil {
		return nil
	}

	desc := "baifo root agent"
	name := "baifo"
	streaming := true
	if root := a.rootTemplate(); root != nil {
		if root.Description != "" {
			desc = root.Description
		}
		if root.Name != "" {
			name = root.Name
		}
		streaming = a.providers.StreamingEnabled(root.LLM.Effective())
	}

	return []a2a.AgentEntry{
		{
			ID:          "root",
			Name:        name,
			Description: desc,
			Agent:       a.root.Agent,
			Streaming:   streaming,
		},
	}
}

// SessionService exposes the App's session service so the A2A
// executors share the same persistence layer as the in-process
// Facade. Pointer return is fine: the service is safe for concurrent
// use and outlives every per-agent executor.
func (a *App) SessionService() session.Service {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.sessions
}

// MemoryService returns the long-term memory service backing the
// facts store. ADK's adka2a executor accepts a memory.Service for
// search_memory / save_to_memory tools; we hand over the same one
// the root agent already uses so a conversation that goes through
// the A2A boundary sees the same facts as the in-process path.
//
// Returns nil when no facts store is wired (degraded boot); the
// executor handles a nil service gracefully.
func (a *App) MemoryService() memory.Service {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if a.facts == nil {
		return nil
	}
	// facts.Store implements memory.Service. Assert here so a future
	// refactor that breaks the contract surfaces as a compile error
	// in this single place, not as a runtime nil at server boot.
	return a.facts
}
