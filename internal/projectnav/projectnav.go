package projectnav

import (
	"regexp"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ShayCichocki/marginctl/internal/project"
	"github.com/ShayCichocki/marginctl/internal/theme"
)

type ChapterSelectedMsg struct {
	Path string
}

// ChapterPreviewMsg remains as a message type for source compatibility. The
// navigator no longer emits it; movement is intentionally selection-only.
type ChapterPreviewMsg struct{ Path string }

type ChapterCreateMsg struct{}

type Model struct {
	chapters    []project.FileEntry
	currentFile string
	cursor      int
	width       int
	height      int
	focused     bool
}

func New() Model {
	return Model{}
}

func (m *Model) SetChapters(chapters []project.FileEntry) {
	m.chapters = append([]project.FileEntry(nil), chapters...)
	// Reset cursor if out of bounds
	if m.cursor >= len(m.chapters) {
		m.cursor = len(m.chapters) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

func (m *Model) SetCurrentFile(path string) {
	m.currentFile = path
	// The app calls this after every save/load of the active file, so re-read
	// that one chapter's heading here: an edited title shows up immediately.
	for i := range m.chapters {
		if m.chapters[i].Path == path {
			m.chapters[i].Title = project.ReadChapterTitle(path)
		}
	}
	// Move cursor to current file
	for i, ch := range m.chapters {
		if ch.Path == path {
			m.cursor = i
			break
		}
	}
}

func (m *Model) SetSize(width, height int) {
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
			if m.cursor < len(m.chapters)-1 {
				m.cursor++
			}
			return m, nil
		case "k", "up":
			if m.cursor > 0 {
				m.cursor--
			}
			return m, nil
		case "enter":
			if m.cursor >= 0 && m.cursor < len(m.chapters) {
				chapter := m.chapters[m.cursor]
				return m, func() tea.Msg {
					return ChapterSelectedMsg{Path: chapter.Path}
				}
			}
		case "g":
			if len(m.chapters) > 0 {
				m.cursor = 0
			}
			return m, nil
		case "G":
			if m.cursor = len(m.chapters) - 1; m.cursor < 0 {
				m.cursor = 0
			}
			return m, nil
		case "n":
			return m, func() tea.Msg {
				return ChapterCreateMsg{}
			}
		}
	}

	return m, nil
}

var orderPrefixRe = regexp.MustCompile(`^(\d+)_`)

// chapterLabel is the navigator text for a chapter: "N  Title" when the file
// has a title heading (N from the filename's ordering prefix, leading zeros
// dropped), else the filename without its .md extension.
func chapterLabel(c project.FileEntry) string {
	title := strings.TrimSpace(theme.Sanitize(c.Title))
	if title == "" {
		return strings.TrimSuffix(c.Name, ".md")
	}
	if mt := orderPrefixRe.FindStringSubmatch(c.Name); mt != nil {
		n := strings.TrimLeft(mt[1], "0")
		if n == "" {
			n = "0"
		}
		return n + "  " + title
	}
	return title
}

func (m Model) View() string {
	if m.width == 0 {
		return ""
	}

	contentWidth := m.width
	contentHeight := m.height

	if len(m.chapters) == 0 {
		emptyMsg := "No chapters found.\nAdd .md files to\nchapters/ folder."
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
	if endIdx > len(m.chapters) {
		endIdx = len(m.chapters)
	}

	var lines []string
	for i := startIdx; i < endIdx; i++ {
		chapter := m.chapters[i]

		name := theme.Sanitize(chapterLabel(chapter))

		// Truncate if too long (rune- and display-width-aware)
		name = theme.Truncate(name, contentWidth)

		var style lipgloss.Style
		if i == m.cursor {
			style = theme.FileNavSelected
		} else if chapter.Path == m.currentFile {
			style = theme.FileNavDir // Highlight current file
		} else {
			style = theme.FileNavItem
		}

		line := style.Width(contentWidth).Render(name)
		lines = append(lines, line)
	}

	marker := "  CHAPTERS"
	if m.focused {
		marker = "> CHAPTERS"
	}
	content := lipgloss.JoinVertical(lipgloss.Left, lipgloss.NewStyle().Foreground(theme.TextDim).Background(theme.BgPanel).Width(contentWidth).Bold(true).Render(marker), lipgloss.JoinVertical(lipgloss.Left, lines...))
	return m.containerStyle().Render(content)
}

func (m Model) containerStyle() lipgloss.Style {
	style := theme.FileNavPane
	if m.focused {
		style = theme.Focused
	}
	// MaxHeight bounds the whole box (border is added outside Height); see outline.
	return style.Width(m.width).Height(m.height).MaxHeight(m.height + 2)
}
