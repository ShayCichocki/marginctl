package app

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ShayCichocki/marginctl/internal/command"
)

// showHelp renders the keybinding/command reference in the results pane.
func (m *Model) showHelp() tea.Cmd {
	m.results.Show(command.Result{Type: command.ResultText, Content: m.helpText()})
	m.focus = FocusResults
	m.editor.Blur()
	m.updateLayout()
	m.updateStatusBar()
	return nil
}

// helpText builds the in-app reference: global keys, editor basics, ":"
// commands, and every registered command grouped by category (pulled live from
// the registry so new commands appear without touching this file).
func (m *Model) helpText() string {
	var b strings.Builder
	b.WriteString(`MARGINCTL HELP

Global keys (vim-only — panes and commands live at the : prompt)
  Esc        back out (pane → editor, cancel prompt/LLM request)
  Ctrl+C     cancel (like Esc; quitting is :q / :q!)

Editor (vim-like)
  i a o      enter Insert mode        v          Visual mode
  h j k l    move                     w b e      word motions
  0 $        line start/end           { }        paragraphs
  gg G       top / bottom             %          matching bracket
  d c y      operators (dw, cw, y}…)  dd yy cc   line forms
  p P        paste after/before       r          replace char
  u Ctrl+R   undo / redo              /pat n N   search
  Ctrl+F/B   page down / up           Ctrl+D/U   half-page down / up
  counts compose: 3j, 2dd, d3w

: commands
  :w [path]    save (path = save-as)     :w! overwrite changed/existing target
  :q :q!       quit / force quit         :wq :wq! save and quit
  :help        this screen               :setup   configure API-key provider
  :connect openai  browser ChatGPT OAuth login   :connect openai --device  device login
  :auth        show local ChatGPT auth status     :disconnect  delete local auth store
  :model [id]  show / switch model       :search <text>  search all chapters
  :replace old/new  replace across chapters (confirm first)
  :note <text>  attach a note to the Visual selection   :notes [open|resolved|orphans]
  :note-edit N text   :note-resolve N   :note-delete N   :notes-project [filter]
  :ChapterRename name   :ChapterReorder N   :ChapterInsert N name
  :ChapterDuplicate [name]   :ChapterArchive   :ChapterTrash   :ChapterRestore path
  :ShowChapters / :ShowState / :ShowOutline  open a left-pane navigator
  :ToggleNav / :panes  hide/show navigator   :DistractionFree  editor-only canvas
  :navwidth N / :resultswidth N  resize panes (persisted per project)
  :notifications inspect persistent failure  :ack acknowledge it
  :recover     restore crash-recovered changes   :discard  delete them
  Tab / Shift+Tab  cycle command-name completions
  Prompt editing: Left/Right Home/End Delete Ctrl+W; Up/Down command history; path Tab completion
  Navigator j/k/g/G only move selection; Enter opens. Too-small mode blocks hidden actions.
  From Visual mode ":" keeps the selection — run :Tighten, :Expand, :AddCharacter, :note, …
  :Continuity aborts on unreadable state and reports checked/omitted state entries.
  :ExtractState proposals: y apply all, n discard all, or ":" from the results pane
  (only while proposals are pending; just these, :w and :q run there):
  :proposal-review N   :proposal-accept N   :proposal-edit N text   :proposal-merge N
  :proposal-skip N     :proposals  show the list again
  In a review: y accept it, n skip it, Esc or q back to the list (nothing is discarded).

LLM auth
  :setup keeps API-key providers separate: anthropic, openai, and vercel.
  :connect openai activates provider chatgpt and stores ChatGPT OAuth locally.
  Auth store path is auth.json under the platform data directory.
  Unix: ${XDG_DATA_HOME:-~/.local/share}/marginctl/auth.json
  macOS: ~/Library/Application Support/marginctl/auth.json
  Windows: %AppData%\marginctl\auth.json
  MARGINCTL_LLM_PROVIDER overrides config and can block chatgpt activation until unset.
  ChatGPT OAuth uses an opencode/Codex-style private contract, not a stable public OpenAI API.
  If that private contract changes, reconnect with :connect openai or switch to API-key openai.

: named commands (PascalCase verbs)
`)

	// List the PascalCase aliases (sorted) with the registry command they run, so
	// this stays in sync with colonAliases.
	aliasVerbs := make([]string, 0, len(colonAliases))
	for v := range colonAliases {
		aliasVerbs = append(aliasVerbs, v)
	}
	sort.Strings(aliasVerbs)
	for _, v := range aliasVerbs {
		name := colonAliases[v]
		if c := m.registry.Get(colonAliases[v]); c != nil {
			name = c.Name
		}
		fmt.Fprintf(&b, "  :%-14s %s\n", v, name)
	}

	b.WriteString("\nAll commands (run via : or the matching PascalCase verb)\n")

	// Group registry commands by category, preserving registration order.
	var categories []string
	byCategory := make(map[string][]command.Command)
	for _, c := range m.registry.All() {
		if _, seen := byCategory[c.Category]; !seen {
			categories = append(categories, c.Category)
		}
		byCategory[c.Category] = append(byCategory[c.Category], c)
	}
	for _, cat := range categories {
		fmt.Fprintf(&b, "\n  %s\n", cat)
		for _, c := range byCategory[cat] {
			fmt.Fprintf(&b, "    %-24s %s\n", c.Name, c.Description)
		}
	}
	return b.String()
}
