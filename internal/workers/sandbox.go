// Copyright 2025 - Alby Hernández and the baifo contributors
// SPDX-License-Identifier: Apache-2.0

package workers

import (
	"fmt"
	"os"
	"path/filepath"
)

// SandboxAllocator owns the per-worker filesystem workspace. Every
// worker gets an isolated directory under <DataDir>/workspaces/<id>/
// that the filesystem-MCP can chroot into. The directory is created
// at Spawn time and removed at Cleanup time (typically right after
// Collect or on Shutdown).
//
// This is NOT a security boundary — a process running inside the
// worker can absolutely escape with an exec(2). The point of the
// per-worker directory is hygiene: a known, predictable cwd that
// gets garbage-collected after the worker dies so the filesystem
// doesn't accumulate scratch from every spawn. Real isolation is
// the user's responsibility (containers, namespaces, VMs).
type SandboxAllocator struct {
	// DataDir is the absolute path of the .baifo/data directory.
	// Workspaces live under <DataDir>/workspaces/.
	DataDir string
}

// Allocate returns the absolute workspace path for the worker,
// creating the directory tree if needed.
func (a *SandboxAllocator) Allocate(workerID string) (string, error) {
	if a.DataDir == "" {
		return "", fmt.Errorf("workspace allocation requires DataDir")
	}
	path := filepath.Join(a.DataDir, "workspaces", workerID)
	if err := os.MkdirAll(path, 0o700); err != nil {
		return "", fmt.Errorf("create workspace: %w", err)
	}
	return path, nil
}

// Cleanup removes the worker's workspace. Idempotent: returns nil
// for unknown ids and for unconfigured allocators.
func (a *SandboxAllocator) Cleanup(workerID string) error {
	if a.DataDir == "" {
		return nil
	}
	path := filepath.Join(a.DataDir, "workspaces", workerID)
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("remove workspace: %w", err)
	}
	return nil
}
