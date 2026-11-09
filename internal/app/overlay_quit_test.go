package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

const dirtyProse = "# Chapter 1\n\nalpha beta gamma delta epsilon"

func runCompile(t *testing.T, m Model, root string) string {
	t.Helper()
	cmd := m.executeCommand("Compile")
	if cmd == nil {
		t.Fatal("Compile returned no command")
	}
	// Mutate the model afterwards: the snapshot must already have been taken.
	m.editor.SetContent("MUTATED AFTER SNAPSHOT")
	cmd()
	data, err := os.ReadFile(filepath.Join(root, "manuscript.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestCompileIncludesUnsavedChapter(t *testing.T) {
	m, ch1, _ := openTestModel(t)
	root := m.project.Root
	m.editor.SetContent(dirtyProse)
	m.editor.Dirty = true
	got := runCompile(t, m, root)
	if !strings.Contains(got, "alpha beta gamma delta epsilon") || !strings.Contains(got, "World") {
		t.Fatalf("manuscript missing unsaved text:\n%s", got)
	}
	if strings.Contains(got, "MUTATED") {
		t.Fatal("compile read model state after snapshot")
	}
	disk, _ := os.ReadFile(ch1)
	if strings.Contains(string(disk), "alpha") {
		t.Fatal("compile must not save the chapter")
	}
	// A second compile keeps a .bak of the first.
	m.editor.SetContent(dirtyProse + " zeta")
	runCompile(t, m, root)
	bak, err := os.ReadFile(filepath.Join(root, "manuscript.md.bak"))
	if err != nil || !strings.Contains(string(bak), "epsilon") {
		t.Fatalf("missing .bak of prior compile: %v", err)
	}
}

func TestCompileDoesNotOverlayNonChapterBuffer(t *testing.T) {
	m, _, _ := openTestModel(t)
	root := m.project.Root
	m.project.CurrentFile = filepath.Join(root, "state", "characters", "bob.md")
	m.editor.SetContent("STATEFILE secret words")
	got := runCompile(t, m, root)
	if strings.Contains(got, "STATEFILE") || !strings.Contains(got, "Hello") {
		t.Fatalf("non-chapter buffer was overlaid:\n%s", got)
	}
	m.project.CurrentFile = ""
	m.editor.SetContent("STATEFILE secret words")
	got = runCompile(t, m, root)
	if strings.Contains(got, "STATEFILE") {
		t.Fatal("unnamed buffer was overlaid")
	}
}

func wordCountText(t *testing.T, m Model) string {
	t.Helper()
	m = update(m, tea.WindowSizeMsg{Width: 140, Height: 40})
	m.executeCommand("WordCount")
	return m.View()
}

func TestWordCountIncludesUnsavedChapter(t *testing.T) {
	m, _, _ := openTestModel(t)
	m.editor.SetContent(dirtyProse) // 8 words + chapter 2's 4
	m.editor.Dirty = true
	if out := wordCountText(t, m); !strings.Contains(out, "12 words") {
		t.Fatalf("expected 12 words including unsaved text:\n%s", out)
	}
}

func TestWordCountIgnoresNonChapterBuffer(t *testing.T) {
	m, _, _ := openTestModel(t)
	m.project.CurrentFile = filepath.Join(m.project.Root, "state", "characters", "bob.md")
	m.editor.SetContent("one two three four five six seven")
	if out := wordCountText(t, m); !strings.Contains(out, "8 words") {
		t.Fatalf("expected disk-only 8 words:\n%s", out)
	}
}

func TestCloseFileKeepsPendingRecoverySwap(t *testing.T) {
	m, ch1, _ := openTestModel(t)
	sp := swapPath(ch1)
	if err := os.WriteFile(sp, []byte("recovered"), 0600); err != nil {
		t.Fatal(err)
	}
	m.recovery = recoveryState{present: true, canonicalFile: ch1, swapPath: sp, content: "recovered"}
	cmd := m.executeCommand("CloseFile")
	if cmd == nil {
		t.Fatal("expected quit command")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatal("CloseFile did not quit")
	}
	if _, err := os.Stat(sp); err != nil {
		t.Fatalf("pending recovery swap was deleted: %v", err)
	}
}

func TestCloseFileCleanRemovesStaleSwapAndDirtyBlocks(t *testing.T) {
	m, ch1, _ := openTestModel(t)
	sp := swapPath(ch1)
	if err := os.WriteFile(sp, []byte("stale"), 0600); err != nil {
		t.Fatal(err)
	}
	m.recovery = recoveryState{}
	if cmd := m.executeCommand("CloseFile"); cmd == nil {
		t.Fatal("expected quit")
	}
	if _, err := os.Stat(sp); !os.IsNotExist(err) {
		t.Fatalf("stale swap should be removed on clean quit: %v", err)
	}
	m2, _, _ := openTestModel(t)
	m2.editor.SetContent("dirty")
	m2.editor.Dirty = true
	if cmd := m2.executeCommand("CloseFile"); cmd != nil {
		if _, ok := cmd().(tea.QuitMsg); ok {
			t.Fatal("dirty CloseFile must not quit")
		}
	}
}
