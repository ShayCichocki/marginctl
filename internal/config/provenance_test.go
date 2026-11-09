package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestEnvProviderSwitchDoesNotReuseFileCredential(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	path := filepath.Join(dir, "marginctl", "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("[llm]\nprovider='anthropic'\napi_key='anthropic-file-key'\nmodel='old'\nendpoint='https://old.example/v1'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MARGINCTL_LLM_PROVIDER", "openai")
	t.Setenv("OPENAI_API_KEY", "openai-env-key")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLM.APIKey != "openai-env-key" || cfg.LLM.APIKeySource != SourceEnv {
		t.Fatalf("key/source = %q/%q", cfg.LLM.APIKey, cfg.LLM.APIKeySource)
	}
	if cfg.LLM.Model != "" || cfg.LLM.Endpoint != "" {
		t.Fatalf("incompatible file values retained: %#v", cfg.LLM)
	}
}
