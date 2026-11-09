package editor

import (
	"strings"
	"testing"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
)

// runes returns a KeyRunes message carrying s (used both for single keypresses
// like "x" in normal mode and for typed/pasted text in insert mode).
func runes(s string) tea.KeyMsg {
	return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)}
}

// feed drives a sequence of key messages through Update, returning the model.
func feed(m Model, msgs ...tea.KeyMsg) Model {
	for _, msg := range msgs {
		m, _ = m.Update(msg)
	}
	return m
}

// Common multibyte fixtures: em-dash (3 bytes), curly quote (3 bytes),
// e-acute (2 bytes), and an emoji (4 bytes).
const (
	emDash   = "—"
	curlyOpn = "“"
	eAcute   = "é"
	emoji    = "😀"
)

func TestInsertMultibyteAdvancesByColumn(t *testing.T) {
	m := New()
	m.SetSize(80, 10)

	m.Mode = ModeInsert
	m = feed(m, runes("a"+emDash+eAcute+emoji+"b"))

	if got, want := m.Content(), "a—é😀b"; got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
	// Five runes typed -> cursor sits one column past the last (rune index 5).
	if m.cursorCol != 5 {
		t.Fatalf("cursorCol = %d, want 5 (one column per rune, not per byte)", m.cursorCol)
	}
}

func TestMotionOverMultibyteIsPerRune(t *testing.T) {
	m := New()
	m.SetSize(80, 10)
	m.SetContent("a" + emDash + eAcute + emoji + "b") // 5 runes, 10 bytes

	// l five times from col 0: normal mode clamps to last char (index 4).
	for i := 0; i < 5; i++ {
		m = feed(m, runes("l"))
	}
	if m.cursorCol != 4 {
		t.Fatalf("after 5×l cursorCol = %d, want 4 (clamped to last rune)", m.cursorCol)
	}

	// h back to the start, one rune at a time.
	for i := 0; i < 4; i++ {
		m = feed(m, runes("h"))
	}
	if m.cursorCol != 0 {
		t.Fatalf("after 4×h cursorCol = %d, want 0", m.cursorCol)
	}
}

func TestDeleteMultibyteWithX(t *testing.T) {
	m := New()
	m.SetSize(80, 10)
	m.SetContent("a" + emDash + eAcute + "b")

	// Move onto the em-dash (col 1) and delete it.
	m = feed(m, runes("l"), runes("x"))

	if got, want := m.Content(), "aéb"; got != want {
		t.Fatalf("content = %q, want %q — x must remove exactly one rune", got, want)
	}
	if !utf8.ValidString(m.Content()) {
		t.Fatalf("content is not valid UTF-8: %q", m.Content())
	}
}

func TestBackspaceMultibyteInInsert(t *testing.T) {
	m := New()
	m.SetSize(80, 10)
	m.SetContent("caf" + eAcute)
	// Enter insert after the é (append), then backspace once.
	m = feed(m, runes("A"), tea.KeyMsg{Type: tea.KeyBackspace})

	if got, want := m.Content(), "caf"; got != want {
		t.Fatalf("content = %q, want %q — backspace over é must remove one rune", got, want)
	}
	if !utf8.ValidString(m.Content()) {
		t.Fatalf("backspacing over é yielded invalid UTF-8: %q", m.Content())
	}
}

func TestWrapNeverSplitsRune(t *testing.T) {
	// A spaceless line of em-dashes longer than the wrap width must break on
	// rune boundaries: every segment is valid UTF-8 and the pieces reconstruct
	// the original exactly (no space was consumed).
	line := strings.Repeat(emDash, 250)
	wrapped := wrapLine(line, wrapWidth)

	if strings.Join(wrapped, "") != line {
		t.Fatal("reconstructed wrapped line does not equal the original")
	}
	for i, seg := range wrapped {
		if !utf8.ValidString(seg) {
			t.Fatalf("segment %d is not valid UTF-8: %q", i, seg)
		}
		if rc := utf8.RuneCountInString(seg); rc > wrapWidth {
			t.Fatalf("segment %d has %d runes, exceeds width %d", i, rc, wrapWidth)
		}
	}
}

func TestReplaceRangeMultibyteColumns(t *testing.T) {
	m := New()
	// "café — bar": rune indices c0 a1 f2 é3 (space)4 —5 (space)6 b7 a8 r9.
	m.SetContent("café — bar")

	// Replace the em-dash at rune col 5 with "to".
	m.ReplaceRange(Position{Row: 0, Col: 5}, Position{Row: 0, Col: 5}, "to")

	if got, want := m.Content(), "café to bar"; got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
}

func TestWordMotionOverMultibyte(t *testing.T) {
	m := New()
	m.SetSize(80, 10)
	m.SetContent("café " + emDash + " over") // words: café(0-3) —(5) over(7-10)

	m = feed(m, runes("w"))
	if m.cursorCol != 5 {
		t.Fatalf("after w cursorCol = %d, want 5 (start of second word)", m.cursorCol)
	}
	m = feed(m, runes("w"))
	if m.cursorCol != 7 {
		t.Fatalf("after 2×w cursorCol = %d, want 7 (start of third word)", m.cursorCol)
	}
}

func TestDollarLandsOnLastCharMultibyte(t *testing.T) {
	m := New()
	m.SetSize(80, 10)
	m.SetContent("caf" + eAcute) // 4 runes, index of last char = 3

	m = feed(m, runes("$"))
	if m.cursorCol != 3 {
		t.Fatalf("$ cursorCol = %d, want 3 (last rune, not one past)", m.cursorCol)
	}
	// x at end-of-line after $ must delete the final character.
	m = feed(m, runes("x"))
	if got, want := m.Content(), "caf"; got != want {
		t.Fatalf("after $x content = %q, want %q", got, want)
	}
}
