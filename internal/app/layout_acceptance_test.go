package app

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ShayCichocki/marginctl/internal/command"
	"github.com/ShayCichocki/marginctl/internal/llmcmd"
)

func TestResponsiveLayoutInvariants(t *testing.T) {
	for _, w := range []int{40, 56, 72, 92, 120, 180} {
		l := calculateLayout(w, 24, true, true, false, 24, 44)
		if l.LeftTotal+l.EditorTotal+l.ResultsTotal != w {
			t.Fatalf("width %d totals %+v", w, l)
		}
		if l.EditorTotal < minEditorTotal {
			t.Fatalf("width %d editor=%d", w, l.EditorTotal)
		}
		if l.LeftVisible && l.LeftTotal < minNavTotal {
			t.Fatal("nav minimum")
		}
		if l.ResultsVisible && l.ResultsTotal < minResultsTotal {
			t.Fatal("results minimum")
		}
	}
}
func TestDistractionFreeOwnsCanvas(t *testing.T) {
	l := calculateLayout(120, 30, true, true, true, 30, 50)
	if l.LeftVisible || l.ResultsVisible || l.EditorTotal != 120 {
		t.Fatalf("layout=%+v", l)
	}
}

// The results pane hides itself on accept/reject; the layout must reclaim its
// width on the same keypress instead of leaving an empty bordered column.
func TestLayoutReclaimsResultsPaneOnAcceptReject(t *testing.T) {
	for _, key := range []string{"y", "n"} {
		m, _, _ := openTestModel(t)
		m = update(m, tea.WindowSizeMsg{Width: 160, Height: 40})
		m.editor.SetContent("The quick brown fox")
		m.setPendingAction(pendingLLMResult, llmcmd.Operation{}, command.Result{
			Type: command.ResultDiff, OriginalText: "quick", ProposedText: "slow",
			EndCol: 8, StartCol: 4, File: m.project.CurrentFile, BufferGen: m.bufferGen,
		})
		m.focus = FocusResults
		m.editor.Blur()
		m.updateLayout()
		if !m.layout.ResultsVisible {
			t.Fatalf("%s: results pane should be laid out while a diff is pending", key)
		}

		m = update(m, keyRunes(key))
		if m.results.Visible() {
			t.Fatalf("%s: results pane should hide itself", key)
		}
		if m.layout.ResultsVisible {
			t.Fatalf("%s: layout still reserves the results column", key)
		}
		if m.focus != FocusEditor {
			t.Fatalf("%s: focus = %v, want editor", key, m.focus)
		}
	}
}
