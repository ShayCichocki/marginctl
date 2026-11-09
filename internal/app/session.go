package app

import (
	"os"
	"path/filepath"

	"github.com/ShayCichocki/marginctl/internal/fsutil"
	"github.com/ShayCichocki/marginctl/internal/session"
)

// sessionKey identifies the current project (or single file) in the sessions
// store: the project root for a project, else the loose file's path. Empty when
// there is nothing to key on.
func (m *Model) sessionKey() string {
	if m.project == nil {
		return ""
	}
	var key string
	if m.project.IsProject {
		key = m.project.Root
	} else {
		key = m.project.CurrentFile
	}
	if key == "" {
		return ""
	}
	if abs, err := filepath.Abs(key); err == nil {
		return abs
	}
	return key
}

// saveSession records the current file, cursor, and left-pane layout for the
// open project so a later reopen restores it. Best-effort: failures are ignored
// (session state is a convenience, never a correctness requirement).
func (m *Model) saveSession() {
	key := m.sessionKey()
	if key == "" || m.project.CurrentFile == "" {
		return
	}
	row, col := m.editor.CursorPosition()
	_ = session.Save(key, session.Entry{
		File:            m.project.CurrentFile,
		CursorRow:       row,
		CursorCol:       col,
		LeftPane:        leftPaneLabel(m.leftPaneMode),
		LeftVisible:     m.leftPaneVisible,
		DistractionFree: m.distractionFree,
		NavWidth:        m.navWidth,
		ResultsWidth:    m.resultsWidth,
	})
}

// restoreSession applies a saved entry after a project is opened: it restores
// the left-pane layout, and (for a project opened without an explicit file)
// picks the last-open chapter when it still exists on disk. It returns the file
// path to restore the cursor for (empty if none), so OpenPath can reposition the
// cursor after it loads that file.
func (m *Model) restoreSession() string {
	entry, ok := session.Load(m.sessionKey())
	if !ok {
		return ""
	}
	if mode, valid := leftPaneFromLabel(entry.LeftPane); valid {
		m.leftPaneMode = mode
	}
	// Older entries did not persist widths/visibility; zero widths identify that
	// shape and retain the historical visible-left default.
	if entry.NavWidth != 0 {
		m.leftPaneVisible = entry.LeftVisible
		m.navWidth = entry.NavWidth
	}
	if entry.ResultsWidth != 0 {
		m.resultsWidth = entry.ResultsWidth
	}
	m.distractionFree = entry.DistractionFree
	if entry.File != "" {
		canonical, err := fsutil.ContainedPath(m.project.Root, entry.File, false)
		if err != nil {
			// A stale or malicious session must never escape the newly opened root.
			entry.File = ""
		} else {
			entry.File = canonical
		}
	}
	// Prefer the last-open file when the caller didn't request a specific one and
	// the remembered file is still present.
	if m.project.IsProject && m.project.CurrentFile == "" && entry.File != "" {
		if info, err := os.Stat(entry.File); err == nil && info.Mode().IsRegular() {
			m.project.SetCurrentFile(entry.File)
		}
	}
	return entry.File
}

// leftPaneLabel/leftPaneFromLabel map the left-pane layout enum to a stable
// string for the sessions file.
func leftPaneLabel(mode LeftPaneMode) string {
	switch mode {
	case PaneModeProject:
		return "project"
	case PaneModeState:
		return "state"
	default:
		return "outline"
	}
}

func leftPaneFromLabel(s string) (LeftPaneMode, bool) {
	switch s {
	case "outline":
		return PaneModeOutline, true
	case "project":
		return PaneModeProject, true
	case "state":
		return PaneModeState, true
	default:
		return PaneModeOutline, false
	}
}
