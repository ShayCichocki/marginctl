package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ShayCichocki/marginctl/internal/projectnav"
	"github.com/ShayCichocki/marginctl/internal/state"
	"github.com/ShayCichocki/marginctl/internal/statenav"
)

// typeText feeds s into the active prompt one key at a time (spaces as the
// KeySpace a real terminal sends).
func typeText(m Model, s string) Model {
	for _, r := range s {
		if r == ' ' {
			m = update(m, tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}})
			continue
		}
		m = update(m, keyRunes(string(r)))
	}
	return m
}

func promptModel(t *testing.T) (Model, string) {
	t.Helper()
	isolateAppEnv(t)
	m, ch1, _ := openTestModel(t)
	return m, ch1
}

func chapterFiles(t *testing.T, m Model) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(m.project.Root, "chapters"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), ".") {
			names = append(names, e.Name())
		}
	}
	return names
}

// ---- search prompt -------------------------------------------------------

func TestSearchPromptEnterFindsMatch(t *testing.T) {
	m, _ := promptModel(t)
	m = update(m, keyRunes("/"))
	if m.activePrompt() != promptSearch {
		t.Fatal("/ did not open the search prompt")
	}
	m = typeText(m, "Hello")
	m = pressEnter(m)
	if m.activePrompt() != promptNone || m.searchBuffer != "" {
		t.Fatalf("prompt not closed: %v %q", m.activePrompt(), m.searchBuffer)
	}
	if m.noticeIsErr {
		t.Fatalf("unexpected error notice %q", m.notice)
	}
}

func TestSearchPromptEnterReportsPatternNotFound(t *testing.T) {
	m, _ := promptModel(t)
	m = update(m, keyRunes("/"))
	m = typeText(m, "zzqqxx")
	m = pressEnter(m)
	if !m.noticeIsErr || m.notice != "Pattern not found: zzqqxx" {
		t.Fatalf("notice = %q err=%v", m.notice, m.noticeIsErr)
	}
	if m.activePrompt() != promptNone {
		t.Fatal("prompt still active")
	}
}

func TestSearchPromptEscClearsAndEmptyEnterIsSilent(t *testing.T) {
	m, _ := promptModel(t)
	m = update(m, keyRunes("/"))
	m = typeText(m, "abc")
	if m.searchBuffer != "abc" {
		t.Fatalf("buffer = %q", m.searchBuffer)
	}
	m = pressEsc(m)
	if m.activePrompt() != promptNone || m.searchBuffer != "" || m.notice != "" {
		t.Fatalf("esc: prompt=%v buf=%q notice=%q", m.activePrompt(), m.searchBuffer, m.notice)
	}
	m = update(m, keyRunes("/"))
	m = pressEnter(m)
	if m.activePrompt() != promptNone || m.notice != "" {
		t.Fatalf("empty enter: prompt=%v notice=%q", m.activePrompt(), m.notice)
	}
}

func TestSearchPromptBackspaceAndSpace(t *testing.T) {
	m, _ := promptModel(t)
	m = update(m, keyRunes("/"))
	m = typeText(m, "ab c")
	if m.searchBuffer != "ab c" {
		t.Fatalf("buffer = %q", m.searchBuffer)
	}
	m = update(m, tea.KeyMsg{Type: tea.KeyBackspace})
	if m.searchBuffer != "ab " {
		t.Fatalf("buffer after backspace = %q", m.searchBuffer)
	}
}

// ---- new chapter prompt --------------------------------------------------

func openChapterPrompt(m Model) Model {
	m = update(m, projectnav.ChapterCreateMsg{})
	return m
}

func TestNewChapterPromptBlockedWhenDirty(t *testing.T) {
	m, _ := promptModel(t)
	m = dirtyEditor(t, m)
	m = update(m, projectnav.ChapterCreateMsg{})
	if m.activePrompt() != promptNone || !m.noticeIsErr {
		t.Fatalf("dirty buffer allowed chapter prompt: prompt=%v notice=%q", m.activePrompt(), m.notice)
	}
}

func TestNewChapterPromptEmptyAndEscCancel(t *testing.T) {
	m, _ := promptModel(t)
	before := chapterFiles(t, m)
	m = openChapterPrompt(m)
	if m.activePrompt() != promptChapter {
		t.Fatal("chapter prompt not opened")
	}
	m = pressEnter(m)
	if m.activePrompt() != promptNone || m.err != nil {
		t.Fatalf("empty enter: prompt=%v err=%v", m.activePrompt(), m.err)
	}
	m = openChapterPrompt(m)
	m = typeText(m, "Draft")
	m = pressEsc(m)
	if m.activePrompt() != promptNone || m.chapterName != "" {
		t.Fatalf("esc: prompt=%v name=%q", m.activePrompt(), m.chapterName)
	}
	if got := chapterFiles(t, m); strings.Join(got, ",") != strings.Join(before, ",") {
		t.Fatalf("cancelled prompt created files: %v", got)
	}
}

func TestNewChapterPromptCreatesLoadsAndRefreshesNav(t *testing.T) {
	m, _ := promptModel(t)
	m = openChapterPrompt(m)
	m = typeText(m, "Third Act")
	m = pressEnter(m)
	if m.err != nil {
		t.Fatalf("err: %v", m.err)
	}
	want := filepath.Join(m.project.Root, "chapters", "003_Third Act.md")
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("chapter not created: %v (files %v)", err, chapterFiles(t, m))
	}
	if m.project.CurrentFile != want || !strings.HasPrefix(m.editor.Content(), "# Third Act") {
		t.Fatalf("new chapter not loaded: file=%q content=%q", m.project.CurrentFile, m.editor.Content())
	}
	if m.focus != FocusEditor || m.activePrompt() != promptNone {
		t.Fatalf("focus=%v prompt=%v", m.focus, m.activePrompt())
	}
	found := false
	for _, ch := range m.project.ListChapters() {
		found = found || ch.Path == want
	}
	if !found {
		t.Fatal("project chapter list missing new chapter")
	}
}

func TestNewChapterPromptSanitizesTraversal(t *testing.T) {
	m, _ := promptModel(t)
	root := m.project.Root
	m = openChapterPrompt(m)
	m = typeText(m, "../evil")
	m = pressEnter(m)
	if m.err != nil {
		t.Fatalf("err: %v", m.err)
	}
	if want := filepath.Join(root, "chapters", "003_evil.md"); m.project.CurrentFile != want {
		t.Fatalf("CurrentFile = %q, want %q", m.project.CurrentFile, want)
	}
	for _, escaped := range []string{filepath.Join(root, "evil.md"), filepath.Join(root, "evil"), filepath.Join(filepath.Dir(root), "evil.md")} {
		if fileExists(escaped) {
			t.Fatalf("%s escaped chapters/", escaped)
		}
	}
}

func TestNewChapterPromptRejectsUnusableName(t *testing.T) {
	m, ch1 := promptModel(t)
	before := chapterFiles(t, m)
	m = openChapterPrompt(m)
	m = typeText(m, "..")
	m = pressEnter(m)
	if m.err == nil {
		t.Fatal("name with nothing usable accepted")
	}
	if m.activePrompt() != promptNone || m.project.CurrentFile != ch1 {
		t.Fatalf("prompt=%v current=%q", m.activePrompt(), m.project.CurrentFile)
	}
	if got := chapterFiles(t, m); strings.Join(got, ",") != strings.Join(before, ",") {
		t.Fatalf("files changed: %v", got)
	}
}

// ---- new state file prompt ----------------------------------------------

func openStatePrompt(m Model, typ state.StateType, category string) Model {
	return update(m, statenav.StateCreateMsg{Type: typ, Category: category})
}

func TestNewStatePromptBlockedWhenDirty(t *testing.T) {
	m, _ := promptModel(t)
	m = dirtyEditor(t, m)
	m = openStatePrompt(m, state.StateCharacter, "")
	if m.activePrompt() != promptNone || !m.noticeIsErr {
		t.Fatalf("prompt=%v notice=%q", m.activePrompt(), m.notice)
	}
}

func TestNewStatePromptEmptyAndEscCancel(t *testing.T) {
	m, ch1 := promptModel(t)
	m = openStatePrompt(m, state.StateCharacter, "")
	if m.activePrompt() != promptStateName {
		t.Fatal("state prompt not opened")
	}
	m = pressEnter(m)
	if m.activePrompt() != promptNone || m.err != nil || m.project.CurrentFile != ch1 {
		t.Fatalf("empty enter: prompt=%v err=%v cur=%q", m.activePrompt(), m.err, m.project.CurrentFile)
	}
	m = openStatePrompt(m, state.StateCharacter, "")
	m = typeText(m, "Ghost")
	m = pressEsc(m)
	if m.stateName != "" || m.state.EntryExists(state.StateCharacter, "", "Ghost") {
		t.Fatal("cancelled state prompt created an entry")
	}
}

func TestNewStatePromptCreatesAndRefreshesNav(t *testing.T) {
	m, _ := promptModel(t)
	m = openStatePrompt(m, state.StateCharacter, "")
	m = typeText(m, "Ann Lee")
	m = pressEnter(m)
	if m.err != nil {
		t.Fatalf("err: %v", m.err)
	}
	if !m.state.EntryExists(state.StateCharacter, "", "Ann Lee") {
		t.Fatal("character not created")
	}
	cur := m.project.CurrentFile
	if !strings.HasPrefix(cur, filepath.Join(m.project.Root, "state")) || !fileExists(cur) {
		t.Fatalf("new state file not loaded: %q", cur)
	}
	if m.focus != FocusEditor || m.stateCategory != "" || m.activePrompt() != promptNone {
		t.Fatalf("focus=%v cat=%q prompt=%v", m.focus, m.stateCategory, m.activePrompt())
	}
}

func TestNewStatePromptSanitizesTraversal(t *testing.T) {
	m, _ := promptModel(t)
	root := m.project.Root
	m = openStatePrompt(m, state.StateCharacter, "")
	m = typeText(m, "../../escape")
	m = pressEnter(m)
	if m.err != nil {
		t.Fatalf("err: %v", m.err)
	}
	if !m.state.EntryExists(state.StateCharacter, "", "../../escape") {
		t.Fatal("sanitized entry not created inside state/characters")
	}
	for _, escaped := range []string{filepath.Join(root, "escape.md"), filepath.Join(root, "state", "escape.md"), filepath.Join(filepath.Dir(root), "escape.md")} {
		if fileExists(escaped) {
			t.Fatalf("%s escaped", escaped)
		}
	}
}

func TestNewStatePromptRejectsUnusableAndDuplicate(t *testing.T) {
	m, ch1 := promptModel(t)
	m = openStatePrompt(m, state.StateCharacter, "")
	m = typeText(m, "..")
	m = pressEnter(m)
	// ".." is just an entry title now; it slugs to a safe "entry.md".
	if m.err != nil || filepath.Base(m.project.CurrentFile) != "entry.md" || filepath.Base(filepath.Dir(m.project.CurrentFile)) != "characters" {
		t.Fatalf("dotdot name: err=%v cur=%q (was %q)", m.err, m.project.CurrentFile, ch1)
	}

	// Duplicate: second creation must not overwrite the first.
	m = openStatePrompt(m, state.StateCharacter, "")
	m = typeText(m, "Dup")
	m = pressEnter(m)
	path := m.project.CurrentFile
	if err := os.WriteFile(path, []byte("hand written"), 0644); err != nil {
		t.Fatal(err)
	}
	m = openStatePrompt(m, state.StateCharacter, "")
	m = typeText(m, "Dup")
	m = pressEnter(m)
	if m.err == nil {
		t.Fatal("duplicate state file accepted")
	}
	if data, _ := os.ReadFile(path); string(data) != "hand written" {
		t.Fatalf("existing state file overwritten: %q", data)
	}
}

// ---- new category prompt -------------------------------------------------

func TestNewCategoryPrompt(t *testing.T) {
	m, ch1 := promptModel(t)
	m = update(m, statenav.CategoryCreateMsg{})
	if m.activePrompt() != promptCategory {
		t.Fatal("category prompt not opened")
	}
	m = pressEnter(m)
	if m.activePrompt() != promptNone || m.err != nil {
		t.Fatalf("empty enter: prompt=%v err=%v", m.activePrompt(), m.err)
	}

	m = update(m, statenav.CategoryCreateMsg{})
	m = typeText(m, "Spells")
	m = pressEsc(m)
	if fileExists(filepath.Join(m.project.Root, "state", "Spells")) {
		t.Fatal("esc created the category")
	}

	m = update(m, statenav.CategoryCreateMsg{})
	m = typeText(m, "Spells")
	m = pressEnter(m)
	if m.err != nil {
		t.Fatalf("err: %v", m.err)
	}
	if info, err := os.Stat(filepath.Join(m.project.Root, "state", "Spells")); err != nil || !info.IsDir() {
		t.Fatalf("category dir missing: %v", err)
	}
	if !contains(m.state.Categories(), "Spells") {
		t.Fatalf("categories = %v", m.state.Categories())
	}
	if m.project.CurrentFile != ch1 {
		t.Fatal("category creation changed the open file")
	}
}

func TestNewCategoryPromptTraversalAndUnusable(t *testing.T) {
	m, _ := promptModel(t)
	root := m.project.Root
	m = update(m, statenav.CategoryCreateMsg{})
	m = typeText(m, "../../outside")
	m = pressEnter(m)
	if fileExists(filepath.Join(filepath.Dir(root), "outside")) || fileExists(filepath.Join(root, "outside")) {
		t.Fatal("category escaped state/")
	}
	if !fileExists(filepath.Join(root, "state", "outside")) {
		t.Fatalf("sanitized category missing (err=%v)", m.err)
	}

	m = update(m, statenav.CategoryCreateMsg{})
	m = typeText(m, "..")
	m = pressEnter(m)
	if m.err == nil {
		t.Fatal("unusable category name accepted")
	}
}

// ---- path input modes ----------------------------------------------------

func openPathPrompt(m Model, mode string) Model {
	m.setPrompt(promptPath)
	m.pathInputMode = mode
	m.pathBuffer = ""
	m.prompt.Reset()
	return m
}

func TestPathPromptEscAndEmptyEnterCancel(t *testing.T) {
	m, ch1 := promptModel(t)
	m = openPathPrompt(m, "open")
	m = typeText(m, "/nowhere")
	m = pressEsc(m)
	if m.activePrompt() != promptNone || m.pathBuffer != "" || m.pathInputMode != "" {
		t.Fatalf("esc: prompt=%v buf=%q mode=%q", m.activePrompt(), m.pathBuffer, m.pathInputMode)
	}
	m = openPathPrompt(m, "open")
	m = pressEnter(m)
	if m.activePrompt() != promptNone || m.err != nil || m.project.CurrentFile != ch1 {
		t.Fatalf("empty enter: prompt=%v err=%v", m.activePrompt(), m.err)
	}
}

func TestPathPromptOpenSwitchesProjectAndFailureIsAtomic(t *testing.T) {
	m, ch1 := promptModel(t)
	otherRoot, otherCh, _ := newTestProject(t)

	m = openPathPrompt(m, "open")
	m = typeText(m, filepath.Join(t.TempDir(), "missing"))
	m = pressEnter(m)
	if m.err == nil || m.project.CurrentFile != ch1 {
		t.Fatalf("failed open: err=%v cur=%q", m.err, m.project.CurrentFile)
	}

	m = openPathPrompt(m, "open")
	m = typeText(m, otherRoot)
	m = pressEnter(m)
	if m.err != nil || m.project.Root != otherRoot || m.project.CurrentFile != otherCh {
		t.Fatalf("open: err=%v root=%q cur=%q", m.err, m.project.Root, m.project.CurrentFile)
	}
}

func TestPathPromptNewProject(t *testing.T) {
	m, ch1 := promptModel(t)
	fresh := filepath.Join(t.TempDir(), "novel")

	m = openPathPrompt(m, "new")
	m = typeText(m, fresh)
	m = pressEnter(m)
	if m.err != nil {
		t.Fatalf("new project: %v", m.err)
	}
	if info, err := os.Stat(filepath.Join(fresh, "chapters")); err != nil || !info.IsDir() {
		t.Fatalf("chapters/ not scaffolded: %v", err)
	}

	// Scaffolding over a non-empty directory is refused and leaves it intact.
	m2, _ := promptModel(t)
	occupied := t.TempDir()
	marker := filepath.Join(occupied, "keep.txt")
	_ = os.WriteFile(marker, []byte("keep"), 0644)
	m2 = openPathPrompt(m2, "new")
	m2 = typeText(m2, occupied)
	m2 = pressEnter(m2)
	if m2.err == nil {
		t.Fatal("new project over non-empty dir accepted")
	}
	if data, _ := os.ReadFile(marker); string(data) != "keep" || fileExists(filepath.Join(occupied, "chapters")) {
		t.Fatal("occupied directory was modified")
	}
	_ = ch1
}

func TestPathPromptSaveModes(t *testing.T) {
	m, _ := promptModel(t)
	m = dirtyEditor(t, m)
	m = pressEsc(m)
	content := m.editor.Content()

	existing := filepath.Join(m.project.Root, "chapters", "002_chapter_2.md")
	before, _ := os.ReadFile(existing)

	// save onto an existing different file is refused without force.
	m = openPathPrompt(m, "save")
	m = typeText(m, existing)
	m = pressEnter(m)
	if m.err == nil {
		t.Fatal("save-as over existing file accepted without force")
	}
	if after, _ := os.ReadFile(existing); string(after) != string(before) {
		t.Fatal("existing file overwritten without force")
	}
	if m.editor.Content() != content || !m.editor.Dirty {
		t.Fatal("failed save-as altered the buffer")
	}

	// New target succeeds and rebinds the buffer.
	target := filepath.Join(m.project.Root, "chapters", "009_saved.md")
	m = openPathPrompt(m, "save")
	m = typeText(m, target)
	m = pressEnter(m)
	if m.err != nil {
		t.Fatalf("save-as: %v", m.err)
	}
	if data, _ := os.ReadFile(target); string(data) != content {
		t.Fatalf("saved content = %q", data)
	}
	if m.project.CurrentFile != target || m.editor.Dirty {
		t.Fatalf("buffer not rebound: cur=%q dirty=%v", m.project.CurrentFile, m.editor.Dirty)
	}

	// saveforce overwrites.
	m = openPathPrompt(m, "saveforce")
	m = typeText(m, existing)
	m = pressEnter(m)
	if m.err != nil {
		t.Fatalf("forced save-as: %v", m.err)
	}
	if data, _ := os.ReadFile(existing); string(data) != content {
		t.Fatalf("forced save did not overwrite: %q", data)
	}
}

func TestPathPromptSaveRejectsPathOutsideProject(t *testing.T) {
	m, _ := promptModel(t)
	m = dirtyEditor(t, m)
	m = pressEsc(m)
	outside := filepath.Join(t.TempDir(), "escape.md")
	m = openPathPrompt(m, "save")
	m = typeText(m, outside)
	m = pressEnter(m)
	if m.err == nil || fileExists(outside) {
		t.Fatalf("project save escaped the root (err=%v)", m.err)
	}
}

func TestPathPromptSaveQuitQuits(t *testing.T) {
	m, _ := promptModel(t)
	m = dirtyEditor(t, m)
	m = pressEsc(m)
	target := filepath.Join(m.project.Root, "chapters", "009_q.md")
	m = openPathPrompt(m, "savequit")
	m = typeText(m, target)
	_, cmd := updateCmd(m, tea.KeyMsg{Type: tea.KeyEnter})
	if !isQuit(cmd) {
		t.Fatal("savequit did not quit")
	}
	if !fileExists(target) {
		t.Fatal("savequit quit without saving")
	}
}
