package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ShayCichocki/marginctl/internal/llm"
	"github.com/ShayCichocki/marginctl/internal/llmcmd"
)

func TestContinuityPrefixThenDeltaKeepsPrefix(t *testing.T) {
	isolateAppEnv(t)
	m, _, _ := openTestModel(t)
	m = update(m, tea.WindowSizeMsg{Width: 140, Height: 40})
	m.llmLoading, m.llmReqID = true, 1
	op := llmcmd.Operation{RequestID: 1, ProjectRoot: m.project.Root, SourceFile: m.project.CurrentFile, BufferRevision: m.bufferGen}
	prefix := "Budget note: " + strings.Repeat("this prefix is long enough to wrap in a narrow pane ", 4) + "\nsecond prefix line\n\n"
	m = update(m, llmcmd.StreamStartMsg{ReqID: 1, Operation: op, Prefix: prefix, Ch: make(chan llm.StreamEvent)})
	m = update(m, streamDeltaMsg{operation: op, text: "first delta"})
	v := m.results.View()
	for _, want := range []string{"Budget note:", "second prefix line", "first delta"} {
		if !strings.Contains(v, want) {
			t.Fatalf("missing %q:\n%s", want, v)
		}
	}
	if strings.Count(v, "second prefix line") != 1 {
		t.Fatalf("duplicated prefix:\n%s", v)
	}
}

func TestProposalReviewCarriesOperationAndClosesWithBatch(t *testing.T) {
	m := proposalPane(t)
	m = runColon(m, "proposal-review 1")
	if !m.reviewingProposal() || m.results.Operation() != m.pendingAction.Operation {
		t.Fatalf("review operation = %+v, want %+v", m.results.Operation(), m.pendingAction.Operation)
	}
	m.clearPendingAction()
	if m.results.Visible() || m.focus == FocusResults {
		t.Fatal("review pane must close when the batch is cleared")
	}
	// The too-small Esc path clears the batch the same way.
	m = proposalPane(t)
	m = runColon(m, "proposal-review 1")
	m = update(m, tea.WindowSizeMsg{Width: 10, Height: 5})
	m = pressEsc(m)
	if m.results.ReviewOnly() {
		t.Fatal("review pane survived batch clear")
	}
}

func TestQOnStreamingPaneCancelsLikeEsc(t *testing.T) {
	m, _ := streamingModel(t)
	m.focus = FocusResults
	m.editor.Blur()
	m = key(m, "q")
	if m.llmLoading || m.llmCancel != nil {
		t.Fatal("q did not cancel the stream")
	}
	if m.notice != "LLM request cancelled" {
		t.Fatalf("notice = %q", m.notice)
	}
	if !m.results.Visible() || !strings.Contains(m.results.View(), "cancelled") {
		t.Fatalf("expected cancelled pane:\n%s", m.results.View())
	}
}

func TestResultsPromptEmptyEnterSilent(t *testing.T) {
	m := proposalPane(t)
	m = runColon(m, "")
	if m.notice != "" || m.activePrompt() != promptNone {
		t.Fatalf("notice=%q prompt=%v", m.notice, m.activePrompt())
	}
}
