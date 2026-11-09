package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ShayCichocki/marginctl/internal/editor"
)

// TestComposedViewFitsTerminal: the composed full-screen view must be exactly
// m.height rows and no wider than m.width columns at every terminal size. A
// taller view makes the terminal scroll/clip and the cursor appears to go
// off-screen. Regression for the status-bar-wraps-to-two-rows overflow.
func TestComposedViewFitsTerminal(t *testing.T) {
	sizes := []struct{ w, h int }{
		{80, 24}, {40, 20}, {30, 16}, {120, 40}, {24, 10},
	}
	for _, sz := range sizes {
		m, _, _ := openTestModel(t)
		m.width, m.height = sz.w, sz.h
		m.updateLayout()

		// A long document: big word count + a long chapter path widen the status
		// bar's fields — the exact condition that used to wrap it to two rows.
		m.statusbar.WordCount = 248391
		m.statusbar.Dirty = true
		m.statusbar.FilePath = "chapters/012_a_rather_long_chapter_filename_here.md"
		m.statusbar.Mode = "NORMAL"
		m.updateStatusBar()

		out := m.View()
		if got := lipgloss.Height(out); got != sz.h {
			t.Fatalf("%dx%d: composed view height = %d, want %d", sz.w, sz.h, got, sz.h)
		}
		for i, line := range strings.Split(out, "\n") {
			if lw := lipgloss.Width(line); lw > sz.w {
				t.Fatalf("%dx%d: line %d width %d exceeds %d", sz.w, sz.h, i, lw, sz.w)
			}
		}
	}
}

// TestEditorPaneRendersFullBox: the editor pane must render its whole bordered
// box (content + top and bottom border), so the editor's last content row — and
// a cursor on it — is never clipped. Regression for the MaxHeight-clips-border bug.
func TestEditorPaneRendersFullBox(t *testing.T) {
	for _, sz := range []struct{ w, h int }{{80, 24}, {40, 16}, {120, 40}} {
		m, _, _ := openTestModel(t)
		m.width, m.height = sz.w, sz.h
		m.updateLayout()

		// The editor's bordered box occupies contentHeight = m.height - header - status.
		wantBox := sz.h - 2
		if got := lipgloss.Height(m.editorView()); got != wantBox {
			t.Fatalf("%dx%d: editor box height = %d, want %d (border clipped?)", sz.w, sz.h, got, wantBox)
		}
		// And the editor's own model must render exactly the visible content rows
		// (box minus the two border rows), so its scroll math matches the screen.
		if got := lipgloss.Height(m.editor.View()); got != wantBox-3 {
			t.Fatalf("%dx%d: editor content rows = %d, want %d", sz.w, sz.h, got, wantBox-3)
		}
	}
}

// v2 #52 — below the minimum terminal size, View shows a friendly message sized
// exactly to the terminal instead of a garbled/overflowing multi-pane layout.
func TestTerminalTooSmallGuard(t *testing.T) {
	m, _, _ := openTestModel(t)
	m.width, m.height = 20, 6
	out := m.View()
	if !strings.Contains(out, "too small") {
		t.Fatalf("expected a 'too small' message at 20x6, got:\n%s", out)
	}
	if got := lipgloss.Height(out); got != 6 {
		t.Fatalf("guard view height = %d, want 6", got)
	}
	// At the threshold the normal layout renders (no message).
	m.width, m.height = minTermWidth, minTermHeight
	m.updateLayout()
	if strings.Contains(m.View(), "too small") {
		t.Fatalf("at the minimum size the full layout should render")
	}
}

func TestTerminalTooSmallBlocksHiddenEditing(t *testing.T) {
	m, _, _ := openTestModel(t)
	before := m.editor.Content()
	m.width, m.height = 20, 6
	m = update(m, keyRunes("i"))
	m = update(m, keyRunes("changed"))
	if m.editor.Content() != before || m.editor.Mode != editor.ModeNormal {
		t.Fatalf("hidden edit occurred: mode=%v content=%q", m.editor.Mode, m.editor.Content())
	}
	m = update(m, tea.WindowSizeMsg{Width: 80, Height: 24})
	m = update(m, keyRunes("i"))
	if m.editor.Mode != editor.ModeInsert {
		t.Fatal("editing did not recover after resize")
	}
}

// v2 #55 — an active text prompt (e.g. Save-as) renders over the LLM spinner, so
// a background request can't mask what the user is typing.
func TestBottomBarPromptOutranksSpinner(t *testing.T) {
	m, _, _ := openTestModel(t)
	m.width, m.height = 80, 24
	m.updateLayout()
	m.notice = ""
	m.llmLoading = true
	m.setPrompt(promptPath)
	m.pathInputMode = "save"
	m.pathBuffer = "draft.md"
	if !strings.Contains(m.View(), "Save as:") {
		t.Fatalf("an active path prompt must render over the LLM spinner")
	}
}

// TestStatusBarStaysOneLine: the status bar never spills to a second row, even
// when its fields are wider than the whole bar.
func TestStatusBarStaysOneLine(t *testing.T) {
	for _, w := range []int{20, 30, 40, 80} {
		m, _, _ := openTestModel(t)
		m.width, m.height = w, 24
		m.updateLayout()
		m.statusbar.WordCount = 9999999
		m.statusbar.Dirty = true
		m.statusbar.FilePath = "chapters/some_rather_long_name.md"
		m.statusbar.Mode = "NORMAL"

		bar := m.statusbar.View()
		if h := lipgloss.Height(bar); h != 1 {
			t.Fatalf("width %d: status bar height = %d, want 1", w, h)
		}
		if bw := lipgloss.Width(bar); bw > w {
			t.Fatalf("width %d: status bar width = %d, want <= %d", w, bw, w)
		}
	}
}

func TestDownKeepsCursorInsideComposedEditorPane(t *testing.T) {
	content := strings.Repeat("wrapped-word ", 600) + "\n" + strings.Repeat("short line\n", 1200)
	for _, sz := range []struct{ w, h int }{{minTermWidth, minTermHeight}, {80, 24}, {180, 45}} {
		m := New()
		m.notice = ""
		m.editor.SetContent(content)
		m = update(m, tea.WindowSizeMsg{Width: sz.w, Height: sz.h})
		steps := 1800
		for i := 0; i < steps; i++ {
			m = update(m, tea.KeyMsg{Type: tea.KeyDown})
			cursorRow := m.editor.CursorViewportRow()
			viewportHeight := max(0, m.layout.PaneHeight-1)
			if cursorRow < 0 || cursorRow >= viewportHeight {
				t.Fatalf("%dx%d step %d: cursor viewport row %d outside [0,%d)", sz.w, sz.h, i, cursorRow, viewportHeight)
			}

			view := m.View()
			// Header, pane top border, and editor title precede viewport row zero.
			composedRow := 3 + cursorRow
			rows := strings.Split(view, "\n")
			if composedRow >= len(rows)-1 {
				t.Fatalf("%dx%d step %d: cursor composed row %d clipped from %d-row view", sz.w, sz.h, i, composedRow, len(rows))
			}
			if got := lipgloss.Height(view); got != sz.h {
				t.Fatalf("%dx%d step %d: composed height %d", sz.w, sz.h, i, got)
			}
		}
	}
}
