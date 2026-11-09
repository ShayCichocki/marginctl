package statusbar

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/ShayCichocki/marginctl/internal/theme"
)

type Model struct {
	width                      int
	FilePath                   string
	Mode                       string
	WordCount                  int
	Dirty                      bool
	IsProject                  bool
	LeftPaneMode               string // "outline" or "project"
	Line, Column               int
	ActivePane                 string
	PendingCount               int
	PendingOperator            string
	SearchQuery                string
	SearchCurrent, SearchTotal int
}

func New() Model {
	return Model{}
}

func (m *Model) SetWidth(width int) {
	m.width = width
}

func (m Model) View() string {
	if m.width == 0 {
		return ""
	}

	// Left section: file path (with pane mode indicator for projects)
	filePath := theme.Sanitize(m.FilePath)
	if filePath == "" {
		filePath = "[No File]"
	}
	if m.IsProject && m.LeftPaneMode == "project" {
		filePath = "[P] " + filePath
	}

	// Center section: mode indicator
	centerText := ""
	if m.Mode != "" {
		centerText = fmt.Sprintf(" -- %s -- ", theme.Sanitize(m.Mode))
	}

	// Right section: word count + dirty indicator
	wordCountStr := fmt.Sprintf("%s %d:%d %d words", m.ActivePane, m.Line+1, m.Column+1, m.WordCount)
	if m.SearchQuery != "" {
		wordCountStr = fmt.Sprintf("/%s %d/%d ", theme.Sanitize(m.SearchQuery), m.SearchCurrent, m.SearchTotal) + wordCountStr
	}
	if m.PendingCount > 0 {
		wordCountStr = fmt.Sprintf("%d%s ", m.PendingCount, m.PendingOperator) + wordCountStr
	} else if m.PendingOperator != "" {
		wordCountStr = m.PendingOperator + " " + wordCountStr
	}
	dirty := ""
	if m.Dirty {
		dirty = "[+] "
	}

	// The file path is the only unbounded field; truncate it so the three
	// sections plus a minimum one-space gap on each side of center always fit on
	// one line. Without this the bar wraps to a second row and overflows the
	// layout (pushing the composed view past the terminal height).
	// The style's padding is part of Width(), so budget the content to the
	// inner width; the rendered mode badge also carries its own padding.
	inner := m.width - theme.StatusBar.GetHorizontalFrameSize()
	if inner < 0 {
		inner = 0
	}
	centerWidth := 0
	if centerText != "" {
		centerWidth = lipgloss.Width(centerText) + theme.StatusBarMode.GetHorizontalFrameSize()
	}
	rightWidth := lipgloss.Width(dirty + wordCountStr)
	availForLeft := inner - centerWidth - rightWidth - 2
	if availForLeft < 0 {
		// Extremely narrow: drop the center indicator to reclaim room.
		centerText, centerWidth = "", 0
		availForLeft = inner - rightWidth - 2
	}
	if availForLeft < 0 {
		availForLeft = 0
	}
	filePath = theme.Truncate(filePath, availForLeft)

	left := theme.StatusBarFile.Render(filePath)
	center := ""
	if centerText != "" {
		center = theme.StatusBarMode.Render(centerText)
	}
	right := wordCountStr
	if dirty != "" {
		right = theme.StatusBarDirty.Render(dirty) + right
	}

	// Calculate spacing
	leftWidth := lipgloss.Width(left)
	rightWidth = lipgloss.Width(right)

	// Distribute the leftover space into two gaps that always sum to the leftover
	// (so the assembled bar is exactly m.width wide and never wraps). The center
	// section is placed as close to the middle as the gaps allow.
	leftover := inner - leftWidth - centerWidth - rightWidth
	if leftover < 2 {
		leftover = 2 // truncation above keeps this from happening at real widths
	}
	leftPadding := (inner-centerWidth)/2 - leftWidth
	if leftPadding < 1 {
		leftPadding = 1
	}
	if leftPadding > leftover-1 {
		leftPadding = leftover - 1
	}
	rightPadding := leftover - leftPadding

	// Build the status bar
	bar := left +
		strings.Repeat(" ", leftPadding) +
		center +
		strings.Repeat(" ", rightPadding) +
		right

	// MaxHeight(1) is a hard backstop: even if some field is pathologically wide,
	// the bar can never spill onto a second row and overflow the layout.
	return theme.StatusBar.Width(m.width).MaxHeight(1).Render(bar)
}
