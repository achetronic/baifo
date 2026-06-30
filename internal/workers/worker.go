// SPDX-License-Identifier: Apache-2.0

// Package workers owns the lifecycle of every sub-agent spawned by the
// root. See .agents/WORKER_RUNTIME.md and decision #5 for the design.
//
// A Worker wraps an ADK runner running in its own goroutine. The
// Manager keeps a process-wide registry of live workers, allocates
// their sandboxes, multiplexes their event streams, and exposes the
// lifecycle primitives (spawn, query, list, inspect, collect, kill)
// that the root agent's spawn tools call into.
package workers

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Kind classifies a worker by how it was spawned.
type Kind int

const (
	// KindStatic means the worker was instantiated from an
	// AgentTemplate in agents.yaml.
	KindStatic Kind = iota

	// KindDynamic means the worker was built at runtime from a Spec
	// the root produced. Phase 6 work; defined here so the rest of
	// the Manager stays kind-agnostic.
	KindDynamic
)

// Status enumerates the lifecycle states described in WORKER_RUNTIME.md.
type Status int

const (
	// StatusRunning means the worker is processing input.
	StatusRunning Status = iota

	// StatusIdle means the worker is waiting for the next query.
	StatusIdle

	// StatusDone means the worker terminated normally.
	StatusDone

	// StatusFailed means the worker terminated with an error.
	StatusFailed

	// StatusKilled means the worker was cancelled via kill_agent or
	// at shutdown.
	StatusKilled
)

// String renders Status as a lowercase word for the spawn tools.
func (s Status) String() string {
	switch s {
	case StatusRunning:
		return "running"
	case StatusIdle:
		return "idle"
	case StatusDone:
		return "done"
	case StatusFailed:
		return "failed"
	case StatusKilled:
		return "killed"
	}
	return "unknown"
}

// ErrUnknownWorker is returned by Manager methods when the worker_id
// does not exist in the registry.
var ErrUnknownWorker = errors.New("unknown worker")

// ErrWorkerNameConflict is returned by Spawn when the requested name
// collides with a live worker.
var ErrWorkerNameConflict = errors.New("worker name already in use")

// ErrCollectTimeout is returned by Collect when the configured wait
// elapses before the worker reaches idle/done.
var ErrCollectTimeout = errors.New("collect timed out")

// Spec is the minimal description the Manager needs to spawn a worker.
// It is intentionally decoupled from agent.Spec so static and dynamic
// callers can build it from their own sources (agents.yaml templates
// vs the LLM's spawn_dynamic_agent payload).
type Spec struct {
	Kind Kind
	Name string
	// Description is a one-liner the worker's parent (typically the
	// root) uses to advertise the worker's purpose. Forwarded to
	// llmagent.Config.Description so ADK's agent-transfer routing
	// can describe it. Optional; an empty string is fine.
	Description string
	Prompt      string
	Provider    string
	Model       string
	Skills      []string
	MCPs        []string

	// Reasoning is the optional reasoning-effort knob forwarded to the
	// agent.Builder ("minimal" / "low" / "medium" / "high"; empty =
	// model default). Lets the root dial a worker's thinking up or down
	// to fit the sub-task.
	Reasoning string

	// ReasoningAPI optionally overrides the Anthropic reasoning API the
	// worker's model uses ("enabled" or "adaptive"); empty auto-detects.
	// Forwarded to the agent.Builder. Ignored by non-anthropic providers.
	ReasoningAPI string

	// AllowedSecrets is forwarded to the agent.Builder so the secrets
	// pipeline scopes correctly.
	AllowedSecrets []string

	// InitialMessage, when non-empty, is sent to the worker right
	// after it is created so it starts processing immediately.
	InitialMessage string
}

// WorkerInfo is a snapshot of a worker's public state. The Manager
// hands these out for list_agents / inspect_agent / TUI listings so
// the caller never gets a live pointer to internal state.
type WorkerInfo struct {
	ID        string
	Name      string
	Kind      Kind
	Status    Status
	Spec      Spec
	Sandbox   string
	StartedAt time.Time
	UpdatedAt time.Time
	LastEvent string
	Output    string
	Err       string
}

// Worker is the manager's internal record. We expose a tiny method
// surface so callers can drive supervision without reaching into the
// struct directly.
type Worker struct {
	id        string
	name      string
	kind      Kind
	spec      Spec
	sandbox   string
	startedAt time.Time

	// runtime is the per-worker driver (ADK runner + queue). It is
	// nil until the Manager has wired it (see workers.Driver in the
	// follow-up PR that hooks ADK in). Until then a stub fulfills
	// the lifecycle so unit tests can drive everything without a
	// real LLM.
	runtime Driver

	mu        sync.RWMutex
	status    Status
	updatedAt time.Time
	lastEvent string
	output    string
	err       string

	cancel context.CancelFunc

	// events is the per-worker fan-out. Subscribers receive a copy of
	// every event written by the driver.
	events *EventBus

	// history is a bounded ring buffer mirroring every event the
	// worker has produced. The Manager wires a goroutine that
	// appends from the bus on spawn, so subscribers attached later
	// (typical case: the TUI opening a spy view) can show context
	// instead of starting at "now".
	history *eventHistory

	// done is closed when the worker reaches a terminal state.
	done chan struct{}
}

// Info returns a snapshot of the worker's public state.
func (w *Worker) Info() WorkerInfo {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return WorkerInfo{
		ID:        w.id,
		Name:      w.name,
		Kind:      w.kind,
		Status:    w.status,
		Spec:      w.spec,
		Sandbox:   w.sandbox,
		StartedAt: w.startedAt,
		UpdatedAt: w.updatedAt,
		LastEvent: w.lastEvent,
		Output:    w.output,
		Err:       w.err,
	}
}

// ID returns the worker's stable identifier.
func (w *Worker) ID() string { return w.id }

// Done returns a channel that closes when the worker reaches a
// terminal state (done / failed / killed). Useful for collect_agent.
func (w *Worker) Done() <-chan struct{} { return w.done }

// setStatus mutates the worker's status under the lock and bumps
// UpdatedAt. Returns true when the change is observable (status
// actually transitioned).
func (w *Worker) setStatus(s Status, lastEvent string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.status == s && lastEvent == "" {
		return false
	}
	w.status = s
	w.updatedAt = time.Now()
	if lastEvent != "" {
		w.lastEvent = lastEvent
	}
	return true
}

// markTerminal marks the worker as terminal (done/failed/killed) and
// closes the Done channel exactly once.
func (w *Worker) markTerminal(s Status, output, errMsg, lastEvent string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	switch w.status {
	case StatusDone, StatusFailed, StatusKilled:
		return // already terminal
	}
	w.status = s
	w.updatedAt = time.Now()
	if output != "" {
		w.output = output
	}
	if errMsg != "" {
		w.err = errMsg
	}
	if lastEvent != "" {
		w.lastEvent = lastEvent
	}
	close(w.done)
}

// newWorkerID returns a fresh "w_xxxxxxxx" identifier (8 hex chars of
// the UUID). Short enough to be readable in tool-call args, long
// enough that collision is statistically impossible inside one
// process's lifetime.
func newWorkerID() string {
	return "w_" + uuid.NewString()[:8]
}
