package app

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/ShayCichocki/marginctl/internal/llmcmd"
	"github.com/ShayCichocki/marginctl/internal/outline"
	"github.com/ShayCichocki/marginctl/internal/projectnav"
	"github.com/ShayCichocki/marginctl/internal/results"
	"github.com/ShayCichocki/marginctl/internal/statenav"
)

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if m.tooSmall() {
		return m.updateTooSmall(msg)
	}

	switch msg := msg.(type) {
	case outlineDebounceMsg:
		return m.handleOutlineDebounce(msg)

	case outlineParsedMsg:
		return m.handleOutlineParsed(msg)

	case autosaveTickMsg:
		return m.handleAutosaveTick(msg)

	case tea.WindowSizeMsg:
		return m.handleWindowSize(msg)

	case tea.KeyMsg:
		return m.handleKey(msg)

	case results.ResultsClosedMsg:
		return m.handleResultsClosed(msg)

	case connectBrowserReadyMsg:
		return m.handleConnectBrowserReady(msg)

	case connectDeviceReadyMsg:
		return m.handleConnectDeviceReady(msg)

	case connectResultMsg:
		return m.handleConnectResult(msg)

	case results.OpenMatchMsg:
		return m.openSearchMatch(msg)

	case outline.HeadingSelectedMsg:
		return m.handleHeadingSelected(msg)

	case projectnav.ChapterSelectedMsg:
		return m.handleChapterSelected(msg)

	case projectnav.ChapterCreateMsg:
		return m.handleChapterCreate(msg)

	case statenav.StateSelectedMsg:
		return m.handleStateSelected(msg)

	case fsResultMsg:
		return m.handleFSResult(msg)

	case statenav.StateCreateMsg:
		return m.handleStateCreate(msg)

	case statenav.CategoryCreateMsg:
		return m.handleCategoryCreate(msg)

	case llmTickMsg:
		return m.handleLLMTick(msg)

	case llmcmd.StreamStartMsg:
		return m.handleStreamStart(msg)

	case streamDeltaMsg:
		return m.handleStreamDelta(msg)

	case streamEndMsg:
		return m.handleStreamEnd(msg)

	case llmcmd.ResponseMsg:
		return m.handleLLMResponse(msg)

	case results.DiffAcceptedMsg:
		return m.handleDiffAccepted(msg)

	case results.VoiceGuideAcceptedMsg:
		return m.handleVoiceGuideAccepted(msg)

	case results.DiffRejectedMsg:
		return m.handleDiffRejected(msg)

	case results.ConfirmAcceptedMsg:
		return m.handleConfirmAccepted(msg)

	default:
		return m.handleOtherMsg(msg)
	}
}

// finishUpdate is the shared tail of every Update arm that does not return
// early: refresh the status bar and batch any accumulated commands.
func (m *Model) finishUpdate(cmds []tea.Cmd) (tea.Model, tea.Cmd) {
	// Update status bar
	m.updateStatusBar()

	return m, tea.Batch(cmds...)
}

// tooSmall reports whether the canvas is replaced by the size warning.
func (m *Model) tooSmall() bool {
	return m.width > 0 && m.height > 0 && (m.width < minTermWidth || m.height < minTermHeight)
}

// updateTooSmall handles messages while the canvas is replaced by the size warning.
// No hidden operation may mutate editor or filesystem state. Resize is the only
// event that resumes normal routing; Esc/Ctrl+C safely cancel, and Ctrl+Q is an
// explicit quit.
func (m *Model) updateTooSmall(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = v.Width, v.Height
		m.updateLayout()
		return m, nil
	case tea.KeyMsg:
		switch v.String() {
		case "ctrl+q":
			return m, tea.Quit
		case "esc", "ctrl+c":
			m.cancelLLM()
			m.cancelConnect()
			m.setPrompt(promptNone)
			m.clearPendingAction()
			return m, nil
		}
	}
	return m, nil
}

func (m *Model) handleOutlineDebounce(msg outlineDebounceMsg) (tea.Model, tea.Cmd) {
	if msg.generation != m.outlineGen {
		return m, nil
	}
	content := m.editor.Content()
	generation := msg.generation
	return m, func() tea.Msg {
		return outlineParsedMsg{generation: generation, headings: outline.Parse(content)}
	}
}

func (m *Model) handleOutlineParsed(msg outlineParsedMsg) (tea.Model, tea.Cmd) {
	if msg.generation == m.outlineGen {
		m.outline.SetHeadings(msg.headings)
		// The prompt frame caches independent panes. Advance the generation
		// after the asynchronous pane mutation so it cannot retain stale rows.
		m.outlineGen++
	}
	return m, nil
}

func (m *Model) handleAutosaveTick(msg autosaveTickMsg) (tea.Model, tea.Cmd) {
	// Flush the buffer to its swap file (if dirty) and re-arm the timer.
	m.writeSwap()
	return m, autosaveTick()
}

func (m *Model) handleWindowSize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	m.width = msg.Width
	m.height = msg.Height
	m.updateLayout()
	return m.finishUpdate(nil)
}

func (m *Model) handleResultsClosed(msg results.ResultsClosedMsg) (tea.Model, tea.Cmd) {
	// Results pane was closed — reclaim its width for the editor.
	m.setResultsVisible(false)
	return m, nil
}

func (m *Model) handleHeadingSelected(msg outline.HeadingSelectedMsg) (tea.Model, tea.Cmd) {
	// Jump to heading in editor
	m.editor.GoToLine(msg.Line)
	// Switch focus back to editor
	m.focus = FocusEditor
	m.editor.Focus()
	m.outline.Blur()
	return m.finishUpdate(nil)
}

func (m *Model) handleChapterSelected(msg projectnav.ChapterSelectedMsg) (tea.Model, tea.Cmd) {
	return m.openSelectedFile(msg.Path)
}

// openSelectedFile loads a file chosen in a navigator, refusing to replace a
// dirty buffer.
func (m *Model) openSelectedFile(path string) (tea.Model, tea.Cmd) {
	if m.editor.Dirty {
		m.setErrorNotice("Unsaved changes — :w to save before switching")
		return m, nil
	}
	pg, fg := m.nextFSGeneration()
	coordinator := m.persistence
	return m, fsCommand(pg, fg, "open-file", func() (any, error) { return coordinator.load(path) })
}

func (m *Model) handleChapterCreate(msg projectnav.ChapterCreateMsg) (tea.Model, tea.Cmd) {
	// Check if dirty - block if so
	if m.editor.Dirty {
		m.setErrorNotice("Unsaved changes — :w to save before creating a chapter")
		return m, nil
	}
	// Enter new chapter name input mode
	m.setPrompt(promptChapter)
	m.chapterName = ""
	m.prompt.Reset()
	return m.finishUpdate(nil)
}

func (m *Model) handleStateSelected(msg statenav.StateSelectedMsg) (tea.Model, tea.Cmd) {
	return m.openSelectedFile(msg.Path)
}

func (m *Model) handleStateCreate(msg statenav.StateCreateMsg) (tea.Model, tea.Cmd) {
	// Check if dirty - block if so
	if m.editor.Dirty {
		m.setErrorNotice("Unsaved changes — :w to save before creating a state file")
		return m, nil
	}
	// Enter new state file name input mode
	m.setPrompt(promptStateName)
	m.stateType = msg.Type
	m.stateCategory = msg.Category
	m.stateName = ""
	m.prompt.Reset()
	return m.finishUpdate(nil)
}

func (m *Model) handleCategoryCreate(msg statenav.CategoryCreateMsg) (tea.Model, tea.Cmd) {
	// Enter new category name input mode
	m.setPrompt(promptCategory)
	m.categoryName = ""
	m.prompt.Reset()
	return m.finishUpdate(nil)
}

func (m *Model) handleOtherMsg(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	// Update editor
	var cmd tea.Cmd
	m.editor, cmd = m.editor.Update(msg)
	cmds = append(cmds, cmd)
	return m.finishUpdate(cmds)
}
