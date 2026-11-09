// Package input provides the single-line editor used by every bottom-bar prompt.
package input

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ShayCichocki/marginctl/internal/fsutil"
	"github.com/ShayCichocki/marginctl/internal/theme"
)

type Model struct {
	runes       []rune
	cursor      int
	offset      int
	width       int
	masked      bool
	history     []string
	historyPos  int
	draft       string
	completions []string
	completePos int
}

func New() Model { return Model{historyPos: -1} }

func (m *Model) SetWidth(width int) {
	m.width = width
	if lipgloss.Width(string(m.runes)) < width {
		m.offset = 0
	}
	m.keepCursorVisible()
}
func (m *Model) SetMasked(masked bool) { m.masked = masked }
func (m Model) Value() string          { return string(m.runes) }
func (m Model) Cursor() int            { return m.cursor }
func (m *Model) SetValue(value string) {
	m.runes = clean([]rune(value))
	m.cursor = len(m.runes)
	m.offset = 0
	m.resetCompletion()
}
func (m *Model) Reset() { m.SetValue(""); m.historyPos = -1; m.draft = "" }

func (m *Model) SetHistory(history []string) {
	m.history = append([]string(nil), history...)
	m.historyPos = -1
}
func (m *Model) CommitHistory() {
	v := m.Value()
	if v != "" && (len(m.history) == 0 || m.history[len(m.history)-1] != v) {
		m.history = append(m.history, v)
	}
	m.historyPos = -1
}
func (m Model) History() []string { return append([]string(nil), m.history...) }

func clean(rs []rune) []rune {
	out := make([]rune, 0, len(rs))
	for _, r := range rs {
		if r != '\r' && r != '\n' && !unicode.IsControl(r) {
			out = append(out, r)
		}
	}
	return out
}

func (m *Model) Update(msg tea.KeyMsg) bool {
	changed := false
	switch msg.String() {
	case "left":
		if m.cursor > 0 {
			m.cursor--
		}
	case "right":
		if m.cursor < len(m.runes) {
			m.cursor++
		}
	case "home", "ctrl+a":
		m.cursor = 0
	case "end", "ctrl+e":
		m.cursor = len(m.runes)
	case "backspace":
		if m.cursor > 0 {
			m.runes = append(m.runes[:m.cursor-1], m.runes[m.cursor:]...)
			m.cursor--
			changed = true
		}
	case "delete", "ctrl+d":
		if m.cursor < len(m.runes) {
			m.runes = append(m.runes[:m.cursor], m.runes[m.cursor+1:]...)
			changed = true
		}
	case "ctrl+w", "alt+backspace":
		start := m.cursor
		for start > 0 && unicode.IsSpace(m.runes[start-1]) {
			start--
		}
		for start > 0 && !unicode.IsSpace(m.runes[start-1]) {
			start--
		}
		if start != m.cursor {
			m.runes = append(m.runes[:start], m.runes[m.cursor:]...)
			m.cursor = start
			changed = true
		}
	case "up":
		m.historyMove(-1)
	case "down":
		m.historyMove(1)
	default:
		if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
			rs := clean(msg.Runes)
			if msg.Type == tea.KeySpace {
				rs = []rune{' '}
			}
			if len(rs) > 0 {
				m.runes = append(m.runes, make([]rune, len(rs))...)
				copy(m.runes[m.cursor+len(rs):], m.runes[m.cursor:])
				copy(m.runes[m.cursor:], rs)
				m.cursor += len(rs)
				changed = true
			}
		}
	}
	if changed {
		m.historyPos = -1
		m.resetCompletion()
	}
	m.keepCursorVisible()
	return changed
}

func (m *Model) historyMove(delta int) {
	if len(m.history) == 0 {
		return
	}
	if m.historyPos < 0 {
		m.draft = m.Value()
		m.historyPos = len(m.history)
	}
	m.historyPos += delta
	if m.historyPos < 0 {
		m.historyPos = 0
	}
	if m.historyPos >= len(m.history) {
		m.historyPos = -1
		m.SetValue(m.draft)
		return
	}
	m.SetValue(m.history[m.historyPos])
}

func (m *Model) Complete(candidates []string, backward bool) bool {
	if len(candidates) == 0 {
		return false
	}
	if !sameStrings(m.completions, candidates) {
		m.completions = append([]string(nil), candidates...)
		m.completePos = 0
		if backward {
			m.completePos = len(candidates) - 1
		}
	} else if backward {
		m.completePos = (m.completePos - 1 + len(candidates)) % len(candidates)
	} else {
		m.completePos = (m.completePos + 1) % len(candidates)
	}
	m.SetValue(m.completions[m.completePos])
	// SetValue resets completion, so restore the active cycle.
	m.completions = append([]string(nil), candidates...)
	return true
}

func (m *Model) CompletePath() bool {
	v := m.Value()
	expanded, err := fsutil.ExpandHome(v)
	if err != nil {
		return false
	}
	// ExpandHome (via filepath.Join) drops a trailing separator; keep it so
	// "~/" lists the home directory instead of matching its basename in its parent.
	if v == "~" || (strings.HasSuffix(v, "/") || strings.HasSuffix(v, string(filepath.Separator))) &&
		!strings.HasSuffix(expanded, string(filepath.Separator)) {
		expanded += string(filepath.Separator)
	}
	dir, prefix := filepath.Split(expanded)
	if dir == "" {
		dir = "."
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	var matches []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), prefix) {
			candidate := filepath.Join(dir, e.Name())
			if dir == "." {
				candidate = e.Name()
			}
			if e.IsDir() {
				candidate += string(filepath.Separator)
			}
			matches = append(matches, candidate)
		}
	}
	sort.Strings(matches)
	return m.Complete(matches, false)
}

func (m Model) View() string {
	if m.width <= 0 {
		return ""
	}
	display := append([]rune(nil), m.runes...)
	if m.masked {
		for i := range display {
			display[i] = '•'
		}
	}
	start := m.offset
	if start > len(display) {
		start = len(display)
	}
	left := string(display[start:m.cursor])
	caret, right := " ", ""
	if m.cursor < len(display) {
		caret = string(display[m.cursor])
		right = string(display[m.cursor+1:])
	}
	line := left + lipgloss.NewStyle().Reverse(true).Render(caret) + right
	return theme.Truncate(line, m.width)
}

func (m *Model) keepCursorVisible() {
	if m.width < 2 {
		m.offset = m.cursor
		return
	}
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	for lipgloss.Width(string(m.runes[m.offset:m.cursor])) >= m.width-1 {
		m.offset++
	}
}
func (m *Model) resetCompletion() { m.completions = nil; m.completePos = 0 }
func sameStrings(a, b []string) bool {
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
