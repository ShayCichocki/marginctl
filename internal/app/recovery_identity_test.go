package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// plantSwap writes a swap file for file that is newer than the file itself, as
// a crashed session would have left behind.
func plantSwap(t *testing.T, file, content string) string {
	t.Helper()
	sp := swapPath(file)
	if err := os.WriteFile(sp, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(sp, future, future); err != nil {
		t.Fatal(err)
	}
	return sp
}

func recoveryProject(t *testing.T) (root, ch1, ch2 string) {
	t.Helper()
	isolateAppEnv(t)
	return newTestProject(t)
}

func TestRecoveryOnlyOffersSwapOfTheOpenedFile(t *testing.T) {
	root, ch1, ch2 := recoveryProject(t)
	plantSwap(t, ch2, "ch2 crashed draft")

	m := New()
	if err := m.OpenPath(root); err != nil {
		t.Fatal(err)
	}
	if m.project.CurrentFile != ch1 {
		t.Fatalf("expected ch1 open, got %q", m.project.CurrentFile)
	}
	if m.recovery.present {
		t.Fatalf("ch2's swap offered for ch1: %+v", m.recovery)
	}
	before := m.editor.Content()
	m.notice = ""
	m.executeCommand("recover")
	if m.editor.Content() != before || m.editor.Dirty || !m.noticeIsErr || !strings.Contains(m.notice, "No recovery") {
		t.Fatalf("recover applied foreign swap: content=%q notice=%q", m.editor.Content(), m.notice)
	}
}

func TestRecoveryOfferDoesNotFollowFileSwitch(t *testing.T) {
	root, ch1, ch2 := recoveryProject(t)
	sp1 := plantSwap(t, ch1, "ch1 crashed draft")

	m := New()
	if err := m.OpenPath(root); err != nil {
		t.Fatal(err)
	}
	if !m.recovery.present || m.recovery.swapPath != sp1 {
		t.Fatalf("expected ch1 offer, got %+v", m.recovery)
	}
	if err := m.loadIntoEditor(ch2); err != nil {
		t.Fatal(err)
	}
	if m.recovery.present {
		t.Fatalf("recovery offer for ch1 survived switch to ch2: %+v", m.recovery)
	}
	before := m.editor.Content()
	m.executeCommand("recover")
	m.executeCommand("discard")
	if m.editor.Content() != before || m.editor.Dirty {
		t.Fatal("recover/discard after switch touched the buffer")
	}
	if got, err := os.ReadFile(sp1); err != nil || string(got) != "ch1 crashed draft" {
		t.Fatalf("ch1 swap disturbed by commands on ch2: err=%v got=%q", err, got)
	}
}

// A stale offer that somehow outlives the buffer it was made for (identity no
// longer matches) is refused by both :recover and :discard and nothing on disk
// is removed.
func TestRecoveryStaleOfferRefusedByRecoverAndDiscard(t *testing.T) {
	root, ch1, ch2 := recoveryProject(t)
	sp1 := plantSwap(t, ch1, "ch1 crashed draft")
	sp2 := plantSwap(t, ch2, "ch2 crashed draft")

	m := New()
	if err := m.OpenPath(root); err != nil {
		t.Fatal(err)
	}
	offer := m.recovery
	if !offer.present || offer.swapPath != sp1 {
		t.Fatalf("setup: %+v", offer)
	}
	// Switch buffers without going through applyLoadedFile, then re-inject the old offer.
	if err := m.loadIntoEditor(ch2); err != nil {
		t.Fatal(err)
	}
	m.recovery = offer
	before := m.editor.Content()

	m.notice, m.noticeIsErr = "", false
	m.executeCommand("recover")
	if m.editor.Content() != before || !m.noticeIsErr || !strings.Contains(m.notice, "different buffer") || m.recovery.applied {
		t.Fatalf("recover: content=%q notice=%q applied=%v", m.editor.Content(), m.notice, m.recovery.applied)
	}

	m.notice, m.noticeIsErr = "", false
	m.executeCommand("discard")
	if !m.noticeIsErr || !strings.Contains(m.notice, "different buffer") {
		t.Fatalf("discard notice = %q", m.notice)
	}
	for _, sp := range []string{sp1, sp2} {
		if !fileExists(sp) {
			t.Fatalf("swap %s removed by refused discard", sp)
		}
	}
}

func TestRecoveryOfferWithMismatchedSwapPathOrBufferIDIsRefused(t *testing.T) {
	root, ch1, ch2 := recoveryProject(t)
	m := New()
	if err := m.OpenPath(root); err != nil {
		t.Fatal(err)
	}
	canonical := m.loadedFingerprint.CanonicalPath
	cases := map[string]recoveryState{
		"other file":                 {present: true, canonicalFile: ch2, swapPath: swapPath(ch2), content: "x"},
		"right file wrong swap path": {present: true, canonicalFile: canonical, swapPath: swapPath(ch2), content: "x"},
		"unnamed id on named buffer": {present: true, canonicalFile: canonical, bufferID: "abc", swapPath: swapPath(ch1), content: "x"},
	}
	for name, rec := range cases {
		rec := rec
		t.Run(name, func(t *testing.T) {
			m := m
			m.recovery = rec
			before := m.editor.Content()
			m.executeCommand("recover")
			if m.editor.Content() != before || m.recovery.applied {
				t.Fatal("mismatched offer applied")
			}
			sp := swapPath(ch2)
			_ = os.WriteFile(sp, []byte("keep me"), 0600)
			m.executeCommand("discard")
			if data, err := os.ReadFile(sp); err != nil || string(data) != "keep me" {
				t.Fatalf("mismatched discard removed a foreign swap: %v", err)
			}
			_ = os.Remove(sp)
		})
	}
}

func TestDiscardRemovesOnlyTheMatchingSwap(t *testing.T) {
	root, ch1, ch2 := recoveryProject(t)
	sp1 := plantSwap(t, ch1, "ch1 draft")
	sp2 := plantSwap(t, ch2, "ch2 draft")

	m := New()
	if err := m.OpenPath(root); err != nil {
		t.Fatal(err)
	}
	m.executeCommand("discard")
	if m.noticeIsErr || fileExists(sp1) {
		t.Fatalf("matching swap not removed: notice=%q exists=%v", m.notice, fileExists(sp1))
	}
	if m.recovery.present {
		t.Fatal("recovery state not cleared after discard")
	}
	if !fileExists(sp2) {
		t.Fatal("discard removed another file's swap")
	}
	// A second discard has nothing to act on.
	m.executeCommand("discard")
	if !m.noticeIsErr || !strings.Contains(m.notice, "No recovery") {
		t.Fatalf("second discard notice = %q", m.notice)
	}
	if !fileExists(sp2) {
		t.Fatal("second discard removed another file's swap")
	}
}

func TestRecoverTwiceIsRefusedAndKeepsRecoveredText(t *testing.T) {
	root, ch1, _ := recoveryProject(t)
	plantSwap(t, ch1, "recovered once")

	m := New()
	if err := m.OpenPath(root); err != nil {
		t.Fatal(err)
	}
	m.executeCommand("recover")
	if m.editor.Content() != "recovered once" || !m.editor.Dirty {
		t.Fatalf("recover: %q dirty=%v", m.editor.Content(), m.editor.Dirty)
	}
	m = dirtyEditor(t, m) // further edits after recovery
	edited := m.editor.Content()
	m = pressEsc(m)
	m.notice, m.noticeIsErr = "", false
	m.executeCommand("recover")
	if m.editor.Content() != edited || !m.noticeIsErr || !strings.Contains(m.notice, "already applied") {
		t.Fatalf("second recover clobbered edits: %q notice=%q", m.editor.Content(), m.notice)
	}
}

func TestSaveAfterRecoveryClearsOnlyThatSwap(t *testing.T) {
	root, ch1, ch2 := recoveryProject(t)
	sp1 := plantSwap(t, ch1, "recovered draft")
	sp2 := plantSwap(t, ch2, "other draft")

	m := New()
	if err := m.OpenPath(root); err != nil {
		t.Fatal(err)
	}
	m.executeCommand("recover")
	m.executeCommand("w")
	if m.err != nil {
		t.Fatalf("save: %v", m.err)
	}
	if data, _ := os.ReadFile(ch1); string(data) != "recovered draft" {
		t.Fatalf("saved content = %q", data)
	}
	if fileExists(sp1) || m.recovery.present {
		t.Fatalf("swap not cleared by save (exists=%v present=%v)", fileExists(sp1), m.recovery.present)
	}
	if !fileExists(sp2) {
		t.Fatal("save removed another file's swap")
	}
}

func TestAutosaveWritesSwapForTheCurrentFileOnly(t *testing.T) {
	_, ch1, ch2 := recoveryProject(t)
	root := filepath.Dir(filepath.Dir(ch1))
	m := New()
	if err := m.OpenPath(root); err != nil {
		t.Fatal(err)
	}
	m = dirtyEditor(t, m)
	m = update(m, autosaveTickMsg{})
	first, err := os.ReadFile(swapPath(ch1))
	if err != nil {
		t.Fatalf("expected ch1 swap: %v", err)
	}

	// Discard the dirty edit by reloading ch2 (a deliberate programmatic switch).
	if err := m.loadIntoEditor(ch2); err != nil {
		t.Fatal(err)
	}
	m = update(m, autosaveTickMsg{})
	if fileExists(swapPath(ch2)) {
		t.Fatal("clean buffer wrote a swap")
	}
	m = dirtyEditor(t, m)
	m = update(m, autosaveTickMsg{})
	second, err := os.ReadFile(swapPath(ch2))
	if err != nil || string(second) != m.editor.Content() {
		t.Fatalf("ch2 swap wrong: err=%v got=%q", err, second)
	}
	if now, _ := os.ReadFile(swapPath(ch1)); string(now) != string(first) {
		t.Fatal("ch2 autosave overwrote ch1's swap")
	}
}

func TestUnresolvedRecoveryOfferIsNotOverwrittenByAutosave(t *testing.T) {
	root, ch1, _ := recoveryProject(t)
	sp := plantSwap(t, ch1, "precious crash artifact")

	m := New()
	if err := m.OpenPath(root); err != nil {
		t.Fatal(err)
	}
	m = dirtyEditor(t, m)
	m = update(m, autosaveTickMsg{})
	if data, _ := os.ReadFile(sp); string(data) != "precious crash artifact" {
		t.Fatalf("autosave clobbered unresolved recovery: %q", data)
	}
}

func TestUnnamedBufferSwapIsNeverOfferedToNamedFile(t *testing.T) {
	isolateAppEnv(t)
	seed := New()
	seed.editor.SetRecoveredContent("unnamed draft", "")
	seed.writeSwap()
	usp := unnamedSwapPath(seed.unnamedBufferID)
	if !fileExists(usp) {
		t.Fatal("setup: unnamed swap missing")
	}

	root, _, _ := newTestProject(t)
	m := New()
	if !m.recovery.present {
		t.Fatal("setup: unnamed recovery not offered to unnamed buffer")
	}
	if err := m.OpenPath(root); err != nil {
		t.Fatal(err)
	}
	if m.recovery.present {
		t.Fatalf("unnamed swap carried onto a named file: %+v", m.recovery)
	}
	before := m.editor.Content()
	m.executeCommand("recover")
	m.executeCommand("discard")
	if m.editor.Content() != before {
		t.Fatal("recover changed named buffer using unnamed swap")
	}
	if !fileExists(usp) {
		t.Fatal("discard on named file removed the unnamed buffer's swap")
	}
}

func TestSymlinkedSwapIsNotOffered(t *testing.T) {
	root, ch1, _ := recoveryProject(t)
	secret := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(secret, []byte("do not load"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, swapPath(ch1)); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	m := New()
	if err := m.OpenPath(root); err != nil {
		t.Fatal(err)
	}
	if m.recovery.present {
		t.Fatalf("symlinked swap offered: %+v", m.recovery)
	}
	m.executeCommand("recover")
	if strings.Contains(m.editor.Content(), "do not load") {
		t.Fatal("symlink target loaded as recovered content")
	}
}
