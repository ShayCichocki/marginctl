package app

import (
	"github.com/ShayCichocki/marginctl/internal/command"
	"github.com/ShayCichocki/marginctl/internal/llmcmd"
)

const (
	minNavTotal     = 20
	minEditorTotal  = 36
	minResultsTotal = 36
)

type Layout struct {
	Width, Height                              int
	ContentHeight                              int
	LeftVisible, EditorVisible, ResultsVisible bool
	LeftTotal, EditorTotal, ResultsTotal       int
	LeftWidth, EditorWidth, ResultsWidth       int
	PaneHeight                                 int
}

func calculateLayout(width, height int, wantLeft, wantResults, distractionFree bool, navWidth, resultsWidth int) Layout {
	l := Layout{Width: width, Height: height, ContentHeight: height - 2, EditorVisible: true}
	if l.ContentHeight < 0 {
		l.ContentHeight = 0
	}
	if distractionFree {
		wantLeft, wantResults = false, false
	}
	if navWidth < minNavTotal {
		navWidth = minNavTotal
	}
	if resultsWidth < minResultsTotal {
		resultsWidth = minResultsTotal
	}
	if wantLeft && wantResults && width >= minNavTotal+minEditorTotal+minResultsTotal {
		l.LeftVisible, l.ResultsVisible = true, true
		l.LeftTotal, l.ResultsTotal = navWidth, resultsWidth
		if l.LeftTotal+l.ResultsTotal > width-minEditorTotal {
			l.LeftTotal, l.ResultsTotal = minNavTotal, minResultsTotal
		}
	} else if wantResults && width >= minEditorTotal+minResultsTotal {
		l.ResultsVisible, l.ResultsTotal = true, resultsWidth
		if l.ResultsTotal > width-minEditorTotal {
			l.ResultsTotal = width - minEditorTotal
		}
	} else if wantLeft && width >= minNavTotal+minEditorTotal {
		l.LeftVisible, l.LeftTotal = true, navWidth
		if l.LeftTotal > width-minEditorTotal {
			l.LeftTotal = width - minEditorTotal
		}
	}
	l.EditorTotal = width - l.LeftTotal - l.ResultsTotal
	l.LeftWidth, l.EditorWidth, l.ResultsWidth = max(0, l.LeftTotal-2), max(0, l.EditorTotal-2), max(0, l.ResultsTotal-2)
	l.PaneHeight = max(0, l.ContentHeight-2)
	return l
}

func (m *Model) setResultsVisible(visible bool) {
	if !visible {
		m.results.Hide()
		m.clearPendingAction()
		m.pendingResultsFocus = false // deliberate close: don't re-grab focus later
		if m.focus == FocusResults {
			m.focus = FocusEditor
			m.editor.Focus()
		}
	}
	m.updateLayout()
	m.updateStatusBar()
}

func (m *Model) toggleLeftPane() {
	m.leftPaneVisible = !m.leftPaneVisible
	if !m.leftPaneVisible {
		m.pendingPaneFocus = false
		if m.focus == FocusLeftPane {
			m.focus = FocusEditor
			m.editor.Focus()
		}
	}
	m.updateLayout()
}

func (m *Model) toggleDistractionFree() {
	m.distractionFree = !m.distractionFree
	if m.distractionFree {
		m.pendingResultsFocus, m.pendingPaneFocus = false, false
		m.focus = FocusEditor
		m.editor.Focus()
	}
	m.updateLayout()
}

func (m *Model) showResult(result command.Result) {
	m.showResultAction(result, 0, llmcmd.Operation{})
}

// showResultAction is showResult for a display bound to an operation, so the
// pane takes part in the stale/invalidate paths.
func (m *Model) showResultAction(result command.Result, id uint64, op llmcmd.Operation) {
	m.pendingPaneFocus = false
	m.results.ShowAction(result, id, op)
	m.focus = FocusResults
	m.editor.Blur()
	m.updateLayout() // reconcileFocus bounces focus back if the pane can't fit
	if m.focus != FocusResults {
		m.setNotice("Result ready — widen the terminal to review it")
	}
}

func (m *Model) showPane(mode string) {
	m.pendingResultsFocus = false
	m.leftPaneVisible, m.distractionFree = true, false
	switch mode {
	case "project":
		m.refreshProjectNav()
		m.leftPaneMode = PaneModeProject
		m.projectnav.Focus()
	case "state":
		m.refreshStateNav()
		m.leftPaneMode = PaneModeState
		m.statenav.Focus()
	default:
		m.leftPaneMode = PaneModeOutline
		m.outline.Focus()
	}
	m.focus = FocusLeftPane
	m.editor.Blur()
	m.updateLayout() // reconcileFocus bounces focus back if the pane can't fit
	if m.focus != FocusLeftPane {
		m.setNotice("Navigator needs a wider terminal")
	}
}
