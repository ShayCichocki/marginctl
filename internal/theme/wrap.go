package theme

import (
	"strings"

	"github.com/mattn/go-runewidth"
)

// Wrap soft-wraps a single logical line (which must not itself contain newlines)
// to at most maxWidth terminal cells per output line, returning one or more
// lines. It is rune- and display-width-aware: it prefers to break at spaces, but
// falls back to breaking inside a word that is itself wider than maxWidth (e.g. a
// long URL or an unbroken JSON error body) so nothing overflows the pane. A
// non-positive maxWidth returns the input unchanged as a single line. An empty
// input yields a single empty line, so blank paragraph separators survive.
func Wrap(s string, maxWidth int) []string {
	if maxWidth <= 0 {
		return []string{s}
	}
	if runewidth.StringWidth(s) <= maxWidth {
		return []string{s}
	}

	var lines []string
	var line strings.Builder
	lineWidth := 0

	flush := func() {
		lines = append(lines, line.String())
		line.Reset()
		lineWidth = 0
	}

	// Split on spaces but keep the ability to hard-break over-long words.
	for _, word := range splitPreservingSpaces(s, maxWidth) {
		w := runewidth.StringWidth(word)
		if lineWidth > 0 && lineWidth+w > maxWidth {
			flush()
		}
		line.WriteString(word)
		lineWidth += w
	}
	if line.Len() > 0 || len(lines) == 0 {
		flush()
	}
	return lines
}

// splitPreservingSpaces breaks s into tokens no wider than maxWidth. Runs of
// text are split at spaces (the space stays attached to the preceding token so
// wrapping collapses it at line ends), and any single token still wider than
// maxWidth is hard-split at a cell boundary.
func splitPreservingSpaces(s string, maxWidth int) []string {
	var tokens []string
	for _, field := range splitAfterSpace(s) {
		if runewidth.StringWidth(field) <= maxWidth {
			tokens = append(tokens, field)
			continue
		}
		tokens = append(tokens, hardSplit(field, maxWidth)...)
	}
	return tokens
}

// splitAfterSpace splits s into tokens, keeping each trailing space attached to
// its token (so "a b " -> ["a ", "b "]).
func splitAfterSpace(s string) []string {
	var tokens []string
	var cur strings.Builder
	for _, r := range s {
		cur.WriteRune(r)
		if r == ' ' {
			tokens = append(tokens, cur.String())
			cur.Reset()
		}
	}
	if cur.Len() > 0 {
		tokens = append(tokens, cur.String())
	}
	return tokens
}

// hardSplit breaks a single over-long token into chunks of at most maxWidth
// cells, never slicing through a rune.
func hardSplit(s string, maxWidth int) []string {
	var chunks []string
	var cur strings.Builder
	curWidth := 0
	for _, r := range s {
		rw := runewidth.RuneWidth(r)
		if curWidth+rw > maxWidth && cur.Len() > 0 {
			chunks = append(chunks, cur.String())
			cur.Reset()
			curWidth = 0
		}
		cur.WriteRune(r)
		curWidth += rw
	}
	if cur.Len() > 0 {
		chunks = append(chunks, cur.String())
	}
	return chunks
}
