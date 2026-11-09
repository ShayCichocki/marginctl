package app

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ShayCichocki/marginctl/internal/command"
)

// state/ replaced by a symlink after OpenPath must refuse accepted proposals.
func TestProposalsRejectSymlinkedStateDirAfterOpen(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks")
	}
	for _, dest := range []string{"outside", "chapters"} {
		for _, action := range []string{"update", "append"} {
			t.Run(dest+"/"+action, func(t *testing.T) {
				isolateAppEnv(t)
				root, ch1, _ := newTestProject(t)
				m := New()
				if err := m.OpenPath(root); err != nil {
					t.Fatal(err)
				}
				target := t.TempDir()
				if dest == "chapters" {
					target = filepath.Join(root, "chapters")
				}
				victim := filepath.Join(target, "victim.md")
				if err := os.WriteFile(victim, []byte("precious"), 0644); err != nil {
					t.Fatal(err)
				}
				tl := filepath.Join(target, "timeline.md")
				if err := os.WriteFile(tl, []byte("precious"), 0644); err != nil {
					t.Fatal(err)
				}
				_ = os.RemoveAll(filepath.Join(root, "state"))
				if err := os.Symlink(target, filepath.Join(root, "state")); err != nil {
					t.Fatal(err)
				}
				ch1Before, _ := os.ReadFile(ch1)
				for _, p := range []command.StateProposal{
					{Category: "characters", Name: "x", Action: action, Target: "victim.md", Proposed: "PWNED"},
					{Category: "timeline", Name: "t", Action: action, Proposed: "PWNED"},
				} {
					m.err, m.notice, m.noticeIsErr = nil, "", false
					m.applyStateProposals(armProposals(t, &m, p))
					if m.err == nil || !m.noticeIsErr {
						t.Fatalf("expected surfaced error, err=%v notice=%q", m.err, m.notice)
					}
				}
				for _, f := range []string{victim, tl} {
					if b, _ := os.ReadFile(f); string(b) != "precious" {
						t.Fatalf("%s modified: %q", f, b)
					}
				}
				if b, _ := os.ReadFile(ch1); string(b) != string(ch1Before) {
					t.Fatalf("chapter modified: %q", b)
				}
			})
		}
	}
}

func chaptersModel(t *testing.T, files map[string]string, open string) (Model, string) {
	t.Helper()
	isolateAppEnv(t)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "chapters"), 0755); err != nil {
		t.Fatal(err)
	}
	for n, body := range files {
		if err := os.WriteFile(filepath.Join(root, "chapters", n), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	m := New()
	if err := m.OpenPath(root); err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(m.project.CurrentFile, open) {
		t.Fatalf("setup: open %q, want %q", m.project.CurrentFile, open)
	}
	return m, root
}

var sameTitleChapters = map[string]string{"001_scene.md": "FIRST", "002_scene.md": "SECOND"}

func TestChapterInsertSameTitleKeepsBufferOnSameChapter(t *testing.T) {
	m, root := chaptersModel(t, sameTitleChapters, "001_scene.md")
	m.executeCommand("ChapterInsert 1 Prologue")
	if m.err != nil {
		t.Fatal(m.err)
	}
	want := filepath.Join(root, "chapters", "002_scene.md")
	if m.project.CurrentFile != want || m.editor.Content() != "FIRST" {
		t.Fatalf("buffer at %q showing %q; want %q showing FIRST", m.project.CurrentFile, m.editor.Content(), want)
	}
}

func TestChapterReorderSameTitleKeepsBufferOnSameChapter(t *testing.T) {
	m, root := chaptersModel(t, sameTitleChapters, "001_scene.md")
	m.executeCommand("ChapterReorder 2")
	if m.err != nil {
		t.Fatal(m.err)
	}
	want := filepath.Join(root, "chapters", "002_scene.md")
	if m.project.CurrentFile != want || m.editor.Content() != "FIRST" {
		t.Fatalf("buffer at %q showing %q; want %q showing FIRST", m.project.CurrentFile, m.editor.Content(), want)
	}
}

// Unprefixed chapters are renumbered by duplicate's ordering pass, including
// the open source chapter; the buffer must follow it.
func TestChapterDuplicateRebindsOpenChapterWhenRenumbered(t *testing.T) {
	m, root := chaptersModel(t, map[string]string{"alpha.md": "ALPHA", "beta.md": "BETA"}, "alpha.md")
	m.executeCommand("ChapterDuplicate Echo")
	if m.err != nil {
		t.Fatal(m.err)
	}
	want := filepath.Join(root, "chapters", "001_alpha.md")
	if m.project.CurrentFile != want || !fileExists(want) || m.editor.Content() != "ALPHA" {
		t.Fatalf("open chapter %q (content %q); want %q showing ALPHA", m.project.CurrentFile, m.editor.Content(), want)
	}
}
