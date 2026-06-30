// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package workers

import (
	"context"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"
)

// Driver is the per-worker engine that the Manager drives. The real
// production implementation wires ADK's runner + session.Service +
// agent.Builder; tests provide a fakeDriver that scripts behaviour
// without any LLM. Keeping the Manager generic over Driver is what
// lets us test lifecycle semantics with zero networking.
type Driver interface {
	// Send delivers a user message to the worker. Returns once the
	// message has been accepted (NOT once the worker has finished).
	// The driver is responsible for publishing every event it
	// produces onto bus.
	Send(ctx context.Context, message string, bus *EventBus, workerID string) error

	// WaitIdle blocks until the worker reaches idle (the previous
	// Send has finished producing events) or ctx is cancelled.
	WaitIdle(ctx context.Context) error

	// Output returns the worker's last assistant message. Called by
	// Collect after WaitIdle.
	Output() string

	// Close releases any resources the driver owns. Called once on
	// Kill / Collect.
	Close() error
}

// DriverFactory builds the Driver for a freshly-spawned worker. The
// Manager calls it once per Spawn, after sandbox allocation. Real
// implementations close over the agent.Builder, providers and MCPs
// registries.
type DriverFactory func(workerID string, spec Spec, sandbox string) (Driver, error)

// ManagerConfig bundles construction-time options.
type ManagerConfig struct {
	Sandbox       *SandboxAllocator
	DriverFactory DriverFactory

	// CollectTimeout is the default upper bound for Collect when no
	// explicit timeout is provided. Defaults to 5 minutes.
	CollectTimeout time.Duration
}

// Manager owns every live worker. It is the single chokepoint for
// the spawn / supervise / collect / kill verbs the root agent uses.
type Manager struct {
	cfg ManagerConfig

	mu        sync.RWMutex
	workers   map[string]*Worker
	byName    map[string]string // friendly name → worker id (live only)
	globalBus *EventBus
}

// NewManager constructs a Manager. The bus, sandbox allocator and
// driver factory must be set in cfg; the Manager does not invent
// sensible defaults for them because the production wiring depends
// on caller-owned state (the App).
func NewManager(cfg ManagerConfig) *Manager {
	if cfg.CollectTimeout == 0 {
		cfg.CollectTimeout = 5 * time.Minute
	}
	return &Manager{
		cfg:       cfg,
		workers:   make(map[string]*Worker),
		byName:    make(map[string]string),
		globalBus: NewEventBus(),
	}
}

// GlobalBus returns the multiplex bus used by the TUI sidebar and the
// audit logger.
func (m *Manager) GlobalBus() *EventBus { return m.globalBus }

// Spawn creates a new worker from spec, allocates its sandbox, builds
// its driver, and registers it. If spec.InitialMessage is non-empty
// the message is delivered before returning so the worker is already
// running by the time Spawn returns its ID.
func (m *Manager) Spawn(ctx context.Context, spec Spec) (*Worker, error) {
	if spec.Name == "" {
		return nil, fmt.Errorf("spec.Name is required")
	}

	m.mu.Lock()
	if _, dup := m.byName[spec.Name]; dup {
		m.mu.Unlock()
		return nil, fmt.Errorf("%w: %q", ErrWorkerNameConflict, spec.Name)
	}
	m.mu.Unlock()

	id := newWorkerID()

	var sandboxPath string
	if m.cfg.Sandbox != nil {
		p, err := m.cfg.Sandbox.Allocate(id)
		if err != nil {
			return nil, fmt.Errorf("allocate workspace: %w", err)
		}
		sandboxPath = p
	}

	drv, err := m.cfg.DriverFactory(id, spec, sandboxPath)
	if err != nil {
		if m.cfg.Sandbox != nil {
			if cerr := m.cfg.Sandbox.Cleanup(id); cerr != nil {
				slog.Warn("sandbox cleanup after driver build failure", "worker", id, "error", cerr)
			}
		}
		return nil, fmt.Errorf("build driver: %w", err)
	}

	workerCtx, cancel := context.WithCancel(context.Background())
	w := &Worker{
		id:        id,
		name:      spec.Name,
		kind:      spec.Kind,
		spec:      spec,
		sandbox:   sandboxPath,
		startedAt: time.Now(),
		updatedAt: time.Now(),
		status:    StatusIdle,
		runtime:   drv,
		events:    NewEventBus(),
		history:   newEventHistory(),
		done:      make(chan struct{}),
		cancel:    cancel,
	}

	m.mu.Lock()
	m.workers[id] = w
	m.byName[spec.Name] = id
	m.mu.Unlock()

	// Wire the per-worker bus into the global multiplex AND into the
	// ring buffer. A single subscriber is attached BEFORE the initial
	// message is sent, so we never miss an early event.
	goWireEvents(w, m.globalBus)

	m.publishStatusChange(w)

	if spec.InitialMessage != "" {
		if err := m.send(workerCtx, w, spec.InitialMessage); err != nil {
			w.markTerminal(StatusFailed, "", err.Error(), "initial send")
			return w, nil
		}
	}
	return w, nil
}

// publishStatusChange emits a StatusChange event so subscribers can
// repaint without polling.
func (m *Manager) publishStatusChange(w *Worker) {
	evt := WorkerEvent{
		WorkerID:  w.id,
		Kind:      EventStatusChange,
		Payload:   w.Info(),
		Timestamp: time.Now(),
	}
	w.events.Publish(evt)
	m.globalBus.Publish(evt)
}

// goWireEvents opens a single subscription to the worker's event bus
// and, in one goroutine, does both jobs that previously required two
// separate subscribers:
//
//  1. appends every event to the worker's ring-buffer history so that
//     late subscribers (e.g. the TUI opening a spy view) can replay
//     context;
//  2. forwards non-StatusChange events to the global multiplex bus
//     (StatusChange is routed directly by publishStatusChange to avoid
//     duplication).
//
// The goroutine exits when the worker bus is closed (i.e. after
// unregister calls w.events.Close).
func goWireEvents(w *Worker, global *EventBus) {
	ch, unsubscribe := w.events.Subscribe()
	go func() {
		defer unsubscribe()
		for evt := range ch {
			w.history.Append(evt)
			if evt.Kind != EventStatusChange {
				global.Publish(evt)
			}
		}
	}()
}

// send is the internal helper used by Spawn (initial message) and
// Query (subsequent messages). It transitions the worker to running,
// pushes the message to the driver, then waits for idle in the
// background; the caller does not block on the worker producing
// events.
func (m *Manager) send(_ context.Context, w *Worker, message string) error {
	w.setStatus(StatusRunning, "query")
	m.publishStatusChange(w)

	if err := w.runtime.Send(context.Background(), message, w.events, w.id); err != nil {
		return err
	}

	go func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if err := w.runtime.WaitIdle(ctx); err != nil {
			w.markTerminal(StatusFailed, "", err.Error(), "driver wait")
			m.publishStatusChange(w)
			return
		}
		w.mu.Lock()
		if w.status == StatusRunning {
			w.status = StatusIdle
			w.updatedAt = time.Now()
			w.output = w.runtime.Output()
		}
		w.mu.Unlock()
		m.publishStatusChange(w)
	}()
	return nil
}

// Query delivers a message to an existing worker and transitions it
// back to running.
func (m *Manager) Query(ctx context.Context, id string, message string) error {
	w, ok := m.byID(id)
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownWorker, id)
	}
	return m.send(ctx, w, message)
}

// List returns a snapshot of every live worker, sorted by start time
// (oldest first), with the worker ID as a tie-breaker. Iterating the
// workers map directly yields a random order on every call, which makes
// the /worker list view reshuffle on each refresh and impossible to
// navigate; this stable ordering is what keeps a selected row put.
func (m *Manager) List() []WorkerInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]WorkerInfo, 0, len(m.workers))
	for _, w := range m.workers {
		out = append(out, w.Info())
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].StartedAt.Equal(out[j].StartedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].StartedAt.Before(out[j].StartedAt)
	})
	return out
}

// Get returns one worker's info or ErrUnknownWorker.
func (m *Manager) Get(id string) (WorkerInfo, error) {
	w, ok := m.byID(id)
	if !ok {
		return WorkerInfo{}, fmt.Errorf("%w: %q", ErrUnknownWorker, id)
	}
	return w.Info(), nil
}

// Inspect peeks at the recent events of a worker. since is a 1-based
// cursor; the Manager keeps the events in the per-worker bus, so
// callers must Subscribe / drain rather than rely on a history
// buffer. For Phase 5 Inspect returns the current snapshot only
// (events_since is a TODO that lands when we wire a ring buffer).
func (m *Manager) Inspect(id string, _ int) (WorkerInfo, error) {
	return m.Get(id)
}

// SubscribeWorker attaches a live subscriber to the worker's event
// bus AND returns the recent-history snapshot so the caller can show
// context immediately. The returned cancel function unsubscribes from
// the live stream when the caller is done (typical case: the TUI
// closes the spy view).
//
// Why history first, channel second: race-free playback of "what
// happened before I subscribed" is impossible with channels alone
// because events emitted between the snapshot and the subscription
// would be lost. We accept a tiny window of duplication: an event
// already in the snapshot may also appear on the channel if it was
// published in the few microseconds between snapshot and subscribe.
// Subscribers de-duplicate on (WorkerID, Index).
func (m *Manager) SubscribeWorker(id string) (history []WorkerEvent, stream <-chan WorkerEvent, cancel func(), err error) {
	w, ok := m.byID(id)
	if !ok {
		return nil, nil, nil, fmt.Errorf("%w: %q", ErrUnknownWorker, id)
	}
	snapshot := w.history.Snapshot()
	ch, unsubscribe := w.events.Subscribe()
	return snapshot, ch, unsubscribe, nil
}

// Collect waits for the worker to reach idle/done/failed/killed,
// captures the output, and removes the worker from the registry. The
// timeout defaults to ManagerConfig.CollectTimeout when zero.
func (m *Manager) Collect(ctx context.Context, id string, timeout time.Duration) (WorkerInfo, error) {
	w, ok := m.byID(id)
	if !ok {
		return WorkerInfo{}, fmt.Errorf("%w: %q", ErrUnknownWorker, id)
	}
	if timeout <= 0 {
		timeout = m.cfg.CollectTimeout
	}

	t := time.NewTimer(timeout)
	defer t.Stop()

	// Fast path: terminal already.
	if info := w.Info(); isTerminal(info.Status) {
		return m.finishCollect(w), nil
	}

	// Slow path: block on the worker's event bus until a StatusChange
	// signals idle/terminal, the done channel closes, the caller
	// cancels, or the timeout fires. No polling whatsoever.
	evCh, unsub := w.events.Subscribe()
	defer unsub()

	for {
		select {
		case <-w.done:
			return m.finishCollect(w), nil
		case <-t.C:
			return w.Info(), ErrCollectTimeout
		case <-ctx.Done():
			return w.Info(), ctx.Err()
		case evt := <-evCh:
			if evt.Kind != EventStatusChange {
				continue
			}
			info := w.Info()
			if info.Status == StatusIdle || isTerminal(info.Status) {
				return m.finishCollect(w), nil
			}
		}
	}
}

// finishCollect captures the output, marks the worker done (when not
// terminal already), unregisters it, and cleans its sandbox up.
func (m *Manager) finishCollect(w *Worker) WorkerInfo {
	info := w.Info()
	if !isTerminal(info.Status) {
		output := w.runtime.Output()
		w.markTerminal(StatusDone, output, "", "collected")
		info = w.Info()
	}
	// Publish the terminal-state transition so subscribers on the
	// global bus (TUI footer chip, Workers overlay, lifecycle
	// feed) see the worker leaving the running set. Without this
	// publish the bus learns nothing about the Done transition
	// because unregister(w) closes the per-worker bus immediately
	// after and goReplay only forwards events that were already
	// in flight. The TUI workers chip would otherwise stay stale
	// until the user opens /workers and the overlay refresh
	// fires a manual ListWorkers().
	m.publishStatusChange(w)
	m.unregister(w)
	return info
}

// Kill cancels the worker's context and marks it killed. The worker
// stays in the registry briefly (until Collect or Shutdown) so the
// root can read its last status.
//
// reason is an optional human-readable explanation that surfaces on
// the next Collect as WorkerInfo.Err (and thus CollectResult.Err for
// the root agent's collect_agent tool). Use it to distinguish e.g.
// "killed by user from TUI" from "killed by shutdown" so the LLM
// can react accordingly. Empty reason falls back to the legacy
// "killed" marker.
func (m *Manager) Kill(id string, reason string) error {
	w, ok := m.byID(id)
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownWorker, id)
	}
	w.cancel()
	if err := w.runtime.Close(); err != nil {
		slog.Warn("closing worker runtime on kill", "worker", id, "error", err)
	}
	w.markTerminal(StatusKilled, "", reason, "killed")
	m.publishStatusChange(w)
	return nil
}

// Shutdown cancels every worker and waits up to grace for them to
// drain. Sandboxes are cleaned up on the way out. Always returns
// nil; partial failures are reported via the event bus.
func (m *Manager) Shutdown(grace time.Duration) error {
	m.mu.RLock()
	workers := make([]*Worker, 0, len(m.workers))
	for _, w := range m.workers {
		workers = append(workers, w)
	}
	m.mu.RUnlock()

	for _, w := range workers {
		w.cancel()
		if err := w.runtime.Close(); err != nil {
			slog.Warn("closing worker runtime on shutdown", "worker", w.id, "error", err)
		}
	}

	deadline := time.Now().Add(grace)
	for _, w := range workers {
		remaining := time.Until(deadline)
		if remaining < 0 {
			remaining = 0
		}
		select {
		case <-w.done:
		case <-time.After(remaining):
		}
		w.markTerminal(StatusKilled, "", "", "shutdown")
		// Publish so external subscribers (TUI chips, the
		// lifecycle feed) observe the terminal transition before
		// the per-worker bus closes under unregister. Mirrors
		// the publish in finishCollect and Kill; without it the
		// chip would survive past shutdown until the process
		// itself dies.
		m.publishStatusChange(w)
		m.unregister(w)
	}
	m.globalBus.Close()
	return nil
}

// byID is the read path used by every supervision method.
func (m *Manager) byID(id string) (*Worker, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	w, ok := m.workers[id]
	return w, ok
}

// unregister removes the worker from both indices and cleans its
// sandbox up. Safe to call multiple times.
func (m *Manager) unregister(w *Worker) {
	m.mu.Lock()
	delete(m.workers, w.id)
	if id, ok := m.byName[w.name]; ok && id == w.id {
		delete(m.byName, w.name)
	}
	m.mu.Unlock()
	w.events.Close()

	if m.cfg.Sandbox != nil {
		if err := m.cfg.Sandbox.Cleanup(w.id); err != nil {
			slog.Warn("sandbox cleanup on worker retire", "worker", w.id, "error", err)
		}
	}
}

// isTerminal returns true when Status is one of done/failed/killed.
func isTerminal(s Status) bool {
	return s == StatusDone || s == StatusFailed || s == StatusKilled
}
