// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

package filesystem

import (
	"fmt"
	"os"
	"os/exec"
	"sync"
	"time"
)

// undoStore keeps a one-deep history of file contents indexed by
// absolute path so that write_file and edit_file can be reverted.
//
// Extracted from filesystem-mcp/internal/state/undo.go without
// behavioural changes; renamed to lowercase so it stays private to the
// builtin package.
type undoStore struct {
	mu      sync.Mutex
	entries map[string]undoEntry
}

type undoEntry struct {
	path    string
	content []byte
	existed bool
}

func newUndoStore() *undoStore {
	return &undoStore{entries: make(map[string]undoEntry)}
}

// Save snapshots the current content of path. If the file does not
// exist, we still record the entry so a later Restore can re-delete it.
func (u *undoStore) Save(path string) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	entry := undoEntry{path: path}
	content, err := os.ReadFile(path)
	switch {
	case err == nil:
		entry.existed = true
		entry.content = content
	case os.IsNotExist(err):
		entry.existed = false
	default:
		return fmt.Errorf("save undo state for %q: %w", path, err)
	}
	u.entries[path] = entry
	return nil
}

// Restore reverts path to its last saved state and removes the entry.
func (u *undoStore) Restore(path string) error {
	u.mu.Lock()
	defer u.mu.Unlock()

	entry, ok := u.entries[path]
	if !ok {
		return fmt.Errorf("no undo history for %q", path)
	}

	if !entry.existed {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("undo (remove) %q: %w", path, err)
		}
	} else {
		if err := os.WriteFile(path, entry.content, 0o644); err != nil {
			return fmt.Errorf("undo (restore) %q: %w", path, err)
		}
	}
	delete(u.entries, path)
	return nil
}

// scratchStore is an in-memory key-value store used by the scratch
// tool so agents can stash short notes between tool calls without
// re-emitting them in the conversation.
type scratchStore struct {
	mu   sync.RWMutex
	data map[string]string
}

func newScratchStore() *scratchStore {
	return &scratchStore{data: make(map[string]string)}
}

func (s *scratchStore) Set(key, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[key] = value
}

func (s *scratchStore) Get(key string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.data[key]
	if !ok {
		return "", fmt.Errorf("key %q not found", key)
	}
	return v, nil
}

func (s *scratchStore) Delete(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.data, key)
}

func (s *scratchStore) List() map[string]string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make(map[string]string, len(s.data))
	for k, v := range s.data {
		out[k] = v
	}
	return out
}

// processInfo is the public view of a background command. Field names
// are lowercase in JSON to match what agents see; the cmd handle stays
// out of the JSON encoding because it has no exported fields.
type processInfo struct {
	ID        string    `json:"id"`
	Command   string    `json:"command"`
	WorkDir   string    `json:"workdir"`
	StartedAt time.Time `json:"started_at"`
	Done      bool      `json:"done"`
	ExitCode  int       `json:"exit_code"`

	stdout *safeBuffer
	stderr *safeBuffer
	cmd    *exec.Cmd
}

// safeBuffer is a tiny mutex-guarded buffer; we only need Write +
// String so we don't reach for bytes.Buffer.
type safeBuffer struct {
	mu  sync.Mutex
	buf []byte
}

func (b *safeBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.buf = append(b.buf, p...)
	return len(p), nil
}

func (b *safeBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}

// processStore manages background processes started via exec(background=true).
type processStore struct {
	mu        sync.Mutex
	processes map[string]*processInfo
	counter   int
}

func newProcessStore() *processStore {
	return &processStore{processes: make(map[string]*processInfo)}
}

// Start runs a command in the background and returns its assigned ID.
// stdout/stderr are captured into safeBuffer so Status can read them
// without racing the still-running goroutine.
func (ps *processStore) Start(command, workdir string, env []string) (string, error) {
	ps.mu.Lock()
	ps.counter++
	id := fmt.Sprintf("proc_%d", ps.counter)
	ps.mu.Unlock()

	cmd := exec.Command("sh", "-c", command)
	if workdir != "" {
		cmd.Dir = workdir
	}
	if len(env) > 0 {
		cmd.Env = env
	}
	stdoutBuf, stderrBuf := &safeBuffer{}, &safeBuffer{}
	cmd.Stdout = stdoutBuf
	cmd.Stderr = stderrBuf

	info := &processInfo{
		ID:        id,
		Command:   command,
		WorkDir:   workdir,
		StartedAt: time.Now(),
		stdout:    stdoutBuf,
		stderr:    stderrBuf,
		cmd:       cmd,
	}
	if err := cmd.Start(); err != nil {
		return "", fmt.Errorf("start process: %w", err)
	}

	ps.mu.Lock()
	ps.processes[id] = info
	ps.mu.Unlock()

	go func() {
		err := cmd.Wait()
		info.Done = true
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				info.ExitCode = exitErr.ExitCode()
			} else {
				info.ExitCode = -1
			}
		}
	}()
	return id, nil
}

// Exec runs a command in the foreground with a hard timeout.
func (ps *processStore) Exec(command, workdir string, env []string, timeout time.Duration) (stdout, stderr string, exitCode int, err error) {
	cmd := exec.Command("sh", "-c", command)
	if workdir != "" {
		cmd.Dir = workdir
	}
	if len(env) > 0 {
		cmd.Env = env
	}
	stdoutBuf, stderrBuf := &safeBuffer{}, &safeBuffer{}
	cmd.Stdout = stdoutBuf
	cmd.Stderr = stderrBuf

	if err := cmd.Start(); err != nil {
		return "", "", -1, fmt.Errorf("start command: %w", err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				return stdoutBuf.String(), stderrBuf.String(), exitErr.ExitCode(), nil
			}
			return stdoutBuf.String(), stderrBuf.String(), -1, err
		}
		return stdoutBuf.String(), stderrBuf.String(), 0, nil
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		return stdoutBuf.String(), stderrBuf.String(), -1, fmt.Errorf("command timed out after %s", timeout)
	}
}

func (ps *processStore) Status(id string) (*processInfo, string, string, error) {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	info, ok := ps.processes[id]
	if !ok {
		return nil, "", "", fmt.Errorf("process %q not found", id)
	}
	return info, info.stdout.String(), info.stderr.String(), nil
}

func (ps *processStore) Kill(id string) error {
	ps.mu.Lock()
	info, ok := ps.processes[id]
	ps.mu.Unlock()
	if !ok {
		return fmt.Errorf("process %q not found", id)
	}
	if info.Done {
		return fmt.Errorf("process %q already exited", id)
	}
	if info.cmd.Process == nil {
		return fmt.Errorf("process %q has no OS process", id)
	}
	return info.cmd.Process.Kill()
}

func (ps *processStore) List() []*processInfo {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	out := make([]*processInfo, 0, len(ps.processes))
	for _, info := range ps.processes {
		out = append(out, info)
	}
	return out
}
