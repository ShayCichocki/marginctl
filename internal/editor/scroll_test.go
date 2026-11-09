package editor

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// invariant: the cursor's display line must always be within the rendered window
// [viewOffset, viewOffset+height).
func assertCursorVisible(t *testing.T, m *Model, step string) {
	t.Helper()
	dls := m.buildDisplayLines()
	cdl := m.findCursorDisplayLine(dls)
	if cdl < m.viewOffset || cdl >= m.viewOffset+m.height {
		t.Fatalf("%s: cursor display line %d OUTSIDE window [%d,%d) (height=%d, totalDL=%d, row=%d col=%d)",
			step, cdl, m.viewOffset, m.viewOffset+m.height, m.height, len(dls), m.cursorRow, m.cursorCol)
	}
	if m.viewOffset < 0 {
		t.Fatalf("%s: viewOffset negative: %d", step, m.viewOffset)
	}
}

func scrollDoc() string {
	para := "The entrance was the same entrance you would expect of an information center, maps and photos and maybe a cheesy and poorly designed poster or two of the surrounding region covering the walls with a few brochure holders sitting on the corner of the reception desk."
	var b strings.Builder
	for i := 0; i < 40; i++ {
		b.WriteString(para)
		b.WriteString("\n\n")
	}
	return b.String()
}

func rkey(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

// TestScrollThroughLongDocKeepsCursorVisible walks the cursor down through a long
// wrapped document, back up, and down again — the "scroll up then back down"
// path from the bug report — asserting the cursor never leaves the viewport.
func TestScrollThroughLongDocKeepsCursorVisible(t *testing.T) {
	m := Model{Mode: ModeNormal}
	m.SetSize(60, 20) // narrow → heavy wrapping; 20 visible rows
	m.SetContent(scrollDoc())

	for i := 0; i < 600; i++ {
		m.handleNormalMode(rkey("j"))
		assertCursorVisible(t, &m, "j down")
	}
	for i := 0; i < 600; i++ {
		m.handleNormalMode(rkey("k"))
		assertCursorVisible(t, &m, "k up")
	}
	for i := 0; i < 600; i++ {
		m.handleNormalMode(rkey("j"))
		assertCursorVisible(t, &m, "j down again")
	}
}

// TestScrollReachesLastLine walks the cursor all the way down and asserts it can
// reach the final display line (scrolling isn't cut short before end-of-document).
func TestScrollReachesLastLine(t *testing.T) {
	m := Model{Mode: ModeNormal}
	m.SetSize(60, 20)
	m.SetContent(scrollDoc())

	lastRow := len(m.lines) - 1
	for i := 0; i < 2000 && m.cursorRow < lastRow; i++ {
		m.handleNormalMode(rkey("j"))
	}
	if m.cursorRow != lastRow {
		t.Fatalf("cursor stopped at row %d, want last row %d", m.cursorRow, lastRow)
	}
	assertCursorVisible(t, &m, "at last line")
}

// TestTypingAtBottomStaysVisible mirrors adding text at the end of a long doc.
func TestTypingAtBottomStaysVisible(t *testing.T) {
	m := Model{Mode: ModeNormal}
	m.SetSize(60, 12)
	m.SetContent(scrollDoc())

	m.cursorRow = len(m.lines) - 1
	m.cursorCol = 0
	m.ensureCursorVisible()
	m.Mode = ModeInsert
	for i := 0; i < 400; i++ {
		m.handleInsertMode(rkey("x"))
		assertCursorVisible(t, &m, "typing at bottom")
	}
}

// pageDoc builds n one-column lines so display-line motion maps 1:1 to logical
// rows (no wrapping), letting the page-scroll assertions use exact row numbers.
func pageDoc(n int) string {
	rows := make([]string, n)
	for i := range rows {
		rows[i] = "x"
	}
	return strings.Join(rows, "\n")
}

// TestPageScrollMotions checks the vim Ctrl+F/B/D/U scroll bindings move the
// cursor by a full/half viewport page in Normal mode.
func TestPageScrollMotions(t *testing.T) {
	withStubClipboard(t)
	m := New()
	m.SetSize(80, 10) // height 10 → pageLines 9, halfPageLines 5
	m.SetContent(pageDoc(100))

	if row, _ := m.CursorPosition(); row != 0 {
		t.Fatalf("cursor should start at row 0, got %d", row)
	}

	m = feed(m, tea.KeyMsg{Type: tea.KeyCtrlF}) // down a full page
	if row, _ := m.CursorPosition(); row != 9 {
		t.Fatalf("after Ctrl+F row = %d, want 9", row)
	}
	m = feed(m, tea.KeyMsg{Type: tea.KeyCtrlD}) // down a half page
	if row, _ := m.CursorPosition(); row != 14 {
		t.Fatalf("after Ctrl+D row = %d, want 14", row)
	}
	m = feed(m, tea.KeyMsg{Type: tea.KeyCtrlU}) // up a half page
	if row, _ := m.CursorPosition(); row != 9 {
		t.Fatalf("after Ctrl+U row = %d, want 9", row)
	}
	m = feed(m, tea.KeyMsg{Type: tea.KeyCtrlB}) // up a full page → top
	if row, _ := m.CursorPosition(); row != 0 {
		t.Fatalf("after Ctrl+B row = %d, want 0", row)
	}
}

// TestPageScrollClampsAtEnds verifies scrolling never runs the cursor off the
// buffer at the top or bottom.
func TestPageScrollClampsAtEnds(t *testing.T) {
	withStubClipboard(t)
	m := New()
	m.SetSize(80, 10)
	m.SetContent(pageDoc(5)) // fewer lines than a page

	m = feed(m, tea.KeyMsg{Type: tea.KeyCtrlF})
	if row, _ := m.CursorPosition(); row != 4 {
		t.Fatalf("Ctrl+F past end: row = %d, want last line 4", row)
	}
	m = feed(m, tea.KeyMsg{Type: tea.KeyCtrlB})
	if row, _ := m.CursorPosition(); row != 0 {
		t.Fatalf("Ctrl+B past start: row = %d, want 0", row)
	}
}
