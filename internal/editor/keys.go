package editor

import (
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
)

func (m *Model) handleInsertMode(msg tea.KeyMsg) {
	// The first buffer-mutating key in an insert session commits the armed
	// pre-insert snapshot; movement/escape leave the session undo-neutral.
	switch msg.Type {
	case tea.KeyBackspace, tea.KeyDelete, tea.KeyEnter, tea.KeySpace, tea.KeyTab, tea.KeyRunes:
		m.commitInsertUndo()
	}

	switch msg.Type {
	case tea.KeyEscape:
		m.Mode = ModeNormal
		m.insertUndoArmed = false
		if m.cursorCol > 0 {
			m.cursorCol--
		}
		m.clampNormalCol()

	case tea.KeyBackspace:
		if m.cursorCol > 0 {
			m.lines[m.cursorRow] = deleteRuneAt(m.lines[m.cursorRow], m.cursorCol-1)
			m.cursorCol--
		} else if m.cursorRow > 0 {
			prevLen := runeLen(m.lines[m.cursorRow-1])
			m.lines[m.cursorRow-1] = m.lines[m.cursorRow-1] + m.lines[m.cursorRow]
			m.cursorCol = prevLen
			m.lines = append(m.lines[:m.cursorRow], m.lines[m.cursorRow+1:]...)
			m.cursorRow--
		}

	case tea.KeyDelete:
		if m.cursorCol < runeLen(m.lines[m.cursorRow]) {
			m.lines[m.cursorRow] = deleteRuneAt(m.lines[m.cursorRow], m.cursorCol)
		} else if m.cursorRow < len(m.lines)-1 {
			m.lines[m.cursorRow] = m.lines[m.cursorRow] + m.lines[m.cursorRow+1]
			m.lines = append(m.lines[:m.cursorRow+1], m.lines[m.cursorRow+2:]...)
		}

	case tea.KeyEnter:
		m.splitLineAtCursor()

	case tea.KeyLeft:
		if m.cursorCol > 0 {
			m.cursorCol--
		}

	case tea.KeyRight:
		if m.cursorCol < runeLen(m.lines[m.cursorRow]) {
			m.cursorCol++
		}

	case tea.KeyUp:
		m.moveUpDisplayLine()

	case tea.KeyDown:
		m.moveDownDisplayLine()

	case tea.KeySpace:
		m.lines[m.cursorRow], m.cursorCol = insertRunes(m.lines[m.cursorRow], m.cursorCol, " ")

	case tea.KeyTab:
		m.lines[m.cursorRow], m.cursorCol = insertRunes(m.lines[m.cursorRow], m.cursorCol, "    ")

	case tea.KeyRunes:
		for _, r := range msg.Runes {
			switch {
			case r == '\r':
				// Drop carriage returns from a paste; the \n handles the break.
				continue
			case r == '\n':
				// A multi-line paste arrives as runes with embedded newlines;
				// split the line at the cursor instead of storing a literal \n.
				m.splitLineAtCursor()
			case unicode.IsControl(r):
				continue
			default:
				m.lines[m.cursorRow], m.cursorCol = insertRunes(m.lines[m.cursorRow], m.cursorCol, string(r))
			}
		}
	}

	m.ensureCursorVisible()
}

func (m *Model) handleNormalMode(msg tea.KeyMsg) {
	key := msg.String()

	// `r<char>`: the next key supplies the replacement character.
	if m.pendingReplace {
		m.pendingReplace = false
		n := m.opCount
		m.opCount = 0
		if n < 1 {
			n = 1
		}
		if repl, ok := replacementRune(msg); ok {
			m.applyReplace(repl, n)
		}
		m.clampNormalCol()
		m.ensureCursorVisible()
		return
	}

	// Second half of a `g`-prefixed command (gg / Ngg / dgg / ygg / cgg).
	if m.pendingG {
		m.pendingG = false
		if key == "g" {
			if m.pendingOp != "" {
				op := m.pendingOp
				m.pendingOp, m.opCount, m.count, m.countExplicit = "", 0, 0, false
				m.applyLineOperator(op, 0) // operate from cursor row up to line 0
			} else {
				row := 0
				if m.count > 0 {
					row = m.count - 1
				}
				m.count, m.countExplicit = 0, false
				if row > len(m.lines)-1 {
					row = len(m.lines) - 1
				}
				if row < 0 {
					row = 0
				}
				m.cursorRow = row
				m.cursorCol = firstNonBlank(m.lines[row])
			}
			if m.Mode == ModeNormal {
				m.clampNormalCol()
			}
			m.ensureCursorVisible()
			return
		}
		// A lone `g` (or `dg` not followed by g) cancels; drop any operator too.
		m.pendingOp, m.opCount = "", 0
	}

	// Numeric prefix. `0` is the line-start motion unless a count is building.
	if len(key) == 1 && key[0] >= '0' && key[0] <= '9' && (key != "0" || m.count != 0) {
		m.count = m.count*10 + int(key[0]-'0')
		m.countExplicit = true
		if m.count > 100000 {
			m.count = 100000
		}
		return
	}

	// Operator pending (d/c/y): resolve a doubled operator, gg, or a motion.
	if m.pendingOp != "" {
		op := m.pendingOp
		if key == "g" {
			m.pendingG = true // wait for the second g (dgg)
			return
		}
		if key == op { // dd / cc / yy — linewise over count lines
			m.pendingOp = ""
			n := max(m.opCount, 1) * m.takeCount()
			m.opCount, m.opCountExplicit = 0, false
			m.applyLineOperator(op, m.cursorRow+n-1)
			if m.Mode == ModeNormal {
				m.clampNormalCol()
			}
			m.ensureCursorVisible()
			return
		}
		motionCount, motionExplicit := m.takeCountExplicit()
		count := max(m.opCount, 1) * motionCount
		explicit := m.opCountExplicit || motionExplicit
		m.pendingOp, m.opCount, m.opCountExplicit = "", 0, false
		// vim quirk: `cw` on a word acts like `ce` — it changes to the end of the
		// word without swallowing the trailing whitespace.
		motionKey := key
		if op == "c" && key == "w" && m.cursorCol < runeLen(m.lines[m.cursorRow]) {
			motionKey = "e"
		}
		mo := m.computeMotion(motionKey, count, explicit)
		if mo.valid {
			m.applyOperator(op, mo)
		}
		if m.Mode == ModeNormal {
			m.clampNormalCol()
		}
		m.ensureCursorVisible()
		return
	}

	switch key {
	case "i":
		m.armInsertUndo()
		m.Mode = ModeInsert

	case "a":
		m.armInsertUndo()
		m.Mode = ModeInsert
		if m.cursorCol < runeLen(m.lines[m.cursorRow]) {
			m.cursorCol++
		}

	case "A":
		m.armInsertUndo()
		m.Mode = ModeInsert
		m.cursorCol = runeLen(m.lines[m.cursorRow])

	case "I":
		m.armInsertUndo()
		m.Mode = ModeInsert
		m.cursorCol = firstNonBlank(m.lines[m.cursorRow])

	case "o":
		// The new line is itself an edit, so commit an undo point now; the
		// subsequent typing coalesces into it.
		m.armInsertUndo()
		m.commitInsertUndo()
		newLines := make([]string, len(m.lines)+1)
		copy(newLines, m.lines[:m.cursorRow+1])
		newLines[m.cursorRow+1] = ""
		copy(newLines[m.cursorRow+2:], m.lines[m.cursorRow+1:])
		m.lines = newLines
		m.cursorRow++
		m.cursorCol = 0
		m.Mode = ModeInsert

	case "O":
		m.armInsertUndo()
		m.commitInsertUndo()
		newLines := make([]string, len(m.lines)+1)
		copy(newLines, m.lines[:m.cursorRow])
		newLines[m.cursorRow] = ""
		copy(newLines[m.cursorRow+1:], m.lines[m.cursorRow:])
		m.lines = newLines
		m.cursorCol = 0
		m.Mode = ModeInsert

	case "h", "left":
		n := m.takeCount()
		m.cursorCol -= n
		if m.cursorCol < 0 {
			m.cursorCol = 0
		}

	case "l", "right":
		n := m.takeCount()
		lineLen := runeLen(m.lines[m.cursorRow])
		m.cursorCol += n
		if m.cursorCol > lineLen-1 {
			m.cursorCol = lineLen - 1
		}
		if m.cursorCol < 0 {
			m.cursorCol = 0
		}

	case "j", "down":
		n := m.takeCount()
		for i := 0; i < n; i++ {
			m.moveDownDisplayLine()
		}

	case "k", "up":
		n := m.takeCount()
		for i := 0; i < n; i++ {
			m.moveUpDisplayLine()
		}

	case "w":
		n := m.takeCount()
		for i := 0; i < n; i++ {
			m.cursorRow, m.cursorCol = m.nextWordStart(m.cursorRow, m.cursorCol)
		}

	case "b":
		n := m.takeCount()
		for i := 0; i < n; i++ {
			m.cursorRow, m.cursorCol = m.prevWordStart(m.cursorRow, m.cursorCol)
		}

	case "e":
		n := m.takeCount()
		for i := 0; i < n; i++ {
			m.cursorRow, m.cursorCol = m.nextWordEnd(m.cursorRow, m.cursorCol)
		}

	case "{":
		n := m.takeCount()
		for i := 0; i < n; i++ {
			m.cursorRow = m.prevParagraph(m.cursorRow)
		}
		m.cursorCol = 0

	case "}":
		n := m.takeCount()
		for i := 0; i < n; i++ {
			m.cursorRow = m.nextParagraph(m.cursorRow)
		}
		m.cursorCol = 0

	case "%":
		m.count, m.countExplicit = 0, false
		if tr, tc, ok := m.matchBracket(m.cursorRow, m.cursorCol); ok {
			m.cursorRow, m.cursorCol = tr, tc
		}

	case "0":
		// Go to start of display line
		m.count, m.countExplicit = 0, false
		displayLines := m.buildDisplayLines()
		cursorDL := m.findCursorDisplayLine(displayLines)
		if cursorDL < len(displayLines) {
			m.cursorCol = displayLines[cursorDL].startCol
		}

	case "$":
		// Go to end of the current display line, landing on the last character.
		m.count, m.countExplicit = 0, false
		displayLines := m.buildDisplayLines()
		cursorDL := m.findCursorDisplayLine(displayLines)
		if cursorDL < len(displayLines) {
			dl := displayLines[cursorDL]
			end := dl.startCol + runeLen(dl.text)
			if end > dl.startCol {
				m.cursorCol = end - 1
			} else {
				m.cursorCol = dl.startCol
			}
		}

	case "g":
		m.pendingG = true
		return // preserve any count for Ngg

	case "G":
		if m.count > 0 {
			m.cursorRow = m.count - 1
		} else {
			m.cursorRow = len(m.lines) - 1
		}
		m.count, m.countExplicit = 0, false
		if m.cursorRow > len(m.lines)-1 {
			m.cursorRow = len(m.lines) - 1
		}
		if m.cursorRow < 0 {
			m.cursorRow = 0
		}
		m.cursorCol = firstNonBlank(m.lines[m.cursorRow])

	case "r":
		m.opCount = m.takeCount()
		m.pendingReplace = true
		return

	case "x":
		n := m.takeCount()
		r := graphemes(m.lines[m.cursorRow])
		if m.cursorCol < len(r) {
			m.pushUndo()
			end := m.cursorCol + n
			if end > len(r) {
				end = len(r)
			}
			m.setRegister(joinGraphemes(r[m.cursorCol:end]), false)
			r = append(r[:m.cursorCol], r[end:]...)
			m.lines[m.cursorRow] = joinGraphemes(r)
			if m.cursorCol >= len(r) && m.cursorCol > 0 {
				m.cursorCol--
			}
		}

	case "d":
		m.opCount, m.opCountExplicit = m.takeCountExplicit()
		m.pendingOp = "d"
		return

	case "c":
		m.opCount, m.opCountExplicit = m.takeCountExplicit()
		m.pendingOp = "c"
		return

	case "y":
		m.opCount, m.opCountExplicit = m.takeCountExplicit()
		m.pendingOp = "y"
		return

	case "p":
		n := m.takeCount()
		for i := 0; i < n; i++ {
			m.paste(true)
		}

	case "P":
		n := m.takeCount()
		for i := 0; i < n; i++ {
			m.paste(false)
		}

	case "u":
		n := m.takeCount()
		for i := 0; i < n; i++ {
			m.undo()
		}

	case "ctrl+r":
		n := m.takeCount()
		for i := 0; i < n; i++ {
			m.redo()
		}

	case "ctrl+f":
		m.count, m.countExplicit = 0, false
		m.scrollDisplayLines(m.pageLines(), true)

	case "ctrl+b":
		m.count, m.countExplicit = 0, false
		m.scrollDisplayLines(m.pageLines(), false)

	case "ctrl+d":
		m.count, m.countExplicit = 0, false
		m.scrollDisplayLines(m.halfPageLines(), true)

	case "ctrl+u":
		m.count, m.countExplicit = 0, false
		m.scrollDisplayLines(m.halfPageLines(), false)

	case "n":
		m.count, m.countExplicit = 0, false
		m.SearchNext()

	case "N":
		m.count, m.countExplicit = 0, false
		m.SearchPrev()

	case "v":
		// Enter visual mode
		m.Mode = ModeVisual
		m.selectionAnchor = Position{Row: m.cursorRow, Col: m.cursorCol}
	}

	// Any unrecognized key drops a dangling count prefix.
	m.count, m.countExplicit = 0, false

	// Every Normal-mode motion lands on a real character, never past the end.
	if m.Mode == ModeNormal {
		m.clampNormalCol()
	}
	m.ensureCursorVisible()
}

// replacementRune extracts the single replacement rune for `r` from a key
// message (a printable rune or space); ok is false for anything else.
func replacementRune(msg tea.KeyMsg) (rune, bool) {
	switch msg.Type {
	case tea.KeySpace:
		return ' ', true
	case tea.KeyRunes:
		if len(msg.Runes) == 1 {
			return msg.Runes[0], true
		}
	}
	return 0, false
}
