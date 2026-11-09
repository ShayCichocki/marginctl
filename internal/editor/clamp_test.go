package editor

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestXAfterDollarDeletesLastChar(t *testing.T) {
	m := New()
	m.SetSize(80, 10)
	m.SetContent("hello")

	m = feed(m, key("$"))
	if m.cursorCol != 4 {
		t.Fatalf("$ cursorCol = %d, want 4", m.cursorCol)
	}
	m = feed(m, key("x"))
	if got, want := m.Content(), "hell"; got != want {
		t.Fatalf("after $x content = %q, want %q", got, want)
	}
}

func TestXAfterWordAtEndOfLineDeletesLastChar(t *testing.T) {
	m := New()
	m.SetSize(80, 10)
	m.SetContent("one two") // single line; w from 'one' lands at end

	// w moves to "two"; another w has nowhere to go and must clamp onto a char.
	m = feed(m, key("w"), key("w"))
	if m.cursorCol > runeLen(m.lines[0])-1 {
		t.Fatalf("cursorCol = %d must be clamped to <= %d", m.cursorCol, runeLen(m.lines[0])-1)
	}
	m = feed(m, key("x"))
	// Deleting at the clamped end-of-line position removes a real character.
	if strings.HasSuffix(m.Content(), "two") {
		t.Fatalf("x at end-of-line did not delete a character: %q", m.Content())
	}
}

func TestXAfterVisualDollarEscapeDeletesLastChar(t *testing.T) {
	m := New()
	m.SetSize(80, 10)
	m.SetContent("hello")

	// v$ selects to end of line (cursor at len), esc returns to normal.
	m = feed(m, key("v"), key("$"), tea.KeyMsg{Type: tea.KeyEscape})
	if m.cursorCol != 4 {
		t.Fatalf("after v$<esc> cursorCol = %d, want 4 (clamped onto last char)", m.cursorCol)
	}
	m = feed(m, key("x"))
	if got, want := m.Content(), "hell"; got != want {
		t.Fatalf("after v$<esc>x content = %q, want %q", got, want)
	}
}

func TestInsertEscapeClampsAtEndOfLine(t *testing.T) {
	m := New()
	m.SetSize(80, 10)
	m.SetContent("hi")

	// A (append at end) then type, then escape: cursor must land on last char.
	m = feed(m, key("A"), key("!"))
	m = feed(m, tea.KeyMsg{Type: tea.KeyEscape})
	if got, want := m.Content(), "hi!"; got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
	if m.cursorCol != 2 {
		t.Fatalf("after escape cursorCol = %d, want 2 (on the '!')", m.cursorCol)
	}
	m = feed(m, key("x"))
	if got, want := m.Content(), "hi"; got != want {
		t.Fatalf("after escape+x content = %q, want %q", got, want)
	}
}
