package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ShayCichocki/marginctl/internal/command"
	"github.com/ShayCichocki/marginctl/internal/editor"
	"github.com/ShayCichocki/marginctl/internal/fsutil"
)

// notesPath returns the hidden sidecar that stores a file's range-anchored notes
// (e.g. chapters/001.md → chapters/.001.md.notes.json). Empty for an unnamed
// buffer. The dotfile name keeps it out of chapter/state discovery.
func notesPath(file string) string {
	if file == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(file), "."+filepath.Base(file)+".notes.json")
}

// saveNotes writes the current buffer's notes to its sidecar (removing the file
// when there are none). Best-effort: a write error surfaces on the bottom bar.
func (m *Model) saveNotes() {
	path := notesPath(m.project.CurrentFile)
	if path == "" {
		return
	}
	notes := m.editor.Notes()
	if len(notes) == 0 {
		_ = os.Remove(path)
		return
	}
	data, err := json.MarshalIndent(notes, "", "  ")
	if err != nil {
		m.err = err
		return
	}
	if err := fsutil.WriteFilePrivate(path, data); err != nil {
		m.err = err
	}
}

// loadNotes reads a file's notes sidecar into the editor (clearing notes when
// there is none). A corrupt sidecar is treated as "no notes" rather than fatal.
func (m *Model) loadNotes(file string) {
	path := notesPath(file)
	if path == "" {
		m.editor.SetNotes(nil)
		return
	}
	// A notes sidecar must be a regular file we wrote; never follow a planted
	// symlink into an arbitrary file (same guard as swap recovery).
	if fi, lerr := os.Lstat(path); lerr != nil || !fi.Mode().IsRegular() {
		m.editor.SetNotes(nil)
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		m.editor.SetNotes(nil)
		return
	}
	var notes []editor.Note
	if err := json.Unmarshal(data, &notes); err != nil {
		m.editor.SetNotes(nil)
		return
	}
	m.editor.SetNotes(notes)
}

func (m *Model) cmdNoteEdit(_, arg string) tea.Cmd {
	var n int
	if _, err := fmt.Sscanf(arg, "%d", &n); err != nil || !m.editor.EditNote(n-1, strings.TrimSpace(strings.TrimPrefix(arg, fmt.Sprintf("%d", n)))) {
		m.setErrorNotice("usage: :note-edit <number> <text>")
	} else {
		m.saveNotes()
		m.setNotice("Note updated")
	}
	return nil
}

func (m *Model) cmdNoteResolve(_, arg string) tea.Cmd {
	var n int
	if _, err := fmt.Sscanf(arg, "%d", &n); err != nil || !m.editor.ResolveNote(n-1, true) {
		m.setErrorNotice("usage: :note-resolve <number>")
	} else {
		m.saveNotes()
		m.setNotice("Note resolved")
	}
	return nil
}

func (m *Model) cmdNoteDelete(_, arg string) tea.Cmd {
	var n int
	if _, err := fmt.Sscanf(arg, "%d", &n); err != nil || !m.editor.DeleteNote(n-1) {
		m.setErrorNotice("usage: :note-delete <number>")
	} else {
		m.saveNotes()
		m.setNotice("Note deleted")
	}
	return nil
}

// addNoteCommand attaches a note to the current Visual selection. Use it from
// Visual mode (":" keeps the selection): `:note the comment text`.
func (m *Model) addNoteCommand(body string) tea.Cmd {
	body = strings.TrimSpace(body)
	if body == "" {
		m.setErrorNotice("usage: (select text in Visual mode, then) :note <comment>")
		return nil
	}
	if m.editor.Mode != editor.ModeVisual {
		m.setErrorNotice("Select text in Visual mode first, then :note <comment>")
		return nil
	}
	if !m.editor.AddNote(body) {
		m.setErrorNotice("No selection to attach the note to")
		return nil
	}
	m.saveNotes()
	m.setNotice("Note added")
	return nil
}

// listNotesCommand shows the current file's notes in the results pane, jumpable
// with Enter (reuses the search-list machinery).
func (m *Model) listNotesCommand(filters ...string) tea.Cmd {
	filter := ""
	if len(filters) > 0 {
		filter = filters[0]
	}
	notes := m.editor.Notes()
	if len(notes) == 0 {
		m.setNotice("No notes in this file")
		return nil
	}
	items := make([]string, 0, len(notes)+1)
	targets := make([]command.SearchTarget, 0, len(notes)+1)
	items = append(items, fmt.Sprintf("%d note(s) — Enter to jump", len(notes)))
	targets = append(targets, command.SearchTarget{})
	for i, n := range notes {
		if filter == "open" && n.Resolved || filter == "resolved" && !n.Resolved || filter == "orphans" && !n.Orphaned {
			continue
		}
		tag := ""
		if n.Orphaned {
			tag = " [orphaned]"
		}
		if n.Resolved {
			tag += " [resolved]"
		}
		items = append(items, fmt.Sprintf("%d L%d%s  %s", i+1, n.Row+1, tag, n.Body))
		targets = append(targets, command.SearchTarget{Path: m.project.CurrentFile, Line: n.Row + 1})
	}
	m.results.Show(command.Result{Type: command.ResultList, Items: items, SearchTargets: targets})
	m.focus = FocusResults
	m.editor.Blur()
	m.updateLayout()
	m.updateStatusBar()
	return nil
}

func (m *Model) listProjectNotesCommand(filter string) tea.Cmd {
	if !m.project.IsProject {
		m.setErrorNotice("Not in a project")
		return nil
	}
	var items []string
	var targets []command.SearchTarget
	for _, ch := range m.project.ListChapters() {
		data, err := os.ReadFile(notesPath(ch.Path))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			m.err = err
			return nil
		}
		var notes []editor.Note
		if err := json.Unmarshal(data, &notes); err != nil {
			m.err = fmt.Errorf("read notes for %s: %w", ch.Name, err)
			return nil
		}
		for _, n := range notes {
			if filter == "open" && n.Resolved || filter == "resolved" && !n.Resolved || filter == "orphans" && !n.Orphaned {
				continue
			}
			items = append(items, fmt.Sprintf("%s:%d  %s", ch.Name, n.Row+1, n.Body))
			targets = append(targets, command.SearchTarget{Path: ch.Path, Line: n.Row + 1})
		}
	}
	if len(items) == 0 {
		m.setNotice("No matching project notes")
		return nil
	}
	m.showResult(command.Result{Type: command.ResultList, Items: items, SearchTargets: targets})
	return nil
}
