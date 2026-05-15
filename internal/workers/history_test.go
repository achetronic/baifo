// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

package workers

import "testing"

func TestEventHistory_AppendBelowCapacity(t *testing.T) {
	h := newEventHistory()
	for i := 0; i < 5; i++ {
		h.Append(WorkerEvent{Index: i + 1})
	}
	got := h.Snapshot()
	if len(got) != 5 {
		t.Fatalf("Snapshot len = %d, want 5", len(got))
	}
	for i, evt := range got {
		if evt.Index != i+1 {
			t.Errorf("slot %d index = %d, want %d", i, evt.Index, i+1)
		}
	}
}

func TestEventHistory_RingWrap(t *testing.T) {
	h := newEventHistory()
	// Append 1.5x the capacity, so the buffer wraps and the oldest
	// half is overwritten.
	total := historyCapacity + historyCapacity/2
	for i := 0; i < total; i++ {
		h.Append(WorkerEvent{Index: i + 1})
	}
	got := h.Snapshot()
	if len(got) != historyCapacity {
		t.Fatalf("after wrap Snapshot len = %d, want %d", len(got), historyCapacity)
	}
	// The oldest visible event should be total-historyCapacity+1
	// (1-based). Verify the first and last to catch off-by-ones.
	wantFirst := total - historyCapacity + 1
	if got[0].Index != wantFirst {
		t.Errorf("oldest index = %d, want %d", got[0].Index, wantFirst)
	}
	if got[len(got)-1].Index != total {
		t.Errorf("newest index = %d, want %d", got[len(got)-1].Index, total)
	}
}

func TestEventHistory_LenTracksFill(t *testing.T) {
	h := newEventHistory()
	if h.Len() != 0 {
		t.Errorf("fresh history Len = %d, want 0", h.Len())
	}
	h.Append(WorkerEvent{Index: 1})
	if h.Len() != 1 {
		t.Errorf("after one append Len = %d, want 1", h.Len())
	}
	for i := 0; i < historyCapacity*2; i++ {
		h.Append(WorkerEvent{Index: i + 2})
	}
	if h.Len() != historyCapacity {
		t.Errorf("after overflow Len = %d, want %d", h.Len(), historyCapacity)
	}
}

func TestEventHistory_SnapshotIsCopy(t *testing.T) {
	h := newEventHistory()
	h.Append(WorkerEvent{Index: 1})
	got := h.Snapshot()
	got[0].Index = 999
	again := h.Snapshot()
	if again[0].Index != 1 {
		t.Errorf("Snapshot returned aliased slice: got %d, want 1", again[0].Index)
	}
}
