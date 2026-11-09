package statenav

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/text/cases"
	"golang.org/x/text/language"

	"github.com/ShayCichocki/marginctl/internal/state"
	"github.com/ShayCichocki/marginctl/internal/theme"
)

// StateSelectedMsg is sent when a state file is selected
type StateSelectedMsg struct {
	Path string
}

// StatePreviewMsg remains as a message type for source compatibility. The
// navigator no longer emits it; movement is intentionally selection-only.
type StatePreviewMsg struct{ Path string }

// StateCreateMsg is sent when user wants to create a new state file
type StateCreateMsg struct {
	Type     state.StateType
	Category string // For custom categories
}

// CategoryCreateMsg is sent when user wants to create a new category
type CategoryCreateMsg struct{}

// navItem represents a single item in the navigator
type navItem struct {
	stateFile *state.StateFile // nil for headers
	isHeader  bool
	label     string
	category  string // For empty category headers
}

// Model represents the state navigator component
type Model struct {
	items       []navItem
	categories  []string // All categories including empty
	cursor      int
	currentFile string
	width       int
	height      int
	focused     bool
}

// New creates a new state navigator
func New() Model {
	return Model{}
}

// SetStateFiles updates the navigator with state files
func (m *Model) SetStateFiles(files []state.StateFile) {
	m.SetStateFilesWithCategories(files, nil)
}

// SetStateFilesWithCategories updates the navigator with state files and categories
func (m *Model) SetStateFilesWithCategories(files []state.StateFile, categories []string) {
	m.items = make([]navItem, 0)
	m.categories = categories

	// Build a map of files by category
	filesByCategory := make(map[string][]state.StateFile)
	var topLevelFiles []state.StateFile

	for i := range files {
		f := &files[i]
		if f.Category == "" {
			topLevelFiles = append(topLevelFiles, *f)
		} else {
			filesByCategory[f.Category] = append(filesByCategory[f.Category], *f)
		}
	}

	// Add top-level files first (timeline, threads) - as categories
	for i := range topLevelFiles {
		f := &topLevelFiles[i]
		var categoryName string
		switch f.Type {
		case state.StateTimeline:
			categoryName = "timeline"
		case state.StateThreads:
			categoryName = "threads"
		}
		m.items = append(m.items, navItem{
			isHeader: true,
			label:    f.Type.String(),
			category: categoryName,
		})
		m.items = append(m.items, navItem{
			stateFile: f,
			label:     f.Name,
		})
	}

	// Add categories in order: characters, locations, then custom
	orderedCategories := []string{"characters", "locations"}
	// Add any custom categories
	for _, cat := range categories {
		if cat != "characters" && cat != "locations" {
			orderedCategories = append(orderedCategories, cat)
		}
	}

	for _, cat := range orderedCategories {
		// Add header
		headerLabel := cases.Title(language.English).String(cat)
		m.items = append(m.items, navItem{
			isHeader: true,
			label:    headerLabel,
			category: cat,
		})

		// Add files in this category
		if catFiles, ok := filesByCategory[cat]; ok {
			for i := range catFiles {
				f := &catFiles[i]
				m.items = append(m.items, navItem{
					stateFile: f,
					label:     f.Name,
				})
			}
		}
	}

	// Reset cursor if out of bounds
	if m.cursor >= len(m.items) {
		m.cursor = len(m.items) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0
	}

	// Skip headers
	m.skipTopLevelHeaders(1)
}

// SetCurrentFile highlights the currently open file
func (m *Model) SetCurrentFile(path string) {
	m.currentFile = path
	for i, item := range m.items {
		if item.stateFile != nil && item.stateFile.Path == path {
			m.cursor = i
			break
		}
	}
}

// SetSize sets the component dimensions
func (m *Model) SetSize(width, height int) {
	m.width = width
	m.height = height
}

// Focus sets focus on this component
func (m *Model) Focus() {
	m.focused = true
}

// Blur removes focus from this component
func (m *Model) Blur() {
	m.focused = false
}

// Focused returns whether this component has focus
func (m Model) Focused() bool {
	return m.focused
}

// Update handles input events
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	if !m.focused {
		return m, nil
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "j", "down":
			m.moveCursor(1)
			return m, nil
		case "k", "up":
			m.moveCursor(-1)
			return m, nil
		case "enter":
			if m.cursor >= 0 && m.cursor < len(m.items) && !m.items[m.cursor].isHeader && m.items[m.cursor].stateFile != nil {
				item := m.items[m.cursor]
				return m, func() tea.Msg {
					return StateSelectedMsg{Path: item.stateFile.Path}
				}
			}
		case "g":
			m.cursor = 0
			m.skipTopLevelHeaders(1)
			return m, nil
		case "G":
			m.cursor = len(m.items) - 1
			m.skipTopLevelHeaders(-1)
			return m, nil
		case "n":
			// Context-aware: create based on current item's type
			stateType, category := m.currentStateTypeAndCategory()
			// Only create files in directory-based categories (not timeline/threads)
			if category != "" && (stateType == state.StateCharacter || stateType == state.StateLocation || stateType == state.StateCustom) {
				return m, func() tea.Msg {
					return StateCreateMsg{Type: stateType, Category: category}
				}
			}
		case "N":
			// Create new category
			return m, func() tea.Msg {
				return CategoryCreateMsg{}
			}
		}
	}

	return m, nil
}

// currentStateTypeAndCategory returns the type and category of the currently selected item
func (m Model) currentStateTypeAndCategory() (state.StateType, string) {
	if m.cursor >= 0 && m.cursor < len(m.items) {
		item := m.items[m.cursor]
		if item.stateFile != nil {
			return item.stateFile.Type, item.stateFile.Category
		}
		// If on a header, use its category
		if item.isHeader && item.category != "" {
			switch item.category {
			case "characters":
				return state.StateCharacter, "characters"
			case "locations":
				return state.StateLocation, "locations"
			case "timeline", "threads":
				// These are single files, not directories - return invalid for creation
				return state.StateTimeline, ""
			default:
				return state.StateCustom, item.category
			}
		}
	}
	// Default to character
	return state.StateCharacter, "characters"
}

func (m *Model) moveCursor(delta int) {
	newPos := m.cursor + delta
	if newPos < 0 {
		newPos = 0
	}
	if newPos >= len(m.items) {
		newPos = len(m.items) - 1
	}
	if newPos < 0 {
		newPos = 0 // empty list
	}
	m.cursor = newPos
	// Only skip top-level headers (Timeline, Threads), not category headers
	m.skipTopLevelHeaders(delta)
}

func (m *Model) skipTopLevelHeaders(direction int) {
	// All headers are now selectable, so no skipping needed
	// Just bounds check
	if m.cursor < 0 {
		m.cursor = 0
	}
	if m.cursor >= len(m.items) {
		m.cursor = len(m.items) - 1
	}
	if m.cursor < 0 {
		m.cursor = 0 // empty list
	}
}

// View renders the state navigator
func (m Model) View() string {
	if m.width == 0 {
		return ""
	}

	contentWidth := m.width
	contentHeight := m.height

	if len(m.items) == 0 {
		emptyMsg := "No state files.\nRun :InitState to\ncreate structure."
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
	if endIdx > len(m.items) {
		endIdx = len(m.items)
	}

	var lines []string
	for i := startIdx; i < endIdx; i++ {
		item := m.items[i]

		var style lipgloss.Style
		var text string

		if item.isHeader {
			// All headers are selectable categories
			text = theme.Sanitize("▸ " + item.label)
			if i == m.cursor {
				style = theme.FileNavCategorySelected
			} else {
				style = theme.FileNavCategory
			}
		} else {
			text = theme.Sanitize("  " + item.label) // Indent files under categories
			// Truncate if too long (rune- and display-width-aware)
			text = theme.Truncate(text, contentWidth)

			if i == m.cursor {
				style = theme.FileNavSelected
			} else if item.stateFile != nil && item.stateFile.Path == m.currentFile {
				style = theme.FileNavDir
			} else {
				style = theme.FileNavItem
			}
		}

		text = theme.Truncate(text, contentWidth)
		line := style.Width(contentWidth).Render(text)
		lines = append(lines, line)
	}

	marker := "  STATE"
	if m.focused {
		marker = "> STATE"
	}
	content := lipgloss.JoinVertical(lipgloss.Left, lipgloss.NewStyle().Foreground(theme.TextDim).Background(theme.BgPanel).Width(contentWidth).Bold(true).Render(marker), lipgloss.JoinVertical(lipgloss.Left, lines...))

	// Add help text at bottom if there's room
	helpText := "[n]ew [N]category"
	if contentHeight > len(lines)+2 {
		padding := strings.Repeat("\n", contentHeight-len(lines)-1)
		help := lipgloss.NewStyle().
			Foreground(theme.TextDim).
			Width(contentWidth).
			Render(helpText)
		content = content + padding + help
	}

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
