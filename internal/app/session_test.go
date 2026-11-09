package app

import (
	"fmt"
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// TestRestoredCursorAtEndDoesNotRenderEmpty reproduces the "loads looking empty"
// bug: OpenPath restores the cursor before the first WindowSizeMsg (editor height
// still 0). A restored end-of-file cursor must not leave the viewport scrolled
// past the content once the real size arrives — the editor should show the doc,
// not a screen of ~.
func TestRestoredCursorAtEndDoesNotRenderEmpty(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root, ch1, _ := newTestProject(t)

	// A multi-line chapter; leave the cursor on the last line (as if the user was
	// writing at the end), then save the session and quit.
	var b strings.Builder
	for i := 1; i <= 30; i++ {
		fmt.Fprintf(&b, "Line %d of the chapter.\n", i)
	}
	if err := os.WriteFile(ch1, []byte(b.String()), 0644); err != nil {
		t.Fatal(err)
	}

	m := New()
	if err := m.OpenPath(root); err != nil {
		t.Fatalf("OpenPath: %v", err)
	}
	if err := m.loadIntoEditor(ch1); err != nil {
		t.Fatalf("loadIntoEditor: %v", err)
	}
	last := len(strings.Split(strings.TrimRight(b.String(), "\n"), "\n")) - 1
	m.editor.SetCursorPosition(last, 0)
	m.saveSession()

	// Reopen: OpenPath runs with editor height 0 (no WindowSizeMsg yet)...
	m2 := New()
	if err := m2.OpenPath(ch1); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	// ...then the terminal size arrives.
	next, _ := m2.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m2 = asModel(next)

	out := m2.editorView()
	plain := stripANSI(out)
	// The editor must show real content, not only ~ padding rows...
	if !strings.Contains(plain, "Line ") {
		t.Fatalf("editor rendered no content after restore+resize:\n%s", plain)
	}
	// ...and specifically the line the cursor was restored to (row 29 = "Line 30"),
	// so the viewport is anchored where the user left off rather than at the top
	// with the cursor scrolled off. Requires SetSize to re-run ensureCursorVisible.
	if !strings.Contains(plain, "Line 30 of the chapter.") {
		t.Fatalf("restored cursor line not visible after resize:\n%s", plain)
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	inEsc := false
	for _, r := range s {
		switch {
		case r == '\x1b':
			inEsc = true
		case inEsc && (r == 'm'):
			inEsc = false
		case inEsc:
			// consume
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// TestSessionRestoresLastFileAndCursor is the end-to-end promise of 5.3:
// quitting remembers the open file, cursor, and layout; reopening restores them.
func TestSessionRestoresLastFileAndCursor(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root, ch1, ch2 := newTestProject(t)

	// Session 1: open the project (auto-selects ch1), switch to ch2, move the
	// cursor into the body, change the layout, then quit cleanly with :q.
	m := New()
	if err := m.OpenPath(root); err != nil {
		t.Fatalf("OpenPath: %v", err)
	}
	if err := m.loadIntoEditor(ch2); err != nil {
		t.Fatalf("loadIntoEditor ch2: %v", err)
	}
	m.editor.SetCursorPosition(2, 3) // row 2 = "World"
	m.leftPaneMode = PaneModeState
	m.saveSession()

	// Session 2: reopening the same project root restores ch2 + cursor + layout.
	m2 := New()
	if err := m2.OpenPath(root); err != nil {
		t.Fatalf("reopen OpenPath: %v", err)
	}
	if m2.project.CurrentFile != ch2 {
		t.Fatalf("CurrentFile = %s, want ch2 %s", m2.project.CurrentFile, ch2)
	}
	if row, col := m2.editor.CursorPosition(); row != 2 || col != 3 {
		t.Fatalf("cursor = (%d,%d), want (2,3)", row, col)
	}
	if m2.leftPaneMode != PaneModeState {
		t.Fatalf("leftPaneMode = %v, want PaneModeState", m2.leftPaneMode)
	}
	_ = ch1
}

// A remembered file that has since been deleted must not break reopen; the app
// falls back to the first chapter.
func TestSessionFallsBackWhenFileMissing(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root, ch1, ch2 := newTestProject(t)

	m := New()
	if err := m.OpenPath(root); err != nil {
		t.Fatalf("OpenPath: %v", err)
	}
	if err := m.loadIntoEditor(ch2); err != nil {
		t.Fatalf("loadIntoEditor ch2: %v", err)
	}
	m.saveSession()

	// Delete the remembered file.
	if err := os.Remove(ch2); err != nil {
		t.Fatalf("remove ch2: %v", err)
	}

	m2 := New()
	if err := m2.OpenPath(root); err != nil {
		t.Fatalf("reopen OpenPath: %v", err)
	}
	if m2.project.CurrentFile != ch1 {
		t.Fatalf("CurrentFile = %s, want fallback to ch1 %s", m2.project.CurrentFile, ch1)
	}
}

// An explicit file argument is honored even when the session remembers another
// file, and its own saved cursor is restored.
func TestSessionExplicitFileArgHonored(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	root, ch1, ch2 := newTestProject(t)

	// Remember ch2 for this project.
	m := New()
	if err := m.OpenPath(root); err != nil {
		t.Fatalf("OpenPath: %v", err)
	}
	if err := m.loadIntoEditor(ch2); err != nil {
		t.Fatalf("loadIntoEditor ch2: %v", err)
	}
	m.saveSession()

	// Reopen pointing explicitly at ch1 — it must win over the remembered ch2.
	m2 := New()
	if err := m2.OpenPath(ch1); err != nil {
		t.Fatalf("reopen at ch1: %v", err)
	}
	if m2.project.CurrentFile != ch1 {
		t.Fatalf("CurrentFile = %s, want explicit ch1 %s", m2.project.CurrentFile, ch1)
	}
}
