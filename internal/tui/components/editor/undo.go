// SPDX-License-Identifier: Apache-2.0

package editor

// snapshot is one entry in the undo/redo history. We persist the
// full buffer text plus the cursor and selection — small enough that
// even a hundred-step history weighs only a few KB for the YAML
// sizes baifo edits.
//
// Snapshot-based undo is the simplest correct approach. The trendy
// alternative is operation-based ("delta" undo: store inverse ops
// and apply them on Ctrl+Z), which is faster to undo but trickier
// to maintain when operations compose with selections, paste and
// multi-line edits. Snapshots dodge every edge case for free.
type snapshot struct {
	value  string
	cursor position
	sel    *selection
}

// historyLimit caps how many past snapshots we retain. 100 is a sweet
// spot: enough that users never hit the wall during a session, low
// enough that memory stays under 1MB even for chunky YAML files.
const historyLimit = 100

// pushHistory captures the current state and pushes it onto the undo
// stack. Called BEFORE any mutating operation so Ctrl+Z gets back to
// the state that existed just before that operation.
//
// Pushing also clears the redo stack: any new edit invalidates the
// "future" you could have walked back to. This matches the behaviour
// every text editor users know.
func (m *Model) pushHistory() {
	snap := snapshot{
		value:  m.Value(),
		cursor: m.cursor,
	}
	if m.sel != nil {
		s := *m.sel
		snap.sel = &s
	}
	m.undoStack = append(m.undoStack, snap)
	if len(m.undoStack) > historyLimit {
		// Drop the oldest entry. The slice keeps growing in capacity
		// until the GC reclaims it, which is fine for our scale.
		m.undoStack = m.undoStack[len(m.undoStack)-historyLimit:]
	}
	m.redoStack = nil
}

// undo pops the most recent snapshot, restoring the buffer, cursor
// and selection, and pushes the current state onto the redo stack so
// Ctrl+Y can replay it. No-op when there is nothing to undo.
func (m *Model) undo() {
	if len(m.undoStack) == 0 {
		return
	}
	// Save current state for redo BEFORE replacing it.
	current := snapshot{
		value:  m.Value(),
		cursor: m.cursor,
	}
	if m.sel != nil {
		s := *m.sel
		current.sel = &s
	}
	m.redoStack = append(m.redoStack, current)

	last := m.undoStack[len(m.undoStack)-1]
	m.undoStack = m.undoStack[:len(m.undoStack)-1]
	m.applySnapshot(last)
}

// redo is the mirror of undo: pop from redoStack, push current onto
// undoStack, apply.
func (m *Model) redo() {
	if len(m.redoStack) == 0 {
		return
	}
	current := snapshot{
		value:  m.Value(),
		cursor: m.cursor,
	}
	if m.sel != nil {
		s := *m.sel
		current.sel = &s
	}
	m.undoStack = append(m.undoStack, current)

	last := m.redoStack[len(m.redoStack)-1]
	m.redoStack = m.redoStack[:len(m.redoStack)-1]
	m.applySnapshot(last)
}

// applySnapshot replaces the buffer state with snap. Used by both
// undo and redo. The dirty flag is recomputed from whether the new
// state matches the original (which we don't track) — for now we
// just keep dirty=true after any undo/redo, since the user has been
// making history. A more polished impl would compare to the initial
// snapshot to clear dirty when relevant.
func (m *Model) applySnapshot(snap snapshot) {
	m.buf = newBuffer(snap.value)
	m.cursor = clampPosition(m.buf, snap.cursor)
	if snap.sel != nil {
		s := *snap.sel
		m.sel = &s
	} else {
		m.sel = nil
	}
	m.validationErrors = nil
	m.dirty = true
	m.syncViewport()
}
