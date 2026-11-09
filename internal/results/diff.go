package results

import "strings"

type editKind byte

const (
	equal editKind = iota
	remove
	add
)

type wordEdit struct {
	kind editKind
	text string
}

func wordDiff(a, b string) []wordEdit {
	aw, bw := strings.Fields(a), strings.Fields(b)
	dp := make([][]int, len(aw)+1)
	for i := range dp {
		dp[i] = make([]int, len(bw)+1)
	}
	for i := len(aw) - 1; i >= 0; i-- {
		for j := len(bw) - 1; j >= 0; j-- {
			if aw[i] == bw[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}
	var out []wordEdit
	for i, j := 0, 0; i < len(aw) || j < len(bw); {
		if i < len(aw) && j < len(bw) && aw[i] == bw[j] {
			out = append(out, wordEdit{equal, aw[i]})
			i++
			j++
		} else if j < len(bw) && (i == len(aw) || dp[i][j+1] > dp[i+1][j]) {
			out = append(out, wordEdit{add, bw[j]})
			j++
		} else {
			out = append(out, wordEdit{remove, aw[i]})
			i++
		}
	}
	return out
}

// markedWords wraps each run of consecutive removed/added words in a single
// [-...-] / {+...+} marker pair, so a rewritten sentence reads as one marked
// span instead of one bracket pair per word.
func markedWords(a, b string) (string, string) {
	var old, fresh, removedRun, addedRun []string
	flush := func() {
		if len(removedRun) > 0 {
			old = append(old, "[-"+strings.Join(removedRun, " ")+"-]")
			removedRun = nil
		}
		if len(addedRun) > 0 {
			fresh = append(fresh, "{+"+strings.Join(addedRun, " ")+"+}")
			addedRun = nil
		}
	}
	for _, e := range wordDiff(a, b) {
		switch e.kind {
		case equal:
			flush()
			old = append(old, e.text)
			fresh = append(fresh, e.text)
		case remove:
			removedRun = append(removedRun, e.text)
		case add:
			addedRun = append(addedRun, e.text)
		}
	}
	flush()
	return strings.Join(old, " "), strings.Join(fresh, " ")
}

func unifiedDiff(label, a, b string) []string {
	if label == "" {
		label = "selection"
	}
	aLines, bLines := strings.Split(a, "\n"), strings.Split(b, "\n")
	maxLines := len(aLines)
	if len(bLines) > maxLines {
		maxLines = len(bLines)
	}
	lines := []string{"@@ " + label + " @@"}
	for i := 0; i < maxLines; i++ {
		var x, y string
		if i < len(aLines) {
			x = aLines[i]
		}
		if i < len(bLines) {
			y = bLines[i]
		}
		if x == y {
			lines = append(lines, "  "+x)
			continue
		}
		old, fresh := markedWords(x, y)
		if i < len(aLines) {
			lines = append(lines, "- "+old)
		}
		if i < len(bLines) {
			lines = append(lines, "+ "+fresh)
		}
	}
	return lines
}
