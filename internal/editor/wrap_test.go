package editor

import (
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"
)

// displayWidths returns the number of display lines and asserts none exceeds
// the given column budget.
func assertFits(t *testing.T, m Model, budget int) []displayLine {
	t.Helper()
	dls := m.buildDisplayLines()
	for i, dl := range dls {
		if w := runewidth.StringWidth(dl.text); w > budget {
			t.Fatalf("display line %d has width %d, exceeds budget %d: %q", i, w, budget, dl.text)
		}
	}
	return dls
}

func TestWrapReflowsToPaneWidth(t *testing.T) {
	m := New()
	m.SetContent(strings.Repeat("a", 200))

	// Narrow pane: width 24 -> 20 columns after the 4-col gutter -> 10 lines.
	m.SetSize(24, 40)
	dls := assertFits(t, m, 20)
	if len(dls) != 10 {
		t.Fatalf("narrow reflow produced %d display lines, want 10", len(dls))
	}

	// Wide pane: everything fits on one line.
	m.SetSize(500, 40)
	dls = m.buildDisplayLines()
	if len(dls) != 1 {
		t.Fatalf("wide reflow produced %d display lines, want 1", len(dls))
	}
}

func TestWrapAccountsForWideRunes(t *testing.T) {
	m := New()
	// 30 CJK ideographs, each 2 display columns wide (total width 60).
	m.SetContent(strings.Repeat("你", 30))
	m.SetSize(24, 40) // 20 columns

	dls := assertFits(t, m, 20)
	// 20 columns / 2 cols-per-rune = 10 runes per line -> 3 lines.
	if len(dls) != 3 {
		t.Fatalf("wide-rune reflow produced %d display lines, want 3", len(dls))
	}
}

func TestWrapPrefersWordBoundaries(t *testing.T) {
	m := New()
	m.SetContent("alpha bravo charlie delta")
	m.SetSize(16, 40) // 12 columns

	dls := m.buildDisplayLines()
	// No display line should begin or end with a space, and none should split a
	// word that would otherwise fit on its own line.
	for i, dl := range dls {
		if strings.HasPrefix(dl.text, " ") || strings.HasSuffix(dl.text, " ") {
			t.Fatalf("display line %d has edge whitespace: %q", i, dl.text)
		}
	}
	// First segment should be the first whole word(s) that fit, i.e. "alpha".
	if dls[0].text != "alpha bravo" && dls[0].text != "alpha" {
		t.Fatalf("first display line = %q, expected a word-boundary wrap", dls[0].text)
	}
}

func TestWrapFallsBackBeforeSized(t *testing.T) {
	m := New()
	// width==0 (never sized) must fall back to the default column count, not
	// wrap at zero and loop/spin.
	if got := m.wrapColumns(); got != wrapWidth {
		t.Fatalf("wrapColumns before sizing = %d, want %d", got, wrapWidth)
	}
	m.SetContent(strings.Repeat("x", 250))
	dls := m.buildDisplayLines()
	if len(dls) == 0 {
		t.Fatal("expected at least one display line")
	}
}

func TestWrapTinyWidthDoesNotPanic(t *testing.T) {
	m := New()
	m.SetContent("hello world this is a test")
	// Widths at or below the gutter must clamp to at least 1 column.
	for w := 0; w <= minGutterWidth+1; w++ {
		m.SetSize(w, 10)
		if cols := m.wrapColumns(); cols < 1 {
			t.Fatalf("wrapColumns(%d) = %d, must be >= 1", w, cols)
		}
		_ = m.buildDisplayLines() // must not panic
	}
}

func TestWrapSegmentsOwnEverySourceRune(t *testing.T) {
	for _, text := range []string{"alpha bravo", "abcd ef", "a\tb", "你好吗 x", "e\u0301clair test"} {
		segments := wrapSegments(text, 4)
		owned := make([]int, runeLen(text))
		for _, seg := range segments {
			if seg.displayWidth > 4 {
				t.Fatalf("%q segment width %d", text, seg.displayWidth)
			}
			for col := seg.startCol; col < seg.endCol; col++ {
				owned[col]++
			}
		}
		for col, n := range owned {
			if n != 1 {
				t.Fatalf("%q rune %d owned %d times: %#v", text, col, n, segments)
			}
		}
	}
}

func TestCursorOnConsumedSpaceAndVerticalDisplayColumn(t *testing.T) {
	m := New()
	m.SetContent("alpha bravo\na\t你z")
	m.SetSize(9, 10) // five content columns
	dls := m.buildDisplayLines()
	m.SetCursorPosition(0, 5) // consumed break separator belongs to first segment
	if got := m.findCursorDisplayLine(dls); got != 0 {
		t.Fatalf("break-space display line = %d, want 0", got)
	}
	m.SetCursorPosition(1, 2) // wide rune begins at display column four after tab expansion
	m.moveUpDisplayLine()
	target := dls[m.findCursorDisplayLine(dls)]
	if m.cursorCol < target.startCol || m.cursorCol > target.endCol {
		t.Fatalf("vertical target escaped segment: %d", m.cursorCol)
	}
	m.SetSize(12, 10)
	if got := m.findCursorDisplayLine(m.buildDisplayLines()); got < 0 {
		t.Fatal("cursor lost after resize")
	}
}
