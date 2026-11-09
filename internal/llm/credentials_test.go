package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ShayCichocki/marginctl/internal/auth"
	"github.com/ShayCichocki/marginctl/internal/config"
)

func TestCompleteVercelHeaderPreserved(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer vck-test-key" {
			t.Fatalf("Authorization = %q, want Bearer vck-test-key", got)
		}

		var req openAIRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.Model != vercelModel {
			t.Fatalf("model = %q, want %q", req.Model, vercelModel)
		}

		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"vercel response"}}]}`))
	}))
	defer server.Close()

	client := NewClient(config.LLMConfig{
		Provider: config.ProviderVercel,
		APIKey:   "vck-test-key",
		Endpoint: server.URL,
	})

	got, _, err := client.Complete(context.Background(), "system", "user")
	if err != nil {
		t.Fatal(err)
	}
	if got != "vercel response" {
		t.Fatalf("response = %q, want vercel response", got)
	}
}

func TestClientOpenAIAPIKeyWinsWhenChatGPTAuthStoreExists(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "auth.json")
	if err := auth.SaveTokenStore(storePath, validChatGPTTokenStore()); err != nil {
		t.Fatalf("SaveTokenStore: %v", err)
	}
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		if got := r.Header.Get("Authorization"); got != "Bearer sk-openai-api-key" {
			t.Fatalf("Authorization = %q, want Bearer sk-openai-api-key", got)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"openai api key"}}]}`))
	}))
	defer server.Close()

	client := NewClient(config.LLMConfig{
		Provider: config.ProviderOpenAI,
		APIKey:   "sk-openai-api-key",
		Endpoint: server.URL,
	})

	got, _, err := client.Complete(context.Background(), "system", "user")
	if err != nil {
		t.Fatal(err)
	}
	if got != "openai api key" {
		t.Fatalf("response = %q, want openai api key", got)
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("server calls = %d, want 1", n)
	}
}

func TestClientChatGPTMissingAuthIsReconnectRequiredAndDoesNotBorrowOpenAIKey(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-openai-env-key")
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusTeapot)
	}))
	defer server.Close()

	client := NewClient(config.LLMConfig{
		Provider: config.ProviderChatGPT,
		Endpoint: server.URL,
	})
	client.authStorePath = func() (string, error) {
		return filepath.Join(t.TempDir(), "missing-auth.json"), nil
	}

	_, _, err := client.Complete(context.Background(), "system", "user")
	if err == nil {
		t.Fatal("err = nil, want reconnect-required error")
	}
	if !strings.Contains(err.Error(), "run :connect openai") {
		t.Fatalf("err = %q, want reconnect guidance", err.Error())
	}
	if strings.Contains(err.Error(), "sk-openai-env-key") {
		t.Fatalf("err leaked env API key: %q", err.Error())
	}
	if n := atomic.LoadInt32(&calls); n != 0 {
		t.Fatalf("server calls = %d, want 0 for missing chatgpt auth", n)
	}
}

func TestClientChatGPTConfiguredWhenAuthStoreHasUsableToken(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "auth.json")
	if err := auth.SaveTokenStore(storePath, validChatGPTTokenStore()); err != nil {
		t.Fatalf("SaveTokenStore: %v", err)
	}
	client := NewClient(config.LLMConfig{Provider: config.ProviderChatGPT})
	client.authStorePath = func() (string, error) { return storePath, nil }

	if !client.IsConfigured() {
		t.Fatal("IsConfigured = false, want true for usable chatgpt auth store")
	}
}

func TestProviderPrecedenceEnvOpenAIOverridesFileChatGPT(t *testing.T) {
	clearLLMEnvForClient(t)
	configDir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", configDir)
	configPath := filepath.Join(configDir, "marginctl", "config.toml")
	if err := os.MkdirAll(filepath.Dir(configPath), 0755); err != nil {
		t.Fatalf("mkdir config dir: %v", err)
	}
	if err := os.WriteFile(configPath, []byte("[llm]\nprovider = \"chatgpt\"\n"), 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("MARGINCTL_LLM_PROVIDER", "openai")
	t.Setenv("OPENAI_API_KEY", "sk-openai-env-key")

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(cfg.LLM)

	if client.Provider() != config.ProviderOpenAI {
		t.Fatalf("provider = %q, want openai", client.Provider())
	}
	if !client.IsConfigured() {
		t.Fatal("IsConfigured = false, want true for env-selected openai API key")
	}
}

func validChatGPTTokenStore() auth.TokenStore {
	return auth.TokenStore{
		AuthMode:     auth.AuthModeChatGPT,
		Issuer:       "https://auth.example.invalid",
		AccessToken:  "fake-access-token-client-test",
		RefreshToken: "fake-refresh-token-client-test",
		ExpiresAt:    time.Now().Add(time.Hour),
		LastRefresh:  time.Now().Add(-time.Minute),
		AccountID:    "acct-client-test",
		Email:        "writer@example.invalid",
		ModelHint:    openAIModel,
	}
}

func clearLLMEnvForClient(t *testing.T) {
	t.Helper()
	for _, key := range []string{
		"MARGINCTL_LLM_API_KEY",
		"MARGINCTL_LLM_MODEL",
		"MARGINCTL_LLM_PROVIDER",
		"MARGINCTL_LLM_ENDPOINT",
		"MARGINCTL_LLM_TIMEOUT",
		"ANTHROPIC_API_KEY",
		"OPENAI_API_KEY",
		"AI_GATEWAY_API_KEY",
		"VERCEL_AI_GATEWAY_API_KEY",
	} {
		t.Setenv(key, "")
	}
}
