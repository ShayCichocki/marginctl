package editor

import (
	"strings"
	"unicode"
)

// charClass groups runes the way vim's word motions do: whitespace, "word"
// characters (letters/digits/underscore), and everything else as punctuation.
// A `w`/`b`/`e` motion steps between runs of the same non-space class.
type charClass int

const (
	classSpace charClass = iota
	classWord
	classPunct
)

func classOf(r rune) charClass {
	if r == ' ' || r == '\t' {
		return classSpace
	}
	if r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) {
		return classWord
	}
	return classPunct
}

func classOfCluster(s string) charClass {
	for _, r := range s {
		return classOf(r)
	}
	return classSpace
}

// motion is the resolved target of a movement command. inclusive marks whether
// an operator range should include the target character; linewise marks a
// whole-line motion (j/k/G/gg/{/}). valid is false when the key was not a motion.
type motion struct {
	row       int
	col       int
	inclusive bool
	linewise  bool
	valid     bool
}

// takeCount returns the effective repeat count (at least 1) and clears the
// pending numeric prefix.
func (m *Model) takeCount() int {
	c, _ := m.takeCountExplicit()
	return c
}

func (m *Model) takeCountExplicit() (int, bool) {
	c := m.count
	explicit := m.countExplicit
	m.count, m.countExplicit = 0, false
	if c < 1 {
		return 1, explicit
	}
	return c, explicit
}

func (m Model) lineRunes(row int) []string {
	if row < 0 || row >= len(m.lines) {
		return nil
	}
	return graphemes(m.lines[row])
}

// nextWordStart returns the position of the next word start (vim `w`), stepping
// across lines and treating a blank line as its own word.
func (m Model) nextWordStart(row, col int) (int, int) {
	r := m.lineRunes(row)
	if col < len(r) {
		c := classOfCluster(r[col])
		if c != classSpace {
			for col < len(r) && classOfCluster(r[col]) == c {
				col++
			}
		}
	}
	for {
		r = m.lineRunes(row)
		for col < len(r) && classOfCluster(r[col]) == classSpace {
			col++
		}
		if col < len(r) {
			return row, col
		}
		if row >= len(m.lines)-1 {
			return row, len(r)
		}
		row++
		col = 0
		if len(m.lines[row]) == 0 {
			return row, 0
		}
	}
}

// nextWordEnd returns the position of the next word end (vim `e`, inclusive).
func (m Model) nextWordEnd(row, col int) (int, int) {
	col++
	for {
		r := m.lineRunes(row)
		for col < len(r) && classOfCluster(r[col]) == classSpace {
			col++
		}
		if col >= len(r) {
			if row >= len(m.lines)-1 {
				if len(r) > 0 {
					return row, len(r) - 1
				}
				return row, 0
			}
			row++
			col = 0
			continue
		}
		c := classOfCluster(r[col])
		for col+1 < len(r) && classOfCluster(r[col+1]) == c {
			col++
		}
		return row, col
	}
}

// prevWordStart returns the position of the previous word start (vim `b`).
func (m Model) prevWordStart(row, col int) (int, int) {
	col--
	for {
		if col < 0 {
			if row == 0 {
				return 0, 0
			}
			row--
			col = len(m.lineRunes(row)) - 1
			if col < 0 {
				return row, 0 // blank line is a stop
			}
			continue
		}
		r := m.lineRunes(row)
		if col >= len(r) {
			col = len(r) - 1
			if col < 0 {
				return row, 0
			}
		}
		if classOfCluster(r[col]) == classSpace {
			col--
			continue
		}
		c := classOfCluster(r[col])
		for col-1 >= 0 && classOfCluster(r[col-1]) == c {
			col--
		}
		return row, col
	}
}

// nextParagraph returns the row of the next blank line at or after row+1, or the
// last row.
func (m Model) nextParagraph(row int) int {
	for r := row + 1; r < len(m.lines); r++ {
		if strings.TrimSpace(m.lines[r]) == "" {
			return r
		}
	}
	return len(m.lines) - 1
}

// prevParagraph returns the row of the previous blank line at or before row-1,
// or the first row.
func (m Model) prevParagraph(row int) int {
	for r := row - 1; r >= 0; r-- {
		if strings.TrimSpace(m.lines[r]) == "" {
			return r
		}
	}
	return 0
}

var bracketPairs = map[rune]struct {
	match   rune
	forward bool
}{
	'(': {')', true},
	'[': {']', true},
	'{': {'}', true},
	')': {'(', false},
	']': {'[', false},
	'}': {'{', false},
}

// matchBracket implements `%`: from the first bracket at or after the cursor on
// the current line, find its matching partner (scanning across lines) honoring
// nesting. ok is false when there is no bracket to match.
func (m Model) matchBracket(row, col int) (int, int, bool) {
	r := m.lineRunes(row)
	// Find the first bracket at or after the cursor on this line.
	start := col
	for start < len(r) {
		if _, isBracket := bracketPairs[firstRune(r[start])]; isBracket {
			break
		}
		start++
	}
	if start >= len(r) {
		return 0, 0, false
	}

	open := firstRune(r[start])
	info := bracketPairs[open]
	depth := 0
	cr, cc := row, start
	for {
		rr := m.lineRunes(cr)
		switch firstRune(rr[cc]) {
		case open:
			depth++
		case info.match:
			depth--
			if depth == 0 {
				return cr, cc, true
			}
		}
		if info.forward {
			cc++
			for cc >= len(rr) {
				cr++
				if cr >= len(m.lines) {
					return 0, 0, false
				}
				rr = m.lineRunes(cr)
				cc = 0 // an empty line leaves cc >= len(rr), so keep advancing
			}
		} else {
			cc--
			for cc < 0 {
				cr--
				if cr < 0 {
					return 0, 0, false
				}
				rr = m.lineRunes(cr)
				cc = len(rr) - 1
			}
		}
	}
}

// computeMotion resolves a movement key into a target relative to the cursor,
// applying count. Returns valid=false if key is not a motion.
func (m Model) computeMotion(key string, count int, countExplicit bool) motion {
	row, col := m.cursorRow, m.cursorCol
	switch key {
	case "w":
		for i := 0; i < count; i++ {
			row, col = m.nextWordStart(row, col)
		}
		return motion{row: row, col: col, valid: true}
	case "b":
		for i := 0; i < count; i++ {
			row, col = m.prevWordStart(row, col)
		}
		return motion{row: row, col: col, valid: true}
	case "e":
		for i := 0; i < count; i++ {
			row, col = m.nextWordEnd(row, col)
		}
		return motion{row: row, col: col, inclusive: true, valid: true}
	case "h", "left":
		col -= count
		if col < 0 {
			col = 0
		}
		return motion{row: row, col: col, valid: true}
	case "l", "right":
		col += count
		if l := runeLen(m.lines[row]); col > l {
			col = l
		}
		return motion{row: row, col: col, valid: true}
	case "0":
		return motion{row: row, col: 0, valid: true}
	case "$":
		for i := 1; i < count && row < len(m.lines)-1; i++ {
			row++
		}
		last := runeLen(m.lines[row]) - 1
		if last < 0 {
			last = 0
		}
		return motion{row: row, col: last, inclusive: true, valid: true}
	case "j", "down":
		row += count
		if row > len(m.lines)-1 {
			row = len(m.lines) - 1
		}
		return motion{row: row, col: col, linewise: true, valid: true}
	case "k", "up":
		row -= count
		if row < 0 {
			row = 0
		}
		return motion{row: row, col: col, linewise: true, valid: true}
	case "G":
		row = len(m.lines) - 1
		if countExplicit {
			// A count with G means "go to line N" (1-based).
			row = count - 1
		}
		if row < 0 {
			row = 0
		}
		if row > len(m.lines)-1 {
			row = len(m.lines) - 1
		}
		return motion{row: row, col: firstNonBlank(m.lines[row]), linewise: true, valid: true}
	case "{":
		for i := 0; i < count; i++ {
			row = m.prevParagraph(row)
		}
		return motion{row: row, col: 0, valid: true}
	case "}":
		for i := 0; i < count; i++ {
			row = m.nextParagraph(row)
		}
		return motion{row: row, col: 0, valid: true}
	case "%":
		if tr, tc, ok := m.matchBracket(row, col); ok {
			return motion{row: tr, col: tc, inclusive: true, valid: true}
		}
		return motion{valid: false}
	}
	return motion{valid: false}
}
