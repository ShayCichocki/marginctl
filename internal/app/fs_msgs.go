package app

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/ShayCichocki/marginctl/internal/command"
)

func (m *Model) handleFSResult(msg fsResultMsg) (tea.Model, tea.Cmd) {
	if msg.projectGen != m.projectGen {
		return m, nil // a different project is loaded; this result is meaningless
	}
	superseded := msg.fsGen != m.fsGen
	if msg.err != nil {
		// A failed save must never be silent even if superseded: the user's
		// changes did not reach disk. Other superseded errors are dropped.
		if !superseded || msg.kind == "save-file" {
			m.err = msg.err
		}
		return m, nil
	}
	if superseded {
		// A completed save already hit disk. If it targeted the file still open
		// (and the buffer still holds exactly what was written), adopt its
		// fingerprint so the next :w doesn't flag our own write as an external
		// change. Everything else about a superseded result is ignored.
		if res, ok := msg.value.(saveResult); ok && msg.kind == "save-file" &&
			res.path == m.project.CurrentFile && res.content == m.editor.Content() {
			// Disk now matches the open buffer: adopt the fingerprint and, exactly
			// as the non-superseded path does, mark clean and drop the swap so the
			// buffer doesn't read dirty with a lingering recovery file.
			m.loadedFingerprint = res.fingerprint
			m.editor.MarkClean()
			m.removeSwap()
			m.updateStatusBar()
		}
		return m, nil
	}
	switch msg.kind {
	case "open-file":
		// Re-check dirtiness: the Enter-time guard is stale if the user typed
		// into the current buffer while the load was in flight. Never clobber
		// unsaved edits with the async result (CLAUDE.md: loads preserve a dirty
		// active buffer).
		if m.editor.Dirty {
			m.setErrorNotice("Open cancelled — unsaved changes in the current buffer")
			break
		}
		m.applyLoadedFile(msg.value.(loadResult))
		m.projectnav.SetCurrentFile(m.project.CurrentFile)
		m.statenav.SetCurrentFile(m.project.CurrentFile)
		m.leftPaneMode, m.focus = PaneModeOutline, FocusEditor
		m.editor.Focus()
		m.projectnav.Blur()
		m.statenav.Blur()
	case "save-file":
		res := msg.value.(saveResult)
		m.loadedFingerprint = res.fingerprint
		if res.path == m.project.CurrentFile && res.content == m.editor.Content() {
			m.editor.MarkClean()
			m.removeSwap()
		}
	case "compile":
		m.showResult(command.Result{Type: command.ResultText, Content: "Compiled manuscript → " + msg.value.(string)})
	case "search":
		m.showSearchMatches(msg.value.(searchFSResult))
	}
	m.updateStatusBar()
	return m, nil
}

// searchManuscript runs a project-wide search and shows the hits in the results
