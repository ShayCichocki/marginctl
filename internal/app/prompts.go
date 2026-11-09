package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ShayCichocki/marginctl/internal/fsutil"
	"github.com/ShayCichocki/marginctl/internal/state"
	"github.com/ShayCichocki/marginctl/internal/theme"
)

func (m *Model) syncPrompt(value *string) {
	if m.prompt.Value() != *value {
		m.prompt.SetValue(*value)
	}
}

// editPrompt forwards a key to the shared prompt editor (cursor-aware, with
// history) against buf, then stores the edited value back into buf. onEdit, if
// non-nil, runs after the edit.
func (m *Model) editPrompt(buf *string, msg tea.KeyMsg, onEdit func()) {
	m.syncPrompt(buf)
	m.prompt.Update(msg)
	*buf = m.prompt.Value()
	if onEdit != nil {
		onEdit()
	}
}

func (m *Model) promptLine(prefix string, value *string, masked bool) string {
	m.syncPrompt(value)
	m.prompt.SetMasked(masked)
	m.prompt.SetWidth(max(1, m.width-lipgloss.Width(prefix)))
	line := m.styles.StatusBar.Render(theme.Sanitize(prefix)) + m.prompt.View()
	m.prompt.SetMasked(false)
	return m.styles.StatusBar.Width(m.width).Render(line)
}

func (m *Model) handleCommandInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEscape:
		m.setPrompt(promptNone)
		m.commandBuffer = ""
		m.resetCompletion()
		return m, nil

	case tea.KeyEnter:
		m.prompt.SetValue(m.commandBuffer)
		m.prompt.CommitHistory()
		m.commandHistory = m.prompt.History()
		commandText := m.commandBuffer
		fromResults := m.commandFromResults
		m.setPrompt(promptNone)
		m.commandBuffer = ""
		m.resetCompletion()
		if fromResults && strings.TrimSpace(commandText) == "" {
			return m, nil
		}
		if fromResults && !resultsPaneVerbAllowed(commandText) {
			m.setErrorNotice("Only :proposal-*, :proposals, :w and :q here")
			return m, nil
		}
		cmd := m.executeCommand(commandText)
		return m, cmd

	case tea.KeyTab:
		m.completeCommand(true)
		return m, nil

	case tea.KeyShiftTab:
		m.completeCommand(false)
		return m, nil

	default:
		// Completion restarts whenever the buffer's text actually changes
		// (typing, deletes, ctrl+w, history recall); pure cursor motion keeps it.
		before := m.commandBuffer
		m.editPrompt(&m.commandBuffer, msg, nil)
		if m.commandBuffer != before {
			m.resetCompletion()
		}
		return m, nil
	}
}

// resultsPaneVerbAllowed reports whether a ":" command typed from the results
// pane may run: the proposal commands plus the :w/:q family.
func resultsPaneVerbAllowed(text string) bool {
	verb, _, _ := strings.Cut(strings.TrimSpace(text), " ")
	switch verb {
	case "proposals", "w", "w!", "q", "q!", "wq", "wq!":
		return true
	}
	return strings.HasPrefix(verb, "proposal-")
}

// resetCompletion clears any in-progress Tab-completion cycle. Called on every
// change to the buffer text (and on leaving command mode) so the next Tab recomputes matches
// from the current prefix.
func (m *Model) resetCompletion() {
	m.completionActive = false
	m.completionMatches = nil
	m.completionIdx = 0
}

// completeCommand drives Tab-completion of the ":" command verb. forward=false
// cycles backward (Shift+Tab). It completes only the command name — once a space
// (i.e. an argument) is present it does nothing. The first Tab computes the
// matches for the typed prefix and selects one; subsequent Tabs cycle through
// them (the stored matches, not the now-completed buffer, drive the cycle).
func (m *Model) completeCommand(forward bool) {
	if strings.ContainsRune(m.commandBuffer, ' ') {
		return
	}
	if m.completionActive && len(m.completionMatches) > 0 {
		n := len(m.completionMatches)
		if forward {
			m.completionIdx = (m.completionIdx + 1) % n
		} else {
			m.completionIdx = (m.completionIdx - 1 + n) % n
		}
		m.commandBuffer = m.completionMatches[m.completionIdx]
		m.prompt.SetValue(m.commandBuffer)
		return
	}
	matches := matchColonPrefix(m.commandBuffer)
	if len(matches) == 0 {
		m.resetCompletion()
		return
	}
	m.completionMatches = matches
	m.completionActive = true
	if forward {
		m.completionIdx = 0
	} else {
		m.completionIdx = len(matches) - 1
	}
	m.commandBuffer = matches[m.completionIdx]
	m.prompt.SetValue(m.commandBuffer)
}

// commandBarView renders the ":" prompt line. While a Tab-completion cycle with
// more than one candidate is active, it appends a dim, width-truncated hint
// listing the candidates with the selected one bracketed (e.g.
// ":Show   [ShowChapters] ShowOutline ShowState").
func (m *Model) commandBarView() string {
	m.syncPrompt(&m.commandBuffer)
	m.prompt.SetWidth(max(1, m.width-1))
	prompt := ":" + m.prompt.View()
	if !m.completionActive || len(m.completionMatches) <= 1 {
		return m.styles.StatusBar.Width(m.width).Render(prompt)
	}
	parts := make([]string, len(m.completionMatches))
	for i, v := range m.completionMatches {
		if i == m.completionIdx {
			parts[i] = "[" + v + "]"
		} else {
			parts[i] = v
		}
	}
	promptSeg := m.styles.StatusBar.Render(prompt + "  ")
	remaining := m.width - lipgloss.Width(promptSeg)
	if remaining < 1 {
		return m.styles.StatusBar.Width(m.width).Render(prompt)
	}
	hint := theme.Truncate(strings.Join(parts, " "), remaining)
	hintSeg := m.styles.StatusBar.Foreground(m.styles.TextDim).Width(remaining).Render(hint)
	return lipgloss.JoinHorizontal(lipgloss.Top, promptSeg, hintSeg)
}

func (m *Model) handleSearchInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEscape:
		m.setPrompt(promptNone)
		m.searchBuffer = ""
		return m, nil

	case tea.KeyEnter:
		query := m.searchBuffer
		m.setPrompt(promptNone)
		m.searchBuffer = ""
		if query != "" && !m.editor.StartSearch(query, true) {
			m.setErrorNotice("Pattern not found: " + query)
		}
		m.updateStatusBar()
		return m, nil

	default:
		m.editPrompt(&m.searchBuffer, msg, nil)
		return m, nil
	}
}

func (m *Model) handleNewChapterInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEscape:
		m.setPrompt(promptNone)
		m.chapterName = ""
		return m, nil

	case tea.KeyEnter:
		if m.chapterName == "" {
			m.setPrompt(promptNone)
			return m, nil
		}
		// Create the new chapter
		path, err := m.project.CreateChapter(m.chapterName)
		m.setPrompt(promptNone)
		m.chapterName = ""
		if err != nil {
			m.err = err
			return m, nil
		}
		// Load the new chapter
		if err := m.loadIntoEditor(path); err != nil {
			m.err = err
			return m, nil
		}
		m.projectnav.SetChapters(m.project.ListChapters())
		m.projectnav.SetCurrentFile(path)
		// Switch to outline mode and focus editor
		m.leftPaneMode = PaneModeOutline
		m.focus = FocusEditor
		m.editor.Focus()
		m.projectnav.Blur()
		return m, nil

	default:
		m.editPrompt(&m.chapterName, msg, nil)
		return m, nil
	}
}

func (m *Model) handleNewStateInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEscape:
		m.setPrompt(promptNone)
		m.stateName = ""
		return m, nil

	case tea.KeyEnter:
		if m.stateName == "" {
			m.setPrompt(promptNone)
			return m, nil
		}
		// Create the new state file
		path, err := m.state.CreateStateFileInCategory(m.stateName, m.stateType, m.stateCategory)
		m.setPrompt(promptNone)
		m.stateName = ""
		m.stateCategory = ""
		if err != nil {
			m.err = err
			return m, nil
		}
		// Load the new state file
		if err := m.loadIntoEditor(path); err != nil {
			m.err = err
			return m, nil
		}
		m.statenav.SetStateFilesWithCategories(m.state.All(), m.state.Categories())
		m.statenav.SetCurrentFile(path)
		// Switch to outline mode and focus editor
		m.leftPaneMode = PaneModeOutline
		m.focus = FocusEditor
		m.editor.Focus()
		m.statenav.Blur()
		return m, nil

	default:
		m.editPrompt(&m.stateName, msg, nil)
		return m, nil
	}
}

func (m *Model) handleNewCategoryInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEscape:
		m.setPrompt(promptNone)
		m.categoryName = ""
		return m, nil

	case tea.KeyEnter:
		if m.categoryName == "" {
			m.setPrompt(promptNone)
			return m, nil
		}
		// Create the new category
		err := m.state.CreateCategory(m.categoryName)
		m.setPrompt(promptNone)
		m.categoryName = ""
		if err != nil {
			m.err = err
			return m, nil
		}
		// Refresh state nav
		m.statenav.SetStateFilesWithCategories(m.state.All(), m.state.Categories())
		return m, nil

	default:
		m.editPrompt(&m.categoryName, msg, nil)
		return m, nil
	}
}

func (m *Model) handlePathInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEscape:
		m.setPrompt(promptNone)
		m.pathBuffer = ""
		m.pathInputMode = ""
		return m, nil

	case tea.KeyEnter:
		if m.pathBuffer == "" {
			m.setPrompt(promptNone)
			m.pathInputMode = ""
			return m, nil
		}

		path := m.pathBuffer
		mode := m.pathInputMode
		m.setPrompt(promptNone)
		m.pathBuffer = ""
		m.pathInputMode = ""
		expanded, err := fsutil.ExpandHome(path)
		if err != nil {
			m.err = err
			return m, nil
		}
		path = expanded

		switch mode {
		case "new":
			// Create new project
			if err := m.createNewProject(path); err != nil {
				m.err = err
				return m, nil
			}
		case "save", "savequit", "saveforce", "savequitforce":
			force := mode == "saveforce" || mode == "savequitforce"
			if err := m.saveBufferTo(path, force); err != nil {
				m.err = err
				return m, nil
			}
			if mode == "savequit" || mode == "savequitforce" {
				m.saveSession()
				return m, tea.Quit
			}
		default:
			// Open existing path
			if err := m.OpenPath(path); err != nil {
				m.err = err
				return m, nil
			}
		}

		m.updateLayout()
		m.updateStatusBar()
		return m, nil
	case tea.KeyTab:
		m.syncPrompt(&m.pathBuffer)
		m.prompt.CompletePath()
		m.pathBuffer = m.prompt.Value()
		return m, nil

	default:
		m.editPrompt(&m.pathBuffer, msg, nil)
		return m, nil
	}
}

func (m *Model) createNewProject(path string) error {
	// Never scaffold over an existing project: require the target to be absent
	// or an empty directory, so no existing chapter/state file is overwritten.
	if info, err := os.Stat(path); err == nil {
		if !info.IsDir() {
			return fmt.Errorf("%s already exists and is not a directory", path)
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			return err
		}
		if len(entries) > 0 {
			return fmt.Errorf("%s already exists and is not empty", path)
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	// Create the project directory structure
	chaptersDir := filepath.Join(path, "chapters")
	if err := os.MkdirAll(chaptersDir, 0755); err != nil {
		return err
	}

	// Create state directory structure
	stateDir := filepath.Join(path, "state")
	for _, sub := range []string{"characters", "locations", "style"} {
		if err := os.MkdirAll(filepath.Join(stateDir, sub), 0755); err != nil {
			return err
		}
	}

	// Create default state files and chapter 1. The empty-target check above
	// guarantees none of these exist yet, so a plain atomic write is safe.
	files := []struct {
		path    string
		content string
	}{
		{filepath.Join(stateDir, "timeline.md"), "# Timeline\n\n"},
		{filepath.Join(stateDir, "open_threads.md"), "# Open Threads\n\n"},
		{filepath.Join(stateDir, "style", "voice.md"), state.DefaultVoiceGuideContent()},
		{filepath.Join(chaptersDir, "001_chapter_1.md"), "# Chapter 1\n\n"},
	}
	for _, f := range files {
		if err := fsutil.WriteFile(f.path, []byte(f.content), 0644); err != nil {
			return err
		}
	}

	// Open the new project
	return m.OpenPath(path)
}
