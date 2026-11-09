package editor

import (
	"strings"
	"testing"
)

func TestViewSanitizesControlsWithoutChangingContentOrCursor(t *testing.T) {
	content := "ab\x1b[31mred\x07\r"
	m := New()
	m.SetContent(content)
	m.SetSize(40, 2)
	m.cursorCol = 5

	view := m.View()
	if strings.ContainsAny(view, "\x1b\x07\r") {
		t.Fatalf("view retained terminal controls: %q", view)
	}
	if got := m.Content(); got != content {
		t.Fatalf("render changed content: got %q want %q", got, content)
	}
	if m.cursorCol != 5 {
		t.Fatalf("render changed cursor column: %d", m.cursorCol)
	}
}

func TestReplaceRangeSingleLineMidParagraph(t *testing.T) {
	m := New()
	m.SetContent("The quick brown fox")

	// "quick" occupies columns 4..8 (inclusive).
	m.ReplaceRange(Position{Row: 0, Col: 4}, Position{Row: 0, Col: 8}, "slow")

	if got, want := m.Content(), "The slow brown fox"; got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
}

func TestReplaceRangeLeavesSurroundingLinesIntact(t *testing.T) {
	m := New()
	m.SetContent("line one\nline two\nline three")

	// From row0 col5 ("one") through row2 col3 ("line") inclusive.
	m.ReplaceRange(Position{Row: 0, Col: 5}, Position{Row: 2, Col: 3}, "X")

	if got, want := m.Content(), "line X three"; got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
}

func TestReplaceRangeMultiLineProposal(t *testing.T) {
	m := New()
	m.SetContent("alpha beta gamma")

	// Replace "beta" (cols 6..9) with a two-line proposal.
	m.ReplaceRange(Position{Row: 0, Col: 6}, Position{Row: 0, Col: 9}, "B1\nB2")

	if got, want := m.Content(), "alpha B1\nB2 gamma"; got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
}

func TestReplaceRangeMarksDirtyAndDoesNotResetSavedState(t *testing.T) {
	m := New()
	m.SetContent("hello world")
	if m.Dirty {
		t.Fatal("buffer should be clean immediately after SetContent")
	}

	m.ReplaceRange(Position{Row: 0, Col: 0}, Position{Row: 0, Col: 4}, "goodbye")

	if !m.Dirty {
		t.Fatal("buffer must be dirty after ReplaceRange applies an edit")
	}
	if got, want := m.Content(), "goodbye world"; got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
}

func TestReplaceRangeReplacingBackToOriginalIsClean(t *testing.T) {
	m := New()
	m.SetContent("hello world")

	// Replace "hello" with itself; content matches origContent again.
	m.ReplaceRange(Position{Row: 0, Col: 0}, Position{Row: 0, Col: 4}, "hello")

	if m.Dirty {
		t.Fatal("replacing text with an identical value should leave the buffer clean")
	}
}

func TestReplaceRangeClearsVisualModeAndMovesCursor(t *testing.T) {
	m := New()
	m.SetContent("The quick brown fox")
	m.Mode = ModeVisual
	m.selectionAnchor = Position{Row: 0, Col: 4}
	m.cursorRow, m.cursorCol = 0, 8

	m.ReplaceRange(Position{Row: 0, Col: 4}, Position{Row: 0, Col: 8}, "slow")

	if m.Mode != ModeNormal {
		t.Fatalf("mode = %v, want ModeNormal", m.Mode)
	}
	if m.cursorRow != 0 || m.cursorCol != 4 {
		t.Fatalf("cursor = (%d,%d), want (0,4)", m.cursorRow, m.cursorCol)
	}
}

func TestReplaceRangeClampsOutOfRangePositions(t *testing.T) {
	m := New()
	m.SetContent("short")

	// End row/col well past the buffer must clamp instead of panicking.
	m.ReplaceRange(Position{Row: 0, Col: 0}, Position{Row: 9, Col: 99}, "new")

	if got, want := m.Content(), "new"; got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
}

func TestSelectionRangeMatchesSelectedText(t *testing.T) {
	m := New()
	m.SetContent("The quick brown fox")
	m.Mode = ModeVisual
	m.selectionAnchor = Position{Row: 0, Col: 4}
	m.cursorRow, m.cursorCol = 0, 8

	text, _, _ := m.GetSelection()
	start, end, ok := m.SelectionRange()
	if !ok {
		t.Fatal("SelectionRange should report ok in visual mode")
	}
	if text != "quick" {
		t.Fatalf("selected text = %q, want %q", text, "quick")
	}

	// Applying an identity replacement over the reported range must reproduce
	// the same buffer, i.e. the range lines up with the extracted text.
	m2 := New()
	m2.SetContent("The quick brown fox")
	m2.ReplaceRange(start, end, text)
	if got, want := m2.Content(), "The quick brown fox"; got != want {
		t.Fatalf("round-trip content = %q, want %q", got, want)
	}
}

func TestSetContentClearsVisualSelection(t *testing.T) {
	m := New()
	m.SetContent("line one\nline two\nline three")
	m.Mode = ModeVisual
	m.selectionAnchor = Position{Row: 2, Col: 5}
	m.cursorRow, m.cursorCol = 2, 8

	// Loading a shorter buffer (e.g. a chapter switch) used to leave the anchor
	// pointing at a now-nonexistent row.
	m.SetContent("short")

	if m.Mode != ModeNormal {
		t.Fatalf("mode = %v, want ModeNormal after SetContent", m.Mode)
	}
	if _, _, ok := m.SelectionRange(); ok {
		t.Fatal("SelectionRange should not be active after SetContent")
	}
}

func TestGetSelectionSurvivesBufferShrink(t *testing.T) {
	m := New()
	m.SetContent("line one\nline two\nline three")
	// Force a stale visual selection anchored past the end of a shrunken buffer,
	// bypassing SetContent's reset to exercise the clamp in normalizeSelection.
	m.lines = []string{"short"}
	m.Mode = ModeVisual
	m.selectionAnchor = Position{Row: 5, Col: 40}
	m.cursorRow, m.cursorCol = 9, 99

	// Must not panic; the clamped range stays inside the buffer.
	text, startLine, endLine := m.GetSelection()
	if startLine < 0 || endLine >= len(m.lines) {
		t.Fatalf("lines out of range: start=%d end=%d len=%d", startLine, endLine, len(m.lines))
	}
	_ = text
}
