package outline

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ShayCichocki/marginctl/internal/theme"
)

type HeadingSelectedMsg struct {
	Line int // Line number in document
}

type Heading struct {
	Level int    // 1 for #, 2 for ##, etc.
	Text  string // Heading text without the #
	Line  int    // Line number in document (0-indexed)
}

type Model struct {
	headings []Heading
	cursor   int
	width    int
	height   int
	focused  bool
}

func New() Model {
	return Model{}
}

// ParseContent extracts headings from markdown content. It follows CommonMark
// closely enough for a novel manuscript: ATX headings require a space (or
// end-of-line) after the run of #, a trailing run of # is stripped, fenced code
// blocks are skipped so `# not a heading` inside a fence is ignored, a leading
// YAML frontmatter block (--- … ---) is skipped, and setext underlines (=== / ---)
// promote the preceding paragraph line to a heading.
func (m *Model) ParseContent(content string) {
	m.SetHeadings(Parse(content))
}

// Parse extracts headings without mutating a navigator, allowing callers to do
// document-sized work outside Bubble Tea's interactive update path.
func Parse(content string) []Heading {
	var headings []Heading
	lines := strings.Split(content, "\n")

	inFence := false
	fenceMarker := "" // "```" or "~~~" — a fence closes only on its own marker

	// Skip a leading YAML frontmatter block if the very first line is a lone "---".
	start := 0
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == "---" {
		for j := 1; j < len(lines); j++ {
			if t := strings.TrimSpace(lines[j]); t == "---" || t == "..." {
				start = j + 1
				break
			}
		}
	}

	for i := start; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		// Track fenced code blocks; headings inside them don't count.
		if marker := fenceMarkerOf(trimmed); marker != "" {
			if !inFence {
				inFence = true
				fenceMarker = marker
			} else if marker == fenceMarker {
				inFence = false
				fenceMarker = ""
			}
			continue
		}
		if inFence {
			continue
		}

		if level, text, ok := parseATX(trimmed); ok {
			headings = append(headings, Heading{Level: level, Text: text, Line: i})
			continue
		}

		// Setext: a non-blank line followed by a line of only = (H1) or - (H2).
		if trimmed != "" && !strings.HasPrefix(trimmed, "#") && i+1 < len(lines) {
			if level, ok := setextLevel(strings.TrimSpace(lines[i+1])); ok {
				headings = append(headings, Heading{Level: level, Text: trimmed, Line: i})
			}
		}
	}

	return headings
}

func (m *Model) SetHeadings(headings []Heading) {
	m.headings = headings
	if m.cursor >= len(m.headings) {
		m.cursor = len(m.headings) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

// parseATX parses an ATX heading ("## Title ##"). It requires a space (or
// end-of-line) after the leading run of # so "#hashtag" is not a heading, and
// strips an optional trailing run of #.
func parseATX(trimmed string) (level int, text string, ok bool) {
	if !strings.HasPrefix(trimmed, "#") {
		return 0, "", false
	}
	for _, ch := range trimmed {
		if ch == '#' {
			level++
		} else {
			break
		}
	}
	if level < 1 || level > 6 {
		return 0, "", false
	}
	rest := trimmed[level:]
	// A heading needs whitespace after the #-run (or nothing at all: "##").
	if rest != "" && rest[0] != ' ' && rest[0] != '\t' {
		return 0, "", false
	}
	text = strings.TrimSpace(rest)
	text = strings.TrimRight(text, "#")
	text = strings.TrimRight(text, " \t")
	if text == "" {
		return 0, "", false
	}
	return level, text, true
}

// fenceMarkerOf returns "```" or "~~~" if trimmed opens/closes a code fence,
// else "". A fence is three or more of the same marker char.
func fenceMarkerOf(trimmed string) string {
	for _, marker := range []byte{'`', '~'} {
		n := 0
		for n < len(trimmed) && trimmed[n] == marker {
			n++
		}
		if n >= 3 {
			return strings.Repeat(string(marker), 3)
		}
	}
	return ""
}

// setextLevel reports the heading level of a setext underline: 1 for a run of
// only '=', 2 for a run of only '-'. An empty line or mixed run is not one.
func setextLevel(trimmed string) (int, bool) {
	if trimmed == "" {
		return 0, false
	}
	all := func(c byte) bool {
		for i := 0; i < len(trimmed); i++ {
			if trimmed[i] != c {
				return false
			}
		}
		return true
	}
	if all('=') {
		return 1, true
	}
	if all('-') {
		return 2, true
	}
	return 0, false
}

func (m *Model) SetSize(width, height int) {
	if width == m.width && height == m.height {
		return
	}
	m.width = width
	m.height = height
}

func (m *Model) Focus() {
	m.focused = true
}

func (m *Model) Blur() {
	m.focused = false
}

func (m Model) Focused() bool {
	return m.focused
}

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	if !m.focused {
		return m, nil
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "j", "down":
			if m.cursor < len(m.headings)-1 {
				m.cursor++
			}
		case "k", "up":
			if m.cursor > 0 {
				m.cursor--
			}
		case "enter":
			if m.cursor >= 0 && m.cursor < len(m.headings) {
				heading := m.headings[m.cursor]
				return m, func() tea.Msg {
					return HeadingSelectedMsg{Line: heading.Line}
				}
			}
		case "g":
			m.cursor = 0
		case "G":
			if m.cursor = len(m.headings) - 1; m.cursor < 0 {
				m.cursor = 0
			}
		}
	}

	return m, nil
}

func (m Model) View() string {
	if m.width == 0 {
		return ""
	}

	// Width/height are content dimensions (passed in without border)
	contentWidth := m.width
	contentHeight := m.height

	if len(m.headings) == 0 {
		emptyMsg := "No chapters yet.\nAdd # headings to\ncreate structure."
		content := lipgloss.NewStyle().
			Foreground(theme.TextDim).
			Width(contentWidth).
			Render(emptyMsg)
		return m.containerStyle().Render(content)
	}

	// Calculate visible range
	visibleHeight := contentHeight - 1
	if visibleHeight < 1 {
		visibleHeight = 1
	}

	startIdx := 0
	if m.cursor >= visibleHeight {
		startIdx = m.cursor - visibleHeight + 1
	}
	endIdx := startIdx + visibleHeight
	if endIdx > len(m.headings) {
		endIdx = len(m.headings)
	}

	var lines []string
	for i := startIdx; i < endIdx; i++ {
		heading := m.headings[i]

		// Indent based on heading level
		indent := strings.Repeat("  ", heading.Level-1)
		name := theme.Sanitize(indent + heading.Text)

		// Truncate if too long (rune- and display-width-aware)
		name = theme.Truncate(name, contentWidth)

		var style lipgloss.Style
		if i == m.cursor {
			style = theme.FileNavSelected
		} else if heading.Level == 1 {
			style = theme.FileNavDir // Use bold style for top-level
		} else {
			style = theme.FileNavItem
		}

		line := style.Width(contentWidth).Render(name)
		lines = append(lines, line)
	}

	marker := "  OUTLINE"
	if m.focused {
		marker = "> OUTLINE"
	}
	content := lipgloss.JoinVertical(lipgloss.Left, lipgloss.NewStyle().Foreground(theme.TextDim).Background(theme.BgPanel).Width(contentWidth).Bold(true).Render(marker), lipgloss.JoinVertical(lipgloss.Left, lines...))
	return m.containerStyle().Render(content)
}

func (m Model) containerStyle() lipgloss.Style {
	style := theme.FileNavPane
	if m.focused {
		style = theme.Focused
	}
	// MaxHeight bounds the whole box; the border is added outside Height, so it
	// must be m.height+2 or the bottom border/last row gets clipped.
	return style.Width(m.width).Height(m.height).MaxHeight(m.height + 2)
}
