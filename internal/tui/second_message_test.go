// Copyright 2025 - Alby Hernández and the baifo contributors
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// TestSendingTwoMessagesInARowDoesNotDeadlock reproduces the bug
// Alby reported on 16 nov 2026: after the first reply, the TUI froze
// and only Ctrl+C escaped. Drives two complete submit+drain cycles
// and asserts both streams reach the final 'done' state.
func TestSendingTwoMessagesInARowDoesNotDeadlock(t *testing.T) {
	facade := &fakeFacade{reply: "first"}
	model := NewModel(facade, false, "v0")
	model.splash = false

	// First message.
	model.composer.ta.SetValue("one")
	m, cmd := model.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("first submit: expected streaming cmd")
	}
	m = drainStream(t, m, cmd)

	if mid := m.(Model); mid.streamCancel != nil {
		t.Errorf("after first reply streamCancel should be cleared, got non-nil")
	}

	// Second message — this is where the freeze used to happen.
	facade.reply = "second"
	mid := m.(Model)
	mid.composer.ta.SetValue("two")
	m2, cmd2 := mid.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd2 == nil {
		t.Fatal("second submit: expected streaming cmd, got nil — chat is frozen")
	}

	// drainStream has its own timeout via the chained cmds, but to be
	// extra explicit we cap the whole thing at a generous wall clock.
	done := make(chan tea.Model, 1)
	go func() { done <- drainStream(t, m2, cmd2) }()
	select {
	case final := <-done:
		got := final.(Model)
		// Expect 4 rows: user1, root1, user2, root2.
		if len(got.messages) != 4 {
			t.Fatalf("after two replies: got %d rows, want 4 (%+v)",
				len(got.messages), got.messages)
		}
		if got.messages[3].Text != "second" {
			t.Errorf("last row text: got %q, want %q",
				got.messages[3].Text, "second")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("second stream deadlocked (drainStream did not return in 3s)")
	}
}
