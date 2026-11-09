package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func workflowProject(t *testing.T) *Project {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "chapters"), 0755); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"001_One.md": "one disk\n", "002_Two.md": "two words\n"} {
		if err := os.WriteFile(filepath.Join(root, "chapters", name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	p := New()
	if err := p.Open(root); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestWholeManuscriptOverlayAndReadFailure(t *testing.T) {
	p := workflowProject(t)
	first := p.ListChapters()[0].Path
	total, _, err := p.WordCountWithOverlay(first, "unsaved words here")
	if err != nil || total != 5 {
		t.Fatalf("count=%d err=%v", total, err)
	}
	compiled, err := p.CompileWithOverlay(first, "unsaved prose")
	if err != nil || !strings.Contains(compiled, "unsaved prose") || strings.Contains(compiled, "one disk") {
		t.Fatalf("compile=%q err=%v", compiled, err)
	}
	if err = os.Remove(p.ListChapters()[1].Path); err != nil {
		t.Fatal(err)
	}
	if _, _, err = p.WordCountWithOverlay(first, "draft"); err == nil {
		t.Fatal("unreadable chapter was silently counted")
	}
}

func TestChapterLifecycleMovesNotesAndUsesTrash(t *testing.T) {
	p := workflowProject(t)
	old := p.ListChapters()[0].Path
	if err := os.WriteFile(chapterNotesPath(old), []byte(`[]`), 0600); err != nil {
		t.Fatal(err)
	}
	mv, err := p.RenameChapter(old, "Opening")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(chapterNotesPath(mv.NewPath)); err != nil {
		t.Fatal("notes not migrated:", err)
	}
	mv, err = p.TrashChapter(mv.NewPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(mv.NewPath, filepath.Join(".trash", "chapters")) {
		t.Fatalf("not project trash: %s", mv.NewPath)
	}
	restored, err := p.RestoreChapter(mv.NewPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(restored.NewPath); err != nil {
		t.Fatal(err)
	}
}

func TestReorderAndInsertNormalizeOrdering(t *testing.T) {
	p := workflowProject(t)
	if _, err := p.ReorderChapter(p.ListChapters()[1].Path, 0); err != nil {
		t.Fatal(err)
	}
	if got := p.ListChapters()[0].Name; got != "001_Two.md" {
		t.Fatalf("first=%s", got)
	}
	if _, err := p.InsertChapter(1, "Middle"); err != nil {
		t.Fatal(err)
	}
	want := []string{"001_Two.md", "002_Middle.md", "003_One.md"}
	for i, ch := range p.ListChapters() {
		if ch.Name != want[i] {
			t.Fatalf("chapter %d=%s want %s", i, ch.Name, want[i])
		}
	}
}
