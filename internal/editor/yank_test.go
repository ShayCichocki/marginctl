package editor

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// withStubClipboard isolates a test from the real OS clipboard: writes are
// captured in-memory and reads return that captured value, so paste behaves
// deterministically regardless of the host.
func withStubClipboard(t *testing.T) {
	t.Helper()
	prevW, prevR := osClipboardWrite, osClipboardRead
	var buf string
	osClipboardWrite = func(s string) { buf = s }
	osClipboardRead = func() (string, bool) { return buf, true }
	t.Cleanup(func() {
		osClipboardWrite = prevW
		osClipboardRead = prevR
	})
}

func enterVisual(m Model, anchorCol, cursorCol int) Model {
	m.Mode = ModeVisual
	m.selectionAnchor = Position{Row: 0, Col: anchorCol}
	m.cursorRow, m.cursorCol = 0, cursorCol
	return m
}

func TestVisualYankAndPasteCharwise(t *testing.T) {
	withStubClipboard(t)
	m := New()
	m.SetSize(80, 10)
	m.SetContent("The quick brown fox")

	// Select "quick" (cols 4..8) and yank.
	m = enterVisual(m, 4, 8)
	m = feed(m, key("y"))
	if m.Mode != ModeNormal {
		t.Fatalf("visual y should return to normal mode, got %v", m.Mode)
	}
	if m.register != "quick" || m.registerLine {
		t.Fatalf("register = %q linewise=%v, want %q charwise", m.register, m.registerLine, "quick")
	}
	// Cursor returns to the start of the selection.
	if m.cursorCol != 4 {
		t.Fatalf("cursorCol after yank = %d, want 4", m.cursorCol)
	}

	// Move to end and paste after: "quick" gets inserted after the last char.
	m = feed(m, key("$"), key("p"))
	if got, want := m.Content(), "The quick brown foxquick"; got != want {
		t.Fatalf("after p content = %q, want %q", got, want)
	}
}

func TestVisualDeleteSplicesExactly(t *testing.T) {
	withStubClipboard(t)
	m := New()
	m.SetSize(80, 10)
	m.SetContent("The quick brown fox")

	// Select "quick " (cols 4..9) and delete it.
	m = enterVisual(m, 4, 9)
	m = feed(m, key("d"))

	if got, want := m.Content(), "The brown fox"; got != want {
		t.Fatalf("after visual d content = %q, want %q", got, want)
	}
	if m.register != "quick " {
		t.Fatalf("register = %q, want %q", m.register, "quick ")
	}
	if m.Mode != ModeNormal {
		t.Fatalf("mode after d = %v, want normal", m.Mode)
	}
}

func TestVisualChangeEntersInsertAndUndoesAsOne(t *testing.T) {
	withStubClipboard(t)
	m := New()
	m.SetSize(80, 10)
	m.SetContent("The quick brown fox")

	m = enterVisual(m, 4, 8) // "quick"
	m = feed(m, key("c"))
	if m.Mode != ModeInsert {
		t.Fatalf("visual c should enter insert mode, got %v", m.Mode)
	}
	m = feed(m, key("s"), key("l"), key("o"), key("w"))
	m = feed(m, tea.KeyMsg{Type: tea.KeyEscape})
	if got, want := m.Content(), "The slow brown fox"; got != want {
		t.Fatalf("after change content = %q, want %q", got, want)
	}

	// A single undo restores the original text (delete + typing = one unit).
	m = feed(m, key("u"))
	if got, want := m.Content(), "The quick brown fox"; got != want {
		t.Fatalf("after undo content = %q, want %q (change must undo as one step)", got, want)
	}
}

func TestYankLineAndPasteLinewise(t *testing.T) {
	withStubClipboard(t)
	m := New()
	m.SetSize(80, 10)
	m.SetContent("line one\nline two\nline three")

	// yy on line 0, then p pastes a copy on a new line below.
	m = feed(m, key("y"), key("y"))
	if !m.registerLine || m.register != "line one" {
		t.Fatalf("register = %q linewise=%v, want %q linewise", m.register, m.registerLine, "line one")
	}
	m = feed(m, key("p"))
	if got, want := m.Content(), "line one\nline one\nline two\nline three"; got != want {
		t.Fatalf("after yy p content = %q, want %q", got, want)
	}
	// Cursor lands on the pasted line (row 1).
	if m.cursorRow != 1 {
		t.Fatalf("cursorRow after linewise paste = %d, want 1", m.cursorRow)
	}
}

func TestDeleteLineLinewiseAndPasteAbove(t *testing.T) {
	withStubClipboard(t)
	m := New()
	m.SetSize(80, 10)
	m.SetContent("alpha\nbravo\ncharlie")

	// dd on line 1 removes "bravo" and yanks it linewise.
	m = feed(m, key("j"), key("d"), key("d"))
	if got, want := m.Content(), "alpha\ncharlie"; got != want {
		t.Fatalf("after dd content = %q, want %q", got, want)
	}
	if !m.registerLine || m.register != "bravo" {
		t.Fatalf("register = %q linewise=%v, want %q linewise", m.register, m.registerLine, "bravo")
	}

	// P pastes it back above the current line.
	m = feed(m, key("P"))
	if got, want := m.Content(), "alpha\nbravo\ncharlie"; got != want {
		t.Fatalf("after P content = %q, want %q", got, want)
	}
}

func TestDeleteLastRemainingLineLeavesEmptyBuffer(t *testing.T) {
	withStubClipboard(t)
	m := New()
	m.SetSize(80, 10)
	m.SetContent("only line")

	m = feed(m, key("d"), key("d"))
	if got, want := m.Content(), ""; got != want {
		t.Fatalf("after dd on sole line content = %q, want empty", got)
	}
	if len(m.lines) != 1 {
		t.Fatalf("buffer must keep one (empty) line, got %d", len(m.lines))
	}
}

func TestExternalClipboardPasteIsCharwise(t *testing.T) {
	// Simulate an external copy: the clipboard holds text we never yanked.
	prevW, prevR := osClipboardWrite, osClipboardRead
	osClipboardWrite = func(string) {}
	osClipboardRead = func() (string, bool) { return "EXTERNAL", true }
	t.Cleanup(func() { osClipboardWrite, osClipboardRead = prevW, prevR })

	m := New()
	m.SetSize(80, 10)
	m.SetContent("ab")

	// clipMirror is empty, so the external clipboard text wins as a charwise paste.
	m = feed(m, key("p"))
	if got, want := m.Content(), "aEXTERNALb"; got != want {
		t.Fatalf("external paste content = %q, want %q", got, want)
	}
}

func TestPasteMultilineCharwise(t *testing.T) {
	withStubClipboard(t)
	m := New()
	m.SetSize(80, 10)
	m.SetContent("start end")
	// Manually seed a charwise multi-line register.
	m.setRegister("X\nY", false)

	// Cursor at col 0 ('s'); p inserts after it, splitting the line.
	m = feed(m, key("p"))
	if got, want := m.Content(), "sX\nYtart end"; got != want {
		t.Fatalf("multiline charwise paste content = %q, want %q", got, want)
	}
}
