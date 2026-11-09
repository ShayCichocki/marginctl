package editor

import (
	"fmt"
	"math/rand"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// refMatchBracket is an independent `%` reference over grapheme slices.
func refMatchBracket(lines []string, row, col int) (int, int, bool) {
	type pos struct{ r, c int }
	var flat []pos
	var syms []string
	for r, l := range lines {
		for c, g := range graphemes(l) {
			flat = append(flat, pos{r, c})
			syms = append(syms, g)
		}
	}
	idx := -1
	for i, p := range flat {
		if p.r == row && p.c >= col {
			if _, ok := bracketPairs[firstRune(syms[i])]; ok {
				idx = i
				break
			}
		}
	}
	if idx < 0 {
		return 0, 0, false
	}
	open := firstRune(syms[idx])
	info := bracketPairs[open]
	step := -1
	if info.forward {
		step = 1
	}
	depth := 0
	for i := idx; i >= 0 && i < len(flat); i += step {
		switch firstRune(syms[i]) {
		case open:
			depth++
		case info.match:
			depth--
			if depth == 0 {
				return flat[i].r, flat[i].c, true
			}
		}
	}
	return 0, 0, false
}

func TestMatchBracketForwardAcrossEmptyLineDoesNotPanic(t *testing.T) {
	// Minimized crash: "(" followed by an empty line before its partner.
	m := New()
	m.SetSize(40, 10)
	m.SetContent("(\n\n)")
	m = press(m, "%")
	if m.cursorRow != 2 || m.cursorCol != 0 {
		t.Fatalf("%% from '(' = (%d,%d), want (2,0)", m.cursorRow, m.cursorCol)
	}
	m = press(m, "%")
	if m.cursorRow != 0 || m.cursorCol != 0 {
		t.Fatalf("%% back from ')' = (%d,%d), want (0,0)", m.cursorRow, m.cursorCol)
	}
	// Trailing empty lines and no partner: must report no match, not panic.
	m.SetContent("(\n\n\n")
	_ = press(m, "%")
}

func TestMatchBracketMatchesReference(t *testing.T) {
	pieces := []string{"(", ")", "[", "]", "{", "}", "a", " ", "e\u0301", "(\u0301", "\U0001f468\u200d\U0001f469", "世", ""}
	r := rand.New(rand.NewSource(7))
	iters := 20000
	if testing.Short() {
		iters = 4000
	}
	for i := 0; i < iters; i++ {
		nl := 1 + r.Intn(5)
		lines := make([]string, nl)
		for j := range lines {
			var b strings.Builder
			for k := r.Intn(6); k > 0; k-- {
				b.WriteString(pieces[r.Intn(len(pieces))])
			}
			lines[j] = b.String()
		}
		m := New()
		m.SetSize(40, 10)
		m.SetContent(strings.Join(lines, "\n"))
		row := r.Intn(nl)
		col := r.Intn(len(graphemes(lines[row])) + 2)
		gr, gc, gok := m.matchBracket(row, col)
		wr, wc, wok := refMatchBracket(lines, row, col)
		if gok != wok || (gok && (gr != wr || gc != wc)) {
			t.Fatalf("matchBracket(%q,%d,%d) = (%d,%d,%v), want (%d,%d,%v)", lines, row, col, gr, gc, gok, wr, wc, wok)
		}
	}
}

func TestRenderStateRevalidatesByRowTextWithoutRevisionBump(t *testing.T) {
	m := New()
	m.SetSize(30, 6)
	m.SetContent("*short*\nsecond")
	_ = m.View() // populate every cache
	rev := m.revision
	m.lines[0] = "*a much longer line of italic text here that wraps around*"
	m.lines[1] = "x"
	if m.revision != rev {
		t.Fatal("test must not bump revision")
	}
	m.viewCacheValid = false // force a re-render over the stale display cache
	_ = m.View()             // must not panic
	// Direct state check: styling slices agree with current row clusters.
	for row := range m.lines {
		st := m.renderState(row)
		if want := graphemes(m.lines[row]); !reflect.DeepEqual(st.clusters, want) || len(st.italic) != len(want) || len(st.search) != len(want) {
			t.Fatalf("renderState(%d) stale: %q (italic %d) want %q", row, st.clusters, len(st.italic), want)
		}
	}
}

func TestASCIIFastPathKeepsCRLFAndLoneCR(t *testing.T) {
	cases := []string{"a\r\nb", "\r\n", "\r\n\r\n", "\r", "a\r", "a\rb", "\ra", "x\r\n\u0301", "\r\n\u200d", "ab\r\ncd\r\n"}
	for _, s := range cases {
		if got, want := graphemes(s), oracleGraphemes(s); !sliceEq(got, want) {
			t.Fatalf("graphemes(%q) = %q, want %q", s, got, want)
		}
		n := len(oracleGraphemes(s))
		for col := -1; col <= n+1; col++ {
			if col >= 0 {
				off, cnt, _ := graphemeOffset(s, col)
				wb, wa := oracleSplit(s, col)
				if s[:off] != wb || s[off:] != wa || cnt != min(col, n) {
					t.Fatalf("graphemeOffset(%q,%d) = %d,%d; oracle split (%q,%q)", s, col, off, cnt, wb, wa)
				}
			}
			if got, want := deleteRuneAt(s, col), oracleDeleteRuneAt(s, col); got != want {
				t.Fatalf("deleteRuneAt(%q,%d) = %q, want %q", s, col, got, want)
			}
			for _, ins := range []string{"x", "\n", "\u0301"} {
				gs, gc := insertRunes(s, col, ins)
				ws, wc := oracleInsertRunes(s, col, ins)
				if gs != ws || gc != wc {
					t.Fatalf("insertRunes(%q,%d,%q) = (%q,%d), want (%q,%d)", s, col, ins, gs, gc, ws, wc)
				}
			}
		}
		for i := 0; i < len(s); i++ {
			if simpleASCII(s, i) && s[i] == '\r' {
				t.Fatalf("simpleASCII(%q,%d) true for CR", s, i)
			}
		}
		if g := oracleGraphemes(s); len(g) > 0 && graphemeLen(s) != len(g[0]) {
			t.Fatalf("graphemeLen(%q) = %d, want %d", s, graphemeLen(s), len(g[0]))
		}
	}
}

// TestCachedNormalEditsMatchCacheFreeTwin runs the same randomized normal-mode
// edits and ReplaceRange calls on a model with warm caches and on a twin whose
// derived caches are dropped before every operation (a from-scratch rebuild).
func TestCachedNormalEditsMatchCacheFreeTwin(t *testing.T) {
	// Hermetic: the twins must not see each other's writes through the OS clipboard.
	prevW, prevR := osClipboardWrite, osClipboardRead
	osClipboardWrite = func(string) {}
	osClipboardRead = func() (string, bool) { return "", false }
	t.Cleanup(func() { osClipboardWrite, osClipboardRead = prevW, prevR })
	ops := []string{"x", "dd", "p", "P", "J", "rz", "r\u0301", "3x", "2dd", "yy", "u", "D", "~", "dw", "%", "o", "O"}
	sessions := 40
	if testing.Short() {
		sessions = 8
	}
	for seed := int64(1); seed <= int64(sessions); seed++ {
		r := rand.New(rand.NewSource(seed))
		doc := randDoc(r, 1+r.Intn(12)) + "\n(a\n\n)\n"
		warm, cold := New(), New()
		w, h := 9+r.Intn(30), 5+r.Intn(6)
		for _, m := range []*Model{&warm, &cold} {
			m.SetContent(doc)
			m.SetSize(w, h)
		}
		for step := 0; step < 80; step++ {
			cold.rowCache, cold.displayCache, cold.displayRowRanges = nil, nil, nil
			cold.renderLines, cold.viewCacheValid, cold.searchCache = nil, false, nil
			ctx := fmt.Sprintf("seed=%d step=%d", seed, step)
			if r.Intn(5) == 0 {
				rows := len(warm.lines)
				s := Position{Row: r.Intn(rows), Col: r.Intn(5)}
				e := Position{Row: min(rows-1, s.Row+r.Intn(3)), Col: r.Intn(8)}
				if e.Row == s.Row && e.Col < s.Col {
					e.Col = s.Col
				}
				text := randText(r, 3) + "\n" + randText(r, 2)
				warm.ReplaceRange(s, e, text)
				cold.ReplaceRange(s, e, text)
				ctx += " ReplaceRange"
			} else {
				op := ops[r.Intn(len(ops))]
				if warm.Mode != ModeNormal {
					warm = press(warm, "esc")
					cold = press(cold, "esc")
				}
				for _, mp := range []*Model{&warm, &cold} {
					*mp, _ = mp.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(op)})
				}
				ctx += " " + op
			}
			if !reflect.DeepEqual(warm.lines, cold.lines) || warm.cursorRow != cold.cursorRow || warm.cursorCol != cold.cursorCol {
				t.Fatalf("%s: warm lines=%q cur=(%d,%d), cold lines=%q cur=(%d,%d)", ctx,
					warm.lines, warm.cursorRow, warm.cursorCol, cold.lines, cold.cursorRow, cold.cursorCol)
			}
			assertDisplayMatchesOracle(t, &warm, ctx)
			if got, want := warm.View(), freshView(cold); got != want {
				t.Fatalf("%s: View differs\n got: %q\nwant: %q", ctx, got, want)
			}
		}
	}
}
