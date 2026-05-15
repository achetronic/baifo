// Copyright 2026 The baifo Authors.
// Licensed under the Apache License, Version 2.0; see LICENSE.

package editor

// keymap is the editor's binding table. It maps a tea.KeyMsg.String()
// to an editorAction; the Update method dispatches on action codes
// instead of raw key strings so future customisation (per-host
// rebinds) only needs to mutate the map.
//
// We deliberately keep this in a typed enum rather than function
// pointers: the action stays cheap to copy, easy to inspect in tests,
// and trivial to extend for B.2b/c.
type keymap map[string]action

// action enumerates everything the editor can do in response to a
// single keypress. The naming follows "verb + subject" style so
// listing them gives a quick overview of the editor's surface.
type action int

const (
	actNone action = iota

	actMoveLeft
	actMoveRight
	actMoveUp
	actMoveDown
	actMoveHome
	actMoveEnd
	actMoveDocStart
	actMoveDocEnd
	actMovePageUp
	actMovePageDown
	actMoveWordLeft
	actMoveWordRight

	actSelectLeft
	actSelectRight
	actSelectUp
	actSelectDown
	actSelectHome
	actSelectEnd
	actSelectDocStart
	actSelectDocEnd
	actSelectPageUp
	actSelectPageDown
	actSelectWordLeft
	actSelectWordRight
	actSelectAll

	actBackspace
	actDeleteForward
	actEnter
	actIndent
	actOutdent
	actDuplicateLine
	actDeleteLine
	actMoveLineUp
	actMoveLineDown

	actCopy
	actCut
	actPaste

	actUndo
	actRedo

	actFind

	actSave
	actCancel
)

// defaultKeymap returns the default bindings. We pick conventional
// Unix-y choices (Ctrl+S save, Ctrl+C copy, Ctrl+V paste...) and
// accept that they conflict with the terminal's own Ctrl+C-as-SIGINT
// in some cases. Bubbletea v2 captures Ctrl+C as a key, not as a
// signal, so this is safe inside the editor.
func defaultKeymap() keymap {
	return keymap{
		// Cursor movement (no shift).
		"left":      actMoveLeft,
		"right":     actMoveRight,
		"up":        actMoveUp,
		"down":      actMoveDown,
		"home":      actMoveHome,
		"end":       actMoveEnd,
		"ctrl+home": actMoveDocStart,
		"ctrl+end":  actMoveDocEnd,
		"pgup":      actMovePageUp,
		"pgdown":    actMovePageDown,
		// Word jumps. ctrl+arrow is the Windows/Linux convention,
		// alt+arrow the macOS one; terminals deliver one or the other
		// depending on their keyboard mode, so we register both.
		"ctrl+left":  actMoveWordLeft,
		"ctrl+right": actMoveWordRight,
		"alt+left":   actMoveWordLeft,
		"alt+right":  actMoveWordRight,

		// Selection (Shift + same motion). Note ctrl+shift+arrow has
		// uneven terminal support; we register the most common forms.
		"shift+left":       actSelectLeft,
		"shift+right":      actSelectRight,
		"shift+up":         actSelectUp,
		"shift+down":       actSelectDown,
		"shift+home":       actSelectHome,
		"shift+end":        actSelectEnd,
		"ctrl+shift+home":  actSelectDocStart,
		"ctrl+shift+end":   actSelectDocEnd,
		"shift+pgup":       actSelectPageUp,
		"shift+pgdown":     actSelectPageDown,
		"ctrl+shift+left":  actSelectWordLeft,
		"ctrl+shift+right": actSelectWordRight,
		"alt+shift+left":   actSelectWordLeft,
		"alt+shift+right":  actSelectWordRight,
		"ctrl+a":           actSelectAll,

		// Editing.
		"backspace": actBackspace,
		"delete":    actDeleteForward,
		"enter":     actEnter,

		// Indentation. Tab indents (two spaces, the YAML unit); with a
		// selection it indents every covered line. Shift+Tab outdents.
		"tab":       actIndent,
		"shift+tab": actOutdent,

		// Line operations (VS Code / JetBrains conventions).
		"ctrl+d":         actDuplicateLine,
		"ctrl+k":         actDeleteLine,
		"ctrl+shift+k":   actDeleteLine,
		"alt+up":         actMoveLineUp,
		"alt+down":       actMoveLineDown,
		"alt+shift+up":   actMoveLineUp,
		"alt+shift+down": actMoveLineDown,

		// Clipboard.
		"ctrl+c": actCopy,
		"ctrl+x": actCut,
		"ctrl+v": actPaste,

		// Undo / redo. ctrl+shift+z is the modern redo most users
		// reach for first; ctrl+y stays for the Windows muscle memory.
		"ctrl+z":       actUndo,
		"ctrl+y":       actRedo,
		"ctrl+shift+z": actRedo,

		// Find.
		"ctrl+f": actFind,

		// Save / quit. ctrl+s saves; esc and ctrl+q both leave (with
		// a discard confirmation when the buffer is dirty), so users
		// coming from nano-likes (ctrl+q / ctrl+x to exit) and users
		// who treat Esc as universal-close both get what they expect.
		"ctrl+s": actSave,
		"esc":    actCancel,
		"ctrl+q": actCancel,
	}
}

// isSelectingAction reports whether act extends the current
// selection. Used in the Update loop to decide whether to keep the
// anchor on a motion or to collapse the selection.
func isSelectingAction(act action) bool {
	switch act {
	case actSelectLeft, actSelectRight, actSelectUp, actSelectDown,
		actSelectHome, actSelectEnd, actSelectDocStart, actSelectDocEnd,
		actSelectPageUp, actSelectPageDown,
		actSelectWordLeft, actSelectWordRight, actSelectAll:
		return true
	}
	return false
}

// motionForSelecting maps a select-action to its equivalent move-
// action, so the Update loop can implement "select" as "move with
// anchor preserved" without duplicating switch statements.
func motionForSelecting(act action) action {
	switch act {
	case actSelectLeft:
		return actMoveLeft
	case actSelectRight:
		return actMoveRight
	case actSelectUp:
		return actMoveUp
	case actSelectDown:
		return actMoveDown
	case actSelectHome:
		return actMoveHome
	case actSelectEnd:
		return actMoveEnd
	case actSelectDocStart:
		return actMoveDocStart
	case actSelectDocEnd:
		return actMoveDocEnd
	case actSelectPageUp:
		return actMovePageUp
	case actSelectPageDown:
		return actMovePageDown
	case actSelectWordLeft:
		return actMoveWordLeft
	case actSelectWordRight:
		return actMoveWordRight
	}
	return actNone
}
