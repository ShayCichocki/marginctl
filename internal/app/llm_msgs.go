package app

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ShayCichocki/marginctl/internal/command"
	"github.com/ShayCichocki/marginctl/internal/editor"
	"github.com/ShayCichocki/marginctl/internal/fsutil"
	"github.com/ShayCichocki/marginctl/internal/llm"
	"github.com/ShayCichocki/marginctl/internal/llmcmd"
	"github.com/ShayCichocki/marginctl/internal/results"
	"github.com/ShayCichocki/marginctl/internal/state"
)

// startLLM dispatches an async LLM command. It enters the loading state, creates
// a cancelable request context (bounded by the client's timeout), and stamps the
// returned ResponseMsg with a per-dispatch ID so a cancelled or superseded
// response can be dropped. build receives the request context and returns the
// provider-specific tea.Cmd (e.g. llmcmd.ExecuteTighten).
// spinnerFrames animates the loading indicator, one frame per llmTick.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// llmTickMsg drives the loading spinner + elapsed-time counter. It carries the
// dispatch reqID so a tick chain left over from a superseded/cancelled request
// stops itself instead of racing the current one.
type llmTickMsg struct{ reqID int }

// llmTick schedules the next spinner frame.
func llmTick(reqID int) tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg {
		return llmTickMsg{reqID: reqID}
	})
}

// llmStatusLine renders the live progress line: spinner, label, elapsed seconds,
// and the cancel hint. Shown on the bottom bar while a request is in flight so a
// long call reads as "working (12s)" instead of a frozen "Analyzing…".
func (m *Model) llmStatusLine() string {
	frame := spinnerFrames[m.llmSpinner%len(spinnerFrames)]
	elapsed := int(time.Since(m.llmStart).Seconds())
	return fmt.Sprintf("%s %s (%ds · esc to cancel)", frame, m.llmStatus, elapsed)
}

// streamDeltaMsg carries one chunk of streamed text; streamEndMsg is the terminal
// event. Both carry the dispatch reqID so a cancelled/superseded stream is dropped.
type (
	streamDeltaMsg struct {
		operation llmcmd.Operation
		text      string
	}
	streamEndMsg struct {
		operation llmcmd.Operation
		text      string
		err       error
		usage     llm.Usage
	}
)

const (
	streamFrameBudget = 16 * time.Millisecond
	maxStreamBytes    = 16 << 20
)

// waitForStream reads one event from a streaming completion and turns it into an
// app message; the handler re-subscribes for the next one. A closed channel (the
// producer exited without a terminal event, e.g. on cancel) ends the loop.
func waitForStream(ch <-chan llm.StreamEvent, operation llmcmd.Operation) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return streamEndMsg{operation: operation, err: llm.ErrStreamProtocol}
		}
		switch {
		case ev.Err != nil:
			return streamEndMsg{operation: operation, err: ev.Err}
		case ev.Done:
			return streamEndMsg{operation: operation, usage: ev.Usage}
		default:
			var b strings.Builder
			b.WriteString(ev.Delta)
			timer := time.NewTimer(streamFrameBudget)
			defer timer.Stop()
			for {
				select {
				case next, open := <-ch:
					if !open {
						return streamEndMsg{operation: operation, err: llm.ErrStreamProtocol}
					}
					if next.Err != nil {
						return streamEndMsg{operation: operation, text: b.String(), err: next.Err}
					}
					if next.Done {
						return streamEndMsg{operation: operation, text: b.String(), usage: next.Usage}
					}
					b.WriteString(next.Delta)
				case <-timer.C:
					return streamDeltaMsg{operation: operation, text: b.String()}
				}
			}
		}
	}
}

// formatUsage renders token counts (and cost when the model's rate is known) for
// the transient completion notice.
func formatUsage(u llm.Usage, model string) string {
	if u.InputTokens == 0 && u.OutputTokens == 0 {
		return ""
	}
	s := fmt.Sprintf("%s in / %s out", compactTokens(u.InputTokens), compactTokens(u.OutputTokens))
	if cost, ok := llm.EstimateCost(model, u); ok {
		s += fmt.Sprintf(" · ~$%.4f", cost)
	}
	return s
}

// compactTokens renders a token count compactly (e.g. 1.2k).
func compactTokens(n int) string {
	if n >= 1000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	return fmt.Sprintf("%d", n)
}

func (m *Model) startLLM(status, kind string, build func(reqCtx context.Context) tea.Cmd) tea.Cmd {
	m.llmReqID++
	reqID := m.llmReqID
	reqCtx, cancel := context.WithCancel(context.Background())
	m.llmCancel = cancel
	m.llmLoading = true
	m.llmStatus = status
	m.llmStart = time.Now()
	m.llmSpinner = 0
	op := llmcmd.Operation{RequestID: reqID, Kind: kind, ProjectRoot: m.project.Root,
		SourceFile: m.project.CurrentFile, BufferRevision: m.bufferGen,
		Provider: m.llmClient.Provider(), Model: m.llmClient.Model()}
	if kind == "llm.voice-guide" && m.project.IsProject {
		op.Target = filepath.Join(m.project.Root, "state", "style", "voice.md")
		op.TargetFingerprint, _ = fsutil.FileFingerprint(op.Target)
	} else if kind == "llm.extract-state" {
		op.Target = filepath.Join(m.project.Root, "state")
	}
	m.llmOperation = op
	m.clearPendingAction()
	m.updateStatusBar()

	inner := build(reqCtx)
	send := func() tea.Msg {
		msg := inner()
		switch mm := msg.(type) {
		case llmcmd.ResponseMsg:
			mm.ReqID = reqID
			mm.Operation = op
			return mm
		case llmcmd.StreamStartMsg:
			mm.ReqID = reqID
			mm.Operation = op
			return mm
		}
		return msg
	}
	// Run the request and animate the progress line concurrently.
	return tea.Batch(send, llmTick(reqID))
}

// cancelLLM aborts the in-flight LLM request (if any) and invalidates its
// pending response so a late arrival is ignored. Safe to call when idle.
func (m *Model) cancelLLM() {
	if m.llmCancel != nil {
		m.llmCancel()
		m.llmCancel = nil
	}
	m.llmLoading = false
	m.llmStatus = ""
	m.streamCh = nil
	// Bump the counter so the in-flight response (stamped with the old ID) is
	// recognized as stale and dropped when it arrives.
	m.llmReqID++
}

func (m *Model) handleLLMTick(msg llmTickMsg) (tea.Model, tea.Cmd) {
	// Advance the spinner while the request is in flight. Stop when the
	// request finished (llmLoading false) or was superseded (reqID moved on),
	// so at most one tick chain is ever alive.
	if !m.llmLoading || msg.reqID != m.llmReqID {
		return m, nil
	}
	m.llmSpinner++
	m.updateStatusBar()
	return m, llmTick(msg.reqID)
}

func (m *Model) handleStreamStart(msg llmcmd.StreamStartMsg) (tea.Model, tea.Cmd) {
	// Drop a stream whose dispatch was cancelled/superseded before it began.
	if !m.operationCurrent(msg.Operation, true) {
		if msg.Operation.RequestID == m.llmReqID {
			// Current request, but its source changed under it: retire it so the
			// spinner stops, single-flight reopens, and the producer goroutine is
			// cancelled rather than left to fill its buffer and block.
			m.cancelLLM()
			m.setErrorNotice("Stale LLM result — source context changed")
		}
		return m, nil
	}
	// Begin incremental rendering: seed with any prefix (e.g. continuity
	// budget notes) and subscribe to the token channel. llmLoading stays true
	// so the spinner keeps animating alongside the streamed text.
	m.streamBuf = msg.Prefix
	m.streamCh = msg.Ch
	m.results.StreamText(m.streamBuf)
	m.focus = FocusResults
	m.editor.Blur()
	m.updateLayout()
	m.updateStatusBar()
	return m, waitForStream(msg.Ch, msg.Operation)
}

// abandonReplacedStream stops a report stream whose results pane was replaced
// by other content (or closed) mid-flight: the producer is cancelled and nothing
// more is ever appended to the foreign content.
func (m *Model) abandonReplacedStream() {
	m.cancelLLM()
	m.streamBuf = ""
	m.setNotice("Report stream stopped: results pane was replaced")
	m.updateStatusBar()
}

func (m *Model) handleStreamDelta(msg streamDeltaMsg) (tea.Model, tea.Cmd) {
	// A token from a cancelled/superseded stream: drop it and stop reading
	// (the producer's ctx is already cancelled, so it won't leak).
	if !m.llmLoading || !m.operationCurrent(msg.operation, true) {
		if m.llmLoading && msg.operation.RequestID == m.llmReqID {
			// Current stream gone stale mid-flight: cancel the producer and
			// reopen single-flight rather than spinning forever.
			m.cancelLLM()
			m.setErrorNotice("Stale LLM result — source context changed")
		}
		return m, nil
	}
	if !m.results.Streaming() {
		m.abandonReplacedStream()
		return m, nil
	}
	remaining := maxStreamBytes - len(m.streamBuf)
	if remaining > 0 {
		if len(msg.text) > remaining {
			msg.text = msg.text[:remaining]
		}
		m.streamBuf += msg.text
		m.results.AppendStreamText(msg.text)
	}
	m.updateStatusBar()
	return m, waitForStream(m.streamCh, msg.operation)
}

func (m *Model) handleStreamEnd(msg streamEndMsg) (tea.Model, tea.Cmd) {
	if msg.operation.RequestID != m.llmReqID {
		return m, nil
	}
	if !m.results.Streaming() {
		m.abandonReplacedStream()
		return m, nil
	}
	if msg.text != "" && len(m.streamBuf) < maxStreamBytes {
		remaining := maxStreamBytes - len(m.streamBuf)
		if len(msg.text) > remaining {
			msg.text = msg.text[:remaining]
		}
		m.streamBuf += msg.text
		m.results.AppendStreamText(msg.text)
	}
	m.llmCancel = nil
	m.llmLoading = false
	m.llmStatus = ""
	m.streamCh = nil
	if msg.err != nil {
		label := map[llm.StreamErrorKind]string{
			llm.StreamErrorCanceled: "Cancelled", llm.StreamErrorTimeout: "Timed out",
			llm.StreamErrorProtocol: "Incomplete response", llm.StreamErrorProvider: "Provider failed",
		}[llm.ClassifyStreamError(msg.err)]
		content := m.streamBuf
		if content != "" {
			content += "\n\n"
		}
		content += label + ": " + msg.err.Error()
		m.results.Show(command.Result{Type: command.ResultError, Content: content})
	} else if note := formatUsage(msg.usage, m.llmClient.Model()); note != "" {
		m.setNotice("✓ " + note)
	}
	m.focus = FocusResults
	m.editor.Blur()
	m.updateLayout()
	m.updateStatusBar()
	return m, nil
}

func (m *Model) handleLLMResponse(msg llmcmd.ResponseMsg) (tea.Model, tea.Cmd) {
	// Drop responses from a cancelled or superseded request (their ReqID no
	// longer matches the current dispatch counter).
	if msg.ReqID != m.llmReqID || !m.operationCurrent(msg.Operation, false) {
		if msg.ReqID == m.llmReqID {
			// This IS the in-flight request, but its source context changed under
			// it. Retire it (cancelLLM clears loading/cancel and bumps the counter)
			// so the spinner stops and single-flight reopens — otherwise new LLM
			// commands are rejected as "already in progress" until Esc.
			m.cancelLLM()
			m.setErrorNotice("Stale LLM result — source context changed")
		}
		return m, nil
	}
	// LLM command completed
	m.llmCancel = nil
	m.llmLoading = false
	m.llmStatus = ""
	res := msg.Result
	// A splice-type result (diff) is only safe to apply to the exact buffer
	// it was computed against. Guard emptiness and staleness before showing.
	if res.Type == command.ResultDiff {
		if strings.TrimSpace(res.ProposedText) == "" {
			res = command.Result{Type: command.ResultError, Content: "model returned no text"}
		} else if msg.File != m.project.CurrentFile || msg.BufferGen != m.bufferGen {
			m.setErrorNotice("Stale LLM result discarded — buffer changed during the request")
			m.updateStatusBar()
			return m, nil
		}
	}
	// Extracted state proposals become a confirmation: list the entries and
	// write them only on accept (skipping any that already exist).
	if res.Type == command.ResultStateProposals {
		return m.showStateProposals(res.StateProposals, res.StateSkipped, msg.Operation)
	}
	if res.Type == command.ResultDiff || res.Type == command.ResultVoiceGuide {
		m.setPendingAction(pendingLLMResult, msg.Operation, res)
	} else {
		m.results.ShowAction(res, 0, msg.Operation)
	}
	m.focus = FocusResults
	m.editor.Blur()
	m.updateLayout()
	// Surface token usage/cost on success (non-error, non-diff-stale path).
	if res.Type != command.ResultError {
		if note := formatUsage(msg.Usage, m.llmClient.Model()); note != "" {
			m.setNotice("✓ " + note)
		}
	}
	m.updateStatusBar()
	return m, nil
}

func (m *Model) handleDiffAccepted(msg results.DiffAcceptedMsg) (tea.Model, tea.Cmd) {
	if !m.pendingMatches(msg.ActionID, msg.Operation) {
		return m, nil
	}
	m.clearPendingAction()
	// Refuse to splice a diff whose buffer changed under it (file switch or
	// edit while the proposal sat in the results pane awaiting a decision).
	if msg.File != m.project.CurrentFile || msg.BufferGen != m.bufferGen {
		m.setErrorNotice("Stale diff discarded — the buffer changed; re-run the command")
	} else {
		m.applyDiff(msg)
	}
	m.focus = FocusEditor
	m.editor.Focus()
	m.updateStatusBar()
	return m, nil
}

func (m *Model) handleVoiceGuideAccepted(msg results.VoiceGuideAcceptedMsg) (tea.Model, tea.Cmd) {
	// Write on accept only, on the live model (never from a tea.Cmd
	// goroutine). WriteVoiceGuide is atomic and keeps a .bak of the prior guide.
	if !m.pendingMatches(msg.ActionID, msg.Operation) || !m.operationCurrent(msg.Operation, true) || !m.targetUnchanged(msg.Operation) {
		m.setErrorNotice("Stale voice guide discarded — project or target changed")
		m.clearPendingAction()
		return m, nil
	}
	m.clearPendingAction()
	if m.state == nil && msg.Operation.ProjectRoot == m.project.Root {
		m.state = state.New(msg.Operation.ProjectRoot)
	}
	path := filepath.Join(msg.Operation.ProjectRoot, "state", "style", "voice.md")
	fp, _ := fsutil.FileFingerprint(path)
	if err := m.writeExternalDocument(path, msg.Content, fp); err != nil {
		m.err = err
	} else {
		_ = m.state.Refresh()
		m.statenav.SetStateFilesWithCategories(m.state.All(), m.state.Categories())
		m.setNotice("Voice guide saved to state/style/voice.md")
	}
	m.focus = FocusEditor
	m.editor.Focus()
	m.updateStatusBar()
	return m, nil
}

func (m *Model) handleDiffRejected(msg results.DiffRejectedMsg) (tea.Model, tea.Cmd) {
	// Just close results, no change. Cancels any pending confirmation too.
	if m.pendingAction == nil || msg.ActionID != m.pendingAction.ID {
		return m, nil
	}
	m.clearPendingAction()
	m.focus = FocusEditor
	m.editor.Focus()
	m.updateStatusBar()
	return m, nil
}

func (m *Model) handleConfirmAccepted(msg results.ConfirmAcceptedMsg) (tea.Model, tea.Cmd) {
	if m.pendingAction == nil || msg.ActionID != m.pendingAction.ID {
		return m, nil
	}
	if m.pendingAction.Kind == pendingStateProposals {
		return m.applyStateProposals(msg.ActionID)
	}
	if m.pendingAction.Kind == pendingReplace {
		return m.applyPendingReplace(msg.ActionID)
	}
	return m, nil
}

func (m *Model) applyDiff(diff results.DiffAcceptedMsg) {
	// Splice exactly the character range the selection covered, so untouched
	// text on the boundary lines survives and the buffer stays dirty until
	// saved (ReplaceRange, unlike SetContent, does not reset the saved state).
	start := editor.Position{Row: diff.StartLine, Col: diff.StartCol}
	end := editor.Position{Row: diff.EndLine, Col: diff.EndCol}
	m.editor.ReplaceRange(start, end, diff.ProposedText)
	m.parseOutlineNow(m.editor.Content())
	m.bufferGen++
}
