package llmcmd

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ShayCichocki/marginctl/internal/auth"
	"github.com/ShayCichocki/marginctl/internal/command"
	"github.com/ShayCichocki/marginctl/internal/config"
	"github.com/ShayCichocki/marginctl/internal/llm"
)

// 4.3 — parseStateProposals tolerates surrounding prose/code fences and keeps
// only well-formed character/location entries.
func TestParseStateProposals(t *testing.T) {
	resp := "Here you go:\n```json\n" +
		`[{"category":"characters","name":"Jack","summary":"A wandering knight."},` +
		`{"category":"locations","name":"Rookhaven","summary":"A cliffside town."},` +
		`{"category":"characters","name":"","summary":"nameless"},` +
		`{"category":"objects","name":"Sword","summary":"skip: unknown category"}]` +
		"\n```"
	props, skipped, err := parseStateProposals(resp)
	if err != nil {
		t.Fatal(err)
	}
	if len(props) != 2 || skipped != 2 {
		t.Fatalf("got %d proposals, %d skipped, want 2 and 2: %+v", len(props), skipped, props)
	}
	if props[0].Category != "characters" || props[0].Name != "Jack" {
		t.Fatalf("first proposal = %+v", props[0])
	}
	if props[1].Category != "locations" || props[1].Name != "Rookhaven" {
		t.Fatalf("second proposal = %+v", props[1])
	}

	if _, _, err := parseStateProposals("no array here"); err == nil {
		t.Fatal("expected an error when no JSON array is present")
	}
}

// TestExecuteTightenSurfacesGuardError verifies a truncated/empty completion is
// surfaced to the user as a ResultError rather than landing as a silent diff.
func TestExecuteTightenSurfacesGuardError(t *testing.T) {
	server := respondWith(`{"content":[{"type":"text","text":"partial"}],"stop_reason":"max_tokens"}`)
	defer server.Close()

	client := llm.NewClient(config.LLMConfig{
		Provider: config.ProviderAnthropic,
		APIKey:   "test-key",
		Endpoint: server.URL,
	})

	msg := ExecuteTighten(client, &command.Context{
		HasSelection: true,
		SelectedText: "some selected prose",
	}, context.Background())()

	resp, ok := msg.(ResponseMsg)
	if !ok {
		t.Fatalf("msg type = %T, want ResponseMsg", msg)
	}
	if resp.Result.Type != command.ResultError {
		t.Fatalf("result type = %v, want ResultError", resp.Result.Type)
	}
	// The error text is carried by Result.Content (the redundant ResponseMsg.Error
	// field was removed).
	if !strings.Contains(resp.Result.Content, llm.ErrTruncated.Error()) {
		t.Fatalf("result content = %q, want it to contain %q", resp.Result.Content, llm.ErrTruncated.Error())
	}
}

// 3.5 / 3.2 — the summarizer is wired: registered, guards an empty buffer with a
// one-shot error, and otherwise streams (returns a StreamStartMsg the app drains).
func TestSummarizeRegisteredAndRuns(t *testing.T) {
	reg := command.NewRegistry()
	RegisterCommands(reg, llm.NewClient(config.LLMConfig{}))
	if reg.Get("llm.summarize") == nil {
		t.Fatal("llm.summarize not registered")
	}

	server := sseServer(
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"- A happens"}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"\n- B happens"}}`,
		`{"type":"message_stop"}`,
	)
	defer server.Close()
	client := llm.NewClient(config.LLMConfig{
		Provider: config.ProviderAnthropic,
		APIKey:   "test-key",
		Endpoint: server.URL,
	})

	// Empty buffer → guarded one-shot error, no request.
	empty := ExecuteSummarize(client, &command.Context{EditorContent: "  "}, context.Background())()
	if r := empty.(ResponseMsg).Result; r.Type != command.ResultError {
		t.Fatalf("empty buffer result = %v, want ResultError", r.Type)
	}

	// Non-empty → a streaming start; drain it and assemble the text.
	msg := ExecuteSummarize(client, &command.Context{EditorContent: "Once upon a time."}, context.Background())()
	start, ok := msg.(StreamStartMsg)
	if !ok {
		t.Fatalf("msg type = %T, want StreamStartMsg", msg)
	}
	got := drain(start.Ch)
	if !strings.Contains(got, "A happens") || !strings.Contains(got, "B happens") {
		t.Fatalf("streamed summary = %q", got)
	}
}

// 3.5 — the additional selection-edit writing commands are registered in Visual
// mode and run through the shared ExecuteSelectionEdit, returning a diff on success
// while guarding the "no selection" case.
func TestSelectionEditCommandsRegisteredAndRun(t *testing.T) {
	reg := command.NewRegistry()
	RegisterCommands(reg, llm.NewClient(config.LLMConfig{}))

	for _, id := range []string{"llm.tighten", "llm.show-dont-tell", "llm.dialogue", "llm.expand", "llm.rewrite-voice"} {
		cmd := reg.Get(id)
		if cmd == nil {
			t.Fatalf("%s not registered", id)
		}
		if len(cmd.Modes) != 1 || cmd.Modes[0] != command.ModeVisual {
			t.Fatalf("%s modes = %v, want [Visual]", id, cmd.Modes)
		}
	}

	server := respondWith(`{"content":[{"type":"text","text":"rewritten prose"}]}`)
	defer server.Close()
	client := llm.NewClient(config.LLMConfig{
		Provider: config.ProviderAnthropic,
		APIKey:   "test-key",
		Endpoint: server.URL,
	})

	// No selection → guarded error, no request.
	noSel := ExecuteSelectionEdit(client, &command.Context{HasSelection: false}, context.Background(), "llm.expand", llm.ExpandProsePrompt)()
	if r := noSel.(ResponseMsg).Result; r.Type != command.ResultError {
		t.Fatalf("no-selection result = %v, want ResultError", r.Type)
	}

	// With a selection → a diff carrying the offsets and buffer identity.
	sel := &command.Context{
		HasSelection: true,
		SelectedText: "She was sad.",
		CurrentFile:  "ch1.md",
		BufferGen:    7,
	}
	msg := ExecuteSelectionEdit(client, sel, context.Background(), "llm.show-dont-tell", llm.ShowDontTellPrompt)().(ResponseMsg)
	if msg.Result.Type != command.ResultDiff {
		t.Fatalf("result type = %v, want ResultDiff", msg.Result.Type)
	}
	if msg.Result.ProposedText != "rewritten prose" {
		t.Fatalf("proposed = %q", msg.Result.ProposedText)
	}
	if msg.File != "ch1.md" || msg.BufferGen != 7 {
		t.Fatalf("diff identity = %q/%d, want ch1.md/7", msg.File, msg.BufferGen)
	}
}

// 3.6 — every selection-edit prompt fences the untrusted selection and folds in the
// voice guide, not just tighten.
func TestSelectionEditPromptsFenceAndCarryVoiceGuide(t *testing.T) {
	for _, prompt := range []PromptFunc{llm.TightenParagraphPrompt, llm.ShowDontTellPrompt, llm.PolishDialoguePrompt, llm.ExpandProsePrompt, llm.RewriteInVoicePrompt} {
		system, user := prompt("some prose", "Voice: terse and wry.")
		if !strings.Contains(system, "untrusted data") {
			t.Fatalf("system prompt missing injection guard:\n%s", system)
		}
		if !strings.Contains(system, "Voice: terse and wry.") {
			t.Fatalf("system prompt did not fold in the voice guide:\n%s", system)
		}
		if !strings.Contains(user, "<manuscript>") || !strings.Contains(user, "</manuscript>") {
			t.Fatalf("selection not fenced: %q", user)
		}
	}
}

// 3.8 — continuity operates on the whole chapter, so it must be offered in
// Normal mode (not only Visual).
func TestContinuityRegisteredForNormalMode(t *testing.T) {
	reg := command.NewRegistry()
	RegisterCommands(reg, llm.NewClient(config.LLMConfig{}))

	cmd := reg.Get("llm.continuity")
	if cmd == nil {
		t.Fatal("llm.continuity not registered")
	}
	var hasNormal, hasVisual bool
	for _, mode := range cmd.Modes {
		switch mode {
		case command.ModeNormal:
			hasNormal = true
		case command.ModeVisual:
			hasVisual = true
		}
	}
	if !hasNormal || !hasVisual {
		t.Fatalf("continuity modes = %v, want both Normal and Visual", cmd.Modes)
	}
}

func TestChatGPTCommandPreflightRefreshHonorsRequestCancellation(t *testing.T) {
	// Given
	// The llm package's unexported clock/store seams are not reachable from here,
	// so isolate the real default store path and use the real clock.
	home := t.TempDir()
	for _, k := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "APPDATA", "LOCALAPPDATA"} {
		t.Setenv(k, home)
	}
	storePath, err := auth.AuthStorePath()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	started := make(chan struct{})
	release := make(chan struct{})
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		_, _ = w.Write([]byte(`{"access_token":"fake-access-token-refreshed","refresh_token":"fake-refresh-token-refreshed","expires_in":1800}`))
	}))
	defer issuer.Close()
	store := auth.TokenStore{
		AuthMode:     auth.AuthModeChatGPT,
		Issuer:       issuer.URL,
		AccessToken:  "fake-access-token-old",
		RefreshToken: "fake-refresh-token-old",
		ExpiresAt:    now.Add(time.Minute),
		LastRefresh:  now.Add(-time.Hour),
		AccountID:    "acct-refresh-test",
		Email:        "writer@example.invalid",
	}
	if err = auth.SaveTokenStore(storePath, store); err != nil {
		t.Fatalf("SaveTokenStore: %v", err)
	}
	client := llm.NewClient(config.LLMConfig{Provider: config.ProviderChatGPT})
	reqCtx, cancel := context.WithCancel(context.Background())
	cmd := ExecuteVoiceGuide(client, &command.Context{IsProject: true, EditorContent: "sample prose"}, reqCtx)
	msgCh := make(chan ResponseMsg, 1)

	// When
	go func() {
		msgCh <- cmd().(ResponseMsg)
	}()
	<-started
	cancel()
	close(release)
	msg := <-msgCh

	// Then
	if msg.Result.Type != command.ResultError || !errors.Is(reqCtx.Err(), context.Canceled) {
		t.Fatalf("msg = %+v, want cancelled error result", msg)
	}
	got, err := auth.LoadTokenStore(storePath)
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != "fake-access-token-old" || got.RefreshToken != "fake-refresh-token-old" {
		t.Fatalf("stored tokens = %q/%q, want original tokens preserved after cancellation", got.AccessToken, got.RefreshToken)
	}
}
