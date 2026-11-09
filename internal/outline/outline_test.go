package outline

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func TestViewSanitizesHeading(t *testing.T) {
	m := New()
	m.ParseContent("# Heading\x1b[2J\x07")
	m.SetSize(40, 3)
	if view := m.View(); strings.ContainsAny(view, "\x1b\x07") {
		t.Fatalf("view retained terminal controls: %q", view)
	}
}

func key(s string) tea.KeyMsg {
	if s == "enter" {
		return tea.KeyMsg{Type: tea.KeyEnter}
	}
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

func headingText(hs []Heading) []string {
	out := make([]string, len(hs))
	for i, h := range hs {
		out[i] = h.Text
	}
	return out
}

func eqStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestParseATXRequiresSpace(t *testing.T) {
	var m Model
	m.ParseContent("# Real Heading\n#hashtag not a heading\n## Also Real")
	if got, want := headingText(m.headings), []string{"Real Heading", "Also Real"}; !eqStrings(got, want) {
		t.Fatalf("headings = %v, want %v", got, want)
	}
}

func TestParseATXStripsTrailingHashes(t *testing.T) {
	var m Model
	m.ParseContent("## Chapter Two ##\n### Bare ###")
	if got, want := headingText(m.headings), []string{"Chapter Two", "Bare"}; !eqStrings(got, want) {
		t.Fatalf("headings = %v, want %v", got, want)
	}
}

func TestParseSkipsFencedCode(t *testing.T) {
	content := "# Chapter\n\n```\n# not a heading\n## also not\n```\n\n## After Fence"
	var m Model
	m.ParseContent(content)
	if got, want := headingText(m.headings), []string{"Chapter", "After Fence"}; !eqStrings(got, want) {
		t.Fatalf("headings = %v, want %v", got, want)
	}
}

func TestParseSkipsTildeFence(t *testing.T) {
	content := "~~~\n# hidden\n~~~\n# Visible"
	var m Model
	m.ParseContent(content)
	if got, want := headingText(m.headings), []string{"Visible"}; !eqStrings(got, want) {
		t.Fatalf("headings = %v, want %v", got, want)
	}
}

func TestParseSkipsYAMLFrontmatter(t *testing.T) {
	content := "---\ntitle: My Novel\n# not a heading here\n---\n# First Chapter"
	var m Model
	m.ParseContent(content)
	if got, want := headingText(m.headings), []string{"First Chapter"}; !eqStrings(got, want) {
		t.Fatalf("headings = %v, want %v", got, want)
	}
	if m.headings[0].Line != 4 {
		t.Fatalf("heading line = %d, want 4", m.headings[0].Line)
	}
}

func TestParseSetextHeadings(t *testing.T) {
	content := "Title One\n=========\n\nSection Two\n-----------\n\nbody text"
	var m Model
	m.ParseContent(content)
	if len(m.headings) != 2 {
		t.Fatalf("got %d headings, want 2: %v", len(m.headings), headingText(m.headings))
	}
	if m.headings[0].Level != 1 || m.headings[0].Text != "Title One" {
		t.Fatalf("h0 = %+v, want level 1 'Title One'", m.headings[0])
	}
	if m.headings[1].Level != 2 || m.headings[1].Text != "Section Two" {
		t.Fatalf("h1 = %+v, want level 2 'Section Two'", m.headings[1])
	}
}

// A lone --- after a blank line is a thematic break, not a heading.
func TestParseThematicBreakIsNotHeading(t *testing.T) {
	var m Model
	m.ParseContent("# Real\n\n---\n\nmore prose")
	if got, want := headingText(m.headings), []string{"Real"}; !eqStrings(got, want) {
		t.Fatalf("headings = %v, want %v", got, want)
	}
}

// TestEmptyOutlineNavigationNoPanic covers G-then-enter on an outline with no
// headings, which used to leave cursor at -1 and then index into m.headings.
func TestEmptyOutlineNavigationNoPanic(t *testing.T) {
	for _, k := range []string{"G", "g", "j", "k", "enter"} {
		t.Run(k, func(t *testing.T) {
			m := New()
			m.Focus() // no headings parsed

			m, _ = m.Update(key("G"))
			m, _ = m.Update(key(k))
			if m.cursor < 0 {
				t.Fatalf("cursor = %d, want >= 0", m.cursor)
			}
		})
	}
}
