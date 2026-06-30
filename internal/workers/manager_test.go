// Copyright 2025 - Alby Hernández and the baifo contributors
// SPDX-License-Identifier: Apache-2.0

package workers

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// fakeDriver scripts a deterministic worker: Send accepts immediately,
// the driver "produces" a configured output after delay, and WaitIdle
// blocks until that fake work is done.
type fakeDriver struct {
	mu        sync.Mutex
	delay     time.Duration
	output    string
	sendErr   error
	idleCh    chan struct{}
	closed    bool
	sendCount int
}

func newFakeDriver(output string, delay time.Duration) *fakeDriver {
	return &fakeDriver{output: output, delay: delay, idleCh: make(chan struct{}, 1)}
}

func (d *fakeDriver) Send(_ context.Context, message string, bus *EventBus, workerID string) error {
	d.mu.Lock()
	d.sendCount++
	err := d.sendErr
	d.mu.Unlock()
	if err != nil {
		return err
	}

	bus.Publish(WorkerEvent{
		WorkerID: workerID,
		Kind:     EventAssistantMessage,
		Payload:  "received: " + message,
	})

	go func() {
		time.Sleep(d.delay)
		d.idleCh <- struct{}{}
	}()
	return nil
}

func (d *fakeDriver) WaitIdle(ctx context.Context) error {
	select {
	case <-d.idleCh:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (d *fakeDriver) Output() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.output
}

func (d *fakeDriver) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closed = true
	return nil
}

// newManager wires a Manager backed by fakeDrivers and an isolated
// sandbox allocator rooted at t.TempDir(). out lets each test
// configure the fake's output text.
func newManager(t *testing.T, out string, delay time.Duration) *Manager {
	t.Helper()
	dataDir := t.TempDir()
	alloc := &SandboxAllocator{DataDir: dataDir}
	return NewManager(ManagerConfig{
		Sandbox: alloc,
		DriverFactory: func(_ string, _ Spec, _ string) (Driver, error) {
			return newFakeDriver(out, delay), nil
		},
		CollectTimeout: 2 * time.Second,
	})
}

func TestSpawnRegistersWorkerAndAllocatesSandbox(t *testing.T) {
	m := newManager(t, "hi", 10*time.Millisecond)
	w, err := m.Spawn(context.Background(), Spec{Name: "alice"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if w.ID() == "" {
		t.Error("worker ID should not be empty")
	}
	info, err := m.Get(w.ID())
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if info.Sandbox == "" {
		t.Error("sandbox should be allocated")
	}
	if !filepath.IsAbs(info.Sandbox) {
		t.Errorf("sandbox path should be absolute: %q", info.Sandbox)
	}
}

func TestSpawnRejectsDuplicateName(t *testing.T) {
	m := newManager(t, "hi", 10*time.Millisecond)
	if _, err := m.Spawn(context.Background(), Spec{Name: "dup"}); err != nil {
		t.Fatalf("first Spawn: %v", err)
	}
	_, err := m.Spawn(context.Background(), Spec{Name: "dup"})
	if !errors.Is(err, ErrWorkerNameConflict) {
		t.Errorf("got %v, want ErrWorkerNameConflict", err)
	}
}

func TestSpawnWithInitialMessageRunsImmediately(t *testing.T) {
	m := newManager(t, "out", 20*time.Millisecond)
	w, err := m.Spawn(context.Background(), Spec{Name: "init", InitialMessage: "go"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	// Should transition to running.
	info, _ := m.Get(w.ID())
	if info.Status != StatusRunning && info.Status != StatusIdle {
		t.Errorf("status after spawn with initial message: %v", info.Status)
	}
}

func TestCollectReturnsOutput(t *testing.T) {
	m := newManager(t, "hello-output", 10*time.Millisecond)
	w, err := m.Spawn(context.Background(), Spec{Name: "c", InitialMessage: "go"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	info, err := m.Collect(context.Background(), w.ID(), time.Second)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if info.Output != "hello-output" {
		t.Errorf("output: got %q, want hello-output", info.Output)
	}
	if _, err := m.Get(w.ID()); !errors.Is(err, ErrUnknownWorker) {
		t.Error("worker should be unregistered after Collect")
	}
}

func TestKillMarksWorkerKilled(t *testing.T) {
	m := newManager(t, "x", 100*time.Millisecond)
	w, err := m.Spawn(context.Background(), Spec{Name: "k", InitialMessage: "go"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if err := m.Kill(w.ID(), ""); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	info, err := m.Get(w.ID())
	if err != nil {
		t.Fatalf("Get after Kill: %v", err)
	}
	if info.Status != StatusKilled {
		t.Errorf("status: got %v, want killed", info.Status)
	}
}

func TestQueryRunsAnotherTurn(t *testing.T) {
	m := newManager(t, "round-2", 5*time.Millisecond)
	w, err := m.Spawn(context.Background(), Spec{Name: "q"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if err := m.Query(context.Background(), w.ID(), "follow up"); err != nil {
		t.Fatalf("Query: %v", err)
	}
	// Drain to idle.
	_, err = m.Collect(context.Background(), w.ID(), time.Second)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
}

func TestCollectTimesOutWhenWorkerNeverFinishes(t *testing.T) {
	m := newManager(t, "slow", time.Hour)
	w, err := m.Spawn(context.Background(), Spec{Name: "slow", InitialMessage: "go"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	_, err = m.Collect(context.Background(), w.ID(), 50*time.Millisecond)
	if !errors.Is(err, ErrCollectTimeout) {
		t.Errorf("got %v, want ErrCollectTimeout", err)
	}
}

func TestListReportsLiveWorkers(t *testing.T) {
	m := newManager(t, "out", 10*time.Millisecond)
	if _, err := m.Spawn(context.Background(), Spec{Name: "a"}); err != nil {
		t.Fatalf("Spawn a: %v", err)
	}
	if _, err := m.Spawn(context.Background(), Spec{Name: "b"}); err != nil {
		t.Fatalf("Spawn b: %v", err)
	}
	got := m.List()
	if len(got) != 2 {
		t.Errorf("List: got %d, want 2", len(got))
	}
}

func TestShutdownDrainsAllWorkers(t *testing.T) {
	m := newManager(t, "out", 200*time.Millisecond)
	for _, n := range []string{"a", "b", "c"} {
		if _, err := m.Spawn(context.Background(), Spec{Name: n, InitialMessage: "go"}); err != nil {
			t.Fatalf("Spawn %s: %v", n, err)
		}
	}
	if err := m.Shutdown(500 * time.Millisecond); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if len(m.List()) != 0 {
		t.Errorf("workers remain after Shutdown: %d", len(m.List()))
	}
}

func TestEventBusFanout(t *testing.T) {
	m := newManager(t, "out", 5*time.Millisecond)
	ch, unsub := m.GlobalBus().Subscribe()
	defer unsub()

	if _, err := m.Spawn(context.Background(), Spec{Name: "e"}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	select {
	case evt := <-ch:
		if evt.Kind != EventStatusChange {
			t.Errorf("first event: got %v, want status_change", evt.Kind)
		}
	case <-time.After(time.Second):
		t.Fatal("no event received from global bus")
	}
}
