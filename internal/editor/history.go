package editor

// snapshot captures enough state to restore the buffer for undo/redo.
type snapshot struct {
	lines     []string
	cursorRow int
	cursorCol int
	dirty     bool
}

// maxHistory caps the undo/redo stacks so a long editing session can't grow
// memory without bound.
const maxHistory = 500

const maxHistoryBytes = 16 << 20

// currentSnapshot copies the mutable buffer state for the history stacks.
func (m Model) currentSnapshot() snapshot {
	return snapshot{
		lines:     append([]string(nil), m.lines...),
		cursorRow: m.cursorRow,
		cursorCol: m.cursorCol,
		dirty:     m.Dirty,
	}
}

// pushUndo records the current buffer state so the next mutation can be undone,
// and discards any redo history (a new edit forks the timeline). Call this
// immediately before a mutating op; during an insert session only the entry
// into insert mode pushes, so one undo reverts the whole session.
func (m *Model) pushUndo() {
	m.pushUndoSnapshot(m.currentSnapshot())
	m.clearRedo()
}

func snapshotBytes(s snapshot) int {
	n := 16
	for _, line := range s.lines {
		n += len(line) + 16
	}
	return n
}

func pushBounded(stack []snapshot, used *int, s snapshot) []snapshot {
	sz := snapshotBytes(s)
	if sz > maxHistoryBytes {
		return stack
	}
	stack = append(stack, s)
	*used += sz
	for len(stack) > maxHistory || *used > maxHistoryBytes {
		*used -= snapshotBytes(stack[0])
		stack = stack[1:]
	}
	return stack
}

func (m *Model) pushUndoSnapshot(s snapshot) {
	m.undoStack = pushBounded(m.undoStack, &m.undoBytes, s)
}

func (m *Model) pushRedoSnapshot(s snapshot) {
	m.redoStack = pushBounded(m.redoStack, &m.redoBytes, s)
}

func (m *Model) clearRedo() { m.redoStack, m.redoBytes = nil, 0 }

// armInsertUndo records the pre-insert state so the coming insert session can
// be undone as one unit. The snapshot is not committed until the first edit.
func (m *Model) armInsertUndo() {
	m.insertUndoSnap = m.currentSnapshot()
	m.insertUndoArmed = true
}

// commitInsertUndo pushes the armed pre-insert snapshot onto the undo stack the
// first time a real edit happens in an insert session, coalescing the rest of
// the session into that single entry.
func (m *Model) commitInsertUndo() {
	if !m.insertUndoArmed {
		return
	}
	m.pushUndoSnapshot(m.insertUndoSnap)
	m.clearRedo()
	m.insertUndoArmed = false
}

// restore replaces the buffer with s and clamps the cursor into it.
func (m *Model) restore(s snapshot) {
	m.lines = append([]string(nil), s.lines...)
	if len(m.lines) == 0 {
		m.lines = []string{""}
	}
	m.cursorRow = s.cursorRow
	if m.cursorRow < 0 {
		m.cursorRow = 0
	}
	if m.cursorRow > len(m.lines)-1 {
		m.cursorRow = len(m.lines) - 1
	}
	m.cursorCol = s.cursorCol
	if m.cursorCol < 0 {
		m.cursorCol = 0
	}
	if m.cursorCol > runeLen(m.lines[m.cursorRow]) {
		m.cursorCol = runeLen(m.lines[m.cursorRow])
	}
	m.Mode = ModeNormal
	m.pendingG = false
	m.insertUndoArmed = false
	m.revision++
	// Recompute against the saved snapshot rather than trusting s.dirty: an undo
	// that restores several rows at an unchanged line count would otherwise leave
	// stale dirtyRows entries and report false-dirty. recomputeDirty is authoritative.
	m.recomputeDirty()
	m.ensureCursorVisible()
}

// undo reverts the most recent change, pushing the pre-undo state onto the redo
// stack so it can be re-applied.
func (m *Model) undo() {
	if len(m.undoStack) == 0 {
		return
	}
	m.pushRedoSnapshot(m.currentSnapshot())
	last := m.undoStack[len(m.undoStack)-1]
	m.undoBytes -= snapshotBytes(last)
	m.undoStack = m.undoStack[:len(m.undoStack)-1]
	m.restore(last)
}

// redo re-applies the most recently undone change.
func (m *Model) redo() {
	if len(m.redoStack) == 0 {
		return
	}
	m.pushUndoSnapshot(m.currentSnapshot())
	last := m.redoStack[len(m.redoStack)-1]
	m.redoBytes -= snapshotBytes(last)
	m.redoStack = m.redoStack[:len(m.redoStack)-1]
	m.restore(last)
}
