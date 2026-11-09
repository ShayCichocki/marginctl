package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ShayCichocki/marginctl/internal/command"
	"github.com/ShayCichocki/marginctl/internal/fsutil"
	"github.com/ShayCichocki/marginctl/internal/llm"
	"github.com/ShayCichocki/marginctl/internal/llmcmd"
	"github.com/ShayCichocki/marginctl/internal/results"
)

func operationFor(m Model, id int, kind string) llmcmd.Operation {
	return llmcmd.Operation{RequestID: id, Kind: kind, ProjectRoot: m.project.Root,
		SourceFile: m.project.CurrentFile, BufferRevision: m.bufferGen,
		Provider: m.llmClient.Provider(), Model: m.llmClient.Model()}
}

func TestDelayedProjectScopedResultsCannotCrossProjects(t *testing.T) {
	m, _, _ := openTestModel(t)
	op := operationFor(m, 1, "llm.extract-state")
	m.llmReqID = 1
	rootB, _, _ := newTestProject(t)
	if err := m.OpenPath(rootB); err != nil {
		t.Fatal(err)
	}

	m = update(m, llmcmd.ResponseMsg{ReqID: 1, Operation: op, Result: command.Result{
		Type: command.ResultStateProposals, StateProposals: []command.StateProposal{{Category: "characters", Name: "Late"}},
	}})
	if m.pendingAction != nil || m.results.HasPendingProposal() {
		t.Fatal("delayed project-A result became actionable in project B")
	}
	if m.state != nil && m.state.EntryExists(0, "", "Late") {
		t.Fatal("delayed proposal mutated project B")
	}
}

func TestVoiceGuideTargetFingerprintRevalidatedOnAccept(t *testing.T) {
	m, _, _ := openTestModel(t)
	if err := m.state.Initialize(); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(m.project.Root, "state", "style", "voice.md")
	fp, err := fsutil.FileFingerprint(target)
	if err != nil {
		t.Fatal(err)
	}
	op := operationFor(m, 1, "llm.voice-guide")
	op.Target, op.TargetFingerprint = target, fp
	m.llmReqID = 1
	m.setPendingAction(pendingLLMResult, op, command.Result{Type: command.ResultVoiceGuide, ProposedText: "generated"})
	id := m.pendingAction.ID
	if err := os.WriteFile(target, []byte("external"), 0644); err != nil {
		t.Fatal(err)
	}
	m = update(m, results.VoiceGuideAcceptedMsg{ActionID: id, Operation: op, Content: "generated"})
	b, _ := os.ReadFile(target)
	if string(b) != "external" {
		t.Fatalf("externally changed target overwritten: %q", b)
	}
}

func TestPendingActionIDsPreventHiddenAndDuplicateAcceptance(t *testing.T) {
	m, chapter, _ := openTestModel(t)
	if err := os.WriteFile(chapter, []byte("dragon"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := m.loadIntoEditor(chapter); err != nil {
		t.Fatal(err)
	}
	m.executeCommand("replace dragon/griffin")
	oldID := m.pendingAction.ID
	m.clearPendingAction()
	m.results.Hide()
	m = update(m, results.ConfirmAcceptedMsg{ActionID: oldID})
	b, _ := os.ReadFile(chapter)
	if string(b) != "dragon" {
		t.Fatal("hidden action was accepted")
	}

	m.executeCommand("replace dragon/griffin")
	id := m.pendingAction.ID
	m = update(m, results.ConfirmAcceptedMsg{ActionID: id})
	m = update(m, results.ConfirmAcceptedMsg{ActionID: id})
	b, _ = os.ReadFile(chapter)
	if string(b) != "griffin" {
		t.Fatalf("matching action was not applied exactly once: %q", b)
	}
}

func TestPartialStreamFailureAndCancellationRemainVisibleNonActionable(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"protocol", llm.ErrStreamProtocol, "Incomplete response"},
		{"timeout", context.DeadlineExceeded, "Timed out"},
		{"provider", llm.ErrStreamProvider, "Provider failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _, _ := openTestModel(t)
			m.width, m.height = 120, 30
			m.updateLayout()
			op := operationFor(m, 1, "llm.summarize")
			m.llmReqID, m.llmLoading, m.streamBuf = 1, true, "partial text"
			m.results.StreamText(m.streamBuf)
			m = update(m, streamEndMsg{operation: op, err: tc.err})
			view := strings.Join(strings.Fields(m.results.View()), " ")
			if !strings.Contains(view, "partial text") || !strings.Contains(view, tc.want) {
				t.Fatalf("view = %q", view)
			}
			if m.results.HasPendingProposal() {
				t.Fatal("failed stream became actionable")
			}
		})
	}
}
