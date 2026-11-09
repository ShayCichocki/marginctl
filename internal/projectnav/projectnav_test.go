package projectnav

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ShayCichocki/marginctl/internal/project"
)

func TestViewSanitizesChapterFilename(t *testing.T) {
	m := New()
	m.SetChapters([]project.FileEntry{{Name: "chapter\x1b]8;;https://evil\x07.md", Path: "/chapter"}})
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

// TestEmptyListNavigationNoPanic drives the motions that used to set cursor to
// -1 on an empty chapter list and then index into it.
func TestEmptyListNavigationNoPanic(t *testing.T) {
	for _, k := range []string{"G", "g", "j", "k", "enter"} {
		t.Run(k, func(t *testing.T) {
			m := New()
			m.Focus() // no chapters set

			// G followed by enter is the classic crash: G -> cursor=-1, enter indexes.
			m, cmd := m.Update(key("G"))
			m, _ = m.Update(key(k))
			if m.cursor < 0 {
				t.Fatalf("cursor = %d, want >= 0", m.cursor)
			}
			// Draining the preview cmd must not panic either.
			if cmd != nil {
				_ = cmd()
			}
		})
	}
}

func TestMotionsNeverEmitOpenAndEnterDoes(t *testing.T) {
	m := New()
	m.SetChapters([]project.FileEntry{{Path: "a"}, {Path: "b"}})
	m.Focus()
	for _, k := range []string{"j", "j", "k", "g", "G"} {
		var cmd tea.Cmd
		m, cmd = m.Update(key(k))
		if cmd != nil {
			t.Fatalf("motion %q emitted %T", k, cmd())
		}
	}
	_, cmd := m.Update(key("enter"))
	if cmd == nil {
		t.Fatal("Enter did not emit open")
	}
	if got := cmd().(ChapterSelectedMsg).Path; got != "b" {
		t.Fatalf("path=%q", got)
	}
}
