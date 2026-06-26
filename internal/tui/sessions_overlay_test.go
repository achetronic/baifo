// Licensed under the Apache License, Version 2.0; see LICENSE.

package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/achetronic/baifo/internal/facade"
	"github.com/achetronic/baifo/internal/tui/components/editor"
)

// TestSessionsOverlayRenameOpensEditor guards issue #2: pressing r in
// the /session overlay must open the embedded editor seeded with the
// session's current title (and close the overlay underneath), rather
// than printing the CLI command into the chat.
func TestSessionsOverlayRenameOpensEditor(t *testing.T) {
	f := &recordingFacade{
		activeID: "abc",
		listResp: []facade.SessionInfo{
			{ID: "abc", Title: "first", LastAt: "now", MsgCount: 2},
			{ID: "xyz", Title: "second", LastAt: "before", MsgCount: 0},
		},
	}
	m := newModelWith(t, f)
	mm, _ := m.applySlashResult(slashResult{openSessionsOverlay: true})
	cur := mm.(Model)
	if !cur.sessionsOpen {
		t.Fatal("precondition: sessions overlay should be open")
	}

	mm, _ = cur.Update(tea.KeyPressMsg{Code: 'r'})
	cur = mm.(Model)

	if cur.sessionsOpen {
		t.Fatal("overlay should close when the rename editor opens")
	}
	if cur.editor == nil {
		t.Fatal("r should open the embedded editor")
	}
	if cur.editorSessionTargetID != "abc" {
		t.Fatalf("editor should target session abc, got %q", cur.editorSessionTargetID)
	}
	if cur.editorOnSaveKind != editorKindSessionRename {
		t.Fatalf("save kind should be editorKindSessionRename, got %v", cur.editorOnSaveKind)
	}
}

// TestSessionsOverlayRenameSaveCallsFacade verifies the save flow:
// the new title reaches Facade.RenameSession and the overlay is
// reopened so the user lands back where they started.
func TestSessionsOverlayRenameSaveCallsFacade(t *testing.T) {
	f := &recordingFacade{
		activeID: "abc",
		listResp: []facade.SessionInfo{
			{ID: "abc", Title: "first", LastAt: "now", MsgCount: 2},
		},
	}
	m := newModelWith(t, f)
	mm, _ := m.applySlashResult(slashResult{openSessionsOverlay: true})
	cur := mm.(Model)
	mm, _ = cur.Update(tea.KeyPressMsg{Code: 'r'})
	cur = mm.(Model)

	// Simulate the editor emitting a save with the new title.
	mm, _ = cur.Update(editor.SaveMsg{Value: "renamed title"})
	cur = mm.(Model)

	if f.renamedTo["abc"] != "renamed title" {
		t.Fatalf("RenameSession not called with new title, got %+v", f.renamedTo)
	}
	if cur.editor != nil {
		t.Fatal("editor should close after save")
	}
	if !cur.sessionsOpen {
		t.Fatal("sessions overlay should reopen after a successful rename")
	}
}

// TestSessionsOverlayRenameCancelReopensOverlay verifies that
// cancelling the rename editor returns the user to the sessions
// overlay instead of dumping them in the chat.
func TestSessionsOverlayRenameCancelReopensOverlay(t *testing.T) {
	f := &recordingFacade{
		activeID: "abc",
		listResp: []facade.SessionInfo{
			{ID: "abc", Title: "first", LastAt: "now", MsgCount: 2},
		},
	}
	m := newModelWith(t, f)
	mm, _ := m.applySlashResult(slashResult{openSessionsOverlay: true})
	cur := mm.(Model)
	mm, _ = cur.Update(tea.KeyPressMsg{Code: 'r'})
	cur = mm.(Model)

	mm, _ = cur.Update(editor.CancelMsg{})
	cur = mm.(Model)

	if cur.editor != nil {
		t.Fatal("editor should close on cancel")
	}
	if !cur.sessionsOpen {
		t.Fatal("sessions overlay should reopen after cancelling the rename")
	}
	if len(f.renamedTo) != 0 {
		t.Fatalf("cancel must not rename anything, got %+v", f.renamedTo)
	}
}
