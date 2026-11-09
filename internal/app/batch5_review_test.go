package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ShayCichocki/marginctl/internal/command"
	"github.com/ShayCichocki/marginctl/internal/llm"
	"github.com/ShayCichocki/marginctl/internal/llmcmd"
)

func key(m Model, s string) Model { return update(m, keyRunes(s)) }

// streamingModel starts a real report stream (as handleStreamStart does) and
// returns the model, its operation, and the stream channel.
func streamingModel(t *testing.T) (Model, llmcmd.Operation) {
	t.Helper()
	isolateAppEnv(t)
	m, _, _ := openTestModel(t)
	m = update(m, tea.WindowSizeMsg{Width: 140, Height: 40})
	m.llmLoading, m.llmReqID = true, 1
	op := llmcmd.Operation{RequestID: 1, ProjectRoot: m.project.Root, SourceFile: m.project.CurrentFile, BufferRevision: m.bufferGen}
	m = update(m, llmcmd.StreamStartMsg{ReqID: 1, Operation: op, Ch: make(chan llm.StreamEvent)})
	for i := 0; i < 200; i++ {
		m = update(m, streamDeltaMsg{operation: op, text: fmt.Sprintf("line %d\n", i)})
	}
	if !m.results.Streaming() {
		t.Fatal("stream should be live")
	}
	// Focus can leave the pane (for example a layout bounce); the user then
	// runs a command that replaces it.
	m.focus = FocusEditor
	m.editor.Focus()
	return m, op
}

func TestStreamDeltaAfterPaneReplacedDoesNotPanicOrGarble(t *testing.T) {
	m, op := streamingModel(t)
	m = runColon(m, "WordCount")
	if !strings.Contains(m.results.View(), "Manuscript:") {
		t.Fatalf("word count not shown:\n%s", m.results.View())
	}
	before := m.results.View()
	m = update(m, streamDeltaMsg{operation: op, text: "late token\n"})
	after := m.results.View()
	if after != before || strings.Contains(after, "late token") {
		t.Fatalf("foreign content was modified:\n%s", after)
	}
	if m.llmLoading || m.llmReqID == 1 || m.llmCancel != nil {
		t.Fatalf("stream not cancelled: loading=%v req=%d", m.llmLoading, m.llmReqID)
	}
	if !strings.Contains(m.notice, "results pane was replaced") {
		t.Fatalf("notice = %q", m.notice)
	}
	// Further tokens from the cancelled stream are dropped.
	m = update(m, streamDeltaMsg{operation: op, text: "more"})
	if m.results.View() != before {
		t.Fatal("cancelled stream still mutates the pane")
	}
}

func TestStreamEndAfterPaneReplacedDoesNotTouchPane(t *testing.T) {
	m, op := streamingModel(t)
	m = runColon(m, "WordCount")
	before := m.results.View()
	m = update(m, streamEndMsg{operation: op, text: "tail", err: llm.ErrTruncated})
	if m.results.View() != before {
		t.Fatalf("stream end overwrote foreign content:\n%s", m.results.View())
	}
	if m.llmLoading || m.llmReqID == 1 {
		t.Fatal("stream not cancelled")
	}
	if !strings.Contains(m.notice, "results pane was replaced") {
		t.Fatalf("notice = %q", m.notice)
	}
}

func TestShowActionResetsStreamBookkeeping(t *testing.T) {
	m, _ := streamingModel(t)
	m.results.Show(command.Result{Type: command.ResultText, Content: "short"})
	// Even a direct append (a bug elsewhere) must not panic on stale bookkeeping.
	m.results.AppendStreamText("x\ny")
}

// proposalPane returns a model with a pending two-proposal batch on screen.
func proposalPane(t *testing.T) Model {
	t.Helper()
	m, _, _ := pendingModel(t)
	m = update(m, tea.WindowSizeMsg{Width: 200, Height: 40})
	showTestProposals(t, &m,
		command.StateProposal{Category: "characters", Name: "Zed", Action: "create", Summary: "new", Proposed: "# Zed\n"},
		command.StateProposal{Category: "characters", Name: "Yan", Action: "create", Summary: "other", Proposed: "# Yan\n"},
	)
	return m
}

func TestResultsColonRefusesNonProposalVerbs(t *testing.T) {
	for _, cmd := range []string{"help", "WordCount", "Tighten", "search x", "Compile"} {
		m := proposalPane(t)
		id := m.pendingAction.ID
		m = runColon(m, cmd)
		if !strings.Contains(m.notice, "Only :proposal-*, :proposals, :w and :q here") {
			t.Fatalf(":%s notice = %q", cmd, m.notice)
		}
		if m.pendingAction == nil || m.pendingAction.ID != id || len(m.pendingAction.Proposals) != 2 ||
			m.focus != FocusResults || !strings.Contains(m.results.View(), "1. create characters/Zed") {
			t.Fatalf(":%s disturbed the batch", cmd)
		}
		if m.commandFromResults {
			t.Fatal("results-origin flag not reset after the prompt closed")
		}
	}
	// Allowed verbs still work, and the flag does not leak into the editor prompt.
	m := proposalPane(t)
	m = runColon(m, "proposal-skip 1")
	if len(m.pendingAction.Proposals) != 1 {
		t.Fatal("proposal-skip refused")
	}
	m = runColon(m, "proposals")
	if m.notice != "" && strings.Contains(m.notice, "Only") {
		t.Fatalf(":proposals refused: %q", m.notice)
	}
	m = pressEsc(m) // leave the pane
	m = runColon(m, "help")
	if !strings.Contains(m.results.View(), "Editor") && !strings.Contains(m.results.View(), "help") {
		t.Fatalf("editor-origin :help should run:\n%s", m.results.View())
	}
}

func TestResultsColonEscResetsOriginFlag(t *testing.T) {
	m := proposalPane(t)
	m = key(m, ":")
	if !m.commandFromResults {
		t.Fatal("origin not tracked")
	}
	m = pressEsc(m)
	if m.commandFromResults || m.pendingAction == nil {
		t.Fatal("esc must reset the flag and keep the batch")
	}
}

func TestColonNotOpenFromNonProposalResultsPanes(t *testing.T) {
	// A :replace confirm.
	m, _, _ := pendingModel(t)
	m = update(m, tea.WindowSizeMsg{Width: 200, Height: 40})
	m.executeCommand("replace Hello/Howdy")
	if m.pendingAction == nil || m.pendingAction.Kind != pendingReplace || m.focus != FocusResults {
		t.Fatalf("replace confirm not shown: %+v", m.pendingAction)
	}
	if m = key(m, ":"); m.activePrompt() != promptNone {
		t.Fatal(": must not open over a replace confirm")
	}
	// An LLM diff.
	m, _, _ = pendingModel(t)
	m = update(m, tea.WindowSizeMsg{Width: 200, Height: 40})
	armDiff(&m)
	m.focus = FocusResults
	if m = key(m, ":"); m.activePrompt() != promptNone {
		t.Fatal(": must not open over an LLM diff")
	}
	// Search results.
	m, _, _ = pendingModel(t)
	m = update(m, tea.WindowSizeMsg{Width: 200, Height: 40})
	m.showResult(command.Result{Type: command.ResultList, Items: []string{"a"}})
	if m = key(m, ":"); m.activePrompt() != promptNone {
		t.Fatal(": must not open over search results")
	}
}

func TestProposalReviewHelpEscDoesNotResurrectList(t *testing.T) {
	m := proposalPane(t)
	m = runColon(m, "proposal-review 1")
	if !m.reviewingProposal() {
		t.Fatal("review not active")
	}
	m = key(m, "y") // accepts proposal 1; list refreshes with the rest
	if m.reviewingProposal() || len(m.pendingAction.Proposals) != 1 {
		t.Fatalf("y should accept the reviewed proposal: %+v", m.pendingAction)
	}
	m = runColon(m, "proposal-review 1")
	// Another command replaces the pane (the prompt guard is bypassed on purpose).
	m.executeCommand("help")
	if m.reviewingProposal() {
		t.Fatal("review flag must end when the pane is replaced")
	}
	m = pressEsc(m)
	if m.results.Visible() || m.pendingAction != nil || m.focus != FocusEditor {
		t.Fatalf("esc should close help without resurrecting the list: visible=%v pending=%v", m.results.Visible(), m.pendingAction)
	}
	if m.proposalReviewN != 0 {
		t.Fatal("review state leaked")
	}
}

func TestProposalReviewKeys(t *testing.T) {
	// q returns to the list and keeps the batch.
	m := proposalPane(t)
	m = runColon(m, "proposal-review 2")
	view := m.results.View()
	if !strings.Contains(view, "y accept  n skip  q/Esc back to list") || strings.Contains(view, "reject") {
		t.Fatalf("review footer wrong:\n%s", view)
	}
	m = key(m, "q")
	if m.pendingAction == nil || len(m.pendingAction.Proposals) != 2 || !m.results.Visible() ||
		!strings.Contains(m.results.View(), "1. create characters/Zed") || m.reviewingProposal() {
		t.Fatal("q should return to the list with the batch intact")
	}

	// n skips the reviewed proposal and shows the renumbered list.
	m = runColon(m, "proposal-review 2")
	m = key(m, "n")
	if len(m.pendingAction.Proposals) != 1 || m.pendingAction.Proposals[0].Name != "Zed" || !m.results.Visible() {
		t.Fatalf("n should skip proposal 2: %+v", m.pendingAction)
	}
	if v := m.results.View(); !strings.Contains(v, "y apply all") || strings.Contains(v, "back to list") {
		t.Fatalf("list footer/body wrong:\n%s", v)
	}

	// y applies the reviewed proposal; the last one closes the pane.
	m = runColon(m, "proposal-review 1")
	m = key(m, "y")
	if m.pendingAction != nil || m.results.Visible() {
		t.Fatal("last accept should clear and close")
	}
	if _, err := os.Stat(filepath.Join(m.project.Root, "state", "characters", "zed.md")); err != nil {
		t.Fatalf("y did not write the proposal: %v", err)
	}

	// Esc in review also goes back to the list.
	m = proposalPane(t)
	m = runColon(m, "proposal-review 1")
	m = pressEsc(m)
	if m.pendingAction == nil || !strings.Contains(m.results.View(), "1. create characters/Zed") {
		t.Fatal("esc should return to the list")
	}
}

func TestProposalReviewNarrowHeaderLabel(t *testing.T) {
	m := proposalPane(t)
	m = update(m, tea.WindowSizeMsg{Width: 90, Height: 40})
	m = runColon(m, "proposal-review 1")
	v := m.results.View()
	if strings.Contains(v, "selection") {
		t.Fatalf("misleading selection header:\n%s", v)
	}
}

func TestStaleProposalAcceptHidesPane(t *testing.T) {
	m := proposalPane(t)
	m.bufferGen++ // the buffer changed since extraction
	m = runColon(m, "proposal-accept 1")
	if m.pendingAction != nil || m.results.Visible() {
		t.Fatalf("stale list must not stay actionable: pending=%v visible=%v", m.pendingAction, m.results.Visible())
	}
	if !strings.Contains(m.notice, "Stale result") {
		t.Fatalf("notice = %q", m.notice)
	}
	if m.focus != FocusEditor {
		t.Fatal("focus should return to the editor")
	}
}

func TestSaveAsInsideChaptersRefreshesChapters(t *testing.T) {
	m, _, _ := pendingModel(t)
	m = update(m, tea.WindowSizeMsg{Width: 140, Height: 40})
	target := filepath.Join(m.project.Root, "chapters", "003_new.md")
	m.editor.SetContent("# Three\n\nalpha beta gamma delta")
	m.editor.Dirty = true
	m.executeCommand("w " + target)
	if m.err != nil {
		t.Fatalf("save-as failed: %v", m.err)
	}
	if !m.project.IsChapter(target) {
		t.Fatal("saved-as file is not a chapter after save-as")
	}
	// Further unsaved edits to it reach compile and word count.
	m.editor.SetContent("# Three\n\nalpha beta gamma delta epsilon zeta")
	m.editor.Dirty = true
	if out := wordCountText(t, m); !strings.Contains(out, "3 chapters") || !strings.Contains(out, "16 words") {
		t.Fatalf("word count missing new chapter:\n%s", out)
	}
	if got := runCompile(t, m, m.project.Root); !strings.Contains(got, "epsilon zeta") {
		t.Fatalf("compile missing new chapter edits:\n%s", got)
	}
}

func TestSaveAsOutsideChaptersIsNotAChapter(t *testing.T) {
	m, _, _ := pendingModel(t)
	target := filepath.Join(m.project.Root, "notes.md")
	m.editor.SetContent("scratch")
	m.editor.Dirty = true
	m.executeCommand("w " + target)
	if m.err != nil || m.project.IsChapter(target) {
		t.Fatalf("err=%v chapter=%v", m.err, m.project.IsChapter(target))
	}
}

func TestCleanBufferDoesNotOverlayDisk(t *testing.T) {
	m, ch1, _ := pendingModel(t)
	m.editor.SetContent("STALE in-memory words")
	m.editor.Dirty = false
	if err := os.WriteFile(ch1, []byte("# Chapter 1\n\nNEWER disk text"), 0644); err != nil {
		t.Fatal(err)
	}
	got := runCompile(t, m, m.project.Root)
	if strings.Contains(got, "STALE") || !strings.Contains(got, "NEWER disk text") {
		t.Fatalf("clean buffer overrode disk:\n%s", got)
	}
}

func TestForcedQuitIntentSkipsDirtyBlock(t *testing.T) {
	m, _, _ := pendingModel(t)
	m.editor.SetContent("dirty")
	m.editor.Dirty = true
	_, cmd := m.handleCommandIntent(command.QuitIntent{})
	if cmd != nil {
		if _, ok := cmd().(tea.QuitMsg); ok {
			t.Fatal("unforced quit must be blocked while dirty")
		}
	}
	_, cmd = m.handleCommandIntent(command.QuitIntent{Force: true})
	if cmd == nil {
		t.Fatal("forced quit returned no command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("forced quit did not quit")
	}
}
