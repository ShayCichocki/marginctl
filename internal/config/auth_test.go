package config

import (
	"os"
	"strings"
	"testing"
)

func TestLoadChatGPTProviderDoesNotBorrowOpenAIKey(t *testing.T) {
	clearLLMEnv(t)
	writeConfig(t, "[llm]\nprovider = \" ChatGPT \"\n")
	t.Setenv("OPENAI_API_KEY", "sk-openai-api-key")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLM.Provider != ProviderChatGPT {
		t.Fatalf("provider = %q, want chatgpt", cfg.LLM.Provider)
	}
	if cfg.LLM.APIKey != "" {
		t.Fatalf("APIKey = %q, want empty because chatgpt uses OAuth auth store", cfg.LLM.APIKey)
	}
}

func TestLoadOpenAIKeyInfersOpenAIProviderNotChatGPT(t *testing.T) {
	clearLLMEnv(t)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("OPENAI_API_KEY", "sk-openai-api-key")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLM.Provider != ProviderOpenAI {
		t.Fatalf("provider = %q, want openai", cfg.LLM.Provider)
	}
	if cfg.LLM.Provider == ProviderChatGPT {
		t.Fatal("OPENAI_API_KEY must not automatically configure chatgpt OAuth mode")
	}
}

func TestSaveProviderMetadataWritesChatGPTWithoutSecret(t *testing.T) {
	clearLLMEnv(t)
	writeConfig(t, "[llm]\nprovider = \"openai\"\napi_key = \"sk-old\"\nmodel = \"gpt-5.1\"\ntimeout_seconds = 42\n")

	path, err := SaveProviderMetadata(ProviderChatGPT)
	if err != nil {
		t.Fatal(err)
	}

	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if !strings.Contains(text, `provider = "chatgpt"`) {
		t.Fatalf("config missing chatgpt provider metadata:\n%s", text)
	}
	if strings.Contains(text, "sk-old") || strings.Contains(text, "api_key") {
		t.Fatalf("config leaked or retained API-key material:\n%s", text)
	}
	if strings.Contains(text, "gpt-5.1") {
		t.Fatalf("stale provider model survived chatgpt activation:\n%s", text)
	}
	if !strings.Contains(text, "timeout_seconds = 42") {
		t.Fatalf("provider-agnostic timeout was not preserved:\n%s", text)
	}
}
