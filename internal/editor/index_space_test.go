package editor

// Raw/display index-space tests: the cursor, edits, selections, wrap ranges and
// j/k motion must all speak the same grapheme index (the raw line's), even on
// rows that Sanitize rewrites (control chars, ZWJ merging, Arabic prepend marks,
// format chars). These tests parse the real View() output and compare it with
// what the editing commands act on.

import (
	"math/rand"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
)

const (
	bgNone = iota
	bgCursor
	bgSel
	bgLine
)

var sgrRE = regexp.MustCompile("\x1b\\[([0-9;]*)m")

type viewCell struct {
	ch rune
	bg int
}

// forceTrueColor makes lipgloss emit real SGR sequences for the test.
// (termenv.TrueColor is the zero Profile; set via reflection so the test needs
// no direct termenv import.)
func forceTrueColor(t *testing.T) {
	t.Helper()
	prev := lipgloss.ColorProfile()
	p := prev
	reflect.ValueOf(&p).Elem().SetInt(0)
	lipgloss.SetColorProfile(p)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
}

func markedModel(content string, width int) Model {
	m := New()
	m.styles.EditorCursor = lipgloss.NewStyle().Background(lipgloss.Color("#ff0000"))
	m.styles.EditorSelection = lipgloss.NewStyle().Background(lipgloss.Color("#00ff00"))
	m.styles.EditorCursorLine = lipgloss.NewStyle().Background(lipgloss.Color("#0000ff"))
	m.styles.EditorLineNumber = lipgloss.NewStyle()
	m.styleNoteMarker = lipgloss.NewStyle()
	m.SetContent(content)
	m.SetSize(width+m.gutterWidth(), 200)
	return m
}

func parseCells(line string) []viewCell {
	var cells []viewCell
	bg := bgNone
	for len(line) > 0 {
		if loc := sgrRE.FindStringSubmatchIndex(line); loc != nil && loc[0] == 0 {
			params := line[loc[2]:loc[3]]
			switch {
			case strings.Contains(params, "48;2;255;0;0"):
				bg = bgCursor
			case strings.Contains(params, "48;2;0;255;0"):
				bg = bgSel
			case strings.Contains(params, "48;2;0;0;255"):
				bg = bgLine
			case params == "" || params == "0":
				bg = bgNone
			}
			line = line[loc[1]:]
			continue
		}
		r, n := utf8.DecodeRuneInString(line)
		cells = append(cells, viewCell{r, bg})
		line = line[n:]
	}
	return cells
}

type drawn struct {
	cursorCols []Position // (row, raw col) of every drawn cluster with cursor background
	phantoms   int        // cursor cells drawn past the end of the visible clusters
	selected   map[Position]bool
}

// readView parses m.View() and maps every drawn cluster back to the (row, col)
// the wrapped segment says it owns.
func readView(t *testing.T, m *Model) drawn {
	t.Helper()
	view := m.View()
	dls := m.buildDisplayLines()
	out := drawn{selected: map[Position]bool{}}
	for i, line := range strings.Split(view, "\n") {
		if m.viewOffset+i >= len(dls) {
			break
		}
		dl := dls[m.viewOffset+i]
		cells := parseCells(line)
		if len(cells) < m.gutterWidth() {
			t.Fatalf("short line %q", line)
		}
		cells = cells[m.gutterWidth():]
		pos := 0
		for ci, cluster := range dl.clusters {
			n := utf8.RuneCountInString(cluster)
			if pos+n > len(cells) {
				t.Fatalf("row %d: view line shorter than its clusters: %q", dl.logicalRow, line)
			}
			bg := cells[pos].bg
			for _, c := range cells[pos : pos+n] {
				if c.bg != bg {
					t.Fatalf("row %d col %d: cluster %q drawn with mixed backgrounds", dl.logicalRow, dl.startCol+ci, cluster)
				}
			}
			p := Position{dl.logicalRow, dl.startCol + ci}
			switch bg {
			case bgCursor:
				out.cursorCols = append(out.cursorCols, p)
				out.selected[p] = true
			case bgSel:
				out.selected[p] = true
			}
			pos += n
		}
		for _, c := range cells[pos:] {
			if c.bg == bgCursor {
				out.phantoms++
			}
		}
	}
	return out
}

func indexFragments() []string {
	return []string{"a", "b", " ", "x", "\r", "\x01", "\u200b", "\u200d", "\ufeff", "\u0600", "\u0601", "7",
		"\ufe0f", "\ufe0e", "\u4e16", "\u754c", "\U0001f600", "\U0001f468\u200d\U0001f469\u200d\U0001f467", "\t", "e\u0301", "\x7f", "\u2028", "\u00e9", "wordwordword"}
}

func indexLines() []string {
	lines := []string{
		"", "\r\u200d", "a\r\u200dB", "\u0600" + "1 x", "x\x01y", "\u200b\u200b", "\ufeffabc", "a\ufe0f b",
		"\u0600\u0601\u0602\u0603\u0604\u0605" + "9", "a\t\tb", "\u4e16\u200b\u754c \U0001f600\u200d x", "\r\r\r", "\x01\x02 \x03",
	}
	frag := indexFragments()
	r := rand.New(rand.NewSource(7))
	for i := 0; i < 60; i++ {
		var b strings.Builder
		for n := r.Intn(14); n > 0; n-- {
			b.WriteString(frag[r.Intn(len(frag))])
		}
		lines = append(lines, b.String())
	}
	return lines
}

func quote(s string) string { return strconv.QuoteToASCII(s) }

func TestCursorCellMatchesRawIndex(t *testing.T) {
	forceTrueColor(t)
	for _, line := range indexLines() {
		for _, width := range []int{4, 7, 12, 40} {
			for _, mode := range []Mode{ModeNormal, ModeInsert} {
				m := markedModel("top\n"+line+"\nbottom", width)
				m.Mode = mode
				raw := graphemes(line)
				limit := len(raw)
				if mode == ModeNormal && limit > 0 {
					limit--
				}
				if rl := m.rowEntry(1).rawLen; rl != len(raw) || len(m.logicalClusters(1)) != len(raw) {
					t.Fatalf("line=%s: display clusters %d != raw %d", quote(line), len(m.logicalClusters(1)), len(raw))
				}
				for col := 0; col <= limit; col++ {
					m.cursorRow, m.cursorCol = 1, col
					d := readView(t, &m)
					ctx := "line=" + quote(line) + " col=" + strconv.Itoa(col) + " w=" + strconv.Itoa(width)
					visible := false
					for _, dl := range m.buildDisplayLines() {
						if dl.logicalRow == 1 && col >= dl.startCol && col < dl.endCol-dl.consumedWhitespace {
							visible = true
						}
					}
					if visible {
						if len(d.cursorCols) != 1 || d.cursorCols[0] != (Position{1, col}) || d.phantoms != 0 {
							t.Fatalf("%s: cursor drawn at %v phantoms=%d, want cluster %d", ctx, d.cursorCols, d.phantoms, col)
						}
					} else if len(d.cursorCols) != 0 || d.phantoms != 1 {
						t.Fatalf("%s: want exactly one phantom cursor cell, got %v phantoms=%d", ctx, d.cursorCols, d.phantoms)
					}
				}
			}
		}
	}
}

func TestVisualSelectionMatchesYank(t *testing.T) {
	forceTrueColor(t)
	withStubClipboard(t)
	for _, line := range indexLines() {
		raw := graphemes(line)
		if len(raw) == 0 || len(raw) > 14 {
			continue
		}
		for _, width := range []int{5, 40} {
			base := markedModel("top\n"+line+"\nbottom", width)
			for a := 0; a < len(raw); a++ {
				for c := 0; c < len(raw); c++ {
					m := base
					m.Mode = ModeVisual
					m.selectionAnchor = Position{1, a}
					m.cursorRow, m.cursorCol = 1, c
					lo, hi := min(a, c), max(a, c)
					d := readView(t, &m)
					for _, dl := range m.buildDisplayLines() {
						if dl.logicalRow != 1 {
							continue
						}
						for col := dl.startCol; col < dl.endCol-dl.consumedWhitespace; col++ {
							want := col >= lo && col <= hi
							if d.selected[Position{1, col}] != want {
								t.Fatalf("line=%s w=%d sel=%d..%d: col %d highlighted=%v want %v", quote(line), width, a, c, col, !want, want)
							}
						}
					}
					y := feed(m, key("y"))
					if want := joinGraphemes(raw[lo : hi+1]); y.register != want {
						t.Fatalf("line=%s sel=%d..%d: yank %q want %q", quote(line), a, c, y.register, want)
					}
				}
			}
		}
	}
}

func TestDisplayLineMotionStaysInRowAndNearestColumn(t *testing.T) {
	for _, line := range indexLines() {
		for _, width := range []int{4, 7, 12, 40} {
			m := markedModel("alpha beta gamma\n"+line+"\nomega psi chi", width)
			dls := m.buildDisplayLines()
			for row := 0; row < len(m.lines); row++ {
				rl := runeLen(m.lines[row])
				for col := 0; col <= rl; col++ {
					for _, down := range []bool{true, false} {
						m.cursorRow, m.cursorCol = row, col
						from := m.findCursorDisplayLine(dls)
						before := segmentDisplayColumn(dls[from], col)
						if down {
							m.moveDownDisplayLine()
						} else {
							m.moveUpDisplayLine()
						}
						if m.cursorRow < 0 || m.cursorRow >= len(m.lines) || m.cursorCol < 0 || m.cursorCol > runeLen(m.lines[m.cursorRow]) {
							t.Fatalf("line=%s w=%d (%d,%d) down=%v: landed outside row at (%d,%d)", quote(line), width, row, col, down, m.cursorRow, m.cursorCol)
						}
						to := m.findCursorDisplayLine(dls)
						wantTo := from + 1
						if !down {
							wantTo = from - 1
						}
						if wantTo < 0 || wantTo >= len(dls) {
							if to != from {
								t.Fatalf("moved past the buffer edge")
							}
							continue
						}
						if to != wantTo {
							t.Fatalf("line=%s w=%d (%d,%d) down=%v: landed on display line %d, want %d", quote(line), width, row, col, down, to, wantTo)
						}
						seg := dls[to]
						visEnd := seg.endCol - seg.consumedWhitespace
						after := segmentDisplayColumn(seg, m.cursorCol)
						if m.cursorCol < visEnd {
							if after > before {
								t.Fatalf("line=%s w=%d: landed at display col %d, past previous %d", quote(line), width, after, before)
							}
							// the last visible cluster may sit left of the previous column
							// (the target line is shorter); otherwise it must cover it
							if m.cursorCol != visEnd-1 && before >= after+clusterDisplayWidth(seg.clusters[m.cursorCol-seg.startCol], after) {
								t.Fatalf("line=%s w=%d: cluster at %d does not cover previous col %d", quote(line), width, after, before)
							}
						} else if segmentDisplayColumn(seg, visEnd) > before {
							t.Fatalf("line=%s w=%d: clamped to end although a nearer cell exists", quote(line), width)
						}
					}
				}
			}
		}
	}
}
