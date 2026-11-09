package editor

import "testing"

// TestClampIndependentOfDisplayCacheWarmth pins that the post-edit cursor clamp
// does not read a stale display cache. On lines whose sanitized and raw grapheme
// counts differ (here CR followed by a ZWJ, which merge once the CR is shown as
// a control picture), the revision bumps only after the key handler returns, so
// a warm cache used to describe the pre-edit line.
func TestClampIndependentOfDisplayCacheWarmth(t *testing.T) {
	const line = "\r\u200d世c\ufe0fa"
	for _, keys := range [][]string{{"x"}, {"3x"}, {"dl"}, {"D"}, {"rz"}} {
		var cur [2]int
		for i, warm := range []bool{true, false} {
			m := New()
			m.SetSize(40, 10)
			m.SetContent(line)
			m.cursorCol = 3
			if warm {
				m.buildDisplayLines()
			} else {
				m.rowCache, m.displayCache, m.displayRowRanges = nil, nil, nil
			}
			m = press(m, keys...)
			cur[i] = m.cursorCol
			if n := runeLen(m.lines[0]); n > 0 && m.cursorCol > n-1 {
				t.Fatalf("%v warm=%v: cursorCol %d beyond last grapheme of %q", keys, warm, m.cursorCol, m.lines[0])
			}
		}
		if cur[0] != cur[1] {
			t.Fatalf("%v: warm cursorCol=%d, cold cursorCol=%d", keys, cur[0], cur[1])
		}
	}
}

// TestVisualEndMotionsStayWithinRawLine: Prepend characters such as U+0603 merge
// with the next rune in the raw line but not once sanitized, so visual-mode
// $ / G / l must bound the cursor by the raw grapheme count.
func TestVisualEndMotionsStayWithinRawLine(t *testing.T) {
	for _, keys := range [][]string{{"v", "$"}, {"v", "G"}, {"v", "l", "l"}} {
		m := New()
		m.SetSize(40, 10)
		m.SetContent("\u06030")
		m = press(m, keys...)
		if n := runeLen(m.lines[0]); m.cursorCol > n {
			t.Fatalf("%v: cursorCol %d beyond raw length %d", keys, m.cursorCol, n)
		}
	}
}
