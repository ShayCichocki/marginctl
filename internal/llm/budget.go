package llm

import (
	"fmt"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ShayCichocki/marginctl/internal/state"
)

// maxContinuityInputTokens bounds the *estimated* token size of a continuity
// request (chapter + selected state). It keeps a large story bible from blowing
// past provider context limits (400s) and bounds cost. A package var so tests
// can shrink it; the default is generous but well under a 1M-context model.
var maxContinuityInputTokens = 60000

// estimateTokens is a cheap, offline heuristic (~4 chars per token). There is no
// local tokenizer, so this deliberately runs a touch high to keep the real
// request under the ceiling rather than under it.
func estimateTokens(s string) int {
	return (len(s) + 3) / 4
}

// wordSet lowercases s and splits it into a set of alphanumeric words.
func wordSet(s string) map[string]struct{} {
	set := make(map[string]struct{})
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		set[w] = struct{}{}
	}
	return set
}

// entityName returns the name segment of a "category/name" state key.
func entityName(key string) string {
	if i := strings.LastIndex(key, "/"); i >= 0 {
		return key[i+1:]
	}
	return key
}

// allWordsPresent reports whether every word in want appears in have (and want
// is non-empty) — used to detect a full name/alias mention in the chapter.
func allWordsPresent(have, want map[string]struct{}) bool {
	if len(want) == 0 {
		return false
	}
	for w := range want {
		if _, ok := have[w]; !ok {
			return false
		}
	}
	return true
}

// relevanceScore ranks a state entry against the chapter. A direct mention of the
// entity's name — or any of its front-matter aliases — is weighted heavily so a
// chapter that refers to a character only by an alias (e.g. "Jack" for a file
// named "jonathan" with `aliases: Jack`) still pulls in that entry. Otherwise the
// score is the number of distinct content terms (length > 3) shared with the
// chapter. Higher is more relevant.
func relevanceScore(chapterWords map[string]struct{}, key, content string) int {
	score := 0

	nameHit := allWordsPresent(chapterWords, wordSet(entityName(key)))
	if !nameHit {
		fields, _ := state.ParseFrontMatter(content)
		for _, alias := range splitAliases(fields["aliases"]) {
			if allWordsPresent(chapterWords, wordSet(alias)) {
				nameHit = true
				break
			}
		}
	}
	if nameHit {
		score += 1000
	}

	for w := range wordSet(content) {
		if len(w) <= 3 {
			continue
		}
		if _, ok := chapterWords[w]; ok {
			score++
		}
	}
	return score
}

// splitAliases splits a front-matter `aliases` value on commas/semicolons into
// trimmed, non-empty alias strings.
func splitAliases(s string) []string {
	var out []string
	for _, a := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' }) {
		if a = strings.TrimSpace(a); a != "" {
			out = append(out, a)
		}
	}
	return out
}

// truncateToTokens cuts s to at most maxTokens' worth of characters, backing up
// to a rune boundary so multibyte characters (em-dash, curly quotes, é) are never
// split.
func truncateToTokens(s string, maxTokens int) string {
	maxBytes := maxTokens * 4
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	b := maxBytes
	for b > 0 && !utf8.RuneStart(s[b]) {
		b--
	}
	return s[:b]
}

// budgetContinuity trims a continuity request to fit an estimated token budget.
// The chapter (the thing being checked) is kept first, truncated rune-safely if
// it alone exceeds the budget; the remaining room is filled with the most
// relevant state entries by relevanceScore. A bible that already fits is returned
// unchanged. notes describes any truncation/omission so the caller can surface it.
func budgetContinuity(chapter string, stateFiles map[string]string, maxTokens int) (string, map[string]string, []string) {
	var notes []string

	// Reserve headroom for the system prompt + XML fencing/formatting overhead.
	const overhead = 1500
	budget := maxTokens - overhead
	if budget < 0 {
		budget = 0
	}

	if estimateTokens(chapter) > budget {
		chapter = truncateToTokens(chapter, budget)
		notes = append(notes, "⚠ chapter truncated to fit the context budget — continuity check is partial")
	}
	remaining := budget - estimateTokens(chapter)
	if remaining < 0 {
		remaining = 0
	}

	type scored struct {
		key   string
		score int
	}
	chapterWords := wordSet(chapter)
	ranked := make([]scored, 0, len(stateFiles))
	for k, v := range stateFiles {
		ranked = append(ranked, scored{k, relevanceScore(chapterWords, k, v)})
	}
	// Most relevant first; ties broken by key for a deterministic selection.
	sort.Slice(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].key < ranked[j].key
	})

	selected := make(map[string]string, len(stateFiles))
	dropped := 0
	for _, r := range ranked {
		// +20 tokens for the per-entry "### category/name" header + fence tags.
		cost := estimateTokens(stateFiles[r.key]) + estimateTokens(r.key) + 20
		if cost > remaining {
			dropped++
			continue
		}
		selected[r.key] = stateFiles[r.key]
		remaining -= cost
	}
	if dropped > 0 {
		notes = append(notes, fmt.Sprintf(
			"⚠ %d of %d state entries omitted to fit the context budget (kept the most relevant)",
			dropped, len(stateFiles)))
	}

	return chapter, selected, notes
}

// BudgetContinuity trims a continuity request to the package's configured
// token budget (see budgetContinuity). It lets callers outside llm (the
// llmcmd command adapter) use the budget without reaching into internals.
func BudgetContinuity(chapter string, stateFiles map[string]string) (string, map[string]string, []string) {
	return budgetContinuity(chapter, stateFiles, maxContinuityInputTokens)
}
