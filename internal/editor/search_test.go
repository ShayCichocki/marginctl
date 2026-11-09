package editor

import "testing"

func TestSearchCasePolicyAndCountAfterEdit(t *testing.T) {
	m := New()
	m.SetContent("Alpha alpha\nALPHA")
	if !m.StartSearch("ALpha", true) {
		t.Fatal("mixed-case query should remain case-insensitive")
	}
	q, current, total := m.SearchStatus()
	if q != "ALpha" || current != 2 || total != 3 {
		t.Fatalf("status = %q %d/%d, want ALpha 2/3", q, current, total)
	}
	m = feed(m, key("x"))
	_, _, total = m.SearchStatus()
	if total != 2 {
		t.Fatalf("count after deleting from a match = %d, want 2", total)
	}
}

func TestSearchForwardAndRepeat(t *testing.T) {
	m := New()
	m.SetSize(80, 20)
	m.SetContent("foo one\nbar foo\nfoo three")

	// Search forward for "foo" from (0,0): the next match is on line 1.
	if !m.StartSearch("foo", true) {
		t.Fatal("StartSearch should find a match")
	}
	if m.cursorRow != 1 || m.cursorCol != 4 {
		t.Fatalf("first match = (%d,%d), want (1,4)", m.cursorRow, m.cursorCol)
	}

	// n advances to the next match (line 2, col 0).
	m = feed(m, key("n"))
	if m.cursorRow != 2 || m.cursorCol != 0 {
		t.Fatalf("after n = (%d,%d), want (2,0)", m.cursorRow, m.cursorCol)
	}

	// n again wraps around to the first match (line 0, col 0).
	m = feed(m, key("n"))
	if m.cursorRow != 0 || m.cursorCol != 0 {
		t.Fatalf("after wrap n = (%d,%d), want (0,0)", m.cursorRow, m.cursorCol)
	}
}

func TestSearchPrevWithN(t *testing.T) {
	m := New()
	m.SetSize(80, 20)
	m.SetContent("foo\nbar\nfoo\nbaz")

	if !m.StartSearch("foo", true) {
		t.Fatal("expected a match")
	}
	// From (0,0) forward -> (2,0).
	if m.cursorRow != 2 {
		t.Fatalf("forward match row = %d, want 2", m.cursorRow)
	}
	// N reverses direction: back to (0,0).
	m = feed(m, key("N"))
	if m.cursorRow != 0 {
		t.Fatalf("after N row = %d, want 0", m.cursorRow)
	}
}

func TestSearchNoMatch(t *testing.T) {
	m := New()
	m.SetSize(80, 20)
	m.SetContent("alpha bravo")
	if m.StartSearch("zzz", true) {
		t.Fatal("StartSearch should return false when nothing matches")
	}
	// Cursor stays put.
	if m.cursorRow != 0 || m.cursorCol != 0 {
		t.Fatalf("cursor moved on failed search: (%d,%d)", m.cursorRow, m.cursorCol)
	}
}

func TestSearchMultibyte(t *testing.T) {
	m := New()
	m.SetSize(80, 20)
	m.SetContent("café society\nun café noir")

	// Match on "café" — the second occurrence is on line 1 at rune col 3.
	if !m.StartSearch("café", true) {
		t.Fatal("expected a multibyte match")
	}
	if m.cursorRow != 1 || m.cursorCol != 3 {
		t.Fatalf("multibyte match = (%d,%d), want (1,3)", m.cursorRow, m.cursorCol)
	}
}
