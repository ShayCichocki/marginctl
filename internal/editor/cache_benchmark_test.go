package editor

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ShayCichocki/marginctl/internal/theme"
)

func TestDifferentlyThemedEditorsCoexist(t *testing.T) {
	dark := theme.Default()
	light := theme.Light()
	a := NewWithStyles(theme.NewStyles(dark))
	b := NewWithStyles(theme.NewStyles(light))
	if a.styles.Primary == b.styles.Primary || a.styles.BgDark == b.styles.BgDark {
		t.Fatal("independently themed models share concrete colors")
	}
}

func downWrapped100KModel() Model {
	m := New()
	m.SetContent(strings.Repeat("word ", 100000))
	m.SetSize(80, 30)
	_ = m.View()
	return m
}

func TestSustainedDownWrapped100KLatency(t *testing.T) {
	m := downWrapped100KModel()
	key := tea.KeyMsg{Type: tea.KeyDown}
	started := time.Now()
	for i := 0; i < 1000; i++ {
		m, _ = m.Update(key)
		_ = m.View()
	}
	// The regressed path takes over a minute even on a fast machine because each
	// key re-segments the entire paragraph; the fixed path takes about 2s locally
	// and about 16s on a shared CI runner. The budget sits between the two. The
	// race detector slows the loop several-fold more, so the wall-clock budget
	// applies only to normal builds; the allocation bound below guards the
	// regression precisely in every build.
	if elapsed := time.Since(started); !raceEnabled && elapsed > 40*time.Second {
		t.Fatalf("1000 Down+View cycles on 100k wrapped words took %v", elapsed)
	}
	allocs := testing.AllocsPerRun(20, func() {
		copy := m
		copy, _ = copy.Update(key)
		_ = copy.View()
	})
	if allocs > 2500 {
		t.Fatalf("Down+View allocated %.0f objects, want <= 2500", allocs)
	}
}

func BenchmarkSustainedDownWrapped100KWords(b *testing.B) {
	m := downWrapped100KModel()
	key := tea.KeyMsg{Type: tea.KeyDown}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m, _ = m.Update(key)
		_ = m.View()
	}
}

func visualWrapped100KModel() Model {
	m := downWrapped100KModel()
	m.Mode = ModeVisual
	m.selectionAnchor = Position{}
	return m
}

func BenchmarkVisualSustainedDownWrapped100KWords(b *testing.B) {
	m := visualWrapped100KModel()
	benchmarkVisualMotion(b, &m, tea.KeyMsg{Type: tea.KeyDown})
}

func BenchmarkVisualSustainedUpWrapped100KWords(b *testing.B) {
	m := visualWrapped100KModel()
	for i := 0; i < 2000; i++ {
		m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	benchmarkVisualMotion(b, &m, tea.KeyMsg{Type: tea.KeyUp})
}

func BenchmarkVisualSustainedHorizontalWrapped100KWords(b *testing.B) {
	m := visualWrapped100KModel()
	benchmarkVisualMotion(b, &m, tea.KeyMsg{Type: tea.KeyRight})
}

func BenchmarkVisualSustainedDownMultiline100KWords(b *testing.B) {
	m := New()
	m.SetContent(strings.Repeat("alpha beta gamma delta epsilon\n", 20000))
	m.SetSize(80, 30)
	m.Mode = ModeVisual
	m.selectionAnchor = Position{}
	_ = m.View()
	benchmarkVisualMotion(b, &m, tea.KeyMsg{Type: tea.KeyDown})
}

func benchmarkVisualMotion(b *testing.B, m *Model, key tea.KeyMsg) {
	b.Helper()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		display := m.buildDisplayLines()
		cursorDisplay := m.findCursorDisplayLine(display)
		if key.Type == tea.KeyDown && cursorDisplay == len(display)-1 {
			m.cursorRow, m.cursorCol, m.viewOffset = 0, 0, 0
			m.viewCacheValid = false
		} else if key.Type == tea.KeyUp && cursorDisplay == 0 {
			last := display[len(display)-1]
			m.cursorRow, m.cursorCol = last.logicalRow, last.startCol
			m.ensureCursorVisible()
			m.viewCacheValid = false
		}
		*m, _ = m.Update(key)
		_ = m.View()
	}
}

func TestRenderLongSingleParagraphIsLinear(t *testing.T) {
	m := New()
	m.SetContent("# " + strings.Repeat("word *emphasis* ", 8000)) // >100k graphemes
	m.SetSize(80, 30)
	started := time.Now()
	view := m.View()
	if view == "" {
		t.Fatal("empty view")
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("long paragraph render took %v", elapsed)
	}
	if allocs := testing.AllocsPerRun(10, func() { _ = m.View() }); allocs > 2 {
		t.Fatalf("cached view allocated %.0f objects, want <= 2", allocs)
	}
}

func BenchmarkVisibleRenderSingleParagraph120K(b *testing.B) {
	m := New()
	m.SetContent("# " + strings.Repeat("word *emphasis* ", 8000))
	m.SetSize(80, 30)
	_ = m.View()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.View()
	}
}

func BenchmarkCachedVisibleRender100KWords(b *testing.B) {
	m := New()
	m.SetContent(strings.Repeat("word word word word word\n", 20000))
	m.SetSize(80, 30)
	_ = m.View()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = m.View()
	}
}
