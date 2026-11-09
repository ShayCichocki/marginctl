package config

import (
	"os"
	"path/filepath"
	"testing"
)

// clearLLMEnv neutralizes every environment variable Load consults, so a test's
// expectations don't depend on the developer's real shell (which may export a
// provider key). Individual tests then set only what they exercise.
func clearLLMEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"MARGINCTL_LLM_API_KEY", "MARGINCTL_LLM_MODEL", "MARGINCTL_LLM_PROVIDER",
		"MARGINCTL_LLM_ENDPOINT", "MARGINCTL_LLM_TIMEOUT",
		"ANTHROPIC_API_KEY", "OPENAI_API_KEY",
		"AI_GATEWAY_API_KEY", "VERCEL_AI_GATEWAY_API_KEY",
	} {
		t.Setenv(k, "")
	}
}

// writeConfig points XDG_CONFIG_HOME at a temp dir and writes marginctl's
// config.toml there, so Load reads it.
func writeConfig(t *testing.T, body string) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	cfgDir := filepath.Join(dir, "marginctl")
	if err := os.MkdirAll(cfgDir, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.toml"), []byte(body), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// 3.7 — an explicit file provider must not be flipped by a stray provider key in
// the environment; and its own provider's missing key must not be filled from a
// different provider's key.
func TestLoadExplicitProviderNotOverriddenByStrayKey(t *testing.T) {
	clearLLMEnv(t)
	writeConfig(t, "[llm]\nprovider = \"openai\"\n")
	// Only an Anthropic key is present — the "wrong" provider for this config.
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-xxx")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLM.Provider != ProviderOpenAI {
		t.Fatalf("provider = %q, want openai (explicit choice must win)", cfg.LLM.Provider)
	}
	if cfg.LLM.APIKey != "" {
		t.Fatalf("APIKey = %q, want empty (must not borrow the Anthropic key)", cfg.LLM.APIKey)
	}
}

// 3.7 — when MARGINCTL_LLM_PROVIDER switches the provider, file-sourced
// model/endpoint (which belonged to the old provider) are dropped so the new
// provider's defaults apply.
func TestLoadEnvProviderSwitchResetsModelEndpoint(t *testing.T) {
	clearLLMEnv(t)
	writeConfig(t, "[llm]\nprovider = \"openai\"\nmodel = \"gpt-4.1\"\nendpoint = \"https://api.openai.com/v1/chat/completions\"\n")
	t.Setenv("MARGINCTL_LLM_PROVIDER", "anthropic")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLM.Provider != ProviderAnthropic {
		t.Fatalf("provider = %q, want anthropic", cfg.LLM.Provider)
	}
	if cfg.LLM.Model != "" {
		t.Fatalf("model = %q, want empty so the anthropic default applies", cfg.LLM.Model)
	}
	if cfg.LLM.Endpoint != "" {
		t.Fatalf("endpoint = %q, want empty so the anthropic default applies", cfg.LLM.Endpoint)
	}
}

// 3.7 — an explicit env MODEL/ENDPOINT survives a provider switch.
func TestLoadEnvProviderSwitchKeepsExplicitEnvOverrides(t *testing.T) {
	clearLLMEnv(t)
	writeConfig(t, "[llm]\nprovider = \"openai\"\nmodel = \"gpt-4.1\"\n")
	t.Setenv("MARGINCTL_LLM_PROVIDER", "anthropic")
	t.Setenv("MARGINCTL_LLM_MODEL", "claude-x")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLM.Model != "claude-x" {
		t.Fatalf("model = %q, want claude-x (explicit env override kept)", cfg.LLM.Model)
	}
}

// 3.7 — with no provider chosen anywhere, inference from a present key stands.
func TestLoadInfersProviderWhenNoneChosen(t *testing.T) {
	clearLLMEnv(t)
	// Empty config dir → no file provider.
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("OPENAI_API_KEY", "sk-oai-xxx")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLM.Provider != ProviderOpenAI {
		t.Fatalf("provider = %q, want openai (inferred)", cfg.LLM.Provider)
	}
	if cfg.LLM.APIKey != "sk-oai-xxx" {
		t.Fatalf("APIKey = %q, want the inferred key", cfg.LLM.APIKey)
	}
}

// 3.7 — an unknown TOML key produces a surfaced warning instead of silence.
func TestLoadWarnsOnUnknownKey(t *testing.T) {
	clearLLMEnv(t)
	writeConfig(t, "[llm]\nprovider = \"anthropic\"\nprvider = \"typo\"\n")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Notes) == 0 {
		t.Fatal("expected a warning note for the unknown key")
	}
}

// 3.7 — timeout comes through from env (seconds).
func TestLoadTimeoutFromEnv(t *testing.T) {
	clearLLMEnv(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("MARGINCTL_LLM_TIMEOUT", "42")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLM.TimeoutSeconds != 42 {
		t.Fatalf("TimeoutSeconds = %d, want 42", cfg.LLM.TimeoutSeconds)
	}
}

// 5.2 — SaveSetup creates the config file fresh with 0600 perms (it holds a
// secret) and Load round-trips the values.
func TestSaveSetupWritesNewFile(t *testing.T) {
	clearLLMEnv(t)
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)

	path, err := SaveSetup("Anthropic", " sk-ant-test ")
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(dir, "marginctl", "config.toml"); path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Fatalf("perm = %o, want 600", perm)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLM.Provider != ProviderAnthropic {
		t.Fatalf("provider = %q, want anthropic (normalized)", cfg.LLM.Provider)
	}
	if cfg.LLM.APIKey != "sk-ant-test" {
		t.Fatalf("api key = %q, want trimmed sk-ant-test", cfg.LLM.APIKey)
	}
}

// 5.2 — overwriting an existing config keeps provider-agnostic settings
// (timeout) but clears model/endpoint when the provider changes, since they
// belong to the old provider and would misroute requests (see 3.7).
func TestSaveSetupPreservesTimeoutClearsStaleModel(t *testing.T) {
	clearLLMEnv(t)
	writeConfig(t,
		"[llm]\nprovider = \"openai\"\napi_key = \"sk-old\"\nmodel = \"gpt-5.1\"\nendpoint = \"https://example.com/v1\"\ntimeout_seconds = 42\n")

	if _, err := SaveSetup("anthropic", "sk-ant-new"); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLM.Provider != ProviderAnthropic || cfg.LLM.APIKey != "sk-ant-new" {
		t.Fatalf("provider/key = %q/%q, want anthropic/sk-ant-new", cfg.LLM.Provider, cfg.LLM.APIKey)
	}
	if cfg.LLM.Model != "" || cfg.LLM.Endpoint != "" {
		t.Fatalf("stale model/endpoint survived a provider switch: %q / %q", cfg.LLM.Model, cfg.LLM.Endpoint)
	}
	if cfg.LLM.TimeoutSeconds != 42 {
		t.Fatalf("timeout = %d, want preserved 42", cfg.LLM.TimeoutSeconds)
	}
}

// 5.2 — same-provider re-setup (e.g. rotating a key) keeps the chosen model.
func TestSaveSetupSameProviderKeepsModel(t *testing.T) {
	clearLLMEnv(t)
	writeConfig(t, "[llm]\nprovider = \"anthropic\"\napi_key = \"sk-old\"\nmodel = \"claude-opus-4-8\"\n")

	if _, err := SaveSetup("anthropic", "sk-new"); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLM.APIKey != "sk-new" || cfg.LLM.Model != "claude-opus-4-8" {
		t.Fatalf("key/model = %q/%q, want sk-new/claude-opus-4-8", cfg.LLM.APIKey, cfg.LLM.Model)
	}
}

// 5.2 — FileExists tracks the config file's presence.
func TestFileExists(t *testing.T) {
	clearLLMEnv(t)
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if FileExists() {
		t.Fatal("FileExists = true with no file")
	}
	if _, err := SaveSetup("openai", "sk"); err != nil {
		t.Fatal(err)
	}
	if !FileExists() {
		t.Fatal("FileExists = false after SaveSetup")
	}
}

// 5.4 — the [theme] block decodes (preset + per-color overrides).
func TestLoadThemeConfig(t *testing.T) {
	writeConfig(t, "[theme]\npreset = \"light\"\nprimary = \"#123456\"\nheading = \"#abcdef\"\n")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Theme.Preset != "light" {
		t.Fatalf("preset = %q, want light", cfg.Theme.Preset)
	}
	if cfg.Theme.Primary != "#123456" || cfg.Theme.Heading != "#abcdef" {
		t.Fatalf("theme overrides not decoded: %+v", cfg.Theme)
	}
	// An unrelated [theme] key must not silently vanish — it should surface as a note.
	writeConfig(t, "[theme]\nbogus = \"x\"\n")
	cfg, _ = Load()
	if len(cfg.Notes) == 0 {
		t.Fatal("expected an unknown-key note for a bogus [theme] key")
	}
}
