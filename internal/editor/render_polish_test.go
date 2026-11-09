package editor

import (
	"strings"
	"testing"
)

// v2 #63 — a heading occupies the same columns whether or not the cursor is on
// it (no more 1–5 column jump when entering/leaving the line).
func TestHeadingDoesNotShiftUnderCursor(t *testing.T) {
	m := New()
	m.SetSize(80, 10)
	m.SetContent("## Chapter Two\nbody text")

	// Cursor on the body line: heading is a non-cursor line.
	m.cursorRow, m.cursorCol = 1, 0
	m.ensureCursorVisible()
	off := lineContainingVisible(t, m, "Chapter Two")

	// Move the cursor onto the heading.
	m.cursorRow, m.cursorCol = 0, 0
	m.ensureCursorVisible()
	on := lineContainingVisible(t, m, "Chapter Two")

	if col(off) != col(on) {
		t.Fatalf("heading text column shifted: off-cursor %q vs on-cursor %q", off, on)
	}
	// The marker text itself must still be present (not stripped) in both.
	if !strings.Contains(off, "## Chapter Two") || !strings.Contains(on, "## Chapter Two") {
		t.Fatalf("heading text differs between states: %q / %q", off, on)
	}
}

// col returns the byte index of the first non-space, non-gutter content — the
// visible start column, ignoring ANSI styling.
func col(styledLine string) int {
	plain := stripANSI(styledLine)
	return strings.Index(plain, "Chapter Two")
}

func lineContainingVisible(t *testing.T, m Model, want string) string {
	t.Helper()
	for _, ln := range strings.Split(m.View(), "\n") {
		if strings.Contains(stripANSI(ln), want) {
			return ln
		}
	}
	t.Fatalf("no rendered line contains %q", want)
	return ""
}

func stripANSI(s string) string {
	var b strings.Builder
	inEsc := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == 0x1b {
			inEsc = true
			continue
		}
		if inEsc {
			if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
				inEsc = false
			}
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

// v2 #63 — the line-number gutter widens for ≥1000-line buffers so 4-digit
// numbers align with the continuation-line blanks.
func TestGutterWidensForLargeBuffers(t *testing.T) {
	small := New()
	small.SetContent(strings.Repeat("x\n", 50))
	if small.gutterWidth() != minGutterWidth {
		t.Fatalf("small buffer gutter = %d, want %d", small.gutterWidth(), minGutterWidth)
	}

	big := New()
	big.SetContent(strings.Repeat("x\n", 1500)) // line numbers reach 4 digits
	if got := big.gutterWidth(); got != 5 {     // 4 digits + trailing space
		t.Fatalf("1500-line buffer gutter = %d, want 5", got)
	}
	// The rendered number column must be exactly gutterWidth cells wide.
	big.SetSize(80, 5)
	big.cursorRow = 1200
	big.ensureCursorVisible()
	for _, ln := range strings.Split(big.View(), "\n") {
		plain := stripANSI(ln)
		// Everything up to the first non-digit/space is the gutter.
		i := 0
		for i < len(plain) && (plain[i] == ' ' || (plain[i] >= '0' && plain[i] <= '9')) {
			i++
		}
		if i < big.gutterWidth() {
			t.Fatalf("gutter narrower than %d in %q", big.gutterWidth(), plain)
		}
	}
}

// v2 #63 — in Visual mode a single g waits for a second g (gg) rather than
// jumping to the top immediately.
func TestVisualGRequiresDoubleG(t *testing.T) {
	m := New()
	m.SetSize(80, 10)
	m.SetContent("line0\nline1\nline2\nline3")
	m.cursorRow, m.cursorCol = 3, 0

	m.Mode = ModeVisual
	m.selectionAnchor = Position{Row: 3, Col: 0}

	m = feed(m, key("g")) // first g: pending, no move
	if m.cursorRow != 3 {
		t.Fatalf("single g moved the cursor to row %d; should wait for gg", m.cursorRow)
	}
	m = feed(m, key("g")) // second g: jump to top
	if m.cursorRow != 0 {
		t.Fatalf("gg did not jump to top; row = %d", m.cursorRow)
	}
	if m.Mode != ModeVisual {
		t.Fatalf("gg must stay in Visual mode; mode = %v", m.Mode)
	}
}
