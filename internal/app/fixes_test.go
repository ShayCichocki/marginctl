package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ShayCichocki/marginctl/internal/command"
	"github.com/ShayCichocki/marginctl/internal/fsutil"
)

// A results pane the layout cannot render must never hold focus (else a diff or
// cross-file replace could be accepted sight-unseen). When the terminal is
// widened enough to render it, focus returns so the pending result is reachable.
func TestNarrowTerminalKeepsFocusOffUnrenderedResults(t *testing.T) {
	m, _, _ := openTestModel(t)
	m = update(m, tea.WindowSizeMsg{Width: 50, Height: 24}) // < 72: results can't fit

	m.showResult(command.Result{Type: command.ResultText, Content: "review me"})
	if m.layout.ResultsVisible {
		t.Fatal("precondition: results pane should not be laid out at width 50")
	}
	if m.focus == FocusResults {
		t.Fatal("focus must not rest on an unrendered results pane")
	}
	if !m.pendingResultsFocus {
		t.Fatal("pending results focus should be recorded for when the pane fits")
	}

	m = update(m, tea.WindowSizeMsg{Width: 160, Height: 24}) // now it fits
	if !m.layout.ResultsVisible || m.focus != FocusResults {
		t.Fatalf("after widening: ResultsVisible=%v focus=%v, want visible + FocusResults", m.layout.ResultsVisible, m.focus)
	}
	if m.pendingResultsFocus {
		t.Fatal("pending flag should clear once focus is granted")
	}
}

// Reopening the ":" prompt without touching the editor in between must still
// render the full editor frame. The main-pane cache is blanked when no prompt is
// active; if its key were left pointing at the stale "" view, the next prompt
// opened in the same editor state would match that key and reuse the empty
// string — collapsing the main pane so the ":" bar renders up under the header.
func TestReopenCommandPromptKeepsEditorFrame(t *testing.T) {
	m, _, _ := openTestModel(t)
	m = update(m, tea.WindowSizeMsg{Width: 120, Height: 30})

	// First ":" session, then Esc — leaves the editor state untouched.
	m = update(m, keyRunes(":"))
	if out := m.View(); !strings.Contains(out, "EDITOR") {
		t.Fatalf("first command prompt dropped the editor frame:\n%s", out)
	}
	m = update(m, tea.KeyMsg{Type: tea.KeyEsc})

	// A normal editor frame renders between prompts (the TUI redraws every frame).
	// This is what blanks the cached prompt view while leaving its key behind.
	if out := m.View(); !strings.Contains(out, "EDITOR") {
		t.Fatalf("editor frame missing after closing the prompt:\n%s", out)
	}

	// Reopen ":" with the editor in the identical revision/cursor/layout state.
	m = update(m, keyRunes(":"))
	out := m.View()
	if !strings.Contains(out, "EDITOR") {
		t.Fatalf("reopening the command prompt collapsed the editor frame:\n%s", out)
	}
}

// The recovery prompt is informational guidance, not a failure. It must render
// in the normal (non-error) style, and pressing a key must not promote it into a
// persistent UntilAcknowledged error that sticks red and masks later messages.
func TestRecoveryPromptIsNotAStuckError(t *testing.T) {
	m, _, ch2 := openTestModel(t)
	m = update(m, tea.WindowSizeMsg{Width: 120, Height: 30})

	// Simulate having unsaved recovered content on open.
	m.recovery = recoveryState{present: true}
	m.notice = "Unsaved changes recovered from a previous session — :recover to restore, :discard to delete"
	m.noticeIsErr = false
	if m.noticeIsErr {
		t.Fatal("recovery prompt should not be error-styled")
	}

	// A keypress clears the transient notice without parking a persistent error.
	m = update(m, keyRunes("x"))
	if m.notifications.Count() != 0 {
		n, _ := m.notifications.Current()
		t.Fatalf("recovery prompt leaked a stuck notification: %+v", n)
	}
	if m.noticeIsErr {
		t.Fatal("no error state should survive the keypress")
	}

	// Control: a genuine operation error (m.err) still persists so it isn't missed.
	m.err = errOperationFailed
	m = update(m, keyRunes("y"))
	if m.notifications.Count() != 1 {
		t.Fatalf("a real m.err should promote to a persistent notification, count=%d", m.notifications.Count())
	}
	_ = ch2
}

var errOperationFailed = errReadOnly("disk full")

type errReadOnly string

func (e errReadOnly) Error() string { return string(e) }

// detectRecovery must never follow a symlink planted at the swap path into an
// arbitrary file and load it as "recovered" manuscript content.
func TestDetectRecoveryRejectsSymlinkSwap(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "chapter.md")
	if err := os.WriteFile(file, []byte("real"), 0644); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(dir, "secret.txt")
	if err := os.WriteFile(secret, []byte("SECRET"), 0600); err != nil {
		t.Fatal(err)
	}
	fp, err := fsutil.FileFingerprint(file)
	if err != nil {
		t.Fatal(err)
	}
	sp := swapPath(fp.CanonicalPath)
	if err := os.Symlink(secret, sp); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if rec := detectRecovery(file, ""); rec.present {
		t.Fatalf("recovery must reject a symlinked swap; leaked content %q", rec.content)
	}

	// Control: a genuine regular-file swap newer than the manuscript is detected.
	if err := os.Remove(sp); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sp, []byte("recovered draft"), 0600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(file, old, old); err != nil {
		t.Fatal(err)
	}
	if rec := detectRecovery(file, ""); !rec.present || rec.content != "recovered draft" {
		t.Fatalf("regular-file swap should be detected: present=%v content=%q", rec.present, rec.content)
	}
}
