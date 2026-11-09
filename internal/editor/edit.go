package editor

import (
	"strings"
	"unicode"
)

// finalizeMutation invalidates derived state in O(1); callers invoke it only
// after a known content mutation.
func (m *Model) finalizeMutation() {
	m.revision++
	m.Dirty = true
}

// recomputeDirty rebuilds dirty state from a full comparison against the saved
// snapshot. It is the authoritative O(n) path used after any mutation that
// changes the line count, where the incremental per-row map's indices would
// otherwise be stale: a count-changing op (dd/o/p/range replace) never populates
// dirtyRows, so without this a later single-row edit could clear Dirty while
// another row still differs from disk. When counts differ the buffer is
// unambiguously dirty; when they match, dirtyRows is rebuilt so a later edit can
// clear the flag only when every row truly matches.
func (m *Model) recomputeDirty() {
	if len(m.lines) != len(m.savedLines) {
		m.Dirty = true
		return
	}
	m.dirtyRows = make(map[int]struct{})
	for i := range m.lines {
		if m.lines[i] != m.savedLines[i] {
			m.dirtyRows[i] = struct{}{}
		}
	}
	m.Dirty = len(m.dirtyRows) != 0
}

func (m *Model) updateDirtyRow(row int) {
	if len(m.lines) != len(m.savedLines) {
		m.Dirty = true
		return
	}
	if row >= 0 && row < len(m.lines) {
		if m.lines[row] == m.savedLines[row] {
			delete(m.dirtyRows, row)
		} else {
			m.dirtyRows[row] = struct{}{}
		}
	}
	m.Dirty = len(m.dirtyRows) != 0
}

// insertRunes returns s with ins spliced in at rune index col, along with the
// rune column immediately after the inserted text.
func insertRunes(s string, col int, ins string) (string, int) {
	if col < 0 {
		col = 0
	}
	off, n, lastStart := graphemeOffset(s, col)
	// New column = cluster count of prefix+ins, not col+clusters(ins): a combining
	// mark or ZWJ in ins can merge into the preceding cluster (NFD paste, a typed
	// accent), so the count must come from segmenting the junction. Only the last
	// cluster of the prefix can merge with ins (boundaries before it were decided
	// by text to their left, which is unchanged), so resegment from there rather
	// than materializing every cluster of the line. Precondition: s and ins are
	// valid UTF-8 (invalid bytes segment one-per-cluster and an ins starting with
	// a continuation byte could fuse earlier invalid clusters); callers guarantee it.
	newCol := runeLen(ins)
	if n > 0 {
		newCol = n - 1 + runeLen(s[lastStart:off]+ins)
	}
	return s[:off] + ins + s[off:], newCol
}

// deleteRuneAt returns s with the single rune at index col removed. If col is
// out of range s is returned unchanged.
func deleteRuneAt(s string, col int) string {
	if col < 0 {
		return s
	}
	off, n, _ := graphemeOffset(s, col)
	if n != col || off >= len(s) {
		return s
	}
	return s[:off] + s[off+graphemeLen(s[off:]):]
}

// setRegister stores yanked/deleted text in the unnamed register and mirrors it
// to the OS clipboard (best effort). linewise marks a whole-line yank.
func (m *Model) setRegister(text string, linewise bool) {
	m.register = text
	m.registerLine = linewise
	m.clipMirror = text
	osClipboardWrite(text)
}

// effectiveRegister returns the text to paste and whether it is linewise.
// If the OS clipboard holds something we did not put there (an external copy),
// that text wins as a charwise paste; otherwise the unnamed register is used so
// linewise yy/dd behavior is preserved.
func (m Model) effectiveRegister() (string, bool) {
	if s, ok := osClipboardRead(); ok && s != "" && s != m.clipMirror {
		return sanitizeClipboard(s), false
	}
	return m.register, m.registerLine
}

// sanitizeClipboard cleans text from the OS clipboard (an external copy) before
// it enters the buffer: it normalizes CRLF/CR line endings to LF and drops other
// control characters, which would otherwise land as stray runes in the
// manuscript and desync cluster indices. Tab and newline are preserved.
func sanitizeClipboard(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if r == '\n' || r == '\t' || !unicode.IsControl(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// paste inserts the register at the cursor. after chooses p (after) vs P
// (before) placement.
func (m *Model) paste(after bool) {
	text, linewise := m.effectiveRegister()
	if text == "" {
		return
	}
	m.pushUndo()

	if linewise {
		// Register text for a linewise yank has no trailing newline; split it
		// back into its constituent lines and splice them in whole.
		newLines := strings.Split(text, "\n")
		at := m.cursorRow
		if after {
			at = m.cursorRow + 1
		}
		spliced := make([]string, 0, len(m.lines)+len(newLines))
		spliced = append(spliced, m.lines[:at]...)
		spliced = append(spliced, newLines...)
		spliced = append(spliced, m.lines[at:]...)
		m.lines = spliced
		m.cursorRow = at
		m.cursorCol = firstNonBlank(m.lines[m.cursorRow])
	} else {
		col := m.cursorCol
		// p inserts after the cursor character (unless the line is empty).
		if after && runeLen(m.lines[m.cursorRow]) > 0 {
			col++
		}
		endRow, endCol := m.insertCharwise(m.cursorRow, col, text)
		// Leave the cursor on the last inserted character, vim-style.
		m.cursorRow = endRow
		if endCol > col {
			m.cursorCol = endCol - 1
		} else {
			m.cursorCol = col
		}
		if m.cursorCol > runeLen(m.lines[m.cursorRow]) {
			m.cursorCol = runeLen(m.lines[m.cursorRow])
		}
	}

	m.Mode = ModeNormal
	m.finalizeMutation()
	m.ensureCursorVisible()
}

// insertCharwise splices text (which may contain newlines) into row at rune
// column col, without deleting anything. It returns the position immediately
// after the inserted text.
func (m *Model) insertCharwise(row, col int, text string) (endRow, endCol int) {
	r := graphemes(m.lines[row])
	if col < 0 {
		col = 0
	}
	if col > len(r) {
		col = len(r)
	}
	before := joinGraphemes(r[:col])
	after := joinGraphemes(r[col:])
	segs := strings.Split(before+text+after, "\n")

	endRow = row + len(segs) - 1
	if len(segs) == 1 {
		endCol = col + runeLen(text)
	} else {
		endCol = runeLen(segs[len(segs)-1]) - runeLen(after)
	}

	newLines := make([]string, 0, len(m.lines)+len(segs)-1)
	newLines = append(newLines, m.lines[:row]...)
	newLines = append(newLines, segs...)
	newLines = append(newLines, m.lines[row+1:]...)
	m.lines = newLines
	return endRow, endCol
}

// clampNormalCol confines the cursor to a valid Normal-mode column: on a
// non-empty line the last landing spot is the last character (len-1), never one
// past it. Applied after every Normal-mode motion and on exit from insert/visual
// so `x` at end-of-line, after `w`/`$`, or after `v$<esc>` still deletes a char.
func (m *Model) clampNormalCol() {
	if m.cursorRow < 0 || m.cursorRow >= len(m.lines) {
		return
	}
	// cursorCol is a raw-line grapheme index (what every edit uses), so bound it by
	// the raw line, not the sanitized display clusters: the two counts differ for
	// control/format characters, and the display cache is keyed by a revision that
	// only bumps after the key handler returns, so it can still describe the
	// pre-edit line here.
	max := runeLen(m.lines[m.cursorRow]) - 1
	if max < 0 {
		max = 0
	}
	if m.cursorCol > max {
		m.cursorCol = max
	}
	if m.cursorCol < 0 {
		m.cursorCol = 0
	}
}

// splitLineAtCursor breaks the current line at the cursor into two lines,
// leaving the cursor at the start of the new line. Shared by Enter and by
// newline runes in a multi-line paste.
func (m *Model) splitLineAtCursor() {
	line := m.lines[m.cursorRow]
	off, _, _ := graphemeOffset(line, max(m.cursorCol, 0))
	before, after := line[:off], line[off:]
	m.lines[m.cursorRow] = before
	newLines := make([]string, len(m.lines)+1)
	copy(newLines, m.lines[:m.cursorRow+1])
	newLines[m.cursorRow+1] = after
	copy(newLines[m.cursorRow+2:], m.lines[m.cursorRow+1:])
	m.lines = newLines
	m.cursorRow++
	m.cursorCol = 0
}

// ReplaceRange replaces the inclusive character range [start, end] with text,
// splicing exactly that span so characters on the boundary lines outside the
// range are preserved. Columns are rune indices. text may contain newlines.
// Unlike SetContent, this does not reset the buffer's saved state: Dirty is
// recomputed against origContent, so an applied LLM edit correctly reads as
// unsaved until it is written. The cursor is left at the start of the
// replacement and visual mode is cleared.
func (m *Model) ReplaceRange(start, end Position, text string) {
	if len(m.lines) == 0 {
		m.lines = []string{""}
	}

	// Record an undo point so an applied LLM edit can be reverted like any other.
	m.pushUndo()
	m.insertUndoArmed = false

	// Clamp rows into range, keeping start <= end.
	if start.Row < 0 {
		start.Row = 0
	}
	if start.Row > len(m.lines)-1 {
		start.Row = len(m.lines) - 1
	}
	if end.Row < start.Row {
		end.Row = start.Row
	}
	if end.Row > len(m.lines)-1 {
		end.Row = len(m.lines) - 1
	}

	startRunes := graphemes(m.lines[start.Row])
	endRunes := graphemes(m.lines[end.Row])

	// Clamp columns. end.Col is inclusive, so the splice point after the
	// selection is end.Col+1.
	sc := start.Col
	if sc < 0 {
		sc = 0
	}
	if sc > len(startRunes) {
		sc = len(startRunes)
	}
	ec := end.Col + 1
	if ec < 0 {
		ec = 0
	}
	if ec > len(endRunes) {
		ec = len(endRunes)
	}
	if start.Row == end.Row && ec < sc {
		ec = sc
	}

	prefix := joinGraphemes(startRunes[:sc])
	suffix := joinGraphemes(endRunes[ec:])
	spliced := strings.Split(prefix+text+suffix, "\n")

	newLines := make([]string, 0, len(m.lines)-(end.Row-start.Row)+len(spliced))
	newLines = append(newLines, m.lines[:start.Row]...)
	newLines = append(newLines, spliced...)
	newLines = append(newLines, m.lines[end.Row+1:]...)
	m.lines = newLines

	// Leave the cursor at the start of the replacement, and drop any stale
	// visual selection so it can't linger over now-shifted text.
	m.Mode = ModeNormal
	m.selectionAnchor = Position{}
	m.cursorRow = start.Row
	m.cursorCol = sc
	if m.cursorCol > runeLen(m.lines[m.cursorRow]) {
		m.cursorCol = runeLen(m.lines[m.cursorRow])
	}
	m.finalizeMutation()
	// A range replace can change the line count and shift every following row, so
	// the incremental per-row update is unsafe here; recompute wholesale. This also
	// covers ReplaceRange calls from outside the editor's own Update loop (e.g. the
	// app applying an accepted diff), where the Update-level bookkeeping never runs.
	m.recomputeDirty()
	m.ensureCursorVisible()
}

// clampPosition confines p to a valid location in the current buffer. The
// selection anchor is not moved when the buffer shrinks under it, so every
// consumer of a stored Position must clamp before indexing m.lines.
func (m Model) clampPosition(p Position) Position {
	if len(m.lines) == 0 {
		return Position{}
	}
	if p.Row < 0 {
		p.Row = 0
	}
	if p.Row >= len(m.lines) {
		p.Row = len(m.lines) - 1
	}
	if p.Col < 0 {
		p.Col = 0
	}
	if p.Col > runeLen(m.lines[p.Row]) {
		p.Col = runeLen(m.lines[p.Row])
	}
	return p
}
