package editor

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func TestVisualMotionLargeDocumentsRemainsInteractive(t *testing.T) {
	if testing.Short() {
		t.Skip("large-document regression test")
	}
	tests := []struct {
		name    string
		content string
		key     tea.KeyMsg
	}{
		{"wrapped down", strings.Repeat("word ", 100000), tea.KeyMsg{Type: tea.KeyDown}},
		{"wrapped horizontal", strings.Repeat("word ", 100000), tea.KeyMsg{Type: tea.KeyRight}},
		{"multiline down", strings.Repeat("alpha beta gamma delta epsilon\n", 20000), tea.KeyMsg{Type: tea.KeyDown}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := New()
			m.SetContent(tt.content)
			m.SetSize(80, 30)
			m.Mode = ModeVisual
			m.selectionAnchor = Position{}
			_ = m.View()
			started := time.Now()
			for i := 0; i < 300; i++ {
				m, _ = m.Update(tt.key)
				_ = m.View()
			}
			// The race detector slows this several-fold on shared CI runners, so
			// the wall-clock budget applies only to normal builds.
			if elapsed := time.Since(started); !raceEnabled && elapsed > 6*time.Second {
				t.Fatalf("300 Visual Update+View cycles took %v", elapsed)
			}
		})
	}
}

func TestVisualRenderUsesAuthoritativeWrappedRanges(t *testing.T) {
	m := New()
	m.SetContent("ab cd ef")
	m.SetSize(7, 4) // four content columns after the gutter
	display := m.buildDisplayLines()
	if len(display) != 3 {
		t.Fatalf("display lines = %d, want 3", len(display))
	}
	want := [][2]int{{0, 3}, {3, 6}, {6, 8}}
	for i, dl := range display {
		if got := [2]int{dl.startCol, dl.endCol}; got != want[i] {
			t.Fatalf("display[%d] range = %v, want %v", i, got, want[i])
		}
		if len(dl.clusters) != dl.endCol-dl.startCol-dl.consumedWhitespace {
			t.Fatalf("display[%d] visible cluster span is inconsistent", i)
		}
	}
	m.SetSelectionForTest(0, 1, 0, 6)
	if got, _, _ := m.GetSelection(); got != "b cd e" {
		t.Fatalf("selection = %q, want %q", got, "b cd e")
	}
	if plain := stripANSI(m.View()); !strings.Contains(plain, "ab") || !strings.Contains(plain, "cd") || !strings.Contains(plain, "ef") {
		t.Fatalf("wrapped render lost source text: %q", plain)
	}
}

func TestVisualRenderStylePrecedenceAndRuns(t *testing.T) {
	m := New()
	m.SetContent("# a *bc* d")
	m.SetSize(80, 3)
	m.lastSearch = "bc"
	m.SetSelectionForTest(0, 2, 0, 6)
	dl := m.buildDisplayLines()[0]
	state := m.renderState(0)
	got := m.renderSelectionLine(dl, true, Position{0, 2}, Position{0, 6})

	base := state.base
	want := base.Render("# ") +
		base.Background(m.styles.EditorSelection.GetBackground()).Render("a *") +
		base.Italic(true).Background(m.styles.EditorSelection.GetBackground()).Render("b") +
		base.Italic(true).Background(m.styles.EditorCursor.GetBackground()).Render("c") +
		base.Render("* d")
	if got != want {
		t.Fatalf("rendered style runs differ\n got: %q\nwant: %q", got, want)
	}
}

func TestVisualMovementAndCursorPreserveGraphemeClusters(t *testing.T) {
	m := New()
	m.SetContent("A界e\u0301👩‍💻Z")
	m.SetSize(7, 5)
	m.Mode = ModeVisual
	m.selectionAnchor = Position{}
	for i := 0; i < 4; i++ {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRight})
	}
	if m.cursorCol != 4 {
		t.Fatalf("cursor column = %d, want 4 grapheme clusters", m.cursorCol)
	}
	if got, _, _ := m.GetSelection(); got != "A界e\u0301👩‍💻Z" {
		t.Fatalf("selected graphemes = %q", got)
	}
	view := stripANSI(m.View())
	for _, want := range []string{"A", "界", "e\u0301", "👩‍💻", "Z"} {
		if !strings.Contains(view, want) {
			t.Fatalf("render lost grapheme %q: %q", want, view)
		}
	}
}

func TestEditorDoesNotInterpretImageOrFilePlaceholders(t *testing.T) {
	m := New()
	source := "![cover](/tmp/cover.png) file:///etc/passwd\n\x1b_Gf=100,t=f;AAAA\x1b\\"
	m.SetContent(source)
	m.SetSize(100, 4)
	view := m.View()
	if strings.Contains(view, "\x1b_G") {
		t.Fatalf("source emitted a terminal graphics escape: %q", view)
	}
	plain := stripANSI(view)
	if !strings.Contains(plain, "![cover](/tmp/cover.png)") || !strings.Contains(plain, "file:///etc/passwd") {
		t.Fatalf("text placeholders were not rendered literally: %q", plain)
	}
}
