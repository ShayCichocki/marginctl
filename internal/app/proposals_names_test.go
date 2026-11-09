package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ShayCichocki/marginctl/internal/command"
	"github.com/ShayCichocki/marginctl/internal/llmcmd"
	"github.com/ShayCichocki/marginctl/internal/state"
)

func TestProposalsCreateRealisticNames(t *testing.T) {
	root, _, _ := newTestProject(t)
	m := New()
	if err := m.OpenPath(root); err != nil {
		t.Fatal(err)
	}
	names := []string{"Martin Luther King Jr.", "Washington, D.C.", "Dr. Who?", `"Doc" Holliday`, "Station 7: Outpost", "Con", "O'Brien", "Zoë", "李小龍", "🐉🔥"}
	var props []command.StateProposal
	for _, n := range names {
		props = append(props, command.StateProposal{Category: "characters", Name: n, Action: "create", Summary: "bio"})
	}
	m.applyStateProposals(armProposals(t, &m, props...))
	if m.err != nil || m.noticeIsErr || !strings.Contains(m.notice, "Created 10") {
		t.Fatalf("err=%v notice=%q", m.err, m.notice)
	}
	for _, n := range names {
		if sf := m.state.FindEntry(state.StateCharacter, "", n); sf == nil || sf.Name != n {
			t.Errorf("%q: %+v", n, sf)
		}
	}
	// A second pass reports every one as already existing instead of going silent.
	m.llmReqID = 7
	m.showStateProposals(props, 0, llmcmd.Operation{RequestID: 7, ProjectRoot: m.project.Root, SourceFile: m.project.CurrentFile, BufferRevision: m.bufferGen, Kind: "llm.extract-state"})
	if !strings.Contains(m.notice, "10 skipped (10 already exists)") {
		t.Fatalf("notice = %q", m.notice)
	}
}

func TestProposalsUpdateAppendResolveByName(t *testing.T) {
	root, _, _ := newTestProject(t)
	alice := secWriteState(t, root, "characters/Alice.md", "no heading\n")
	king := secWriteState(t, root, "characters/mlk.md", "# Martin Luther King Jr.\n")
	m := New()
	if err := m.OpenPath(root); err != nil {
		t.Fatal(err)
	}
	m.applyStateProposals(armProposals(t, &m,
		command.StateProposal{Category: "characters", Name: "ALICE", Action: "append", Proposed: "more"},
		command.StateProposal{Category: "characters", Name: "martin luther king jr.", Action: "update", Proposed: "# MLK\n"},
		command.StateProposal{Category: "characters", Name: "Nobody", Action: "update", Proposed: "x"},
	))
	if b, _ := os.ReadFile(alice); string(b) != "no heading\n\nmore\n" {
		t.Fatalf("alice = %q", b)
	}
	if b, _ := os.ReadFile(king); string(b) != "# MLK\n" {
		t.Fatalf("king = %q", b)
	}
	if !strings.Contains(m.notice, "Applied 2 of 3 state proposals (1 updated, 1 appended); 1 skipped (1 no matching entry)") {
		t.Fatalf("notice = %q", m.notice)
	}
	if _, err := os.Stat(filepath.Join(root, "state", "characters", "nobody.md")); err == nil {
		t.Fatal("unknown name wrote a file")
	}
}

func TestShowStateProposalsReportsMalformedNotNothingFound(t *testing.T) {
	root, _, _ := newTestProject(t)
	m := New()
	if err := m.OpenPath(root); err != nil {
		t.Fatal(err)
	}
	m.llmReqID = 7
	op := llmcmd.Operation{RequestID: 7, ProjectRoot: m.project.Root, SourceFile: m.project.CurrentFile, BufferRevision: m.bufferGen, Kind: "llm.extract-state"}
	m.showStateProposals(nil, 2, op)
	if !strings.Contains(m.notice, "2 skipped (2 malformed)") || strings.Contains(m.notice, "No new state entries") {
		t.Fatalf("notice = %q", m.notice)
	}
	m.showStateProposals(nil, 0, op)
	if m.notice != "No new state entries found to add" {
		t.Fatalf("notice = %q", m.notice)
	}
}
