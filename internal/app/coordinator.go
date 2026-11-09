package app

import (
	"errors"
	"os"
	"path/filepath"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ShayCichocki/marginctl/internal/fsutil"
)

// persistenceCoordinator is the only app-level gateway for durable document
// reads and writes. It centralizes canonical identity, optimistic conflicts,
// backups, and fingerprints; root result handling owns buffer synchronization,
// notes, recovery, swaps, and notifications.
type persistenceCoordinator struct{}

// writeExternalDocument applies a non-editor write while preserving the open
// buffer contract. A dirty buffer targeting the same file is a hard conflict;
// a clean one is reloaded and receives the new fingerprint.
func (m *Model) writeExternalDocument(path, content string, expected fsutil.Fingerprint) error {
	canonical, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	// Every caller writes state documents (LLM state proposals, the voice
	// guide), so confine writes to <root>/state: a crafted target must not be
	// able to reach chapters or anything else in the project. ContainedPath
	// also rejects symlinked components.
	if m.project != nil && m.project.Root != "" {
		stateDir := filepath.Join(m.project.Root, "state")
		if cerr := fsutil.RejectSymlinkDir(stateDir); cerr != nil {
			return cerr
		}
		contained, cerr := fsutil.ContainedPath(stateDir, canonical, true)
		if cerr != nil {
			return cerr
		}
		if contained == filepath.Clean(stateDir) {
			return errors.New("refusing to write the state directory itself")
		}
		canonical = contained
	}
	current, _ := filepath.Abs(m.project.CurrentFile)
	if canonical == current && m.editor.Dirty {
		return errors.New("unsaved changes conflict with state update; save or discard first")
	}
	res, err := m.persistence.save(saveRequest{path: canonical, content: content, expected: expected, create: !expected.Exists})
	if err != nil {
		return err
	}
	if canonical == current {
		m.editor.SetContent(content)
		m.loadedFingerprint = res.fingerprint
		m.bufferGen++
	}
	return nil
}

type loadResult struct {
	path        string
	content     string
	fingerprint fsutil.Fingerprint
}

type saveRequest struct {
	path, content string
	force, create bool
	expected      fsutil.Fingerprint
}

type saveResult struct {
	path        string
	content     string
	fingerprint fsutil.Fingerprint
}

func (p persistenceCoordinator) load(path string) (loadResult, error) {
	canonical, err := filepath.Abs(path)
	if err != nil {
		return loadResult{}, err
	}
	data, err := os.ReadFile(canonical)
	if err != nil {
		return loadResult{}, err
	}
	fp, err := fsutil.FileFingerprint(canonical)
	return loadResult{path: canonical, content: string(data), fingerprint: fp}, err
}

func (p persistenceCoordinator) save(req saveRequest) (saveResult, error) {
	canonical, err := filepath.Abs(req.path)
	if err != nil {
		return saveResult{}, err
	}
	if !req.force {
		current, fpErr := fsutil.FileFingerprint(canonical)
		if req.create {
			if fpErr == nil && current.Exists {
				return saveResult{}, errSaveAsExists
			}
			if fpErr != nil && !errors.Is(fpErr, os.ErrNotExist) {
				return saveResult{}, fpErr
			}
		} else if fpErr != nil || !current.Equal(req.expected) {
			return saveResult{}, errExternalChange
		}
	}
	if err := fsutil.WriteFileWithBackup(canonical, []byte(req.content), 0644); err != nil {
		return saveResult{}, err
	}
	fp, err := fsutil.FileFingerprint(canonical)
	return saveResult{path: canonical, content: req.content, fingerprint: fp}, err
}

type fsResultMsg struct {
	projectGen uint64
	fsGen      uint64
	kind       string
	value      any
	err        error
}

func (m *Model) nextFSGeneration() (uint64, uint64) {
	m.fsGen++
	return m.projectGen, m.fsGen
}

func fsCommand(projectGen, fsGen uint64, kind string, work func() (any, error)) tea.Cmd {
	return func() tea.Msg {
		value, err := work()
		return fsResultMsg{projectGen: projectGen, fsGen: fsGen, kind: kind, value: value, err: err}
	}
}

// saveCommand implements :w and :wq. An explicit path argument acts as save-as;
// otherwise the current file is used. When there is no target at all (unnamed
// buffer), it opens the save-as prompt rather than silently doing nothing — so
// :wq can never quit while discarding unsaved content.
func (m *Model) saveCommand(arg string, quit, force bool) tea.Cmd {
	target := arg
	if target == "" {
		target = m.project.CurrentFile
	}

	if target == "" {
		// Nothing named yet — prompt for a path, deferring the write (and any
		// quit) until the user supplies one.
		m.setPrompt(promptPath)
		m.pathBuffer = ""
		if quit && force {
			m.pathInputMode = "savequitforce"
		} else if quit {
			m.pathInputMode = "savequit"
		} else if force {
			m.pathInputMode = "saveforce"
		} else {
			m.pathInputMode = "save"
		}
		return nil
	}

	if err := m.saveBufferTo(target, force); err != nil {
		m.err = err
		return nil
	}
	if quit {
		m.saveSession()
		return tea.Quit
	}
	return nil
}

// saveBufferTo writes the live buffer to path, then makes path the current file
// so the buffer/CurrentFile invariant holds and a later :w targets the same
// file. Used by :w, :wq, and the save-as prompt.
func (m *Model) saveBufferTo(path string, force bool) error {
	_, _, previousSwap := m.currentRecoveryIdentity()
	recoverySwap := ""
	if m.recovery.present && m.recoveryMatchesCurrent() {
		recoverySwap = m.recovery.swapPath
	}
	// Preserve the project's no-symlink policy by validating the path exactly as
	// entered before canonical identity classification can resolve an alias.
	if m.project.IsProject {
		if _, err := fsutil.ContainedPath(m.project.Root, path, true); err != nil {
			return err
		}
	}

	target, err := fsutil.FileFingerprint(path)
	if err != nil {
		return err
	}
	current := target.CanonicalPath == m.loadedFingerprint.CanonicalPath || target.SameFile(m.loadedFingerprint)
	if !force {
		switch {
		case current && !target.Equal(m.loadedFingerprint):
			return errExternalChange
		case !current && target.Exists:
			return errSaveAsExists
		}
	}
	if current {
		// Write the loaded pathname, not a symlink/hardlink alias supplied to :w.
		path = m.loadedFingerprint.CanonicalPath
	} else {
		path = target.CanonicalPath
	}

	if m.project.IsProject {
		err = m.project.SaveFile(path, m.editor.Content())
	} else {
		// Loose and unnamed buffers may be saved anywhere; project saves remain
		// constrained to the canonical project root by Project.SaveFile.
		err = fsutil.WriteFileWithBackup(path, []byte(m.editor.Content()), 0644)
	}
	if err != nil {
		return err
	}
	writtenFingerprint, err := fsutil.FileFingerprint(path)
	if err != nil {
		return err
	}
	m.project.SetCurrentFile(path)
	var refreshErr error
	if m.project.IsProject && !current {
		// A save-as to a new path can create a new chapter; rediscover chapters
		// (through the same containment and symlink rules) so compile/word count
		// see it. A plain save of the open file changes no chapter list.
		refreshErr = m.tryRefreshProjectNav()
	}
	m.projectnav.SetCurrentFile(path)
	m.editor.MarkClean()
	m.loadedFingerprint = writtenFingerprint
	// The buffer is now safely on disk — drop the crash-recovery swap so it
	// can't trigger a spurious recovery prompt next time.
	if previousSwap != "" {
		_ = os.Remove(previousSwap)
	}
	if recoverySwap != "" && recoverySwap != previousSwap {
		_ = os.Remove(recoverySwap)
	}
	m.lastSwapContent = ""
	m.lastSwapWritten = false
	m.recovery = recoveryState{}
	// Persist notes with the file so their (possibly shifted) anchors are saved.
	m.saveNotes()
	m.setNotice("Saved " + m.project.RelativePath(path))
	if refreshErr != nil {
		// The write succeeded; only the chapter list is stale.
		m.setNotice("Saved " + m.project.RelativePath(path) + " (chapter list not refreshed: " + refreshErr.Error() + ")")
	}
	return nil
}
