// SPDX-FileCopyrightText: 2026 Alby Hernández <hola@achetronic.com>
// SPDX-License-Identifier: Apache-2.0

package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestOpenEditorRequeriesWindowSize covers issue #17: on Windows the
// dimensions cached at startup can be stale, and an editor sized from
// them renders wider and taller than the visible console, pushing the
// action footer off-screen. Opening the editor must re-query the
// terminal size so the next WindowSizeMsg re-sizes the editor to the
// real dimensions.
func TestOpenEditorRequeriesWindowSize(t *testing.T) {
	m := NewModel(&fakeFacade{}, "v0")
	m.splash = false
	var tm tea.Model = m
	tm, _ = tm.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m = tm.(Model)

	req := openEditorRequest{Title: "edit agent", InitialValue: "name: a\n", Kind: editorKindAgentUpsert}
	tm, cmd := m.openEmbeddedEditor(req)
	m = tm.(Model)

	// Opening must schedule a window-size re-query.
	if cmd == nil {
		t.Fatal("openEmbeddedEditor returned no command; the size re-query is missing (issue #17)")
	}
	if _, ok := cmd().(tea.Msg); !ok {
		t.Fatal("openEmbeddedEditor command does not yield a tea.Msg")
	}

	// Simulate the terminal reporting the REAL size after the
	// re-query, as updateWithEditor would receive it, and assert
	// the editor adopts it.
	tm, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = tm.(Model)
	if m.width != 80 || m.height != 24 {
		t.Fatalf("host size after re-query: got %dx%d, want 80x24", m.width, m.height)
	}
	view := m.View().Content
	if lines := strings.Count(view, "\n") + 1; lines != 24 {
		t.Errorf("editor view height: got %d lines, want 24", lines)
	}
	if !strings.Contains(view, "[ctrl+s] save") {
		t.Error("action footer missing from the editor view")
	}
}
