// SPDX-License-Identifier: Apache-2.0

package workers

import (
	"context"
	"testing"
	"time"
)

// TestListIsDeterministicAndSorted guards the /worker list view: List
// iterated the workers map directly, so the order reshuffled on every
// call and the view was impossible to navigate. List must now return a
// stable order (by StartedAt, then ID), identical across repeated calls.
func TestListIsDeterministicAndSorted(t *testing.T) {
	m := newManager(t, "hi", time.Hour) // long delay: workers stay live
	for _, name := range []string{"charlie", "alice", "delta", "bravo", "echo"} {
		if _, err := m.Spawn(context.Background(), Spec{Name: name}); err != nil {
			t.Fatalf("Spawn %q: %v", name, err)
		}
	}

	first := idsOf(m.List())
	if len(first) != 5 {
		t.Fatalf("List returned %d workers, want 5", len(first))
	}

	// Repeated calls must yield the exact same order.
	for i := 0; i < 30; i++ {
		got := idsOf(m.List())
		if len(got) != len(first) {
			t.Fatalf("List length changed: %d vs %d", len(got), len(first))
		}
		for j := range got {
			if got[j] != first[j] {
				t.Fatalf("List order is not stable across calls:\n  call 0: %v\n  call %d: %v", first, i+1, got)
			}
		}
	}

	// And the order must actually be sorted by StartedAt, ID as tie-break.
	infos := m.List()
	for i := 1; i < len(infos); i++ {
		prev, cur := infos[i-1], infos[i]
		if cur.StartedAt.Before(prev.StartedAt) {
			t.Errorf("not sorted by StartedAt: [%d]=%v before [%d]=%v", i, cur.StartedAt, i-1, prev.StartedAt)
		}
		if cur.StartedAt.Equal(prev.StartedAt) && cur.ID < prev.ID {
			t.Errorf("tie not broken by ID: [%d].ID=%q < [%d].ID=%q", i, cur.ID, i-1, prev.ID)
		}
	}
}

func idsOf(infos []WorkerInfo) []string {
	out := make([]string, len(infos))
	for i, info := range infos {
		out[i] = info.ID
	}
	return out
}
