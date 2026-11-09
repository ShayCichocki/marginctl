package app

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ShayCichocki/marginctl/internal/command"
	"github.com/ShayCichocki/marginctl/internal/fsutil"
	"github.com/ShayCichocki/marginctl/internal/llmcmd"
)

// replaceRequest is a pending cross-file replace awaiting confirmation.
type replaceRequest struct {
	old, replacement string
}

type pendingActionKind uint8

const (
	pendingReplace pendingActionKind = iota + 1
	pendingStateProposals
	pendingLLMResult
)

// PendingAction is the sole accept/reject capability. Its ID is copied into the
// visible results prompt, so a hidden, superseded, or duplicate decision cannot act.
type PendingAction struct {
	ID        uint64
	Kind      pendingActionKind
	Operation llmcmd.Operation
	Replace   replaceRequest
	Proposals []command.StateProposal
	Skipped   skipTally // proposals dropped before confirmation, by reason
	Result    command.Result
}

func (m *Model) clearPendingAction() {
	m.pendingAction = nil
	m.proposalReviewN = 0
	// A display-only proposal review is meaningless without its batch (and
	// would otherwise keep swallowing y/n/q): close it.
	if m.results.ReviewOnly() {
		m.results.Hide()
		if m.focus == FocusResults {
			m.focus = FocusEditor
			m.editor.Focus()
		}
		m.updateLayout()
	}
}

func (m *Model) setPendingAction(kind pendingActionKind, op llmcmd.Operation, result command.Result) {
	m.nextActionID++
	m.pendingAction = &PendingAction{ID: m.nextActionID, Kind: kind, Operation: op, Result: result}
	m.results.ShowAction(result, m.nextActionID, op)
}

func (m *Model) pendingMatches(id uint64, op llmcmd.Operation) bool {
	return m.pendingAction != nil && id != 0 && id == m.pendingAction.ID && op == m.pendingAction.Operation
}

func (m *Model) operationCurrent(op llmcmd.Operation, actionable bool) bool {
	if op.RequestID == 0 || op.RequestID != m.llmReqID || op.ProjectRoot != m.project.Root ||
		op.SourceFile != m.project.CurrentFile || op.BufferRevision != m.bufferGen {
		return false
	}
	return !actionable || m.targetUnchanged(op)
}

func (m *Model) targetUnchanged(op llmcmd.Operation) bool {
	if op.Target == "" || op.Kind == "llm.extract-state" {
		return true
	}
	fp, err := fsutil.FileFingerprint(op.Target)
	return err == nil && fp.Equal(op.TargetFingerprint)
}

// replaceCommand previews a project-wide replace (":replace old/new") and asks
// for confirmation before writing anything. The buffer must be clean, since the
// replace rewrites chapter files on disk (possibly the open one).
func (m *Model) replaceCommand(arg string) tea.Cmd {
	if !m.project.IsProject {
		m.setErrorNotice("Not in a project")
		return nil
	}
	i := strings.IndexByte(arg, '/')
	if i < 0 || strings.TrimSpace(arg[:i]) == "" {
		m.setErrorNotice("usage: :replace old/new  (replaces old with new across all chapters)")
		return nil
	}
	old, replacement := arg[:i], arg[i+1:]
	if m.editor.Dirty {
		m.setErrorNotice("Unsaved changes — :w before :replace (it rewrites chapters on disk)")
		return nil
	}
	count, err := m.project.CountReplacements(old)
	if err != nil {
		m.err = err
		return nil
	}
	if count == 0 {
		m.setNotice(fmt.Sprintf("no occurrences of %q", old))
		return nil
	}

	preview := fmt.Sprintf("Replace %d occurrence(s) of %q with %q across the manuscript?",
		count, old, replacement)
	m.nextActionID++
	m.pendingAction = &PendingAction{ID: m.nextActionID, Kind: pendingReplace, Replace: replaceRequest{old: old, replacement: replacement}}
	m.results.ShowAction(command.Result{Type: command.ResultConfirm, Content: preview}, m.nextActionID, llmcmd.Operation{})
	m.focus = FocusResults
	m.editor.Blur()
	m.updateLayout()
	m.updateStatusBar()
	return nil
}

// applyPendingReplace performs the confirmed cross-file replace, then reloads the
// open chapter so the buffer reflects the on-disk change.
func (m *Model) applyPendingReplace(id uint64) (tea.Model, tea.Cmd) {
	if m.pendingAction == nil || m.pendingAction.ID != id || m.pendingAction.Kind != pendingReplace {
		return m, nil
	}
	req := m.pendingAction.Replace
	m.clearPendingAction()
	m.focus = FocusEditor
	m.editor.Focus()
	m.updateLayout()
	// The clean-buffer check in replaceCommand predates the confirmation; the
	// reload below would discard unsaved edits made since.
	if m.editor.Dirty {
		m.setErrorNotice("Replace cancelled: unsaved changes in the buffer; save and retry")
		m.updateStatusBar()
		return m, nil
	}
	res, err := m.project.ReplaceInChapters(req.old, req.replacement)
	if err != nil {
		m.err = err
		m.updateStatusBar()
		return m, nil
	}
	// The open chapter may have been rewritten on disk — reload it so the buffer
	// matches (the pre-replace clean-buffer guard makes this safe).
	if m.project.CurrentFile != "" {
		if loadErr := m.loadIntoEditor(m.project.CurrentFile); loadErr != nil {
			m.err = loadErr
		}
	}
	m.refreshProjectNav()
	m.setNotice(fmt.Sprintf("Replaced %d occurrence(s) across %d chapter(s)", res.Replacements, res.Chapters))
	m.updateStatusBar()
	return m, nil
}
