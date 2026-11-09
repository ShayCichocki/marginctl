package results

import (
	"github.com/ShayCichocki/marginctl/internal/command"
	"strings"
	"testing"
)

func TestDiffModesAndWordSpans(t *testing.T) {
	r := command.Result{Type: command.ResultDiff, OriginalText: "The café was quiet.\nSame", ProposedText: "The bright café is quiet.\nSame"}
	m := New()
	m.SetSize(40, 10)
	m.Show(r)
	narrow := strings.Join(m.lines, "\n")
	if !strings.Contains(narrow, "@@") || !strings.Contains(narrow, "{+bright+}") || !strings.Contains(narrow, "  Same") {
		t.Fatalf("narrow=%q", narrow)
	}
	if strings.Contains(narrow, "? ") {
		t.Fatalf("narrow mode must mark words inline, not emit a separate ? line: %q", narrow)
	}
	m.SetSize(100, 10)
	wide := strings.Join(m.lines, "\n")
	if !strings.Contains(wide, "ORIGINAL | PROPOSED") || !strings.Contains(wide, "[-was-]") {
		t.Fatalf("wide=%q", wide)
	}
}

func TestMarkedWordsGroupRuns(t *testing.T) {
	old, fresh := markedWords("a b c d", "a x y d")
	if old != "a [-b c-] d" {
		t.Fatalf("old = %q, want single run marker", old)
	}
	if fresh != "a {+x y+} d" {
		t.Fatalf("fresh = %q, want single run marker", fresh)
	}
}
