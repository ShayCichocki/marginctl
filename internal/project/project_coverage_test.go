package project

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/ShayCichocki/marginctl/internal/fsutil"
)

// covSnapshot records every entry (relative path -> kind/mode/content) under
// root so a test can assert a failed operation changed nothing at all.
func covSnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		info, err := os.Lstat(path)
		if err != nil {
			return err
		}
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, _ := os.Readlink(path)
			out[rel] = "link->" + target
		case info.IsDir():
			out[rel] = fmt.Sprintf("dir %v", info.Mode().Perm())
		default:
			data, _ := os.ReadFile(path)
			out[rel] = fmt.Sprintf("file %v %q", info.Mode().Perm(), data)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func covDiff(a, b map[string]string) string {
	var diffs []string
	for k, v := range a {
		if b[k] != v {
			diffs = append(diffs, fmt.Sprintf("%s: %q -> %q", k, v, b[k]))
		}
	}
	for k, v := range b {
		if _, ok := a[k]; !ok {
			diffs = append(diffs, fmt.Sprintf("%s: (absent) -> %q", k, v))
		}
	}
	sort.Strings(diffs)
	return strings.Join(diffs, "\n")
}

func covAssertUnchanged(t *testing.T, root string, before map[string]string) {
	t.Helper()
	if d := covDiff(before, covSnapshot(t, root)); d != "" {
		t.Fatalf("filesystem changed after failed operation:\n%s", d)
	}
}

func covNames(p *Project) []string {
	var n []string
	for _, c := range p.ListChapters() {
		n = append(n, c.Name)
	}
	return n
}

func covAssertNames(t *testing.T, p *Project, want []string) {
	t.Helper()
	if got := covNames(p); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("in-memory chapters = %v, want %v", got, want)
	}
}

func covWrite(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

// covBlock plants a non-empty directory at path so any rename of a file onto
// it fails, giving deterministic fault injection at an exact position.
func covBlock(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(path, "keep"), 0755); err != nil {
		t.Fatal(err)
	}
}

func TestCovDuplicateCopiesNotesPrivately(t *testing.T) {
	p := workflowProject(t)
	src := p.ListChapters()[0].Path
	covWrite(t, chapterNotesPath(src), `["note"]`, 0644)
	mvs, err := p.DuplicateChapter(src, "Echo")
	if err != nil {
		t.Fatal(err)
	}
	mv := covDupMove(t, mvs)
	covAssertNames(t, p, []string{"001_One.md", "002_Echo.md", "003_Two.md"})
	info, err := os.Stat(chapterNotesPath(mv.NewPath))
	if err != nil {
		t.Fatal("notes not copied:", err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("copied notes mode = %v, want 0600", info.Mode().Perm())
	}
	if b, _ := os.ReadFile(chapterNotesPath(mv.NewPath)); string(b) != `["note"]` {
		t.Fatalf("copied notes = %q", b)
	}
	if b, _ := os.ReadFile(chapterNotesPath(src)); string(b) != `["note"]` {
		t.Fatalf("source notes disturbed: %q", b)
	}
	if b, _ := os.ReadFile(mv.NewPath); string(b) != "one disk\n" {
		t.Fatalf("copied body = %q", b)
	}
}

func TestCovDuplicateDefaultNameForBlank(t *testing.T) {
	p := workflowProject(t)
	mvs, err := p.DuplicateChapter(p.ListChapters()[1].Path, "  ")
	if err != nil {
		t.Fatal(err)
	}
	mv := covDupMove(t, mvs)
	if filepath.Base(mv.NewPath) != "003_Two copy.md" {
		t.Fatalf("default copy name = %s", mv.NewPath)
	}
}

func TestCovDuplicateDoesNotFollowSymlinkedSidecar(t *testing.T) {
	p := workflowProject(t)
	src := p.ListChapters()[0].Path
	outside := filepath.Join(t.TempDir(), "secret.json")
	covWrite(t, outside, "SECRET", 0644)
	if err := os.Symlink(outside, chapterNotesPath(src)); err != nil {
		t.Skip("symlinks unsupported:", err)
	}
	mvs, err := p.DuplicateChapter(src, "Echo")
	if err != nil {
		t.Fatal(err)
	}
	mv := covDupMove(t, mvs)
	if _, err := os.Lstat(chapterNotesPath(mv.NewPath)); err == nil {
		t.Fatal("symlinked sidecar was copied into the duplicate")
	}
	if b, _ := os.ReadFile(outside); string(b) != "SECRET" {
		t.Fatalf("outside file modified: %q", b)
	}
}

func TestCovDuplicateRefusesNameCollisionWithoutOverwrite(t *testing.T) {
	p := workflowProject(t)
	// An unprefixed chapter added by hand: a duplicate titled "Hand" resolves
	// to chapters/Hand.md and must not clobber it.
	covWrite(t, filepath.Join(p.Root, "chapters", "Hand.md"), "precious", 0644)
	if err := p.Refresh(); err != nil {
		t.Fatal(err)
	}
	before := covSnapshot(t, p.Root)
	names := covNames(p)
	if _, err := p.DuplicateChapter(p.ListChapters()[0].Path, "Hand"); err == nil {
		t.Fatal("duplicate onto an existing chapter name succeeded")
	}
	covAssertUnchanged(t, p.Root, before)
	covAssertNames(t, p, names)
}

func TestCovDuplicateOrderingFailureLeavesNothingBehind(t *testing.T) {
	p := workflowProject(t)
	src := p.ListChapters()[0].Path
	covWrite(t, chapterNotesPath(src), `["n"]`, 0600)
	covBlock(t, filepath.Join(p.Root, "chapters", "002_Echo.md"))
	before := covSnapshot(t, p.Root)
	names := covNames(p)
	if _, err := p.DuplicateChapter(src, "Echo"); err == nil {
		t.Fatal("expected ordering failure")
	}
	covAssertUnchanged(t, p.Root, before)
	covAssertNames(t, p, names)
}

func TestCovDuplicateSidecarWriteFailureRemovesCopiedChapter(t *testing.T) {
	p := workflowProject(t)
	src := p.ListChapters()[0].Path
	covWrite(t, chapterNotesPath(src), `["n"]`, 0600)
	// The duplicate's sidecar path is blocked, so writing it fails after the
	// chapter body was already copied (disproves the suspected temp leak).
	covBlock(t, chapterNotesPath(filepath.Join(p.Root, "chapters", "Echo.md")))
	before := covSnapshot(t, p.Root)
	if _, err := p.DuplicateChapter(src, "Echo"); err == nil {
		t.Fatal("expected sidecar failure")
	}
	covAssertUnchanged(t, p.Root, before)
}

func TestCovArchiveMovesChapterAndNotes(t *testing.T) {
	p := workflowProject(t)
	one := p.ListChapters()[0].Path
	covWrite(t, chapterNotesPath(one), `["n"]`, 0600)
	mv, err := p.ArchiveChapter(one)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(mv.NewPath) != filepath.Join(p.Root, "archive") {
		t.Fatalf("archived to %s", mv.NewPath)
	}
	if _, err := os.Stat(one); !os.IsNotExist(err) {
		t.Fatal("source still present")
	}
	if _, err := os.Stat(chapterNotesPath(one)); !os.IsNotExist(err) {
		t.Fatal("source sidecar still present")
	}
	if b, _ := os.ReadFile(chapterNotesPath(mv.NewPath)); string(b) != `["n"]` {
		t.Fatalf("archived sidecar = %q", b)
	}
	covAssertNames(t, p, []string{"002_Two.md"})
}

func TestCovArchiveCollisionChangesNothing(t *testing.T) {
	p := workflowProject(t)
	one := p.ListChapters()[0].Path
	if err := os.MkdirAll(filepath.Join(p.Root, "archive"), 0755); err != nil {
		t.Fatal(err)
	}
	covWrite(t, filepath.Join(p.Root, "archive", "001_One.md"), "older", 0644)
	before := covSnapshot(t, p.Root)
	if _, err := p.ArchiveChapter(one); !errors.Is(err, os.ErrExist) {
		t.Fatalf("err = %v, want ErrExist", err)
	}
	covAssertUnchanged(t, p.Root, before)
}

func TestCovRetrashSameNameFailsCleanly(t *testing.T) {
	p := workflowProject(t)
	one := p.ListChapters()[0].Path
	if _, err := p.TrashChapter(one); err != nil {
		t.Fatal(err)
	}
	// A new chapter with the same file name appears, then is trashed too.
	covWrite(t, one, "second life", 0644)
	if err := p.Refresh(); err != nil {
		t.Fatal(err)
	}
	before := covSnapshot(t, p.Root)
	if _, err := p.TrashChapter(one); !errors.Is(err, os.ErrExist) {
		t.Fatalf("err = %v, want ErrExist", err)
	}
	covAssertUnchanged(t, p.Root, before)
}

func TestCovTrashAndArchiveRejectUnknownPath(t *testing.T) {
	p := workflowProject(t)
	if _, err := p.TrashChapter(filepath.Join(p.Root, "chapters", "nope.md")); err == nil {
		t.Fatal("trash of missing chapter succeeded")
	}
	if _, err := p.ArchiveChapter(filepath.Join(p.Root, "..", "x.md")); err == nil {
		t.Fatal("archive of outside path succeeded")
	}
}

func TestCovRestoreRejectsPathsOutsideArchiveAndTrash(t *testing.T) {
	p := workflowProject(t)
	evil := filepath.Join(p.Root, "archive-evil")
	trashLike := filepath.Join(p.Root, ".trash", "chapters-evil")
	for _, d := range []string{evil, trashLike} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	covWrite(t, filepath.Join(evil, "x.md"), "x", 0644)
	covWrite(t, filepath.Join(trashLike, "y.md"), "y", 0644)
	covWrite(t, filepath.Join(p.Root, "loose.md"), "z", 0644)
	candidates := []string{
		filepath.Join(evil, "x.md"),
		filepath.Join(trashLike, "y.md"),
		filepath.Join(p.Root, "loose.md"),
		p.ListChapters()[0].Path,
		filepath.Join(p.Root, "..", "escape.md"),
	}
	before := covSnapshot(t, p.Root)
	for _, c := range candidates {
		if _, err := p.RestoreChapter(c); err == nil {
			t.Errorf("RestoreChapter(%s) succeeded", c)
		}
	}
	covAssertUnchanged(t, p.Root, before)
}

func TestCovRestoreRejectsSymlinkedCandidate(t *testing.T) {
	p := workflowProject(t)
	if err := os.MkdirAll(filepath.Join(p.Root, "archive"), 0755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "o.md")
	covWrite(t, outside, "outside", 0644)
	link := filepath.Join(p.Root, "archive", "linked.md")
	if err := os.Symlink(outside, link); err != nil {
		t.Skip("symlinks unsupported:", err)
	}
	if _, err := p.RestoreChapter(link); !errors.Is(err, fsutil.ErrUnsafePath) {
		t.Fatalf("err = %v, want ErrUnsafePath", err)
	}
	if _, err := os.Lstat(filepath.Join(p.Root, "chapters", "linked.md")); err == nil {
		t.Fatal("symlink was restored into chapters")
	}
}

func TestCovRestoreFromArchiveRoundTrip(t *testing.T) {
	p := workflowProject(t)
	one := p.ListChapters()[0].Path
	covWrite(t, chapterNotesPath(one), `["n"]`, 0600)
	arch, err := p.ArchiveChapter(one)
	if err != nil {
		t.Fatal(err)
	}
	back, err := p.RestoreChapter(arch.NewPath)
	if err != nil {
		t.Fatal(err)
	}
	if back.NewPath != one {
		t.Fatalf("restored to %s, want %s", back.NewPath, one)
	}
	if b, _ := os.ReadFile(chapterNotesPath(one)); string(b) != `["n"]` {
		t.Fatalf("sidecar lost: %q", b)
	}
	covAssertNames(t, p, []string{"001_One.md", "002_Two.md"})
}

func TestCovRestoreCollisionChangesNothing(t *testing.T) {
	p := workflowProject(t)
	one := p.ListChapters()[0].Path
	arch, err := p.ArchiveChapter(one)
	if err != nil {
		t.Fatal(err)
	}
	covWrite(t, one, "new occupant", 0644)
	before := covSnapshot(t, p.Root)
	if _, err := p.RestoreChapter(arch.NewPath); !errors.Is(err, os.ErrExist) {
		t.Fatalf("err = %v, want ErrExist", err)
	}
	covAssertUnchanged(t, p.Root, before)
}

func TestCovReorderStagingFailureRollsBackEverything(t *testing.T) {
	p := workflowProject(t)
	ch := p.ListChapters()
	covWrite(t, chapterNotesPath(ch[0].Path), `["a"]`, 0600)
	covWrite(t, chapterNotesPath(ch[1].Path), `["b"]`, 0600)
	// Staging chapter #1 moves its body, then fails on the sidecar: renamePair
	// must put the body back and normalizeOrder must unwind chapter #0.
	covBlock(t, chapterNotesPath(filepath.Join(p.Root, "chapters", ".marginctl-order-1-001_One.md")))
	before := covSnapshot(t, p.Root)
	names := covNames(p)
	if _, err := p.ReorderChapter(ch[1].Path, 0); err == nil {
		t.Fatal("expected failure")
	}
	covAssertUnchanged(t, p.Root, before)
	covAssertNames(t, p, names)
}

func TestCovReorderCommitFailureRollsBackEverything(t *testing.T) {
	p := workflowProject(t)
	ch := p.ListChapters()
	covWrite(t, chapterNotesPath(ch[0].Path), `["a"]`, 0600)
	covWrite(t, chapterNotesPath(ch[1].Path), `["b"]`, 0600)
	// After the swap, One becomes 002_One.md; block its sidecar destination.
	covBlock(t, chapterNotesPath(filepath.Join(p.Root, "chapters", "002_One.md")))
	before := covSnapshot(t, p.Root)
	names := covNames(p)
	if _, err := p.ReorderChapter(ch[1].Path, 0); err == nil {
		t.Fatal("expected failure")
	}
	covAssertUnchanged(t, p.Root, before)
	covAssertNames(t, p, names)
}

func TestCovInsertWithPreexistingTargetAtEachPosition(t *testing.T) {
	for k := 0; k < 3; k++ {
		t.Run(fmt.Sprint(k), func(t *testing.T) {
			p := workflowProject(t)
			if _, err := p.InsertChapter(2, "Three"); err != nil {
				t.Fatal(err)
			}
			// Inserting "Zed" at k gives it slot k+1; block exactly that name.
			covBlock(t, filepath.Join(p.Root, "chapters", fmt.Sprintf("%03d_Zed.md", k+1)))
			before := covSnapshot(t, p.Root)
			names := covNames(p)
			if _, err := p.InsertChapter(k, "Zed"); !errors.Is(err, os.ErrExist) {
				t.Fatalf("err = %v, want ErrExist", err)
			}
			covAssertUnchanged(t, p.Root, before)
			covAssertNames(t, p, names)
		})
	}
}

func TestCovNormalizeOrderReadOnlyDirChangesNothing(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	p := workflowProject(t)
	ch := p.ListChapters()
	covWrite(t, chapterNotesPath(ch[0].Path), `["a"]`, 0600)
	dir := filepath.Join(p.Root, "chapters")
	if err := os.Chmod(dir, 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0755) })
	if _, err := p.ReorderChapter(ch[1].Path, 0); err == nil {
		t.Fatal("expected failure in read-only dir")
	}
	if err := os.Chmod(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for k := range covSnapshot(t, p.Root) {
		if strings.Contains(k, "marginctl-order") || strings.Contains(k, ".tmp-") {
			t.Fatalf("leftover temp: %s", k)
		}
	}
	covAssertNames(t, p, []string{"001_One.md", "002_Two.md"})
}

func TestCovRenameChapterKeepsSlotAndRefusesCollision(t *testing.T) {
	p := workflowProject(t)
	one := p.ListChapters()[0].Path
	mv, err := p.RenameChapter(one, "Renamed.md")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(mv.NewPath) != "001_Renamed.md" {
		t.Fatalf("renamed to %s", mv.NewPath)
	}
	covWrite(t, filepath.Join(p.Root, "chapters", "001_Taken.md"), "t", 0644)
	if err := p.Refresh(); err != nil {
		t.Fatal(err)
	}
	snap := covSnapshot(t, p.Root)
	if _, err := p.RenameChapter(mv.NewPath, "Taken"); !errors.Is(err, os.ErrExist) {
		t.Fatalf("err = %v, want ErrExist", err)
	}
	covAssertUnchanged(t, p.Root, snap)
}

func TestCovListRefreshAndPathHelpers(t *testing.T) {
	p := workflowProject(t)
	root := p.Root
	covWrite(t, filepath.Join(root, "notes.md"), "n", 0644)
	covWrite(t, filepath.Join(root, "skip.txt"), "n", 0644)
	covWrite(t, filepath.Join(root, ".hidden.md"), "n", 0644)
	if err := os.Mkdir(filepath.Join(root, "zdir"), 0755); err != nil {
		t.Fatal(err)
	}
	_ = os.Symlink(root, filepath.Join(root, "link")) // must be skipped when supported
	if err := p.Refresh(); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range p.ListFiles() {
		names = append(names, e.Name)
	}
	if got := strings.Join(names, ","); got != "chapters,zdir,notes.md" {
		t.Fatalf("ListFiles = %s (dirs first; no hidden/symlink/non-md)", got)
	}
	// An externally added chapter appears only after Refresh.
	covWrite(t, filepath.Join(root, "chapters", "003_Three.md"), "3", 0644)
	if len(p.ListChapters()) != 2 {
		t.Fatal("chapters changed without Refresh")
	}
	if err := p.Refresh(); err != nil {
		t.Fatal(err)
	}
	if len(p.ListChapters()) != 3 {
		t.Fatalf("chapters after Refresh = %v", covNames(p))
	}

	ch := filepath.Join(root, "chapters", "001_One.md")
	if got := p.RelativePath(ch); got != filepath.Join("chapters", "001_One.md") {
		t.Fatalf("RelativePath = %s", got)
	}
	if got := p.ChapterRelativePath(ch); got != "001_One.md" {
		t.Fatalf("ChapterRelativePath = %s", got)
	}
	p.SetCurrentFile(ch)
	if p.CurrentFile != ch {
		t.Fatalf("CurrentFile = %s", p.CurrentFile)
	}
	if got := (&Project{}).RelativePath("/x/y"); got != "/x/y" {
		t.Fatalf("empty-root RelativePath = %s", got)
	}
}

func TestCovRefreshDetectsRemovedChaptersDir(t *testing.T) {
	p := workflowProject(t)
	if err := os.RemoveAll(filepath.Join(p.Root, "chapters")); err != nil {
		t.Fatal(err)
	}
	if err := p.Refresh(); err == nil {
		t.Fatal("Refresh should report the missing chapters directory")
	}
}

// Duplicating a chapter renumbers unprefixed chapters, including the source;
// every rename must be reported so callers can rebind an open chapter.
func TestDuplicateChapterReportsSourceRenumber(t *testing.T) {
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "chapters"), 0755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"alpha.md", "beta.md"} {
		covWrite(t, filepath.Join(root, "chapters", n), n, 0644)
	}
	p := New()
	if err := p.Open(root); err != nil {
		t.Fatal(err)
	}
	src := p.ListChapters()[0].Path
	moves, err := p.DuplicateChapter(src, "Echo")
	if err != nil {
		t.Fatal(err)
	}
	var srcNew string
	for _, mv := range moves {
		if samePath(mv.OldPath, src) {
			srcNew = mv.NewPath
		}
	}
	if filepath.Base(srcNew) != "001_alpha.md" {
		t.Fatalf("source renumbering not reported: %v", moves)
	}
}

// covDupMove picks the duplicate's own rename (its pre-ordering name carries no
// numeric prefix) out of the full move list DuplicateChapter returns.
func covDupMove(t *testing.T, moves []ChapterMove) ChapterMove {
	t.Helper()
	for _, mv := range moves {
		if !chapterPrefixRe.MatchString(filepath.Base(mv.OldPath)) {
			return mv
		}
	}
	t.Fatalf("no duplicate move in %v", moves)
	return ChapterMove{}
}
