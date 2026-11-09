package theme

import "github.com/charmbracelet/lipgloss"

// Palette is the full set of colors the UI is built from. Values are hex strings
// so they can come straight from a [theme] config block. Apply turns a Palette
// into the concrete color/style vars the rest of the app uses.
type Palette struct {
	Primary   string
	Secondary string
	Accent    string
	Muted     string
	Text      string
	TextDim   string
	Border    string
	BgDark    string
	BgPanel   string
	OnPrimary string
	OnAccent  string

	Amber   string // category headers
	ErrorFg string // error/notice text

	Heading1    string // editor H1 (defaults to Primary)
	Heading2    string
	Heading3    string
	EditorText  string
	CursorFg    string
	DiffAdded   string
	DiffRemoved string
}

// Default is the built-in dark palette.
func Default() Palette {
	return Palette{
		Primary:     "#7C3AED",
		Secondary:   "#6B7280",
		Accent:      "#10B981",
		Muted:       "#4B5563",
		Text:        "#F9FAFB",
		TextDim:     "#9CA3AF",
		Border:      "#374151",
		BgDark:      "#111827",
		BgPanel:     "#1F2937",
		OnPrimary:   "#FFFFFF",
		OnAccent:    "#052E16",
		Amber:       "#F59E0B",
		ErrorFg:     "#F87171",
		Heading1:    "#7C3AED",
		Heading2:    "#8B5CF6",
		Heading3:    "#A78BFA",
		EditorText:  "#E5E7EB",
		CursorFg:    "#FFFFFF",
		DiffAdded:   "#10B981",
		DiffRemoved: "#EF4444",
	}
}

// Light is a built-in light preset.
func Light() Palette {
	return Palette{
		Primary:     "#6D28D9",
		Secondary:   "#6B7280",
		Accent:      "#059669",
		Muted:       "#9CA3AF",
		Text:        "#111827",
		TextDim:     "#4B5563",
		Border:      "#D1D5DB",
		BgDark:      "#FFFFFF",
		BgPanel:     "#F3F4F6",
		OnPrimary:   "#FFFFFF",
		OnAccent:    "#000000",
		Amber:       "#B45309",
		ErrorFg:     "#B91C1C",
		Heading1:    "#6D28D9",
		Heading2:    "#7C3AED",
		Heading3:    "#8B5CF6",
		EditorText:  "#111827",
		CursorFg:    "#FFFFFF",
		DiffAdded:   "#047857",
		DiffRemoved: "#B91C1C",
	}
}

// Preset returns a named preset ("light" → Light, anything else → Default).
func Preset(name string) Palette {
	if name == "light" {
		return Light()
	}
	return Default()
}

// Styles is an immutable, model-owned concrete theme. Building a second style
// set never mutates models which already own one.
type Styles struct {
	Primary, Secondary, Accent, Muted, Text, TextDim, Border                lipgloss.Color
	BgDark, BgPanel, OnPrimary, OnAccent, Amber, ErrorFg                    lipgloss.Color
	CursorFg, DiffAdded, DiffRemov                                          lipgloss.Color
	StatusBar, StatusBarMode, StatusBarFile, StatusBarDirty, StatusBarError lipgloss.Style
	EditorNormal, EditorCursor                                              lipgloss.Style
	FileNavPane, FileNavSelected, FileNavItem, FileNavDir                   lipgloss.Style
	FileNavCategory, FileNavCategorySelected, Focused, Unfocused            lipgloss.Style
	EditorH1, EditorH2, EditorH3, EditorTextStyle                           lipgloss.Style
	EditorLineNumber, EditorCursorLine, EditorSelection                     lipgloss.Style
}

func NewStyles(p Palette) Styles {
	s := Styles{
		Primary: lipgloss.Color(p.Primary), Secondary: lipgloss.Color(p.Secondary), Accent: lipgloss.Color(p.Accent),
		Muted: lipgloss.Color(p.Muted), Text: lipgloss.Color(p.Text), TextDim: lipgloss.Color(p.TextDim),
		Border: lipgloss.Color(p.Border), BgDark: lipgloss.Color(p.BgDark), BgPanel: lipgloss.Color(p.BgPanel),
		OnPrimary: lipgloss.Color(p.OnPrimary), OnAccent: lipgloss.Color(p.OnAccent), Amber: lipgloss.Color(p.Amber),
		ErrorFg: lipgloss.Color(p.ErrorFg), CursorFg: lipgloss.Color(p.CursorFg),
		DiffAdded: lipgloss.Color(p.DiffAdded), DiffRemov: lipgloss.Color(p.DiffRemoved),
	}
	s.StatusBar = lipgloss.NewStyle().Background(s.BgPanel).Foreground(s.Text).Padding(0, 1)
	s.StatusBarMode = lipgloss.NewStyle().Background(s.Primary).Foreground(s.OnPrimary).Padding(0, 1).Bold(true)
	s.StatusBarFile = lipgloss.NewStyle().Foreground(s.TextDim)
	s.StatusBarDirty = lipgloss.NewStyle().Foreground(s.Accent).Bold(true)
	s.StatusBarError = lipgloss.NewStyle().Background(s.BgPanel).Foreground(s.ErrorFg).Bold(true).Padding(0, 1)
	s.EditorNormal = lipgloss.NewStyle().Background(s.BgDark)
	s.EditorCursor = lipgloss.NewStyle().Background(s.Primary).Foreground(s.CursorFg)
	s.FileNavPane = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(s.Border).Background(s.BgPanel)
	s.FileNavSelected = lipgloss.NewStyle().Background(s.Primary).Foreground(s.OnPrimary).Bold(true)
	s.FileNavItem = lipgloss.NewStyle().Foreground(s.TextDim).Background(s.BgPanel)
	s.FileNavDir = lipgloss.NewStyle().Foreground(s.Accent).Bold(true)
	s.FileNavCategory = lipgloss.NewStyle().Foreground(s.Amber).Bold(true).MarginTop(1)
	s.FileNavCategorySelected = lipgloss.NewStyle().Background(s.Amber).Foreground(s.BgDark).Bold(true).MarginTop(1)
	s.Focused = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(s.Primary).Background(s.BgDark)
	s.Unfocused = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(s.Border).Background(s.BgDark)
	s.EditorH1 = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(p.Heading1))
	s.EditorH2 = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(p.Heading2))
	s.EditorH3 = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color(p.Heading3))
	s.EditorTextStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(p.EditorText))
	s.EditorLineNumber = lipgloss.NewStyle().Foreground(s.Muted)
	s.EditorCursorLine = lipgloss.NewStyle().Background(s.BgPanel)
	s.EditorSelection = lipgloss.NewStyle().Background(s.Border).Foreground(s.Text)
	return s
}

var defaults = NewStyles(Default())

// Deprecated package values are immutable defaults retained for utility-only
// callers. Application models use their owned Styles and never mutate these.
var (
	Primary   lipgloss.Color
	Secondary lipgloss.Color
	Accent    lipgloss.Color
	Muted     lipgloss.Color
	Text      lipgloss.Color
	TextDim   lipgloss.Color
	Border    lipgloss.Color
	BgDark    lipgloss.Color
	BgPanel   lipgloss.Color
	OnPrimary lipgloss.Color
	OnAccent  lipgloss.Color
	Amber     lipgloss.Color
	ErrorFg   lipgloss.Color
	CursorFg  lipgloss.Color
	DiffAdded lipgloss.Color
	DiffRemov lipgloss.Color
)

// Styles, rebuilt by Apply from the colors above.
var (
	StatusBar               lipgloss.Style
	StatusBarMode           lipgloss.Style
	StatusBarFile           lipgloss.Style
	StatusBarDirty          lipgloss.Style
	StatusBarError          lipgloss.Style
	EditorNormal            lipgloss.Style
	EditorCursor            lipgloss.Style
	FileNavPane             lipgloss.Style
	FileNavSelected         lipgloss.Style
	FileNavItem             lipgloss.Style
	FileNavDir              lipgloss.Style
	FileNavCategory         lipgloss.Style
	FileNavCategorySelected lipgloss.Style
	Focused                 lipgloss.Style
	Unfocused               lipgloss.Style

	// Editor markdown/cursor styles (previously hardcoded in the editor package).
	EditorH1         lipgloss.Style
	EditorH2         lipgloss.Style
	EditorH3         lipgloss.Style
	EditorTextStyle  lipgloss.Style
	EditorLineNumber lipgloss.Style
	EditorCursorLine lipgloss.Style
	EditorSelection  lipgloss.Style
)

func init() { applyCompatibility(defaults) }

// Apply rebuilds every color and style var from p. Call at startup (and whenever
// the configured theme changes). Consumers that captured a style value before an
// Apply keep the old look, so panes that build styles lazily (or read the vars at
// render time) reflect the change.
func Apply(p Palette) {
	applyCompatibility(NewStyles(p))
}

func applyCompatibility(s Styles) {
	Primary = s.Primary
	Secondary = s.Secondary
	Accent, Muted, Text, TextDim, Border = s.Accent, s.Muted, s.Text, s.TextDim, s.Border
	BgDark, BgPanel, OnPrimary, OnAccent = s.BgDark, s.BgPanel, s.OnPrimary, s.OnAccent
	Amber, ErrorFg, CursorFg = s.Amber, s.ErrorFg, s.CursorFg
	DiffAdded, DiffRemov = s.DiffAdded, s.DiffRemov
	StatusBar, StatusBarMode, StatusBarFile = s.StatusBar, s.StatusBarMode, s.StatusBarFile
	StatusBarDirty, StatusBarError = s.StatusBarDirty, s.StatusBarError
	EditorNormal, EditorCursor = s.EditorNormal, s.EditorCursor
	FileNavPane, FileNavSelected, FileNavItem, FileNavDir = s.FileNavPane, s.FileNavSelected, s.FileNavItem, s.FileNavDir
	FileNavCategory, FileNavCategorySelected = s.FileNavCategory, s.FileNavCategorySelected
	Focused, Unfocused = s.Focused, s.Unfocused
	EditorH1, EditorH2, EditorH3, EditorTextStyle = s.EditorH1, s.EditorH2, s.EditorH3, s.EditorTextStyle
	EditorLineNumber, EditorCursorLine, EditorSelection = s.EditorLineNumber, s.EditorCursorLine, s.EditorSelection
}
