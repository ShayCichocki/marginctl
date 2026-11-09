package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ShayCichocki/marginctl/internal/command"
	"github.com/ShayCichocki/marginctl/internal/fsutil"
	"github.com/ShayCichocki/marginctl/internal/llmcmd"
	"github.com/ShayCichocki/marginctl/internal/results"
	"github.com/ShayCichocki/marginctl/internal/state"
)

const pendingTestReqID = 7

// pendingModel opens the test project hermetically and arms the request
// counter so operations built by pendingOp are "current".
func pendingModel(t *testing.T) (Model, string, string) {
	t.Helper()
	isolateAppEnv(t)
	m, ch1, ch2 := openTestModel(t)
	m.llmReqID = pendingTestReqID
	return m, ch1, ch2
}

func pendingOp(m *Model, kind string) llmcmd.Operation {
	return llmcmd.Operation{
		RequestID:      pendingTestReqID,
		Kind:           kind,
		ProjectRoot:    m.project.Root,
		SourceFile:     m.project.CurrentFile,
		BufferRevision: m.bufferGen,
	}
}

// ---- pendingLLMResult: diff accept ---------------------------------------

func armDiff(m *Model) (llmcmd.Operation, results.DiffAcceptedMsg) {
	op := pendingOp(m, "llm.tighten")
	m.setPendingAction(pendingLLMResult, op, command.Result{Type: command.ResultDiff, OriginalText: "# Chapter 1", ProposedText: "XX"})
	return op, results.DiffAcceptedMsg{
		ActionID: m.pendingAction.ID, Operation: op,
		OriginalText: "# Chapter 1", ProposedText: "XX",
		StartLine: 0, EndLine: 0, StartCol: 0, EndCol: 9,
		File: m.project.CurrentFile, BufferGen: m.bufferGen,
	}
}

func TestPendingIdentityDiffAcceptTable(t *testing.T) {
	type tc struct {
		name    string
		mutate  func(m *Model, msg *results.DiffAcceptedMsg)
		applied bool
	}
	cases := []tc{
		{"matching identity applies", func(*Model, *results.DiffAcceptedMsg) {}, true},
		{"stale action id", func(_ *Model, msg *results.DiffAcceptedMsg) { msg.ActionID++ }, false},
		{"zero action id", func(_ *Model, msg *results.DiffAcceptedMsg) { msg.ActionID = 0 }, false},
		{"different operation", func(_ *Model, msg *results.DiffAcceptedMsg) { msg.Operation.RequestID++ }, false},
		{"different operation target", func(_ *Model, msg *results.DiffAcceptedMsg) { msg.Operation.SourceFile = "/elsewhere.md" }, false},
		{"different file in message", func(_ *Model, msg *results.DiffAcceptedMsg) { msg.File = "/elsewhere.md" }, false},
		{"older buffer revision in message", func(_ *Model, msg *results.DiffAcceptedMsg) { msg.BufferGen-- }, false},
		{"buffer revision advanced after pending", func(m *Model, _ *results.DiffAcceptedMsg) { m.bufferGen++ }, false},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			m, _, _ := pendingModel(t)
			_, msg := armDiff(&m)
			beforeContent := m.editor.Content()
			beforeGen := m.bufferGen
			c.mutate(&m, &msg)
			genAfterMutate := m.bufferGen
			m = update(m, msg)
			if c.applied {
				if m.editor.Content() == beforeContent || !strings.HasPrefix(m.editor.Content(), "XX") {
					t.Fatalf("expected diff applied, content = %q", m.editor.Content())
				}
				return
			}
			if m.editor.Content() != beforeContent {
				t.Fatalf("buffer changed by non-matching accept: %q", m.editor.Content())
			}
			if m.bufferGen != genAfterMutate || (genAfterMutate == beforeGen && m.editor.Dirty) {
				t.Fatalf("buffer revision/dirty flag moved: gen %d -> %d dirty=%v", genAfterMutate, m.bufferGen, m.editor.Dirty)
			}
		})
	}
}

func TestPendingIdentityDiffAcceptAfterFileSwitchIsNoop(t *testing.T) {
	m, _, ch2 := pendingModel(t)
	_, msg := armDiff(&m)
	if err := m.loadIntoEditor(ch2); err != nil {
		t.Fatal(err)
	}
	before := m.editor.Content()
	m = update(m, msg)
	if m.editor.Content() != before || m.editor.Dirty {
		t.Fatalf("diff for another file touched the new buffer: %q dirty=%v", m.editor.Content(), m.editor.Dirty)
	}
	if m.pendingAction != nil {
		t.Fatal("pending action survived a file switch")
	}
}

func TestPendingIdentityRejectOnlyHonorsCurrentID(t *testing.T) {
	m, _, _ := pendingModel(t)
	armDiff(&m)
	id := m.pendingAction.ID
	m = update(m, results.DiffRejectedMsg{ActionID: id + 1})
	if m.pendingAction == nil {
		t.Fatal("reject with stale id cleared the pending action")
	}
	m = update(m, results.DiffRejectedMsg{ActionID: id})
	if m.pendingAction != nil {
		t.Fatal("reject with matching id did not clear pending action")
	}
}

// ---- pendingLLMResult: voice guide ---------------------------------------

func TestPendingIdentityVoiceGuideAcceptTable(t *testing.T) {
	type tc struct {
		name    string
		mutate  func(m *Model, msg *results.VoiceGuideAcceptedMsg, voice string)
		applied bool
	}
	cases := []tc{
		{"matching identity writes", func(*Model, *results.VoiceGuideAcceptedMsg, string) {}, true},
		{"stale action id", func(_ *Model, msg *results.VoiceGuideAcceptedMsg, _ string) { msg.ActionID++ }, false},
		{"zero action id", func(_ *Model, msg *results.VoiceGuideAcceptedMsg, _ string) { msg.ActionID = 0 }, false},
		{"request superseded", func(m *Model, _ *results.VoiceGuideAcceptedMsg, _ string) { m.llmReqID++ }, false},
		{"buffer revision changed", func(m *Model, _ *results.VoiceGuideAcceptedMsg, _ string) { m.bufferGen++ }, false},
		{"project changed", func(_ *Model, msg *results.VoiceGuideAcceptedMsg, _ string) {
			msg.Operation.ProjectRoot = "/other/project"
		}, false},
		{"target changed on disk", func(_ *Model, _ *results.VoiceGuideAcceptedMsg, voice string) {
			_ = os.MkdirAll(filepath.Dir(voice), 0755)
			_ = os.WriteFile(voice, []byte("edited elsewhere"), 0644)
		}, false},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			m, _, _ := pendingModel(t)
			voice := filepath.Join(m.project.Root, "state", "style", "voice.md")
			if err := os.MkdirAll(filepath.Dir(voice), 0755); err != nil {
				t.Fatal(err)
			}
			op := pendingOp(&m, "llm.voice-guide")
			op.Target = voice
			op.TargetFingerprint, _ = fsutil.FileFingerprint(voice)
			m.setPendingAction(pendingLLMResult, op, command.Result{Type: command.ResultVoiceGuide, Content: "NEW VOICE"})
			msg := results.VoiceGuideAcceptedMsg{ActionID: m.pendingAction.ID, Operation: op, Content: "NEW VOICE"}
			c.mutate(&m, &msg, voice)
			m = update(m, msg)

			data, err := os.ReadFile(voice)
			if c.applied {
				if err != nil || string(data) != "NEW VOICE" {
					t.Fatalf("voice guide not written: err=%v data=%q", err, data)
				}
				return
			}
			if c.name == "target changed on disk" {
				if string(data) != "edited elsewhere" {
					t.Fatalf("concurrent edit overwritten: %q", data)
				}
				return
			}
			if err == nil {
				t.Fatalf("voice guide written despite non-matching identity: %q", data)
			}
		})
	}
}

// ---- pendingStateProposals ----------------------------------------------

func armPendingProposals(t *testing.T, m *Model) (llmcmd.Operation, uint64) {
	t.Helper()
	op := pendingOp(m, "llm.extract-state")
	props := []command.StateProposal{{Category: "characters", Name: "Zed", Summary: "a stranger", Action: "create"}}
	m.nextActionID++
	m.pendingAction = &PendingAction{ID: m.nextActionID, Kind: pendingStateProposals, Operation: op, Proposals: props}
	m.results.ShowAction(command.Result{Type: command.ResultConfirm, Content: "Create 1"}, m.nextActionID, op)
	return op, m.nextActionID
}

func TestPendingIdentityStateProposalAcceptTable(t *testing.T) {
	type tc struct {
		name    string
		mutate  func(m *Model, id *uint64)
		applied bool
	}
	cases := []tc{
		{"matching identity creates", func(*Model, *uint64) {}, true},
		{"stale action id", func(_ *Model, id *uint64) { *id++ }, false},
		{"zero action id", func(_ *Model, id *uint64) { *id = 0 }, false},
		{"request superseded", func(m *Model, _ *uint64) { m.llmReqID++ }, false},
		{"buffer revision changed", func(m *Model, _ *uint64) { m.bufferGen++ }, false},
		{"source file changed", func(m *Model, _ *uint64) {
			m.project.SetCurrentFile(filepath.Join(m.project.Root, "chapters", "002_chapter_2.md"))
		}, false},
		{"operation project changed", func(m *Model, _ *uint64) { m.pendingAction.Operation.ProjectRoot = "/other" }, false},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			m, _, _ := pendingModel(t)
			if m.state == nil {
				t.Fatal("project state not loaded")
			}
			_, id := armPendingProposals(t, &m)
			c.mutate(&m, &id)
			m = update(m, results.ConfirmAcceptedMsg{ActionID: id})
			exists := m.state.EntryExists(state.StateCharacter, "", "Zed")
			if exists != c.applied {
				t.Fatalf("entry exists = %v, want %v", exists, c.applied)
			}
		})
	}
}

// ---- pendingReplace ------------------------------------------------------

func TestPendingIdentityReplaceAcceptTable(t *testing.T) {
	type tc struct {
		name    string
		mutate  func(m *Model, id *uint64)
		applied bool
	}
	cases := []tc{
		{"matching id replaces", func(*Model, *uint64) {}, true},
		{"stale action id", func(_ *Model, id *uint64) { *id++ }, false},
		{"zero action id", func(_ *Model, id *uint64) { *id = 0 }, false},
		{"superseded by newer pending action", func(m *Model, _ *uint64) { m.nextActionID++; m.pendingAction.ID = m.nextActionID }, false},
		{"pending cleared", func(m *Model, _ *uint64) { m.clearPendingAction() }, false},
		{"wrong pending kind", func(m *Model, _ *uint64) { m.pendingAction.Kind = pendingStateProposals }, false},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			m, ch1, _ := pendingModel(t)
			m.executeCommand("replace Hello/Howdy")
			if m.pendingAction == nil || m.pendingAction.Kind != pendingReplace {
				t.Fatalf("replace preview not armed; notice=%q", m.notice)
			}
			id := m.pendingAction.ID
			c.mutate(&m, &id)
			m = update(m, results.ConfirmAcceptedMsg{ActionID: id})
			data, _ := os.ReadFile(ch1)
			replaced := strings.Contains(string(data), "Howdy")
			if replaced != c.applied {
				t.Fatalf("replaced = %v, want %v (disk=%q)", replaced, c.applied, data)
			}
		})
	}
}

func TestPendingIdentityReplaceClearedByProjectSwitch(t *testing.T) {
	m, ch1, _ := pendingModel(t)
	m.executeCommand("replace Hello/Howdy")
	id := m.pendingAction.ID
	otherRoot, otherCh, _ := newTestProject(t)
	if err := m.OpenPath(otherRoot); err != nil {
		t.Fatal(err)
	}
	m = update(m, results.ConfirmAcceptedMsg{ActionID: id})
	for _, p := range []string{ch1, otherCh} {
		if data, _ := os.ReadFile(p); strings.Contains(string(data), "Howdy") {
			t.Fatalf("stale replace applied to %s", p)
		}
	}
}

func TestPendingIdentityReplaceRefusedWhenBufferDirty(t *testing.T) {
	m, ch1, _ := pendingModel(t)
	m.executeCommand("replace Hello/Howdy")
	id := m.pendingAction.ID
	// The buffer acquires unsaved work after the preview was approved for a clean one.
	m.editor.SetRecoveredContent("# Chapter 1\n\nunsaved Hello draft", "# Chapter 1\n\nHello")
	if !m.editor.Dirty {
		t.Fatal("setup: editor not dirty")
	}
	draft := m.editor.Content()
	m = update(m, results.ConfirmAcceptedMsg{ActionID: id})
	data, _ := os.ReadFile(ch1)
	if m.editor.Content() != draft {
		t.Fatalf("dirty buffer clobbered: %q", m.editor.Content())
	}
	if strings.Contains(string(data), "Howdy") {
		t.Fatal("replace rewrote disk under a dirty buffer")
	}
}

// ---- colon proposal commands --------------------------------------------

func TestProposalCommandWithoutPendingIsRefused(t *testing.T) {
	m, _, _ := pendingModel(t)
	for _, v := range []string{"proposal-review", "proposal-accept", "proposal-edit", "proposal-merge", "proposal-skip"} {
		m.notice, m.noticeIsErr = "", false
		m.executeCommand(v + " 1")
		if !m.noticeIsErr || !strings.Contains(m.notice, "No state proposals pending") {
			t.Errorf("%s: notice=%q", v, m.notice)
		}
	}
}

func TestProposalCommandBadNumberShowsUsage(t *testing.T) {
	m, _, _ := pendingModel(t)
	armPendingProposals(t, &m)
	for _, arg := range []string{"", "0", "2", "x"} {
		m.notice, m.noticeIsErr = "", false
		m.executeCommand(strings.TrimSpace("proposal-skip " + arg))
		if !m.noticeIsErr || !strings.Contains(m.notice, "usage") {
			t.Errorf("arg %q: notice=%q", arg, m.notice)
		}
	}
	if len(m.pendingAction.Proposals) != 1 {
		t.Fatal("bad number mutated proposals")
	}
}

func TestProposalCommandSkipEditReview(t *testing.T) {
	m, _, _ := pendingModel(t)
	armPendingProposals(t, &m)

	m.executeCommand("proposal-edit 1")
	if !m.noticeIsErr {
		t.Fatalf("edit with no content should show usage, got %q", m.notice)
	}
	m.executeCommand("proposal-edit 1 Zed is tall")
	if m.pendingAction.Proposals[0].Proposed != "Zed is tall" || m.pendingAction.Proposals[0].Action != "create" {
		t.Fatalf("edit not recorded: %+v", m.pendingAction.Proposals[0])
	}
	m.executeCommand("proposal-review 1")
	if !m.results.Visible() {
		t.Fatal("review did not show a diff")
	}
	m.executeCommand("proposal-skip 1")
	if m.pendingAction != nil {
		t.Fatalf("skip of the last proposal should clear the pending action: %+v", m.pendingAction)
	}
}

func TestProposalCommandAcceptCreatesEntryAndClearsPending(t *testing.T) {
	m, _, _ := pendingModel(t)
	armPendingProposals(t, &m)
	m.executeCommand("proposal-accept 1")
	if !m.state.EntryExists(state.StateCharacter, "", "Zed") {
		t.Fatalf("entry not created; err=%v notice=%q", m.err, m.notice)
	}
	if m.pendingAction != nil {
		t.Fatal("pending action not cleared after last proposal applied")
	}
}

func TestProposalCommandAcceptHonorsOperationIdentity(t *testing.T) {
	m, _, _ := pendingModel(t)
	armPendingProposals(t, &m)
	// The buffer the proposals were extracted from has since changed.
	m.bufferGen++
	m.executeCommand("proposal-accept 1")
	if m.state.EntryExists(state.StateCharacter, "", "Zed") {
		t.Fatal("stale proposal accept wrote state")
	}
}

func TestListProjectNotesCommandFiltersAndReportsEmpty(t *testing.T) {
	m, ch1, _ := pendingModel(t)

	m.notice, m.noticeIsErr = "", false
	m.executeCommand("notes-project")
	if m.notice != "No matching project notes" || m.results.Visible() {
		t.Fatalf("empty project: notice=%q visible=%v", m.notice, m.results.Visible())
	}

	if err := os.WriteFile(ch1, []byte("alpha\nbeta gamma\ndelta\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_ = m.project.Refresh()
	if err := m.loadIntoEditor(ch1); err != nil {
		t.Fatal(err)
	}
	m.editor.SetSelectionForTest(1, 0, 1, 9)
	m.executeCommand("note tighten this")
	if !strings.Contains(m.notice, "Note added") {
		t.Fatalf("setup: %q", m.notice)
	}

	m.executeCommand("notes-project")
	if !m.results.Visible() {
		t.Fatal("notes-project should list the open note")
	}

	m.results.Hide()
	m.notice = ""
	m.executeCommand("notes-project resolved")
	if m.notice != "No matching project notes" || m.results.Visible() {
		t.Fatalf("resolved filter should be empty: notice=%q", m.notice)
	}
}

func TestListProjectNotesCommandOutsideProject(t *testing.T) {
	isolateAppEnv(t)
	m := New()
	m.executeCommand("notes-project")
	if !m.noticeIsErr || m.notice != "Not in a project" {
		t.Fatalf("notice = %q", m.notice)
	}
}
