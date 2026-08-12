// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

// Package memory wraps the long-term memory toolset shipped by
// adk-utils-go behind the same `Tools.ADKTools()` shape every other
// baifo-owned toolset uses (spawn, todos, meta). It exists so the
// composition root (internal/app) can ask one consistent question
// to every tool source ("give me your ADK tools") instead of
// open-coding the external library's slightly different constructor
// pattern at the call site.
//
// The actual tools (search_memory, save_to_memory, update_memory,
// delete_memory) come straight from
// github.com/achetronic/adk-utils-go/tools/memory. We do NOT
// reimplement them — this package is plumbing only.
package memory

import (
	"google.golang.org/adk/v2/memory"
	"google.golang.org/adk/v2/tool"

	memorytoolset "github.com/achetronic/adk-utils-go/tools/memory"
)

// Tools bundles the deps needed to construct the memory toolset.
// MemoryService is the long-term store (in baifo that's
// *facts.Store). AppName is forwarded to the toolset so the agent's
// tool calls write under the right namespace.
type Tools struct {
	MemoryService memory.Service
	AppName       string
}

// New returns a Tools ready for ADKTools(). Kept as a constructor
// for symmetry with the rest of internal/tools/*.
func New(svc memory.Service, appName string) *Tools {
	return &Tools{MemoryService: svc, AppName: appName}
}

// ADKTools returns the four memory tools. Returns nil (not an
// error) when MemoryService is nil — that matches the pre-existing
// boot semantics where a missing facts store silently disables the
// surface rather than crashing the chat.
func (t *Tools) ADKTools() ([]tool.Tool, error) {
	if t == nil || t.MemoryService == nil {
		return nil, nil
	}
	ts, err := memorytoolset.NewToolset(memorytoolset.ToolsetConfig{
		MemoryService: t.MemoryService,
		AppName:       t.AppName,
	})
	if err != nil {
		return nil, err
	}
	return ts.Tools(nil)
}
