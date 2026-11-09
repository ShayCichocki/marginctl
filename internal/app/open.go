package app

import (
	"github.com/ShayCichocki/marginctl/internal/editor"
	"github.com/ShayCichocki/marginctl/internal/fsutil"
	"github.com/ShayCichocki/marginctl/internal/llmcmd"
	"github.com/ShayCichocki/marginctl/internal/outline"
	"github.com/ShayCichocki/marginctl/internal/project"
	"github.com/ShayCichocki/marginctl/internal/projectnav"
	"github.com/ShayCichocki/marginctl/internal/results"
	"github.com/ShayCichocki/marginctl/internal/session"
	"github.com/ShayCichocki/marginctl/internal/state"
	"github.com/ShayCichocki/marginctl/internal/statenav"
)

func (m *Model) OpenPath(path string) error {
	// Build every project-scoped component off to the side. No live field changes
	// until project discovery, state discovery, session validation, file reads,
	// fingerprinting, notes, and recovery detection have all completed.
	staged := *m
	staged.projectGen = m.projectGen + 1
	staged.project = project.New()
	staged.state = nil
	staged.projectnav = projectnav.New()
	staged.statenav = statenav.New()
	staged.outline = outline.New()
	staged.editor = editor.NewWithStyles(m.styles)
	staged.results = results.New()
	staged.focus = FocusEditor
	staged.clearPendingAction()
	staged.recovery = recoveryState{}
	staged.lastSwapContent = ""
	staged.lastSwapWritten = false
	staged.loadedFingerprint = fsutil.Fingerprint{}
	staged.leftPaneMode = PaneModeOutline
	staged.llmCancel = nil
	staged.llmLoading = false
	staged.llmStatus = ""
	staged.streamCh = nil
	staged.streamBuf = ""
	staged.llmReqID = m.llmReqID + 1
	staged.llmOperation = llmcmd.Operation{}

	if err := staged.project.Open(path); err != nil {
		return err
	}

	// Set up project navigator if this is a project
	if staged.project.IsProject {
		chapters := staged.project.ListChapters()
		staged.projectnav.SetChapters(chapters)

		staged.state = state.New(staged.project.Root)
		if err := staged.state.Refresh(); err != nil {
			return err
		}
		staged.statenav.SetStateFilesWithCategories(staged.state.All(), staged.state.Categories())
	}

	// Restore the prior session (layout + last-open file when none was requested).
	// Runs after project setup so sessionKey/state are populated; returns the file
	// whose cursor position should be restored once it's loaded.
	sessionFile := staged.restoreSession()
	if !staged.project.IsProject {
		// Project/state panes have no meaning in loose-file or unnamed mode.
		staged.leftPaneMode = PaneModeOutline
	}

	// If no file specified (and none restored) but we have chapters, load the first.
	if staged.project.IsProject && staged.project.CurrentFile == "" {
		if chapters := staged.project.ListChapters(); len(chapters) > 0 {
			staged.project.SetCurrentFile(chapters[0].Path)
		}
	}

	// If a specific file was opened (or auto-selected), load it
	if staged.project.CurrentFile != "" {
		path := staged.project.CurrentFile
		if err := staged.loadIntoEditor(path); err != nil {
			return err
		}
		staged.projectnav.SetCurrentFile(path)

		// Restore the saved cursor position for the file we left off in.
		if sessionFile != "" && sessionFile == path {
			if entry, ok := session.Load(staged.sessionKey()); ok {
				staged.editor.SetCursorPosition(entry.CursorRow, entry.CursorCol)
			}
		}

		staged.recovery = detectRecovery(path, "")
	} else {
		staged.recovery = detectRecovery("", staged.unnamedBufferID)
	}
	if staged.recovery.present {
		// Informational prompt, not a failure (see New): non-error styling keeps it
		// off the red error-promotion path so it can't stick until :ack.
		staged.setNotice("Unsaved changes recovered from a previous session — :recover to restore, :discard to delete")
	}

	// Non-project launch with no file: a bare empty buffer is otherwise a dead
	// end. Point the user at how to name/save it (save-as works via :w <path>).
	// Only when no other notice (first-run LLM hint / recovery) is showing.
	if !staged.project.IsProject && staged.project.CurrentFile == "" && staged.notice == "" {
		staged.setNotice("Empty buffer — type to write, :w <path> to save as a file, :help for commands")
	}

	// The old session is persisted only after the replacement is known-good.
	m.cancelLLM()
	m.saveSession()
	*m = staged
	m.updateLayout()
	m.updateStatusBar()
	return nil
}

// loadIntoEditor reads path and makes it the live editor buffer, keeping the
// core invariant that the editor buffer always corresponds to CurrentFile.
// Every site that replaces the buffer with a real file's contents must go
// through here so a later :w can never write to the wrong file.
func (m *Model) loadIntoEditor(path string) error {
	loaded, err := m.persistence.load(path)
	if err != nil {
		return err
	}
	m.applyLoadedFile(loaded)
	return nil
}

func (m *Model) applyLoadedFile(loaded loadResult) {
	path, content := loaded.path, loaded.content
	m.project.SetCurrentFile(path)
	m.editor.SetContent(content)
	m.parseOutlineNow(content)
	m.bufferGen++ // new buffer identity — invalidates any in-flight LLM diff
	if op := m.results.Operation(); op.RequestID != 0 && (op.SourceFile != path || op.BufferRevision != m.bufferGen) {
		m.results.MarkStale()
		m.results.InvalidateAction()
		m.clearPendingAction()
	}
	m.loadedFingerprint = loaded.fingerprint
	// The swap file (if any) tracks the previous buffer; a fresh load starts
	// its own autosave lineage.
	m.lastSwapContent = ""
	m.lastSwapWritten = false
	m.recovery = detectRecovery(path, "")
	// Load this file's range-anchored notes (re-anchored against the content).
	m.loadNotes(path)
}

// refreshProjectNav re-reads the project's chapter list from disk and pushes it
// into the project navigator. A read error is surfaced but non-fatal (the nav
// keeps its prior contents).
func (m *Model) refreshProjectNav() {
	if err := m.tryRefreshProjectNav(); err != nil {
		m.err = err
	}
}

// tryRefreshProjectNav is refreshProjectNav that returns the error instead of
// recording it, for callers where a refresh failure must not read as their own.
func (m *Model) tryRefreshProjectNav() error {
	if err := m.project.Refresh(); err != nil {
		return err
	}
	m.projectnav.SetChapters(m.project.ListChapters())
	m.projectnav.SetCurrentFile(m.project.CurrentFile)
	return nil
}

// refreshStateNav re-scans the state/ directory and pushes the result into the
// state navigator.
func (m *Model) refreshStateNav() {
	if m.state == nil {
		return
	}
	if err := m.state.Refresh(); err != nil {
		m.err = err
		return
	}
	m.statenav.SetStateFilesWithCategories(m.state.All(), m.state.Categories())
}
