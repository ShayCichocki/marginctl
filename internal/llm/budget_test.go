package llm

import (
	"strings"
	"testing"
)

// A bible that already fits the budget must pass through untouched — same
// behavior as before budgeting existed, no notes.
func TestBudgetKeepsSmallBibleWhole(t *testing.T) {
	state := map[string]string{
		"characters/york":  "Lord York, an aging duke.",
		"locations/harbor": "A cold northern harbor.",
	}
	chapter := "York walked to the harbor at dawn."

	gotChapter, gotState, notes := budgetContinuity(chapter, state, maxContinuityInputTokens)
	if gotChapter != chapter {
		t.Fatalf("chapter changed: %q", gotChapter)
	}
	if len(gotState) != len(state) {
		t.Fatalf("kept %d entries, want all %d", len(gotState), len(state))
	}
	if len(notes) != 0 {
		t.Fatalf("unexpected notes: %v", notes)
	}
}

// Over budget, the most relevant entries (named in the chapter) survive and the
// irrelevant filler is dropped with a note.
func TestBudgetDropsIrrelevantWhenOverBudget(t *testing.T) {
	orig := maxContinuityInputTokens
	maxContinuityInputTokens = 1800 // overhead is 1500, so ~300 tokens of room
	defer func() { maxContinuityInputTokens = orig }()

	filler := strings.Repeat("irrelevant background lore about nothing. ", 40)
	state := map[string]string{
		"characters/york":     "Lord York, an aging duke with a silver beard.",
		"characters/mabel":    filler,
		"locations/elsewhere": filler,
		"locations/nowhere":   filler,
	}
	chapter := "York stroked his silver beard and considered the duke's next move."

	_, gotState, notes := budgetContinuity(chapter, state, maxContinuityInputTokens)

	if _, ok := gotState["characters/york"]; !ok {
		t.Fatalf("the relevant entry (characters/york) was dropped; kept: %v", keys(gotState))
	}
	if len(gotState) >= len(state) {
		t.Fatalf("nothing was dropped despite the tight budget; kept %d of %d", len(gotState), len(state))
	}
	if !hasNote(notes, "omitted") {
		t.Fatalf("expected an omission note, got: %v", notes)
	}
}

// A chapter larger than the whole budget is truncated (rune-safe) with a note,
// rather than sent oversized.
func TestBudgetTruncatesHugeChapter(t *testing.T) {
	orig := maxContinuityInputTokens
	maxContinuityInputTokens = 2000 // 500 tokens ≈ 2000 chars after overhead
	defer func() { maxContinuityInputTokens = orig }()

	chapter := strings.Repeat("The rain fell on the quiet town. ", 500) // ~16k chars
	state := map[string]string{"characters/york": "Lord York."}

	gotChapter, _, notes := budgetContinuity(chapter, state, maxContinuityInputTokens)
	if len(gotChapter) >= len(chapter) {
		t.Fatalf("chapter was not truncated (%d >= %d)", len(gotChapter), len(chapter))
	}
	if estimateTokens(gotChapter) > maxContinuityInputTokens {
		t.Fatalf("truncated chapter still exceeds the budget: %d tokens", estimateTokens(gotChapter))
	}
	if !hasNote(notes, "truncated") {
		t.Fatalf("expected a truncation note, got: %v", notes)
	}
}

// truncateToTokens must never split a multibyte rune.
func TestTruncateIsRuneSafe(t *testing.T) {
	s := strings.Repeat("é—", 100) // all multibyte
	got := truncateToTokens(s, 10)
	if got == "" || got == s {
		t.Fatalf("unexpected truncation length: %d", len(got))
	}
	for _, r := range got {
		if r == '�' {
			t.Fatal("truncation produced an invalid rune")
		}
	}
	if !strings.HasPrefix(s, got) {
		t.Fatal("truncated string is not a prefix of the original")
	}
}

// relevanceScore must rank a chapter-named entity above unrelated filler.
func TestRelevanceRanksNamedEntityHighest(t *testing.T) {
	chapterWords := wordSet("York rode north through the storm.")
	named := relevanceScore(chapterWords, "characters/york", "A duke.")
	unrelated := relevanceScore(chapterWords, "characters/mabel", "A baker in a distant village.")
	if named <= unrelated {
		t.Fatalf("named entity score %d not above unrelated %d", named, unrelated)
	}
}

func keys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func hasNote(notes []string, substr string) bool {
	for _, n := range notes {
		if strings.Contains(n, substr) {
			return true
		}
	}
	return false
}

// 4.2 — a character referenced in the chapter only by a front-matter alias is
// still ranked relevant (name-level boost), so it survives a tight budget over
// an entry that isn't mentioned at all.
func TestBudgetUsesFrontMatterAliases(t *testing.T) {
	orig := maxContinuityInputTokens
	maxContinuityInputTokens = 1800
	defer func() { maxContinuityInputTokens = orig }()

	filler := strings.Repeat("unrelated background lore about nothing here. ", 40)
	stateFiles := map[string]string{
		// File is named "jonathan" but the chapter only ever says "Jack".
		"characters/jonathan": "---\nname: Jonathan\naliases: Jack, Johnny\n---\n\n# Jonathan\n",
		"characters/other":    filler,
		"locations/far":       filler,
	}
	chapter := "Jack drew his blade and stepped into the light."

	_, gotState, _ := budgetContinuity(chapter, stateFiles, maxContinuityInputTokens)

	if _, ok := gotState["characters/jonathan"]; !ok {
		t.Fatalf("alias-referenced entry was dropped; kept: %v", keys(gotState))
	}
}
