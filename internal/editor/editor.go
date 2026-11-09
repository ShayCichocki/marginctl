package editor

import (
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ShayCichocki/marginctl/internal/theme"
)

// wrapWidth is the fallback wrap column count used before the editor has been
// given a real size (SetSize). Once sized, wrapping uses the actual pane width.
const wrapWidth = 100

// minGutterWidth is the line-number column's floor (3 digits + a trailing
// space). It widens past this once the buffer reaches ≥1000 lines so 4+ digit
// numbers don't misalign the continuation-line gutter (v2 #63).
const minGutterWidth = 4

// gutterWidth returns the current line-number column width: enough digits for
// the highest line number, plus a trailing space, floored at minGutterWidth.
func (m Model) gutterWidth() int {
	w := len(strconv.Itoa(len(m.lines))) + 1
	if w < minGutterWidth {
		return minGutterWidth
	}
	return w
}

type Mode int

const (
	ModeNormal Mode = iota
	ModeInsert
	ModeVisual
)

func (m Mode) String() string {
	switch m {
	case ModeInsert:
		return "INSERT"
	case ModeVisual:
		return "VISUAL"
	default:
		return ""
	}
}

// Position represents a cursor position. Col is a rune index into the logical
// line (not a byte offset), so multibyte characters count as one column.
type Position struct {
	Row int
	Col int
}

type Model struct {
	styles          theme.Styles
	styleNoteMarker lipgloss.Style
	lines           []string
	cursorRow       int
	cursorCol       int // rune index into lines[cursorRow]
	viewOffset      int // display line offset for scrolling
	width           int
	height          int
	Mode            Mode
	Dirty           bool
	origContent     string
	savedLines      []string
	dirtyRows       map[int]struct{}
	revision        uint64
	savedRevision   uint64
	focused         bool
	pendingG        bool

	// Undo/redo history. Each entry is a full-buffer snapshot taken just before
	// a mutating op. An entire insert session coalesces into a single entry:
	// entering insert arms insertUndoSnap, and only the first real edit in the
	// session commits it (so an insert that types nothing adds no undo point).
	undoStack       []snapshot
	redoStack       []snapshot
	undoBytes       int
	redoBytes       int
	insertUndoArmed bool
	insertUndoSnap  snapshot

	// Yank/paste register (vim "unnamed" register). registerLine marks a
	// linewise yank (yy/dd) so p/P re-insert it as whole lines. clipMirror is
	// the last text pushed to the OS clipboard, so a paste can tell an external
	// copy apart from our own yank and treat it as charwise. The register
	// deliberately survives SetContent — you can yank in one chapter and paste
	// into another.
	register     string
	registerLine bool
	clipMirror   string

	// Operator-pending / count state for the vim motion machine.
	//   count       accumulates a numeric prefix (0 = none).
	//   pendingOp   the waiting operator: "" | "d" | "c" | "y".
	//   opCount     the count captured when the operator key was pressed.
	//   pendingReplace  true after `r`, waiting for the replacement char.
	count           int
	countExplicit   bool
	pendingOp       string
	opCount         int
	opCountExplicit bool
	pendingReplace  bool

	// Last `/` search pattern and the direction it was issued in, for n/N.
	lastSearch    string
	searchForward bool

	// Visual selection
	selectionAnchor Position // where selection started

	// Range-anchored notes. Each note sticks to its selected text (Anchor): after
	// an edit or a reload, relocateNotes re-finds the anchor near its last row, so
	// a note follows its text rather than tracking raw offsets through every
	// mutation. See Note.
	notes                []Note
	displayCache         []displayLine
	displayRowRanges     []displayRange
	displayCacheRevision uint64
	displayCacheWidth    int
	wordCountRevision    uint64
	wordCountCache       int
	searchCacheRevision  uint64
	searchCacheQuery     string
	searchCache          []Position
	renderLineRevision   uint64
	renderLineQuery      string
	renderLines          map[int]renderLineState
	rowCache             []rowWrap // per-row clusters + wrap segments; see rowWrap
	viewCacheKey         editorViewKey
	viewCache            string
	viewCacheValid       bool
	notesRevision        uint64
}

type editorViewKey struct {
	revision                  uint64
	cursorRow, cursorCol      int
	viewOffset, width, height int
	mode                      Mode
	focused                   bool
	search                    string
	selection                 Position
	notesRevision             uint64
}

func New() Model {
	return NewWithStyles(theme.NewStyles(theme.Default()))
}

func NewWithStyles(styles theme.Styles) Model {
	return Model{
		lines: []string{""}, Mode: ModeNormal, focused: true,
		styles: styles, styleNoteMarker: lipgloss.NewStyle().Foreground(styles.Accent).Bold(true),
	}
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m *Model) SetContent(content string) {
	if content == "" {
		m.lines = []string{""}
	} else {
		m.lines = strings.Split(content, "\n")
	}
	m.origContent = content
	m.rowCache = nil // release the previous document's per-row render work
	m.savedLines = append([]string(nil), m.lines...)
	m.dirtyRows = make(map[int]struct{})
	m.revision++
	m.savedRevision = m.revision
	m.Dirty = false
	m.cursorRow = 0
	m.cursorCol = 0
	m.viewOffset = 0
	// Drop any active visual selection: its anchor refers to the old buffer and
	// could point past the end of the new one, panicking GetSelection/ReplaceRange.
	m.Mode = ModeNormal
	m.selectionAnchor = Position{}
	m.pendingG = false
	// Loading a fresh buffer starts a new edit history; undoing across a chapter
	// switch would restore text into the wrong file.
	m.undoStack = nil
	m.redoStack = nil
	m.undoBytes, m.redoBytes = 0, 0
	m.insertUndoArmed = false
}

// SetRecoveredContent loads content into the buffer while pinning the saved
// baseline to orig (the on-disk file's contents). Unlike SetContent, the buffer
// is therefore Dirty whenever the recovered content differs from what is on
// disk, so a crash-recovered swap reads as unsaved until the user saves it.
func (m *Model) SetRecoveredContent(content, orig string) {
	m.SetContent(content)
	m.origContent = orig
	m.savedLines = strings.Split(orig, "\n")
	if orig == "" {
		m.savedLines = []string{""}
	}
	m.dirtyRows = make(map[int]struct{})
	if len(m.lines) == len(m.savedLines) {
		for row := range m.lines {
			m.updateDirtyRow(row)
		}
	}
	m.Dirty = content != orig
}

func (m Model) Content() string {
	return strings.Join(m.lines, "\n")
}

// Revision is incremented whenever buffer content changes. Consumers can use it
// to invalidate derived state without joining or comparing the whole document.
func (m Model) Revision() uint64 { return m.revision }

func (m *Model) MarkClean() {
	m.origContent = m.Content()
	m.savedLines = append(m.savedLines[:0], m.lines...)
	m.dirtyRows = make(map[int]struct{})
	m.savedRevision = m.revision
	m.Dirty = false
}

// CursorPosition returns the cursor's current (row, col) as rune indices, for
// persisting session state.
func (m Model) CursorPosition() (row, col int) {
	return m.cursorRow, m.cursorCol
}

// CursorViewportRow returns the cursor's zero-based row in the rendered editor
// viewport. It is useful to containers that need to verify composed layout.
func (m *Model) CursorViewportRow() int {
	display := m.buildDisplayLines()
	return m.findCursorDisplayLine(display) - m.viewOffset
}

// PendingState exposes the compact Vim count/operator identity for chrome.
func (m Model) PendingState() (int, string) {
	op := m.pendingOp
	if m.pendingReplace {
		op = "r"
	}
	return m.count, op
}

// SetCursorPosition moves the cursor to (row, col), clamping into the current
// buffer so a restored position from a since-shortened file can't land out of
// bounds. It scrolls the target into view. Intended for restoring a saved
// session position right after SetContent.
func (m *Model) SetCursorPosition(row, col int) {
	if len(m.lines) == 0 {
		m.lines = []string{""}
	}
	if row < 0 {
		row = 0
	}
	if row > len(m.lines)-1 {
		row = len(m.lines) - 1
	}
	m.cursorRow = row
	if col < 0 {
		col = 0
	}
	if max := runeLen(m.lines[row]); col > max {
		col = max
	}
	m.cursorCol = col
	m.ensureCursorVisible()
}

func (m *Model) Focus() {
	m.focused = true
}

func (m *Model) Blur() {
	m.focused = false
}

func (m Model) Focused() bool {
	return m.focused
}

func (m *Model) WordCount() int {
	if m.wordCountRevision == m.revision {
		return m.wordCountCache
	}
	count := 0
	for _, line := range m.lines {
		count += len(strings.Fields(line))
	}
	m.wordCountRevision, m.wordCountCache = m.revision, count
	return count
}

func (m *Model) SetSize(width, height int) {
	if width == m.width && height == m.height {
		return
	}
	m.width = width
	m.height = height
	// Re-anchor the viewport around the cursor now that the real size is known.
	// Crucial at startup: OpenPath restores the cursor before the first
	// WindowSizeMsg (height still 0), so the viewport must be recomputed here or
	// a restored end-of-file cursor leaves the pane scrolled past the content.
	m.ensureCursorVisible()
}

// GoToLine moves cursor to specified logical line
func (m *Model) GoToLine(line int) {
	if line < 0 {
		line = 0
	}
	if line >= len(m.lines) {
		line = len(m.lines) - 1
	}
	m.cursorRow = line
	m.cursorCol = 0
	m.ensureCursorVisible()
}

func (m Model) Update(msg tea.Msg) (Model, tea.Cmd) {
	beforeRevision, beforeRows, beforeRow := m.revision, len(m.lines), m.cursorRow
	beforeLine := ""
	if beforeRow >= 0 && beforeRow < len(m.lines) {
		beforeLine = m.lines[beforeRow]
	}
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch m.Mode {
		case ModeInsert:
			m.handleInsertMode(msg)
		case ModeVisual:
			m.handleVisualMode(msg)
		default:
			m.handleNormalMode(msg)
		}
	}

	if m.revision == beforeRevision && (len(m.lines) != beforeRows || beforeRow >= len(m.lines) || m.lines[beforeRow] != beforeLine) {
		m.finalizeMutation()
	}
	if m.revision != beforeRevision {
		if len(m.lines) == beforeRows {
			m.updateDirtyRow(beforeRow)
		} else {
			// Line count changed: per-row map indices no longer align with the saved
			// snapshot, so recompute dirtiness wholesale (O(n), but the splice was too).
			m.recomputeDirty()
		}
	}

	// Keep note anchors tracking the text they were attached to.
	if m.revision != beforeRevision && len(m.notes) > 0 {
		m.relocateNotes()
	}

	return m, nil
}
