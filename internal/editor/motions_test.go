package editor

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func escKey() tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyEscape} }

// feedKeys sends each character of s as its own normal-mode keypress.
func feedKeys(m Model, s string) Model {
	for _, r := range s {
		m = feed(m, key(string(r)))
	}
	return m
}

func TestOperatorDeleteWord(t *testing.T) {
	withStubClipboard(t)
	m := New()
	m.SetSize(80, 10)
	m.SetContent("foo bar baz")

	m = feedKeys(m, "dw")
	if got, want := m.Content(), "bar baz"; got != want {
		t.Fatalf("dw content = %q, want %q", got, want)
	}
	if m.register != "foo " {
		t.Fatalf("register after dw = %q, want %q", m.register, "foo ")
	}
}

func TestOperatorChangeWordActsLikeChangeToEnd(t *testing.T) {
	withStubClipboard(t)
	m := New()
	m.SetSize(80, 10)
	m.SetContent("foo bar baz")

	// cw on "foo" should change just the word (like ce), leaving the space.
	m = feedKeys(m, "cw")
	if m.Mode != ModeInsert {
		t.Fatalf("cw should enter insert mode, got %v", m.Mode)
	}
	m = feedKeys(m, "ZAP") // typed in insert mode (uppercase letters are runes)
	m = feed(m, escKey())
	if got, want := m.Content(), "ZAP bar baz"; got != want {
		t.Fatalf("cw content = %q, want %q", got, want)
	}
}

func TestOperatorYankWord(t *testing.T) {
	withStubClipboard(t)
	m := New()
	m.SetSize(80, 10)
	m.SetContent("alpha beta")

	m = feedKeys(m, "yw")
	if m.register != "alpha " {
		t.Fatalf("yw register = %q, want %q", m.register, "alpha ")
	}
	// Yank does not modify the buffer and leaves the cursor at the start.
	if m.Content() != "alpha beta" || m.cursorCol != 0 {
		t.Fatalf("yw changed buffer/cursor: content=%q col=%d", m.Content(), m.cursorCol)
	}
}

func TestOperatorDeleteToEndOfLine(t *testing.T) {
	withStubClipboard(t)
	m := New()
	m.SetSize(80, 10)
	m.SetContent("keep this away")
	m = feedKeys(m, "w")  // cursor at "this"
	m = feedKeys(m, "d$") // delete to end of line
	if got, want := m.Content(), "keep "; got != want {
		t.Fatalf("d$ content = %q, want %q", got, want)
	}
}

func TestOperatorDeleteToLineStart(t *testing.T) {
	withStubClipboard(t)
	m := New()
	m.SetSize(80, 10)
	m.SetContent("keep this") // cols: k0 e1 e2 p3 ' '4 t5 h6 i7 s8
	m = feedKeys(m, "w")      // to "this" (col 5)
	m = feedKeys(m, "ll")     // to 'i' (col 7)
	m = feedKeys(m, "d0")     // delete cols [0,6], leaving "is"
	if got, want := m.Content(), "is"; got != want {
		t.Fatalf("d0 content = %q, want %q", got, want)
	}
}

func TestCountedMotionAndDelete(t *testing.T) {
	withStubClipboard(t)
	m := New()
	m.SetSize(80, 10)
	m.SetContent("one two three four")

	// d3w deletes three words.
	m = feedKeys(m, "d3w")
	if got, want := m.Content(), "four"; got != want {
		t.Fatalf("d3w content = %q, want %q", got, want)
	}
}

func TestCountBeforeOperator(t *testing.T) {
	withStubClipboard(t)
	m := New()
	m.SetSize(80, 10)
	m.SetContent("a b c d e")

	// 3dw is equivalent to d3w.
	m = feedKeys(m, "3dw")
	if got, want := m.Content(), "d e"; got != want {
		t.Fatalf("3dw content = %q, want %q", got, want)
	}
}

func TestDoubledDeleteLinesWithCount(t *testing.T) {
	withStubClipboard(t)
	m := New()
	m.SetSize(80, 10)
	m.SetContent("l1\nl2\nl3\nl4")

	// 2dd deletes two lines starting at the cursor.
	m = feedKeys(m, "2dd")
	if got, want := m.Content(), "l3\nl4"; got != want {
		t.Fatalf("2dd content = %q, want %q", got, want)
	}
	if !m.registerLine || m.register != "l1\nl2" {
		t.Fatalf("2dd register = %q linewise=%v", m.register, m.registerLine)
	}
}

func TestChangeLine(t *testing.T) {
	withStubClipboard(t)
	m := New()
	m.SetSize(80, 10)
	m.SetContent("first line\nsecond line")

	m = feedKeys(m, "cc")
	if m.Mode != ModeInsert {
		t.Fatalf("cc should enter insert, got %v", m.Mode)
	}
	m = feedKeys(m, "new")
	m = feed(m, escKey())
	if got, want := m.Content(), "new\nsecond line"; got != want {
		t.Fatalf("cc content = %q, want %q", got, want)
	}
}

func TestCountedVerticalMotion(t *testing.T) {
	m := New()
	m.SetSize(80, 20)
	m.SetContent("l0\nl1\nl2\nl3\nl4")

	m = feedKeys(m, "3j")
	if m.cursorRow != 3 {
		t.Fatalf("3j cursorRow = %d, want 3", m.cursorRow)
	}
	m = feedKeys(m, "2k")
	if m.cursorRow != 1 {
		t.Fatalf("2k cursorRow = %d, want 1", m.cursorRow)
	}
}

func TestPunctuationAwareWord(t *testing.T) {
	m := New()
	m.SetSize(80, 10)
	m.SetContent("foo.bar baz")

	// w from 'f' should stop at the '.' (punctuation is its own word run).
	m = feedKeys(m, "w")
	if m.cursorCol != 3 {
		t.Fatalf("w cursorCol = %d, want 3 (on '.')", m.cursorCol)
	}
	// Next w lands on "bar".
	m = feedKeys(m, "w")
	if m.cursorCol != 4 {
		t.Fatalf("second w cursorCol = %d, want 4 (on 'b')", m.cursorCol)
	}
}

func TestWordEndMotion(t *testing.T) {
	m := New()
	m.SetSize(80, 10)
	m.SetContent("alpha bravo")
	m = feedKeys(m, "e")
	if m.cursorCol != 4 {
		t.Fatalf("e cursorCol = %d, want 4 (end of 'alpha')", m.cursorCol)
	}
}

func TestReplaceChar(t *testing.T) {
	m := New()
	m.SetSize(80, 10)
	m.SetContent("cat")
	m = feed(m, key("r"), key("b"))
	if got, want := m.Content(), "bat"; got != want {
		t.Fatalf("r content = %q, want %q", got, want)
	}
	if m.cursorCol != 0 {
		t.Fatalf("r cursorCol = %d, want 0", m.cursorCol)
	}
}

func TestCountedReplaceChar(t *testing.T) {
	m := New()
	m.SetSize(80, 10)
	m.SetContent("aaaa")
	m = feed(m, key("3"), key("r"), key("x"))
	if got, want := m.Content(), "xxxa"; got != want {
		t.Fatalf("3rx content = %q, want %q", got, want)
	}
	// 5r on a 4-char line is a no-op.
	m2 := New()
	m2.SetSize(80, 10)
	m2.SetContent("aaaa")
	m2 = feed(m2, key("5"), key("r"), key("x"))
	if got, want := m2.Content(), "aaaa"; got != want {
		t.Fatalf("5rx should be a no-op, content = %q", got)
	}
}

func TestParagraphMotions(t *testing.T) {
	m := New()
	m.SetSize(80, 20)
	m.SetContent("a\nb\n\nc\nd\n\ne")

	m = feedKeys(m, "}") // to first blank line (row 2)
	if m.cursorRow != 2 {
		t.Fatalf("} cursorRow = %d, want 2", m.cursorRow)
	}
	m = feedKeys(m, "}") // to next blank line (row 5)
	if m.cursorRow != 5 {
		t.Fatalf("second } cursorRow = %d, want 5", m.cursorRow)
	}
	m = feedKeys(m, "{") // back to previous blank (row 2)
	if m.cursorRow != 2 {
		t.Fatalf("{ cursorRow = %d, want 2", m.cursorRow)
	}
}

func TestMatchingBracket(t *testing.T) {
	m := New()
	m.SetSize(80, 10)
	m.SetContent("x = (a + (b))")

	// From col 0, % finds the first bracket '(' at col 4 and jumps to its match.
	m = feedKeys(m, "%")
	if m.cursorCol != 12 {
		t.Fatalf("%% cursorCol = %d, want 12 (matching ')')", m.cursorCol)
	}
	// % again jumps back.
	m = feedKeys(m, "%")
	if m.cursorCol != 4 {
		t.Fatalf("second %% cursorCol = %d, want 4", m.cursorCol)
	}
}

func TestGotoLineFirstNonBlank(t *testing.T) {
	m := New()
	m.SetSize(80, 20)
	m.SetContent("l0\n  indented\nl2")

	m = feedKeys(m, "G")
	if m.cursorRow != 2 {
		t.Fatalf("G cursorRow = %d, want 2", m.cursorRow)
	}
	m = feedKeys(m, "gg")
	if m.cursorRow != 0 || m.cursorCol != 0 {
		t.Fatalf("gg = (%d,%d), want (0,0)", m.cursorRow, m.cursorCol)
	}
	// 2gg -> line 1, first non-blank column (past the leading spaces).
	m = feedKeys(m, "2gg")
	if m.cursorRow != 1 || m.cursorCol != 2 {
		t.Fatalf("2gg = (%d,%d), want (1,2)", m.cursorRow, m.cursorCol)
	}
}

func TestGotoLineNG(t *testing.T) {
	m := New()
	m.SetSize(80, 20)
	m.SetContent("l0\nl1\nl2\nl3")
	m = feedKeys(m, "3G")
	if m.cursorRow != 2 {
		t.Fatalf("3G cursorRow = %d, want 2", m.cursorRow)
	}
}

func TestDeleteToTopLinewise(t *testing.T) {
	withStubClipboard(t)
	m := New()
	m.SetSize(80, 20)
	m.SetContent("l0\nl1\nl2\nl3")
	// Move to line 2, then dgg deletes lines 0..2 linewise.
	m = feedKeys(m, "jj")
	m = feedKeys(m, "dgg")
	if got, want := m.Content(), "l3"; got != want {
		t.Fatalf("dgg content = %q, want %q", got, want)
	}
}
