// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// seededHistoryModel returns a Model with three sent user messages
// (interleaved with root replies, to prove non-user rows are skipped)
// and focus on the composer, ready for recall.
func seededHistoryModel(t *testing.T) Model {
	t.Helper()
	m := NewModel(&fakeFacade{}, "v0")
	m.splash = false
	m.messages = []Message{
		{Kind: MessageUser, Time: time.Now(), Text: "first"},
		{Kind: MessageRoot, Time: time.Now(), Text: "reply one"},
		{Kind: MessageUser, Time: time.Now(), Text: "second"},
		{Kind: MessageRoot, Time: time.Now(), Text: "reply two"},
		{Kind: MessageUser, Time: time.Now(), Text: "third"},
	}
	return m
}

func ctrlUp() tea.KeyPressMsg   { return tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModCtrl} }
func ctrlDown() tea.KeyPressMsg { return tea.KeyPressMsg{Code: tea.KeyDown, Mod: tea.ModCtrl} }

// TestHistoryRecallCtrlUpWalksOlder confirms Ctrl+Up loads the most
// recent user message first, then progressively older ones.
func TestHistoryRecallCtrlUpWalksOlder(t *testing.T) {
	m := seededHistoryModel(t)

	m = driveModel(t, m, ctrlUp()).(Model)
	if got := m.composer.ta.Value(); got != "third" {
		t.Fatalf("first Ctrl+Up = %q, want %q", got, "third")
	}
	m = driveModel(t, m, ctrlUp()).(Model)
	if got := m.composer.ta.Value(); got != "second" {
		t.Fatalf("second Ctrl+Up = %q, want %q", got, "second")
	}
	m = driveModel(t, m, ctrlUp()).(Model)
	if got := m.composer.ta.Value(); got != "first" {
		t.Fatalf("third Ctrl+Up = %q, want %q", got, "first")
	}
	// Past the oldest entry it should clamp, not wrap.
	m = driveModel(t, m, ctrlUp()).(Model)
	if got := m.composer.ta.Value(); got != "first" {
		t.Fatalf("Ctrl+Up past oldest = %q, want it to stay %q", got, "first")
	}
}

// TestHistoryRecallCtrlDownRestoresDraft confirms Ctrl+Down walks back
// toward newer entries and finally restores the in-progress draft the
// user had typed before browsing.
func TestHistoryRecallCtrlDownRestoresDraft(t *testing.T) {
	m := seededHistoryModel(t)
	m.composer.ta.SetValue("draft in progress")

	// Up twice → "third" then "second".
	m = driveModel(t, m, ctrlUp()).(Model)
	m = driveModel(t, m, ctrlUp()).(Model)
	if got := m.composer.ta.Value(); got != "second" {
		t.Fatalf("after two Ctrl+Up = %q, want %q", got, "second")
	}
	// Down once → "third".
	m = driveModel(t, m, ctrlDown()).(Model)
	if got := m.composer.ta.Value(); got != "third" {
		t.Fatalf("Ctrl+Down = %q, want %q", got, "third")
	}
	// Down again → back to the stashed draft.
	m = driveModel(t, m, ctrlDown()).(Model)
	if got := m.composer.ta.Value(); got != "draft in progress" {
		t.Fatalf("Ctrl+Down to draft = %q, want %q", got, "draft in progress")
	}
}

// TestHistoryRecallCtrlDownWithoutBrowsingIsNoop confirms Ctrl+Down
// while not browsing leaves the composer untouched.
func TestHistoryRecallCtrlDownWithoutBrowsingIsNoop(t *testing.T) {
	m := seededHistoryModel(t)
	m.composer.ta.SetValue("untouched")
	m = driveModel(t, m, ctrlDown()).(Model)
	if got := m.composer.ta.Value(); got != "untouched" {
		t.Errorf("Ctrl+Down with no recall in progress changed buffer to %q", got)
	}
	if m.historyIdx != -1 {
		t.Errorf("historyIdx = %d, want -1 (not browsing)", m.historyIdx)
	}
}

// TestHistoryRecallEmptyHistoryNoop confirms recall is inert when no
// user messages exist yet.
func TestHistoryRecallEmptyHistoryNoop(t *testing.T) {
	m := NewModel(&fakeFacade{}, "v0")
	m.splash = false
	m.composer.ta.SetValue("hi")
	m = driveModel(t, m, ctrlUp()).(Model)
	if got := m.composer.ta.Value(); got != "hi" {
		t.Errorf("Ctrl+Up with empty history changed buffer to %q", got)
	}
}

// TestHistoryRecallResetByEditing confirms that typing after a recall
// resets the browse position so the next Ctrl+Up starts from newest.
func TestHistoryRecallResetByEditing(t *testing.T) {
	m := seededHistoryModel(t)

	m = driveModel(t, m, ctrlUp()).(Model) // "third"
	m = driveModel(t, m, ctrlUp()).(Model) // "second"
	if m.historyIdx != 1 {
		t.Fatalf("historyIdx after two Ctrl+Up = %d, want 1", m.historyIdx)
	}
	// A normal keystroke edits the buffer and should reset recall.
	m = driveModel(t, m, tea.KeyPressMsg{Code: 'x', Text: "x"}).(Model)
	if m.historyIdx != -1 {
		t.Fatalf("editing should reset historyIdx to -1, got %d", m.historyIdx)
	}
	// Next Ctrl+Up starts again from the newest message.
	m = driveModel(t, m, ctrlUp()).(Model)
	if got := m.composer.ta.Value(); got != "third" {
		t.Fatalf("Ctrl+Up after edit = %q, want newest %q", got, "third")
	}
}

// TestWordMotionBindingsIncludeCtrlArrows confirms the composer binds
// Ctrl+Left / Ctrl+Right for word-wise cursor motion (alongside the
// Alt defaults), so the user gets the conventional shortcut.
func TestWordMotionBindingsIncludeCtrlArrows(t *testing.T) {
	c := newComposer(NewTheme())
	if !keyBindingHasKey(c.ta.KeyMap.WordBackward.Keys(), "ctrl+left") {
		t.Errorf("WordBackward should bind ctrl+left, got %v", c.ta.KeyMap.WordBackward.Keys())
	}
	if !keyBindingHasKey(c.ta.KeyMap.WordForward.Keys(), "ctrl+right") {
		t.Errorf("WordForward should bind ctrl+right, got %v", c.ta.KeyMap.WordForward.Keys())
	}
}

func keyBindingHasKey(keys []string, want string) bool {
	for _, k := range keys {
		if k == want {
			return true
		}
	}
	return false
}
