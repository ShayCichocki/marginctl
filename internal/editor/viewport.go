package editor

func (m *Model) ensureCursorVisible() {
	// With no known height yet (before the first WindowSizeMsg) there's no
	// viewport to fit the cursor into; computing an offset here would scroll past
	// the content. Leave viewOffset alone until SetSize re-runs this.
	if m.height <= 0 {
		return
	}

	displayLines := m.buildDisplayLines()
	cursorDisplayLine := m.findCursorDisplayLine(displayLines)

	if cursorDisplayLine < m.viewOffset {
		m.viewOffset = cursorDisplayLine
	}
	if cursorDisplayLine >= m.viewOffset+m.height {
		m.viewOffset = cursorDisplayLine - m.height + 1
	}
	maxOffset := max(0, len(displayLines)-m.height)
	if m.viewOffset > maxOffset {
		m.viewOffset = maxOffset
	}
	if m.viewOffset < 0 {
		m.viewOffset = 0
	}
}

// pageLines is the number of display lines a full-page scroll (Ctrl+F/Ctrl+B)
// advances by; one line of overlap is kept for context, vim-style. A floor of 1
// keeps scrolling sane on tiny or not-yet-sized viewports.
func (m *Model) pageLines() int {
	if m.height < 2 {
		return 1
	}
	return m.height - 1
}

// halfPageLines is the scroll distance for Ctrl+D/Ctrl+U.
func (m *Model) halfPageLines() int {
	if h := m.height / 2; h > 0 {
		return h
	}
	return 1
}

// scrollDisplayLines moves the cursor n display lines down (or up) and lets
// ensureCursorVisible carry the viewport along — the mechanism behind the vim
// page/half-page scroll bindings Ctrl+F/Ctrl+B/Ctrl+D/Ctrl+U.
func (m *Model) scrollDisplayLines(n int, down bool) {
	for i := 0; i < n; i++ {
		if down {
			m.moveDownDisplayLine()
		} else {
			m.moveUpDisplayLine()
		}
	}
}

// moveDownDisplayLine moves cursor down one display line
func (m *Model) moveDownDisplayLine() { m.moveDisplayLine(1) }

// moveUpDisplayLine moves cursor up one display line
func (m *Model) moveUpDisplayLine() { m.moveDisplayLine(-1) }

// moveDisplayLine moves the cursor delta display lines (+1 or -1), landing on
// the source column whose cell is nearest the cursor's current display column.
// Segment columns are raw grapheme indices (the row cache sanitizes each raw
// cluster on its own), so no coordinate translation is needed.
func (m *Model) moveDisplayLine(delta int) {
	displayLines := m.buildDisplayLines()
	cursorDL := m.findCursorDisplayLine(displayLines)
	target := cursorDL + delta
	if target < 0 || target >= len(displayLines) {
		return
	}
	colOffset := 0
	if cursorDL < len(displayLines) {
		colOffset = segmentDisplayColumn(displayLines[cursorDL], m.cursorCol)
	}
	seg := displayLines[target]
	col := segmentSourceColumn(seg, colOffset)
	// A hard-wrapped segment with no consumed separator ends where the next one
	// starts; that column belongs to the next display line, so stay on this one.
	if r := m.displayRowRanges[seg.logicalRow]; target < r.end-1 && col >= seg.endCol && col > seg.startCol {
		col = seg.endCol - 1
	}
	m.cursorRow = seg.logicalRow
	m.cursorCol = min(col, m.rowEntry(m.cursorRow).rawLen) // cheap safety; always true
}
