package theme

import (
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"
)

func TestWrapShortLineUnchanged(t *testing.T) {
	got := Wrap("hello", 80)
	if len(got) != 1 || got[0] != "hello" {
		t.Fatalf("Wrap = %q, want single unchanged line", got)
	}
}

func TestWrapNonPositiveWidthPassesThrough(t *testing.T) {
	got := Wrap("anything at all", 0)
	if len(got) != 1 || got[0] != "anything at all" {
		t.Fatalf("Wrap = %q, want single pass-through line", got)
	}
}

func TestWrapEmptyStringYieldsOneEmptyLine(t *testing.T) {
	got := Wrap("", 10)
	if len(got) != 1 || got[0] != "" {
		t.Fatalf("Wrap = %q, want one empty line", got)
	}
}

func TestWrapBreaksAtSpacesWithinWidth(t *testing.T) {
	got := Wrap("the quick brown fox jumps", 10)
	for _, line := range got {
		if w := runewidth.StringWidth(line); w > 10 {
			t.Fatalf("line %q width %d exceeds 10", line, w)
		}
	}
	if strings.Join(got, "") != "the quick brown fox jumps" {
		t.Fatalf("rejoined = %q, want original text preserved", got)
	}
}

func TestWrapHardBreaksOverlongUnbrokenToken(t *testing.T) {
	// A long JSON error body with no spaces must still be broken to fit.
	body := `{"detail":"Themodeldoesnotsupportthisrequestformat"}`
	got := Wrap(body, 12)
	if len(got) < 2 {
		t.Fatalf("Wrap = %q, want the long token hard-split into multiple lines", got)
	}
	for _, line := range got {
		if w := runewidth.StringWidth(line); w > 12 {
			t.Fatalf("line %q width %d exceeds 12", line, w)
		}
	}
	if strings.Join(got, "") != body {
		t.Fatalf("hard-split lost content: %q", got)
	}
}

func TestWrapWideRunesRespectCellWidth(t *testing.T) {
	// CJK runes occupy two cells; wrapping must count cells, not runes.
	got := Wrap("日本語のテキストです", 6)
	for _, line := range got {
		if w := runewidth.StringWidth(line); w > 6 {
			t.Fatalf("line %q width %d exceeds 6 cells", line, w)
		}
	}
}
