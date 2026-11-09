package editor

import "strings"

// StartSearch stores the query as the active search pattern and jumps to the
// first match in the given direction from the cursor. Returns false if the
// query is empty or has no match.
func (m *Model) StartSearch(query string, forward bool) bool {
	if query == "" {
		return false
	}
	m.lastSearch = query
	m.searchForward = forward
	return m.searchFrom(forward)
}

// SearchStatus returns the retained query and the cursor's 1-based match index.
// Searches are Unicode case-insensitive, matching project-wide search.
func (m *Model) SearchStatus() (query string, current, total int) {
	if m.lastSearch == "" {
		return "", 0, 0
	}
	matches := m.cachedSearchMatches()
	for i, p := range matches {
		if p.Row == m.cursorRow && p.Col == m.cursorCol {
			current = i + 1
			break
		}
	}
	return m.lastSearch, current, len(matches)
}

func (m *Model) cachedSearchMatches() []Position {
	if m.searchCacheRevision != m.revision || m.searchCacheQuery != m.lastSearch {
		m.searchCache = m.searchMatches()
		m.searchCacheRevision, m.searchCacheQuery = m.revision, m.lastSearch
	}
	return m.searchCache
}

func foldRunes(s string) []string {
	r := graphemes(s)
	for i := range r {
		r[i] = strings.ToLower(r[i])
	}
	return r
}

func (m Model) searchMatches() []Position {
	q := foldRunes(m.lastSearch)
	var out []Position
	for row, text := range m.lines {
		line := foldRunes(text)
		for col := 0; col+len(q) <= len(line); col++ {
			if matchAt(line, col, q) {
				out = append(out, Position{Row: row, Col: col})
			}
		}
	}
	return out
}

// SearchNext repeats the last search in its original direction (`n`).
func (m *Model) SearchNext() bool { return m.repeatSearch(m.searchForward) }

// SearchPrev repeats the last search in the opposite direction (`N`).
func (m *Model) SearchPrev() bool { return m.repeatSearch(!m.searchForward) }

func (m *Model) repeatSearch(forward bool) bool {
	if m.lastSearch == "" {
		return false
	}
	return m.searchFrom(forward)
}

// searchFrom finds the next occurrence of the active pattern starting one
// position away from the cursor, wrapping around the buffer. On a match it
// moves the cursor and returns true.
func (m *Model) searchFrom(forward bool) bool {
	q := foldRunes(m.lastSearch)
	n := len(m.lines)
	if n == 0 {
		return false
	}

	// Scan every line once, starting from the cursor's line, wrapping around.
	for off := 0; off <= n; off++ {
		var row int
		if forward {
			row = (m.cursorRow + off) % n
		} else {
			row = ((m.cursorRow-off)%n + n) % n
		}
		line := foldRunes(m.lines[row])

		// Restrict the column range on the very first line so we don't re-match
		// at the cursor itself.
		var cols []int
		if forward {
			startCol := 0
			if off == 0 {
				startCol = m.cursorCol + 1
			}
			for c := startCol; c+len(q) <= len(line); c++ {
				cols = append(cols, c)
			}
		} else {
			startCol := len(line) - len(q)
			if off == 0 {
				startCol = m.cursorCol - 1
			}
			if startCol > len(line)-len(q) {
				startCol = len(line) - len(q)
			}
			for c := startCol; c >= 0; c-- {
				cols = append(cols, c)
			}
		}

		for _, c := range cols {
			if matchAt(line, c, q) {
				m.cursorRow = row
				m.cursorCol = c
				m.clampNormalCol()
				m.ensureCursorVisible()
				return true
			}
		}
	}
	return false
}

func matchAt(line []string, col int, q []string) bool {
	if col < 0 || col+len(q) > len(line) {
		return false
	}
	for i, r := range q {
		if line[col+i] != r {
			return false
		}
	}
	return true
}
