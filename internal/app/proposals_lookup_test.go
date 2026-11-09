package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ShayCichocki/marginctl/internal/command"
	"github.com/ShayCichocki/marginctl/internal/llmcmd"
)

func TestSectionHeadingProposalDoesNotOverwriteHandMadeEntry(t *testing.T) {
	root, _, _ := newTestProject(t)
	var paths []string
	for _, n := range []string{"Alice", "Bob", "Carol"} {
		paths = append(paths, secWriteState(t, root, "characters/"+n+".md", n+" is a character.\n\n# Appearance\ntall\n"))
	}
	m := New()
	if err := m.OpenPath(root); err != nil {
		t.Fatal(err)
	}
	m.applyStateProposals(armProposals(t, &m,
		command.StateProposal{Category: "characters", Name: "Appearance", Action: "update", Proposed: "PWNED"}))
	for _, p := range paths {
		if b, _ := os.ReadFile(p); strings.Contains(string(b), "PWNED") {
			t.Fatalf("%s overwritten", p)
		}
	}
	if !strings.Contains(m.notice, "1 no matching entry") {
		t.Fatalf("notice = %q", m.notice)
	}
}

func TestContinuityContextKeepsEntriesSharingAHeading(t *testing.T) {
	root, _, _ := newTestProject(t)
	secWriteState(t, root, "characters/alice.md", "# Alice\n")
	secWriteState(t, root, "characters/alicia.md", "# Alice\n")
	secWriteState(t, root, "characters/bob.md", "# Bob\n")
	m := New()
	if err := m.OpenPath(root); err != nil {
		t.Fatal(err)
	}
	ctx := m.commandContext()
	if len(ctx.StateFilePaths) != 3 {
		t.Fatalf("context has %d entries, want 3: %v", len(ctx.StateFilePaths), ctx.StateFilePaths)
	}
	if _, ok := ctx.StateFilePaths["characters/Bob"]; !ok {
		t.Fatalf("unique names keep the plain key: %v", ctx.StateFilePaths)
	}
	for k := range ctx.StateFilePaths {
		if strings.HasPrefix(k, "characters/Alice") && !strings.Contains(k, ".md]") {
			t.Fatalf("colliding key %q not disambiguated", k)
		}
	}
}

func TestConfirmationShowsExactFilesAndTruthfulCounts(t *testing.T) {
	root, _, _ := newTestProject(t)
	alice := secWriteState(t, root, "characters/alice.md", "# Alice\n")
	secWriteState(t, root, "timeline.md", "# Timeline\n")
	m := New()
	if err := m.OpenPath(root); err != nil {
		t.Fatal(err)
	}
	props := []command.StateProposal{
		{Category: "characters", Name: "Zoë", Action: "create", Summary: "new"},
		{Category: "characters", Name: "Alice", Action: "update", Summary: "changed", Proposed: "# Alice\nx\n"},
		{Category: "timeline", Name: "t", Action: "append", Summary: "later", Proposed: "Day 2"},
	}
	m.llmReqID = 7
	m.showStateProposals(props, 0, llmcmd.Operation{RequestID: 7, ProjectRoot: m.project.Root, SourceFile: m.project.CurrentFile, BufferRevision: m.bufferGen, Kind: "llm.extract-state"})
	if m.pendingAction == nil {
		t.Fatalf("no confirmation; notice=%q", m.notice)
	}
	m.results.SetSize(300, 40)
	shown := m.results.View()
	t.Log("\n" + shown)
	for _, want := range []string{"state/characters/zoë.md", "state/characters/alice.md", "state/timeline.md", "1 created, 1 updated, 1 appended"} {
		if !strings.Contains(shown, want) {
			t.Errorf("confirmation lacks %q:\n%s", want, shown)
		}
	}
	m.applyStateProposals(m.pendingAction.ID)
	if strings.Contains(m.notice, "Created 3") || !strings.Contains(m.notice, "1 created, 1 updated, 1 appended") {
		t.Fatalf("notice = %q", m.notice)
	}
	if b, _ := os.ReadFile(alice); string(b) != "# Alice\nx\n" {
		t.Fatalf("alice = %q", b)
	}
	if _, err := os.Stat(filepath.Join(root, "state", "characters", "zoë.md")); err != nil {
		t.Fatal(err)
	}
}

func TestAppendOnlyNoticeDoesNotSayCreated(t *testing.T) {
	root, _, _ := newTestProject(t)
	secWriteState(t, root, "characters/alice.md", "# Alice\n")
	m := New()
	if err := m.OpenPath(root); err != nil {
		t.Fatal(err)
	}
	m.applyStateProposals(armProposals(t, &m, command.StateProposal{Category: "characters", Name: "Alice", Action: "append", Proposed: "more"}))
	if strings.Contains(m.notice, "Created") || !strings.Contains(m.notice, "1 appended") {
		t.Fatalf("notice = %q", m.notice)
	}
}
