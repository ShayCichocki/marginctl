package editor

import (
	"sort"

	"github.com/ShayCichocki/marginctl/internal/theme"
)

// displayLine represents a wrapped line for display. startCol/endCol are raw
// grapheme indices into the logical line ([startCol, endCol) is the half-open
// span of clusters this display line covers); clusters[i] is the sanitized text
// of raw cluster startCol+i.
type displayLine struct {
	logicalRow         int    // which logical line this came from
	startCol           int    // starting source rune column (inclusive)
	endCol             int    // ending source rune column (exclusive), including consumed whitespace
	text               string // the actual text to display
	displayWidth       int
	consumedWhitespace int // trailing source runes owned by this segment but not rendered
	clusters           []string
}

type displayRange struct {
	start int
	end   int
}

// wrapColumns returns the number of display columns to wrap at: the pane width
// minus the line-number gutter, or the fallback wrapWidth before the editor has
// been sized. Never returns less than 1.
func (m Model) wrapColumns() int {
	if m.width <= 0 {
		return wrapWidth
	}
	if w := m.width - m.gutterWidth(); w >= 1 {
		return w
	}
	return 1
}

// wrapLine wraps a single logical line into display lines. width is measured in
// display columns (via go-runewidth), so wide runes are accounted for and a
// segment never exceeds the pane; breaks land on rune boundaries, preferring
// the last space that fits (word wrap) and force-breaking mid-word otherwise.
func wrapLine(text string, width int) []string {
	segments := wrapSegments(text, width)
	out := make([]string, len(segments))
	for i := range segments {
		out[i] = segments[i].text
	}
	return out
}

func wrapSegments(text string, width int) []displayLine {
	if width < 1 {
		width = wrapWidth
	}
	runes := graphemes(text)
	return wrapClustersSrc(text, runes, width)
}

// wrapClustersSrc is the single source of truth for wrap segment boundaries.
// src must be the string whose graphemes are runes (the clusters are
// consecutive substrings of it); segment text is a zero-copy slice of src.
func wrapClustersSrc(src string, runes []string, width int) []displayLine {
	if width < 1 {
		width = wrapWidth
	}
	if len(runes) == 0 {
		return []displayLine{{text: "", clusters: runes}}
	}
	// segText returns the text of runes[start:end] where off is the byte offset
	// of runes[start] within src, plus the byte length of that span.
	segText := func(off, start, end int) (string, int) {
		n := 0
		for _, r := range runes[start:end] {
			n += len(r)
		}
		return src[off : off+n], n
	}
	var result []displayLine
	start, off := 0, 0
	for start < len(runes) {
		// Grow the segment until the next rune would overflow the width.
		w := 0
		end := start
		lastSpace := -1
		for end < len(runes) {
			rw := clusterDisplayWidth(runes[end], w)
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
			text, _ := segText(off, start, end)
			result = append(result, displayLine{startCol: start, endCol: end, text: text, displayWidth: w, clusters: runes[start:end]})
			break
		}

		// Break before `end`. Prefer the last space that fit (word wrap); the
		// consumed space is dropped so it doesn't lead the next display line.
		breakAt := end
		consumeSpace := false
		if lastSpace > start {
			breakAt = lastSpace
			consumeSpace = true
		} else if end < len(runes) && clusterIsSpace(runes[end]) {
			// A separator immediately after a full hard-wrapped segment is owned
			// by that segment, even though it consumes no display cell.
			breakAt = end
			consumeSpace = true
		}
		shownWidth := 0
		for _, r := range runes[start:breakAt] {
			shownWidth += clusterDisplayWidth(r, shownWidth)
		}
		text, n := segText(off, start, breakAt)
		seg := displayLine{startCol: start, endCol: breakAt, text: text, displayWidth: shownWidth, clusters: runes[start:breakAt]}
		start = breakAt
		off += n
		if consumeSpace && start < len(runes) && clusterIsSpace(runes[start]) {
			off += len(runes[start])
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

// rowWrap is the per-logical-row render cache entry.
//
// Cache design: a row's clusters and wrap segments are a pure function of its
// source string and the wrap width. Instead of keying on the global buffer
// revision (which one keystroke bumps, discarding every row), entries are
// validated against the row's current source string: m.rowCache[i] is reused
// iff its src equals m.lines[i] (and its width equals the current wrap width
// for segs). Unchanged rows therefore survive any edit elsewhere; changed rows
// miss on string inequality, so a stale entry can never be returned. When the
// line count changes, realignRowCache shifts the unchanged prefix and suffix
// entries into place, so inserting/deleting lines does not invalidate the rows
// below. segs keep startCol/endCol/clusters exactly as wrapClustersSrc produced
// them (logicalRow is stamped on copy in buildDisplayLines); only the cheap
// global flattening and row ranges are rebuilt per revision.
type rowWrap struct {
	src      string
	ok       bool     // entry is populated; distinguishes a real empty line (src == "") from a zero-value slot
	clean    string   // theme.Sanitize(src)
	clusters []string // one entry per raw grapheme: Sanitize(raw cluster i)
	rawLen   int      // len(graphemes(src)) == len(clusters)
	width    int      // wrap width segs was computed for; valid only when segs != nil
	segs     []displayLine
}

// realignRowCache resizes the row cache to the current line count, carrying
// over entries for the unchanged leading and trailing rows (compared by source
// string) so a line insert/delete/paste keeps every untouched row's work.
func (m *Model) realignRowCache() {
	old := m.rowCache
	n := len(m.lines)
	next := make([]rowWrap, n)
	p := 0
	for p < n && p < len(old) && old[p].ok && old[p].src == m.lines[p] {
		next[p] = old[p]
		p++
	}
	for k := 1; k <= n-p && k <= len(old)-p; k++ {
		e := old[len(old)-k]
		if !e.ok || e.src != m.lines[n-k] {
			break
		}
		next[n-k] = e
	}
	m.rowCache = next
}

func (m *Model) rowEntry(row int) *rowWrap {
	if len(m.rowCache) != len(m.lines) {
		m.realignRowCache()
	}
	e := &m.rowCache[row]
	if !e.ok || e.src != m.lines[row] {
		src := m.lines[row]
		clean := theme.Sanitize(src)
		var clusters []string
		if clean == src {
			clusters = graphemes(src)
		} else {
			// Sanitize each raw cluster on its own so display cluster i is
			// always raw cluster i: one index space serves the cursor, edits,
			// selections, wrap ranges and motions. strings.Map is rune-wise, so
			// the concatenation still equals clean (segment slicing relies on
			// that). A sanitized cluster may span several display graphemes
			// (e.g. U+0600 + digit becomes U+FFFD + digit); it is drawn as one
			// unit whose width is the width of its sanitized text.
			raw := graphemes(src)
			clusters = make([]string, len(raw))
			for i, c := range raw {
				clusters[i] = theme.Sanitize(c)
			}
		}
		*e = rowWrap{src: src, ok: true, clean: clean, clusters: clusters, rawLen: len(clusters)}
	}
	return e
}

func (m *Model) logicalClusters(row int) []string {
	return m.rowEntry(row).clusters
}

// buildDisplayLines creates all display lines with metadata. startCol/endCol
// are rune indices, and the consumed-space offset between wrapped segments is
// only applied when a space was actually the break point.
func (m *Model) buildDisplayLines() []displayLine {
	width := m.wrapColumns()
	if m.displayCache != nil && m.displayCacheRevision == m.revision && m.displayCacheWidth == width {
		return m.displayCache
	}
	ranges := make([]displayRange, len(m.lines))
	total := 0
	for logRow := range m.lines {
		e := m.rowEntry(logRow)
		if e.segs == nil || e.width != width {
			if len(e.clusters) == 0 {
				e.segs = []displayLine{{}}
			} else {
				e.segs = wrapClustersSrc(e.clean, e.clusters, width)
			}
			e.width = width
		}
		ranges[logRow].start = total
		total += len(e.segs)
		ranges[logRow].end = total
	}

	display := make([]displayLine, 0, total)
	for logRow := range m.lines {
		for _, seg := range m.rowCache[logRow].segs {
			seg.logicalRow = logRow
			display = append(display, seg)
		}
	}

	m.displayCache, m.displayRowRanges = display, ranges
	m.displayCacheRevision, m.displayCacheWidth = m.revision, width
	return m.displayCache
}

// findCursorDisplayLine finds which display line the cursor is on. A display
// line covers the half-open rune span [startCol, endCol); a cursor at or past
// the last endCol of its logical row maps to that row's final display line.
func (m Model) findCursorDisplayLine(displayLines []displayLine) int {
	if m.cursorRow < 0 || m.cursorRow >= len(m.displayRowRanges) {
		return 0
	}
	r := m.displayRowRanges[m.cursorRow]
	if r.start < 0 || r.end > len(displayLines) || r.start >= r.end {
		return 0
	}
	i := sort.Search(r.end-r.start, func(i int) bool { return displayLines[r.start+i].endCol > m.cursorCol })
	if i < r.end-r.start {
		return r.start + i
	}
	return r.end - 1
}

func segmentDisplayColumn(seg displayLine, sourceCol int) int {
	clusters := seg.clusters
	if sourceCol < seg.startCol {
		sourceCol = seg.startCol
	}
	visibleEnd := seg.endCol - seg.consumedWhitespace
	if sourceCol > visibleEnd {
		sourceCol = visibleEnd
	}
	w := 0
	for _, r := range clusters[:sourceCol-seg.startCol] {
		w += clusterDisplayWidth(r, w)
	}
	return w
}

func segmentSourceColumn(seg displayLine, displayCol int) int {
	clusters := seg.clusters
	visibleEnd := seg.endCol - seg.consumedWhitespace
	w := 0
	for col := seg.startCol; col < visibleEnd; col++ {
		rw := clusterDisplayWidth(clusters[col-seg.startCol], w)
		if displayCol < w+rw {
			return col
		}
		w += rw
	}
	return visibleEnd
}
