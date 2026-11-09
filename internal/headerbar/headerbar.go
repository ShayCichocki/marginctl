package headerbar

import (
	"github.com/charmbracelet/lipgloss"
	"strings"

	"github.com/ShayCichocki/marginctl/internal/theme"
)

type Model struct {
	width     int
	Title     string
	Project   string
	File      string
	Operation string
}

func New() Model {
	return Model{
		Title: "marginctl",
	}
}

func (m *Model) SetWidth(width int) {
	m.width = width
}

func (m Model) View() string {
	if m.width == 0 {
		return ""
	}

	style := lipgloss.NewStyle().
		Background(theme.BgPanel).
		Foreground(theme.Text).
		Bold(true).
		Width(m.width).
		Align(lipgloss.Center)

	parts := []string{m.Title}
	if m.Project != "" {
		parts = append(parts, m.Project)
	}
	if m.File != "" {
		parts = append(parts, m.File)
	}
	if m.Operation != "" {
		parts = append(parts, "["+m.Operation+"]")
	}
	return style.Render(theme.Truncate(theme.Sanitize(strings.Join(parts, " / ")), m.width))
}
