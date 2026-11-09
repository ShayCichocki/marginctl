package project

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChapterTitlesFromHeadings(t *testing.T) {
	root := t.TempDir()
	ch := filepath.Join(root, "chapters")
	if err := os.MkdirAll(ch, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"001_chapter_1.md": "# The Monster in the Machine\n\nText",
		"002_chapter_2.md": "No heading\n# Late heading\n",
		"003_chapter_3.md": "---\nname: FM\n---\n# Body Title\n",
	}
	for n, c := range files {
		if err := os.WriteFile(filepath.Join(ch, n), []byte(c), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	p := New()
	if err := p.Open(root); err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range p.ListChapters() {
		got = append(got, c.Title)
	}
	want := []string{"The Monster in the Machine", "", "Body Title"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("titles = %q, want %q", got, want)
	}
	if err := os.WriteFile(filepath.Join(ch, "002_chapter_2.md"), []byte("# Renamed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := p.Refresh(); err != nil {
		t.Fatal(err)
	}
	if got := p.ListChapters()[1].Title; got != "Renamed" {
		t.Fatalf("refresh title = %q", got)
	}
}
