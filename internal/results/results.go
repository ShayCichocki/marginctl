package results

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ShayCichocki/marginctl/internal/command"
	"github.com/ShayCichocki/marginctl/internal/llmcmd"
	"github.com/ShayCichocki/marginctl/internal/theme"
)

// ResultsClosedMsg is sent when the results pane is dismissed
type ResultsClosedMsg struct{}

// OpenMatchMsg is sent when the user presses Enter on a navigable search result,
// asking the app to open Path and move the cursor to Line (1-based).
type OpenMatchMsg struct {
	Path string
	Line int
}

// DiffAcceptedMsg is sent when the user accepts a diff
type DiffAcceptedMsg struct {
	ActionID     uint64
	Operation    llmcmd.Operation
	OriginalText string
	ProposedText string
	StartLine    int
	EndLine      int
	StartCol     int
	EndCol       int

	// Identity of the buffer the diff was produced against, so the app can
	// discard it if the file/buffer changed while it was pending review.
	File      string
	BufferGen int
}

// DiffRejectedMsg is sent when the user rejects a diff
type DiffRejectedMsg struct{ ActionID uint64 }

// ConfirmAcceptedMsg is sent when the user answers "y" to a ResultConfirm prompt.
// The app owns what the confirmation applies to (e.g. a pending replace).
type ConfirmAcceptedMsg struct{ ActionID uint64 }

// VoiceGuideAcceptedMsg is sent when the user accepts a proposed voice guide.
// The app writes it (atomically, keeping a .bak) on the live model.
type VoiceGuideAcceptedMsg struct {
	ActionID  uint64
	Operation llmcmd.Operation
	Content   string
}

// Model represents the results pane
type Model struct {
	result    command.Result
	scrollPos int
	lines     []string
	width     int
	height    int
	visible   bool
	focused   bool
	actionID  uint64
	operation llmcmd.Operation
	stale     bool
	streaming bool
	cancelled bool

	// Navigable search-list state. selIdx indexes into m.lines (and, since a
	// search ResultList aligns SearchTargets 1:1 with Items, into the targets).
	selectable     bool
	selIdx         int
	displayTargets []command.SearchTarget
	newOutput      bool
	streamTail     string
	streamTailFrom int
}

// New creates a new results pane
func New() Model {
	return Model{}
}

// SetSize sets the content dimensions (inside border). A width change rewraps
// the current content: the pane is allocated its width only once it becomes
// visible (updateLayout runs after Show), and the terminal can be resized while
// a result is on screen, so the wrapped line list must be rebuilt to match.
func (m *Model) SetSize(width, height int) {
	widthChanged := width != m.width
	m.width = width
	m.height = height
	if widthChanged && m.visible {
		m.buildLines()
		if m.scrollPos > 0 {
			if maxScroll := len(m.lines) - m.height; m.scrollPos > maxScroll {
				m.scrollPos = maxScroll
			}
			if m.scrollPos < 0 {
				m.scrollPos = 0
			}
		}
	}
}

// wrap soft-wraps each logical line to the pane's content width, flattening the
// result. Width 0 (not yet laid out) passes lines through unwrapped; View still
// truncates as a fallback.
func (m *Model) wrap(logical []string) []string {
	var out []string
	for _, line := range logical {
		out = append(out, theme.Wrap(theme.Sanitize(line), m.width)...)
	}
	return out
}

// Show displays results from a command
func (m *Model) Show(result command.Result) {
	m.ShowAction(result, 0, llmcmd.Operation{})
}

// ShowAction displays a result bound to the action and operation that produced it.
func (m *Model) ShowAction(result command.Result, actionID uint64, operation llmcmd.Operation) {
	m.result = result
	m.actionID = actionID
	m.operation = operation
	m.visible = true
	m.scrollPos = 0
	m.focused = true
	m.stale = false
	m.streaming = false
	m.cancelled = false
	m.newOutput = false
	// Any replacement of the content ends a stream: the tail bookkeeping
	// describes lines that no longer exist.
	m.streamTail, m.streamTailFrom = "", 0
	m.buildLines()

	// A search list with targets is navigable: land the selection on the first
	// jumpable line.
	m.selectable = result.Type == command.ResultList && len(result.SearchTargets) > 0
	m.selIdx = -1
	if m.selectable {
		m.selIdx = m.nextSelectable(-1, +1)
	}
}

// nextSelectable returns the next index (in dir = +1/-1) at or after from whose
// SearchTarget has a non-empty Path, or the current selIdx if none is found.
func (m Model) nextSelectable(from, dir int) int {
	targets := m.displayTargets
	for i := from + dir; i >= 0 && i < len(targets); i += dir {
		if targets[i].Path != "" {
			return i
		}
	}
	if from >= 0 {
		return from // no move
	}
	return -1
}

// followSelection scrolls so the selected line stays within the visible window.
func (m *Model) followSelection() {
	if m.selIdx < 0 {
		return
	}
	if m.selIdx < m.scrollPos {
		m.scrollPos = m.selIdx
	} else if m.height > 0 && m.selIdx >= m.scrollPos+m.height {
		m.scrollPos = m.selIdx - m.height + 1
	}
}

// StreamText updates the pane with in-progress streamed text, pinning the view to
// the bottom so the newest tokens stay visible. It does not touch focus (the app
// owns that) — use it for incremental rendering while a streaming request runs.
func (m *Model) StreamText(content string) {
	if m.streaming && strings.HasPrefix(content, m.result.Content) {
		m.AppendStreamText(content[len(m.result.Content):])
		return
	}
	wasBottom := m.atBottom()
	hadContent := m.result.Content != ""
	m.result = command.Result{Type: command.ResultText, Content: content}
	m.visible = true
	m.streaming = true
	m.buildLines()
	m.syncStreamTail()
	maxScroll := len(m.lines) - m.height
	if maxScroll < 0 {
		maxScroll = 0
	}
	if wasBottom {
		m.scrollPos = maxScroll
	} else if hadContent {
		m.newOutput = true
	}
}

// AppendStreamText wraps only the unfinished logical line plus newly completed
// lines. Prior streamed output is never split and wrapped again.
func (m *Model) AppendStreamText(delta string) {
	if delta == "" {
		return
	}
	wasBottom := m.atBottom()
	m.result.Content += delta
	combined := m.streamTail + delta
	logical := strings.Split(combined, "\n")
	m.lines = m.lines[:min(len(m.lines), max(0, m.streamTailFrom))]
	for _, line := range logical[:len(logical)-1] {
		m.lines = append(m.lines, m.wrap([]string{line})...)
	}
	m.streamTail = logical[len(logical)-1]
	m.streamTailFrom = len(m.lines)
	m.lines = append(m.lines, m.wrap([]string{m.streamTail})...)
	m.visible, m.streaming = true, true
	if wasBottom {
		m.scrollPos = max(0, len(m.lines)-m.height)
	} else {
		m.newOutput = true
	}
}

func (m Model) atBottom() bool {
	maxScroll := max(0, len(m.lines)-m.height)
	return m.scrollPos >= maxScroll
}

// StreamFailed preserves diagnostic partial output while making the terminal
// state explicit and non-actionable.
func (m *Model) StreamFailed(content, status string) {
	if content != "" {
		content += "\n\n"
	}
	m.Show(command.Result{Type: command.ResultError, Content: content + status})
	m.cancelled = strings.Contains(strings.ToLower(status), "cancel")
}

// Streaming reports whether the pane is still showing live streamed text. It is
// false as soon as the content is replaced (Show/ShowAction/Hide), so a late
// stream token can never be appended to foreign content.
func (m Model) Streaming() bool { return m.visible && m.streaming }

// ReviewOnly reports whether the pane shows a display-only proposal review.
func (m Model) ReviewOnly() bool { return m.visible && m.result.ReviewOnly }

// Operation returns the immutable source context associated with the display.
func (m Model) Operation() llmcmd.Operation { return m.operation }

// MarkStale keeps a completed report visible but labels it non-current.
func (m *Model) MarkStale() {
	if !m.visible || m.operation.RequestID == 0 || m.HasPendingProposal() {
		return
	}
	m.result.Content += "\n\n[Stale: source context changed]"
	m.stale = true
	m.buildLines()
}

// InvalidateAction replaces an accept-enabled proposal with a non-actionable notice.
func (m *Model) InvalidateAction() {
	if m.HasPendingProposal() {
		m.Show(command.Result{Type: command.ResultError, Content: "Stale proposal: source context changed; run the command again"})
	}
}

// Hide closes the results pane
func (m *Model) Hide() {
	m.visible = false
	m.focused = false
	m.result = command.Result{}
	m.actionID = 0
	m.operation = llmcmd.Operation{}
	m.lines = nil
	m.displayTargets = nil
	m.selectable = false
	m.selIdx = -1
	m.stale, m.streaming, m.cancelled = false, false, false
	m.streamTail, m.streamTailFrom = "", 0
}

// Visible returns whether the results pane is shown
func (m Model) Visible() bool {
	return m.visible
}

// HasPendingProposal reports whether an accept/reject-style result (a diff, a
// voice guide, or a confirm prompt) is currently shown and awaiting a decision.
func (m Model) HasPendingProposal() bool {
	return m.visible &&
		(m.result.Type == command.ResultDiff ||
			m.result.Type == command.ResultVoiceGuide ||
			m.result.Type == command.ResultConfirm)
}

// Focus gives the pane keyboard focus
func (m *Model) Focus() {
	m.focused = true
}

// Blur removes keyboard focus
func (m *Model) Blur() {
	m.focused = false
}

// Focused returns whether the pane has focus
func (m Model) Focused() bool {
	return m.focused
}

// syncStreamTail recomputes the unfinished-line bookkeeping from the current
// content and wrapped lines. It must run after any rebuild while streaming,
// since the offset depends on the wrap width.
func (m *Model) syncStreamTail() {
	content := m.result.Content
	m.streamTail = content
	if i := strings.LastIndex(content, "\n"); i >= 0 {
		m.streamTail = content[i+1:]
	}
	m.streamTailFrom = max(0, len(m.lines)-len(m.wrap([]string{m.streamTail})))
}

func (m *Model) buildLines() {
	m.buildLinesRaw()
	if m.streaming {
		m.syncStreamTail()
	}
}

func (m *Model) buildLinesRaw() {
	m.lines = nil

	switch m.result.Type {
	case command.ResultText:
		// Soft-wrap prose output to the pane width so long lines stay readable.
		m.lines = m.wrap(strings.Split(m.result.Content, "\n"))

	case command.ResultList:
		for i, item := range m.result.Items {
			target := command.SearchTarget{}
			if i < len(m.result.SearchTargets) {
				target = m.result.SearchTargets[i]
			}
			item = theme.Sanitize(item)
			prefix, excerpt, ok := strings.Cut(item, "  ")
			if !ok || target.Path == "" || m.width <= lipgloss.Width(prefix)+2 {
				m.lines = append(m.lines, item)
				m.displayTargets = append(m.displayTargets, target)
				continue
			}
			wrapped := theme.Wrap(excerpt, max(1, m.width-lipgloss.Width(prefix)-2))
			for j, line := range wrapped {
				if j == 0 {
					m.lines = append(m.lines, prefix+"  "+line)
				} else {
					m.lines = append(m.lines, strings.Repeat(" ", lipgloss.Width(prefix)+2)+line)
				}
				if j == 0 {
					m.displayTargets = append(m.displayTargets, target)
				} else {
					m.displayTargets = append(m.displayTargets, command.SearchTarget{})
				}
			}
		}

	case command.ResultError:
		// Error bodies (e.g. a provider's JSON) are frequently one long line;
		// wrap so the whole message is visible instead of running off-screen.
		m.lines = append([]string{"Error:"}, m.wrap(strings.Split(m.result.Content, "\n"))...)

	case command.ResultDiff:
		// Build diff display
		m.lines = m.buildDiffLines()

	case command.ResultVoiceGuide:
		m.lines = m.buildVoiceGuideLines()

	case command.ResultConfirm:
		lines := m.wrap(strings.Split(m.result.Content, "\n"))
		m.lines = lines

	default:
		m.lines = []string{"(no output)"}
	}
}

func (m *Model) buildVoiceGuideLines() []string {
	var lines []string
	lines = append(lines, "── PROPOSED VOICE GUIDE ──")
	lines = append(lines, "")
	lines = append(lines, m.wrap(strings.Split(m.result.ProposedText, "\n"))...)
	lines = append(lines, "")
	return lines
}

func (m *Model) buildDiffLines() []string {
	if m.width < 72 {
		var out []string
		for _, line := range unifiedDiff(m.result.DiffLabel, m.result.OriginalText, m.result.ProposedText) {
			if strings.HasPrefix(line, "+ ") || strings.HasPrefix(line, "- ") {
				out = append(out, m.wrapDiff(line[:2], line[2:])...)
			} else {
				out = append(out, m.wrap([]string{line})...)
			}
		}
		return out
	}
	left, right := markedWords(m.result.OriginalText, m.result.ProposedText)
	half := (m.width - 3) / 2
	l := theme.Wrap("- "+left, half)
	r := theme.Wrap("+ "+right, half)
	n := len(l)
	if len(r) > n {
		n = len(r)
	}
	out := []string{"ORIGINAL | PROPOSED"}
	for i := 0; i < n; i++ {
		var a, b string
		if i < len(l) {
			a = l[i]
		}
		if i < len(r) {
			b = r[i]
		}
		out = append(out, theme.Truncate(a, half)+strings.Repeat(" ", max(0, half-lipgloss.Width(a)))+" | "+b)
	}
	return out
}

// wrapDiff wraps one diff line to the pane width and re-applies the +/- marker to
// every wrapped segment, so continuation lines stay within the pane AND keep the
// prefix that View colors on (removed = red, added = green).
func (m *Model) wrapDiff(marker, line string) []string {
	line = theme.Sanitize(line)
	contentWidth := m.width - len(marker)
	if contentWidth < 1 {
		// Pane too narrow (or not yet laid out) to reserve room for the marker;
		// emit the single prefixed line and let View truncate it.
		return []string{marker + line}
	}
	segments := theme.Wrap(line, contentWidth)
	out := make([]string, len(segments))
	for i, seg := range segments {
		out[i] = marker + seg
	}
	return out
}

// Update handles input when the results pane is focused
func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	if !m.visible || !m.focused {
		return m, nil
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		if m.stale && (msg.String() == "y" || msg.String() == "n") {
			return m, nil
		}
		if m.result.ReviewOnly && (msg.String() == "y" || msg.String() == "n" || msg.String() == "q") {
			// The app maps these to per-proposal actions; the pane never
			// decides (or hides itself) on a display-only review.
			return m, nil
		}
		switch msg.String() {
		case "esc", "q":
			m.Hide()
			return m, func() tea.Msg { return ResultsClosedMsg{} }

		case "y":
			// Accept diff
			if m.result.Type == command.ResultDiff {
				msg := DiffAcceptedMsg{
					ActionID:     m.actionID,
					Operation:    m.operation,
					OriginalText: m.result.OriginalText,
					ProposedText: m.result.ProposedText,
					StartLine:    m.result.StartLine,
					EndLine:      m.result.EndLine,
					StartCol:     m.result.StartCol,
					EndCol:       m.result.EndCol,
					File:         m.result.File,
					BufferGen:    m.result.BufferGen,
				}
				m.Hide()
				return m, func() tea.Msg { return msg }
			}
			// Accept voice guide (writes a file, not the editor buffer).
			if m.result.Type == command.ResultVoiceGuide {
				content := m.result.ProposedText
				id, op := m.actionID, m.operation
				m.Hide()
				return m, func() tea.Msg { return VoiceGuideAcceptedMsg{ActionID: id, Operation: op, Content: content} }
			}
			// Accept a generic confirmation (e.g. cross-file replace).
			if m.result.Type == command.ResultConfirm {
				id := m.actionID
				m.Hide()
				return m, func() tea.Msg { return ConfirmAcceptedMsg{ActionID: id} }
			}

		case "enter":
			// Jump to the selected search match.
			if m.selectable && m.selIdx >= 0 && m.selIdx < len(m.displayTargets) {
				t := m.displayTargets[m.selIdx]
				if t.Path != "" {
					m.Hide()
					return m, func() tea.Msg { return OpenMatchMsg{Path: t.Path, Line: t.Line} }
				}
			}

		case "n":
			// Reject a pending proposal (diff, voice guide, or confirm).
			if m.HasPendingProposal() {
				id := m.actionID
				m.Hide()
				return m, func() tea.Msg { return DiffRejectedMsg{ActionID: id} }
			}

		case "j", "down":
			// In a navigable search list, j/k move the selection (and scroll
			// follows it); otherwise they scroll the text.
			if m.selectable {
				m.selIdx = m.nextSelectable(m.selIdx, +1)
				m.followSelection()
				return m, nil
			}
			maxScroll := len(m.lines) - m.height
			if maxScroll < 0 {
				maxScroll = 0
			}
			if m.scrollPos < maxScroll {
				m.scrollPos++
			}
			if m.scrollPos == maxScroll {
				m.newOutput = false
			}

		case "k", "up":
			if m.selectable {
				m.selIdx = m.nextSelectable(m.selIdx, -1)
				m.followSelection()
				return m, nil
			}
			if m.scrollPos > 0 {
				m.scrollPos--
			}

		case "g":
			m.scrollPos = 0

		case "G":
			maxScroll := len(m.lines) - m.height
			if maxScroll < 0 {
				maxScroll = 0
			}
			m.scrollPos = maxScroll
			m.newOutput = false
		}
	}

	return m, nil
}

// View renders the results pane content (without border - app handles that)
func (m Model) View() string {
	// A non-positive width would make the truncation below slice with a
	// negative bound; too small to render, so render nothing.
	if !m.visible || m.width < 1 {
		return ""
	}

	contentWidth := m.width
	contentHeight := m.height - 2
	if contentHeight < 0 {
		contentHeight = 0
	}

	// Calculate visible range
	endIdx := m.scrollPos + contentHeight
	if endIdx > len(m.lines) {
		endIdx = len(m.lines)
	}

	rendered := make([]string, 0, contentHeight)
	for i := m.scrollPos; i < endIdx; i++ {
		line := m.lines[i]

		// Truncate long lines (rune- and display-width-aware)
		line = theme.Truncate(line, contentWidth)

		style := lipgloss.NewStyle().Background(theme.BgPanel).Foreground(theme.Text)

		// Highlight the selected line in a navigable search list.
		if m.selectable && i == m.selIdx {
			style = style.Background(theme.Primary).Foreground(theme.Text).Bold(true)
		}

		// Style based on result type
		switch m.result.Type {
		case command.ResultError:
			style = style.Foreground(theme.DiffRemov)
		case command.ResultDiff:
			// Diff coloring
			if strings.HasPrefix(line, "- ") {
				style = style.Foreground(theme.DiffRemov) // removed
			} else if strings.HasPrefix(line, "+ ") {
				style = style.Foreground(theme.DiffAdded) // added
			} else if strings.HasPrefix(line, "──") || strings.HasPrefix(line, "[") {
				style = style.Foreground(theme.TextDim)
			}
		}

		rendered = append(rendered, style.Render(padLine(line, contentWidth)))
	}
	blank := lipgloss.NewStyle().Background(theme.BgPanel).Render(strings.Repeat(" ", contentWidth))
	for len(rendered) < contentHeight {
		rendered = append(rendered, blank)
	}

	header := m.headerLine()
	footer := m.footerLine()
	body := lipgloss.JoinVertical(lipgloss.Left, rendered...)
	return lipgloss.JoinVertical(lipgloss.Left, header, body, footer)
}

func padLine(line string, width int) string {
	return line + strings.Repeat(" ", max(0, width-lipgloss.Width(line)))
}

func (m Model) headerLine() string {
	typeName := map[command.ResultType]string{command.ResultText: "REPORT", command.ResultList: "LIST", command.ResultError: "ERROR", command.ResultDiff: "DIFF", command.ResultVoiceGuide: "VOICE", command.ResultConfirm: "CONFIRM"}[m.result.Type]
	state := "completed"
	if m.streaming {
		state = "streaming"
	}
	if m.stale {
		state = "stale"
	}
	source := m.operation.SourceFile
	if source == "" {
		source = m.result.File
	}
	meta := strings.TrimSpace(m.operation.Provider + "/" + m.operation.Model)
	line := fmt.Sprintf("> %s | %s | %s | %s", typeName, source, meta, state)
	line = theme.Truncate(line, m.width)
	return lipgloss.NewStyle().Background(theme.BgPanel).Foreground(theme.Text).Bold(true).Render(padLine(line, m.width))
}
func (m Model) footerLine() string {
	actions := "j/k scroll  Esc close"
	if m.HasPendingProposal() {
		actions = "y accept  n reject  Esc close"
	}
	if m.result.ReviewOnly {
		actions = "y accept  n skip  q/Esc back to list"
	}
	if m.stale {
		actions = "STALE - actions disabled  Esc close"
	}
	if m.cancelled {
		actions = "cancelled  Esc close"
	}
	if m.newOutput {
		actions = "NEW OUTPUT - G follow  " + actions
	}
	end := m.scrollPos + max(0, m.height-2)
	if end > len(m.lines) {
		end = len(m.lines)
	}
	line := fmt.Sprintf("%s | %d-%d/%d", actions, min(m.scrollPos+1, max(1, len(m.lines))), end, len(m.lines))
	line = theme.Truncate(line, m.width)
	return lipgloss.NewStyle().Background(theme.BgPanel).Foreground(theme.TextDim).Render(padLine(line, m.width))
}
