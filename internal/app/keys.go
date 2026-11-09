package app

import (
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ShayCichocki/marginctl/internal/editor"
	"github.com/ShayCichocki/marginctl/internal/notification"
)

func (m *Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	// Promote a genuine operation error (m.err, set by save/load/state writes)
	// to a persistent typed notification before clearing the compatibility
	// fields, so an async failure isn't lost when the next key is pressed.
	//
	// Transient string notices (m.notice) are NOT promoted, even error-styled
	// ones: they are command-line status like vim's — shown while present,
	// cleared on the next key. Promoting them turned informational prompts
	// (recovery hint, connect prompts) into UntilAcknowledged errors that
	// stuck red and masked every later message until an explicit :ack.
	if m.err != nil {
		m.notifications.Push(notification.Notification{Severity: notification.Error, Summary: m.err.Error(), Persistence: notification.UntilAcknowledged})
	}
	m.err = nil
	m.clearNotice()

	// Ctrl+C is vim's "cancel / return to Normal mode" — treat it exactly like
	// Esc everywhere (back out of a prompt, leave Insert/Visual, close a pane).
	// Quitting is :q / :q! only, so a stray Ctrl+C can never discard the buffer.
	if msg.String() == "ctrl+c" {
		msg = tea.KeyMsg{Type: tea.KeyEsc}
	}

	// While an LLM request is in flight, Esc aborts it. Handled before the
	// overlay/prompt/editor routing so a long call is always cancelable.
	// "q" on a focused streaming report pane is the same cancel.
	if m.llmLoading && (msg.String() == "esc" ||
		(msg.String() == "q" && m.focus == FocusResults && m.results.Streaming() && m.activePrompt() == promptNone)) {
		m.results.StreamFailed(m.streamBuf, "Cancelled by user")
		m.cancelLLM()
		m.setNotice("LLM request cancelled")
		m.updateStatusBar()
		return m, nil
	}
	if m.connect.waiting() && msg.String() == "esc" {
		m.cancelConnect()
		m.setNotice("Connect cancelled")
		m.updateStatusBar()
		return m, nil
	}

	// Exactly one tagged prompt owns keyboard input.
	switch m.activePrompt() {
	case promptCommand:
		return m.handleCommandInput(msg)
	case promptSearch:
		return m.handleSearchInput(msg)
	case promptChapter:
		return m.handleNewChapterInput(msg)
	case promptStateName:
		return m.handleNewStateInput(msg)
	case promptCategory:
		return m.handleNewCategoryInput(msg)
	case promptPath:
		return m.handlePathInput(msg)
	case promptSetup:
		return m.handleSetupInput(msg)
	case promptConnect:
		return m.handleConnectInput(msg)
	}

	// Global keybindings. The keymap is vim-only: pane navigation, saving, and
	// every command live at the ":" prompt (:ShowChapters / :ShowState /
	// :ShowOutline / :w / :q …), so there are no Ctrl-chord shortcuts here.
	switch msg.String() {
	case ":":
		// Enter command mode from Normal or Visual mode (vim-style). The
		// editor stays in Visual so a command like :Tighten or :note still
		// sees the live selection — this is how selection commands are reached
		// now that there's no palette.
		// It is also available from the results pane while a state-proposal batch
		// is pending (without hiding it or dropping the batch) so per-proposal
		// commands are reachable; only those and :w/:q are accepted there.
		// Focus is never moved, so closing the prompt leaves it where it was.
		fromResults := m.focus == FocusResults && m.results.Visible() && m.proposalsPending()
		if fromResults || (m.focus == FocusEditor && (m.editor.Mode == editor.ModeNormal || m.editor.Mode == editor.ModeVisual)) {
			m.setPrompt(promptCommand)
			m.commandFromResults = fromResults
			m.commandBuffer = ""
			m.prompt.Reset()
			m.prompt.SetHistory(m.commandHistory)
			m.resetCompletion()
			return m, nil
		}

	case "/":
		// Enter search input (only from the editor in normal mode)
		if m.editor.Mode == editor.ModeNormal && m.focus == FocusEditor {
			m.setPrompt(promptSearch)
			m.searchBuffer = ""
			m.prompt.Reset()
			return m, nil
		}

	case "esc":
		// Close results pane if focused
		if m.focus == FocusResults {
			if m.reviewingProposal() {
				m.showProposalList()
				return m, nil
			}
			m.setResultsVisible(false)
			return m, nil
		}
		// If in left pane, return to editor
		if m.focus == FocusLeftPane {
			m.focus = FocusEditor
			m.editor.Focus()
			m.outline.Blur()
			m.projectnav.Blur()
			m.statenav.Blur()
			return m, nil
		}
	}

	// A proposal review is display-only: y/n act on the reviewed proposal and q
	// returns to the list; none of them can hide the pane or discard the batch.
	if m.focus == FocusResults && m.reviewingProposal() {
		n := m.proposalReviewN
		switch msg.String() {
		case "q":
			m.showProposalList()
			return m, nil
		case "y":
			return m, m.proposalCommand("proposal-accept", fmt.Sprint(n))
		case "n":
			return m, m.proposalCommand("proposal-skip", fmt.Sprint(n))
		}
	}

	// Route to focused component
	switch m.focus {
	case FocusLeftPane:
		switch m.leftPaneMode {
		case PaneModeOutline:
			var cmd tea.Cmd
			m.outline, cmd = m.outline.Update(msg)
			cmds = append(cmds, cmd)
		case PaneModeProject:
			var cmd tea.Cmd
			m.projectnav, cmd = m.projectnav.Update(msg)
			cmds = append(cmds, cmd)
		case PaneModeState:
			var cmd tea.Cmd
			m.statenav, cmd = m.statenav.Update(msg)
			cmds = append(cmds, cmd)
		}
	case FocusResults:
		var cmd tea.Cmd
		m.results, cmd = m.results.Update(msg)
		if msg.String() == "esc" || msg.String() == "q" {
			m.clearPendingAction()
		}
		// The pane may have hidden itself (accept/reject/open-match). Reclaim
		// its width immediately; the follow-up message must not be preempted
		// here (an accept still needs the pending action intact).
		if !m.results.Visible() {
			m.focus = FocusEditor
			m.editor.Focus()
			m.updateLayout()
			m.updateStatusBar()
		}
		cmds = append(cmds, cmd)
	default:
		before := m.editor.Revision()
		var cmd tea.Cmd
		m.editor, cmd = m.editor.Update(msg)
		cmds = append(cmds, cmd)

		// Re-parse the outline only when the buffer actually changed —
		// a bare cursor motion shouldn't re-walk every line for headings.
		after := m.editor.Revision()
		if after != before {
			m.outlineGen++
			generation := m.outlineGen
			cmds = append(cmds, tea.Tick(outlineDebounce, func(time.Time) tea.Msg {
				return outlineDebounceMsg{generation: generation}
			}))
			// Any edit mid-request invalidates a pending LLM diff's coordinates.
			m.bufferGen++
			m.results.MarkStale()
			m.results.InvalidateAction()
			m.clearPendingAction()
		}
	}
	return m.finishUpdate(cmds)
}
