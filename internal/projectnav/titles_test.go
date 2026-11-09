package projectnav

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"

	"github.com/ShayCichocki/marginctl/internal/project"
)

func chapterEntry(name, path, title string) project.FileEntry {
	return project.FileEntry{Name: name, Path: path, Title: title}
}

func TestViewShowsTitleWithOrderNumber(t *testing.T) {
	m := New()
	m.SetChapters([]project.FileEntry{
		chapterEntry("001_chapter_1.md", "/a", "The Monster in the Machine"),
		chapterEntry("002_chapter_2.md", "/b", ""),
		chapterEntry("notes.md", "/c", "Loose Title"),
	})
	m.SetSize(40, 6)
	view := m.View()
	for _, want := range []string{"1  The Monster in the Machine", "002_chapter_2", "Loose Title"} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q:\n%s", want, view)
		}
	}
}

func TestViewTruncatesLongAndUnicodeTitles(t *testing.T) {
	for _, title := range []string{
		strings.Repeat("Very long title ", 20),
		strings.Repeat("日本語の長い題名", 10),
		"emoji 👩‍👩‍👧‍👦 " + strings.Repeat("é", 80),
	} {
		m := New()
		m.SetChapters([]project.FileEntry{chapterEntry("003_x.md", "/a", title)})
		m.SetSize(24, 3)
		view := m.View()
		for _, line := range strings.Split(view, "\n") {
			if w := lipgloss.Width(line); w > 26 {
				t.Fatalf("line wider than pane (%d): %q", w, line)
			}
		}
		if !strings.Contains(view, "3  ") || !strings.Contains(view, "…") {
			t.Fatalf("expected number prefix and ellipsis:\n%s", view)
		}
	}
}

func TestViewSanitizesTitle(t *testing.T) {
	m := New()
	m.SetChapters([]project.FileEntry{chapterEntry("001_a.md", "/a", "Evil\x1b[31m\x1b]8;;http://x\x07 title\x00")})
	m.SetSize(60, 3)
	if view := m.View(); strings.ContainsAny(view, "\x1b\x07\x00") {
		t.Fatalf("view retained controls: %q", view)
	}
}

func TestSetCurrentFileRereadsEditedTitle(t *testing.T) {
	path := filepath.Join(t.TempDir(), "001_chapter_1.md")
	if err := os.WriteFile(path, []byte("# Chapter 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := New()
	m.SetChapters([]project.FileEntry{chapterEntry("001_chapter_1.md", path, project.ReadChapterTitle(path))})
	m.SetSize(40, 3)
	if !strings.Contains(m.View(), "1  Chapter 1") {
		t.Fatalf("initial title missing:\n%s", m.View())
	}
	if err := os.WriteFile(path, []byte("# The Monster in the Machine\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m.SetCurrentFile(path) // what the app calls after a save
	if !strings.Contains(m.View(), "1  The Monster in the Machine") {
		t.Fatalf("edited title not picked up:\n%s", m.View())
	}
}
