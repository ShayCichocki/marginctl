package app

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ShayCichocki/marginctl/internal/fsutil"
)

const autosaveInterval = 2 * time.Second

type autosaveTickMsg struct{}

// recoveryState binds an offer to the exact buffer it belongs to. Content is
// separate from presence so an intentionally emptied draft remains recoverable.
type recoveryState struct {
	present       bool
	canonicalFile string
	bufferID      string
	content       string
	swapPath      string
	timestamp     time.Time
	applied       bool
}

func autosaveTick() tea.Cmd {
	return tea.Tick(autosaveInterval, func(time.Time) tea.Msg { return autosaveTickMsg{} })
}

func swapPath(file string) string {
	if file == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(file), "."+filepath.Base(file)+".swp")
}

func dataDir() string {
	if xdg := os.Getenv("XDG_DATA_HOME"); xdg != "" {
		return filepath.Join(xdg, "marginctl")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "share", "marginctl")
}

func loadUnnamedBufferID() string {
	dir := dataDir()
	if dir == "" {
		return ""
	}
	path := filepath.Join(dir, "unnamed-buffer-id")
	if data, err := os.ReadFile(path); err == nil && len(data) > 0 {
		return string(data)
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return ""
	}
	id := hex.EncodeToString(raw[:])
	if err := os.MkdirAll(dir, 0700); err != nil {
		return ""
	}
	if err := fsutil.WriteFilePrivate(path, []byte(id)); err != nil {
		return ""
	}
	return id
}

func unnamedSwapPath(id string) string {
	if id == "" || dataDir() == "" {
		return ""
	}
	return filepath.Join(dataDir(), "recovery", "unnamed-"+id+".swp")
}

func (m *Model) currentRecoveryIdentity() (file, id, path string) {
	if m.project != nil && m.project.CurrentFile != "" {
		// The loaded fingerprint already carries the validated canonical identity.
		// Re-hashing the entire manuscript merely to locate its swap made quit and
		// autosave latency proportional to file size.
		canonical := m.loadedFingerprint.CanonicalPath
		if canonical == "" {
			canonical = filepath.Clean(m.project.CurrentFile)
		}
		return canonical, "", swapPath(canonical)
	}
	return "", m.unnamedBufferID, unnamedSwapPath(m.unnamedBufferID)
}

func (m *Model) writeSwap() {
	if m.project == nil || !m.editor.Dirty {
		return
	}
	// Never overwrite an unresolved crash artifact with edits made while the
	// recovery prompt is still awaiting an explicit decision.
	if m.recovery.present && !m.recovery.applied {
		return
	}
	content := m.editor.Content()
	if m.lastSwapWritten && content == m.lastSwapContent {
		return
	}
	_, _, sp := m.currentRecoveryIdentity()
	if sp == "" {
		return
	}
	if err := os.MkdirAll(filepath.Dir(sp), 0700); err != nil {
		m.err = err
		return
	}
	if err := fsutil.WriteFilePrivate(sp, []byte(content)); err != nil {
		m.err = err
		return
	}
	m.lastSwapContent = content
	m.lastSwapWritten = true
}

func (m *Model) removeSwap() {
	_, _, sp := m.currentRecoveryIdentity()
	if sp != "" {
		_ = os.Remove(sp)
	}
	m.lastSwapContent = ""
	m.lastSwapWritten = false
	m.recovery = recoveryState{}
}

func detectRecovery(file, id string) recoveryState {
	var sp string
	canonical := ""
	if file != "" {
		fp, err := fsutil.FileFingerprint(file)
		if err != nil {
			return recoveryState{}
		}
		canonical = fp.CanonicalPath
		sp = swapPath(canonical)
	} else {
		sp = unnamedSwapPath(id)
	}
	if sp == "" {
		return recoveryState{}
	}
	// Lstat (not Stat): a swap sidecar must be a regular file we wrote. A symlink
	// planted at the swap path (e.g. in a cloned project) must never be followed
	// into an arbitrary file and loaded as "recovered" manuscript content.
	info, err := os.Lstat(sp)
	if err != nil || !info.Mode().IsRegular() {
		return recoveryState{}
	}
	if canonical != "" {
		if fileInfo, err := os.Stat(canonical); err == nil && info.ModTime().Before(fileInfo.ModTime()) {
			return recoveryState{}
		}
	}
	data, err := os.ReadFile(sp)
	if err != nil {
		return recoveryState{}
	}
	return recoveryState{present: true, canonicalFile: canonical, bufferID: id, content: string(data), swapPath: sp, timestamp: info.ModTime()}
}

func (m *Model) recoveryMatchesCurrent() bool {
	file, id, sp := m.currentRecoveryIdentity()
	return m.recovery.present && m.recovery.canonicalFile == file && m.recovery.bufferID == id && m.recovery.swapPath == sp
}

// recoverSwap restores crash-recovered swap contents into the buffer, leaving it
// dirty against the on-disk file so the user can review and :w it.
func (m *Model) recoverSwap() tea.Cmd {
	if !m.recovery.present {
		m.setErrorNotice("No recovery available")
		return nil
	}
	if !m.recoveryMatchesCurrent() {
		m.setErrorNotice("Recovery belongs to a different buffer; refusing to apply")
		return nil
	}
	if m.recovery.applied {
		m.setErrorNotice("Recovery already applied — :w to save or :discard to delete")
		return nil
	}
	orig, _ := m.project.ReadFile(m.project.CurrentFile)
	if m.project.CurrentFile == "" {
		orig = ""
	}
	m.editor.SetRecoveredContent(m.recovery.content, orig)
	m.parseOutlineNow(m.editor.Content())
	m.bufferGen++
	m.recovery.applied = true
	m.setNotice("Recovered unsaved changes — :w to save, u to undo")
	return nil
}

// discardRecovery deletes the recovered swap file and dismisses the offer.
func (m *Model) discardRecovery() tea.Cmd {
	if !m.recovery.present {
		m.setErrorNotice("No recovery available")
		return nil
	}
	if !m.recoveryMatchesCurrent() {
		m.setErrorNotice("Recovery belongs to a different buffer; refusing to discard")
		return nil
	}
	m.removeSwap()
	m.setNotice("Discarded recovery")
	return nil
}
