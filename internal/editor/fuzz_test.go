package editor

import (
	"strings"
	"testing"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
)

func assertEditorInvariant(t *testing.T, m Model) {
	t.Helper()
	if len(m.lines) == 0 || m.cursorRow < 0 || m.cursorRow >= len(m.lines) {
		t.Fatalf("invalid cursor row %d for %d lines", m.cursorRow, len(m.lines))
	}
	if m.cursorCol < 0 || m.cursorCol > len(graphemes(m.lines[m.cursorRow])) {
		t.Fatalf("cursor column %d outside row %q", m.cursorCol, m.lines[m.cursorRow])
	}
	// One index space: display clusters of the cursor row are the raw clusters.
	if n := len(m.logicalClusters(m.cursorRow)); n != len(graphemes(m.lines[m.cursorRow])) {
		t.Fatalf("row %q: %d display clusters vs %d raw", m.lines[m.cursorRow], n, len(graphemes(m.lines[m.cursorRow])))
	}
	if !utf8.ValidString(m.Content()) {
		t.Fatal("buffer is not valid UTF-8")
	}
	if m.Mode == ModeVisual {
		start, end, ok := m.SelectionRange()
		if !ok || start.Row < 0 || end.Row >= len(m.lines) || start.Row > end.Row {
			t.Fatalf("invalid selection: %v..%v", start, end)
		}
	}
}

func FuzzEditorKeySequences(f *testing.F) {
	f.Add("hello 👨‍👩‍👧‍👦", []byte("illx\x1b0vllx"))
	f.Add("e\u0301clair\nsecond", []byte("j0i!\x1bu\x12"))
	// "(\n\n)" with `%`: bracket match across an empty line (regression). Keys are
	// indices into the alphabet below; '%' is its last entry (index 14, reached by key byte 29).
	f.Add("(\n\n)", []byte{29, 29})
	f.Fuzz(func(t *testing.T, content string, keys []byte) {
		if !utf8.ValidString(content) || len(content) > 4096 || len(keys) > 512 {
			t.Skip()
		}
		// The memory clipboard is process-wide: without a reset, one iteration's
		// yank becomes the next one's paste and the corpus is no longer
		// deterministic per input.
		resetMemoryClipboard()
		m := New()
		m.SetContent(content)
		m.SetSize(17, 9)
		for _, key := range keys {
			var msg tea.KeyMsg
			switch key % 12 {
			case 0:
				msg = tea.KeyMsg{Type: tea.KeyEsc}
			case 1:
				msg = tea.KeyMsg{Type: tea.KeyBackspace}
			case 2:
				msg = tea.KeyMsg{Type: tea.KeyDelete}
			case 3:
				msg = tea.KeyMsg{Type: tea.KeyEnter}
			default:
				const alphabet = "hjlwxbivudp$0G%"
				msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{rune(alphabet[int(key)%len(alphabet)])}}
			}
			m, _ = m.Update(msg)
			assertEditorInvariant(t, m)
		}
	})
}

func FuzzWrapCoordinateOwnership(f *testing.F) {
	f.Add("alpha bravo 👩🏽‍🚀 e\u0301", uint8(5))
	f.Add("a\tb 你好", uint8(2))
	f.Fuzz(func(t *testing.T, text string, width uint8) {
		if !utf8.ValidString(text) || strings.Contains(text, "\n") || len(text) > 4096 {
			t.Skip()
		}
		segments := wrapSegments(text, int(width%80)+1)
		owned := make([]int, runeLen(text))
		for _, segment := range segments {
			if segment.startCol < 0 || segment.endCol < segment.startCol || segment.endCol > len(owned) {
				t.Fatalf("invalid segment: %+v", segment)
			}
			for col := segment.startCol; col < segment.endCol; col++ {
				owned[col]++
			}
		}
		for col, count := range owned {
			if count != 1 {
				t.Fatalf("source rune %d owned %d times", col, count)
			}
		}
	})
}

func FuzzReplaceRange(f *testing.F) {
	f.Add("before 👩🏽‍🚀 after", 0, 7, 0, 7, "X")
	f.Add("one\ntwo\nthree", -4, -2, 99, 99, "e\u0301\nnew")
	f.Fuzz(func(t *testing.T, content string, sr, sc, er, ec int, replacement string) {
		if !utf8.ValidString(content) || !utf8.ValidString(replacement) || len(content)+len(replacement) > 8192 {
			t.Skip()
		}
		m := New()
		m.SetContent(content)
		m.ReplaceRange(Position{Row: sr, Col: sc}, Position{Row: er, Col: ec}, replacement)
		assertEditorInvariant(t, m)
	})
}
