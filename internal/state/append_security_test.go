package state

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func appendFixture(t *testing.T) (*Manager, string) {
	t.Helper()
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "state"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "chapters"), 0755); err != nil {
		t.Fatal(err)
	}
	return New(root), root
}

func TestAppendToFileNormal(t *testing.T) {
	m, root := appendFixture(t)
	p := filepath.Join(root, "state", "timeline.md")
	if err := os.WriteFile(p, []byte("# T\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := m.AppendToFile("timeline.md", "Day 1"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "# T\n\nDay 1\n" {
		t.Fatalf("got %q", b)
	}
}

func TestAppendToFileRejectsTraversal(t *testing.T) {
	m, root := appendFixture(t)
	ch := filepath.Join(root, "chapters", "001.md")
	if err := os.WriteFile(ch, []byte("prose"), 0644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../chapters/001.md", "..", ".", "", "sub/x.md", `a\b.md`, "a\x00b", ch} {
		if err := m.AppendToFile(name, "PWNED"); err == nil {
			t.Errorf("AppendToFile(%q) succeeded", name)
		}
	}
	if b, _ := os.ReadFile(ch); string(b) != "prose" {
		t.Fatalf("chapter modified: %q", b)
	}
}

func TestAppendToFileRejectsSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks")
	}
	m, root := appendFixture(t)
	ch := filepath.Join(root, "chapters", "001.md")
	if err := os.WriteFile(ch, []byte("prose"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(ch, filepath.Join(root, "state", "timeline.md")); err != nil {
		t.Fatal(err)
	}
	if err := m.AppendToFile("timeline.md", "PWNED"); err == nil {
		t.Fatal("append through symlink succeeded")
	}
	if b, _ := os.ReadFile(ch); string(b) != "prose" {
		t.Fatalf("chapter modified: %q", b)
	}
}
