package app

import (
	"fmt"
	"path/filepath"

	"github.com/charmbracelet/lipgloss"

	"github.com/ShayCichocki/marginctl/internal/editor"
	"github.com/ShayCichocki/marginctl/internal/notification"
	"github.com/ShayCichocki/marginctl/internal/state"
	"github.com/ShayCichocki/marginctl/internal/theme"
)

func (m *Model) updateLayout() {
	m.layout = calculateLayout(m.width, m.height, m.leftPaneVisible, m.results.Visible(), m.distractionFree, m.navWidth, m.resultsWidth)
	m.headerbar.SetWidth(m.width)
	m.outline.SetSize(m.layout.LeftWidth, m.layout.PaneHeight)
	m.projectnav.SetSize(m.layout.LeftWidth, m.layout.PaneHeight)
	m.statenav.SetSize(m.layout.LeftWidth, m.layout.PaneHeight)
	m.editor.SetSize(m.layout.EditorWidth, max(0, m.layout.PaneHeight-1))
	m.results.SetSize(m.layout.ResultsWidth, m.layout.PaneHeight)
	m.prompt.SetWidth(max(1, m.width-2))
	m.statusbar.SetWidth(m.width)
	m.reconcileFocus()
}

// reconcileFocus keeps keyboard focus off a pane the current layout does not
// render. Focusing an unrendered results pane would let a diff or cross-file
// replace be accepted sight-unseen (y/n), and an unrendered navigator would take
// Enter for a file load. When the owning pane reappears (e.g. the terminal is
// widened) focus returns to it, so a pending result stays reachable rather than
// stranded. Called at the end of every updateLayout, so focus and visibility can
// never drift apart.
//
// Note: the FocusEditor case consults activePrompt, which includes the connect
// prompt derived from connect state (not just the stored promptState). So an
// open connect picker also holds off pending-pane re-focus, like any other
// bottom-line prompt.
func (m *Model) reconcileFocus() {
	switch m.focus {
	case FocusResults:
		if !m.layout.ResultsVisible {
			m.focus = FocusEditor
			m.editor.Focus()
			// Content is still shown (results.Visible()) — remember to return once
			// it fits. A deliberate close clears visibility first, so this stays false.
			m.pendingResultsFocus = m.results.Visible()
		}
	case FocusLeftPane:
		if !m.layout.LeftVisible {
			m.focus = FocusEditor
			m.editor.Focus()
			m.pendingPaneFocus = true
		}
	case FocusEditor:
		// Don't pull focus out from under active typing or a bottom-line prompt;
		// the pending pane will be re-grabbed on the next layout pass once the user
		// is idle in normal mode. Esc always returns to the editor regardless.
		if m.activePrompt() != promptNone || m.editor.Mode == editor.ModeInsert {
			return
		}
		switch {
		case m.pendingResultsFocus && m.layout.ResultsVisible:
			m.pendingResultsFocus = false
			m.focus = FocusResults
			m.editor.Blur()
		case m.pendingPaneFocus && m.layout.LeftVisible:
			m.pendingPaneFocus = false
			m.focus = FocusLeftPane
			m.editor.Blur()
		}
	}
}

func (m *Model) parseOutlineNow(content string) {
	m.outlineGen++
	m.outline.ParseContent(content)
}

func (m *Model) updateStatusBar() {
	// Show chapter-relative path if this is a project with a chapter file open
	if m.project.IsProject && m.project.CurrentFile != "" {
		m.statusbar.FilePath = m.project.ChapterRelativePath(m.project.CurrentFile)
	} else {
		m.statusbar.FilePath = m.project.RelativePath(m.project.CurrentFile)
	}
	m.statusbar.Mode = m.editor.Mode.String()
	m.statusbar.WordCount = m.editor.WordCount()
	m.statusbar.Dirty = m.editor.Dirty
	m.statusbar.IsProject = m.project.IsProject
	m.statusbar.LeftPaneMode = m.leftPaneModeString()
	m.statusbar.Line, m.statusbar.Column = m.editor.CursorPosition()
	m.statusbar.PendingCount, m.statusbar.PendingOperator = m.editor.PendingState()
	m.statusbar.SearchQuery, m.statusbar.SearchCurrent, m.statusbar.SearchTotal = m.editor.SearchStatus()
	m.statusbar.ActivePane = map[Focus]string{FocusEditor: "EDIT", FocusLeftPane: "NAV", FocusResults: "RESULT"}[m.focus]
	m.headerbar.Project = filepath.Base(m.project.Root)
	m.headerbar.File = m.project.RelativePath(m.project.CurrentFile)
	if m.llmLoading {
		m.headerbar.Operation = m.llmStatus
	} else {
		m.headerbar.Operation = ""
	}
}

// Minimum terminal size below which the multi-pane layout can't render without
// zero/negative widths. Below this we show a friendly message instead.
const (
	minTermWidth  = 40
	minTermHeight = 10
)

func (m *Model) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	if m.width < minTermWidth || m.height < minTermHeight {
		msg := fmt.Sprintf("Terminal too small — need %d×%d", minTermWidth, minTermHeight)
		// Width() wraps the message to the (narrow) terminal so no line overflows.
		box := lipgloss.NewStyle().Width(m.width).Align(lipgloss.Center).Render(msg)
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
	}

	// Header bar
	header := m.headerbar.View()

	// Prompt edits only change the bottom row. Reuse the expensive pane frame.
	row, col := m.editor.CursorPosition()
	mainKey := promptMainViewKey{revision: m.editor.Revision(), row: row, col: col,
		focus: m.focus, leftMode: m.leftPaneMode, layout: m.layout,
		resultsVisible: m.results.Visible(), streamBytes: len(m.streamBuf), outlineGen: m.outlineGen}
	mainContent := ""
	if m.activePrompt() != promptNone && mainKey == m.promptMainKey {
		mainContent = m.promptMainView
	} else {
		editorView := m.editorView()
		panes := make([]string, 0, 3)
		if m.layout.LeftVisible {
			panes = append(panes, m.leftPaneView())
		}
		panes = append(panes, editorView)
		if m.layout.ResultsVisible {
			panes = append(panes, m.resultsView())
		}
		mainContent = lipgloss.JoinHorizontal(lipgloss.Top, panes...)
		if m.activePrompt() != promptNone {
			m.promptMainKey, m.promptMainView = mainKey, mainContent
		} else {
			// Blank the cache AND its key together. Leaving a stale key while the
			// view is "" lets the next prompt (opened with the editor in the same
			// revision/cursor/layout state) match that key and reuse the empty
			// string — collapsing the whole main pane so the ":" bar renders up
			// under the header instead of a real editor frame.
			m.promptMainKey, m.promptMainView = promptMainViewKey{}, ""
		}
	}

	// Command line or status bar. A transient notice/error takes priority so
	// failures and blocked actions are never silently swallowed.
	notice, noticeIsErr := m.notice, m.noticeIsErr
	if notice == "" && m.err != nil {
		notice, noticeIsErr = m.err.Error(), true
	}
	// Transient Info/Success/Warning notifications are inspect-only
	// (:notifications); drawing them would pin the bar until :ack.
	if n, ok := m.notifications.Current(); ok && (n.Persistence == notification.UntilAcknowledged || n.Severity == notification.Error || n.Severity == notification.Partial) {
		if notice == "" || (!noticeIsErr && (n.Severity == notification.Error || n.Severity == notification.Partial)) {
			notice, noticeIsErr = n.Summary, n.Severity == notification.Error || n.Severity == notification.Partial
		}
	}

	pk := m.activePrompt()
	var bottomBar string
	if notice != "" {
		style := m.styles.StatusBar
		if noticeIsErr {
			style = m.styles.StatusBarError
		}
		bottomBar = style.Width(m.width).Render(theme.Sanitize(notice))
	} else if pk == promptCommand {
		bottomBar = m.commandBarView()
	} else if pk == promptSearch {
		bottomBar = m.promptLine("/", &m.searchBuffer, false)
	} else if pk == promptSetup {
		bottomBar = m.styles.StatusBar.Width(m.width).Render(theme.Sanitize(m.setupPromptLine()))
	} else if m.connect.active() {
		bottomBar = m.styles.StatusBar.Width(m.width).Render(theme.Sanitize(m.connectPromptLine()))
	} else if pk == promptChapter {
		bottomBar = m.promptLine("New chapter: ", &m.chapterName, false)
	} else if pk == promptStateName {
		typeLabel := "character"
		if m.stateType == state.StateLocation {
			typeLabel = "location"
		} else if m.stateType == state.StateCustom && m.stateCategory != "" {
			typeLabel = m.stateCategory
		}
		bottomBar = m.promptLine("New "+typeLabel+": ", &m.stateName, false)
	} else if pk == promptCategory {
		bottomBar = m.promptLine("New category: ", &m.categoryName, false)
	} else if pk == promptPath {
		var prompt string
		switch m.pathInputMode {
		case "new":
			prompt = "New project path: "
		case "save", "savequit", "saveforce", "savequitforce":
			prompt = "Save as: "
		default:
			prompt = "Open path: "
		}
		bottomBar = m.promptLine(prompt, &m.pathBuffer, false)
	} else if m.llmLoading {
		// Ranked below every active text prompt: a spinner must never mask a prompt
		// the user is typing into while a request runs in the background.
		bottomBar = m.styles.StatusBar.Width(m.width).Render(theme.Sanitize(m.llmStatusLine()))
	} else {
		bottomBar = m.statusbar.View()
	}

	// The bottom bar is a single row. A long notice/prompt rendered at full width
	// would otherwise wrap onto extra rows and push the composed view past the
	// terminal height (scrolling/clipping the editor). Hard-cap it to one line.
	bottomBar = lipgloss.NewStyle().MaxWidth(m.width).MaxHeight(1).Render(bottomBar)

	content := lipgloss.JoinVertical(lipgloss.Left, header, mainContent, bottomBar)

	// Ensure we fit exactly in the terminal
	return lipgloss.Place(m.width, m.height, lipgloss.Left, lipgloss.Top, content)
}

func (m Model) leftPaneView() string {
	switch m.leftPaneMode {
	case PaneModeProject:
		return m.projectnav.View()
	case PaneModeState:
		return m.statenav.View()
	default:
		return m.outline.View()
	}
}

func (m Model) leftPaneModeString() string {
	switch m.leftPaneMode {
	case PaneModeProject:
		return "project"
	case PaneModeState:
		return "state"
	default:
		return "outline"
	}
}

func (m Model) editorView() string {
	editorStyle := m.styles.Unfocused
	if m.focus == FocusEditor {
		editorStyle = m.styles.Focused
	}

	// MaxHeight bounds the *whole* box (content + border). The border is added
	// outside Height, so it must be editorContentHeight+2 (== contentHeight);
	// MaxHeight(editorContentHeight) would clip the bottom border and the last
	// content row — leaving the editor's final row (and a cursor on it) off-screen.
	marker := "  EDITOR"
	if m.focus == FocusEditor {
		marker = "> EDITOR"
	}
	content := lipgloss.JoinVertical(lipgloss.Left, lipgloss.NewStyle().Width(m.layout.EditorWidth).Background(m.styles.BgDark).Foreground(m.styles.TextDim).Bold(true).Render(marker), m.editor.View())
	return editorStyle.
		Width(m.layout.EditorWidth).
		Height(m.layout.PaneHeight).
		MaxHeight(m.layout.ContentHeight).
		Render(content)
}

func (m Model) resultsView() string {
	resultsStyle := m.styles.Unfocused
	if m.focus == FocusResults {
		resultsStyle = m.styles.Focused
	}

	// MaxHeight must bound the whole box (content + border); see editorView.
	return resultsStyle.
		Width(m.layout.ResultsWidth).
		Height(m.layout.PaneHeight).
		MaxHeight(m.layout.ContentHeight).
		Render(m.results.View())
}
