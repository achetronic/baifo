// Copyright 2025 - Alby Hernández and the baifo contributors
// SPDX-License-Identifier: Apache-2.0

// Package filesystem implements the in-process built-in filesystem MCP.
//
// The logic is extracted (not imported) from github.com/achetronic/filesystem-mcp
// internal/tools/*.go and adapted for direct use inside baifo:
//
//   - removed the dependency on github.com/mark3labs/mcp-go (tools are
//     plain Go funcs wrapped as ADK tool.Tool via functiontool.New);
//   - removed RBAC / JWT plumbing (sandbox lives at the Builder level,
//     and v1 grants every agent unrestricted access to its sandbox);
//   - merged write_file + append_file into a single tool with
//     `encoding` and `mode` parameters to dodge LLM JSON-serialisation
//     issues with long content (backticks, newlines, embedded JSON).
//
// All side effects (undo state, scratch values, background processes)
// live in per-instance stores; constructing two Tools yields two
// independent universes, which lets isolated workers have their own.
package filesystem

import "log/slog"

// Config bundles construction-time options.
type Config struct {
	// Logger is used for non-fatal errors (e.g. failure to record undo
	// state). Defaults to slog.Default() when nil.
	Logger *slog.Logger

	// MaxExecOutputChars caps stdout/stderr (each) returned by exec and
	// process_status. 0 means unlimited.
	MaxExecOutputChars int

	// MaxReadFileChars caps the total characters returned by one
	// read_file call. 0 means unlimited.
	MaxReadFileChars int

	// MaxSearchOutputChars caps the total characters (matched lines plus
	// their context) returned by one search call. 0 means unlimited.
	MaxSearchOutputChars int
}

// Tools is the entry point of the built-in filesystem MCP. It owns the
// per-instance mutable state (undo, scratch, processes) and exposes
// methods that match the MCP tool surface one-to-one.
//
// Methods are designed to be called via functiontool.New, but they are
// also usable directly from Go code (e.g. tests, internal scripting).
type Tools struct {
	logger               *slog.Logger
	undo                 *undoStore
	scratch              *scratchStore
	processes            *processStore
	maxExecOutputChars   int
	maxReadFileChars     int
	maxSearchOutputChars int
}

// New constructs a Tools instance with empty stores.
func New(cfg Config) *Tools {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	return &Tools{
		logger:               cfg.Logger,
		undo:                 newUndoStore(),
		scratch:              newScratchStore(),
		processes:            newProcessStore(),
		maxExecOutputChars:   cfg.MaxExecOutputChars,
		maxReadFileChars:     cfg.MaxReadFileChars,
		maxSearchOutputChars: cfg.MaxSearchOutputChars,
	}
}
