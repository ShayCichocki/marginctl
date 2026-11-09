package statenav

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ShayCichocki/marginctl/internal/state"
)

func covFixture() ([]state.StateFile, []string) {
	files := []state.StateFile{
		{Name: "timeline", Path: "/s/timeline.md", Type: state.StateTimeline},
		{Name: "open_threads", Path: "/s/open_threads.md", Type: state.StateThreads},
		{Name: "Alice", Path: "/s/characters/Alice.md", Type: state.StateCharacter, Category: "characters"},
		{Name: "Bob", Path: "/s/characters/Bob.md", Type: state.StateCharacter, Category: "characters"},
		{Name: "Paris", Path: "/s/locations/Paris.md", Type: state.StateLocation, Category: "locations"},
		{Name: "Fireball", Path: "/s/magic/Fireball.md", Type: state.StateCustom, Category: "magic"},
	}
	return files, []string{"characters", "locations", "magic", "empty"}
}

func covModel() Model {
	m := New()
	files, cats := covFixture()
	m.SetStateFilesWithCategories(files, cats)
	m.SetSize(40, 30)
	m.Focus()
	return m
}

func covKey(m Model, key string) (Model, tea.Cmd) {
	switch key {
	case "enter":
		return m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	case "esc":
		return m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	case "down":
		return m.Update(tea.KeyMsg{Type: tea.KeyDown})
	case "up":
		return m.Update(tea.KeyMsg{Type: tea.KeyUp})
	}
	return m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
}

// covCursorOn moves the cursor to the item with the given label.
func covCursorOn(t *testing.T, m *Model, label string, header bool) {
	t.Helper()
	for i, it := range m.items {
		if it.label == label && it.isHeader == header {
			m.cursor = i
			return
		}
	}
	t.Fatalf("no item %q (header=%v)", label, header)
}

func TestCovEnterEmitsTypedSelectionForEveryKind(t *testing.T) {
	for _, tc := range []struct{ label, path string }{
		{"timeline", "/s/timeline.md"},
		{"open_threads", "/s/open_threads.md"},
		{"Alice", "/s/characters/Alice.md"},
		{"Paris", "/s/locations/Paris.md"},
		{"Fireball", "/s/magic/Fireball.md"},
	} {
		t.Run(tc.label, func(t *testing.T) {
			m := covModel()
			covCursorOn(t, &m, tc.label, false)
			_, cmd := covKey(m, "enter")
			if cmd == nil {
				t.Fatal("Enter emitted nothing")
			}
			msg, ok := cmd().(StateSelectedMsg)
			if !ok || msg.Path != tc.path {
				t.Fatalf("msg = %#v, want StateSelectedMsg{%s}", cmd(), tc.path)
			}
		})
	}
}

func TestCovEnterOnStyleCategoryFileSelectsVoiceGuide(t *testing.T) {
	// The voice guide lives in the custom "style" category.
	m := New()
	m.SetStateFilesWithCategories([]state.StateFile{
		{Name: "voice", Path: "/s/style/voice.md", Type: state.StateCustom, Category: "style"},
	}, []string{"characters", "locations", "style"})
	m.Focus()
	covCursorOn(t, &m, "voice", false)
	_, cmd := covKey(m, "enter")
	if cmd == nil {
		t.Fatal("no cmd")
	}
	if msg, ok := cmd().(StateSelectedMsg); !ok || msg.Path != "/s/style/voice.md" {
		t.Fatalf("msg = %#v", cmd())
	}
}

func TestCovEnterOnHeaderEmitsNothing(t *testing.T) {
	m := covModel()
	for i, it := range m.items {
		if !it.isHeader {
			continue
		}
		m.cursor = i
		if _, cmd := covKey(m, "enter"); cmd != nil {
			t.Fatalf("Enter on header %q emitted %#v", it.label, cmd())
		}
	}
}

func TestCovBlurredModelIgnoresKeys(t *testing.T) {
	m := covModel()
	m.Blur()
	if m.Focused() {
		t.Fatal("still focused")
	}
	before := m.cursor
	nm, cmd := covKey(m, "j")
	if cmd != nil || nm.cursor != before {
		t.Fatal("blurred model reacted to input")
	}
	if _, cmd = covKey(m, "enter"); cmd != nil {
		t.Fatal("blurred model emitted a selection")
	}
}

func TestCovMotionClampsAtBothEnds(t *testing.T) {
	m := covModel()
	last := len(m.items) - 1
	m, _ = covKey(m, "G")
	if m.cursor != last {
		t.Fatalf("G -> %d, want %d", m.cursor, last)
	}
	for i := 0; i < 3; i++ {
		m, _ = covKey(m, "j")
	}
	if m.cursor != last {
		t.Fatalf("j past end -> %d, want %d", m.cursor, last)
	}
	m, _ = covKey(m, "down")
	if m.cursor != last {
		t.Fatalf("down past end -> %d", m.cursor)
	}
	m, _ = covKey(m, "g")
	if m.cursor != 0 {
		t.Fatalf("g -> %d", m.cursor)
	}
	for i := 0; i < 3; i++ {
		m, _ = covKey(m, "k")
	}
	m, _ = covKey(m, "up")
	if m.cursor != 0 {
		t.Fatalf("k before start -> %d", m.cursor)
	}
	m, _ = covKey(m, "j")
	if m.cursor != 1 {
		t.Fatalf("j -> %d", m.cursor)
	}
	m, _ = covKey(m, "k")
	if m.cursor != 0 {
		t.Fatalf("k -> %d", m.cursor)
	}
}

func TestCovEscIsNotConsumedSoAppCanBackOut(t *testing.T) {
	// Esc backs out to the editor in app.Update; the navigator must neither
	// swallow it into a selection/creation message nor move the cursor.
	m := covModel()
	covCursorOn(t, &m, "Bob", false)
	before := m.cursor
	nm, cmd := covKey(m, "esc")
	if cmd != nil || nm.cursor != before {
		t.Fatalf("Esc changed state: cursor %d->%d cmd=%v", before, nm.cursor, cmd != nil)
	}
}

func TestCovCreateMessagesAreContextAware(t *testing.T) {
	type want struct {
		ty  state.StateType
		cat string
	}
	cases := []struct {
		label  string
		header bool
		want   *want
	}{
		{"Characters", true, &want{state.StateCharacter, "characters"}},
		{"Alice", false, &want{state.StateCharacter, "characters"}},
		{"Locations", true, &want{state.StateLocation, "locations"}},
		{"Paris", false, &want{state.StateLocation, "locations"}},
		{"Magic", true, &want{state.StateCustom, "magic"}},
		{"Fireball", false, &want{state.StateCustom, "magic"}},
		{"Empty", true, &want{state.StateCustom, "empty"}},
		{"Timeline", true, nil},
		{"timeline", false, nil},
		{"Threads", true, nil},
		{"open_threads", false, nil},
	}
	for _, tc := range cases {
		m := covModel()
		covCursorOn(t, &m, tc.label, tc.header)
		_, cmd := covKey(m, "n")
		if tc.want == nil {
			if cmd != nil {
				t.Errorf("n on %q emitted %#v, want nothing", tc.label, cmd())
			}
			continue
		}
		if cmd == nil {
			t.Errorf("n on %q emitted nothing", tc.label)
			continue
		}
		msg, ok := cmd().(StateCreateMsg)
		if !ok || msg.Type != tc.want.ty || msg.Category != tc.want.cat {
			t.Errorf("n on %q = %#v, want %+v", tc.label, cmd(), *tc.want)
		}
	}
	m := covModel()
	if _, cmd := covKey(m, "N"); cmd == nil {
		t.Fatal("N emitted nothing")
	} else if _, ok := cmd().(CategoryCreateMsg); !ok {
		t.Fatalf("N = %#v", cmd())
	}
}

func TestCovSelectionStaysInBoundsWhenListShrinks(t *testing.T) {
	m := covModel()
	m, _ = covKey(m, "G")
	files, _ := covFixture()
	m.SetStateFilesWithCategories(files[:3], []string{"characters"})
	if m.cursor < 0 || m.cursor >= len(m.items) {
		t.Fatalf("cursor %d out of range [0,%d)", m.cursor, len(m.items))
	}
	// Enter now either selects a real file or nothing, but never panics.
	_, cmd := covKey(m, "enter")
	if cmd != nil {
		if msg, ok := cmd().(StateSelectedMsg); !ok || msg.Path == "" {
			t.Fatalf("msg = %#v", cmd())
		}
	}
	if view := m.View(); view == "" {
		t.Fatal("empty view after shrink")
	}
}

func TestCovEmptyListIsSafe(t *testing.T) {
	m := New()
	m.SetSize(30, 10)
	m.Focus()
	if view := m.View(); !strings.Contains(view, "No state files") {
		// An empty navigator still builds the default categories, so either
		// message or headers is acceptable; it just must render.
		if view == "" {
			t.Fatal("empty navigator rendered nothing")
		}
	}
	m.SetStateFiles(nil)
	// With no files the navigator still shows its default category headers;
	// Enter on a header is a no-op.
	for _, k := range []string{"enter", "j", "k", "g", "G", "n", "esc"} {
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("key %q panicked on empty/header-only navigator: %v", k, r)
				}
			}()
			m, _ = covKey(m, k)
		}()
	}
}

func TestCovZeroItemNavigatorNeverPanicsOnMotionThenEnter(t *testing.T) {
	// A model that was never populated has zero items. j/G drive the cursor to
	// -1 (clamp in moveCursor/skipTopLevelHeaders uses len-1), after which
	// Enter indexes m.items[-1].
	m := New()
	m.Focus()
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("panic on unpopulated navigator: %v", r)
		}
	}()
	for _, k := range []string{"j", "G", "enter"} {
		m, _ = covKey(m, k)
	}
	if m.cursor < 0 {
		t.Fatalf("cursor = %d on an empty navigator", m.cursor)
	}
}

func TestCovSetCurrentFileMovesCursorAndMarksCurrent(t *testing.T) {
	m := covModel()
	m.SetCurrentFile("/s/locations/Paris.md")
	if got := m.items[m.cursor]; got.stateFile == nil || got.stateFile.Path != "/s/locations/Paris.md" {
		t.Fatalf("cursor on %+v", got)
	}
	before := m.cursor
	m.SetCurrentFile("/s/does/not/exist.md")
	if m.cursor != before {
		t.Fatalf("unknown path moved the cursor %d -> %d", before, m.cursor)
	}
	if m.currentFile != "/s/does/not/exist.md" {
		t.Fatalf("currentFile = %q", m.currentFile)
	}
	if view := m.View(); view == "" {
		t.Fatal("view empty")
	}
}

func TestCovViewHandlesTinyAndZeroSizes(t *testing.T) {
	m := covModel()
	m.SetSize(0, 0)
	if m.View() != "" {
		t.Fatal("zero-width view should be empty")
	}
	for _, sz := range [][2]int{{1, 1}, {3, 2}, {8, 3}} {
		m.SetSize(sz[0], sz[1])
		_ = m.View() // must not panic
	}
	m.Focus()
	m.SetSize(30, 4)
	m, _ = covKey(m, "G")
	if v := m.View(); !strings.Contains(v, "STATE") {
		t.Fatalf("scrolled view lost its marker:\n%s", v)
	}
}

func TestCovHeaderOrdering(t *testing.T) {
	m := covModel()
	var labels []string
	for _, it := range m.items {
		if it.isHeader {
			labels = append(labels, it.label)
		}
	}
	want := "Timeline,Threads,Characters,Locations,Magic,Empty"
	if got := strings.Join(labels, ","); got != want {
		t.Fatalf("headers = %s, want %s", got, want)
	}
}
