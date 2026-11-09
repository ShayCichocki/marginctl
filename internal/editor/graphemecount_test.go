package editor

import "testing"

func TestGraphemeCountMatchesGraphemesWithoutAllocating(t *testing.T) {
	for _, s := range []string{"", "plain ascii", "a\tb\x01c", "éx", "👨‍👩‍👧 family", "line\r\nbreak", "日本語\t語"} {
		if got, want := graphemeCount(s), len(graphemes(s)); got != want {
			t.Errorf("graphemeCount(%q) = %d, want %d", s, got, want)
		}
		if a := testing.AllocsPerRun(10, func() { _ = graphemeCount(s) }); a != 0 {
			t.Errorf("graphemeCount(%q) allocates %v", s, a)
		}
	}
}

func TestRowEntryRawLenCountsRawGraphemes(t *testing.T) {
	m := New()
	m.SetContent("a\tb́\x01c\n")
	if got, want := m.rowEntry(0).rawLen, len(graphemes(m.lines[0])); got != want {
		t.Fatalf("rawLen = %d, want %d", got, want)
	}
}
