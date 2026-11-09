package app

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ShayCichocki/marginctl/internal/command"
	"github.com/ShayCichocki/marginctl/internal/project"
	"github.com/ShayCichocki/marginctl/internal/results"
)

// file, or loads the target file first (blocked if the buffer is dirty, so an
// unsaved edit is never clobbered). Line is 1-based; GoToLine is 0-indexed.
func (m *Model) openSearchMatch(msg results.OpenMatchMsg) (tea.Model, tea.Cmd) {
	m.focus = FocusEditor
	m.editor.Focus()
	m.updateLayout()

	if msg.Path != m.project.CurrentFile {
		if m.editor.Dirty {
			m.setErrorNotice("Unsaved changes — :w to save before jumping to another file")
			m.updateStatusBar()
			return m, nil
		}
		if err := m.loadIntoEditor(msg.Path); err != nil {
			m.err = err
			m.updateStatusBar()
			return m, nil
		}
		m.projectnav.SetCurrentFile(msg.Path)
	}

	m.editor.GoToLine(msg.Line - 1)
	m.updateStatusBar()
	return m, nil
}

// pane as a list of "chapter:line  text" entries.
// openSearchMatch jumps to a search hit: it moves the cursor within the current
func (m *Model) searchManuscript(pattern string) tea.Cmd {
	if strings.TrimSpace(pattern) == "" {
		m.setErrorNotice("usage: :search <text>")
		return nil
	}
	if !m.project.IsProject {
		m.setErrorNotice("Not in a project")
		return nil
	}
	pg, fg := m.nextFSGeneration()
	root := m.project.Root
	return fsCommand(pg, fg, "search", func() (any, error) {
		p := project.New()
		if err := p.Open(root); err != nil {
			return nil, err
		}
		matches, err := p.SearchChapters(pattern)
		return searchFSResult{pattern: pattern, matches: matches}, err
	})
}

type searchFSResult struct {
	pattern string
	matches []project.SearchMatch
}

func (m *Model) showSearchMatches(result searchFSResult) {
	pattern, matches := result.pattern, result.matches
	if len(matches) == 0 {
		m.setNotice(fmt.Sprintf("no matches for %q", pattern))
		return
	}

	items := make([]string, 0, len(matches)+2)
	targets := make([]command.SearchTarget, 0, len(matches)+2)
	items = append(items, fmt.Sprintf("%d matches for %q — Enter to jump", len(matches), pattern), "")
	targets = append(targets, command.SearchTarget{}, command.SearchTarget{})
	for _, mt := range matches {
		items = append(items, fmt.Sprintf("%s:%d  %s", mt.Chapter, mt.Line, mt.Text))
		targets = append(targets, command.SearchTarget{Path: mt.Path, Line: mt.Line})
	}
	m.results.Show(command.Result{Type: command.ResultList, Items: items, SearchTargets: targets})
	m.focus = FocusResults
	m.editor.Blur()
	m.updateLayout()
	m.updateStatusBar()
}
