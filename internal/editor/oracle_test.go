package editor

// Test-only oracle: verbatim copies of the pre-optimization implementations
// (commit 149951f) of the cluster, edit and wrap helpers. The differential tests
// in incremental_render_test.go compare the production fast paths against these.

import (
	"strings"
	"unicode"

	"github.com/rivo/uniseg"

	"github.com/ShayCichocki/marginctl/internal/theme"
)

func oracleGraphemes(s string) []string {
	g := uniseg.NewGraphemes(s)
	var out []string
	for g.Next() {
		out = append(out, g.Str())
	}
	return out
}

func sanitizedForTest(s string) string { return theme.Sanitize(s) }

func oracleJoin(g []string) string { return strings.Join(g, "") }

func oracleRuneLen(s string) int { return len(oracleGraphemes(s)) }

func oracleInsertRunes(s string, col int, ins string) (string, int) {
	r := oracleGraphemes(s)
	if col < 0 {
		col = 0
	}
	if col > len(r) {
		col = len(r)
	}
	prefix := oracleJoin(r[:col])
	return prefix + ins + oracleJoin(r[col:]), oracleRuneLen(prefix + ins)
}

func oracleDeleteRuneAt(s string, col int) string {
	r := oracleGraphemes(s)
	if col < 0 || col >= len(r) {
		return s
	}
	return oracleJoin(append(r[:col], r[col+1:]...))
}

// oracleSplit mirrors the string work of splitLineAtCursor.
func oracleSplit(s string, col int) (before, after string) {
	r := oracleGraphemes(s)
	if col < 0 {
		col = 0
	}
	if col > len(r) {
		col = len(r)
	}
	return oracleJoin(r[:col]), oracleJoin(r[col:])
}

func oracleClusterIsSpace(s string) bool {
	for _, r := range s {
		return unicode.IsSpace(r)
	}
	return false
}

func oracleClusterDisplayWidth(s string, column int) int {
	if s == "\t" {
		return 4 - column%4
	}
	return uniseg.StringWidth(s)
}

func oracleWrapClusters(runes []string, width int) []displayLine {
	if width < 1 {
		width = wrapWidth
	}
	if len(runes) == 0 {
		return []displayLine{{text: "", clusters: runes}}
	}
	var result []displayLine
	start := 0
	for start < len(runes) {
		w := 0
		end := start
		lastSpace := -1
		for end < len(runes) {
			rw := oracleClusterDisplayWidth(runes[end], w)
			if w+rw > width && end > start {
				break
			}
			if runes[end] == " " {
				lastSpace = end
			}
			w += rw
			end++
		}

		if end >= len(runes) {
			result = append(result, displayLine{startCol: start, endCol: end, text: oracleJoin(runes[start:end]), displayWidth: w, clusters: runes[start:end]})
			break
		}

		breakAt := end
		consumeSpace := false
		if lastSpace > start {
			breakAt = lastSpace
			consumeSpace = true
		} else if end < len(runes) && oracleClusterIsSpace(runes[end]) {
			breakAt = end
			consumeSpace = true
		}
		shownWidth := 0
		for _, r := range runes[start:breakAt] {
			shownWidth += oracleClusterDisplayWidth(r, shownWidth)
		}
		seg := displayLine{startCol: start, endCol: breakAt, text: oracleJoin(runes[start:breakAt]), displayWidth: shownWidth, clusters: runes[start:breakAt]}
		start = breakAt
		if consumeSpace && start < len(runes) && oracleClusterIsSpace(runes[start]) {
			start++
			seg.endCol = start
			seg.consumedWhitespace = 1
		}
		result = append(result, seg)
	}

	if len(result) == 0 {
		result = append(result, displayLine{text: "", clusters: runes})
	}

	return result
}

// oracleBuildDisplayLines rebuilds every display line from scratch, exactly as
// the old revision-keyed buildDisplayLines did on a cache miss.
func oracleBuildDisplayLines(lines []string, width int) ([]displayLine, []displayRange) {
	var display []displayLine
	ranges := make([]displayRange, len(lines))
	for logRow := range lines {
		ranges[logRow].start = len(display)
		// Display cluster i is raw cluster i, sanitized on its own, so raw and
		// display columns share one index space. (The pre-split oracle
		// segmented theme.Sanitize(line), whose cluster count could differ
		// from the raw line's and made the cursor/selection cells drift.)
		runes := oracleGraphemes(lines[logRow])
		for i := range runes {
			runes[i] = theme.Sanitize(runes[i])
		}
		if len(runes) == 0 {
			display = append(display, displayLine{logicalRow: logRow})
			ranges[logRow].end = len(display)
			continue
		}
		for _, seg := range oracleWrapClusters(runes, width) {
			display = append(display, displayLine{
				logicalRow: logRow,
				startCol:   seg.startCol, endCol: seg.endCol, text: seg.text,
				displayWidth: seg.displayWidth, consumedWhitespace: seg.consumedWhitespace,
				clusters: seg.clusters,
			})
		}
		ranges[logRow].end = len(display)
	}
	return display, ranges
}
