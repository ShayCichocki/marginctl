package editor

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

type renderLineState struct {
	src      string // line text the styling was computed from; a mismatch invalidates it
	clusters []string
	italic   []bool
	search   []bool
	base     lipgloss.Style
}

func (m *Model) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}

	key := editorViewKey{revision: m.revision,
		cursorRow: m.cursorRow, cursorCol: m.cursorCol, viewOffset: m.viewOffset,
		width: m.width, height: m.height, mode: m.Mode, focused: m.focused,
		search: m.lastSearch, selection: m.selectionAnchor, notesRevision: m.notesRevision}
	if m.viewCacheValid && key == m.viewCacheKey {
		return m.viewCache
	}
	displayLines := m.buildDisplayLines()
	cursorDisplayLine := m.findCursorDisplayLine(displayLines)
	var selectionStart, selectionEnd Position
	if m.Mode == ModeVisual {
		selectionStart, selectionEnd = m.normalizeSelectionCached()
	}

	var lines []string

	endIdx := m.viewOffset + m.height
	if endIdx > len(displayLines) {
		endIdx = len(displayLines)
	}

	noted := m.noteRows()
	lastLogicalRow := -1
	for i := m.viewOffset; i < endIdx; i++ {
		dl := displayLines[i]
		isCursorLine := (i == cursorDisplayLine)

		// Line number - only show on first display line of logical line. A line
		// with a note replaces the gutter's trailing space with a marker.
		gw := m.gutterWidth()
		var lineNum string
		if dl.logicalRow != lastLogicalRow {
			marker := " "
			if noted[dl.logicalRow] {
				marker = "●"
			}
			lineNum = m.styles.EditorLineNumber.Render(fmt.Sprintf("%*d", gw-1, dl.logicalRow+1)) + m.styleNoteMarker.Render(marker)
			lastLogicalRow = dl.logicalRow
		} else {
			lineNum = m.styles.EditorLineNumber.Render(strings.Repeat(" ", gw))
		}

		// Content
		var content string
		if m.Mode == ModeVisual {
			content = m.renderSelectionLine(dl, isCursorLine, selectionStart, selectionEnd)
		} else if isCursorLine {
			content = m.renderCursorLine(dl)
		} else {
			content = m.renderMarkdownLine(dl, m.lines[dl.logicalRow])
		}

		lines = append(lines, lineNum+content)
	}

	// Pad remaining lines with ~
	for i := endIdx; i < m.viewOffset+m.height; i++ {
		lineNum := m.styles.EditorLineNumber.Render(strings.Repeat(" ", m.gutterWidth()))
		lines = append(lines, lineNum+"~")
	}

	m.viewCache = strings.Join(lines, "\n")
	m.viewCacheKey, m.viewCacheValid = key, true
	return m.viewCache
}

func (m *Model) renderCursorLine(dl displayLine) string {
	if len(dl.clusters) == 0 {
		return m.styles.EditorCursor.Render(" ")
	}
	out := m.renderRuns(dl, Position{}, Position{}, false, true)
	if m.cursorCol >= dl.endCol-dl.consumedWhitespace {
		out += m.styles.EditorCursor.Render(" ")
	}
	return out
}

func (m *Model) renderSelectionLine(dl displayLine, isCursorLine bool, start, end Position) string {
	if len(dl.clusters) == 0 {
		if isCursorLine {
			return m.styles.EditorCursor.Render(" ")
		}
		if positionInSelection(dl.logicalRow, 0, start, end) {
			return m.styles.EditorSelection.Render(" ")
		}
		return " "
	}
	out := m.renderRuns(dl, start, end, true, isCursorLine)
	if isCursorLine && m.cursorCol >= dl.endCol-dl.consumedWhitespace {
		out += m.styles.EditorCursor.Render(" ")
	}
	return out
}

type renderCue struct {
	italic, search, selected, cursor, cursorLine bool
}

func (m *Model) renderRuns(dl displayLine, selectionStart, selectionEnd Position, visual, cursorLine bool) string {
	state := m.renderState(dl.logicalRow)
	var out, run strings.Builder
	var previous renderCue
	haveRun := false
	flush := func() {
		if !haveRun {
			return
		}
		style := state.base.Italic(previous.italic)
		switch {
		case previous.cursor:
			style = style.Background(m.styles.EditorCursor.GetBackground())
		case previous.selected || previous.search:
			style = style.Background(m.styles.EditorSelection.GetBackground())
		case previous.cursorLine:
			style = style.Background(m.styles.EditorCursorLine.GetBackground())
		}
		out.WriteString(style.Render(run.String()))
		run.Reset()
	}
	for i, cluster := range dl.clusters {
		col := dl.startCol + i
		cue := renderCue{italic: state.italic[col], search: state.search[col], cursorLine: cursorLine}
		cue.selected = visual && positionInSelection(dl.logicalRow, col, selectionStart, selectionEnd)
		cue.cursor = cursorLine && col == m.cursorCol
		if haveRun && cue != previous {
			flush()
			haveRun = false
		}
		if !haveRun {
			previous, haveRun = cue, true
		}
		run.WriteString(cluster)
	}
	flush()
	return out.String()
}

func positionInSelection(row, col int, start, end Position) bool {
	return row >= start.Row && row <= end.Row &&
		(row != start.Row || col >= start.Col) && (row != end.Row || col <= end.Col)
}

func (m *Model) normalizeSelectionCached() (Position, Position) {
	clamp := func(p Position) Position {
		p.Row = max(0, min(p.Row, len(m.lines)-1))
		p.Col = max(0, min(p.Col, len(m.logicalClusters(p.Row))))
		return p
	}
	anchor, cursor := clamp(m.selectionAnchor), clamp(Position{Row: m.cursorRow, Col: m.cursorCol})
	if anchor.Row < cursor.Row || (anchor.Row == cursor.Row && anchor.Col <= cursor.Col) {
		return anchor, cursor
	}
	return cursor, anchor
}

func (m *Model) renderMarkdownLine(dl displayLine, fullLogicalLine string) string {
	// Render the raw display text (markers included) in the heading style rather
	// than stripping the "#" and re-indenting. Stripping only happened on
	// non-cursor lines, so a heading jumped 1–5 columns whenever the cursor
	// entered or left it. Keeping the text as-is means the columns are identical
	// in every render path (v2 #63).
	state := m.renderState(dl.logicalRow)
	start := dl.startCol
	end := min(start+len(dl.clusters), len(state.clusters))
	var b, run strings.Builder
	var previous lipgloss.Style
	var previousItalic, previousSearch bool
	haveRun := false
	flush := func() {
		if haveRun {
			b.WriteString(previous.Render(run.String()))
			run.Reset()
		}
	}
	for col := start; col < end; col++ {
		style := m.styleAt(state, col)
		if state.search[col] {
			style = style.Background(m.styles.EditorSelection.GetBackground())
		}
		italic, searched := state.italic[col], state.search[col]
		if haveRun && (italic != previousItalic || searched != previousSearch) {
			flush()
			haveRun = false
		}
		if !haveRun {
			previous, haveRun = style, true
			previousItalic, previousSearch = italic, searched
		}
		run.WriteString(state.clusters[col])
	}
	flush()
	return b.String()
}

func (m *Model) renderState(row int) renderLineState {
	if m.renderLines == nil || m.renderLineRevision != m.revision || m.renderLineQuery != m.lastSearch {
		m.renderLineRevision, m.renderLineQuery = m.revision, m.lastSearch
		m.renderLines = make(map[int]renderLineState)
	}
	if state, ok := m.renderLines[row]; ok && state.src == m.lines[row] {
		return state
	}
	clusters := m.logicalClusters(row)
	state := renderLineState{src: m.lines[row], clusters: clusters, italic: make([]bool, len(clusters)), search: make([]bool, len(clusters)), base: m.markdownStyle(m.lines[row])}
	insideStar, insideUnderscore := false, false
	for i, cluster := range clusters {
		state.italic[i] = (insideStar && cluster != "*") || (insideUnderscore && cluster != "_")
		switch cluster {
		case "*":
			insideStar = !insideStar
		case "_":
			insideUnderscore = !insideUnderscore
		}
	}
	q := foldRunes(m.lastSearch)
	line := foldRunes(m.lines[row])
	for start := 0; len(q) > 0 && start+len(q) <= len(line); start++ {
		if matchAt(line, start, q) {
			for i := start; i < start+len(q) && i < len(state.search); i++ {
				state.search[i] = true
			}
		}
	}
	m.renderLines[row] = state
	return state
}

func (m Model) styleAt(state renderLineState, col int) lipgloss.Style {
	base := state.base
	if col >= 0 && col < len(state.italic) && state.italic[col] {
		base = base.Italic(true)
	}
	return base
}

func (m Model) markdownStyle(fullLogicalLine string) lipgloss.Style {
	if style := m.headingStyle(fullLogicalLine); style != nil {
		return *style
	}
	return m.styles.EditorTextStyle
}

// headingStyle returns the style for an ATX heading line (nil if not a heading).
func (m Model) headingStyle(fullLogicalLine string) *lipgloss.Style {
	trimmed := strings.TrimSpace(fullLogicalLine)
	switch {
	case strings.HasPrefix(trimmed, "# "):
		return &m.styles.EditorH1
	case strings.HasPrefix(trimmed, "## "):
		return &m.styles.EditorH2
	case strings.HasPrefix(trimmed, "### "):
		return &m.styles.EditorH3
	}
	return nil
}
