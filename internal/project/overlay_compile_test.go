package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompileToFileWithOverlayAndIsChapter(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "chapters"), 0755); err != nil {
		t.Fatal(err)
	}
	c1 := filepath.Join(root, "chapters", "001_one.md")
	c2 := filepath.Join(root, "chapters", "002_two.md")
	for _, c := range []string{c1, c2} {
		if err := os.WriteFile(c, []byte("disk "+filepath.Base(c)+"\n"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	p := New()
	if err := p.Open(root); err != nil {
		t.Fatal(err)
	}
	if !p.IsChapter(c1) || p.IsChapter("") || p.IsChapter(filepath.Join(root, "other.md")) {
		t.Fatal("IsChapter misclassified paths")
	}
	if _, err := p.CompileToFile(); err != nil {
		t.Fatal(err)
	}
	out, err := p.CompileToFileWithOverlay(c1, "unsaved prose\n")
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(out)
	if !strings.Contains(string(got), "unsaved prose") || strings.Contains(string(got), "disk 001") || !strings.Contains(string(got), "disk 002") {
		t.Fatalf("overlay not applied:\n%s", got)
	}
	bak, err := os.ReadFile(out + ".bak")
	if err != nil || !strings.Contains(string(bak), "disk 001") {
		t.Fatalf("missing .bak of prior compile: %v", err)
	}
}
