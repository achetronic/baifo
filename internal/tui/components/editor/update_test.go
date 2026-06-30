// Copyright 2025 - Alby Hernández and the baifo contributors
// SPDX-License-Identifier: Apache-2.0

package editor

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// keyMsg builds a tea.KeyMsg from a key string ("ctrl+s", "a", ...).
// Bubbletea v2 expects a Key struct; for tests we use KeyPressMsg
// with the Text field set so .String() returns the right thing.
type fakeKey struct {
	s    string
	text string
}

func (k fakeKey) String() string { return k.s }
func (k fakeKey) Key() tea.Key   { return tea.Key{Text: k.text} }

// press wraps a key string into a tea.KeyMsg-compatible value for
// driving Update in tests. text is the printable representation (use
// "" for non-printables like "enter" / "ctrl+s").
func press(s, text string) tea.KeyMsg { return fakeKey{s: s, text: text} }

func newTestModel(t *testing.T, initial string) Model {
	t.Helper()
	m := New(Options{Title: "Test", InitialValue: initial})
	m.SetSize(80, 20)
	return m
}

func TestUpdate_TypingInsertsRunes(t *testing.T) {
	m := newTestModel(t, "")
	m, _ = m.Update(press("a", "a"))
	m, _ = m.Update(press("b", "b"))
	m, _ = m.Update(press("c", "c"))
	if got := m.Value(); got != "abc" {
		t.Errorf("value: got %q, want abc", got)
	}
	if !m.Dirty() {
		t.Errorf("dirty should be true after typing")
	}
}

func TestUpdate_EnterSplitsLine(t *testing.T) {
	m := newTestModel(t, "hello")
	// Move cursor to col 3 then press Enter.
	m, _ = m.Update(press("right", ""))
	m, _ = m.Update(press("right", ""))
	m, _ = m.Update(press("right", ""))
	m, _ = m.Update(press("enter", ""))
	if got := m.Value(); got != "hel\nlo" {
		t.Errorf("value: got %q, want %q", got, "hel\nlo")
	}
}

func TestUpdate_BackspaceDeletes(t *testing.T) {
	m := newTestModel(t, "abc")
	m, _ = m.Update(press("end", ""))
	m, _ = m.Update(press("backspace", ""))
	if got := m.Value(); got != "ab" {
		t.Errorf("value: got %q, want ab", got)
	}
}

func TestUpdate_DeleteForward(t *testing.T) {
	m := newTestModel(t, "abc")
	m, _ = m.Update(press("delete", ""))
	if got := m.Value(); got != "bc" {
		t.Errorf("value: got %q, want bc", got)
	}
}

func TestUpdate_SelectionWithShiftRight(t *testing.T) {
	m := newTestModel(t, "hello")
	m, _ = m.Update(press("shift+right", ""))
	m, _ = m.Update(press("shift+right", ""))
	m, _ = m.Update(press("shift+right", ""))
	if !m.sel.active() {
		t.Fatalf("selection should be active after shift+right")
	}
	a, b := m.sel.rng()
	if a != (position{0, 0}) || b != (position{0, 3}) {
		t.Errorf("rng: got (%v,%v), want ((0,0),(0,3))", a, b)
	}
	// Typing a character should replace the selection.
	m, _ = m.Update(press("X", "X"))
	if got := m.Value(); got != "Xlo" {
		t.Errorf("value after replace: got %q, want Xlo", got)
	}
}

func TestUpdate_SelectAllThenBackspace(t *testing.T) {
	m := newTestModel(t, "hello\nworld")
	m, _ = m.Update(press("ctrl+a", ""))
	if !m.sel.active() {
		t.Fatalf("ctrl+a should activate selection")
	}
	m, _ = m.Update(press("backspace", ""))
	if got := m.Value(); got != "" {
		t.Errorf("value after select+delete: got %q, want empty", got)
	}
}

func TestUpdate_SaveCallsValidatorAndEmits(t *testing.T) {
	validated := ""
	opts := Options{
		Title:        "save test",
		InitialValue: "abc",
		OnSave: func(buf string) []error {
			validated = buf
			return nil
		},
	}
	m := New(opts)
	m.SetSize(80, 20)

	_, cmd := m.Update(press("ctrl+s", ""))
	if validated != "abc" {
		t.Errorf("validator not called with buffer, got %q", validated)
	}
	if cmd == nil {
		t.Fatalf("ctrl+s with no errors should emit SaveMsg cmd")
	}
	msg := cmd()
	if save, ok := msg.(SaveMsg); !ok || save.Value != "abc" {
		t.Errorf("emitted msg: got %#v, want SaveMsg{abc}", msg)
	}
}

func TestUpdate_SaveSurfacesValidatorErrors(t *testing.T) {
	opts := Options{
		InitialValue: "broken",
		OnSave: func(string) []error {
			return []error{errors.New("invalid yaml")}
		},
	}
	m := New(opts)
	m.SetSize(80, 20)

	// Make the buffer dirty so we can verify it stays dirty after a
	// failed save. Without this edit the buffer is clean from the
	// start and the test would assert nothing useful.
	m, _ = m.Update(press("a", "a"))

	m2, cmd := m.Update(press("ctrl+s", ""))
	if cmd != nil {
		t.Errorf("save with errors should NOT emit a cmd")
	}
	if len(m2.validationErrors) != 1 {
		t.Fatalf("expected 1 validation error, got %d", len(m2.validationErrors))
	}
	if !strings.Contains(m2.validationErrors[0].Error(), "invalid yaml") {
		t.Errorf("error: got %q", m2.validationErrors[0].Error())
	}
	// Buffer must still be marked dirty; user hasn't successfully saved.
	if !m2.Dirty() {
		t.Errorf("dirty should remain true after failed save")
	}
}

func TestUpdate_EscOnCleanBufferCancelsImmediately(t *testing.T) {
	m := newTestModel(t, "abc")
	_, cmd := m.Update(press("esc", ""))
	if cmd == nil {
		t.Fatalf("esc on clean buffer should emit CancelMsg")
	}
	if _, ok := cmd().(CancelMsg); !ok {
		t.Errorf("expected CancelMsg, got %T", cmd())
	}
}

func TestUpdate_EscOnDirtyBufferOpensConfirm(t *testing.T) {
	m := newTestModel(t, "abc")
	m, _ = m.Update(press("a", "a")) // make dirty
	m2, cmd := m.Update(press("esc", ""))
	if cmd != nil {
		t.Errorf("first esc on dirty buffer should not emit cmd yet")
	}
	if !m2.confirmDiscard {
		t.Errorf("confirmDiscard flag should be true")
	}
	// n keeps editing.
	m3, _ := m2.Update(press("n", "n"))
	if m3.confirmDiscard {
		t.Errorf("'n' should dismiss the confirm modal")
	}
	// Re-open and confirm with y.
	m4, _ := m3.Update(press("esc", ""))
	_, cmd = m4.Update(press("y", "y"))
	if cmd == nil {
		t.Fatalf("'y' on the confirm should emit CancelMsg")
	}
	if _, ok := cmd().(CancelMsg); !ok {
		t.Errorf("expected CancelMsg, got %T", cmd())
	}
}

func TestUpdate_PasteInsertsMultiline(t *testing.T) {
	m := newTestModel(t, "before|after")
	// Move cursor to col 6 (right after the '|').
	for i := 0; i < 7; i++ {
		m, _ = m.Update(press("right", ""))
	}
	m, _ = m.Update(tea.PasteMsg{Content: "X\nY\n"})
	want := "before|X\nY\nafter"
	if got := m.Value(); got != want {
		t.Errorf("value: got %q, want %q", got, want)
	}
}

func TestUpdate_BlurStopsConsumingKeys(t *testing.T) {
	m := newTestModel(t, "abc")
	m.Blur()
	m2, _ := m.Update(press("x", "x"))
	if m2.Value() != "abc" {
		t.Errorf("blurred editor should ignore keys, got %q", m2.Value())
	}
}
