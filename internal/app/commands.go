package app

import (
	"context"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ShayCichocki/marginctl/internal/command"
	"github.com/ShayCichocki/marginctl/internal/fsutil"
	"github.com/ShayCichocki/marginctl/internal/llmcmd"
	"github.com/ShayCichocki/marginctl/internal/project"
	"github.com/ShayCichocki/marginctl/internal/state"
)

// builtinCommand is one row of the ":" builtin command table. Every verb in
// verbs dispatches to run; run receives the verb that was typed so a family of
// related verbs can share one handler. The table is the single source for both
// dispatch (executeCommand) and completion (builtinColonVerbs).
type builtinCommand struct {
	verbs []string
	run   func(m *Model, verb, arg string) tea.Cmd
}

var builtinCommands = []builtinCommand{
	{[]string{"w", "w!", "wq", "wq!"}, (*Model).cmdWrite},
	{[]string{"q"}, (*Model).cmdQuit},
	{[]string{"q!"}, (*Model).cmdForceQuit},
	{[]string{"search"}, func(m *Model, _, arg string) tea.Cmd { return m.searchManuscript(arg) }},
	{[]string{"replace"}, func(m *Model, _, arg string) tea.Cmd { return m.replaceCommand(arg) }},
	{[]string{"model"}, (*Model).cmdModel},
	{[]string{"setup"}, func(m *Model, _, _ string) tea.Cmd { return m.startSetup(false) }},
	{[]string{"setup!"}, func(m *Model, _, _ string) tea.Cmd { return m.startSetup(true) }},
	{[]string{"connect"}, func(m *Model, _, arg string) tea.Cmd { return m.startConnect(arg) }},
	{[]string{"disconnect"}, func(m *Model, _, _ string) tea.Cmd { m.disconnectAuth(); return nil }},
	{[]string{"auth"}, func(m *Model, _, arg string) tea.Cmd { return m.showAuthStatus(arg) }},
	{[]string{"help"}, func(m *Model, _, _ string) tea.Cmd { return m.showHelp() }},
	{[]string{"recover"}, func(m *Model, _, _ string) tea.Cmd { return m.recoverSwap() }},
	{[]string{"discard"}, func(m *Model, _, _ string) tea.Cmd { return m.discardRecovery() }},
	{[]string{"note"}, func(m *Model, _, arg string) tea.Cmd { return m.addNoteCommand(arg) }},
	{[]string{"notes"}, func(m *Model, _, arg string) tea.Cmd { return m.listNotesCommand(arg) }},
	{[]string{"note-edit"}, (*Model).cmdNoteEdit},
	{[]string{"note-resolve"}, (*Model).cmdNoteResolve},
	{[]string{"note-delete"}, (*Model).cmdNoteDelete},
	{[]string{"notes-project"}, func(m *Model, _, arg string) tea.Cmd { return m.listProjectNotesCommand(arg) }},
	{[]string{"ChapterRename", "ChapterDuplicate", "ChapterArchive", "ChapterTrash", "ChapterRestore", "ChapterReorder", "ChapterInsert"},
		func(m *Model, verb, arg string) tea.Cmd { return m.chapterLifecycleCommand(verb, arg) }},
	{[]string{"proposal-review", "proposal-accept", "proposal-edit", "proposal-merge", "proposal-skip"},
		func(m *Model, verb, arg string) tea.Cmd { return m.proposalCommand(verb, arg) }},
	{[]string{"proposals"}, func(m *Model, _, _ string) tea.Cmd { m.showProposalList(); return nil }},
	{[]string{"panes", "ToggleNav"}, func(m *Model, _, _ string) tea.Cmd { m.toggleLeftPane(); return nil }},
	{[]string{"DistractionFree"}, func(m *Model, _, _ string) tea.Cmd { m.toggleDistractionFree(); return nil }},
	{[]string{"ack"}, func(m *Model, _, _ string) tea.Cmd { m.notifications.Acknowledge(); return nil }},
	{[]string{"notifications"}, (*Model).cmdNotifications},
	{[]string{"navwidth"}, (*Model).cmdNavWidth},
	{[]string{"resultswidth"}, (*Model).cmdResultsWidth},
}

// builtinCommandByVerb indexes the table by verb for dispatch.
var builtinCommandByVerb = func() map[string]builtinCommand {
	byVerb := make(map[string]builtinCommand)
	for _, bc := range builtinCommands {
		for _, v := range bc.verbs {
			byVerb[v] = bc
		}
	}
	return byVerb
}()

func (m *Model) cmdWrite(verb, arg string) tea.Cmd {
	quit := verb == "wq" || verb == "wq!"
	force := verb == "w!" || verb == "wq!"
	if arg != "" {
		expanded, err := fsutil.ExpandHome(arg)
		if err != nil {
			m.err = err
			return nil
		}
		arg = expanded
	}
	return m.saveCommand(arg, quit, force)
}

// cmdQuit quits, blocking when the buffer is dirty (:q! force-quits).
func (m *Model) cmdQuit(_, _ string) tea.Cmd {
	return m.quitCmd()
}

// quitCmd is the shared ordinary-quit path for :q and :CloseFile.
func (m *Model) quitCmd() tea.Cmd {
	if m.editor.Dirty {
		m.setErrorNotice("Unsaved changes — :w to save, :q! to discard")
		return nil
	}
	m.saveSession()
	// An unresolved recovery offer survives an ordinary quit. Only an
	// explicit discard/force-quit or a successful save removes it.
	if !m.recovery.present {
		m.removeSwap()
	}
	return tea.Quit
}

// chapterOverlay returns the active buffer as a manuscript overlay, but only
// when it is a chapter of the current project.
func (m *Model) chapterOverlay() (path, content string) {
	// A clean buffer equals what it loaded; disk may be newer, so only a dirty
	// buffer overrides it.
	if m.project == nil || !m.editor.Dirty || !m.project.IsChapter(m.project.CurrentFile) {
		return "", ""
	}
	return m.project.CurrentFile, m.editor.Content()
}

// cmdForceQuit is an intentional discard, so it drops the swap too.
func (m *Model) cmdForceQuit(_, _ string) tea.Cmd {
	m.saveSession()
	m.removeSwap()
	return tea.Quit
}

func (m *Model) cmdNotifications(_, _ string) tea.Cmd {
	n, ok := m.notifications.Current()
	if !ok {
		m.setNotice("No notifications")
		return nil
	}
	content := n.Summary
	if n.Details != "" {
		content += "\n\n" + n.Details
	}
	if n.Operation != "" {
		content = n.Operation + "\n\n" + content
	}
	m.results.Show(command.Result{Type: command.ResultError, Content: content})
	m.focus = FocusResults
	m.editor.Blur()
	m.updateLayout()
	return nil
}

func (m *Model) cmdNavWidth(_, arg string) tea.Cmd {
	var width int
	if _, err := fmt.Sscanf(arg, "%d", &width); err != nil || width < minNavTotal {
		m.setErrorNotice(fmt.Sprintf("usage: :navwidth <%d+>", minNavTotal))
		return nil
	}
	m.navWidth = width
	m.updateLayout()
	return nil
}

func (m *Model) cmdResultsWidth(_, arg string) tea.Cmd {
	var width int
	if _, err := fmt.Sscanf(arg, "%d", &width); err != nil || width < minResultsTotal {
		m.setErrorNotice(fmt.Sprintf("usage: :resultswidth <%d+>", minResultsTotal))
		return nil
	}
	m.resultsWidth = width
	m.updateLayout()
	return nil
}

// cmdModel is the in-app model picker. No arg reports the current model; an
// arg switches it for the session (not persisted — writing config.toml would
// risk serializing an env-sourced api_key to disk; edit the file for a
// permanent change).
func (m *Model) cmdModel(_, arg string) tea.Cmd {
	if arg == "" {
		m.setNotice(fmt.Sprintf("model: %s (%s) — :model <id> to switch", m.llmClient.Model(), m.llmClient.Provider()))
		return nil
	}
	m.llmClient.SetModel(arg)
	m.config.LLM.Model = m.llmClient.Model()
	m.setNotice("model → " + m.llmClient.Model() + " (this session)")
	return nil
}

// commandContext snapshots the model into an immutable command.Context. The
// voice guide's content is loaded here, on the main goroutine, so every LLM
// prompt builder receives it (an unreadable guide degrades to none).
func (m *Model) commandContext() *command.Context {
	selectedText, selStart, selEnd := m.editor.GetSelection()
	selStartPos, selEndPos, _ := m.editor.SelectionRange()

	statePaths := make(map[string]string)
	voiceGuidePath := ""
	if m.state != nil && m.state.Exists() {
		// Display names (entry headings) are not unique, so entries sharing a
		// name in one category get their file stem appended to stay distinct.
		files := m.state.All()
		seen := make(map[string]int, len(files))
		for _, sf := range files {
			seen[sf.Category+"/"+sf.Name]++
		}
		for _, sf := range files {
			name := sf.Category + "/" + sf.Name
			if seen[name] > 1 {
				name += " [" + sf.Stem + ".md]"
			}
			if sf.Category == "style" && sf.Stem == "voice" {
				voiceGuidePath = sf.Path
			} else {
				statePaths[name] = sf.Path
			}
		}
	}
	voiceGuide := ""
	if voiceGuidePath != "" {
		if data, err := os.ReadFile(voiceGuidePath); err == nil {
			voiceGuide = string(data)
		} else {
			m.setErrorNotice("Voice guide unreadable, proceeding without it: " + err.Error())
		}
	}

	return &command.Context{
		EditorContent:     m.editor.Content(),
		EditorDirty:       m.editor.Dirty,
		CurrentFile:       m.project.CurrentFile,
		BufferGen:         m.bufferGen,
		IsProject:         m.project.IsProject,
		HasState:          m.state != nil && m.state.Exists(),
		HasSelection:      m.editor.HasSelection(),
		SelectedText:      selectedText,
		SelectionStart:    selStart,
		SelectionEnd:      selEnd,
		SelectionStartCol: selStartPos.Col,
		SelectionEndCol:   selEndPos.Col,
		StateFilePaths:    statePaths,
		VoiceGuide:        voiceGuide,
		VoiceGuidePath:    voiceGuidePath,
	}
}

func (m *Model) executeRegisteredCommand(id string) (tea.Model, tea.Cmd) {
	cmd := m.registry.Get(id)
	if cmd == nil {
		m.updateStatusBar()
		return m, nil
	}

	ctx := m.commandContext()

	// Single-flight: never stack two LLM calls, and never start a new one while a
	// diff/voice-guide proposal is still awaiting the user's accept/reject —
	// otherwise a second result could replace an undecided one.
	if strings.HasPrefix(id, "llm.") {
		if m.llmLoading {
			m.setErrorNotice("LLM request already in progress")
			m.updateStatusBar()
			return m, nil
		}
		if m.results.HasPendingProposal() {
			m.setErrorNotice("Decide the pending proposal first (y/n)")
			m.updateStatusBar()
			return m, nil
		}
	}

	// Handle LLM commands asynchronously. Each goes through startLLM so it is
	// cancelable (Esc while loading) and its response carries a dispatch ID.
	// Selection-based edit commands share ExecuteSelectionEdit; each differs only
	// in status text and prompt.
	if edit, ok := selectionEditCommands[id]; ok {
		return m, m.startLLM(edit.status, id, func(rc context.Context) tea.Cmd {
			return llmcmd.ExecuteSelectionEdit(m.llmClient, ctx, rc, id, edit.prompt)
		})
	}
	if id == "llm.continuity" {
		return m, m.startLLM("Checking continuity", id, func(rc context.Context) tea.Cmd {
			return llmcmd.ExecuteContinuityCheck(m.llmClient, ctx, rc)
		})
	}
	if id == "llm.summarize" {
		return m, m.startLLM("Summarizing chapter", id, func(rc context.Context) tea.Cmd {
			return llmcmd.ExecuteSummarize(m.llmClient, ctx, rc)
		})
	}
	if id == "llm.voice-guide" {
		return m, m.startLLM("Generating voice guide", id, func(rc context.Context) tea.Cmd {
			return llmcmd.ExecuteVoiceGuide(m.llmClient, ctx, rc)
		})
	}
	if id == "llm.extract-state" {
		return m, m.startLLM("Extracting state", id, func(rc context.Context) tea.Cmd {
			return llmcmd.ExecuteExtractState(m.llmClient, ctx, rc)
		})
	}

	result := cmd.Execute(ctx)
	if result.Intent != nil {
		return m.handleCommandIntent(result.Intent)
	}

	// Handle special results
	if result.Type == command.ResultText && result.Content == "__QUIT__" {
		m.saveSession()
		m.removeSwap()
		return m, tea.Quit
	}

	// Show results if there's output
	if result.Type == command.ResultText || result.Type == command.ResultList || result.Type == command.ResultError || result.Type == command.ResultDiff {
		if result.Content != "" || len(result.Items) > 0 || result.ProposedText != "" {
			m.results.Show(result)
			m.focus = FocusResults
			m.editor.Blur()
			m.updateLayout()
		}
	}

	m.updateStatusBar()
	return m, nil
}

func (m *Model) handleCommandIntent(intent command.Intent) (tea.Model, tea.Cmd) {
	switch v := intent.(type) {
	case command.SaveIntent:
		pg, fg := m.nextFSGeneration()
		req := saveRequest{path: v.Path, content: v.Content, force: v.Force, expected: m.loadedFingerprint, create: v.Path != m.project.CurrentFile}
		coordinator := m.persistence
		return m, fsCommand(pg, fg, "save-file", func() (any, error) { return coordinator.save(req) })
	case command.PromptIntent:
		m.prompt.Reset()
		switch v.Kind {
		case "new-project":
			m.pathInputMode, m.pathBuffer = "new", ""
			m.setPrompt(promptPath)
		case "open-project":
			m.pathInputMode, m.pathBuffer = "open", ""
			m.setPrompt(promptPath)
		case "save":
			m.pathInputMode, m.pathBuffer = "save", ""
			m.setPrompt(promptPath)
		case "new-chapter":
			m.chapterName = ""
			m.setPrompt(promptChapter)
		}
	case command.PaneIntent:
		m.showPane(v.Mode)
	case command.QuitIntent:
		if v.Force { // defensive: no command currently builds QuitIntent{Force: true}
			return m, m.cmdForceQuit("", "")
		}
		return m, m.quitCmd()
	case command.WordCountIntent:
		op, oc := m.chapterOverlay()
		total, per, err := m.project.WordCountWithOverlay(op, oc)
		if err != nil {
			m.err = err
			return m, nil
		}
		var b strings.Builder
		fmt.Fprintf(&b, "Manuscript: %d words across %d chapters\n\n", total, len(per))
		for _, c := range per {
			fmt.Fprintf(&b, "%7d  %s\n", c.Words, c.Chapter)
		}
		m.showResult(command.Result{Type: command.ResultText, Content: b.String()})
	case command.CompileIntent:
		pg, fg := m.nextFSGeneration()
		root := m.project.Root
		op, oc := m.chapterOverlay()
		return m, fsCommand(pg, fg, "compile", func() (any, error) {
			p := project.New()
			if err := p.Open(root); err != nil {
				return nil, err
			}
			return p.CompileToFileWithOverlay(op, oc)
		})
	case command.HelpIntent:
		m.showResult(command.Result{Type: command.ResultText, Content: m.helpText()})
	case command.SetupIntent:
		return m, m.startSetup(false)
	case command.ConnectIntent:
		return m, m.startConnect(v.Provider)
	case command.DisconnectIntent:
		m.disconnectAuth()
	case command.AuthStatusIntent:
		m.showResult(command.Result{Type: command.ResultText, Content: m.authStatusText()})
	case command.InitializeStateIntent:
		if m.state == nil {
			m.state = state.New(m.project.Root)
		}
		if err := m.state.Initialize(); err != nil {
			m.err = err
		} else {
			m.refreshStateNav()
			m.showResult(command.Result{Type: command.ResultText, Content: "State initialized"})
		}
	case command.CreateStateIntent:
		if m.state != nil {
			typ := state.StateCharacter
			if v.Category == "locations" {
				typ = state.StateLocation
			}
			if _, err := m.state.CreateStateFileWithBody(v.Name, typ, "", v.Body); err != nil {
				m.err = err
			} else {
				m.refreshStateNav()
			}
		}
	case command.AppendStateIntent:
		if m.state != nil {
			m.err = m.state.AppendToFile(v.Path, v.Text)
		}
	}
	m.updateStatusBar()
	return m, nil
}

// executeRegisteredCommandCmd runs a registry command by ID and returns just its
// tea.Cmd (the model mutates in place through the pointer receiver). Lets a
// ":"-command aliases reuse a registry command's implementation.
func (m *Model) executeRegisteredCommandCmd(id string) tea.Cmd {
	_, cmd := m.executeRegisteredCommand(id)
	return cmd
}

func (m *Model) executeCommand(cmd string) tea.Cmd {
	cmd = strings.TrimSpace(cmd)

	// Split off an optional argument so ":w path.md" / ":wq path.md" work.
	verb, arg := cmd, ""
	if i := strings.IndexByte(cmd, ' '); i >= 0 {
		verb = cmd[:i]
		arg = strings.TrimSpace(cmd[i+1:])
	}

	if bc, ok := builtinCommandByVerb[verb]; ok {
		return bc.run(m, verb, arg)
	}
	// PascalCase aliases (and hidden lowercase back-compat aliases) dispatch to
	// a registry command by ID.
	if id, ok := colonAliases[verb]; ok {
		return m.executeRegisteredCommandCmd(id)
	}
	if id, ok := hiddenColonAliases[verb]; ok {
		return m.executeRegisteredCommandCmd(id)
	}
	// Feedback for a mistyped command rather than a silent no-op.
	if verb != "" {
		m.setErrorNotice("unknown command: :" + verb)
	}
	return nil
}
