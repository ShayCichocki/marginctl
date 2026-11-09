package editor

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// benchTypeAndView measures one keystroke as the app experiences it: an insert-
// mode Update followed by a View of the whole editor.
func benchTypeAndView(b *testing.B, content string) {
	m := New()
	m.SetContent(content)
	m.SetSize(100, 40)
	// Start in the middle of the document, in insert mode.
	m.cursorRow = len(m.lines) / 2
	m = press(m, "A")
	_ = m.View()
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m, _ = m.Update(msg)
		_ = m.View()
	}
}

func BenchmarkTypeViewManyShortLines20K(b *testing.B) {
	benchTypeAndView(b, strings.Repeat("Some ordinary sentence of prose here.\n", 20000))
}

func BenchmarkTypeViewParagraphs100x5KB(b *testing.B) {
	para := strings.Repeat("word ", 1000)
	benchTypeAndView(b, strings.Repeat(para+"\n", 100))
}

func BenchmarkTypeViewSingleParagraph500KB(b *testing.B) {
	benchTypeAndView(b, strings.Repeat("word ", 100000))
}

func BenchmarkEnterViewManyShortLines20K(b *testing.B) {
	m := New()
	m.SetContent(strings.Repeat("Some ordinary sentence of prose here.\n", 20000))
	m.SetSize(100, 40)
	m.cursorRow = 10000
	m = press(m, "A")
	_ = m.View()
	msg := tea.KeyMsg{Type: tea.KeyEnter}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m, _ = m.Update(msg)
		_ = m.View()
	}
}

// A 500KB paragraph containing a tab: Sanitize rewrites the row, so every edit
// also recomputes the raw grapheme count.
func BenchmarkTypeViewParagraphWithTab500KB(b *testing.B) {
	benchTypeAndView(b, "\t"+strings.Repeat("word ", 100000))
}
