package statenav

import (
	tea "github.com/charmbracelet/bubbletea"
	"strings"
	"testing"

	"github.com/ShayCichocki/marginctl/internal/state"
)

func TestViewSanitizesStateNamesAndCategories(t *testing.T) {
	m := New()
	m.SetStateFilesWithCategories([]state.StateFile{{
		Name:     "Alice\x1b[2J\x07",
		Path:     "/state/alice.md",
		Type:     state.StateCustom,
		Category: "cast\x1b]0;owned\x07",
	}}, []string{"cast\x1b]0;owned\x07"})
	m.SetSize(40, 10)
	if view := m.View(); strings.ContainsAny(view, "\x1b\x07") {
		t.Fatalf("view retained terminal controls: %q", view)
	}
}

func TestMotionsAreSelectionOnly(t *testing.T) {
	m := New()
	m.SetStateFiles([]state.StateFile{{Name: "A", Path: "a", Type: state.StateCharacter, Category: "characters"}, {Name: "B", Path: "b", Type: state.StateCharacter, Category: "characters"}})
	m.Focus()
	for _, r := range []rune{'j', 'k', 'g', 'G'} {
		var cmd tea.Cmd
		m, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		if cmd != nil {
			t.Fatalf("motion %q emitted message", r)
		}
	}
}

func TestViewShowsEntryDisplayNames(t *testing.T) {
	m := New()
	m.SetStateFilesWithCategories([]state.StateFile{{
		Name: "Martin Luther King Jr.",
		Stem: "martin-luther-king-jr",
		Path: "/state/characters/martin-luther-king-jr.md",
		Type: state.StateCharacter, Category: "characters",
	}}, []string{"characters"})
	m.SetSize(60, 10)
	if view := m.View(); !strings.Contains(view, "Martin Luther King Jr.") {
		t.Fatalf("display name missing: %q", view)
	}
}
