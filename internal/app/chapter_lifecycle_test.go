package app

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// snapshotTree records every file (relative path -> content) under root so a
// test can prove an operation changed nothing on disk.
func snapshotTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		if d.IsDir() {
			out[rel+"/"] = ""
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out[rel] = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	return out
}

func sameSnapshot(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// lifecycleModel opens the test project in an isolated environment.
func lifecycleModel(t *testing.T) (Model, string, string, string) {
	t.Helper()
	isolateAppEnv(t)
	m, ch1, ch2 := openTestModel(t)
	return m, m.project.Root, ch1, ch2
}

func TestChapterLifecycleDirtyBufferIsNeverLostOrOverwritten(t *testing.T) {
	cmds := []string{
		"ChapterRename Fresh",
		"ChapterDuplicate Copy",
		"ChapterArchive",
		"ChapterTrash",
		"ChapterRestore archive/001_chapter_1.md",
		"ChapterReorder 2",
		"ChapterInsert 1 Prologue",
	}
	for _, c := range cmds {
		c := c
		t.Run(c, func(t *testing.T) {
			m, root, ch1, _ := lifecycleModel(t)
			m = dirtyEditor(t, m)
			content := m.editor.Content()
			before := snapshotTree(t, root)
			fp := m.loadedFingerprint

			m.err = nil
			m.notice, m.noticeIsErr = "", false
			_ = m.executeCommand(c)

			if !m.noticeIsErr || !strings.Contains(m.notice, "Unsaved") {
				t.Fatalf("expected unsaved-changes refusal, notice=%q err=%v", m.notice, m.noticeIsErr)
			}
			if m.editor.Content() != content || !m.editor.Dirty {
				t.Fatal("dirty buffer was altered")
			}
			if m.project.CurrentFile != ch1 || m.loadedFingerprint != fp {
				t.Fatal("current file / fingerprint changed despite refusal")
			}
			if !sameSnapshot(before, snapshotTree(t, root)) {
				t.Fatal("disk changed despite refusal")
			}
		})
	}
}

func TestChapterLifecycleRenameFollowsOpenBuffer(t *testing.T) {
	m, root, ch1, _ := lifecycleModel(t)
	want := m.editor.Content()
	m.executeCommand("ChapterRename Opening")
	if m.err != nil {
		t.Fatalf("rename: %v", m.err)
	}
	newPath := filepath.Join(root, "chapters", "001_Opening.md")
	if fileExists(ch1) || !fileExists(newPath) {
		t.Fatalf("rename did not move file on disk (old exists=%v new exists=%v)", fileExists(ch1), fileExists(newPath))
	}
	if m.project.CurrentFile != newPath {
		t.Fatalf("CurrentFile = %q, want %q", m.project.CurrentFile, newPath)
	}
	if m.editor.Content() != want || m.editor.Dirty {
		t.Fatalf("buffer changed: dirty=%v content=%q", m.editor.Dirty, m.editor.Content())
	}
	if m.loadedFingerprint.CanonicalPath != newPath {
		t.Fatalf("loadedFingerprint follows %q, want %q", m.loadedFingerprint.CanonicalPath, newPath)
	}
	// A subsequent edit and :w must land in the renamed file, not resurrect the old one.
	m = dirtyEditor(t, m)
	m = pressEsc(m)
	if cmd := m.executeCommand("w"); cmd != nil {
		t.Logf("w returned cmd")
	}
	if m.err != nil {
		t.Fatalf("save after rename: %v", m.err)
	}
	if fileExists(ch1) {
		t.Fatal("save after rename resurrected the old path")
	}
	data, _ := os.ReadFile(newPath)
	if !strings.Contains(string(data), "Z") {
		t.Fatalf("edit not saved to renamed file: %q", data)
	}
}

func TestChapterLifecycleUnusableNamesRejectedWithoutDiskChange(t *testing.T) {
	// Names with nothing usable after sanitization are errors.
	names := []string{"", "..", "...", "///", `\\`}
	for _, verb := range []string{"ChapterRename", "ChapterInsert 1"} {
		for _, name := range names {
			verb, name := verb, name
			t.Run(verb+"/"+name, func(t *testing.T) {
				m, root, ch1, _ := lifecycleModel(t)
				before := snapshotTree(t, root)
				m.err = nil
				m.notice, m.noticeIsErr = "", false
				_ = m.executeCommand(strings.TrimSpace(verb + " " + name))
				if m.err == nil && !m.noticeIsErr {
					t.Fatalf("unusable name accepted (notice=%q)", m.notice)
				}
				if !sameSnapshot(before, snapshotTree(t, root)) {
					t.Fatal("filesystem changed after rejected name")
				}
				if m.project.CurrentFile != ch1 {
					t.Fatalf("CurrentFile moved to %q", m.project.CurrentFile)
				}
			})
		}
	}
}

// Separators are stripped by fsutil.SanitizeName, so a traversal attempt either
// is rejected or collapses to a plain name inside chapters/. Either way nothing
// may be created or moved outside the project root.
func TestChapterLifecycleTraversalNamesNeverEscapeProject(t *testing.T) {
	for _, verb := range []string{"ChapterRename", "ChapterDuplicate", "ChapterInsert 1"} {
		for _, name := range []string{"../x", "a/b", `..\x`, "../../etc/passwd", "/abs/path"} {
			verb, name := verb, name
			t.Run(verb+"/"+name, func(t *testing.T) {
				m, root, _, _ := lifecycleModel(t)
				parent := filepath.Dir(root)
				_ = m.executeCommand(verb + " " + name)
				for _, leaked := range []string{"x", "x.md", "b", "b.md", "passwd", "passwd.md", "ab.md", "path.md"} {
					if fileExists(filepath.Join(parent, leaked)) {
						t.Fatalf("%s leaked outside the project root", leaked)
					}
				}
				if fileExists("/abs") {
					t.Fatal("absolute path honored")
				}
				for _, ch := range m.project.ListChapters() {
					if filepath.Dir(ch.Path) != filepath.Join(root, "chapters") {
						t.Fatalf("chapter outside chapters/: %s", ch.Path)
					}
				}
			})
		}
	}
}

func TestChapterLifecycleRenameSanitizesSeparators(t *testing.T) {
	m, root, _, _ := lifecycleModel(t)
	m.executeCommand("ChapterRename ../x")
	if m.err != nil {
		t.Fatalf("rename: %v", m.err)
	}
	if want := filepath.Join(root, "chapters", "001_x.md"); m.project.CurrentFile != want || !fileExists(want) {
		t.Fatalf("CurrentFile = %q, want %q", m.project.CurrentFile, want)
	}
}

func TestChapterLifecycleRenameOntoExistingChapterRejected(t *testing.T) {
	m, root, ch1, _ := lifecycleModel(t)
	taken := filepath.Join(root, "chapters", "001_taken.md")
	if err := os.WriteFile(taken, []byte("precious"), 0644); err != nil {
		t.Fatal(err)
	}
	before := snapshotTree(t, root)
	m.err = nil
	m.executeCommand("ChapterRename taken")
	if m.err == nil {
		t.Fatal("rename onto an existing chapter was accepted")
	}
	if got, _ := os.ReadFile(taken); string(got) != "precious" {
		t.Fatalf("existing chapter overwritten: %q", got)
	}
	if !sameSnapshot(before, snapshotTree(t, root)) || m.project.CurrentFile != ch1 {
		t.Fatal("state changed after rejected rename")
	}
}

func TestChapterLifecycleDuplicateKeepsOpenBuffer(t *testing.T) {
	m, root, ch1, _ := lifecycleModel(t)
	want := m.editor.Content()
	m.executeCommand("ChapterDuplicate Echo")
	if m.err != nil {
		t.Fatalf("duplicate: %v", m.err)
	}
	if m.project.CurrentFile != ch1 || m.editor.Content() != want {
		t.Fatalf("duplicate disturbed open buffer: file=%q", m.project.CurrentFile)
	}
	copyPath := filepath.Join(root, "chapters", "002_Echo.md")
	if data, err := os.ReadFile(copyPath); err != nil || string(data) != want {
		t.Fatalf("duplicate missing or wrong (err=%v data=%q)", err, data)
	}
}

func TestChapterLifecycleArchiveAndTrashCloseBufferAndRestoreRoundTrips(t *testing.T) {
	for _, verb := range []string{"ChapterArchive", "ChapterTrash"} {
		verb := verb
		t.Run(verb, func(t *testing.T) {
			m, root, ch1, _ := lifecycleModel(t)
			orig, _ := os.ReadFile(ch1)
			m.executeCommand(verb)
			if m.err != nil {
				t.Fatalf("%s: %v", verb, m.err)
			}
			if fileExists(ch1) {
				t.Fatal("chapter still in chapters/")
			}
			if m.project.CurrentFile != "" || m.editor.Content() != "" {
				t.Fatalf("buffer still bound to removed chapter: %q %q", m.project.CurrentFile, m.editor.Content())
			}
			rel := filepath.Join("archive", filepath.Base(ch1))
			if verb == "ChapterTrash" {
				rel = filepath.Join(".trash", "chapters", filepath.Base(ch1))
			}
			if got, err := os.ReadFile(filepath.Join(root, rel)); err != nil || string(got) != string(orig) {
				t.Fatalf("moved content wrong: err=%v got=%q", err, got)
			}
			m.err = nil
			m.executeCommand("ChapterRestore " + rel)
			if m.err != nil {
				t.Fatalf("restore: %v", m.err)
			}
			if got, err := os.ReadFile(ch1); err != nil || string(got) != string(orig) {
				t.Fatalf("restored content wrong: err=%v got=%q", err, got)
			}
		})
	}
}

func TestChapterLifecycleRestoreRejectsPathsOutsideArchiveOrTrash(t *testing.T) {
	for _, arg := range []string{"chapters/002_chapter_2.md", "../outside.md", "/etc/hosts", "state"} {
		arg := arg
		t.Run(arg, func(t *testing.T) {
			m, root, _, _ := lifecycleModel(t)
			outside := filepath.Join(filepath.Dir(root), "outside.md")
			_ = os.WriteFile(outside, []byte("o"), 0644)
			before := snapshotTree(t, filepath.Dir(root))
			m.err = nil
			m.executeCommand("ChapterRestore " + arg)
			if m.err == nil {
				t.Fatal("restore of non-archived path accepted")
			}
			if !sameSnapshot(before, snapshotTree(t, filepath.Dir(root))) {
				t.Fatal("disk changed")
			}
		})
	}
}

func TestChapterLifecycleReorderFollowsOpenBuffer(t *testing.T) {
	m, root, ch1, ch2 := lifecycleModel(t)
	want := m.editor.Content()
	m.executeCommand("ChapterReorder 2")
	if m.err != nil {
		t.Fatalf("reorder: %v", m.err)
	}
	newCurrent := filepath.Join(root, "chapters", "002_chapter_1.md")
	if fileExists(ch1) || !fileExists(newCurrent) || !fileExists(filepath.Join(root, "chapters", "001_chapter_2.md")) {
		t.Fatalf("unexpected on-disk order after reorder (ch2 exists=%v)", fileExists(ch2))
	}
	if m.project.CurrentFile != newCurrent || m.editor.Content() != want || m.loadedFingerprint.CanonicalPath != newCurrent {
		t.Fatalf("open buffer did not follow reorder: file=%q fp=%q", m.project.CurrentFile, m.loadedFingerprint.CanonicalPath)
	}
}

func TestChapterLifecycleReorderBadPositionRejected(t *testing.T) {
	for _, arg := range []string{"", "0", "-1", "x"} {
		m, root, _, _ := lifecycleModel(t)
		before := snapshotTree(t, root)
		m.err = nil
		m.executeCommand(strings.TrimSpace("ChapterReorder " + arg))
		if m.err == nil || !sameSnapshot(before, snapshotTree(t, root)) {
			t.Errorf("ChapterReorder %q: err=%v or disk changed", arg, m.err)
		}
	}
}

// Inserting a chapter before the open one renumbers the open chapter on disk
// (001_ -> 002_). The buffer's identity must follow, or a later :w targets a
// path that no longer exists.
func TestChapterLifecycleInsertBeforeOpenChapterKeepsBufferBound(t *testing.T) {
	m, root, ch1, _ := lifecycleModel(t)
	m.executeCommand("ChapterInsert 1 Prologue")
	if m.err != nil {
		t.Fatalf("insert: %v", m.err)
	}
	if !fileExists(filepath.Join(root, "chapters", "001_Prologue.md")) {
		t.Fatal("inserted chapter missing")
	}
	if fileExists(ch1) && m.project.CurrentFile == ch1 {
		return // not renumbered; binding still valid
	}
	if !fileExists(m.project.CurrentFile) {
		t.Fatalf("CurrentFile %q does not exist after insert", m.project.CurrentFile)
	}
}

func TestChapterLifecycleOutsideProjectAndNoCurrentFile(t *testing.T) {
	isolateAppEnv(t)
	m := New()
	m.executeCommand("ChapterRename x")
	if !m.noticeIsErr || !strings.Contains(m.notice, "Not in a project") {
		t.Fatalf("notice = %q", m.notice)
	}

	m2, root, _, _ := lifecycleModel(t)
	m2.executeCommand("ChapterTrash")
	before := snapshotTree(t, root)
	m2.err = nil
	m2.executeCommand("ChapterRename y")
	if m2.err == nil || !sameSnapshot(before, snapshotTree(t, root)) {
		t.Fatalf("rename with no open chapter: err=%v", m2.err)
	}
}
