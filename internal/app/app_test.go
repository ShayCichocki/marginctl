package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ShayCichocki/marginctl/internal/command"
	"github.com/ShayCichocki/marginctl/internal/config"
	"github.com/ShayCichocki/marginctl/internal/editor"
	"github.com/ShayCichocki/marginctl/internal/fsutil"
	"github.com/ShayCichocki/marginctl/internal/llm"
	"github.com/ShayCichocki/marginctl/internal/llmcmd"
	"github.com/ShayCichocki/marginctl/internal/projectnav"
	"github.com/ShayCichocki/marginctl/internal/results"
	"github.com/ShayCichocki/marginctl/internal/state"
)

// newTestProject writes a minimal two-chapter project to a temp dir and returns
// the project root plus the two chapter paths.
func newTestProject(t *testing.T) (root, ch1, ch2 string) {
	t.Helper()
	root = t.TempDir()
	chapters := filepath.Join(root, "chapters")
	if err := os.MkdirAll(chapters, 0755); err != nil {
		t.Fatalf("mkdir chapters: %v", err)
	}
	ch1 = filepath.Join(chapters, "001_chapter_1.md")
	ch2 = filepath.Join(chapters, "002_chapter_2.md")
	if err := os.WriteFile(ch1, []byte("# Chapter 1\n\nHello"), 0644); err != nil {
		t.Fatalf("write ch1: %v", err)
	}
	if err := os.WriteFile(ch2, []byte("# Chapter 2\n\nWorld"), 0644); err != nil {
		t.Fatalf("write ch2: %v", err)
	}
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("canonicalize project root: %v", err)
	}
	ch1 = filepath.Join(root, "chapters", filepath.Base(ch1))
	ch2 = filepath.Join(root, "chapters", filepath.Base(ch2))
	return root, ch1, ch2
}

func TestViewSanitizesNotice(t *testing.T) {
	m := New()
	m.width, m.height = 80, 24
	m.updateLayout()
	m.notice = "error\x1b]52;c;c2VjcmV0\x07\r"
	view := m.View()
	if strings.ContainsAny(view, "\x1b\x07\r") {
		t.Fatalf("view retained terminal controls: %q", view)
	}
	if m.notice != "error\x1b]52;c;c2VjcmV0\x07\r" {
		t.Fatalf("render changed stored notice: %q", m.notice)
	}
}

func openTestModel(t *testing.T) (Model, string, string) {
	t.Helper()
	root, ch1, ch2 := newTestProject(t)
	m := New()
	if err := m.OpenPath(root); err != nil {
		t.Fatalf("OpenPath: %v", err)
	}
	// OpenPath auto-selects the first chapter.
	if m.project.CurrentFile != ch1 {
		t.Fatalf("expected CurrentFile=%s after open, got %s", ch1, m.project.CurrentFile)
	}
	return m, ch1, ch2
}

func keyRunes(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

// asModel normalizes Update's return: the top-level Update returns a Model
// value, but the prompt handlers (command/search/setup/…) return *Model.
func asModel(next tea.Model) Model {
	return *next.(*Model)
}

func update(m Model, msg tea.Msg) Model {
	next, _ := m.Update(msg)
	return asModel(next)
}

// updateCmd is like update but also returns the emitted command.
func updateCmd(m Model, msg tea.Msg) (Model, tea.Cmd) {
	next, cmd := m.Update(msg)
	return asModel(next), cmd
}

// dirtyEditor drives real keystrokes to make the editor buffer dirty.
func dirtyEditor(t *testing.T, m Model) Model {
	t.Helper()
	m = update(m, keyRunes("i")) // enter insert mode
	m = update(m, keyRunes("Z")) // type a character
	if !m.editor.Dirty {
		t.Fatalf("expected editor to be dirty after editing")
	}
	return m
}

func isQuit(cmd tea.Cmd) bool {
	if cmd == nil {
		return false
	}
	_, ok := cmd().(tea.QuitMsg)
	return ok
}

// 0.1 — preview must never clobber an unsaved buffer.
func TestChapterPreviewBlockedWhenDirty(t *testing.T) {
	m, ch1, ch2 := openTestModel(t)
	m = dirtyEditor(t, m)
	dirtyContent := m.editor.Content()

	m = update(m, projectnav.ChapterPreviewMsg{Path: ch2})

	if m.editor.Content() != dirtyContent {
		t.Errorf("dirty buffer was overwritten by preview:\n got: %q", m.editor.Content())
	}
	if m.project.CurrentFile != ch1 {
		t.Errorf("CurrentFile changed during blocked preview: got %s want %s", m.project.CurrentFile, ch1)
	}
}

// Navigator preview messages are ignored: movement must never change the active
// buffer. Opening is explicit through ChapterSelectedMsg (Enter).
func TestChapterPreviewIsNonDestructive(t *testing.T) {
	m, ch1, ch2 := openTestModel(t)
	want := m.editor.Content()

	m = update(m, projectnav.ChapterPreviewMsg{Path: ch2})

	if m.editor.Content() != want {
		t.Errorf("preview changed editor:\n got: %q\nwant: %q", m.editor.Content(), want)
	}
	if m.project.CurrentFile != ch1 {
		t.Errorf("preview changed CurrentFile: got %s want %s", m.project.CurrentFile, ch1)
	}
}

// 0.1 — selecting a chapter while dirty is blocked with feedback (not silent).
func TestChapterSelectBlockedWhenDirty(t *testing.T) {
	m, ch1, ch2 := openTestModel(t)
	m = dirtyEditor(t, m)

	m = update(m, projectnav.ChapterSelectedMsg{Path: ch2})

	if m.project.CurrentFile != ch1 {
		t.Errorf("CurrentFile changed during blocked select: got %s want %s", m.project.CurrentFile, ch1)
	}
	if m.notice == "" {
		t.Errorf("expected a user-visible notice when select is blocked")
	}
}

// Vim-only keymap: Ctrl+C is a cancel (like Esc), never a quit. It must not
// discard a dirty buffer, and a second press must not quit either — quitting is
// :q / :q! only.
func TestCtrlCNeverQuitsAndActsLikeEsc(t *testing.T) {
	m, _, _ := openTestModel(t)
	m = dirtyEditor(t, m) // leaves the editor dirty and in insert mode

	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC})
	m = *next.(*Model)
	if isQuit(cmd) {
		t.Fatalf("Ctrl+C must never quit")
	}
	if m.editor.Mode != editor.ModeNormal {
		t.Errorf("Ctrl+C should return to Normal mode like Esc; got mode %v", m.editor.Mode)
	}
	if !m.editor.Dirty {
		t.Errorf("Ctrl+C must not discard unsaved changes")
	}

	if _, cmd := m.Update(tea.KeyMsg{Type: tea.KeyCtrlC}); isQuit(cmd) {
		t.Fatalf("a repeated Ctrl+C must not quit")
	}
}

// 0.3 — :q on a dirty buffer is blocked with a message instead of a silent no-op.
func TestQuitCommandBlockedWhenDirty(t *testing.T) {
	m, _, _ := openTestModel(t)
	m = dirtyEditor(t, m)
	// Exit insert mode so we can enter command mode.
	m = update(m, tea.KeyMsg{Type: tea.KeyEscape})

	cmd := m.executeCommand("q")
	if isQuit(cmd) {
		t.Fatalf(":q on a dirty buffer must not quit")
	}
	if m.notice == "" {
		t.Errorf("expected a notice explaining how to save/discard")
	}

	if cmd := m.executeCommand("q!"); !isQuit(cmd) {
		t.Errorf(":q! must force quit")
	}
}

// A clean :q, typed through the : prompt, quits (drives the full
// handleCommandInput path so the returned tea.Quit is what Update emits).
func TestCleanQuitViaColonPrompt(t *testing.T) {
	m, _, _ := openTestModel(t)
	if m.editor.Dirty {
		t.Fatalf("freshly opened buffer should be clean")
	}
	m = update(m, keyRunes(":"))
	m = update(m, keyRunes("q"))
	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if !isQuit(cmd) {
		t.Fatalf("clean :q must quit")
	}
}

// 0.4 — accepting a diff on a partial-line selection replaces exactly the
// selected span and leaves the rest of the line untouched.
func TestApplyDiffIsCharacterPrecise(t *testing.T) {
	m, _, _ := openTestModel(t)
	m.editor.SetContent("The quick brown fox")
	m.setPendingAction(pendingLLMResult, llmcmd.Operation{}, command.Result{Type: command.ResultDiff})

	// Select "quick" (cols 4..8 on line 0) and accept a replacement. The diff
	// carries the identity of the buffer it was computed against.
	m = update(m, results.DiffAcceptedMsg{
		ActionID:     m.pendingAction.ID,
		OriginalText: "quick",
		ProposedText: "slow",
		StartLine:    0,
		EndLine:      0,
		StartCol:     4,
		EndCol:       8,
		File:         m.project.CurrentFile,
		BufferGen:    m.bufferGen,
	})

	if got, want := m.editor.Content(), "The slow brown fox"; got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
}

// 0.5 — an applied LLM edit must stay dirty until the user saves it.
func TestApplyDiffStaysDirty(t *testing.T) {
	m, _, _ := openTestModel(t)
	m.editor.SetContent("The quick brown fox")
	m.setPendingAction(pendingLLMResult, llmcmd.Operation{}, command.Result{Type: command.ResultDiff})
	if m.editor.Dirty {
		t.Fatal("buffer should be clean before applying the diff")
	}

	m = update(m, results.DiffAcceptedMsg{
		ActionID:     m.pendingAction.ID,
		OriginalText: "quick",
		ProposedText: "slow",
		StartLine:    0,
		EndLine:      0,
		StartCol:     4,
		EndCol:       8,
		File:         m.project.CurrentFile,
		BufferGen:    m.bufferGen,
	})

	if !m.editor.Dirty {
		t.Fatal("buffer must be dirty after accepting a diff (unsaved change)")
	}
}

// The voice guide's *content* (not just its path) must reach the command
// context, so every selection-edit prompt actually folds it in.
func TestCommandContextLoadsVoiceGuide(t *testing.T) {
	m, _, _ := openTestModel(t)
	styleDir := filepath.Join(m.project.Root, "state", "style")
	if err := os.MkdirAll(styleDir, 0755); err != nil {
		t.Fatalf("mkdir style: %v", err)
	}
	guide := "# Voice Guide\nTerse and wry.\n"
	if err := os.WriteFile(filepath.Join(styleDir, "voice.md"), []byte(guide), 0644); err != nil {
		t.Fatalf("write voice guide: %v", err)
	}
	m.state = state.New(m.project.Root)

	ctx := m.commandContext()
	if ctx.VoiceGuidePath == "" {
		t.Fatal("voice guide path not discovered")
	}
	if ctx.VoiceGuide != guide {
		t.Fatalf("ctx.VoiceGuide = %q, want file content", ctx.VoiceGuide)
	}
	// End to end: the guide must land in a selection-edit system prompt.
	system, _ := llm.ExpandProsePrompt("prose", ctx.VoiceGuide)
	if !strings.Contains(system, "Terse and wry.") {
		t.Fatalf("expand prompt missing voice guide:\n%s", system)
	}
}

// Without a state/ directory the context carries no voice guide and no notice.
func TestCommandContextWithoutVoiceGuide(t *testing.T) {
	m, _, _ := openTestModel(t)
	ctx := m.commandContext()
	if ctx.VoiceGuide != "" || ctx.VoiceGuidePath != "" {
		t.Fatalf("unexpected voice guide: path=%q content=%q", ctx.VoiceGuidePath, ctx.VoiceGuide)
	}
}

// 0.8 — ":w <path>" writes an unnamed buffer to disk and adopts that path as
// the current file, so a later ":w" targets the same file.
func TestWriteAsPathUnnamedBuffer(t *testing.T) {
	dir := t.TempDir()
	m := dirtyEditor(t, New())
	content := m.editor.Content()
	path := filepath.Join(dir, "note.md")
	wantPath, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("canonicalize save directory: %v", err)
	}
	wantPath = filepath.Join(wantPath, "note.md")

	cmd := m.executeCommand("w " + path)
	if isQuit(cmd) {
		t.Fatal(":w must not quit")
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("file not written: %v", err)
	}
	if string(got) != content {
		t.Fatalf("saved content = %q, want %q", got, content)
	}
	if m.project.CurrentFile != wantPath {
		t.Fatalf("CurrentFile = %q, want %q", m.project.CurrentFile, wantPath)
	}
	if m.editor.Dirty {
		t.Fatal("buffer should be clean after a successful save")
	}
}

// 0.8 — ":w" on an unnamed buffer opens the save-as prompt instead of silently
// doing nothing.
func TestWriteUnnamedBufferOpensPrompt(t *testing.T) {
	m := dirtyEditor(t, New())

	cmd := m.executeCommand("w")
	if isQuit(cmd) {
		t.Fatal(":w must not quit")
	}
	if m.activePrompt() != promptPath || m.pathInputMode != "save" {
		t.Fatalf("expected save-as prompt, got prompt=%v mode=%q", m.activePrompt(), m.pathInputMode)
	}
}

func TestForcedWriteUnnamedBufferPromptOverwritesExistingTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "existing.md")
	if err := os.WriteFile(target, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	m := dirtyEditor(t, New())
	want := m.editor.Content()
	m.executeCommand("w!")
	if m.activePrompt() != promptPath || m.pathInputMode != "saveforce" {
		t.Fatalf("forced prompt mode = %q", m.pathInputMode)
	}
	m.pathBuffer = target
	if _, cmd := m.handlePathInput(tea.KeyMsg{Type: tea.KeyEnter}); isQuit(cmd) {
		t.Fatal(":w! must not quit")
	}
	if got, _ := os.ReadFile(target); string(got) != want {
		t.Fatalf("forced prompted save wrote %q, want %q", got, want)
	}
}

// 0.14 — ":wq" on an unnamed buffer prompts for a path and only quits after the
// write succeeds; it can never exit with unsaved content.
func TestWriteQuitUnnamedBufferPromptsThenSaves(t *testing.T) {
	dir := t.TempDir()
	m := dirtyEditor(t, New())
	content := m.editor.Content()

	cmd := m.executeCommand("wq")
	if isQuit(cmd) {
		t.Fatal(":wq on an unnamed buffer must not quit before a path is given")
	}
	if m.activePrompt() != promptPath || m.pathInputMode != "savequit" {
		t.Fatalf("expected savequit prompt, got prompt=%v mode=%q", m.activePrompt(), m.pathInputMode)
	}

	path := filepath.Join(dir, "out.md")
	wantPath, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("canonicalize save directory: %v", err)
	}
	wantPath = filepath.Join(wantPath, "out.md")
	m.pathBuffer = path
	_, cmd = m.handlePathInput(tea.KeyMsg{Type: tea.KeyEnter})
	if !isQuit(cmd) {
		t.Fatal("expected quit after a successful save-as via :wq")
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("file not written: %v", err)
	}
	if string(got) != content {
		t.Fatalf("saved content = %q, want %q", got, content)
	}
	if m.project.CurrentFile != wantPath {
		t.Fatalf("CurrentFile = %q, want %q", m.project.CurrentFile, wantPath)
	}
}

// 0.12 — "New Project" at an existing, non-empty path must change nothing and
// surface an error, never overwriting an existing chapter/state file.
func TestCreateNewProjectRefusesExistingContent(t *testing.T) {
	m := New()
	root, ch1, _ := newTestProject(t)
	before, _ := os.ReadFile(ch1)

	if err := m.createNewProject(root); err == nil {
		t.Fatal("expected an error scaffolding over an existing non-empty dir")
	}

	after, _ := os.ReadFile(ch1)
	if string(after) != string(before) {
		t.Fatalf("existing chapter was modified: got %q want %q", after, before)
	}
}

// 0.12 — a fresh path still scaffolds a working project.
func TestCreateNewProjectFreshPath(t *testing.T) {
	m := New()
	root := filepath.Join(t.TempDir(), "novel")

	if err := m.createNewProject(root); err != nil {
		t.Fatalf("createNewProject on a fresh path: %v", err)
	}
	if _, err := os.Stat(filepath.Join(root, "chapters", "001_chapter_1.md")); err != nil {
		t.Fatalf("expected scaffolded chapter: %v", err)
	}
}

// 0.13 — a diff computed against one chapter must not splice into another after
// the user switches files.
func TestStaleDiffAfterFileSwitchDiscarded(t *testing.T) {
	m, _, ch2 := openTestModel(t)

	// Capture the identity of ch1's buffer, then switch to ch2.
	staleDiff := results.DiffAcceptedMsg{
		ActionID:     1,
		OriginalText: "Hello",
		ProposedText: "GOODBYE",
		StartLine:    2,
		EndLine:      2,
		StartCol:     0,
		EndCol:       4,
		File:         m.project.CurrentFile,
		BufferGen:    m.bufferGen,
	}
	m.nextActionID = 1
	m.pendingAction = &PendingAction{ID: 1, Kind: pendingLLMResult}
	var cmd tea.Cmd
	m, cmd = updateCmd(m, projectnav.ChapterSelectedMsg{Path: ch2})
	m = update(m, cmd())
	ch2Content := m.editor.Content()

	// Accepting the stale diff must be refused and leave ch2 untouched.
	m = update(m, staleDiff)
	if m.editor.Content() != ch2Content {
		t.Fatalf("stale diff spliced into ch2: %q", m.editor.Content())
	}
	if m.pendingAction != nil || m.results.HasPendingProposal() {
		t.Fatal("edited diff must no longer be actionable")
	}
}

// 0.13 — a diff whose buffer was edited (same file) mid-review is discarded.
func TestStaleDiffAfterEditDiscarded(t *testing.T) {
	m, _, _ := openTestModel(t)
	staleDiff := results.DiffAcceptedMsg{
		ActionID:     1,
		OriginalText: "Hello",
		ProposedText: "GOODBYE",
		StartLine:    2,
		EndLine:      2,
		StartCol:     0,
		EndCol:       4,
		File:         m.project.CurrentFile,
		BufferGen:    m.bufferGen,
	}
	m.nextActionID = 1
	m.pendingAction = &PendingAction{ID: 1, Kind: pendingLLMResult}

	// Edit the buffer (bumps bufferGen) before accepting.
	m = dirtyEditor(t, m)
	edited := m.editor.Content()

	m = update(m, staleDiff)
	if m.editor.Content() != edited {
		t.Fatalf("stale diff applied over an edited buffer: %q", m.editor.Content())
	}
	if m.pendingAction != nil || m.results.HasPendingProposal() {
		t.Fatal("edited diff must no longer be actionable")
	}
}

// 0.13 — a stale diff ResponseMsg (buffer changed while the request was in
// flight) is dropped instead of being shown.
func TestStaleDiffResponseDiscarded(t *testing.T) {
	m, _, ch2 := openTestModel(t)
	resp := llmcmd.ResponseMsg{
		CommandID: "llm.tighten",
		File:      m.project.CurrentFile,
		BufferGen: m.bufferGen,
		Result: command.Result{
			Type:         command.ResultDiff,
			OriginalText: "Hello",
			ProposedText: "Hi",
			StartLine:    2,
			EndLine:      2,
			StartCol:     0,
			EndCol:       4,
			File:         m.project.CurrentFile,
			BufferGen:    m.bufferGen,
		},
	}

	m = update(m, projectnav.ChapterSelectedMsg{Path: ch2})
	m = update(m, resp)

	if m.results.Visible() {
		t.Fatal("stale diff response should not be shown in the results pane")
	}
}

// 0.15 — accepting a voice-guide proposal writes the file; the goroutine never
// touches the model (write happens here, on the live model).
func TestVoiceGuideAcceptWritesFile(t *testing.T) {
	m, _, _ := openTestModel(t)
	// Ensure a state/ dir exists.
	if err := m.state.Initialize(); err != nil {
		t.Fatalf("init state: %v", err)
	}

	target := filepath.Join(m.project.Root, "state", "style", "voice.md")
	fp, _ := fsutil.FileFingerprint(target)
	op := llmcmd.Operation{RequestID: 1, Kind: "llm.voice-guide", ProjectRoot: m.project.Root, SourceFile: m.project.CurrentFile, BufferRevision: m.bufferGen, Target: target, TargetFingerprint: fp}
	m.llmReqID = 1
	m.setPendingAction(pendingLLMResult, op, command.Result{Type: command.ResultVoiceGuide})
	m = update(m, results.VoiceGuideAcceptedMsg{ActionID: m.pendingAction.ID, Operation: op, Content: "# Voice\n\nTerse.\n"})

	got, err := os.ReadFile(filepath.Join(m.project.Root, "state", "style", "voice.md"))
	if err != nil {
		t.Fatalf("voice guide not written: %v", err)
	}
	if string(got) != "# Voice\n\nTerse.\n" {
		t.Fatalf("voice guide content = %q", got)
	}
}

// 0.15 — a voice-guide proposal is presented for review (not auto-saved); a
// rejection must not write anything.
func TestVoiceGuideResponseShownForReview(t *testing.T) {
	m, _, _ := openTestModel(t)
	m.llmReqID = 1
	m = update(m, llmcmd.ResponseMsg{
		CommandID: "llm.voice-guide",
		ReqID:     1,
		Operation: llmcmd.Operation{RequestID: 1, Kind: "llm.voice-guide", ProjectRoot: m.project.Root, SourceFile: m.project.CurrentFile, BufferRevision: m.bufferGen},
		Result: command.Result{
			Type:         command.ResultVoiceGuide,
			ProposedText: "# Voice\n\nProposed.\n",
		},
	})
	if !m.results.Visible() || !m.results.HasPendingProposal() {
		t.Fatal("voice guide should be shown as a pending proposal for review")
	}
}

// 3.1 — Esc while an LLM request is in flight aborts it: the cancel func fires,
// loading clears, and the dispatch counter advances so the pending response is
// treated as stale.
func TestLLMCancelOnEsc(t *testing.T) {
	m, _, _ := openTestModel(t)

	cancelled := false
	m.llmLoading = true
	m.llmStatus = "Analyzing text... (esc to cancel)"
	m.llmReqID = 1
	m.llmCancel = func() { cancelled = true }

	m = update(m, tea.KeyMsg{Type: tea.KeyEsc})

	if !cancelled {
		t.Fatal("Esc should have invoked the cancel func")
	}
	if m.llmLoading {
		t.Fatal("loading flag should be cleared after cancel")
	}
	if m.llmReqID != 2 {
		t.Fatalf("llmReqID = %d, want 2 (bumped to invalidate the in-flight response)", m.llmReqID)
	}
	if m.notice == "" {
		t.Fatal("expected a cancellation notice")
	}
}

// 3.1 — a response from a cancelled/superseded request (stale ReqID) is dropped
// rather than shown, even for non-diff results.
func TestLLMStaleResponseByReqIDDropped(t *testing.T) {
	m, _, _ := openTestModel(t)
	m.llmReqID = 2 // current dispatch is #2 (e.g. after a cancel bumped it)

	m = update(m, llmcmd.ResponseMsg{
		CommandID: "llm.continuity",
		ReqID:     1, // from the cancelled request #1
		Result: command.Result{
			Type:    command.ResultText,
			Content: "continuity report",
		},
	})

	if m.results.Visible() {
		t.Fatal("a response with a stale ReqID must not be shown")
	}
}

func TestLLMReconnectErrorShownInResultsPane(t *testing.T) {
	// Given
	m, _, _ := openTestModel(t)
	m.width = 1000
	m.height = 30
	m.updateLayout()
	m.llmReqID = 1
	m.llmLoading = true

	// When
	m = update(m, llmcmd.ResponseMsg{
		CommandID: "llm.voice-guide",
		ReqID:     1,
		Operation: llmcmd.Operation{RequestID: 1, Kind: "llm.voice-guide", ProjectRoot: m.project.Root, SourceFile: m.project.CurrentFile, BufferRevision: m.bufferGen},
		Result: command.Result{
			Type:    command.ResultError,
			Content: "chatgpt auth unavailable; run :connect openai",
		},
	})

	// Then
	if !m.results.Visible() || !strings.Contains(m.results.View(), "run :connect openai") {
		t.Fatalf("results visible=%v view=%q, want reconnect guidance", m.results.Visible(), m.results.View())
	}
}

func TestLLMContractDriftErrorShownInResultsPane(t *testing.T) {
	// Given
	m, _, _ := openTestModel(t)
	m.width = 1000
	m.height = 30
	m.updateLayout()
	m.llmReqID = 1
	m.llmLoading = true

	// When
	m = update(m, llmcmd.ResponseMsg{
		CommandID: "llm.voice-guide",
		ReqID:     1,
		Operation: llmcmd.Operation{RequestID: 1, Kind: "llm.voice-guide", ProjectRoot: m.project.Root, SourceFile: m.project.CurrentFile, BufferRevision: m.bufferGen},
		Result: command.Result{
			Type:    command.ResultError,
			Content: "ChatGPT backend contract changed; update marginctl",
		},
	})

	// Then
	if !m.results.Visible() || !strings.Contains(m.results.View(), "ChatGPT backend contract changed") {
		t.Fatalf("results visible=%v view=%q, want contract drift error", m.results.Visible(), m.results.View())
	}
}

// 3.2 — the loading spinner advances on each tick while a request is in flight,
// composes an elapsed-time progress line, and stops ticking once the request
// finishes or is superseded (so no stray tick chain lingers).
func TestLLMSpinnerTick(t *testing.T) {
	m, _, _ := openTestModel(t)
	m.llmLoading = true
	m.llmReqID = 1
	m.llmStatus = "Checking continuity"
	m.llmStart = time.Now().Add(-3 * time.Second)

	before := m.llmSpinner
	m, cmd := updateCmd(m, llmTickMsg{reqID: 1})
	if m.llmSpinner != before+1 {
		t.Fatalf("spinner = %d, want %d", m.llmSpinner, before+1)
	}
	if cmd == nil {
		t.Fatal("a live tick should re-arm the next tick")
	}

	line := m.llmStatusLine()
	if !strings.Contains(line, "Checking continuity") || !strings.Contains(line, "esc to cancel") {
		t.Fatalf("status line missing label/hint: %q", line)
	}
	if !strings.Contains(line, "3s") {
		t.Fatalf("status line missing elapsed seconds: %q", line)
	}

	// A tick from a superseded request (stale reqID) must not re-arm.
	m.llmReqID = 2
	if _, cmd := updateCmd(m, llmTickMsg{reqID: 1}); cmd != nil {
		t.Fatal("a stale-reqID tick must not re-arm")
	}

	// A tick after the request finished must not re-arm.
	m.llmLoading = false
	if _, cmd := updateCmd(m, llmTickMsg{reqID: 2}); cmd != nil {
		t.Fatal("a tick after loading ended must not re-arm")
	}
}

// 3.2 — a streaming text result renders incrementally: the seed prefix shows
// first, deltas accumulate into the pane, and the terminal event surfaces a usage
// notice. A stale-reqID delta is ignored.
func TestStreamingRendersIncrementally(t *testing.T) {
	m, _, _ := openTestModel(t)
	m.llmLoading = true
	m.llmReqID = 1
	m.llmStatus = "Checking continuity"
	m.llmStart = time.Now()
	op := llmcmd.Operation{RequestID: 1, ProjectRoot: m.project.Root, SourceFile: m.project.CurrentFile, BufferRevision: m.bufferGen}

	ch := make(chan llm.StreamEvent) // never actually read (cmds discarded in tests)
	m = update(m, llmcmd.StreamStartMsg{ReqID: 1, Operation: op, Ch: ch, Prefix: "⚠ note\n\n"})
	if !m.results.Visible() || m.streamBuf != "⚠ note\n\n" {
		t.Fatalf("prefix not seeded: buf=%q visible=%v", m.streamBuf, m.results.Visible())
	}

	m = update(m, streamDeltaMsg{operation: op, text: "No issues"})
	m = update(m, streamDeltaMsg{operation: op, text: " found."})
	if m.streamBuf != "⚠ note\n\nNo issues found." {
		t.Fatalf("streamBuf = %q", m.streamBuf)
	}

	// A delta from a superseded request must not mutate the buffer.
	staleOp := op
	staleOp.RequestID = 99
	m = update(m, streamDeltaMsg{operation: staleOp, text: "GARBAGE"})
	if strings.Contains(m.streamBuf, "GARBAGE") {
		t.Fatal("stale-reqID delta leaked into the buffer")
	}

	m = update(m, streamEndMsg{operation: op, usage: llm.Usage{InputTokens: 1500, OutputTokens: 340}})
	if m.llmLoading {
		t.Fatal("loading should clear on stream end")
	}
	if !strings.HasPrefix(m.notice, "✓") || !strings.Contains(m.notice, "1.5k in") {
		t.Fatalf("expected a usage notice, got %q", m.notice)
	}
}

// 3.2 — end-to-end: the real ExecuteSummarize → CompleteStream → waitForStream →
// app-handler loop assembles streamed text against a live SSE server. Drives the
// actual tea.Cmd chain (not synthetic messages) so the wiring between the client
// channel and the app is exercised for real.
func TestStreamingEndToEndThroughApp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, f := range []string{
			`{"type":"message_start","message":{"usage":{"input_tokens":40}}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"- beat one\n"}}`,
			`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"- beat two"}}`,
			`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":9}}`,
			`{"type":"message_stop"}`,
		} {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", f)
		}
	}))
	defer srv.Close()

	m, _, _ := openTestModel(t)
	m.llmClient = llm.NewClient(config.LLMConfig{Provider: config.ProviderAnthropic, APIKey: "k", Endpoint: srv.URL})
	m.llmLoading = true
	m.llmReqID = 1

	// Kick off the real streaming runner and stamp the reqID like startLLM does.
	start := llmcmd.ExecuteSummarize(m.llmClient, &command.Context{EditorContent: "Once upon a time."}, context.Background())().(llmcmd.StreamStartMsg)
	start.ReqID = 1
	start.Operation = llmcmd.Operation{RequestID: 1, ProjectRoot: m.project.Root, SourceFile: m.project.CurrentFile, BufferRevision: m.bufferGen}

	var cmd tea.Cmd
	m, cmd = updateCmd(m, start)
	// Run the subscription chain to completion (each delta re-arms; end returns nil).
	for i := 0; cmd != nil && i < 100; i++ {
		m, cmd = updateCmd(m, cmd())
	}

	if !strings.Contains(m.streamBuf, "beat one") || !strings.Contains(m.streamBuf, "beat two") {
		t.Fatalf("assembled stream = %q", m.streamBuf)
	}
	if m.llmLoading {
		t.Fatal("loading should have cleared at stream end")
	}
	if !strings.HasPrefix(m.notice, "✓") {
		t.Fatalf("expected a completion notice, got %q", m.notice)
	}
}

// 3.2 — a streaming error (non-cancel) replaces the pane with an error result.
func TestStreamingErrorShown(t *testing.T) {
	m, _, _ := openTestModel(t)
	m.llmLoading = true
	m.llmReqID = 1
	op := llmcmd.Operation{RequestID: 1, ProjectRoot: m.project.Root, SourceFile: m.project.CurrentFile, BufferRevision: m.bufferGen}
	ch := make(chan llm.StreamEvent)
	m = update(m, llmcmd.StreamStartMsg{ReqID: 1, Operation: op, Ch: ch})
	m = update(m, streamEndMsg{operation: op, err: llm.ErrTruncated})
	if m.llmLoading {
		t.Fatal("loading should clear on error")
	}
	if !m.results.Visible() {
		t.Fatal("an error result should be shown")
	}
}

// 4.1 — :search spans all chapters and shows hits in the results pane; :compile
// writes a single combined document; both are reachable as registry commands too.
func TestManuscriptSearchAndCompile(t *testing.T) {
	m, ch1, _ := openTestModel(t)
	root := filepath.Dir(filepath.Dir(ch1)) // ch1 is <root>/chapters/<file>
	// openTestModel's two chapters — write known content into ch1.
	if err := os.WriteFile(ch1, []byte("The dragon slept.\nquiet.\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_ = m.project.Refresh()

	// :search with no results.
	m = update(m, m.searchManuscript("unicorn")())
	if !strings.Contains(m.notice, "no matches") {
		t.Fatalf("expected a no-match notice, got %q", m.notice)
	}

	// :search with a hit opens the results pane.
	m = update(m, m.searchManuscript("DRAGON")())
	if !m.results.Visible() {
		t.Fatal("a hit should open the results pane")
	}

	// The manuscript commands are registered for the palette.
	for _, id := range []string{"manuscript.wordcount", "manuscript.compile"} {
		if m.registry.Get(id) == nil {
			t.Fatalf("%s not registered", id)
		}
	}

	// :compile writes a single combined document under the project root.
	m = update(m, m.executeRegisteredCommandCmd("manuscript.compile")())
	if _, err := os.Stat(filepath.Join(root, "manuscript.md")); err != nil {
		t.Fatalf("compile did not write manuscript.md: %v", err)
	}
}

// 4.1 — Enter on a search result jumps to that file+line (loading the file if
// it differs from the current one), unless the buffer is dirty.
func TestSearchJumpToMatch(t *testing.T) {
	m, ch1, ch2 := openTestModel(t)
	if err := os.WriteFile(ch2, []byte("one\ntwo\nthe griffin roared\nfour\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_ = m.project.Refresh()

	m = update(m, m.searchManuscript("griffin")()) // hit is in ch2, line 3
	if !m.results.Visible() {
		t.Fatal("search should open the results pane")
	}

	// The currently open file is ch1; jumping to ch2 loads it and positions the cursor.
	next, _ := m.Update(results.OpenMatchMsg{Path: ch2, Line: 3})
	m = asModel(next)
	if m.project.CurrentFile != ch2 {
		t.Fatalf("jump did not load ch2; CurrentFile=%s", m.project.CurrentFile)
	}
	if row, _ := m.editor.CursorPosition(); row != 2 { // Line 3 → row index 2
		t.Fatalf("cursor row = %d, want 2 (line 3)", row)
	}
	if m.focus != FocusEditor {
		t.Fatalf("focus should return to the editor after a jump")
	}

	// A dirty buffer blocks a cross-file jump (no clobber).
	m = dirtyEditor(t, m)
	m = update(m, tea.KeyMsg{Type: tea.KeyEscape}) // leave insert
	next, _ = m.Update(results.OpenMatchMsg{Path: ch1, Line: 1})
	m = asModel(next)
	if m.project.CurrentFile != ch2 {
		t.Fatalf("dirty buffer must block a cross-file jump; CurrentFile=%s", m.project.CurrentFile)
	}
	if m.notice == "" {
		t.Fatal("expected a notice when a dirty jump is blocked")
	}
}

// 3.4 — :model reports and switches the session model; unknown commands give
// feedback instead of a silent no-op.
func TestModelCommandAndUnknown(t *testing.T) {
	m, _, _ := openTestModel(t)

	m.executeCommand("model")
	if !strings.Contains(m.notice, "model:") {
		t.Fatalf("`:model` should report current model, got %q", m.notice)
	}

	m.executeCommand("model claude-opus-4-8")
	if m.llmClient.Model() != "claude-opus-4-8" {
		t.Fatalf("model = %q, want claude-opus-4-8", m.llmClient.Model())
	}
	if !strings.Contains(m.notice, "claude-opus-4-8") {
		t.Fatalf("switch notice = %q", m.notice)
	}

	m.executeCommand("bogusverb")
	if !strings.Contains(m.notice, "unknown command") {
		t.Fatalf("unknown-command feedback missing, got %q", m.notice)
	}
}

// 3.4 — usage formatting: compact token counts + a cost estimate for known models,
// and empty for no usage.
func TestFormatUsage(t *testing.T) {
	if got := formatUsage(llm.Usage{}, "claude-sonnet-5"); got != "" {
		t.Fatalf("zero usage = %q, want empty", got)
	}
	got := formatUsage(llm.Usage{InputTokens: 1500, OutputTokens: 340}, "claude-sonnet-5")
	if !strings.Contains(got, "1.5k in") || !strings.Contains(got, "340 out") || !strings.Contains(got, "$") {
		t.Fatalf("usage line = %q", got)
	}
	// Unknown model → tokens, no cost.
	unknown := formatUsage(llm.Usage{InputTokens: 100, OutputTokens: 50}, "some-unknown-model")
	if strings.Contains(unknown, "$") {
		t.Fatalf("unknown model should not show cost: %q", unknown)
	}
}

// 2.2 — a plain :w must refuse to overwrite the current file when it changed on
// disk since load, and :w! forces the write.
func TestExternalChangeBlocksPlainWrite(t *testing.T) {
	m, ch1, _ := openTestModel(t)
	m = dirtyEditor(t, m)
	m = update(m, tea.KeyMsg{Type: tea.KeyEscape}) // leave insert mode
	bufContent := m.editor.Content()

	// An external writer changes ch1 on disk after we loaded it; force a
	// distinctly different mtime so the guard fires regardless of clock
	// granularity.
	if err := os.WriteFile(ch1, []byte("EXTERNAL EDIT"), 0644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(ch1, future, future); err != nil {
		t.Fatal(err)
	}

	if cmd := m.executeCommand("w"); isQuit(cmd) {
		t.Fatal(":w must not quit")
	}
	if m.err == nil {
		t.Fatal("expected an external-change error on plain :w")
	}
	if got, _ := os.ReadFile(ch1); string(got) != "EXTERNAL EDIT" {
		t.Fatalf("plain :w clobbered the external change: %q", got)
	}

	// :w! forces the overwrite with the buffer contents.
	m.executeCommand("w!")
	if got, _ := os.ReadFile(ch1); string(got) != bufContent {
		t.Fatalf(":w! should have written the buffer, got %q want %q", got, bufContent)
	}
	if m.editor.Dirty {
		t.Fatal("buffer should be clean after a forced save")
	}
}

func TestExternalChangeWithPreservedMtimeAndDeletionBlocksPlainWrite(t *testing.T) {
	for _, change := range []struct {
		name string
		do   func(t *testing.T, path string, modTime time.Time)
	}{
		{"preserved mtime", func(t *testing.T, path string, modTime time.Time) {
			if err := os.WriteFile(path, []byte("EXTERNAL EDIT"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(path, modTime, modTime); err != nil {
				t.Fatal(err)
			}
		}},
		{"deletion", func(t *testing.T, path string, _ time.Time) {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(change.name, func(t *testing.T) {
			m, ch1, _ := openTestModel(t)
			m = dirtyEditor(t, m)
			change.do(t, ch1, m.loadedFingerprint.ModTime)
			m.executeCommand("w")
			if !errors.Is(m.err, errExternalChange) {
				t.Fatalf("save error = %v, want external-change conflict", m.err)
			}
		})
	}
}

func TestSaveAsProtectsExistingTargetAndForceOverwrites(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.md")
	target := filepath.Join(dir, "target.md")
	if err := os.WriteFile(source, []byte("source"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target, []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}
	m := New()
	if err := m.OpenPath(source); err != nil {
		t.Fatal(err)
	}
	m = dirtyEditor(t, m)
	want := m.editor.Content()
	m.executeCommand("w " + target)
	if !errors.Is(m.err, errSaveAsExists) {
		t.Fatalf("save-as error = %v, want existing-target conflict", m.err)
	}
	if got, _ := os.ReadFile(target); string(got) != "keep" {
		t.Fatalf("plain save-as overwrote target: %q", got)
	}
	m.err = nil
	m.executeCommand("w! " + target)
	if m.err != nil {
		t.Fatalf("forced save-as: %v", m.err)
	}
	if got, _ := os.ReadFile(target); string(got) != want {
		t.Fatalf("forced save-as wrote %q, want %q", got, want)
	}
}

func TestSaveAliasesAreCurrentFile(t *testing.T) {
	for _, kind := range []string{"relative", "symlink", "hardlink"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			source := filepath.Join(dir, "source.md")
			if err := os.WriteFile(source, []byte("source"), 0644); err != nil {
				t.Fatal(err)
			}
			m := New()
			if err := m.OpenPath(source); err != nil {
				t.Fatal(err)
			}
			m = dirtyEditor(t, m)
			want := m.editor.Content()
			alias := filepath.Join(dir, ".", "source.md")
			switch kind {
			case "symlink":
				alias = filepath.Join(dir, "alias.md")
				if err := os.Symlink(source, alias); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				alias = filepath.Join(dir, "alias.md")
				if err := os.Link(source, alias); err != nil {
					t.Fatal(err)
				}
			}
			m.executeCommand("w " + alias)
			if m.err != nil {
				t.Fatalf("save through %s alias: %v", kind, m.err)
			}
			if got, _ := os.ReadFile(source); string(got) != want {
				t.Fatalf("source after %s save = %q, want %q", kind, got, want)
			}
		})
	}
}

func TestSaveAsNewTargetSucceeds(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.md")
	target := filepath.Join(dir, "new.md")
	if err := os.WriteFile(source, []byte("source"), 0644); err != nil {
		t.Fatal(err)
	}
	m := New()
	if err := m.OpenPath(source); err != nil {
		t.Fatal(err)
	}
	m = dirtyEditor(t, m)
	m.executeCommand("w " + target)
	if m.err != nil {
		t.Fatalf("new save-as: %v", m.err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("new target missing: %v", err)
	}
}

// 2.2 — a normal :w with no external change still saves.
func TestSaveWithoutExternalChangeSucceeds(t *testing.T) {
	m, ch1, _ := openTestModel(t)
	m = dirtyEditor(t, m)
	m = update(m, tea.KeyMsg{Type: tea.KeyEscape})
	want := m.editor.Content()

	if cmd := m.executeCommand("w"); isQuit(cmd) {
		t.Fatal(":w must not quit")
	}
	if m.err != nil {
		t.Fatalf("unexpected error on clean save: %v", m.err)
	}
	if got, _ := os.ReadFile(ch1); string(got) != want {
		t.Fatalf("save wrote %q, want %q", got, want)
	}
}

// 2.5 — a chapter added on disk after open appears when the project nav is
// opened (:ShowChapters / chapter.list re-reads the listing).
func TestProjectNavRefreshesOnFocus(t *testing.T) {
	m, _, _ := openTestModel(t)
	before := len(m.project.ListChapters())

	// A chapter created outside the app.
	newCh := filepath.Join(m.project.Root, "chapters", "003_added.md")
	if err := os.WriteFile(newCh, []byte("# Added\n"), 0644); err != nil {
		t.Fatal(err)
	}

	next, _ := m.executeRegisteredCommand("chapter.list")
	m = asModel(next)

	if got := len(m.project.ListChapters()); got != before+1 {
		t.Fatalf("chapter count = %d, want %d after external add + open", got, before+1)
	}
}

// 2.1 — a dirty buffer flushes to a swap file on the autosave tick, and a clean
// save removes it.
func TestAutosaveSwapLifecycle(t *testing.T) {
	m, ch1, _ := openTestModel(t)
	m = dirtyEditor(t, m)

	// Autosave tick writes the swap.
	m = update(m, autosaveTickMsg{})
	sp := swapPath(ch1)
	got, err := os.ReadFile(sp)
	if err != nil {
		t.Fatalf("expected swap file after autosave tick: %v", err)
	}
	if string(got) != m.editor.Content() {
		t.Fatalf("swap content = %q, want %q", got, m.editor.Content())
	}

	// Saving clears the swap.
	m = update(m, tea.KeyMsg{Type: tea.KeyEscape})
	m.executeCommand("w")
	if _, err := os.Stat(sp); !os.IsNotExist(err) {
		t.Fatalf("swap should be removed after save, stat err = %v", err)
	}
}

// 2.1 — a clean buffer never writes a swap file.
func TestAutosaveNoSwapWhenClean(t *testing.T) {
	m, ch1, _ := openTestModel(t)
	update(m, autosaveTickMsg{})
	if _, err := os.Stat(swapPath(ch1)); !os.IsNotExist(err) {
		t.Fatalf("clean buffer must not write a swap, stat err = %v", err)
	}
}

// 2.1 — opening a project whose chapter has a newer swap offers recovery, and
// :recover restores the unsaved content as a dirty buffer.
func TestOpenOffersRecoveryAndRestores(t *testing.T) {
	root, ch1, _ := newTestProject(t)

	// Simulate a crashed session: a swap file newer than the chapter.
	recovered := "# Chapter 1\n\nHello, recovered draft"
	sp := swapPath(ch1)
	if err := os.WriteFile(sp, []byte(recovered), 0644); err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(sp, future, future); err != nil {
		t.Fatal(err)
	}

	m := New()
	if err := m.OpenPath(root); err != nil {
		t.Fatalf("OpenPath: %v", err)
	}
	if !m.recovery.present || m.recovery.content != recovered {
		t.Fatalf("expected recovery offered, recovery = %+v", m.recovery)
	}
	if m.notice == "" {
		t.Fatal("expected a recovery notice on open")
	}

	m.recoverSwap()
	if m.editor.Content() != recovered {
		t.Fatalf("recovered content = %q, want %q", m.editor.Content(), recovered)
	}
	if !m.editor.Dirty {
		t.Fatal("recovered buffer must be dirty (differs from disk) until saved")
	}
}

// 2.1 — a swap older than the file is stale (superseded by a later save) and is
// not offered for recovery.
func TestOpenIgnoresStaleSwap(t *testing.T) {
	root, ch1, _ := newTestProject(t)
	sp := swapPath(ch1)
	if err := os.WriteFile(sp, []byte("old draft"), 0644); err != nil {
		t.Fatal(err)
	}
	// Swap in the past, file touched now.
	past := time.Now().Add(-time.Hour)
	if err := os.Chtimes(sp, past, past); err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	if err := os.Chtimes(ch1, now, now); err != nil {
		t.Fatal(err)
	}

	m := New()
	if err := m.OpenPath(root); err != nil {
		t.Fatalf("OpenPath: %v", err)
	}
	if m.recovery.present {
		t.Fatalf("stale swap must not be offered, got %+v", m.recovery)
	}
}

func TestRecoverySupportsEmptyContentAndUsesPrivateSwap(t *testing.T) {
	m, ch1, _ := openTestModel(t)
	m.editor.SetRecoveredContent("", "non-empty")
	m.writeSwap()

	info, err := os.Stat(swapPath(ch1))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("swap mode = %o, want 0600", info.Mode().Perm())
	}
	future := time.Now().Add(time.Hour)
	if err := os.Chtimes(swapPath(ch1), future, future); err != nil {
		t.Fatal(err)
	}

	next := New()
	if err := next.OpenPath(ch1); err != nil {
		t.Fatal(err)
	}
	if !next.recovery.present || next.recovery.content != "" {
		t.Fatalf("empty recovery was not preserved: %+v", next.recovery)
	}
	next.recoverSwap()
	if !next.recovery.applied || next.editor.Content() != "" {
		t.Fatalf("empty recovery was not applied: %+v", next.recovery)
	}
}

func TestRecoveryRefusesDifferentBuffer(t *testing.T) {
	m, _, ch2 := openTestModel(t)
	m.recovery = recoveryState{present: true, canonicalFile: ch2, swapPath: swapPath(ch2), content: "wrong"}
	before := m.editor.Content()
	m.recoverSwap()
	if m.editor.Content() != before || !m.noticeIsErr {
		t.Fatal("mismatched recovery changed the live buffer")
	}
}

func TestUnnamedBufferRecoverySurvivesRestart(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	m := New()
	m.editor.SetRecoveredContent("unnamed draft", "")
	m.writeSwap()
	sp := unnamedSwapPath(m.unnamedBufferID)
	if _, err := os.Stat(sp); err != nil {
		t.Fatal(err)
	}

	restarted := New()
	if restarted.unnamedBufferID != m.unnamedBufferID || !restarted.recovery.present || restarted.recovery.content != "unnamed draft" {
		t.Fatalf("unnamed recovery did not survive restart: %+v", restarted.recovery)
	}
	restarted.executeCommand("q")
	if _, err := os.Stat(sp); err != nil {
		t.Fatalf("ordinary quit removed unresolved recovery: %v", err)
	}
	restarted.discardRecovery()
	if _, err := os.Stat(sp); !os.IsNotExist(err) {
		t.Fatalf("explicit discard did not remove recovery: %v", err)
	}
}

func TestOpenPathFailureIsAtomic(t *testing.T) {
	m, ch1, _ := openTestModel(t)
	beforeRoot := m.project.Root
	beforeContent := m.editor.Content()
	if err := m.OpenPath(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("OpenPath(missing) succeeded")
	}
	if m.project.Root != beforeRoot || m.project.CurrentFile != ch1 || m.editor.Content() != beforeContent {
		t.Fatal("failed OpenPath partially mutated the live model")
	}
}

// 0.18 — Tab in insert mode indents the editor rather than cycling focus.
func TestTabInsertModeIndents(t *testing.T) {
	m, _, _ := openTestModel(t)
	m = update(m, keyRunes("i")) // insert mode
	if m.editor.Mode != editor.ModeInsert {
		t.Fatalf("expected insert mode")
	}
	before := m.editor.Content()
	m = update(m, tea.KeyMsg{Type: tea.KeyTab})
	if m.editor.Content() == before {
		t.Fatal("Tab in insert mode should have indented (changed content)")
	}
	if m.focus != FocusEditor {
		t.Fatal("Tab in insert mode must not cycle focus away from the editor")
	}
}

// 0.17 — a space is typeable in the command prompt (KeySpace path).
func TestPromptAcceptsSpace(t *testing.T) {
	m, _, _ := openTestModel(t)
	m.setPrompt(promptCommand)
	m.commandBuffer = "w"
	// handleCommandInput has a pointer receiver and mutates m in place.
	m.handleCommandInput(tea.KeyMsg{Type: tea.KeySpace})
	if m.commandBuffer != "w " {
		t.Fatalf("commandBuffer = %q, want %q", m.commandBuffer, "w ")
	}
}

// Non-project/empty launch shows guidance instead of a silent dead-end empty
// editor (save-as via :w <path> already works).
func TestEmptyBufferShowsGuidance(t *testing.T) {
	m := New()
	m.notice = "" // simulate a configured user (no first-run LLM hint)
	dir := t.TempDir()
	if err := m.OpenPath(dir); err != nil {
		t.Fatalf("OpenPath(empty dir): %v", err)
	}
	if !strings.Contains(m.notice, ":w <path>") {
		t.Fatalf("expected empty-buffer guidance, got %q", m.notice)
	}
	if m.noticeIsErr {
		t.Fatal("guidance must not render as an error")
	}
}

// 4.1 — :replace previews a confirmation and only rewrites chapters after the
// user accepts; the open buffer is reloaded to reflect the on-disk change.
func TestReplaceCommandPreviewAndConfirm(t *testing.T) {
	m, ch1, _ := openTestModel(t)
	if err := os.WriteFile(ch1, []byte("The dragon woke the dragon."), 0644); err != nil {
		t.Fatal(err)
	}
	_ = m.project.Refresh()
	// Reload so the editor buffer matches the file we just wrote.
	if err := m.loadIntoEditor(ch1); err != nil {
		t.Fatal(err)
	}

	// :replace opens a confirmation; nothing is written yet.
	m.executeCommand("replace dragon/griffin")
	if !m.results.Visible() || !m.results.HasPendingProposal() {
		t.Fatalf("expected a pending confirmation in the results pane")
	}
	if m.pendingAction == nil || m.pendingAction.Kind != pendingReplace {
		t.Fatal("expected a pending replace request")
	}
	if got, _ := os.ReadFile(ch1); strings.Contains(string(got), "griffin") {
		t.Fatal("replace must not write before confirmation")
	}

	// Accept: chapters are rewritten and the buffer reloads.
	next, _ := m.Update(results.ConfirmAcceptedMsg{ActionID: m.pendingAction.ID})
	m = asModel(next)
	got, _ := os.ReadFile(ch1)
	if string(got) != "The griffin woke the griffin." {
		t.Fatalf("content after confirm = %q", got)
	}
	if m.pendingAction != nil {
		t.Fatal("pending replace should be cleared after apply")
	}
	if !strings.Contains(m.editor.Content(), "griffin") {
		t.Fatalf("editor buffer not reloaded: %q", m.editor.Content())
	}
}

// 4.1 — rejecting the :replace confirmation writes nothing.
func TestReplaceCommandCancel(t *testing.T) {
	m, ch1, _ := openTestModel(t)
	if err := os.WriteFile(ch1, []byte("The dragon."), 0644); err != nil {
		t.Fatal(err)
	}
	_ = m.project.Refresh()
	if err := m.loadIntoEditor(ch1); err != nil {
		t.Fatal(err)
	}

	m.executeCommand("replace dragon/griffin")
	next, _ := m.Update(results.DiffRejectedMsg{ActionID: m.pendingAction.ID}) // "n"
	m = asModel(next)
	if m.pendingAction != nil {
		t.Fatal("cancel should clear the pending replace")
	}
	if got, _ := os.ReadFile(ch1); string(got) != "The dragon." {
		t.Fatalf("cancel must not modify the file, got %q", got)
	}
}

// 4.3 — Extract State proposes bible entries from a chapter, and accepting the
// confirmation writes the new character/location files.
func TestExtractStateProposalsAndCreate(t *testing.T) {
	// Fake Anthropic endpoint returning a JSON proposal array.
	body := `[{"category":"characters","name":"Jack","summary":"A wandering knight."},` +
		`{"category":"locations","name":"Rookhaven","summary":"A cliffside town."}]`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"content":[{"type":"text","text":%q}]}`, body)
	}))
	defer srv.Close()

	m, _, _ := openTestModel(t)
	// The state/ dir must exist for creation.
	if m.state == nil {
		m.state = state.New(m.project.Root)
	}
	if err := m.state.Initialize(); err != nil {
		t.Fatal(err)
	}
	m.llmClient = llm.NewClient(config.LLMConfig{Provider: config.ProviderAnthropic, APIKey: "k", Endpoint: srv.URL})
	m.llmLoading = true
	m.llmReqID = 1

	// Run the real runner and feed the ResponseMsg through the app.
	resp := llmcmd.ExecuteExtractState(m.llmClient, &command.Context{EditorContent: "Jack rode into Rookhaven."}, context.Background())().(llmcmd.ResponseMsg)
	resp.ReqID = 1
	resp.Operation = llmcmd.Operation{RequestID: 1, Kind: "llm.extract-state", ProjectRoot: m.project.Root, SourceFile: m.project.CurrentFile, BufferRevision: m.bufferGen, Target: filepath.Join(m.project.Root, "state")}
	m = update(m, resp)

	if m.pendingAction == nil || len(m.pendingAction.Proposals) != 2 {
		t.Fatalf("expected 2 pending proposals")
	}
	if !m.results.Visible() || !m.results.HasPendingProposal() {
		t.Fatal("expected a confirmation in the results pane")
	}

	// Accept: files are written.
	m = update(m, results.ConfirmAcceptedMsg{ActionID: m.pendingAction.ID})
	if m.pendingAction != nil {
		t.Fatal("pending proposals should be cleared after apply")
	}
	root := m.project.Root
	for _, rel := range []string{"state/characters/jack.md", "state/locations/rookhaven.md"} {
		if _, err := os.Stat(filepath.Join(root, rel)); err != nil {
			t.Fatalf("expected %s to be created: %v", rel, err)
		}
	}
	got, _ := os.ReadFile(filepath.Join(root, "state/characters/jack.md"))
	if !strings.Contains(string(got), "A wandering knight.") {
		t.Fatalf("summary not written into the entry: %q", got)
	}
}

// 4.4 — a note added via :note in Visual mode persists to a sidecar and is
// reloaded (re-anchored) when the file is reopened; :notes lists it.
func TestNotePersistsAndReloads(t *testing.T) {
	m, ch1, _ := openTestModel(t)
	root := m.project.Root
	if err := os.WriteFile(ch1, []byte("alpha\nbeta gamma\ndelta\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_ = m.project.Refresh()
	if err := m.loadIntoEditor(ch1); err != nil {
		t.Fatal(err)
	}

	// Select "beta gamma" on row 1 and attach a note via :note.
	ed := &m.editor
	ed.SetSelectionForTest(1, 0, 1, 9)
	m.executeCommand("note this needs work")
	if !strings.Contains(m.notice, "Note added") {
		t.Fatalf("expected a confirmation, got %q", m.notice)
	}

	// Sidecar written next to the file.
	if _, err := os.Stat(notesPath(ch1)); err != nil {
		t.Fatalf("notes sidecar not written: %v", err)
	}

	// Reopen from scratch: the note reloads and re-anchors.
	m2 := New()
	if err := m2.OpenPath(root); err != nil {
		t.Fatalf("reopen project: %v", err)
	}
	notes := m2.editor.Notes()
	if len(notes) != 1 || notes[0].Body != "this needs work" || notes[0].Row != 1 {
		t.Fatalf("note not reloaded correctly: %+v", notes)
	}

	// :notes lists it as a jumpable result.
	m2.listNotesCommand()
	if !m2.results.Visible() {
		t.Fatal(":notes should open the results pane")
	}
}

func TestNotesSidecarUsesPrivatePermissions(t *testing.T) {
	m, ch1, _ := openTestModel(t)
	m.editor.SetContent("private prose")
	m.editor.SetSelectionForTest(0, 0, 0, 6)
	if !m.editor.AddNote("private note") {
		t.Fatal("AddNote failed")
	}
	m.saveNotes()
	info, err := os.Stat(notesPath(ch1))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Fatalf("notes mode = %o, want 0600", got)
	}
}

// 4.4 / regression — ":" works from Visual mode (keeping the selection), so a
// selection command like :note runs on the live selection.
func TestColonWorksFromVisualMode(t *testing.T) {
	m, ch1, _ := openTestModel(t)
	if err := os.WriteFile(ch1, []byte("The dragon slept.\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_ = m.project.Refresh()
	if err := m.loadIntoEditor(ch1); err != nil {
		t.Fatal(err)
	}

	// Enter Visual mode and select a span via real keys.
	m = update(m, keyRunes("v"))
	m = update(m, keyRunes("e")) // extend to end of "The"
	if m.editor.Mode != editor.ModeVisual {
		t.Fatalf("expected Visual mode, got %v", m.editor.Mode)
	}

	// ":" must enter command mode without dropping the selection.
	m = update(m, keyRunes(":"))
	if m.activePrompt() != promptCommand {
		t.Fatal(`":" should enter command mode from Visual`)
	}
	if m.editor.Mode != editor.ModeVisual {
		t.Fatal("the selection must survive entering command mode")
	}

	// Type :note ... and run it — the note attaches to the selection.
	for _, r := range "note a comment" {
		m = update(m, keyRunes(string(r)))
	}
	m = update(m, tea.KeyMsg{Type: tea.KeyEnter})
	if !strings.Contains(m.notice, "Note added") {
		t.Fatalf("expected :note to add a note, notice=%q", m.notice)
	}
	if len(m.editor.Notes()) != 1 {
		t.Fatalf("expected 1 note, got %d", len(m.editor.Notes()))
	}
}
