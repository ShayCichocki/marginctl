package llmcmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ShayCichocki/marginctl/internal/command"
	"github.com/ShayCichocki/marginctl/internal/fsutil"
	"github.com/ShayCichocki/marginctl/internal/llm"
)

// RegisterCommands adds LLM commands to the registry
func RegisterCommands(registry *command.Registry, client *llm.Client) {
	// Selection-based edit commands. Each rewrites the visual selection and returns
	// a diff for accept/reject; they differ only in prompt, so they share
	// ExecuteSelectionEdit (dispatched in app.executeRegisteredCommand).
	for _, c := range []struct{ id, name, desc string }{
		{"llm.tighten", "Tighten Text", "Tighten and improve selected text"},
		{"llm.show-dont-tell", "Show, Don't Tell", "Dramatize telling in the selection through action and sensory detail"},
		{"llm.dialogue", "Polish Dialogue", "Make selected dialogue natural and character-distinct"},
		{"llm.expand", "Expand Prose", "Expand the selection with vivid, concrete detail"},
		{"llm.rewrite-voice", "Rewrite in Voice", "Rewrite the selection to match the project voice guide"},
	} {
		registry.Register(command.Command{
			ID:          c.id,
			Name:        c.name,
			Description: c.desc,
			Category:    "LLM",
			Modes:       []int{command.ModeVisual},
			Execute: func(ctx *command.Context) command.Result {
				// Placeholder — actual execution is async via ExecuteSelectionEdit.
				return command.Result{Type: command.ResultError, Content: "Use async execution"}
			},
		})
	}

	registry.Register(command.Command{
		ID:          "llm.continuity",
		Name:        "Check Continuity",
		Description: "Check chapter for continuity issues",
		Category:    "LLM",
		// Operates on the whole chapter (not a selection), so it belongs in the
		// Normal mode as well as Visual.
		Modes: []int{command.ModeNormal, command.ModeVisual},
		Execute: func(ctx *command.Context) command.Result {
			// This is a placeholder - actual execution is async via ExecuteAsync
			return command.Result{Type: command.ResultError, Content: "Use async execution"}
		},
	})

	registry.Register(command.Command{
		ID:          "llm.summarize",
		Name:        "Summarize Chapter",
		Description: "Summarize the current chapter",
		Category:    "LLM",
		// Whole-chapter operation, so available in Normal and Visual modes.
		Modes: []int{command.ModeNormal, command.ModeVisual},
		Execute: func(ctx *command.Context) command.Result {
			// This is a placeholder - actual execution is async via ExecuteAsync
			return command.Result{Type: command.ResultError, Content: "Use async execution"}
		},
	})

	registry.Register(command.Command{
		ID:          "llm.voice-guide",
		Name:        "Generate Voice Guide",
		Description: "Create or update the project voice guide from the current text",
		Category:    "LLM",
		Modes:       []int{command.ModeNormal, command.ModeVisual},
		Execute: func(ctx *command.Context) command.Result {
			// This is a placeholder - actual execution is async via ExecuteAsync
			return command.Result{Type: command.ResultError, Content: "Use async execution"}
		},
	})

	registry.Register(command.Command{
		ID:          "llm.extract-state",
		Name:        "Extract State",
		Description: "Propose new character/location bible entries from the chapter or selection",
		Category:    "LLM",
		Modes:       []int{command.ModeNormal, command.ModeVisual},
		Execute: func(ctx *command.Context) command.Result {
			// Placeholder — actual execution is async via ExecuteExtractState.
			return command.Result{Type: command.ResultError, Content: "Use async execution"}
		},
	})
}

// ExecuteExtractState asks the model for candidate story-bible entries in the
// current chapter (or selection) and returns them as reviewable proposals. The
// app presents them for confirmation and writes the accepted ones.
func ExecuteExtractState(client *llm.Client, ctx *command.Context, reqCtx context.Context) tea.Cmd {
	return func() tea.Msg {
		errResult := func(msg string) tea.Msg {
			return ResponseMsg{CommandID: "llm.extract-state", Result: command.Result{Type: command.ResultError, Content: msg}}
		}
		if !client.IsConfiguredContext(reqCtx) {
			return errResult(client.UnconfiguredMessage(reqCtx))
		}
		text := ctx.EditorContent
		if ctx.HasSelection {
			text = ctx.SelectedText
		}
		if strings.TrimSpace(text) == "" {
			return errResult("No text to extract from. Open a chapter or select prose first.")
		}

		system, user := llm.ExtractStatePrompt(text)
		response, usage, err := client.Complete(reqCtx, system, user)
		if err != nil {
			return errResult(err.Error())
		}
		props, skipped, perr := parseStateProposals(response)
		if perr != nil {
			return errResult("could not parse model response: " + perr.Error())
		}
		return ResponseMsg{
			CommandID: "llm.extract-state",
			Usage:     usage,
			Result:    command.Result{Type: command.ResultStateProposals, StateProposals: props, StateSkipped: skipped},
		}
	}
}

// parseStateProposals extracts a JSON array of {category,name,summary} from the
// model response (tolerant of surrounding prose/code fences) and keeps only
// well-formed entries. Names are kept verbatim (they become entry headings, never
// paths); only genuinely malformed proposals are dropped, and their count is
// returned so the app can report it instead of failing silently.
func parseStateProposals(s string) ([]command.StateProposal, int, error) {
	start := strings.IndexByte(s, '[')
	end := strings.LastIndexByte(s, ']')
	if start < 0 || end < start {
		return nil, 0, fmt.Errorf("no JSON array in response")
	}
	var raw []struct {
		Category string `json:"category"`
		Name     string `json:"name"`
		Summary  string `json:"summary"`
		Action   string `json:"action"`
		Target   string `json:"target"`
		Original string `json:"original"`
		Proposed string `json:"proposed"`
	}
	if err := json.Unmarshal([]byte(s[start:end+1]), &raw); err != nil {
		return nil, 0, err
	}
	var out []command.StateProposal
	skipped := 0
	for _, r := range raw {
		name := strings.TrimSpace(r.Name)
		cat := strings.ToLower(strings.TrimSpace(r.Category))
		allowed := map[string]bool{"characters": true, "locations": true}
		// Extended proposal kinds carry an explicit operation. This preserves the
		// legacy parser's rejection of arbitrary categories while allowing the
		// richer extraction schema.
		if strings.TrimSpace(r.Action) != "" {
			allowed["timeline"], allowed["relationships"], allowed["objects"], allowed["threads"], allowed["open_threads"] = true, true, true, true, true
		}
		if name == "" || !allowed[cat] || strings.IndexFunc(name, unicode.IsControl) >= 0 {
			skipped++
			continue
		}
		// Model output is untrusted (manuscript text can carry prompt injection):
		// a name is only ever an entry title (the app derives a safe slug
		// filename or looks up an existing entry), but an explicit target is a
		// path and must be a clean relative one.
		if target := strings.TrimSpace(r.Target); target != "" && fsutil.SafeRelPath(target) != nil {
			skipped++
			continue
		}
		action := strings.ToLower(strings.TrimSpace(r.Action))
		if action == "" {
			action = "create"
		}
		if action != "create" && action != "update" && action != "append" {
			skipped++
			continue
		}
		out = append(out, command.StateProposal{Category: cat, Name: name, Summary: strings.TrimSpace(r.Summary), Action: action, Target: strings.TrimSpace(r.Target), Original: r.Original, Proposed: r.Proposed})
	}
	return out, skipped, nil
}

// PromptFunc builds the (system, user) prompt pair for a selection-based edit from
// the selected text and the project voice guide.
type PromptFunc func(text, voiceGuide string) (system, user string)

// ExecuteSelectionEdit runs any selection-based diff command (tighten, show-don't-tell,
// polish-dialogue, expand) asynchronously. It requires a visual-mode selection and
// returns a ResultDiff carrying character offsets + File/BufferGen so the accepted
// edit splices back into exactly the selected range of the buffer it was computed
// against (see the ResponseMsg/DiffAcceptedMsg staleness guards in handleLLMResponse and handleDiffAccepted in internal/app/llm_msgs.go). reqCtx
// bounds and can cancel the underlying request (per-call timeout + user abort).
func ExecuteSelectionEdit(client *llm.Client, ctx *command.Context, reqCtx context.Context, commandID string, prompt PromptFunc) tea.Cmd {
	return func() tea.Msg {
		if !client.IsConfiguredContext(reqCtx) {
			return ResponseMsg{
				CommandID: commandID,
				Result: command.Result{
					Type:    command.ResultError,
					Content: client.UnconfiguredMessage(reqCtx),
				},
			}
		}

		if !ctx.HasSelection {
			return ResponseMsg{
				CommandID: commandID,
				Result: command.Result{
					Type:    command.ResultError,
					Content: "No text selected. Press 'v' to enter visual mode and select text first.",
				},
			}
		}

		system, user := prompt(ctx.SelectedText, ctx.VoiceGuide)

		response, usage, err := client.Complete(reqCtx, system, user)
		if err != nil {
			return ResponseMsg{
				CommandID: commandID,
				Result: command.Result{
					Type:    command.ResultError,
					Content: err.Error(),
				},
			}
		}

		return ResponseMsg{
			CommandID: commandID,
			File:      ctx.CurrentFile,
			BufferGen: ctx.BufferGen,
			Usage:     usage,
			Result: command.Result{
				Type:         command.ResultDiff,
				OriginalText: ctx.SelectedText,
				ProposedText: response,
				StartLine:    ctx.SelectionStart,
				EndLine:      ctx.SelectionEnd,
				StartCol:     ctx.SelectionStartCol,
				EndCol:       ctx.SelectionEndCol,
				File:         ctx.CurrentFile,
				BufferGen:    ctx.BufferGen,
			},
		}
	}
}

// ExecuteTighten runs the tighten command asynchronously. Thin wrapper over
// ExecuteSelectionEdit kept for its stable exported signature.
func ExecuteTighten(client *llm.Client, ctx *command.Context, reqCtx context.Context) tea.Cmd {
	return ExecuteSelectionEdit(client, ctx, reqCtx, "llm.tighten", llm.TightenParagraphPrompt)
}

// ExecuteVoiceGuide generates or updates the project's voice guide asynchronously.
func ExecuteVoiceGuide(client *llm.Client, ctx *command.Context, reqCtx context.Context) tea.Cmd {
	return func() tea.Msg {
		if !client.IsConfiguredContext(reqCtx) {
			return ResponseMsg{
				CommandID: "llm.voice-guide",
				Result: command.Result{
					Type:    command.ResultError,
					Content: client.UnconfiguredMessage(reqCtx),
				},
			}
		}

		if !ctx.IsProject {
			return ResponseMsg{
				CommandID: "llm.voice-guide",
				Result: command.Result{
					Type:    command.ResultError,
					Content: "Not in a project",
				},
			}
		}

		sample := ctx.EditorContent
		if ctx.HasSelection {
			sample = ctx.SelectedText
		}
		if strings.TrimSpace(sample) == "" {
			return ResponseMsg{
				CommandID: "llm.voice-guide",
				Result: command.Result{
					Type:    command.ResultError,
					Content: "No writing sample available. Open a chapter or select prose first.",
				},
			}
		}

		system, user := llm.VoiceGuidePrompt(sample, ctx.VoiceGuide)
		response, usage, err := client.Complete(reqCtx, system, user)
		if err != nil {
			return ResponseMsg{
				CommandID: "llm.voice-guide",
				Result: command.Result{
					Type:    command.ResultError,
					Content: err.Error(),
				},
			}
		}

		// Never write from this goroutine: return the proposal for review and let
		// the app write it (atomically, keeping a .bak) only if the user accepts.
		return ResponseMsg{
			CommandID: "llm.voice-guide",
			Usage:     usage,
			Result: command.Result{
				Type:         command.ResultVoiceGuide,
				OriginalText: ctx.VoiceGuide,
				ProposedText: response,
			},
		}
	}
}

// ExecuteSummarize summarizes the current chapter asynchronously. It is a
// streaming, text-result command: on success it hands back a StreamStartMsg whose
// channel the app drains to render the summary token-by-token. Guard failures
// (unconfigured, empty buffer) still return a one-shot ResponseMsg error.
func ExecuteSummarize(client *llm.Client, ctx *command.Context, reqCtx context.Context) tea.Cmd {
	return func() tea.Msg {
		if !client.IsConfiguredContext(reqCtx) {
			return ResponseMsg{
				CommandID: "llm.summarize",
				Result: command.Result{
					Type:    command.ResultError,
					Content: client.UnconfiguredMessage(reqCtx),
				},
			}
		}

		if strings.TrimSpace(ctx.EditorContent) == "" {
			return ResponseMsg{
				CommandID: "llm.summarize",
				Result: command.Result{
					Type:    command.ResultError,
					Content: "Nothing to summarize — open a chapter first.",
				},
			}
		}

		system, user := llm.SummarizeChapterPrompt(ctx.EditorContent)
		return StreamStartMsg{
			CommandID: "llm.summarize",
			Ch:        client.CompleteStream(reqCtx, system, user),
		}
	}
}

// ExecuteContinuityCheck runs the continuity check as a streaming, text-result
// command. Budgeting still happens up front; any budget notes seed the stream as
// its Prefix so the user sees them before the model's findings arrive.
func ExecuteContinuityCheck(client *llm.Client, ctx *command.Context, reqCtx context.Context) tea.Cmd {
	return func() tea.Msg {
		if !client.IsConfiguredContext(reqCtx) {
			return ResponseMsg{
				CommandID: "llm.continuity",
				Result: command.Result{
					Type:    command.ResultError,
					Content: client.UnconfiguredMessage(reqCtx),
				},
			}
		}

		stateFiles := make(map[string]string, len(ctx.StateFilePaths))
		for name, path := range ctx.StateFilePaths {
			data, err := os.ReadFile(path)
			if err != nil {
				return ResponseMsg{CommandID: "llm.continuity", Result: command.Result{Type: command.ResultError, Content: fmt.Sprintf("continuity aborted; unreadable state %s: %v", name, err)}}
			}
			stateFiles[name] = string(data)
		}
		if len(stateFiles) == 0 {
			return ResponseMsg{
				CommandID: "llm.continuity",
				Result: command.Result{
					Type:    command.ResultError,
					Content: "No state files found. Run :InitState to create world state.",
				},
			}
		}

		// Budget the request: cap the estimated token size, truncating an
		// over-long chapter and keeping only the most relevant state entries so a
		// large story bible can't blow past the provider's context limit.
		total := len(stateFiles)
		chapter, selectedState, notes := llm.BudgetContinuity(ctx.EditorContent, stateFiles)
		system, user := llm.ContinuityCheckPrompt(chapter, selectedState)

		omitted := make([]string, 0, total-len(selectedState))
		for name := range stateFiles {
			if _, ok := selectedState[name]; !ok {
				omitted = append(omitted, name)
			}
		}
		sort.Strings(omitted)
		prefix := fmt.Sprintf("Continuity context: checked %d of %d state entries", len(selectedState), total)
		if len(omitted) > 0 {
			prefix += "; budget omissions: " + strings.Join(omitted, ", ")
		}
		prefix += "\n"
		if len(notes) > 0 {
			prefix += strings.Join(notes, "\n") + "\n"
		}
		prefix += "\n"
		return StreamStartMsg{
			CommandID: "llm.continuity",
			Ch:        client.CompleteStream(reqCtx, system, user),
			Prefix:    prefix,
		}
	}
}
