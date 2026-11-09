package app

import (
	"context"
	"errors"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ShayCichocki/marginctl/internal/command"
	"github.com/ShayCichocki/marginctl/internal/config"
	"github.com/ShayCichocki/marginctl/internal/editor"
	"github.com/ShayCichocki/marginctl/internal/fsutil"
	"github.com/ShayCichocki/marginctl/internal/headerbar"
	inputmodel "github.com/ShayCichocki/marginctl/internal/input"
	"github.com/ShayCichocki/marginctl/internal/llm"
	"github.com/ShayCichocki/marginctl/internal/llmcmd"
	"github.com/ShayCichocki/marginctl/internal/notification"
	"github.com/ShayCichocki/marginctl/internal/outline"
	"github.com/ShayCichocki/marginctl/internal/project"
	"github.com/ShayCichocki/marginctl/internal/projectnav"
	"github.com/ShayCichocki/marginctl/internal/results"
	"github.com/ShayCichocki/marginctl/internal/state"
	"github.com/ShayCichocki/marginctl/internal/statenav"
	"github.com/ShayCichocki/marginctl/internal/statusbar"
	"github.com/ShayCichocki/marginctl/internal/theme"
)

type Focus int

const (
	FocusEditor Focus = iota
	FocusLeftPane
	FocusResults
)

// selectionEditCommands maps each selection-based LLM edit command to its
// loading-status text and prompt builder. All are dispatched through
// llmcmd.ExecuteSelectionEdit, so adding a writing command here (plus its
// registration + prompt in internal/llm) is all it takes to wire a new one.
var selectionEditCommands = map[string]struct {
	status string
	prompt llmcmd.PromptFunc
}{
	"llm.tighten":        {"Analyzing text", llm.TightenParagraphPrompt},
	"llm.show-dont-tell": {"Rewriting (show, don't tell)", llm.ShowDontTellPrompt},
	"llm.dialogue":       {"Polishing dialogue", llm.PolishDialoguePrompt},
	"llm.expand":         {"Expanding prose", llm.ExpandProsePrompt},
	"llm.rewrite-voice":  {"Rewriting in voice", llm.RewriteInVoicePrompt},
}

type LeftPaneMode int

const (
	PaneModeOutline LeftPaneMode = iota
	PaneModeProject
	PaneModeState
)

type Model struct {
	editor      editor.Model
	outline     outline.Model
	projectnav  projectnav.Model
	statenav    statenav.Model
	headerbar   headerbar.Model
	statusbar   statusbar.Model
	project     *project.Project
	state       *state.Manager
	config      config.Config
	llmClient   *llm.Client
	persistence persistenceCoordinator
	styles      theme.Styles

	// Results pane and the command registry (":"-command dispatch)
	results  results.Model
	registry *command.Registry

	focus                  Focus
	leftPaneMode           LeftPaneMode
	width                  int
	height                 int
	layout                 Layout
	leftPaneVisible        bool
	distractionFree        bool
	navWidth, resultsWidth int

	// bufferGen changes on every buffer load or mutation. An LLM diff carries
	// the gen it was produced against; if the gen has moved on by the time the
	// diff comes back (or is accepted), the diff is stale and dropped so it can
	// never splice into the wrong lines/file.
	bufferGen  int
	outlineGen uint64
	projectGen uint64
	fsGen      uint64

	// loadedFingerprint is the on-disk identity and content state of CurrentFile
	// when it was last loaded or saved. Plain saves must still match it.
	loadedFingerprint fsutil.Fingerprint

	// Crash-recovery state is identity-bound so an offer can never be applied to
	// a subsequently opened buffer. unnamedBufferID is stable across restarts.
	lastSwapContent string
	lastSwapWritten bool
	recovery        recoveryState
	unnamedBufferID string

	pendingAction *PendingAction
	nextActionID  uint64

	commandBuffer string
	// Tab-completion state for the ":" prompt: the candidate verbs matching the
	// typed prefix, the currently-selected index, and whether a cycle is active
	// (reset on any buffer edit so the next Tab recomputes from the new prefix).
	completionMatches []string
	completionIdx     int
	completionActive  bool
	searchBuffer      string
	chapterName       string
	stateType         state.StateType
	stateCategory     string
	stateName         string
	categoryName      string
	llmLoading        bool
	// llmStatus is the short base label for the in-flight request (e.g.
	// "Checking continuity"); the animated spinner + elapsed time + cancel hint
	// are composed around it in llmStatusLine().
	llmStatus  string
	llmStart   time.Time
	llmSpinner int
	// streamBuf accumulates a streaming text result (continuity/summarize) as
	// deltas arrive; the results pane re-renders it on each token. streamCh is the
	// live event channel the delta handler re-subscribes to (nil when idle).
	streamBuf string
	streamCh  <-chan llm.StreamEvent
	// llmCancel aborts the in-flight LLM request (nil when none is running).
	// llmReqID is a monotonic dispatch counter: it stamps each request's
	// ResponseMsg and is bumped on cancellation so a late/aborted response can
	// be identified and dropped.
	llmCancel    context.CancelFunc
	llmReqID     int
	llmOperation llmcmd.Operation

	// pendingResultsFocus / pendingPaneFocus record that a pane holds content the
	// user should act on but the terminal was too narrow to render it, so focus was
	// kept off it (see reconcileFocus). When the pane fits again, focus returns to
	// it — so a diff/confirmation is never accepted sight-unseen, and never stranded.
	pendingResultsFocus bool
	pendingPaneFocus    bool

	// proposalReviewN is the 1-based proposal shown by :proposal-review (0 when
	// none). It only counts while the pane still shows that review (see
	// reviewingProposal), so a replaced pane can never resurrect the list.
	proposalReviewN int

	// commandFromResults records that the ":" prompt was opened from the results
	// pane, where only proposal commands and :w/:q are accepted.
	commandFromResults bool

	// Path input for project commands
	pathInputMode string // "new" or "open"
	pathBuffer    string

	// Interactive LLM provider setup (:setup / first run). setupStep walks
	// provider → API key; the key is masked in the view.
	setupStep     int
	setupProvider string
	setupBuffer   string

	// Interactive OAuth/device auth connection (:connect openai). The state
	// machine mirrors setup while long-running auth work runs through tea.Cmds.
	connect          connectState
	connectAttemptID int
	connectCancel    context.CancelFunc
	connectDeps      connectDeps

	err error

	// Error surfaces (three, with different lifetimes):
	//   - err: a hard failure; the only one promoted (on the next key) to a
	//     persistent notification and shown until acknowledged.
	//   - notice/noticeIsErr: a transient bottom-line message (hints, blocked
	//     actions, soft errors), rendered until the next keystroke. Always set
	//     through setNotice/setErrorNotice/clearNotice so the pair stays in
	//     sync; transient notices are never promoted, even error-styled ones.
	//   - notifications: the persistent, typed notification queue.
	notice         string
	noticeIsErr    bool
	notifications  notification.Model
	prompt         inputmodel.Model
	commandHistory []string
	promptState    promptKind
	promptMainKey  promptMainViewKey
	promptMainView string
}

type promptMainViewKey struct {
	revision       uint64
	row, col       int
	focus          Focus
	leftMode       LeftPaneMode
	layout         Layout
	resultsVisible bool
	streamBytes    int
	outlineGen     uint64
}

type outlineDebounceMsg struct{ generation uint64 }

type outlineParsedMsg struct {
	generation uint64
	headings   []outline.Heading
}

const outlineDebounce = 75 * time.Millisecond

func New() Model {
	registry := command.NewRegistry()
	registry.RegisterDefaults()

	cfg, cfgErr := config.Load()

	styles := theme.NewStyles(paletteFromConfig(cfg.Theme))

	// Create LLM client
	llmClient := llm.NewClient(cfg.LLM)

	// Register LLM commands
	llmcmd.RegisterCommands(registry, llmClient)

	// Surface config problems on the bottom bar at startup (rendered until the
	// first keystroke) rather than swallowing them: a parse error or an unknown
	// key otherwise leaves the LLM silently misconfigured.
	var notice string
	noticeIsErr := false
	if cfgErr != nil {
		notice = "config error: " + cfgErr.Error()
		noticeIsErr = true
	} else if len(cfg.Notes) > 0 {
		notice = strings.Join(cfg.Notes, "; ")
		noticeIsErr = true
	} else if !config.FileExists() && !llmClient.IsConfigured() {
		// First run: no config file and no key from the environment. The editor
		// is fully usable without an LLM, so this is a hint, not an error.
		notice = "No LLM provider configured — :setup to connect one, :help for keybindings (the editor works without an LLM)"
	}

	m := Model{
		editor:          editor.NewWithStyles(styles),
		outline:         outline.New(),
		projectnav:      projectnav.New(),
		statenav:        statenav.New(),
		headerbar:       headerbar.New(),
		statusbar:       statusbar.New(),
		project:         project.New(),
		results:         results.New(),
		registry:        registry,
		config:          cfg,
		llmClient:       llmClient,
		styles:          styles,
		connectDeps:     defaultConnectDeps(),
		unnamedBufferID: loadUnnamedBufferID(),
		focus:           FocusEditor,
		leftPaneMode:    PaneModeOutline,
		leftPaneVisible: true,
		navWidth:        24,
		resultsWidth:    60,
		notice:          notice,
		noticeIsErr:     noticeIsErr,
	}
	m.recovery = detectRecovery("", m.unnamedBufferID)
	if m.recovery.present {
		// An informational prompt, not a failure: keep it out of the red error
		// style (and off the error-promotion path) so it reads as guidance.
		m.setNotice("Unsaved changes recovered from a previous session — :recover to restore, :discard to delete")
	}
	return m
}

// paletteFromConfig builds a theme palette from the [theme] config: a preset
// ("dark"/"light") with optional per-color hex overrides layered on top.
func paletteFromConfig(tc config.ThemeConfig) theme.Palette {
	p := theme.Preset(tc.Preset)
	if tc.Primary != "" {
		p.Primary = tc.Primary
		p.Heading1 = tc.Primary
	}
	if tc.Accent != "" {
		p.Accent = tc.Accent
	}
	if tc.Text != "" {
		p.Text = tc.Text
		p.EditorText = tc.Text
	}
	if tc.Background != "" {
		p.BgDark = tc.Background
	}
	if tc.Heading != "" {
		p.Heading1 = tc.Heading
		p.Heading2 = tc.Heading
		p.Heading3 = tc.Heading
	}
	return p
}

func (m Model) Init() tea.Cmd {
	// Start the periodic swap-file autosave so a crash mid-edit is recoverable.
	return autosaveTick()
}

// errExternalChange is returned by a plain :w when CurrentFile changed on disk
// since it was loaded, so the write is refused until the user forces it (:w!).
var errExternalChange = errors.New("file changed on disk since load — :w! to overwrite")

var errSaveAsExists = errors.New("save-as target already exists — :w! <path> to overwrite")
