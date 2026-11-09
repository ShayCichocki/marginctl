package editor

import (
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// diffPieces mixes plain ASCII with everything that makes grapheme clusters,
// widths and wrapping interesting: combining marks (e + U+0301), lone combining marks and ZWJ, ZWJ families, regional-indicator
// flags (and lone indicators that can pair up), CJK double-width, tabs, CR,
// control characters, variation selectors and long runs.
var diffPieces = []string{
	"a", "b", "c", "x", " ", " ", " ", "word ", "Hello", ".", "*", "_", "# ", "#",
	"e\u0301", "\u0301", "\u200d", "\ufe0f", "\u270c\ufe0f", "\U0001f44d\U0001f3fd",
	"\U0001f468\u200d\U0001f469\u200d\U0001f467\u200d\U0001f466", "\U0001f468", "\U0001f469",
	"\U0001f1fa\U0001f1f8", "\U0001f1fa", "\U0001f1f8", "\U0001f1eb", "\U0001f1f7",
	"\u4e16", "\u754c", "\u3042", "\t", "\r", "\x01", "\u200b", "\u0e01\u0e33",
	"\u1100\u1161\u11a8", "\u0600a", strings.Repeat("longword", 12), strings.Repeat("ab ", 30),
}

func randText(r *rand.Rand, n int) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(diffPieces[r.Intn(len(diffPieces))])
	}
	return b.String()
}

func randDoc(r *rand.Rand, rows int) string {
	lines := make([]string, rows)
	for i := range lines {
		switch r.Intn(6) {
		case 0:
			lines[i] = ""
		case 1:
			lines[i] = randText(r, 60)
		default:
			lines[i] = randText(r, r.Intn(10))
		}
	}
	return strings.Join(lines, "\n")
}

func sliceEq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func displayEq(got, want displayLine) bool {
	return got.logicalRow == want.logicalRow && got.startCol == want.startCol && got.endCol == want.endCol &&
		got.text == want.text && got.displayWidth == want.displayWidth &&
		got.consumedWhitespace == want.consumedWhitespace && sliceEq(got.clusters, want.clusters)
}

func assertDisplayMatchesOracle(t *testing.T, m *Model, ctx string) {
	t.Helper()
	got := m.buildDisplayLines()
	want, wantRanges := oracleBuildDisplayLines(m.lines, m.wrapColumns())
	if len(got) != len(want) {
		t.Fatalf("%s: %d display lines, oracle %d (lines=%q)", ctx, len(got), len(want), m.lines)
	}
	for i := range got {
		if !displayEq(got[i], want[i]) {
			t.Fatalf("%s: display[%d] = %+v, oracle %+v", ctx, i, got[i], want[i])
		}
	}
	if !reflect.DeepEqual(m.displayRowRanges, wantRanges) {
		t.Fatalf("%s: row ranges differ: %v vs %v", ctx, m.displayRowRanges, wantRanges)
	}
	for row := range m.lines {
		// One display cluster per raw cluster (each sanitized on its own).
		want := oracleGraphemes(m.lines[row])
		for i := range want {
			want[i] = sanitizedForTest(want[i])
		}
		if !sliceEq(m.logicalClusters(row), want) {
			t.Fatalf("%s: logicalClusters(%d) differ", ctx, row)
		}
	}
}

// freshView renders m from a clone with every derived cache dropped.
func freshView(m Model) string {
	m.rowCache, m.displayCache, m.displayRowRanges = nil, nil, nil
	m.renderLines, m.viewCacheValid, m.searchCache = nil, false, nil
	return m.View()
}

func TestGraphemesMatchOracle(t *testing.T) {
	r := rand.New(rand.NewSource(1))
	cases := []string{"", "a", "abc", "a\r\nb", "\r\n", "\r", "a\r", "ab\u0301c", "\u0301", "\u200d", "\U0001f1fa\U0001f1f8\U0001f1fa", "line\twith\ttabs", "\x00\x1f\x7f"}
	for i := 0; i < 3000; i++ {
		cases = append(cases, randText(r, r.Intn(12)))
	}
	for _, s := range cases {
		if got, want := graphemes(s), oracleGraphemes(s); !sliceEq(got, want) || (got == nil) != (want == nil) {
			t.Fatalf("graphemes(%q) = %q, want %q", s, got, want)
		}
	}
}

func TestClusterDisplayWidthMatchesOracle(t *testing.T) {
	for b := 0; b < 128; b++ {
		for col := 0; col < 9; col++ {
			s := string(rune(b))
			if got, want := clusterDisplayWidth(s, col), oracleClusterDisplayWidth(s, col); got != want {
				t.Fatalf("width(%q, %d) = %d, want %d", s, col, got, want)
			}
		}
	}
	for _, p := range diffPieces {
		for _, c := range oracleGraphemes(p) {
			for col := 0; col < 5; col++ {
				if got, want := clusterDisplayWidth(c, col), oracleClusterDisplayWidth(c, col); got != want {
					t.Fatalf("width(%q, %d) = %d, want %d", c, col, got, want)
				}
			}
		}
	}
}

func TestEditHelpersMatchOracle(t *testing.T) {
	r := rand.New(rand.NewSource(2))
	for i := 0; i < 20000; i++ {
		s := randText(r, r.Intn(10))
		ins := randText(r, r.Intn(3))
		if r.Intn(3) == 0 {
			ins = string([]rune(ins)[:min(1, len([]rune(ins)))])
		}
		n := len(oracleGraphemes(s))
		col := r.Intn(n+4) - 2
		gs, gc := insertRunes(s, col, ins)
		ws, wc := oracleInsertRunes(s, col, ins)
		if gs != ws || gc != wc {
			t.Fatalf("insertRunes(%q,%d,%q) = (%q,%d), oracle (%q,%d)", s, col, ins, gs, gc, ws, wc)
		}
		if got, want := deleteRuneAt(s, col), oracleDeleteRuneAt(s, col); got != want {
			t.Fatalf("deleteRuneAt(%q,%d) = %q, oracle %q", s, col, got, want)
		}
		off, cnt, _ := graphemeOffset(s, max(col, 0))
		wb, wa := oracleSplit(s, col)
		if s[:off] != wb || s[off:] != wa || cnt != min(max(col, 0), n) {
			t.Fatalf("split(%q,%d) = (%q,%q) n=%d, oracle (%q,%q)", s, col, s[:off], s[off:], cnt, wb, wa)
		}
	}
}

func TestInsertRunesTypedRuneByRuneMatchesOracle(t *testing.T) {
	// Typing a ZWJ family / flag / NFD accent one rune at a time exercises merges
	// into the previous cluster at every junction.
	r := rand.New(rand.NewSource(3))
	for i := 0; i < 2000; i++ {
		text := randText(r, 8)
		a, ac := "", 0
		b, bc := "", 0
		for _, ch := range text {
			// Insert at a random valid column so the junction sits mid-line too.
			if r.Intn(3) == 0 {
				ac = r.Intn(len(oracleGraphemes(a)) + 1)
				bc = ac
			}
			a, ac = insertRunes(a, ac, string(ch))
			b, bc = oracleInsertRunes(b, bc, string(ch))
			if a != b || ac != bc {
				t.Fatalf("diverged after %q: (%q,%d) vs oracle (%q,%d)", string(ch), a, ac, b, bc)
			}
		}
	}
}

func TestWrapClustersSrcMatchesOracle(t *testing.T) {
	r := rand.New(rand.NewSource(4))
	for i := 0; i < 3000; i++ {
		s := randText(r, 1+r.Intn(40))
		clean := sanitizedForTest(s)
		cl := oracleGraphemes(clean)
		for _, w := range []int{1, 2, 3, 5, 8, 13, 40, 100} {
			got := wrapClustersSrc(clean, cl, w)
			want := oracleWrapClusters(cl, w)
			if len(got) != len(want) {
				t.Fatalf("wrap(%q,%d): %d segs vs %d", clean, w, len(got), len(want))
			}
			for j := range got {
				if !displayEq(got[j], want[j]) {
					t.Fatalf("wrap(%q,%d) seg %d = %+v vs %+v", clean, w, j, got[j], want[j])
				}
			}
			if got2 := wrapSegments(s, w); len(got2) != len(oracleWrapClusters(oracleGraphemes(s), w)) {
				t.Fatalf("wrapSegments mismatch for %q", s)
			}
		}
	}
}

var diffKeys = []tea.KeyMsg{
	{Type: tea.KeyEnter}, {Type: tea.KeyBackspace}, {Type: tea.KeyDelete}, {Type: tea.KeyEsc},
	{Type: tea.KeyLeft}, {Type: tea.KeyRight}, {Type: tea.KeyUp}, {Type: tea.KeyDown},
	{Type: tea.KeyTab}, {Type: tea.KeySpace}, {Type: tea.KeyCtrlR},
}

var diffNormalKeys = []string{
	"i", "a", "A", "I", "o", "O", "x", "dd", "p", "P", "u", "J", "yy", "w", "b", "$", "0", "G", "gg",
	"v", "V", "d", "y", "c", "D", "C", "j", "k", "h", "l", "r", "~", "ciw", "dw", ">", "{", "}", "3x", "2dd",
}

// expectInsert predicts the buffer after an insert-mode key by applying the
// oracle edit helpers. ok is false for keys it does not model.
func expectInsert(m Model, msg tea.KeyMsg) (lines []string, row, col int, ok bool) {
	if m.Mode != ModeInsert {
		return nil, 0, 0, false
	}
	lines = append([]string(nil), m.lines...)
	row, col = m.cursorRow, m.cursorCol
	switch msg.Type {
	case tea.KeyRunes:
		for _, ch := range msg.Runes {
			switch {
			case ch == '\r':
			case ch == '\n':
				b, a := oracleSplit(lines[row], col)
				lines = append(lines[:row+1], append([]string{a}, lines[row+1:]...)...)
				lines[row] = b
				row, col = row+1, 0
			case ch < 0x20 || ch == 0x7f || (ch >= 0x80 && ch < 0xa0):
			default:
				lines[row], col = oracleInsertRunes(lines[row], col, string(ch))
			}
		}
	case tea.KeySpace:
		lines[row], col = oracleInsertRunes(lines[row], col, " ")
	case tea.KeyTab:
		lines[row], col = oracleInsertRunes(lines[row], col, "    ")
	case tea.KeyEnter:
		b, a := oracleSplit(lines[row], col)
		lines = append(lines[:row+1], append([]string{a}, lines[row+1:]...)...)
		lines[row] = b
		row, col = row+1, 0
	case tea.KeyBackspace:
		if col == 0 {
			return nil, 0, 0, false
		}
		lines[row] = oracleDeleteRuneAt(lines[row], col-1)
		col--
	case tea.KeyDelete:
		if col >= len(oracleGraphemes(lines[row])) {
			return nil, 0, 0, false
		}
		lines[row] = oracleDeleteRuneAt(lines[row], col)
	default:
		return nil, 0, 0, false
	}
	return lines, row, col, true
}

func runDifferentialSession(t *testing.T, seed int64, width int) {
	r := rand.New(rand.NewSource(seed))
	m := New()
	m.SetContent(randDoc(r, 1+r.Intn(30)))
	m.SetSize(width, 3+r.Intn(10))
	for step := 0; step < 160; step++ {
		var msg tea.Msg
		var desc string
		switch k := r.Intn(20); {
		case k < 7:
			text := []rune(diffPieces[r.Intn(len(diffPieces))])
			msg, desc = tea.KeyMsg{Type: tea.KeyRunes, Runes: text}, fmt.Sprintf("type %q", string(text))
		case k < 11:
			key := diffKeys[r.Intn(len(diffKeys))]
			msg, desc = key, "key "+key.String()
		case k < 16:
			keys := diffNormalKeys[r.Intn(len(diffNormalKeys))]
			msg, desc = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(keys)}, "normal "+keys
		case k == 16:
			// Resize: cached rows must be re-wrapped for the new width.
			nw := 6 + r.Intn(60)
			m.SetSize(nw, 3+r.Intn(10))
			desc = fmt.Sprintf("resize %d", nw)
		case k == 17:
			// Multi-line paste through the insert path.
			if m.Mode != ModeInsert {
				m = press(m, "i")
			}
			text := []rune(randText(r, 4) + "\n" + randText(r, 3) + "\n\n" + randText(r, 2))
			msg, desc = tea.KeyMsg{Type: tea.KeyRunes, Runes: text, Paste: true}, "paste"
		case k == 18:
			rows := len(m.lines)
			s := Position{Row: r.Intn(rows), Col: r.Intn(5)}
			e := Position{Row: min(rows-1, s.Row+r.Intn(3)), Col: r.Intn(8)}
			if e.Row == s.Row && e.Col < s.Col {
				e.Col = s.Col
			}
			m.ReplaceRange(s, e, randText(r, 3)+"\n"+randText(r, 2))
			desc = "ReplaceRange"
		default:
			if r.Intn(8) == 0 {
				m.SetContent(randDoc(r, 1+r.Intn(30)))
				desc = "SetContent"
			} else {
				m = press(m, "esc")
				desc = "esc"
			}
		}
		ctx := fmt.Sprintf("seed=%d width=%d step=%d %s mode=%v", seed, width, step, desc, m.Mode)
		var wantLines []string
		var wantRow, wantCol int
		var haveWant bool
		if km, ok := msg.(tea.KeyMsg); ok {
			wantLines, wantRow, wantCol, haveWant = expectInsert(m, km)
		}
		if msg != nil {
			m, _ = m.Update(msg)
		}
		if haveWant {
			if !reflect.DeepEqual(m.lines, wantLines) || m.cursorRow != wantRow || m.cursorCol != wantCol {
				t.Fatalf("%s: edit diverged from oracle: lines=%q cursor=(%d,%d), want lines=%q cursor=(%d,%d)",
					ctx, m.lines, m.cursorRow, m.cursorCol, wantLines, wantRow, wantCol)
			}
		}
		// Display and raw columns share one index space, so the raw bound is exact.
		if m.cursorRow < 0 || m.cursorRow >= len(m.lines) || m.cursorCol < 0 ||
			m.cursorCol > len(oracleGraphemes(m.lines[m.cursorRow])) {
			t.Fatalf("%s: cursor (%d,%d) outside buffer %q", ctx, m.cursorRow, m.cursorCol, m.lines)
		}
		assertDisplayMatchesOracle(t, &m, ctx)
		if got, want := m.View(), freshView(m); got != want {
			t.Fatalf("%s: View differs from from-scratch render\n got: %q\nwant: %q", ctx, got, want)
		}
	}
}

func TestDifferentialEditSessionsMatchOracle(t *testing.T) {
	widths := []int{9, 14, 23, 40, 80}
	seeds := 20
	if testing.Short() {
		seeds = 4
	}
	for seed := int64(1); seed <= int64(seeds); seed++ {
		for _, w := range widths {
			runDifferentialSession(t, seed*100+int64(w), w)
		}
	}
}

func TestRowCacheStalenessAfterEditResizeUndoRedo(t *testing.T) {
	m := New()
	m.SetContent("alpha beta gamma delta epsilon\nsecond line here\n\nfourth e\u0301 \U0001f468\u200d\U0001f469\u200d\U0001f467 end\nfifth")
	m.SetSize(14, 6)
	check := func(ctx string) {
		t.Helper()
		assertDisplayMatchesOracle(t, &m, ctx)
		if m.View() != freshView(m) {
			t.Fatalf("%s: stale View", ctx)
		}
	}
	check("initial")
	m = press(m, "A", "Z", "Z")
	check("typed at end of row 0")
	m.SetSize(30, 6)
	check("resize wider after edit")
	m = press(m, "esc", "j", "j", "dd")
	check("dd")
	m.SetSize(11, 6)
	check("resize narrower after delete")
	m = press(m, "u")
	check("undo")
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlR})
	check("redo")
	m = press(m, "gg", "O")
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("one\ntwo\nthree")})
	check("paste above everything")
	m = press(m, "esc", "G", "J")
	check("join")
	m = press(m, "gg", "v", "j", "j", "d")
	check("visual delete")
	m = press(m, "u", "u", "u", "u", "u")
	check("undo burst")
}

func TestRowCacheReusesUnchangedRows(t *testing.T) {
	m := New()
	m.SetContent(strings.Repeat("some words to wrap around the pane edge\n", 500))
	m.SetSize(30, 10)
	_ = m.buildDisplayLines()
	ident := func(row int) *displayLine { return &m.rowCache[row].segs[0] }
	before := make([]*displayLine, len(m.lines))
	for i := range before {
		before[i] = ident(i)
	}
	m.cursorRow = 250
	m = press(m, "A", "x")
	_ = m.View()
	reused := 0
	for i := range before {
		if i != 250 && ident(i) == before[i] {
			reused++
		}
	}
	if reused != len(before)-1 {
		t.Fatalf("a one-row edit rebuilt %d unchanged rows", len(before)-1-reused)
	}
	// A line insert shifts rows below it; their entries must follow them.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	_ = m.View()
	for i := 252; i < len(m.lines); i++ {
		if ident(i) != before[i-1] {
			t.Fatalf("row %d was rebuilt after a line insert above it", i)
		}
	}
	assertDisplayMatchesOracle(t, &m, "after enter")
}
