package results

import (
	"strings"
	"testing"
)

func streamLinesAt(width int, content string) []string {
	var m Model
	m.SetSize(width, 10)
	m.StreamText(content)
	return append([]string(nil), m.lines...)
}

func TestStreamRewrapKeepsTailOffsets(t *testing.T) {
	para := strings.Repeat("alpha beta gamma delta ", 12)
	first := "head line\n" + para + "\n" + para
	delta := " more words here that wrap\nnext line\n" + para
	for _, tc := range []struct{ from, to int }{{40, 100}, {100, 40}} {
		var m Model
		m.SetSize(tc.from, 10)
		m.StreamText(first)
		m.SetSize(tc.to, 10)
		m.AppendStreamText(delta)
		want := streamLinesAt(tc.to, first+delta)
		if strings.Join(m.lines, "\n") != strings.Join(want, "\n") {
			t.Fatalf("%d->%d: lines diverge\n got %q\nwant %q", tc.from, tc.to, m.lines, want)
		}
	}
}
