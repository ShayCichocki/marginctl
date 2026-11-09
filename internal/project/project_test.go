package project

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/ShayCichocki/marginctl/internal/fsutil"
)

func TestOpenCanonicalizesRootAndAllowsContainedFile(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "chapters"), 0755); err != nil {
		t.Fatal(err)
	}
	chapter := filepath.Join(root, "chapters", "001_one.md")
	if err := os.WriteFile(chapter, []byte("# One\n"), 0644); err != nil {
		t.Fatal(err)
	}
	p := New()
	if err := p.Open(filepath.Join(root, ".")); err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(p.Root) {
		t.Fatalf("root %q is not absolute", p.Root)
	}
	if got, err := p.ReadFile(chapter); err != nil || got != "# One\n" {
		t.Fatalf("ReadFile() = %q, %v", got, err)
	}
}

func TestOpenRejectsSymlinkedChaptersRoot(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "chapters")); err != nil {
		t.Fatal(err)
	}
	p := New()
	if err := p.Open(root); !errors.Is(err, fsutil.ErrUnsafePath) {
		t.Fatalf("Open() error = %v, want ErrUnsafePath", err)
	}
}

func TestProjectRejectsSymlinkedChapterAndTargetDirectory(t *testing.T) {
	p := newProjectDir(t)
	outside := t.TempDir()
	target := filepath.Join(outside, "private.md")
	if err := os.WriteFile(target, []byte("private"), 0644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(p.Root, "chapters", "linked.md")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ReadFile(link); !errors.Is(err, fsutil.ErrUnsafePath) {
		t.Fatalf("ReadFile() error = %v, want ErrUnsafePath", err)
	}
	if err := p.SaveFile(link, "overwrite"); !errors.Is(err, fsutil.ErrUnsafePath) {
		t.Fatalf("SaveFile() error = %v, want ErrUnsafePath", err)
	}
	dirLink := filepath.Join(p.Root, "linked-dir")
	if err := os.Symlink(outside, dirLink); err != nil {
		t.Fatal(err)
	}
	if err := p.SaveFile(filepath.Join(dirLink, "escape.md"), "escape"); !errors.Is(err, fsutil.ErrUnsafePath) {
		t.Fatalf("SaveFile through directory symlink error = %v, want ErrUnsafePath", err)
	}
}

// newProjectDir creates a temp project with a chapters/ dir and returns an
// opened Project rooted there.
func newProjectDir(t *testing.T) *Project {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "chapters"), 0755); err != nil {
		t.Fatal(err)
	}
	p := New()
	if err := p.Open(dir); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestCreateChapterSanitizesTraversal(t *testing.T) {
	p := newProjectDir(t)

	path, err := p.CreateChapter("../escape")
	if err != nil {
		t.Fatalf("CreateChapter: %v", err)
	}

	// The file must live inside chapters/, not a sibling of the project root.
	wantDir := filepath.Join(p.Root, "chapters")
	if got := filepath.Dir(path); got != wantDir {
		t.Fatalf("chapter written to %q, want inside %q", path, wantDir)
	}
	if _, err := os.Stat(filepath.Join(p.Root, "escape.md")); !os.IsNotExist(err) {
		t.Fatalf("traversal escaped project root: %v", err)
	}
}

func TestCreateChapterRejectsEmpty(t *testing.T) {
	p := newProjectDir(t)
	if _, err := p.CreateChapter("../.."); err == nil {
		t.Fatal("expected error for a name that sanitizes to empty")
	}
}

// TestCreateChapterAssignsOrderingPrefix verifies each new chapter gets an
// incrementing zero-padded prefix and the title heading stays prefix-free.
func TestCreateChapterAssignsOrderingPrefix(t *testing.T) {
	p := newProjectDir(t)

	p1, err := p.CreateChapter("The Beginning")
	if err != nil {
		t.Fatal(err)
	}
	if got := filepath.Base(p1); got != "001_The Beginning.md" {
		t.Fatalf("first chapter = %q, want 001_The Beginning.md", got)
	}
	data, _ := os.ReadFile(p1)
	if string(data) != "# The Beginning\n\n" {
		t.Fatalf("heading = %q, want prefix-free title", data)
	}

	p2, err := p.CreateChapter("The Middle")
	if err != nil {
		t.Fatal(err)
	}
	if got := filepath.Base(p2); got != "002_The Middle.md" {
		t.Fatalf("second chapter = %q, want 002_The Middle.md", got)
	}
}

// TestCreateChapterStripsUserTypedPrefixAndExt verifies a user-typed prefix or
// .md extension isn't doubled up.
func TestCreateChapterStripsUserTypedPrefixAndExt(t *testing.T) {
	p := newProjectDir(t)
	path, err := p.CreateChapter("007_Bond.md")
	if err != nil {
		t.Fatal(err)
	}
	if got := filepath.Base(path); got != "001_Bond.md" {
		t.Fatalf("filename = %q, want 001_Bond.md", got)
	}
}

// TestChaptersNaturalSort verifies "Chapter 10" sorts after "Chapter 9".
func TestChaptersNaturalSort(t *testing.T) {
	root := t.TempDir()
	chapters := filepath.Join(root, "chapters")
	if err := os.MkdirAll(chapters, 0755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"Chapter 9.md", "Chapter 10.md", "Chapter 2.md"} {
		if err := os.WriteFile(filepath.Join(chapters, n), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	p := New()
	if err := p.Open(root); err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, c := range p.ListChapters() {
		got = append(got, c.Name)
	}
	want := []string{"Chapter 2.md", "Chapter 9.md", "Chapter 10.md"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

// TestOpenResetsPriorProjectState verifies that opening a plain directory after
// a project clears the stale chapter list and project flag.
func TestOpenResetsPriorProjectState(t *testing.T) {
	p := newProjectDir(t)
	if _, err := p.CreateChapter("Chapter One"); err != nil {
		t.Fatal(err)
	}
	if !p.IsProject || len(p.ListChapters()) == 0 {
		t.Fatalf("setup: expected a project with chapters, got IsProject=%v chapters=%d", p.IsProject, len(p.ListChapters()))
	}

	// Now open an unrelated plain directory (no chapters/).
	plain := t.TempDir()
	if err := p.Open(plain); err != nil {
		t.Fatal(err)
	}
	if p.IsProject {
		t.Error("IsProject should be false after opening a non-project dir")
	}
	if len(p.ListChapters()) != 0 {
		t.Errorf("stale chapters leaked: %v", p.ListChapters())
	}
	if p.CurrentFile != "" {
		t.Errorf("stale CurrentFile leaked: %q", p.CurrentFile)
	}
}

// TestOpenFileInsideProjectDetectsRoot verifies that opening a chapter file
// directly still discovers its enclosing project (chapters/ sibling).
func TestOpenFileInsideProjectDetectsRoot(t *testing.T) {
	root := t.TempDir()
	chapters := filepath.Join(root, "chapters")
	if err := os.MkdirAll(chapters, 0755); err != nil {
		t.Fatal(err)
	}
	ch1 := filepath.Join(chapters, "001_intro.md")
	if err := os.WriteFile(ch1, []byte("# Intro\n"), 0644); err != nil {
		t.Fatal(err)
	}

	p := New()
	if err := p.Open(ch1); err != nil {
		t.Fatal(err)
	}
	if !p.IsProject {
		t.Error("opening a file inside chapters/ should detect a project")
	}
	canonicalRoot, err := fsutil.CanonicalRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	if p.Root != canonicalRoot {
		t.Errorf("Root = %q, want %q", p.Root, canonicalRoot)
	}
	canonicalChapter := filepath.Join(canonicalRoot, "chapters", "001_intro.md")
	if p.CurrentFile != canonicalChapter {
		t.Errorf("CurrentFile = %q, want %q", p.CurrentFile, canonicalChapter)
	}
	if len(p.ListChapters()) != 1 {
		t.Errorf("expected 1 chapter, got %d", len(p.ListChapters()))
	}
}

// TestOpenLooseFileIsSingleFileMode verifies a file with no enclosing project
// falls back to single-file mode rooted at its parent.
func TestOpenLooseFileIsSingleFileMode(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(f, []byte("hi"), 0644); err != nil {
		t.Fatal(err)
	}
	p := New()
	if err := p.Open(f); err != nil {
		t.Fatal(err)
	}
	if p.IsProject {
		t.Error("a loose file must not be treated as a project")
	}
	canonicalRoot, err := fsutil.CanonicalRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.Root != canonicalRoot {
		t.Errorf("Root = %q, want %q", p.Root, canonicalRoot)
	}
}

func TestOpenFailureLeavesProjectUnchanged(t *testing.T) {
	p := projectWithChapters(t, map[string]string{"001_ok.md": "# OK\n"})
	root, current, isProject := p.Root, p.CurrentFile, p.IsProject
	chapters := append([]FileEntry(nil), p.ListChapters()...)

	if err := p.Open(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("Open(missing) succeeded")
	}
	if p.Root != root || p.CurrentFile != current || p.IsProject != isProject || !reflect.DeepEqual(p.ListChapters(), chapters) {
		t.Fatalf("failed Open mutated project: %+v", p)
	}
}

// projectWithChapters writes the given name→content chapters and opens a project.
func projectWithChapters(t *testing.T, chapters map[string]string) *Project {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "chapters")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for name, content := range chapters {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	p := New()
	if err := p.Open(root); err != nil {
		t.Fatal(err)
	}
	return p
}

// 4.1 — project-wide search spans all chapters, is case-insensitive, and reports
// chapter + 1-based line, ordered by chapter then line.
func TestSearchChapters(t *testing.T) {
	p := projectWithChapters(t, map[string]string{
		"001_a.md": "The York gate.\nNothing here.\nyork again.\n",
		"002_b.md": "A quiet harbor.\n",
	})

	matches, err := p.SearchChapters("YORK")
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 2 {
		t.Fatalf("got %d matches, want 2: %+v", len(matches), matches)
	}
	if matches[0].Chapter != "001_a.md" || matches[0].Line != 1 {
		t.Fatalf("first match = %+v, want 001_a.md:1", matches[0])
	}
	if matches[1].Line != 3 {
		t.Fatalf("second match line = %d, want 3", matches[1].Line)
	}
	// A blank pattern matches nothing.
	if m, _ := p.SearchChapters("   "); m != nil {
		t.Fatalf("blank pattern returned %d matches, want 0", len(m))
	}
	// No hit → empty.
	if m, _ := p.SearchChapters("dragon"); len(m) != 0 {
		t.Fatalf("expected no matches, got %d", len(m))
	}
}

// 4.1 — word count totals across chapters with a per-chapter breakdown in order.
func TestWordCount(t *testing.T) {
	p := projectWithChapters(t, map[string]string{
		"001_a.md": "one two three",    // 3
		"002_b.md": "four five\nsix\n", // 3
	})
	total, per := p.WordCount()
	if total != 6 {
		t.Fatalf("total = %d, want 6", total)
	}
	if len(per) != 2 || per[0].Chapter != "001_a.md" || per[0].Words != 3 || per[1].Words != 3 {
		t.Fatalf("per-chapter = %+v", per)
	}
}

// 4.1 — compile concatenates chapters in order into one document and writes it to
// the project root (not chapters/, so a recompile never ingests its own output).
func TestCompileToFile(t *testing.T) {
	p := projectWithChapters(t, map[string]string{
		"001_a.md": "# One\n\nAlpha.\n",
		"002_b.md": "# Two\n\nBeta.\n",
	})

	text, err := p.Compile()
	if err != nil {
		t.Fatal(err)
	}
	if got := indexOrder(text, "One", "Alpha", "Two", "Beta"); !got {
		t.Fatalf("compiled text not in chapter order:\n%s", text)
	}

	path, err := p.CompileToFile()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(path) != p.Root {
		t.Fatalf("compiled to %s, want project root", path)
	}
	onDisk, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(onDisk) != text {
		t.Fatal("on-disk compile differs from Compile() output")
	}
	// The compiled file is not a chapter and must not appear in the chapter list.
	for _, ch := range p.ListChapters() {
		if ch.Name == "manuscript.md" {
			t.Fatal("compiled manuscript leaked into the chapter list")
		}
	}
}

// indexOrder reports whether the substrings appear in the given order in s.
func indexOrder(s string, subs ...string) bool {
	prev := -1
	for _, sub := range subs {
		i := strings.Index(s[prev+1:], sub)
		if i < 0 {
			return false
		}
		prev = prev + 1 + i
	}
	return true
}

// TestChaptersSkipDirectoryNamedMd verifies discovery ignores a directory whose
// name ends in .md.
func TestChaptersSkipDirectoryNamedMd(t *testing.T) {
	root := t.TempDir()
	chapters := filepath.Join(root, "chapters")
	if err := os.MkdirAll(filepath.Join(chapters, "weird.md"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(chapters, "real.md"), []byte("# real\n"), 0644); err != nil {
		t.Fatal(err)
	}
	p := New()
	if err := p.Open(root); err != nil {
		t.Fatal(err)
	}
	chs := p.ListChapters()
	if len(chs) != 1 || chs[0].Name != "real.md" {
		t.Fatalf("expected only real.md, got %v", chs)
	}
}

// 4.1 — cross-file replace counts and rewrites literal matches across chapters.
func TestReplaceInChapters(t *testing.T) {
	root := t.TempDir()
	ch := filepath.Join(root, "chapters")
	if err := os.MkdirAll(ch, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ch, "001_a.md"), []byte("The dragon and the dragon."), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ch, "002_b.md"), []byte("No beasts here."), 0644); err != nil {
		t.Fatal(err)
	}
	p := New()
	if err := p.Open(root); err != nil {
		t.Fatal(err)
	}

	if n, err := p.CountReplacements("dragon"); err != nil || n != 2 {
		t.Fatalf("CountReplacements = %d, %v; want 2", n, err)
	}

	res, err := p.ReplaceInChapters("dragon", "griffin")
	if err != nil {
		t.Fatal(err)
	}
	if res.Replacements != 2 || res.Chapters != 1 {
		t.Fatalf("result = %+v; want 2 replacements in 1 chapter", res)
	}
	got, _ := os.ReadFile(filepath.Join(ch, "001_a.md"))
	if string(got) != "The griffin and the griffin." {
		t.Fatalf("content = %q", got)
	}
}

func newReplaceProject(t *testing.T) (*Project, string, string) {
	t.Helper()
	root := t.TempDir()
	chapters := filepath.Join(root, "chapters")
	if err := os.Mkdir(chapters, 0755); err != nil {
		t.Fatal(err)
	}
	a := filepath.Join(chapters, "001_a.md")
	b := filepath.Join(chapters, "002_b.md")
	if err := os.WriteFile(a, []byte("dragon one"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(b, []byte("dragon two"), 0640); err != nil {
		t.Fatal(err)
	}
	p := New()
	if err := p.Open(root); err != nil {
		t.Fatal(err)
	}
	return p, a, b
}

func replaceFaultAt(phase, base string, fault error) func(string, string) error {
	return func(gotPhase, path string) error {
		if gotPhase == phase && filepath.Base(path) == base {
			return fault
		}
		return nil
	}
}

func assertFileContent(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("%s = %q, want %q", filepath.Base(path), got, want)
	}
}

func TestReplaceInChaptersReadFailureWritesNothing(t *testing.T) {
	p, a, b := newReplaceProject(t)
	p.replaceFault = replaceFaultAt("read", filepath.Base(b), errors.New("injected read"))
	res, err := p.ReplaceInChapters("dragon", "griffin")
	if err == nil || !strings.Contains(err.Error(), "read=failed") || len(res.Files) != 2 {
		t.Fatalf("result/error = %+v, %v", res, err)
	}
	assertFileContent(t, a, "dragon one")
	assertFileContent(t, b, "dragon two")
}

func TestReplaceInChaptersStagingFailureWritesNothing(t *testing.T) {
	p, a, b := newReplaceProject(t)
	p.replaceFault = replaceFaultAt("stage", filepath.Base(b), errors.New("injected stage"))
	res, err := p.ReplaceInChapters("dragon", "griffin")
	if err == nil || !strings.Contains(err.Error(), "stage=failed: injected stage") || len(res.Files) != 2 {
		t.Fatalf("result/error = %+v, %v", res, err)
	}
	assertFileContent(t, a, "dragon one")
	assertFileContent(t, b, "dragon two")
	matches, globErr := filepath.Glob(filepath.Join(filepath.Dir(a), ".*.replace-*"))
	if globErr != nil || len(matches) != 0 {
		t.Fatalf("staging files left behind: %v, %v", matches, globErr)
	}
}

func TestReplaceInChaptersMidCommitRollsBack(t *testing.T) {
	p, a, b := newReplaceProject(t)
	p.replaceFault = replaceFaultAt("commit", filepath.Base(b), errors.New("injected commit"))
	res, err := p.ReplaceInChapters("dragon", "griffin")
	if err == nil || !strings.Contains(err.Error(), "commit=committed rollback=restored") || len(res.Files) != 2 {
		t.Fatalf("result/error = %+v, %v", res, err)
	}
	assertFileContent(t, a, "dragon one")
	assertFileContent(t, b, "dragon two")
	info, statErr := os.Stat(a)
	if statErr != nil {
		t.Fatal(statErr)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("rolled-back mode = %v; want 0600", info.Mode().Perm())
	}
}

func TestReplaceInChaptersReportsRollbackFailure(t *testing.T) {
	p, a, b := newReplaceProject(t)
	p.replaceFault = func(phase, path string) error {
		if phase == "commit" && filepath.Base(path) == filepath.Base(b) {
			return errors.New("injected commit")
		}
		if phase == "rollback" && filepath.Base(path) == filepath.Base(a) {
			return errors.New("injected rollback")
		}
		return nil
	}
	res, err := p.ReplaceInChapters("dragon", "griffin")
	if err == nil || !strings.Contains(err.Error(), "rollback=failed: injected rollback") || len(res.Files) != 2 {
		t.Fatalf("result/error = %+v, %v", res, err)
	}
	assertFileContent(t, a, "griffin one")
	assertFileContent(t, b, "dragon two")
}

func TestReplaceInChaptersTransactionalSuccessPreservesModes(t *testing.T) {
	p, a, b := newReplaceProject(t)
	res, err := p.ReplaceInChapters("dragon", "griffin")
	if err != nil || res.Chapters != 2 || res.Replacements != 2 || len(res.Files) != 2 {
		t.Fatalf("result/error = %+v, %v", res, err)
	}
	assertFileContent(t, a, "griffin one")
	assertFileContent(t, b, "griffin two")
	for path, want := range map[string]os.FileMode{a: 0600, b: 0640} {
		info, statErr := os.Stat(path)
		if statErr != nil {
			t.Fatal(statErr)
		}
		if info.Mode().Perm() != want {
			t.Fatalf("%s mode = %v; want %v", filepath.Base(path), info.Mode().Perm(), want)
		}
	}
}

func TestReplaceInChaptersRevalidatesContainment(t *testing.T) {
	p, a, _ := newReplaceProject(t)
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("dragon outside"), 0644); err != nil {
		t.Fatal(err)
	}
	p.replaceFault = func(phase, path string) error {
		if phase == "read" && filepath.Base(path) == filepath.Base(a) {
			if err := os.Remove(path); err != nil {
				return err
			}
			return os.Symlink(outside, path)
		}
		return nil
	}
	if _, err := p.ReplaceInChapters("dragon", "griffin"); !errors.Is(err, fsutil.ErrUnsafePath) {
		t.Fatalf("ReplaceInChapters() error = %v, want ErrUnsafePath", err)
	}
	assertFileContent(t, outside, "dragon outside")
}
