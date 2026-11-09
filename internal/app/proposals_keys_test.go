package app

import (
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ShayCichocki/marginctl/internal/command"
)

// runColon drives the real key path: ":" then the text then Enter.
func runColon(m Model, text string) Model {
	m = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{':'}})
	m = typeText(m, text)
	return update(m, tea.KeyMsg{Type: tea.KeyEnter})
}

func showTestProposals(t *testing.T, m *Model, props ...command.StateProposal) {
	t.Helper()
	m.showStateProposals(props, 0, pendingOp(m, "llm.extract-state"))
	if m.pendingAction == nil || m.focus != FocusResults {
		t.Fatalf("proposals not shown: pending=%v focus=%v notice=%q", m.pendingAction, m.focus, m.notice)
	}
}

func TestProposalAcceptViaColonFromResultsPane(t *testing.T) {
	m, _, _ := pendingModel(t)
	m = update(m, tea.WindowSizeMsg{Width: 200, Height: 40})
	alice := secWriteState(t, m.project.Root, "characters/alice.md", "# Alice\nTall.\n")
	if err := m.state.Refresh(); err != nil {
		t.Fatal(err)
	}
	showTestProposals(t, &m,
		command.StateProposal{Category: "characters", Name: "Zed", Action: "create", Summary: "new", Proposed: "# Zed\n"},
		command.StateProposal{Category: "characters", Name: "Alice", Action: "append", Summary: "more", Proposed: "Brave."},
		command.StateProposal{Category: "characters", Name: "Yan", Action: "create", Summary: "other", Proposed: "# Yan\n"},
	)
	id := m.pendingAction.ID

	m = runColon(m, "proposal-accept 2")
	if b, _ := os.ReadFile(alice); string(b) != "# Alice\nTall.\n\nBrave.\n" {
		t.Fatalf("alice = %q (append must preserve existing text)", b)
	}
	if m.pendingAction == nil || m.pendingAction.ID != id || len(m.pendingAction.Proposals) != 2 {
		t.Fatalf("remaining proposals lost: %+v", m.pendingAction)
	}
	if m.focus != FocusResults || !m.results.Visible() {
		t.Fatalf("results pane should stay focused, focus=%v", m.focus)
	}
	shown := m.results.View()
	if !strings.Contains(shown, "2 state proposal") || !strings.Contains(shown, "2. create characters/Yan") || strings.Contains(shown, "Alice") {
		t.Fatalf("pane not refreshed:\n%s", shown)
	}

	// y still applies what remains, with the original identity.
	m, cmd := updateCmd(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	if cmd == nil {
		t.Fatal("y emitted no command")
	}
	m = update(m, cmd())
	if m.pendingAction != nil {
		t.Fatal("y should apply the rest")
	}
}

func TestProposalPromptEscKeepsProposals(t *testing.T) {
	m, _, _ := pendingModel(t)
	m = update(m, tea.WindowSizeMsg{Width: 200, Height: 40})
	showTestProposals(t, &m, command.StateProposal{Category: "characters", Name: "Zed", Action: "create", Summary: "new"})
	m = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{':'}})
	if m.activePrompt() != promptCommand {
		t.Fatal("':' should open the command prompt from the results pane")
	}
	m = typeText(m, "proposal-ac")
	m = pressEsc(m)
	if m.activePrompt() != promptNone || m.pendingAction == nil || m.focus != FocusResults || !m.results.Visible() {
		t.Fatalf("esc from prompt must return to the results pane: prompt=%v focus=%v", m.activePrompt(), m.focus)
	}
	// '/' stays editor-only.
	m = update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	if m.activePrompt() != promptNone {
		t.Fatal("'/' must not open search from the results pane")
	}
}

func TestProposalSkipEditReviewViaKeysAndLastClosesPane(t *testing.T) {
	m, _, _ := pendingModel(t)
	m = update(m, tea.WindowSizeMsg{Width: 200, Height: 40})
	showTestProposals(t, &m,
		command.StateProposal{Category: "characters", Name: "Zed", Action: "create", Summary: "new"},
		command.StateProposal{Category: "characters", Name: "Yan", Action: "create", Summary: "other"},
	)
	m = runColon(m, "proposal-edit 1 Zed is tall")
	if m.pendingAction.Proposals[0].Proposed != "Zed is tall" {
		t.Fatalf("edit not applied: %+v", m.pendingAction.Proposals[0])
	}
	// Review swaps the pane to a diff but keeps the batch; Esc returns to the list.
	m = runColon(m, "proposal-review 1")
	if m.pendingAction == nil || !m.results.Visible() || m.focus != FocusResults {
		t.Fatal("review must keep the batch and pane")
	}
	m = pressEsc(m)
	if m.pendingAction == nil || !strings.Contains(m.results.View(), "1. create characters/Zed") {
		t.Fatalf("esc after review should restore the list:\n%s", m.results.View())
	}
	// :proposals also restores the list.
	m = runColon(m, "proposal-review 2")
	m = runColon(m, "proposals")
	if !strings.Contains(m.results.View(), "2. create characters/Yan") {
		t.Fatalf(":proposals should restore the list:\n%s", m.results.View())
	}
	m = runColon(m, "proposal-skip 1")
	if len(m.pendingAction.Proposals) != 1 || !strings.Contains(m.results.View(), "1. create characters/Yan") {
		t.Fatalf("renumbering failed:\n%s", m.results.View())
	}
	m = runColon(m, "proposal-skip 1")
	if m.pendingAction != nil || m.results.Visible() || m.focus != FocusEditor {
		t.Fatalf("last skip should clear and close: pending=%v visible=%v focus=%v", m.pendingAction, m.results.Visible(), m.focus)
	}
}

func TestProposalAcceptAppendPreservesExisting(t *testing.T) {
	m, _, _ := pendingModel(t)
	m = update(m, tea.WindowSizeMsg{Width: 200, Height: 40})
	tl := secWriteState(t, m.project.Root, "timeline.md", "# Timeline\nDay 1\n")
	armProposals(t, &m, command.StateProposal{Category: "timeline", Name: "t", Action: "append", Proposed: "Day 2"})
	m.executeCommand("proposal-accept 1")
	if b, _ := os.ReadFile(tl); string(b) != "# Timeline\nDay 1\n\nDay 2\n" {
		t.Fatalf("timeline = %q", b)
	}
}
