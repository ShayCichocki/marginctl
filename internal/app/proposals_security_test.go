package app

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ShayCichocki/marginctl/internal/command"
	"github.com/ShayCichocki/marginctl/internal/llmcmd"
)

func armProposals(t *testing.T, m *Model, props ...command.StateProposal) uint64 {
	t.Helper()
	if m.state == nil {
		t.Fatal("test model has no state manager")
	}
	m.llmReqID = 7
	m.nextActionID++
	op := llmcmd.Operation{RequestID: 7, ProjectRoot: m.project.Root, SourceFile: m.project.CurrentFile, BufferRevision: m.bufferGen, Kind: "llm.extract-state"}
	m.pendingAction = &PendingAction{ID: m.nextActionID, Kind: pendingStateProposals, Operation: op, Proposals: props}
	return m.nextActionID
}

func secWriteState(t *testing.T, root, rel, content string) string {
	t.Helper()
	p := filepath.Join(root, "state", filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestProposalsCannotEscapeStateDir(t *testing.T) {
	root, ch1, _ := newTestProject(t)
	absOutside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(absOutside, []byte("outside"), 0644); err != nil {
		t.Fatal(err)
	}
	base := strings.TrimSuffix(filepath.Base(ch1), ".md")
	cases := map[string]command.StateProposal{
		"update traversal target": {Category: "characters", Name: "x", Action: "update", Target: "../chapters/" + filepath.Base(ch1), Proposed: "PWNED"},
		"append traversal target": {Category: "characters", Name: "x", Action: "append", Target: "../chapters/" + filepath.Base(ch1), Proposed: "PWNED"},
		"absolute chapter target": {Category: "characters", Name: "x", Action: "update", Target: ch1, Proposed: "PWNED"},
		"absolute outside target": {Category: "characters", Name: "x", Action: "update", Target: absOutside, Proposed: "PWNED"},
		"traversal name update":   {Category: "characters", Name: "../../chapters/" + base, Action: "update", Proposed: "PWNED"},
		"traversal name append":   {Category: "locations", Name: "../../chapters/" + base, Action: "append", Proposed: "PWNED"},
		"traversal category":      {Category: "../chapters", Name: base, Action: "update", Proposed: "PWNED"},
		"state dir itself":        {Category: "characters", Name: "x", Action: "update", Target: ".", Proposed: "PWNED"},
	}
	for name, p := range cases {
		t.Run(name, func(t *testing.T) {
			m := New()
			if err := m.OpenPath(root); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(ch1)
			id := armProposals(t, &m, p)
			m.applyStateProposals(id)
			after, _ := os.ReadFile(ch1)
			if string(before) != string(after) {
				t.Fatalf("chapter modified: %q", after)
			}
			if out, _ := os.ReadFile(absOutside); string(out) != "outside" {
				t.Fatalf("outside file modified: %q", out)
			}
			// An explicit Target must fail hard with a surfaced error; a
			// name-only proposal is a lookup miss and must be reported as skipped.
			if p.Target != "" {
				if m.err == nil || !m.noticeIsErr {
					t.Fatalf("explicit target must surface an error, err=%v notice=%q isErr=%v", m.err, m.notice, m.noticeIsErr)
				}
			} else if (m.err == nil || !m.noticeIsErr) && !strings.Contains(m.notice, "skipped") {
				t.Fatalf("expected surfaced error or skip report, err=%v notice=%q isErr=%v", m.err, m.notice, m.noticeIsErr)
			}
		})
	}
}

func TestProposalsRejectSymlinkedStateFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks")
	}
	for _, action := range []string{"update", "append"} {
		t.Run(action, func(t *testing.T) {
			root, ch1, _ := newTestProject(t)
			if err := os.MkdirAll(filepath.Join(root, "state", "characters"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(ch1, filepath.Join(root, "state", "characters", "alice.md")); err != nil {
				t.Fatal(err)
			}
			m := New()
			if err := m.OpenPath(root); err != nil {
				t.Fatal(err)
			}
			before, _ := os.ReadFile(ch1)
			for _, p := range []command.StateProposal{
				{Category: "characters", Name: "alice", Action: action, Proposed: "PWNED"},
				{Category: "characters", Name: "x", Action: action, Target: "characters/alice.md", Proposed: "PWNED"},
			} {
				m.err, m.notice, m.noticeIsErr = nil, "", false
				m.applyStateProposals(armProposals(t, &m, p))
				after, _ := os.ReadFile(ch1)
				if string(before) != string(after) {
					t.Fatalf("chapter modified through symlink: %q", after)
				}
				if p.Target != "" {
					if m.err == nil || !m.noticeIsErr {
						t.Fatalf("explicit target must surface an error, got err=%v notice=%q", m.err, m.notice)
					}
				} else if !strings.Contains(m.notice, "skipped") {
					t.Fatalf("expected skip report, got err=%v notice=%q", m.err, m.notice)
				}
			}
		})
	}
}

func TestProposalsLegitimateUpdateAppendCreateStillWork(t *testing.T) {
	root, _, _ := newTestProject(t)
	alice := secWriteState(t, root, "characters/alice.md", "# Alice\n")
	tl := secWriteState(t, root, "timeline.md", "# Timeline\n")
	m := New()
	if err := m.OpenPath(root); err != nil {
		t.Fatal(err)
	}
	m.applyStateProposals(armProposals(t, &m,
		command.StateProposal{Category: "characters", Name: "alice", Action: "update", Proposed: "# Alice\nnew\n"},
		command.StateProposal{Category: "timeline", Name: "t", Action: "append", Proposed: "Day 2"},
		command.StateProposal{Category: "locations", Name: "x", Action: "update", Target: "characters/alice.md", Proposed: "# Alice\nvia target\n"},
		command.StateProposal{Category: "characters", Name: "bob", Action: "create", Summary: "Bob bio"},
	))
	if m.noticeIsErr || m.err != nil {
		t.Fatalf("unexpected error: %v / %q", m.err, m.notice)
	}
	if b, _ := os.ReadFile(alice); string(b) != "# Alice\nvia target\n" {
		t.Fatalf("alice = %q", b)
	}
	if b, _ := os.ReadFile(tl); string(b) != "# Timeline\n\nDay 2\n" {
		t.Fatalf("timeline = %q", b)
	}
	if _, err := os.Stat(filepath.Join(root, "state", "characters", "bob.md")); err != nil {
		t.Fatalf("create path broken: %v", err)
	}
	if !strings.Contains(m.notice, "Applied 4 state proposals (1 created, 2 updated, 1 appended)") {
		t.Fatalf("notice = %q", m.notice)
	}
}

func TestProposalsPartialFailureIsReported(t *testing.T) {
	root, ch1, _ := newTestProject(t)
	alice := secWriteState(t, root, "characters/alice.md", "# Alice\n")
	m := New()
	if err := m.OpenPath(root); err != nil {
		t.Fatal(err)
	}
	m.applyStateProposals(armProposals(t, &m,
		command.StateProposal{Category: "characters", Name: "alice", Action: "update", Proposed: "ok"},
		command.StateProposal{Category: "characters", Name: "x", Action: "update", Target: "../chapters/" + filepath.Base(ch1), Proposed: "PWNED"},
	))
	if !m.noticeIsErr || !strings.Contains(m.notice, "Applied 1 of 2") || !strings.Contains(m.notice, "1 failed") {
		t.Fatalf("notice = %q isErr=%v", m.notice, m.noticeIsErr)
	}
	if m.err == nil {
		t.Fatal("m.err not set")
	}
	if b, _ := os.ReadFile(alice); string(b) != "ok" {
		t.Fatalf("valid proposal not applied: %q", b)
	}
}

func TestProposalCommandRefusesEscapingTarget(t *testing.T) {
	root, ch1, _ := newTestProject(t)
	m := New()
	if err := m.OpenPath(root); err != nil {
		t.Fatal(err)
	}
	armProposals(t, &m, command.StateProposal{Category: "characters", Name: "x", Action: "update", Target: "../chapters/" + filepath.Base(ch1), Proposed: "PWNED"})
	for _, verb := range []string{"proposal-review", "proposal-merge", "proposal-edit"} {
		m.err = nil
		m.proposalCommand(verb, "1 PWNED")
		if m.err == nil {
			t.Fatalf("%s: expected error", verb)
		}
	}
	if b, _ := os.ReadFile(ch1); string(b) != "# Chapter 1\n\nHello" {
		t.Fatalf("chapter modified: %q", b)
	}
}
