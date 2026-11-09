package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ShayCichocki/marginctl/internal/auth"
	"github.com/ShayCichocki/marginctl/internal/config"
)

func newChatGPTStreamClient(t *testing.T, frames ...string) (*Client, func()) {
	t.Helper()
	storePath := filepath.Join(t.TempDir(), "auth.json")
	if err := auth.SaveTokenStore(storePath, validChatGPTTokenStore()); err != nil {
		t.Fatalf("SaveTokenStore: %v", err)
	}
	server := sseServer(frames...)
	client := NewClient(config.LLMConfig{Provider: config.ProviderChatGPT, Endpoint: server.URL})
	client.authStorePath = func() (string, error) { return storePath, nil }
	return client, server.Close
}

func TestCompleteStreamChatGPTSendsOAuthHeadersAndParsesResponsesSSE(t *testing.T) {
	// Given
	storePath := filepath.Join(t.TempDir(), "auth.json")
	if err := auth.SaveTokenStore(storePath, validChatGPTTokenStore()); err != nil {
		t.Fatalf("SaveTokenStore: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer fake-access-token-client-test" {
			t.Fatalf("Authorization = %q, want ChatGPT bearer", got)
		}
		if got := r.Header.Get("ChatGPT-Account-Id"); got != "acct-client-test" {
			t.Fatalf("ChatGPT-Account-Id = %q, want account header", got)
		}
		var req responsesRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if !req.Stream {
			t.Fatalf("request = %+v, want stream=true", req)
		}
		if req.Store {
			t.Fatalf("store = %v, want explicit false for codex backend", req.Store)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"Hel\"}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"lo\"}\n\n"))
		_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":13,\"output_tokens\":3}}}\n\n"))
	}))
	defer server.Close()
	client := NewClient(config.LLMConfig{Provider: config.ProviderChatGPT, Endpoint: server.URL})
	client.authStorePath = func() (string, error) { return storePath, nil }

	// When
	text, term := drainEvents(client.CompleteStream(context.Background(), "system", "user"))

	// Then
	if text != "Hello" {
		t.Fatalf("text = %q, want Hello", text)
	}
	if !term.Done || term.Err != nil {
		t.Fatalf("terminal = %+v, want Done", term)
	}
	if term.Usage.InputTokens != 13 || term.Usage.OutputTokens != 3 {
		t.Fatalf("usage = %+v, want 13/3", term.Usage)
	}
}

func TestCompleteStreamChatGPTReturnsContractChangedWhenSSESchemaDrifts(t *testing.T) {
	// Given
	storePath := filepath.Join(t.TempDir(), "auth.json")
	if err := auth.SaveTokenStore(storePath, validChatGPTTokenStore()); err != nil {
		t.Fatalf("SaveTokenStore: %v", err)
	}
	server := sseServer(`{"unexpected":"codex stream shape","refresh_token":"fake-refresh-token-leak"}`)
	defer server.Close()
	client := NewClient(config.LLMConfig{Provider: config.ProviderChatGPT, Endpoint: server.URL})
	client.authStorePath = func() (string, error) { return storePath, nil }

	// When
	_, term := drainEvents(client.CompleteStream(context.Background(), "system", "user"))

	// Then
	if term.Err == nil || !strings.Contains(term.Err.Error(), "ChatGPT backend contract changed") {
		t.Fatalf("terminal err = %v, want ChatGPT backend contract changed", term.Err)
	}
	if strings.Contains(term.Err.Error(), "fake-refresh-token") {
		t.Fatalf("err leaked token: %q", term.Err.Error())
	}
}

func TestCompleteStreamChatGPTRejectsDeltaThenEOF(t *testing.T) {
	client, closeServer := newChatGPTStreamClient(t, `{"type":"response.output_text.delta","delta":"partial"}`)
	defer closeServer()
	text, term := drainEvents(client.CompleteStream(context.Background(), "system", "user"))
	if text != "partial" || !errors.Is(term.Err, ErrStreamProtocol) {
		t.Fatalf("text = %q, terminal = %+v, want partial protocol failure", text, term)
	}
}

func TestCompleteStreamChatGPTRejectsMalformedFrame(t *testing.T) {
	client, closeServer := newChatGPTStreamClient(t, `{"type":`)
	defer closeServer()
	_, term := drainEvents(client.CompleteStream(context.Background(), "system", "user"))
	if !errors.Is(term.Err, ErrStreamProtocol) || ClassifyStreamError(term.Err) != StreamErrorProtocol {
		t.Fatalf("terminal err = %v, want classified protocol failure", term.Err)
	}
}

func TestCompleteStreamChatGPTRedactsSSEErrorMessage(t *testing.T) {
	// Given
	storePath := filepath.Join(t.TempDir(), "auth.json")
	if err := auth.SaveTokenStore(storePath, validChatGPTTokenStore()); err != nil {
		t.Fatalf("SaveTokenStore: %v", err)
	}
	server := sseServer(`{"error":{"message":"authorization Bearer sk-chatgpt-fake-123456 refresh_token fake-refresh-token-chatgpt"}}`)
	defer server.Close()
	client := NewClient(config.LLMConfig{Provider: config.ProviderChatGPT, Endpoint: server.URL})
	client.authStorePath = func() (string, error) { return storePath, nil }

	// When
	_, term := drainEvents(client.CompleteStream(context.Background(), "system", "user"))

	// Then
	if term.Err == nil || !strings.Contains(term.Err.Error(), "API error:") {
		t.Fatalf("terminal err = %v, want API error", term.Err)
	}
	if strings.Contains(term.Err.Error(), "sk-chatgpt-fake-123456") || strings.Contains(term.Err.Error(), "fake-refresh-token-chatgpt") {
		t.Fatalf("err leaked token: %q", term.Err.Error())
	}
}

func TestCompleteStreamChatGPTMapsRefusalAndFailureEvents(t *testing.T) {
	cases := []struct {
		name       string
		frames     []string
		wantErrSub string
	}{
		{
			name:       "refusal event",
			frames:     []string{`{"type":"response.refusal.done","refusal":"declined"}`, `{"type":"response.completed","response":{"status":"completed"}}`},
			wantErrSub: ErrRefused.Error(),
		},
		{
			name:       "failed event carries redacted message",
			frames:     []string{`{"type":"response.failed","response":{"error":{"message":"boom Bearer sk-chatgpt-fake-987654"}}}`},
			wantErrSub: "API error:",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			storePath := filepath.Join(t.TempDir(), "auth.json")
			if err := auth.SaveTokenStore(storePath, validChatGPTTokenStore()); err != nil {
				t.Fatalf("SaveTokenStore: %v", err)
			}
			server := sseServer(tc.frames...)
			defer server.Close()
			client := NewClient(config.LLMConfig{Provider: config.ProviderChatGPT, Endpoint: server.URL})
			client.authStorePath = func() (string, error) { return storePath, nil }

			_, term := drainEvents(client.CompleteStream(context.Background(), "system", "user"))
			if term.Err == nil || !strings.Contains(term.Err.Error(), tc.wantErrSub) {
				t.Fatalf("terminal err = %v, want contains %q", term.Err, tc.wantErrSub)
			}
			if strings.Contains(term.Err.Error(), "sk-chatgpt-fake") {
				t.Fatalf("err leaked token: %q", term.Err.Error())
			}
		})
	}
}

func TestCompleteStreamChatGPTRetries401BeforeReadingStream(t *testing.T) {
	// Given
	storePath := filepath.Join(t.TempDir(), "auth.json")
	now := time.Date(2026, 7, 9, 15, 0, 0, 0, time.UTC)
	var refreshCalls int32
	issuer := newRefreshIssuer(t, &refreshCalls, http.StatusOK, `{"access_token":"fake-access-token-stream-refreshed","expires_in":1800}`)
	store := chatGPTRefreshTokenStore(issuer.URL, "fake-access-token-stream-stale", "fake-refresh-token-old", now.Add(time.Hour))
	if err := auth.SaveTokenStore(storePath, store); err != nil {
		t.Fatalf("SaveTokenStore: %v", err)
	}
	var apiCalls int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch atomic.AddInt32(&apiCalls, 1) {
		case 1:
			if got := r.Header.Get("Authorization"); got != "Bearer fake-access-token-stream-stale" {
				t.Fatalf("first Authorization = %q, want stale stream bearer", got)
			}
			w.WriteHeader(http.StatusUnauthorized)
		default:
			if got := r.Header.Get("Authorization"); got != "Bearer fake-access-token-stream-refreshed" {
				t.Fatalf("retry Authorization = %q, want refreshed stream bearer", got)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n"))
			_, _ = w.Write([]byte("data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\"}}\n\n"))
		}
	}))
	defer api.Close()
	client := newChatGPTRefreshTestClient(storePath, now)
	client.endpoint = api.URL

	// When
	text, term := drainEvents(client.CompleteStream(context.Background(), "system", "user"))

	// Then
	if term.Err != nil || !term.Done {
		t.Fatalf("terminal = %+v, want Done", term)
	}
	if text != "ok" {
		t.Fatalf("text = %q, want ok", text)
	}
	if n := atomic.LoadInt32(&apiCalls); n != 2 {
		t.Fatalf("api calls = %d, want initial + one retry", n)
	}
	if n := atomic.LoadInt32(&refreshCalls); n != 1 {
		t.Fatalf("refresh calls = %d, want 1", n)
	}
}

func TestCompleteStreamOpenAIIgnoresChatGPTAuthHeaders(t *testing.T) {
	// Given
	storePath := filepath.Join(t.TempDir(), "auth.json")
	if err := auth.SaveTokenStore(storePath, validChatGPTTokenStore()); err != nil {
		t.Fatalf("SaveTokenStore: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer sk-openai-api-key" {
			t.Fatalf("Authorization = %q, want public OpenAI key", got)
		}
		if got := r.Header.Get("ChatGPT-Account-Id"); got != "" {
			t.Fatalf("ChatGPT-Account-Id = %q, want omitted for openai", got)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"openai\"},\"finish_reason\":\"stop\"}]}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()
	client := NewClient(config.LLMConfig{Provider: config.ProviderOpenAI, APIKey: "sk-openai-api-key", Endpoint: server.URL})
	client.authStorePath = func() (string, error) { return storePath, nil }

	// When
	text, term := drainEvents(client.CompleteStream(context.Background(), "system", "user"))

	// Then
	if term.Err != nil || !term.Done {
		t.Fatalf("terminal = %+v, want Done", term)
	}
	if text != "openai" {
		t.Fatalf("text = %q, want openai", text)
	}
}
