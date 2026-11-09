package editor

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func key(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func TestUndoRedoInsertSession(t *testing.T) {
	m := New()
	m.SetSize(80, 10)
	m.SetContent("hello")

	// Append " world" in one insert session, then leave insert mode.
	m = feed(m, key("A"))
	m = feed(m, key(" "), key("w"), key("o"), key("r"), key("l"), key("d"))
	m = feed(m, tea.KeyMsg{Type: tea.KeyEscape})

	if got, want := m.Content(), "hello world"; got != want {
		t.Fatalf("after edit content = %q, want %q", got, want)
	}

	// One undo reverts the whole insert session.
	m = feed(m, key("u"))
	if got, want := m.Content(), "hello"; got != want {
		t.Fatalf("after undo content = %q, want %q (session must coalesce)", got, want)
	}

	// Redo re-applies it.
	m = feed(m, tea.KeyMsg{Type: tea.KeyCtrlR})
	if got, want := m.Content(), "hello world"; got != want {
		t.Fatalf("after redo content = %q, want %q", got, want)
	}
}

func TestUndoRestoresCursorAndCleanState(t *testing.T) {
	m := New()
	m.SetSize(80, 10)
	m.SetContent("abc")
	if m.Dirty {
		t.Fatal("buffer should start clean")
	}

	// Delete the first character with x, then undo.
	m = feed(m, key("x"))
	if got, want := m.Content(), "bc"; got != want {
		t.Fatalf("after x content = %q, want %q", got, want)
	}
	if !m.Dirty {
		t.Fatal("buffer should be dirty after x")
	}

	m = feed(m, key("u"))
	if got, want := m.Content(), "abc"; got != want {
		t.Fatalf("after undo content = %q, want %q", got, want)
	}
	if m.Dirty {
		t.Fatal("undo back to original content should be clean again")
	}
	if m.cursorRow != 0 || m.cursorCol != 0 {
		t.Fatalf("cursor = (%d,%d), want (0,0)", m.cursorRow, m.cursorCol)
	}
}

func TestEmptyInsertSessionAddsNoUndoPoint(t *testing.T) {
	m := New()
	m.SetSize(80, 10)
	m.SetContent("abc")

	// Make a real edit (x), then enter+leave insert without typing.
	m = feed(m, key("x")) // "bc"
	m = feed(m, key("i"), tea.KeyMsg{Type: tea.KeyEscape})

	// A single undo must revert the x, not be consumed by the empty session.
	m = feed(m, key("u"))
	if got, want := m.Content(), "abc"; got != want {
		t.Fatalf("after undo content = %q, want %q (empty insert must not add undo point)", got, want)
	}
}

func TestRedoStackClearedByNewEdit(t *testing.T) {
	m := New()
	m.SetSize(80, 10)
	m.SetContent("abc")

	m = feed(m, key("x"))                       // "bc"
	m = feed(m, key("u"))                       // back to "abc", redo now holds "bc"
	m = feed(m, key("x"))                       // "bc" via a fresh edit -> redo must be cleared
	m = feed(m, tea.KeyMsg{Type: tea.KeyCtrlR}) // no-op: nothing to redo

	if got, want := m.Content(), "bc"; got != want {
		t.Fatalf("content = %q, want %q (redo should have been cleared by new edit)", got, want)
	}
}

func TestUndoOnEmptyHistoryIsNoop(t *testing.T) {
	m := New()
	m.SetSize(80, 10)
	m.SetContent("abc")

	m = feed(m, key("u"))
	m = feed(m, tea.KeyMsg{Type: tea.KeyCtrlR})
	if got, want := m.Content(), "abc"; got != want {
		t.Fatalf("content = %q, want %q (undo/redo with no history must be inert)", got, want)
	}
}

func TestUndoCoversReplaceRange(t *testing.T) {
	m := New()
	m.SetSize(80, 10)
	m.SetContent("The quick brown fox")

	m.ReplaceRange(Position{Row: 0, Col: 4}, Position{Row: 0, Col: 8}, "slow")
	if got, want := m.Content(), "The slow brown fox"; got != want {
		t.Fatalf("after ReplaceRange content = %q, want %q", got, want)
	}

	m = feed(m, key("u"))
	if got, want := m.Content(), "The quick brown fox"; got != want {
		t.Fatalf("after undo content = %q, want %q (LLM apply must be undoable)", got, want)
	}
}

func TestSetContentClearsHistory(t *testing.T) {
	m := New()
	m.SetSize(80, 10)
	m.SetContent("chapter one")
	m = feed(m, key("x")) // dirty edit in chapter one

	// Switching to another file must not let undo reach back into the old one.
	m.SetContent("chapter two")
	m = feed(m, key("u"))
	if got, want := m.Content(), "chapter two"; got != want {
		t.Fatalf("undo after SetContent content = %q, want %q (history must reset)", got, want)
	}
}

func TestDirtyIsSymmetricWithinInsertSessionAndRecovery(t *testing.T) {
	m := New()
	m.SetContent("abc")
	m = feed(m, key("A"), key("x"), tea.KeyMsg{Type: tea.KeyBackspace})
	if m.Dirty {
		t.Fatal("insert then backspace to saved bytes must be clean")
	}
	m = feed(m, tea.KeyMsg{Type: tea.KeyEscape}, key("u"))
	if m.Dirty {
		t.Fatal("undo at saved content must remain clean")
	}
	m.SetRecoveredContent("changed", "disk")
	if !m.Dirty {
		t.Fatal("recovered content differing from disk must be dirty")
	}
	m.ReplaceRange(Position{Row: 0, Col: 0}, Position{Row: 0, Col: 6}, "disk")
	if m.Dirty {
		t.Fatal("replacing recovered content with disk baseline must be clean")
	}
}
