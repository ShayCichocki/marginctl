package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

func contains(xs []string, want string) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}

func TestMatchColonPrefix(t *testing.T) {
	// Empty prefix returns the whole (sorted) set.
	all := matchColonPrefix("")
	if !contains(all, "w") || !contains(all, "ShowChapters") || !contains(all, "InitState") || !contains(all, "Compile") || !contains(all, "connect") || !contains(all, "disconnect") || !contains(all, "auth") {
		t.Fatalf("empty prefix missing expected verbs: %v", all)
	}
	for i := 1; i < len(all); i++ {
		if all[i-1] > all[i] {
			t.Fatalf("results not sorted: %v", all)
		}
	}

	// A unique prefix returns exactly one match.
	if got := matchColonPrefix("Comp"); len(got) != 1 || got[0] != "Compile" {
		t.Fatalf(`matchColonPrefix("Comp") = %v, want [Compile]`, got)
	}

	// A shared prefix returns all its matches.
	got := matchColonPrefix("Show")
	for _, want := range []string{"ShowChapters", "ShowOutline", "ShowState"} {
		if !contains(got, want) {
			t.Fatalf(`matchColonPrefix("Show") = %v, missing %s`, got, want)
		}
	}

	// Case-sensitive: lowercase "show" matches nothing (aliases are PascalCase).
	if got := matchColonPrefix("show"); len(got) != 0 {
		t.Fatalf(`matchColonPrefix("show") = %v, want empty (case-sensitive)`, got)
	}
}

func TestMatchColonPrefixIncludesAuthVerbs(t *testing.T) {
	// Given
	all := allColonVerbs()

	// Then
	for _, want := range []string{"auth", "connect", "disconnect"} {
		if !contains(all, want) {
			t.Fatalf("colon verbs missing %q: %v", want, all)
		}
	}
	if got := matchColonPrefix("con"); !contains(got, "connect") {
		t.Fatalf(`matchColonPrefix("con") = %v, want connect`, got)
	}
}

// enterCommand puts the model into ":" command mode with the given typed prefix.
func enterCommand(m Model, prefix string) Model {
	m = update(m, keyRunes(":"))
	for _, r := range prefix {
		m = update(m, keyRunes(string(r)))
	}
	return m
}

func tab(m Model) Model      { return update(m, tea.KeyMsg{Type: tea.KeyTab}) }
func shiftTab(m Model) Model { return update(m, tea.KeyMsg{Type: tea.KeyShiftTab}) }

func TestTabCompletesUniquePrefix(t *testing.T) {
	m, _, _ := openTestModel(t)
	m = enterCommand(m, "Comp")
	m = tab(m)
	if m.commandBuffer != "Compile" {
		t.Fatalf("Tab on 'Comp' → %q, want 'Compile'", m.commandBuffer)
	}
}

func TestTabCyclesSharedPrefix(t *testing.T) {
	m, _, _ := openTestModel(t)
	m = enterCommand(m, "Show")

	m = tab(m)
	first := m.commandBuffer
	m = tab(m)
	second := m.commandBuffer

	if first == second {
		t.Fatalf("Tab did not advance: both %q", first)
	}
	for _, got := range []string{first, second} {
		if !strings.HasPrefix(got, "Show") {
			t.Fatalf("completion %q does not start with 'Show'", got)
		}
	}

	// Shift+Tab steps back to the first candidate.
	m = shiftTab(m)
	if m.commandBuffer != first {
		t.Fatalf("Shift+Tab → %q, want back to %q", m.commandBuffer, first)
	}
}

func TestEditingResetsCompletionCycle(t *testing.T) {
	m, _, _ := openTestModel(t)
	m = enterCommand(m, "Show")
	m = tab(m) // completes to a ShowX; completionActive now true
	if !m.completionActive {
		t.Fatal("expected an active completion cycle after Tab")
	}
	// Any edit must reset the cycle so the next Tab recomputes.
	m = update(m, tea.KeyMsg{Type: tea.KeyBackspace})
	if m.completionActive {
		t.Fatal("editing the buffer should reset the completion cycle")
	}
}

func TestTabNoCompletionInArg(t *testing.T) {
	m, _, _ := openTestModel(t)
	m = enterCommand(m, "w ")
	m = update(m, keyRunes("f"))
	before := m.commandBuffer
	m = tab(m)
	if m.commandBuffer != before {
		t.Fatalf("Tab in the arg region changed buffer: %q → %q", before, m.commandBuffer)
	}
}

func TestCommandBarShowsCompletionHint(t *testing.T) {
	m, _, _ := openTestModel(t)
	m.width = 120
	m = enterCommand(m, "Show")
	m = tab(m)

	out := m.commandBarView()
	if !strings.Contains(out, "ShowOutline") || !strings.Contains(out, "ShowState") {
		t.Fatalf("completion hint should list the other candidates, got:\n%s", out)
	}
	// The selected candidate is bracketed.
	if !strings.Contains(out, "["+m.commandBuffer+"]") {
		t.Fatalf("selected candidate %q should be bracketed in the hint:\n%s", m.commandBuffer, out)
	}
}

// A PascalCase alias dispatches its registry command; :ShowChapters must behave
// like the palette's chapter.list.
func TestColonAliasDispatch(t *testing.T) {
	m, _, _ := openTestModel(t)
	for verb, id := range colonAliases {
		if m.registry.Get(id) == nil {
			t.Fatalf("alias :%s → %s not registered in the palette", verb, id)
		}
	}

	// :Compile runs manuscript.compile end-to-end.
	m.executeCommand("Compile")
	// (compile writes manuscript.md; exercised more fully in TestManuscriptSearchAndCompile —
	// here we only assert it isn't reported as unknown.)
	if strings.Contains(m.notice, "unknown command") {
		t.Fatalf(":Compile reported unknown: %q", m.notice)
	}

	// Back-compat: lowercase :compile still works but is hidden from completion.
	m.executeCommand("compile")
	if strings.Contains(m.notice, "unknown command") {
		t.Fatalf(":compile (back-compat) reported unknown: %q", m.notice)
	}
	if contains(allColonVerbs(), "compile") {
		t.Fatal("hidden alias 'compile' should not appear in completion set")
	}
}

// Cursor movement inside the ":" prompt does not change the buffer, so it must
// keep the Tab-completion cycle; text edits must reset it.
func TestCompletionCycleSurvivesCursorMotionButNotEdits(t *testing.T) {
	cursorKeys := map[string]tea.KeyMsg{
		"left":   {Type: tea.KeyLeft},
		"right":  {Type: tea.KeyRight},
		"home":   {Type: tea.KeyHome},
		"end":    {Type: tea.KeyEnd},
		"ctrl+a": {Type: tea.KeyCtrlA},
	}
	for name, key := range cursorKeys {
		t.Run("keeps/"+name, func(t *testing.T) {
			m, _, _ := openTestModel(t)
			m = tab(enterCommand(m, "Show"))
			if !m.completionActive {
				t.Fatal("expected an active cycle after Tab")
			}
			matches := append([]string(nil), m.completionMatches...)
			m = update(m, key)
			if !m.completionActive || len(m.completionMatches) != len(matches) {
				t.Fatalf("%s reset the completion cycle", name)
			}
		})
	}
	editKeys := map[string]tea.KeyMsg{
		"backspace": {Type: tea.KeyBackspace},
		"space":     {Type: tea.KeySpace},
		"rune":      {Type: tea.KeyRunes, Runes: []rune{'x'}},
		"ctrl+w":    {Type: tea.KeyCtrlW},
	}
	for name, key := range editKeys {
		t.Run("resets/"+name, func(t *testing.T) {
			m, _, _ := openTestModel(t)
			m = tab(enterCommand(m, "Show"))
			m = update(m, key)
			if m.completionActive || m.completionMatches != nil {
				t.Fatalf("%s did not reset the completion cycle", name)
			}
		})
	}
	// Forward deletes need the cursor inside the text.
	for name, key := range map[string]tea.KeyMsg{"delete": {Type: tea.KeyDelete}, "ctrl+d": {Type: tea.KeyCtrlD}} {
		t.Run("resets/"+name, func(t *testing.T) {
			m, _, _ := openTestModel(t)
			m = tab(enterCommand(m, "Show"))
			m = update(m, tea.KeyMsg{Type: tea.KeyLeft})
			if !m.completionActive {
				t.Fatal("cursor motion reset the cycle")
			}
			m = update(m, key)
			if m.completionActive || m.completionMatches != nil {
				t.Fatalf("%s did not reset the completion cycle", name)
			}
		})
	}
	t.Run("resets/history", func(t *testing.T) {
		m, _, _ := openTestModel(t)
		m = update(enterCommand(m, "Sh"), tea.KeyMsg{Type: tea.KeyEnter})
		m = tab(enterCommand(m, "Show"))
		if !m.completionActive {
			t.Fatal("expected an active cycle after Tab")
		}
		m = update(m, tea.KeyMsg{Type: tea.KeyUp})
		if m.commandBuffer != "Sh" || m.completionActive || m.completionMatches != nil {
			t.Fatalf("history up: buffer=%q active=%v", m.commandBuffer, m.completionActive)
		}
		// Down returns to the draft; with a cycle started on the recalled text
		// the buffer changes again and the cycle must reset again.
		m = tab(m)
		m.commandBuffer = "Sh"
		m.prompt.SetValue("Sh")
		m = update(m, tea.KeyMsg{Type: tea.KeyDown})
		if m.completionActive || m.completionMatches != nil {
			t.Fatalf("history down did not reset the cycle (buffer=%q)", m.commandBuffer)
		}
	})
}
