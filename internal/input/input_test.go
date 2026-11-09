package input

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func keyRunes(s string) tea.KeyMsg     { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }
func keyType(t tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: t} }

func TestRuneSafeCursorEditing(t *testing.T) {
	m := New()
	m.SetWidth(20)
	m.Update(keyRunes("aé界"))
	m.Update(keyType(tea.KeyLeft))
	m.Update(keyType(tea.KeyBackspace))
	m.Update(keyRunes("🙂"))
	if got := m.Value(); got != "a🙂界" {
		t.Fatalf("value=%q", got)
	}
	if m.Cursor() != 2 {
		t.Fatalf("cursor=%d", m.Cursor())
	}
}

func TestPathAndCandidateCompletion(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "chapter.md"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	m := New()
	m.SetValue(filepath.Join(dir, "chap"))
	if !m.CompletePath() || !strings.HasSuffix(m.Value(), "chapter.md") {
		t.Fatalf("path=%q", m.Value())
	}
	m.SetValue("Sh")
	if !m.Complete([]string{"ShowChapters", "ShowState"}, false) || m.Value() != "ShowChapters" {
		t.Fatal(m.Value())
	}
	m.Complete([]string{"ShowChapters", "ShowState"}, false)
	if m.Value() != "ShowState" {
		t.Fatal(m.Value())
	}
	m.Update(keyRunes("x"))
	if m.Complete([]string{"ShowState"}, false) && m.Value() != "ShowState" {
		t.Fatal("completion did not reset")
	}
}
func TestHistoryMaskAndControls(t *testing.T) {
	m := New()
	m.SetWidth(8)
	m.SetHistory([]string{"first", "second"})
	m.Update(keyType(tea.KeyUp))
	if m.Value() != "second" {
		t.Fatal(m.Value())
	}
	m.SetValue("secret")
	m.SetMasked(true)
	v := m.View()
	if strings.Contains(v, "secret") || !strings.Contains(v, "•") {
		t.Fatalf("masked=%q", v)
	}
	m.SetValue("")
	m.Update(keyRunes("ok\n\x1bno"))
	if m.Value() != "okno" {
		t.Fatalf("controls=%q", m.Value())
	}
}
func TestLongInputScrollAndWordDelete(t *testing.T) {
	m := New()
	m.SetWidth(6)
	m.Update(keyRunes("one two three"))
	if strings.Contains(m.View(), "one") {
		t.Fatalf("view did not scroll: %q", m.View())
	}
	m.Update(tea.KeyMsg{Type: tea.KeyCtrlW})
	if m.Value() != "one two " {
		t.Fatalf("delete=%q", m.Value())
	}
}
