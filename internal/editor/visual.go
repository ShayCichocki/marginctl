package editor

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m *Model) handleVisualMode(msg tea.KeyMsg) {
	key := msg.String()

	// Second half of a `gg` motion — consistent with Normal mode, a single `g`
	// no longer jumps to the top; it waits for the second `g` (v2 #63).
	if m.pendingG {
		m.pendingG = false
		if key == "g" {
			m.cursorRow = 0
			m.cursorCol = 0
			m.ensureCursorVisible()
			return
		}
		// A lone `g` is cancelled; fall through and process this key normally.
	}

	switch key {
	case "escape", "esc":
		// Exit visual mode
		m.Mode = ModeNormal

	case "h", "left":
		if m.cursorCol > 0 {
			m.cursorCol--
		}

	case "l", "right":
		lineLen := runeLen(m.lines[m.cursorRow])
		if m.cursorCol < lineLen {
			m.cursorCol++
		}

	case "j", "down":
		m.moveDownDisplayLine()

	case "k", "up":
		m.moveUpDisplayLine()

	case "ctrl+f":
		m.scrollDisplayLines(m.pageLines(), true)

	case "ctrl+b":
		m.scrollDisplayLines(m.pageLines(), false)

	case "ctrl+d":
		m.scrollDisplayLines(m.halfPageLines(), true)

	case "ctrl+u":
		m.scrollDisplayLines(m.halfPageLines(), false)

	case "w":
		m.cursorRow, m.cursorCol = m.nextWordStart(m.cursorRow, m.cursorCol)

	case "b":
		m.cursorRow, m.cursorCol = m.prevWordStart(m.cursorRow, m.cursorCol)

	case "e":
		m.cursorRow, m.cursorCol = m.nextWordEnd(m.cursorRow, m.cursorCol)

	case "0":
		displayLines := m.buildDisplayLines()
		cursorDL := m.findCursorDisplayLine(displayLines)
		if cursorDL < len(displayLines) {
			m.cursorCol = displayLines[cursorDL].startCol
		}

	case "$":
		m.cursorCol = runeLen(m.lines[m.cursorRow])

	case "{":
		m.cursorRow = m.prevParagraph(m.cursorRow)
		m.cursorCol = 0

	case "}":
		m.cursorRow = m.nextParagraph(m.cursorRow)
		m.cursorCol = 0

	case "%":
		if tr, tc, ok := m.matchBracket(m.cursorRow, m.cursorCol); ok {
			m.cursorRow, m.cursorCol = tr, tc
		}

	case "g":
		// Wait for the second g (gg jumps to the top), like Normal mode.
		m.pendingG = true
		return

	case "G":
		m.cursorRow = len(m.lines) - 1
		if m.cursorRow < 0 {
			m.cursorRow = 0
		}
		m.cursorCol = runeLen(m.lines[m.cursorRow])

	case "y":
		// Yank the selection charwise, then return to normal mode with the
		// cursor at the start of what was selected.
		text, _, _ := m.GetSelection()
		start, _, _ := m.SelectionRange()
		m.setRegister(text, false)
		m.Mode = ModeNormal
		m.cursorRow, m.cursorCol = start.Row, start.Col

	case "d", "x":
		// Delete the selection into the register and splice it out exactly.
		text, _, _ := m.GetSelection()
		start, end, _ := m.SelectionRange()
		m.setRegister(text, false)
		m.ReplaceRange(start, end, "")

	case "c":
		// Change: delete the selection, then drop into insert mode. The delete
		// and the ensuing typing coalesce into a single undo (ReplaceRange left
		// insertUndoArmed false, so no new undo point is pushed while typing).
		text, _, _ := m.GetSelection()
		start, end, _ := m.SelectionRange()
		m.setRegister(text, false)
		m.ReplaceRange(start, end, "")
		m.Mode = ModeInsert
	}

	// Returning to Normal mode (esc, y, d/x) must leave the cursor on a real
	// character; a change (c) stays in insert mode and is left alone.
	if m.Mode == ModeNormal {
		m.clampNormalCol()
	}
	m.ensureCursorVisible()
}

// SetSelectionForTest puts the editor in Visual mode with the given anchor and
// cursor, so tests can create a selection without driving keystrokes.
func (m *Model) SetSelectionForTest(anchorRow, anchorCol, curRow, curCol int) {
	m.Mode = ModeVisual
	m.selectionAnchor = Position{Row: anchorRow, Col: anchorCol}
	m.cursorRow, m.cursorCol = curRow, curCol
}

// GetSelection returns the selected text and line range
// Returns empty string if no selection is active
func (m Model) GetSelection() (text string, startLine, endLine int) {
	if m.Mode != ModeVisual {
		return "", 0, 0
	}

	// Determine start and end positions (selection can go either direction)
	startPos, endPos := m.normalizeSelection()

	// Extract text
	var sb strings.Builder
	for row := startPos.Row; row <= endPos.Row; row++ {
		runes := graphemes(m.lines[row])
		startCol := 0
		endCol := len(runes)

		if row == startPos.Row {
			startCol = startPos.Col
		}
		if row == endPos.Row {
			endCol = endPos.Col + 1 // inclusive
			if endCol > len(runes) {
				endCol = len(runes)
			}
		}
		if startCol < 0 {
			startCol = 0
		}
		if startCol > len(runes) {
			startCol = len(runes)
		}
		if endCol < startCol {
			endCol = startCol
		}

		sb.WriteString(joinGraphemes(runes[startCol:endCol]))
		if row < endPos.Row {
			sb.WriteString("\n")
		}
	}

	return sb.String(), startPos.Row, endPos.Row
}

// SelectionRange returns the normalized inclusive bounds of the active visual
// selection. ok is false when not in visual mode. The bounds line up with the
// text returned by GetSelection: start is the first selected character and end
// is the last one (inclusive).
func (m Model) SelectionRange() (start, end Position, ok bool) {
	if m.Mode != ModeVisual {
		return Position{}, Position{}, false
	}
	start, end = m.normalizeSelection()
	return start, end, true
}

// normalizeSelection returns start and end positions with start always before end
func (m Model) normalizeSelection() (start, end Position) {
	anchor := m.clampPosition(m.selectionAnchor)
	cursor := m.clampPosition(Position{Row: m.cursorRow, Col: m.cursorCol})

	// Compare positions
	if anchor.Row < cursor.Row || (anchor.Row == cursor.Row && anchor.Col <= cursor.Col) {
		return anchor, cursor
	}
	return cursor, anchor
}

// HasSelection returns true if there is an active selection
func (m Model) HasSelection() bool {
	return m.Mode == ModeVisual
}
