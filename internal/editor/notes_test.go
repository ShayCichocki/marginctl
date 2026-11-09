package editor

import "testing"

// 4.4 — a note anchors to its selected text and follows it through edits
// (lines inserted above), and is orphaned when the text is deleted.
func TestNoteFollowsTextThroughEdits(t *testing.T) {
	m := New()
	m.SetSize(80, 10)
	m.SetContent("alpha\nbeta gamma\ndelta")

	// Select "beta gamma" on row 1.
	m.Mode = ModeVisual
	m.selectionAnchor = Position{Row: 1, Col: 0}
	m.cursorRow, m.cursorCol = 1, 9 // inclusive end of "beta gamma"
	if !m.AddNote("check this") {
		t.Fatal("AddNote returned false with an active selection")
	}
	m.Mode = ModeNormal

	notes := m.Notes()
	if len(notes) != 1 || notes[0].Row != 1 || notes[0].Body != "check this" {
		t.Fatalf("unexpected note: %+v", notes)
	}
	if !m.noteRows()[1] {
		t.Fatal("expected a note marker on row 1")
	}

	// Insert two lines above → the anchor text is now on row 3.
	m.SetContent("new0\nnew1\nalpha\nbeta gamma\ndelta")
	if notes = m.Notes(); notes[0].Row != 3 || notes[0].Orphaned {
		t.Fatalf("note did not follow its text: %+v", notes[0])
	}

	// Delete the anchored text → orphaned.
	m.SetContent("alpha\ndelta")
	if notes = m.Notes(); !notes[0].Orphaned {
		t.Fatalf("note should be orphaned once its text is gone: %+v", notes[0])
	}
}

// AddNote with no selection is a no-op.
func TestAddNoteRequiresSelection(t *testing.T) {
	m := New()
	m.SetContent("hello")
	if m.AddNote("x") {
		t.Fatal("AddNote should fail without a visual selection")
	}
}
