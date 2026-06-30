// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package editor

import "testing"

func TestUndo_RestoresPreviousBuffer(t *testing.T) {
	m := newTestModel(t, "abc")
	m, _ = m.Update(press("end", ""))
	m, _ = m.Update(press("x", "x"))
	if m.Value() != "abcx" {
		t.Fatalf("setup: got %q", m.Value())
	}
	m, _ = m.Update(press("ctrl+z", ""))
	if m.Value() != "abc" {
		t.Errorf("undo: got %q, want abc", m.Value())
	}
}

func TestRedo_ReappliesUndoneEdit(t *testing.T) {
	m := newTestModel(t, "abc")
	m, _ = m.Update(press("end", ""))
	m, _ = m.Update(press("x", "x"))
	m, _ = m.Update(press("ctrl+z", ""))
	m, _ = m.Update(press("ctrl+y", ""))
	if m.Value() != "abcx" {
		t.Errorf("redo: got %q, want abcx", m.Value())
	}
}

func TestUndo_NoOpOnEmptyHistory(t *testing.T) {
	m := newTestModel(t, "abc")
	// Without any edits, ctrl+z should not panic and should leave
	// the buffer untouched.
	m2, _ := m.Update(press("ctrl+z", ""))
	if m2.Value() != "abc" {
		t.Errorf("undo empty: got %q, want abc", m2.Value())
	}
}

func TestUndo_MultipleSteps(t *testing.T) {
	m := newTestModel(t, "")
	m, _ = m.Update(press("a", "a"))
	m, _ = m.Update(press("b", "b"))
	m, _ = m.Update(press("c", "c"))
	if m.Value() != "abc" {
		t.Fatalf("setup: got %q", m.Value())
	}
	m, _ = m.Update(press("ctrl+z", ""))
	if m.Value() != "ab" {
		t.Errorf("undo 1: got %q", m.Value())
	}
	m, _ = m.Update(press("ctrl+z", ""))
	if m.Value() != "a" {
		t.Errorf("undo 2: got %q", m.Value())
	}
	m, _ = m.Update(press("ctrl+z", ""))
	if m.Value() != "" {
		t.Errorf("undo 3: got %q", m.Value())
	}
}

func TestUndo_NewEditInvalidatesRedoStack(t *testing.T) {
	m := newTestModel(t, "")
	m, _ = m.Update(press("a", "a"))
	m, _ = m.Update(press("ctrl+z", ""))
	// At this point redoStack has one entry. A new edit must clear it.
	m, _ = m.Update(press("b", "b"))
	m, _ = m.Update(press("ctrl+y", ""))
	if m.Value() != "b" {
		t.Errorf("ctrl+y after new edit should be a no-op, got %q", m.Value())
	}
}

func TestUndo_RestoresCursorPosition(t *testing.T) {
	m := newTestModel(t, "hello")
	m, _ = m.Update(press("end", ""))
	m, _ = m.Update(press("!", "!"))
	// Cursor now at (0, 6). Undo should put it back to (0, 5).
	m, _ = m.Update(press("ctrl+z", ""))
	if m.cursor.line != 0 || m.cursor.col != 5 {
		t.Errorf("cursor after undo: got %v, want (0,5)", m.cursor)
	}
}

func TestUndo_RestoresSelectionDelete(t *testing.T) {
	m := newTestModel(t, "hello")
	m, _ = m.Update(press("shift+right", ""))
	m, _ = m.Update(press("shift+right", ""))
	m, _ = m.Update(press("backspace", ""))
	if m.Value() != "llo" {
		t.Fatalf("delete selection: got %q", m.Value())
	}
	m, _ = m.Update(press("ctrl+z", ""))
	if m.Value() != "hello" {
		t.Errorf("undo: got %q, want hello", m.Value())
	}
}
