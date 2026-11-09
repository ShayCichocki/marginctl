package command

import (
	"strings"
)

// Editor modes for command filtering
const (
	ModeAny    = -1 // Available in all modes
	ModeNormal = 0  // Only in normal mode
	ModeVisual = 2  // Only in visual mode
)

// ResultType indicates what kind of output a command produces
type ResultType int

const (
	ResultNone ResultType = iota
	ResultText
	ResultList
	ResultError
	ResultDiff           // Shows diff with accept/reject options
	ResultVoiceGuide     // Proposed voice guide with accept/reject (writes a file on accept)
	ResultConfirm        // A yes/no confirmation prompt (Content is the question)
	ResultStateProposals // Proposed new state-bible entries (create-on-accept)
)

// StateProposal is a candidate story-bible entry extracted from prose.
type StateProposal struct {
	Category string // characters, locations, timeline, relationships, objects, threads, or custom
	Name     string
	Summary  string // a short description that seeds the new file's body
	Action   string // create, update, or append
	Target   string // project-relative state target when updating/appending
	Original string // exact target content used to build the review diff
	Proposed string // exact content to persist on accept
}

// SearchTarget locates a search hit so the results pane can jump to it. A zero
// value (empty Path) marks a non-navigable display line (a header/blank).
type SearchTarget struct {
	Path string
	Line int // 1-based line number within Path
}

// Result holds command execution output
type Result struct {
	Type    ResultType
	Content string   // For text and error types
	Items   []string // For list type

	// SearchTargets, when set, aligns 1:1 with Items and makes the list
	// navigable: pressing Enter on a line with a non-empty Path jumps to it.
	SearchTargets []SearchTarget

	// StateProposals holds candidate bible entries (Type == ResultStateProposals).
	StateProposals []StateProposal
	// StateSkipped counts model proposals dropped as malformed at parse time
	// (empty name, unknown category or action, control characters, bad target).
	StateSkipped int

	// Diff fields (used when Type == ResultDiff)
	OriginalText string // The text being replaced
	ProposedText string // The LLM's suggestion
	StartLine    int    // Where to apply the change (0-indexed)
	EndLine      int    // End of text range (inclusive)
	StartCol     int    // Column of the first selected char on StartLine
	EndCol       int    // Column of the last selected char on EndLine (inclusive)

	// DiffLabel names what a diff compares in the narrow header ("" means
	// "selection"). ReviewOnly marks a display-only diff: the pane never accepts
	// or rejects it itself, the app maps its keys to per-proposal actions.
	DiffLabel  string
	ReviewOnly bool

	// Identity of the buffer this result was produced against. Used to discard
	// a diff that would splice into a file/buffer that has since changed.
	File      string
	BufferGen int
	Intent    Intent
}

// Intent is a closed set of side-effect requests. Commands only validate an
// immutable Context and return one of these values; the root app owns policy,
// persistence, conflict handling, and asynchronous execution.
type Intent interface{ commandIntent() }

type (
	SaveIntent struct {
		Path, Content string
		Force, Quit   bool
	}
	PromptIntent          struct{ Kind string }
	PaneIntent            struct{ Mode string }
	QuitIntent            struct{ Force bool }
	CompileIntent         struct{}
	WordCountIntent       struct{}
	HelpIntent            struct{}
	SetupIntent           struct{}
	ConnectIntent         struct{ Provider string }
	DisconnectIntent      struct{}
	AuthStatusIntent      struct{}
	InitializeStateIntent struct{}
	CreateStateIntent     struct{ Category, Name, Body string }
	AppendStateIntent     struct{ Path, Text string }
)

func (SaveIntent) commandIntent()            {}
func (PromptIntent) commandIntent()          {}
func (PaneIntent) commandIntent()            {}
func (QuitIntent) commandIntent()            {}
func (CompileIntent) commandIntent()         {}
func (WordCountIntent) commandIntent()       {}
func (HelpIntent) commandIntent()            {}
func (SetupIntent) commandIntent()           {}
func (ConnectIntent) commandIntent()         {}
func (DisconnectIntent) commandIntent()      {}
func (AuthStatusIntent) commandIntent()      {}
func (InitializeStateIntent) commandIntent() {}
func (CreateStateIntent) commandIntent()     {}
func (AppendStateIntent) commandIntent()     {}

// Context provides commands with access to app state
type Context struct {
	// These will be set by the app before executing
	EditorContent string
	EditorDirty   bool
	CurrentFile   string
	BufferGen     int
	IsProject     bool
	HasState      bool

	// Selection info (for LLM commands)
	HasSelection      bool
	SelectedText      string
	SelectionStart    int // line number of the first selected char
	SelectionEnd      int // line number of the last selected char
	SelectionStartCol int // column of the first selected char on SelectionStart
	SelectionEndCol   int // column of the last selected char on SelectionEnd (inclusive)

	// State files (for continuity checking)
	StateFiles     map[string]string // filename -> content
	StateFilePaths map[string]string // immutable logical name -> path snapshot
	VoiceGuide     string
	VoiceGuidePath string

	// Project management
}

// ExecuteFunc is the signature for command execution
type ExecuteFunc func(ctx *Context) Result

// Command represents an executable action
type Command struct {
	ID          string      // Unique identifier (e.g., "file.save")
	Name        string      // Display name (e.g., "Save File")
	Description string      // Short help text
	Category    string      // For grouping (e.g., "File", "Chapter")
	Modes       []int       // Which editor modes this command is available in (nil = all)
	Execute     ExecuteFunc // The action
}

// Registry holds all registered commands
type Registry struct {
	commands []Command
	// byID maps a command ID to its index in commands. Storing an index rather
	// than a *Command avoids dangling pointers: appending to commands can move
	// its backing array, which would invalidate any cached element pointers.
	byID map[string]int
}

// NewRegistry creates a new command registry
func NewRegistry() *Registry {
	return &Registry{
		commands: make([]Command, 0),
		byID:     make(map[string]int),
	}
}

// Register adds a command to the registry
func (r *Registry) Register(cmd Command) {
	r.commands = append(r.commands, cmd)
	r.byID[cmd.ID] = len(r.commands) - 1
}

// Get retrieves a command by ID (nil if none). The pointer is computed from the
// current backing array on each call, so it is always valid.
func (r *Registry) Get(id string) *Command {
	idx, ok := r.byID[id]
	if !ok {
		return nil
	}
	return &r.commands[idx]
}

// All returns all registered commands
func (r *Registry) All() []Command {
	return r.commands
}

// Filter returns commands matching the search string
// Matches against ID, Name, and Description (case-insensitive)
func (r *Registry) Filter(search string) []Command {
	if search == "" {
		return r.commands
	}

	search = strings.ToLower(search)
	var matches []Command

	for _, cmd := range r.commands {
		if strings.Contains(strings.ToLower(cmd.ID), search) ||
			strings.Contains(strings.ToLower(cmd.Name), search) ||
			strings.Contains(strings.ToLower(cmd.Description), search) {
			matches = append(matches, cmd)
		}
	}

	return matches
}

// FilterByMode returns commands matching the search string and available in the given mode
func (r *Registry) FilterByMode(search string, mode int) []Command {
	var matches []Command

	for _, cmd := range r.commands {
		// Check if command is available in this mode
		if !commandAvailableInMode(cmd, mode) {
			continue
		}

		// If no search, include all matching mode commands
		if search == "" {
			matches = append(matches, cmd)
			continue
		}

		// Filter by search string
		searchLower := strings.ToLower(search)
		if strings.Contains(strings.ToLower(cmd.ID), searchLower) ||
			strings.Contains(strings.ToLower(cmd.Name), searchLower) ||
			strings.Contains(strings.ToLower(cmd.Description), searchLower) {
			matches = append(matches, cmd)
		}
	}

	return matches
}

// commandAvailableInMode checks if a command is available in the given mode
func commandAvailableInMode(cmd Command, mode int) bool {
	// nil or empty Modes means available in all modes
	if len(cmd.Modes) == 0 {
		return true
	}

	for _, m := range cmd.Modes {
		if m == ModeAny || m == mode {
			return true
		}
	}
	return false
}

// RegisterDefaults adds the standard commands to the registry
func (r *Registry) RegisterDefaults() {
	// Project commands - normal mode only
	r.Register(Command{
		ID:          "project.new",
		Name:        "New Project",
		Description: "Create a new project at a path",
		Category:    "Project",
		Modes:       []int{ModeNormal},
		Execute: func(ctx *Context) Result {
			if ctx.EditorDirty {
				return Result{Type: ResultError, Content: "Unsaved changes. Save first or use :q!"}
			}
			return Result{Type: ResultNone, Intent: PromptIntent{Kind: "new-project"}}
		},
	})

	r.Register(Command{
		ID:          "project.open",
		Name:        "Open Project",
		Description: "Open an existing project or file",
		Category:    "Project",
		Modes:       []int{ModeNormal},
		Execute: func(ctx *Context) Result {
			if ctx.EditorDirty {
				return Result{Type: ResultError, Content: "Unsaved changes. Save first or use :q!"}
			}
			return Result{Type: ResultNone, Intent: PromptIntent{Kind: "open-project"}}
		},
	})

	// File commands - normal mode only
	r.Register(Command{
		ID:          "file.save",
		Name:        "Save File",
		Description: "Save the current file",
		Category:    "File",
		Modes:       []int{ModeNormal},
		Execute: func(ctx *Context) Result {
			if ctx.CurrentFile == "" {
				// Unnamed buffer — open the save-as prompt instead of failing.
				return Result{Type: ResultNone, Intent: PromptIntent{Kind: "save"}}
			}
			return Result{Type: ResultNone, Intent: SaveIntent{Path: ctx.CurrentFile, Content: ctx.EditorContent}}
		},
	})

	r.Register(Command{
		ID:          "file.close",
		Name:        "Close File",
		Description: "Close current file (with dirty check)",
		Category:    "File",
		Modes:       []int{ModeNormal},
		Execute: func(ctx *Context) Result {
			if ctx.EditorDirty {
				return Result{Type: ResultError, Content: "Unsaved changes. Save first or use :q!"}
			}
			return Result{Type: ResultNone, Intent: QuitIntent{}}
		},
	})

	// Chapter commands - normal mode only
	r.Register(Command{
		ID:          "chapter.new",
		Name:        "New Chapter",
		Description: "Create a new chapter",
		Category:    "Chapter",
		Modes:       []int{ModeNormal},
		Execute: func(ctx *Context) Result {
			if !ctx.IsProject {
				return Result{Type: ResultError, Content: "Not in a project"}
			}
			if ctx.EditorDirty {
				return Result{Type: ResultError, Content: "Unsaved changes"}
			}
			return Result{Type: ResultNone, Intent: PromptIntent{Kind: "new-chapter"}}
		},
	})

	r.Register(Command{
		ID:          "chapter.list",
		Name:        "Show Chapters",
		Description: "Switch to project navigator",
		Category:    "Chapter",
		Modes:       []int{ModeNormal},
		Execute: func(ctx *Context) Result {
			if !ctx.IsProject {
				return Result{Type: ResultError, Content: "Not in a project"}
			}
			return Result{Type: ResultNone, Intent: PaneIntent{Mode: "project"}}
		},
	})

	// Whole-manuscript commands - normal mode only
	r.Register(Command{
		ID:          "manuscript.wordcount",
		Name:        "Word Count",
		Description: "Total and per-chapter word count for the whole manuscript",
		Category:    "Manuscript",
		Modes:       []int{ModeNormal},
		Execute: func(ctx *Context) Result {
			if !ctx.IsProject {
				return Result{Type: ResultError, Content: "Not in a project"}
			}
			return Result{Type: ResultNone, Intent: WordCountIntent{}}
		},
	})

	r.Register(Command{
		ID:          "manuscript.compile",
		Name:        "Compile Manuscript",
		Description: "Concatenate all chapters into a single Markdown document",
		Category:    "Manuscript",
		Modes:       []int{ModeNormal},
		Execute: func(ctx *Context) Result {
			if !ctx.IsProject {
				return Result{Type: ResultError, Content: "Not in a project"}
			}
			return Result{Type: ResultNone, Intent: CompileIntent{}}
		},
	})

	// Help & onboarding — available in every mode
	r.Register(Command{
		ID:          "help.keys",
		Name:        "Help: Keybindings",
		Description: "Show the keybinding and command reference",
		Category:    "Help",
		Execute: func(ctx *Context) Result {
			return Result{Type: ResultNone, Intent: HelpIntent{}}
		},
	})

	r.Register(Command{
		ID:          "app.setup",
		Name:        "Configure LLM Provider",
		Description: "First-time setup: choose a provider and save an API key",
		Category:    "Help",
		Modes:       []int{ModeNormal},
		Execute: func(ctx *Context) Result {
			return Result{Type: ResultNone, Intent: SetupIntent{}}
		},
	})

	r.Register(Command{
		ID:          "auth.connect",
		Name:        "Connect ChatGPT",
		Description: "Connect ChatGPT/Codex OAuth auth (:connect openai)",
		Category:    "Help",
		Modes:       []int{ModeNormal},
		Execute: func(ctx *Context) Result {
			return Result{Type: ResultNone, Intent: ConnectIntent{Provider: "openai"}}
		},
	})

	r.Register(Command{
		ID:          "auth.disconnect",
		Name:        "Disconnect ChatGPT",
		Description: "Delete the local ChatGPT auth store without remote revocation",
		Category:    "Help",
		Modes:       []int{ModeNormal},
		Execute: func(ctx *Context) Result {
			return Result{Type: ResultNone, Intent: DisconnectIntent{}}
		},
	})

	r.Register(Command{
		ID:          "auth.status",
		Name:        "ChatGPT Auth Status",
		Description: "Show redacted ChatGPT auth-store and activation status",
		Category:    "Help",
		Modes:       []int{ModeNormal},
		Execute: func(ctx *Context) Result {
			return Result{Type: ResultNone, Intent: AuthStatusIntent{}}
		},
	})

	// View commands - normal mode only
	r.Register(Command{
		ID:          "outline.show",
		Name:        "Show Outline",
		Description: "Switch to document outline",
		Category:    "View",
		Modes:       []int{ModeNormal},
		Execute: func(ctx *Context) Result {
			return Result{Type: ResultNone, Intent: PaneIntent{Mode: "outline"}}
		},
	})

	r.Register(Command{
		ID:          "state.init",
		Name:        "Initialize State",
		Description: "Create missing state directories and starter files",
		Category:    "State",
		Modes:       []int{ModeNormal},
		Execute: func(ctx *Context) Result {
			if !ctx.IsProject {
				return Result{Type: ResultError, Content: "Not in a project"}
			}
			return Result{Type: ResultNone, Intent: InitializeStateIntent{}}
		},
	})

	r.Register(Command{
		ID:          "state.show",
		Name:        "Show State Files",
		Description: "Switch to state navigator",
		Category:    "State",
		Modes:       []int{ModeNormal},
		Execute: func(ctx *Context) Result {
			if !ctx.IsProject {
				return Result{Type: ResultError, Content: "Not in a project"}
			}
			if !ctx.HasState {
				return Result{Type: ResultError, Content: "No state/ directory. Run :InitState first"}
			}
			return Result{Type: ResultNone, Intent: PaneIntent{Mode: "state"}}
		},
	})

	// Visual mode commands - state management from selection
	r.Register(Command{
		ID:          "state.add-character",
		Name:        "Add as Character",
		Description: "Create character from selected text",
		Category:    "State",
		Modes:       []int{ModeVisual},
		Execute: func(ctx *Context) Result {
			if !ctx.HasSelection || strings.TrimSpace(ctx.SelectedText) == "" {
				return Result{Type: ResultError, Content: "No text selected"}
			}
			if !ctx.HasState {
				return Result{Type: ResultError, Content: "No state/ directory. Run :InitState first"}
			}
			return Result{Type: ResultNone, Intent: CreateStateIntent{Category: "characters", Name: ctx.SelectedText}}
		},
	})

	r.Register(Command{
		ID:          "state.add-location",
		Name:        "Add as Location",
		Description: "Create location from selected text",
		Category:    "State",
		Modes:       []int{ModeVisual},
		Execute: func(ctx *Context) Result {
			if !ctx.HasSelection || strings.TrimSpace(ctx.SelectedText) == "" {
				return Result{Type: ResultError, Content: "No text selected"}
			}
			if !ctx.HasState {
				return Result{Type: ResultError, Content: "No state/ directory. Run :InitState first"}
			}
			return Result{Type: ResultNone, Intent: CreateStateIntent{Category: "locations", Name: ctx.SelectedText}}
		},
	})

	r.Register(Command{
		ID:          "state.add-timeline",
		Name:        "Add to Timeline",
		Description: "Append selected text to timeline",
		Category:    "State",
		Modes:       []int{ModeVisual},
		Execute: func(ctx *Context) Result {
			if !ctx.HasSelection || strings.TrimSpace(ctx.SelectedText) == "" {
				return Result{Type: ResultError, Content: "No text selected"}
			}
			if !ctx.HasState {
				return Result{Type: ResultError, Content: "No state/ directory. Run :InitState first"}
			}
			return Result{Type: ResultNone, Intent: AppendStateIntent{Path: "timeline.md", Text: ctx.SelectedText}}
		},
	})
}
