// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

package workers

import (
	"context"
	"testing"
	"time"
)

// TestKillReasonSurfacesOnCollect documents the contract Alby asked
// for on 16 nov 2026: when a human kills a worker from the TUI, the
// reason must reach the root agent through the next Collect, so the
// LLM can distinguish a user-driven cancellation from a system
// failure and decide whether to abort the delegated task.
func TestKillReasonSurfacesOnCollect(t *testing.T) {
	mgr := newTestManager(t, "ignored", 5*time.Minute) // long delay so the worker stays alive
	w, err := mgr.Spawn(context.Background(), Spec{Name: "victim", InitialMessage: "go"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	const reason = "killed by user from TUI"
	if err := mgr.Kill(w.ID(), reason); err != nil {
		t.Fatalf("Kill: %v", err)
	}

	info, err := mgr.Collect(context.Background(), w.ID(), time.Second)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if info.Status != StatusKilled {
		t.Errorf("status after kill+collect = %v, want killed", info.Status)
	}
	if info.Err != reason {
		t.Errorf("err after kill = %q, want %q", info.Err, reason)
	}
}

// TestKillWithoutReasonStillWorks documents the legacy contract:
// Kill with an empty reason leaves Err empty (status alone is enough
// to convey \"killed\"). This way the agent-driven kill_agent tool
// can keep its old behaviour when the LLM does not bother to pass
// a reason.
func TestKillWithoutReasonStillWorks(t *testing.T) {
	mgr := newTestManager(t, "ignored", 5*time.Minute)
	w, err := mgr.Spawn(context.Background(), Spec{Name: "victim2", InitialMessage: "go"})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if err := mgr.Kill(w.ID(), ""); err != nil {
		t.Fatalf("Kill: %v", err)
	}
	info, err := mgr.Collect(context.Background(), w.ID(), time.Second)
	if err != nil {
		t.Fatalf("Collect: %v", err)
	}
	if info.Status != StatusKilled {
		t.Errorf("status = %v, want killed", info.Status)
	}
	if info.Err != "" {
		t.Errorf("err with empty reason = %q, want empty", info.Err)
	}
}
