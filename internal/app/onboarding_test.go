package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ShayCichocki/marginctl/internal/config"
)

// isolateConfigEnv points the config path at a fresh temp dir and neutralizes
// every env var config.Load consults, so setup/first-run tests are hermetic
// regardless of the developer's shell. Returns the config.toml path.
func isolateConfigEnv(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	for _, k := range []string{
		"MARGINCTL_LLM_API_KEY", "MARGINCTL_LLM_MODEL", "MARGINCTL_LLM_PROVIDER",
		"MARGINCTL_LLM_ENDPOINT", "MARGINCTL_LLM_TIMEOUT",
		"ANTHROPIC_API_KEY", "OPENAI_API_KEY",
		"AI_GATEWAY_API_KEY", "VERCEL_AI_GATEWAY_API_KEY",
	} {
		t.Setenv(k, "")
	}
	return filepath.Join(dir, "marginctl", "config.toml")
}

func pressEnter(m Model) Model { return update(m, tea.KeyMsg{Type: tea.KeyEnter}) }
func pressEsc(m Model) Model   { return update(m, tea.KeyMsg{Type: tea.KeyEscape}) }

// 5.2 — a brand-new user (no config file, no env key) gets a visible pointer to
// :setup instead of a silently unconfigured LLM.
func TestFirstRunNoticeSuggestsSetup(t *testing.T) {
	isolateConfigEnv(t)
	m := New()
	if !strings.Contains(m.notice, ":setup") {
		t.Fatalf("expected first-run notice mentioning :setup, got %q", m.notice)
	}
	if m.noticeIsErr {
		t.Fatal("first-run hint must not render as an error")
	}
}

// 5.2 — the full :setup flow: choose a provider, paste a key, and the config is
// written (0600) while the live client is reconfigured — no restart needed.
func TestSetupFlowWritesConfigAndReconfiguresClient(t *testing.T) {
	cfgPath := isolateConfigEnv(t)
	m := New()

	m = update(m, keyRunes(":"))
	m = update(m, keyRunes("setup"))
	m = pressEnter(m)
	if m.activePrompt() != promptSetup || m.setupStep != setupStepProvider {
		t.Fatalf("expected setup provider step, got prompt=%v step=%d", m.activePrompt(), m.setupStep)
	}

	// A bogus provider choice is rejected and the prompt stays open.
	m = update(m, keyRunes("9"))
	m = pressEnter(m)
	if m.activePrompt() != promptSetup || m.setupStep != setupStepProvider || !m.noticeIsErr {
		t.Fatalf("expected invalid provider to be rejected in-place")
	}
	if m.setupBuffer != "" {
		t.Fatalf("rejected input should be cleared for retyping, got %q", m.setupBuffer)
	}

	m = update(m, keyRunes("2")) // openai
	m = pressEnter(m)
	if m.activePrompt() != promptSetup || m.setupStep != setupStepKey || m.setupProvider != config.ProviderOpenAI {
		t.Fatalf("expected key step for openai, got step=%d provider=%q", m.setupStep, m.setupProvider)
	}

	m = update(m, keyRunes("test api key value"))
	// The key is masked in the rendered prompt.
	if line := m.setupPromptLine(); strings.Contains(line, "test api key value") || !strings.Contains(line, "•") {
		t.Fatalf("API key must be masked in the prompt, got %q", line)
	}
	m = pressEnter(m)

	if m.activePrompt() == promptSetup {
		t.Fatal("setup should be finished")
	}
	if m.err != nil {
		t.Fatalf("setup failed: %v", m.err)
	}
	info, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatalf("config not written: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Fatalf("config perm = %o, want 600 (holds a secret)", perm)
	}
	body, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(body), `provider = "openai"`) || !strings.Contains(string(body), "test api key value") {
		t.Fatalf("config missing provider/key:\n%s", body)
	}
	if !m.llmClient.IsConfigured() || m.llmClient.Provider() != config.ProviderOpenAI {
		t.Fatalf("live client not reconfigured: configured=%v provider=%q",
			m.llmClient.IsConfigured(), m.llmClient.Provider())
	}
	if !strings.Contains(m.notice, "LLM configured") {
		t.Fatalf("expected a success notice, got %q", m.notice)
	}
}

// 5.2 — :setup never clobbers an existing config file; :setup! is the explicit
// override. Esc cancels without writing anything.
func TestSetupRefusesExistingConfigAndEscCancels(t *testing.T) {
	cfgPath := isolateConfigEnv(t)
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0755); err != nil {
		t.Fatal(err)
	}
	orig := "[llm]\nprovider = \"anthropic\"\napi_key = \"original test key\"\n"
	if err := os.WriteFile(cfgPath, []byte(orig), 0600); err != nil {
		t.Fatal(err)
	}

	m := New()
	m = update(m, keyRunes(":"))
	m = update(m, keyRunes("setup"))
	m = pressEnter(m)
	if m.activePrompt() == promptSetup {
		t.Fatal(":setup must refuse when a config file exists")
	}
	if !strings.Contains(m.notice, ":setup!") {
		t.Fatalf("refusal should point at :setup!, got %q", m.notice)
	}

	// :setup! opens the flow; Esc cancels it without touching the file.
	m = update(m, keyRunes(":"))
	m = update(m, keyRunes("setup!"))
	m = pressEnter(m)
	if m.activePrompt() != promptSetup {
		t.Fatal(":setup! should start the flow over an existing config")
	}
	m = pressEsc(m)
	if m.activePrompt() == promptSetup {
		t.Fatal("Esc should cancel setup")
	}
	body, err := os.ReadFile(cfgPath)
	if err != nil || string(body) != orig {
		t.Fatalf("cancelled setup must leave the config untouched, got %q (%v)", body, err)
	}
}

// 5.2 — :help (and the help.keys command) render the keybinding
// reference, including registry commands pulled live from the registry.
func TestHelpShowsKeybindingReference(t *testing.T) {
	isolateConfigEnv(t)
	m, _, _ := openTestModel(t)

	m = update(m, keyRunes(":"))
	m = update(m, keyRunes("help"))
	m = pressEnter(m)

	if !m.results.Visible() {
		t.Fatal(":help should open the results pane")
	}
	help := m.helpText()
	for _, want := range []string{"Ctrl+F/B", ":ShowChapters", ":setup", ":connect openai", ":connect openai --device", ":disconnect", ":auth", "auth.json", "MARGINCTL_LLM_PROVIDER", "API-key", "ChatGPT OAuth", "opencode", "private", "u Ctrl+R", "Configure LLM Provider", "Connect ChatGPT", "Disconnect ChatGPT", "ChatGPT Auth Status", "Tighten"} {
		if !strings.Contains(help, want) {
			t.Fatalf("help text missing %q", want)
		}
	}
	for _, forbidden := range []string{"access" + "_token", "refresh" + "_token", "id" + "_token", "code" + "_verifier"} {
		if strings.Contains(help, forbidden) {
			t.Fatalf("help text leaked token-field example %q", forbidden)
		}
	}

	if cmd := m.registry.Get("help.keys"); cmd == nil {
		t.Fatal("help.keys not registered")
	}
	if cmd := m.registry.Get("app.setup"); cmd == nil {
		t.Fatal("app.setup not registered")
	}
	for _, id := range []string{"auth.connect", "auth.disconnect", "auth.status"} {
		if cmd := m.registry.Get(id); cmd == nil {
			t.Fatalf("%s not registered", id)
		}
	}
}
