package editor

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// recomputeDirty must catch a row that differs from the saved snapshot even when
// the incremental per-row map never recorded it (the state left by a
// line-count-changing op), and a later clean single-row edit must not clear it.
func TestRecomputeDirtyCatchesUntrackedRow(t *testing.T) {
	m := New()
	m.SetContent("alpha\nbeta") // clean; savedLines = [alpha, beta]
	m.lines[0] = "ALPHA"        // a difference the stale map would miss
	m.dirtyRows = make(map[int]struct{})

	m.recomputeDirty()
	if !m.Dirty {
		t.Fatal("recomputeDirty must mark dirty when a row differs from saved")
	}
	m.updateDirtyRow(1) // line 1 still equals saved
	if !m.Dirty {
		t.Fatal("Dirty cleared though line 0 still differs from saved")
	}
}

// End to end: churning the line count (dd then O) and then making a net-zero edit
// on an unchanged row must leave the buffer dirty, because another row differs.
func TestDirtyStaysTrueAfterLineCountChurn(t *testing.T) {
	withStubClipboard(t)
	m := New()
	m.SetSize(80, 10)
	m.SetContent("alpha\nbeta")

	m = feedKeys(m, "dd")                                  // -> "beta"
	m = feed(m, key("O"), tea.KeyMsg{Type: tea.KeyEscape}) // -> "\nbeta"
	m = feed(m, key("j"), key("A"), key("Z"),              // append Z on line 1
		tea.KeyMsg{Type: tea.KeyBackspace}, // delete it again
		tea.KeyMsg{Type: tea.KeyEscape})    // line 1 back to "beta"

	if m.Content() == "alpha\nbeta" {
		t.Fatalf("precondition: content should differ from saved, got %q", m.Content())
	}
	if !m.Dirty {
		t.Fatalf("buffer differs from saved (%q) but Dirty is false", m.Content())
	}
}

// A combining mark merges into the preceding cluster, so the cursor must not
// advance past it (grapheme-index invariant).
func TestInsertCombiningMarkDoesNotOvershoot(t *testing.T) {
	s, col := insertRunes("e", 1, "́") // e + combining acute -> "é"
	if s != "é" {
		t.Fatalf("result = %q, want %q", s, "é")
	}
	if col != 1 {
		t.Fatalf("column after combining insert = %d, want 1 (single cluster)", col)
	}
	if _, col := insertRunes("ab", 1, "X"); col != 2 {
		t.Fatalf("ascii insert column = %d, want 2", col)
	}
}

// An undo that restores several rows at an unchanged line count must rebuild the
// dirty map against the saved snapshot, not leave stale dirtyRows behind (which
// would read as false-dirty on the next edit).
func TestUndoRebuildsDirtyAgainstSaved(t *testing.T) {
	m := New()
	m.SetContent("a\nb\nc")
	m.MarkClean()
	m.pushUndo()
	m.lines[0], m.lines[1] = "X", "Y"
	m.recomputeDirty() // dirtyRows = {0,1}
	m.undo()           // restore -> content back to saved
	if m.Content() != "a\nb\nc" {
		t.Fatalf("content after undo = %q", m.Content())
	}
	if m.Dirty || len(m.dirtyRows) != 0 {
		t.Fatalf("undo to saved content must be clean: Dirty=%v dirtyRows=%v", m.Dirty, m.dirtyRows)
	}
}

func TestSanitizeClipboard(t *testing.T) {
	if got := sanitizeClipboard("a\r\nb\rc\x00\td"); got != "a\nb\nc\td" {
		t.Fatalf("sanitizeClipboard = %q, want %q", got, "a\nb\nc\td")
	}
}
