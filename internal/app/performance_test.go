package app

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

func commandPromptPerformanceModel() Model {
	m := New()
	m.editor.SetContent("# Chapter\n\n" + strings.Repeat("word *emphasis* ", 8000))
	m.width, m.height = 120, 40
	m.updateLayout()
	m.setPrompt(promptCommand)
	_ = m.View()
	return m
}

func TestCommandPromptUpdateViewLatency(t *testing.T) {
	m := commandPromptPerformanceModel()
	started := time.Now()
	for i := 0; i < 100; i++ {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
		m = *updated.(*Model)
		_ = m.View()
	}
	// The race detector slows this several-fold on shared CI runners, so the
	// wall-clock budget applies only to normal builds.
	if elapsed := time.Since(started); !raceEnabled && elapsed > time.Second {
		t.Fatalf("100 prompt Update+View cycles took %v", elapsed)
	}
	allocs := testing.AllocsPerRun(20, func() {
		copy := m
		updated, _ := copy.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
		_ = updated.(*Model).View()
	})
	if allocs > 300 {
		t.Fatalf("prompt Update+View allocated %.0f objects, want <= 300", allocs)
	}
}

func BenchmarkCommandPromptUpdateView100K(b *testing.B) {
	m := commandPromptPerformanceModel()
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
		m = *updated.(*Model)
		_ = m.View()
	}
}
