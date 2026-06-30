// SPDX-License-Identifier: Apache-2.0

package workers

import (
	"context"
	"testing"
	"time"
)

// TestSubscribeWorker_ReturnsHistoryAndLiveStream is the main contract
// the TUI relies on: when you open a spy view on an already-running
// worker, you must see the events that happened before you arrived
// AND every event that arrives after.
func TestSubscribeWorker_ReturnsHistoryAndLiveStream(t *testing.T) {
	mgr := newTestManager(t, "hello", 30*time.Millisecond)

	w, err := mgr.Spawn(context.Background(), Spec{
		Name:           "spy-target",
		InitialMessage: "first",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	// Wait briefly so the initial Send has put something in the
	// per-worker bus AND therefore in the history buffer.
	time.Sleep(20 * time.Millisecond)

	history, stream, cancel, err := mgr.SubscribeWorker(w.ID())
	if err != nil {
		t.Fatalf("SubscribeWorker: %v", err)
	}
	defer cancel()

	if len(history) == 0 {
		t.Errorf("history should contain the initial spawn / send events, got 0")
	}

	// Trigger a new event AFTER subscription and check it arrives.
	if err := mgr.Query(context.Background(), w.ID(), "second"); err != nil {
		t.Fatalf("Query: %v", err)
	}

	select {
	case evt := <-stream:
		// Any event is fine; the point is that the channel is live.
		_ = evt
	case <-time.After(time.Second):
		t.Fatal("no live event arrived on the stream after Query")
	}
}

// TestSubscribeWorker_UnknownIDErrors documents the contract for the
// missing-worker case so callers know they don't have to special-case
// it themselves.
func TestSubscribeWorker_UnknownIDErrors(t *testing.T) {
	mgr := newTestManager(t, "irrelevant", 10*time.Millisecond)
	_, _, _, err := mgr.SubscribeWorker("w_doesnotexist")
	if err == nil {
		t.Fatal("SubscribeWorker on unknown id: expected error, got nil")
	}
}

// newTestManager mirrors the helper the other manager tests use, but
// is inlined here to keep this file self-contained.
func newTestManager(t *testing.T, output string, delay time.Duration) *Manager {
	t.Helper()
	return NewManager(ManagerConfig{
		Sandbox: &SandboxAllocator{DataDir: t.TempDir()},
		DriverFactory: func(_ string, _ Spec, _ string) (Driver, error) {
			return newFakeDriver(output, delay), nil
		},
	})
}
