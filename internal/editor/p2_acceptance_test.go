package editor

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func press(m Model, keys ...string) Model {
	for _, key := range keys {
		var msg tea.KeyMsg
		if key == "esc" {
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		} else {
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
		}
		m, _ = m.Update(msg)
	}
	return m
}

func TestGraphemeClustersAreAtomic(t *testing.T) {
	clusters := []string{"e\u0301", "\u270c\ufe0f", "\U0001f44d\U0001f3fd", "\U0001f1fa\U0001f1f8", "\U0001f468\u200d\U0001f469\u200d\U0001f467\u200d\U0001f466"}
	for _, cluster := range clusters {
		t.Run(cluster, func(t *testing.T) {
			m := New()
			m.SetContent(cluster + "x")
			m = press(m, "x")
			if got := m.Content(); got != "x" {
				t.Fatalf("deleted part of cluster: %q", got)
			}
		})
	}
}

func TestExplicitCountGOperators(t *testing.T) {
	for _, keys := range [][]string{{"d", "1", "G"}, {"1", "d", "G"}, {"c", "1", "G", "esc"}, {"1", "c", "G", "esc"}, {"y", "1", "G"}, {"1", "y", "G"}} {
		m := New()
		m.SetContent("one\ntwo\nthree")
		m.GoToLine(1)
		m = press(m, keys...)
		if keys[0] == "y" || keys[1] == "y" {
			if m.register != "one\ntwo" {
				t.Fatalf("%v yanked %q", keys, m.register)
			}
		} else if strings.Contains(m.Content(), "one") {
			t.Fatalf("%v targeted EOF instead of line 1: %q", keys, m.Content())
		}
	}
}

func TestUndoHistoryHasByteBudget(t *testing.T) {
	m := New()
	large := strings.Repeat("x", maxHistoryBytes/2)
	for i := 0; i < 5; i++ {
		m.SetContent(large)
		m.pushUndo()
	}
	if m.undoBytes > maxHistoryBytes {
		t.Fatalf("undo history uses %d bytes, budget %d", m.undoBytes, maxHistoryBytes)
	}
}

func BenchmarkRapidInsertLargeChapter(b *testing.B) {
	m := New()
	m.SetContent(strings.Repeat("word ", 100000))
	m = press(m, "A")
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("x")}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		m, _ = m.Update(msg)
	}
}

func BenchmarkWrapLongLine(b *testing.B) {
	line := strings.Repeat("family \U0001f468\u200d\U0001f469\u200d\U0001f467\u200d\U0001f466 ", 10000)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = wrapSegments(line, 80)
	}
}
