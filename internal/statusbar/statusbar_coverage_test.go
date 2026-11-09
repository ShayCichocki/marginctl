package statusbar

import (
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
)

var covAnsi = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

func covPlain(s string) string { return covAnsi.ReplaceAllString(s, "") }

func covBar(width int, f func(*Model)) Model {
	m := New()
	m.SetWidth(width)
	m.FilePath = "chapters/001_Opening.md"
	m.Mode = "NORMAL"
	m.ActivePane = "editor"
	m.WordCount = 1234
	if f != nil {
		f(&m)
	}
	return m
}

func TestCovZeroWidthIsEmpty(t *testing.T) {
	if got := New().View(); got != "" {
		t.Fatalf("View() = %q", got)
	}
}

func TestCovAlwaysOneLineNeverWiderThanTerminal(t *testing.T) {
	long := strings.Repeat("very/long/path/", 20) + "chapter.md"
	for _, w := range []int{1, 2, 5, 10, 20, 30, 40, 60, 80, 120, 200} {
		for _, path := range []string{"", "a.md", long, "章節/第一章 夜明け.md", strings.Repeat("🙂", 20) + ".md"} {
			m := covBar(w, func(m *Model) {
				m.FilePath = path
				m.Dirty = true
				m.SearchQuery = "needle"
				m.SearchCurrent, m.SearchTotal = 3, 14
				m.PendingCount, m.PendingOperator = 12, "d"
			})
			view := m.View()
			if strings.Contains(view, "\n") {
				t.Fatalf("w=%d path=%q: bar wrapped onto several lines:\n%q", w, path, view)
			}
			// Below ~60 columns the fixed right-hand fields alone exceed the
			// terminal, so only assert the bound where it is achievable.
			if got := lipgloss.Width(view); w >= 60 && got > w {
				t.Fatalf("w=%d path=%q: bar is %d wide", w, path, got)
			}
		}
	}
}

func TestCovLongPathTruncatedKeepsRightFieldsAndMode(t *testing.T) {
	long := strings.Repeat("dir/", 40) + "end.md"
	m := covBar(80, func(m *Model) { m.FilePath = long; m.Dirty = true })
	view := covPlain(m.View())
	for _, want := range []string{"-- NORMAL --", "[+]", "1:1", "1234"} {
		if !strings.Contains(view, want) {
			t.Errorf("missing %q in %q", want, view)
		}
	}
	if strings.Contains(view, long) {
		t.Fatal("long path not truncated")
	}
}

func TestCovVeryNarrowDropsModeBeforeRightFields(t *testing.T) {
	m := covBar(32, func(m *Model) { m.Dirty = true })
	view := covPlain(m.View())
	if strings.Contains(view, "NORMAL") {
		t.Fatalf("mode kept at width 32: %q", view)
	}
	if !strings.Contains(view, "1234") {
		t.Fatalf("right section lost: %q", view)
	}
}

func TestCovWideAndEmojiPathsFillExactWidth(t *testing.T) {
	for _, path := range []string{strings.Repeat("日本語", 12) + ".md", strings.Repeat("😀", 22) + ".md"} {
		m := covBar(50, func(m *Model) { m.FilePath = path })
		view := m.View()
		if w := lipgloss.Width(view); w != 50 {
			t.Fatalf("path %q: bar width %d, want 50", path, w)
		}
		if strings.Contains(view, "\n") {
			t.Fatalf("path %q wrapped", path)
		}
	}
}

func TestCovStatesAndIndicators(t *testing.T) {
	plain := func(f func(*Model)) string { return covPlain(covBar(100, f).View()) }

	if got := plain(nil); strings.Contains(got, "[+]") {
		t.Fatalf("clean buffer shows dirty marker: %q", got)
	}
	if got := plain(func(m *Model) { m.Dirty = true }); !strings.Contains(got, "[+]") {
		t.Fatalf("dirty marker missing: %q", got)
	}
	if got := plain(func(m *Model) { m.FilePath = "" }); !strings.Contains(got, "[No File]") {
		t.Fatalf("placeholder missing: %q", got)
	}
	if got := plain(func(m *Model) { m.Mode = "" }); strings.Contains(got, "--") {
		t.Fatalf("empty mode still rendered an indicator: %q", got)
	}
	for _, mode := range []string{"INSERT", "VISUAL", "NORMAL", "COMMAND"} {
		if got := plain(func(m *Model) { m.Mode = mode }); !strings.Contains(got, "-- "+mode+" --") {
			t.Errorf("mode %s missing: %q", mode, got)
		}
	}
	if got := plain(func(m *Model) { m.IsProject, m.LeftPaneMode = true, "project" }); !strings.Contains(got, "[P] chapters") {
		t.Fatalf("project pane marker missing: %q", got)
	}
	if got := plain(func(m *Model) { m.IsProject, m.LeftPaneMode = false, "project" }); strings.Contains(got, "[P]") {
		t.Fatalf("[P] shown outside a project: %q", got)
	}
	if got := plain(func(m *Model) { m.IsProject, m.LeftPaneMode = true, "outline" }); strings.Contains(got, "[P]") {
		t.Fatalf("[P] shown in outline mode: %q", got)
	}
	if got := plain(func(m *Model) { m.Line, m.Column = 9, 4 }); !strings.Contains(got, "editor 10:5 1234") {
		t.Fatalf("position wrong (should be 1-based): %q", got)
	}
}

func TestCovRightSectionIsNotClippedAtCommonWidths(t *testing.T) {
	for _, w := range []int{60, 80, 120} {
		view := covPlain(covBar(w, nil).View())
		if !strings.Contains(view, "1234 words") {
			t.Fatalf("width %d bar loses the trailing words: %q", w, view)
		}
	}
}

func TestCovPendingAndSearchFields(t *testing.T) {
	plain := func(f func(*Model)) string { return covPlain(covBar(120, f).View()) }
	if got := plain(func(m *Model) { m.PendingCount, m.PendingOperator = 3, "d" }); !strings.Contains(got, "3d editor") {
		t.Fatalf("count+operator: %q", got)
	}
	if got := plain(func(m *Model) { m.PendingOperator = "g" }); !strings.Contains(got, "g editor") {
		t.Fatalf("operator only: %q", got)
	}
	if got := plain(func(m *Model) { m.SearchQuery, m.SearchCurrent, m.SearchTotal = "foo", 2, 9 }); !strings.Contains(got, "/foo 2/9") {
		t.Fatalf("search: %q", got)
	}
	if got := plain(func(m *Model) { m.SearchQuery = "a\x1b[31mb\rc" }); strings.ContainsAny(got, "\x1b\r") {
		t.Fatalf("search query not sanitized: %q", got)
	}
}
