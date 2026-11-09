package editor

import "strings"

// extractRange returns the text in the inclusive [start, end] range.
func (m Model) extractRange(start, end Position) string {
	if end.Row < start.Row || (end.Row == start.Row && end.Col < start.Col) {
		start, end = end, start
	}
	var sb strings.Builder
	for row := start.Row; row <= end.Row; row++ {
		r := m.lineRunes(row)
		sc, ec := 0, len(r)
		if row == start.Row {
			sc = start.Col
		}
		if row == end.Row {
			ec = end.Col + 1
		}
		if sc < 0 {
			sc = 0
		}
		if sc > len(r) {
			sc = len(r)
		}
		if ec > len(r) {
			ec = len(r)
		}
		if ec < sc {
			ec = sc
		}
		sb.WriteString(joinGraphemes(r[sc:ec]))
		if row < end.Row {
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

// prevPosition returns the position one rune before p, crossing to the end of
// the previous line when needed.
func (m Model) prevPosition(p Position) Position {
	if p.Col > 0 {
		return Position{Row: p.Row, Col: p.Col - 1}
	}
	if p.Row > 0 {
		return Position{Row: p.Row - 1, Col: runeLen(m.lines[p.Row-1])}
	}
	return Position{Row: 0, Col: 0}
}

func posLess(a, b Position) bool {
	return a.Row < b.Row || (a.Row == b.Row && a.Col < b.Col)
}

// applyOperator runs a pending operator (d/c/y) over the resolved motion.
func (m *Model) applyOperator(op string, mo motion) {
	if mo.linewise {
		m.applyLineOperator(op, mo.row)
		return
	}
	m.applyCharOperator(op, Position{Row: mo.row, Col: mo.col}, mo.inclusive)
}

// applyCharOperator deletes/yanks/changes the charwise range from the cursor to
// target. inclusive controls whether target's character is part of the range.
func (m *Model) applyCharOperator(op string, target Position, inclusive bool) {
	cur := Position{Row: m.cursorRow, Col: m.cursorCol}
	start, end := cur, target
	if posLess(end, start) {
		// Backward motion: delete [target, cursor) — exclusive of the cursor.
		start, end = end, cur
		inclusive = false
	}

	delEnd := end
	if !inclusive {
		delEnd = m.prevPosition(end)
		if posLess(delEnd, start) {
			return // empty range, nothing to do
		}
	}

	text := m.extractRange(start, delEnd)
	m.setRegister(text, false)

	switch op {
	case "y":
		m.cursorRow, m.cursorCol = start.Row, start.Col
		m.Mode = ModeNormal
		m.clampNormalCol()
	case "d":
		m.ReplaceRange(start, delEnd, "")
	case "c":
		m.ReplaceRange(start, delEnd, "")
		m.Mode = ModeInsert // coalesces into ReplaceRange's undo entry
	}
}

// applyLineOperator runs a linewise operator over the logical lines between the
// cursor row and endRow (inclusive).
func (m *Model) applyLineOperator(op string, endRow int) {
	startRow := m.cursorRow
	if endRow < startRow {
		startRow, endRow = endRow, startRow
	}
	if startRow < 0 {
		startRow = 0
	}
	if endRow > len(m.lines)-1 {
		endRow = len(m.lines) - 1
	}

	text := strings.Join(m.lines[startRow:endRow+1], "\n")
	m.setRegister(text, true)

	if op == "y" {
		m.cursorRow = startRow
		m.cursorCol = firstNonBlank(m.lines[startRow])
		m.Mode = ModeNormal
		m.ensureCursorVisible()
		return
	}

	m.pushUndo()
	remaining := make([]string, 0, len(m.lines))
	remaining = append(remaining, m.lines[:startRow]...)
	if op == "c" {
		// cc: clear the line(s) to a single empty line and enter insert.
		remaining = append(remaining, "")
	}
	remaining = append(remaining, m.lines[endRow+1:]...)
	if len(remaining) == 0 {
		remaining = []string{""}
	}
	m.lines = remaining

	m.cursorRow = startRow
	if m.cursorRow > len(m.lines)-1 {
		m.cursorRow = len(m.lines) - 1
	}
	if op == "c" {
		m.cursorCol = 0
		m.Mode = ModeInsert
	} else {
		m.cursorCol = firstNonBlank(m.lines[m.cursorRow])
		m.Mode = ModeNormal
	}
	m.finalizeMutation()
	m.ensureCursorVisible()
}

// applyReplace implements `r`: replace count characters under the cursor with
// repl, staying on the current line. A no-op if count exceeds the characters
// remaining on the line.
func (m *Model) applyReplace(repl rune, count int) {
	r := graphemes(m.lines[m.cursorRow])
	if m.cursorCol+count > len(r) {
		return
	}
	m.pushUndo()
	for i := 0; i < count; i++ {
		r[m.cursorCol+i] = string(repl)
	}
	m.lines[m.cursorRow] = joinGraphemes(r)
	m.cursorCol += count - 1
	m.finalizeMutation()
}

func firstRune(s string) rune {
	for _, r := range s {
		return r
	}
	return 0
}
