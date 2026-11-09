package project

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ShayCichocki/marginctl/internal/fsutil"
)

// ChapterMove describes a durable chapter identity change, including its notes
// sidecar. Callers use it to update open buffers, sessions, and navigators.
type ChapterMove struct{ OldPath, NewPath string }

func chapterNotesPath(path string) string {
	return filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+".notes.json")
}

func (p *Project) chapter(path string) (FileEntry, int, error) {
	canonical, err := fsutil.ContainedPath(p.Root, path, false)
	if err != nil {
		return FileEntry{}, -1, err
	}
	for i, ch := range p.chapters {
		if samePath(ch.Path, canonical) {
			return ch, i, nil
		}
	}
	return FileEntry{}, -1, os.ErrNotExist
}

func cleanChapterTitle(name string) (string, error) {
	name, err := fsutil.SanitizeName(name)
	if err != nil {
		return "", err
	}
	name = chapterPrefixRe.ReplaceAllString(strings.TrimSuffix(strings.TrimSuffix(name, ".md"), ".MD"), "")
	if name == "" {
		return "", errors.New("empty chapter title")
	}
	return name, nil
}

// RenameChapter changes a chapter title without changing its ordering slot.
func (p *Project) RenameChapter(path, name string) (ChapterMove, error) {
	ch, idx, err := p.chapter(path)
	if err != nil {
		return ChapterMove{}, err
	}
	title, err := cleanChapterTitle(name)
	if err != nil {
		return ChapterMove{}, err
	}
	prefix := fmt.Sprintf("%03d_", idx+1)
	if match := chapterPrefixRe.FindString(ch.Name); match != "" {
		prefix = match
	}
	return p.moveChapter(ch.Path, filepath.Join(filepath.Dir(ch.Path), prefix+title+".md"))
}

// DuplicateChapter copies a chapter and its notes immediately after the source,
// then normalizes ordering prefixes transactionally. It returns every rename
// performed (the duplicate plus any renumbered existing chapters).
func (p *Project) DuplicateChapter(path, name string) ([]ChapterMove, error) {
	ch, idx, err := p.chapter(path)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(name) == "" {
		name = strings.TrimSuffix(chapterPrefixRe.ReplaceAllString(ch.Name, ""), ".md") + " copy"
	}
	title, err := cleanChapterTitle(name)
	if err != nil {
		return nil, err
	}
	tmp := filepath.Join(filepath.Dir(ch.Path), title+".md")
	// Never overwrite an existing file (for example an unprefixed chapter added
	// by hand): the failure cleanup below would otherwise delete it too.
	if _, statErr := os.Lstat(tmp); statErr == nil {
		return nil, os.ErrExist
	} else if !os.IsNotExist(statErr) {
		return nil, statErr
	}
	data, err := fsutil.ReadFileUnderRoot(p.Root, ch.Path)
	if err != nil {
		return nil, err
	}
	if err = fsutil.WriteFileUnderRoot(p.Root, tmp, data, 0644, false); err != nil {
		return nil, err
	}
	// Copy the notes sidecar only when it is a regular file we wrote; never
	// follow a planted symlink at the sidecar path into an arbitrary file.
	notesSrc := chapterNotesPath(ch.Path)
	if fi, lerr := os.Lstat(notesSrc); lerr == nil && fi.Mode().IsRegular() {
		if notes, readErr := os.ReadFile(notesSrc); readErr == nil {
			if err = fsutil.WriteFilePrivate(chapterNotesPath(tmp), notes); err != nil {
				_ = os.Remove(tmp)
				return nil, err
			}
		}
	}
	p.chapters = append(p.chapters, FileEntry{})
	copy(p.chapters[idx+2:], p.chapters[idx+1:])
	p.chapters[idx+1] = FileEntry{Name: filepath.Base(tmp), Path: tmp}
	moves, err := p.normalizeOrder()
	if err != nil {
		_ = os.Remove(tmp)
		_ = os.Remove(chapterNotesPath(tmp))
		p.resyncChapters()
		return nil, err
	}
	for _, move := range moves {
		if samePath(move.OldPath, tmp) {
			return moves, nil
		}
	}
	return nil, errors.New("duplicate ordering failed")
}

// InsertChapter creates a chapter at the zero-based position. It returns every
// rename performed (the new file plus any renumbered existing chapters).
func (p *Project) InsertChapter(index int, name string) ([]ChapterMove, error) {
	title, err := cleanChapterTitle(name)
	if err != nil {
		return nil, err
	}
	if index < 0 {
		index = 0
	}
	if index > len(p.chapters) {
		index = len(p.chapters)
	}
	tmp := filepath.Join(p.Root, "chapters", title+".md")
	if _, statErr := os.Lstat(tmp); statErr == nil {
		return nil, os.ErrExist
	} else if !os.IsNotExist(statErr) {
		return nil, statErr
	}
	if err = fsutil.WriteFileUnderRoot(p.Root, tmp, []byte("# "+title+"\n\n"), 0644, false); err != nil {
		return nil, err
	}
	p.chapters = append(p.chapters, FileEntry{})
	copy(p.chapters[index+1:], p.chapters[index:])
	p.chapters[index] = FileEntry{Name: filepath.Base(tmp), Path: tmp}
	moves, err := p.normalizeOrder()
	if err != nil {
		_ = os.Remove(tmp)
		p.resyncChapters()
		return nil, err
	}
	for _, move := range moves {
		if samePath(move.OldPath, tmp) {
			// Report every rename (existing chapters shift too), including the new file's.
			return moves, nil
		}
	}
	return nil, errors.New("insert ordering failed")
}

// ReorderChapter moves a chapter to a zero-based position and rewrites ordering
// prefixes as one rollback-safe rename transaction.
func (p *Project) ReorderChapter(path string, index int) ([]ChapterMove, error) {
	_, old, err := p.chapter(path)
	if err != nil {
		return nil, err
	}
	if index < 0 {
		index = 0
	}
	if index >= len(p.chapters) {
		index = len(p.chapters) - 1
	}
	ch := p.chapters[old]
	p.chapters = append(p.chapters[:old], p.chapters[old+1:]...)
	p.chapters = append(p.chapters, FileEntry{})
	copy(p.chapters[index+1:], p.chapters[index:])
	p.chapters[index] = ch
	moves, err := p.normalizeOrder()
	if err != nil {
		p.resyncChapters()
		return nil, err
	}
	return moves, nil
}

// resyncChapters rebuilds the in-memory chapter list from disk after a failed
// transaction so it cannot keep an unapplied order or a removed temp chapter.
// Best effort: if the directory is unreadable the failure is already being
// reported to the caller.
func (p *Project) resyncChapters() { _ = p.refreshChapters() }

func (p *Project) normalizeOrder() ([]ChapterMove, error) {
	type staged struct{ old, tmp, final string }
	items := make([]staged, len(p.chapters))
	for i, ch := range p.chapters {
		title := chapterPrefixRe.ReplaceAllString(strings.TrimSuffix(ch.Name, ".md"), "")
		items[i] = staged{ch.Path, filepath.Join(filepath.Dir(ch.Path), fmt.Sprintf(".marginctl-order-%d-%s", i, ch.Name)), filepath.Join(filepath.Dir(ch.Path), fmt.Sprintf("%03d_%s.md", i+1, title))}
		if _, err := os.Stat(items[i].final); err == nil && !samePath(items[i].final, ch.Path) {
			// Existing chapter targets are safe because every chapter is staged first.
			found := false
			for _, x := range p.chapters {
				found = found || samePath(x.Path, items[i].final)
			}
			if !found {
				return nil, os.ErrExist
			}
		}
	}
	stagedCount := 0
	for i := range items {
		if err := renamePair(items[i].old, items[i].tmp); err != nil {
			for j := stagedCount - 1; j >= 0; j-- {
				_ = renamePair(items[j].tmp, items[j].old)
			}
			return nil, err
		}
		stagedCount++
	}
	committed := 0
	for i := range items {
		if err := renamePair(items[i].tmp, items[i].final); err != nil {
			for j := committed - 1; j >= 0; j-- {
				_ = renamePair(items[j].final, items[j].old)
			}
			for j := i; j < len(items); j++ {
				_ = renamePair(items[j].tmp, items[j].old)
			}
			return nil, err
		}
		committed++
	}
	moves := make([]ChapterMove, len(items))
	for i, x := range items {
		moves[i] = ChapterMove{x.old, x.final}
	}
	_ = p.refreshChapters()
	return moves, nil
}

func renamePair(old, next string) error {
	if err := os.Rename(old, next); err != nil {
		return err
	}
	oldNotes, nextNotes := chapterNotesPath(old), chapterNotesPath(next)
	if _, err := os.Stat(oldNotes); err == nil {
		if err = os.Rename(oldNotes, nextNotes); err != nil {
			if rbErr := os.Rename(next, old); rbErr != nil {
				// The chapter body is now split from its sidecar; say so.
				return errors.Join(err, fmt.Errorf("rollback of %s failed: %w", filepath.Base(next), rbErr))
			}
			return err
		}
	}
	return nil
}

func (p *Project) moveChapter(old, next string) (ChapterMove, error) {
	if _, err := fsutil.ContainedPath(p.Root, next, true); err != nil {
		return ChapterMove{}, err
	}
	if _, err := os.Stat(next); err == nil {
		return ChapterMove{}, os.ErrExist
	}
	if err := os.MkdirAll(filepath.Dir(next), 0755); err != nil {
		return ChapterMove{}, err
	}
	if err := renamePair(old, next); err != nil {
		return ChapterMove{}, err
	}
	_ = p.refreshChapters()
	return ChapterMove{old, next}, nil
}

func (p *Project) ArchiveChapter(path string) (ChapterMove, error) {
	ch, _, e := p.chapter(path)
	if e != nil {
		return ChapterMove{}, e
	}
	return p.moveChapter(ch.Path, filepath.Join(p.Root, "archive", ch.Name))
}
func (p *Project) TrashChapter(path string) (ChapterMove, error) {
	ch, _, e := p.chapter(path)
	if e != nil {
		return ChapterMove{}, e
	}
	return p.moveChapter(ch.Path, filepath.Join(p.Root, ".trash", "chapters", ch.Name))
}
func (p *Project) RestoreChapter(path string) (ChapterMove, error) {
	canonical, err := fsutil.ContainedPath(p.Root, path, false)
	if err != nil {
		return ChapterMove{}, err
	}
	if !strings.HasPrefix(canonical+string(os.PathSeparator), filepath.Join(p.Root, "archive")+string(os.PathSeparator)) && !strings.HasPrefix(canonical+string(os.PathSeparator), filepath.Join(p.Root, ".trash", "chapters")+string(os.PathSeparator)) {
		return ChapterMove{}, errors.New("chapter is not archived or trashed")
	}
	return p.moveChapter(canonical, filepath.Join(p.Root, "chapters", filepath.Base(canonical)))
}
