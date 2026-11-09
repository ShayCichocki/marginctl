package app

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ShayCichocki/marginctl/internal/project"
	"github.com/ShayCichocki/marginctl/internal/session"
)

func (m *Model) chapterLifecycleCommand(verb, arg string) tea.Cmd {
	if !m.project.IsProject {
		m.setErrorNotice("Not in a project")
		return nil
	}
	if m.editor.Dirty {
		m.setErrorNotice("Unsaved changes; save before changing chapter structure")
		return nil
	}
	current := m.project.CurrentFile
	var move project.ChapterMove
	var moves []project.ChapterMove
	var err error
	switch verb {
	case "ChapterRename":
		if arg == "" {
			err = errors.New("usage: :ChapterRename <name>")
		} else {
			move, err = m.project.RenameChapter(current, arg)
			moves = []project.ChapterMove{move}
		}
	case "ChapterDuplicate":
		moves, err = m.project.DuplicateChapter(current, arg)
	case "ChapterArchive":
		move, err = m.project.ArchiveChapter(current)
		moves = []project.ChapterMove{move}
	case "ChapterTrash":
		move, err = m.project.TrashChapter(current)
		moves = []project.ChapterMove{move}
	case "ChapterRestore":
		if arg == "" {
			err = errors.New("usage: :ChapterRestore <project-relative path>")
		} else {
			move, err = m.project.RestoreChapter(filepath.Join(m.project.Root, arg))
			moves = []project.ChapterMove{move}
		}
	case "ChapterReorder":
		var pos int
		if _, e := fmt.Sscanf(arg, "%d", &pos); e != nil || pos < 1 {
			err = errors.New("usage: :ChapterReorder <position>")
		} else {
			moves, err = m.project.ReorderChapter(current, pos-1)
		}
	case "ChapterInsert":
		var pos int
		if _, e := fmt.Sscanf(arg, "%d", &pos); e != nil || pos < 1 {
			err = errors.New("usage: :ChapterInsert <position> <name>")
		} else {
			name := strings.TrimSpace(strings.TrimPrefix(arg, fmt.Sprintf("%d", pos)))
			moves, err = m.project.InsertChapter(pos-1, name)
		}
	}
	if err != nil {
		m.err = err
		return nil
	}
	// Resolve the open chapter from its ORIGINAL path in a single lookup: moves
	// are simultaneous renames, and chaining them (a later move whose OldPath
	// equals an earlier NewPath) would slide the buffer onto another chapter.
	original := current
	for _, mv := range moves {
		if sameFile(original, mv.OldPath) {
			current = mv.NewPath
			// Only the open chapter's remembered path is rewritten, and only
			// once: rewriting every move sequentially would chain in the store.
			_ = session.RewriteFilePath(m.sessionKey(), mv.OldPath, mv.NewPath)
		}
	}
	if verb == "ChapterArchive" || verb == "ChapterTrash" {
		_ = session.RewriteFilePath(m.sessionKey(), current, "")
		m.project.SetCurrentFile("")
		m.editor.SetContent("")
		m.editor.SetNotes(nil)
	} else if current != "" && current != m.project.CurrentFile {
		m.project.SetCurrentFile(current)
		if err = m.loadIntoEditor(current); err != nil {
			m.err = err
		}
	}
	m.refreshProjectNav()
	m.saveSession()
	m.setNotice("Chapter operation complete")
	return nil
}

func sameFile(a, b string) bool {
	aa, _ := filepath.Abs(a)
	bb, _ := filepath.Abs(b)
	return filepath.Clean(aa) == filepath.Clean(bb)
}
