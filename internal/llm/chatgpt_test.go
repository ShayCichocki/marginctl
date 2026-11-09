package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

// writeResponsesSSE writes codex Responses SSE frames to w (test helper). The
// codex backend has no non-streaming mode, so every chatgpt reply is a stream.
func writeResponsesSSE(w http.ResponseWriter, frames ...string) {
	w.Header().Set("Content-Type", "text/event-stream")
	for _, f := range frames {
		_, _ = fmt.Fprintf(w, "data: %s\n\n", f)
	}
}

// sseTextComplete builds the frames for a successful streamed reply: one text
// delta followed by a response.completed carrying usage.
func sseTextComplete(text string, inTok, outTok int) []string {
	return []string{
		fmt.Sprintf(`{"type":"response.output_text.delta","delta":%q}`, text),
		fmt.Sprintf(`{"type":"response.completed","response":{"usage":{"input_tokens":%d,"output_tokens":%d}}}`, inTok, outTok),
	}
}

func TestChatGPTDefaultEndpointIsCodexResponses(t *testing.T) {
	// Given
	client := NewClient(config.LLMConfig{Provider: config.ProviderChatGPT})

	// Then
	if client.endpoint != chatGPTCodexEndpoint {
		t.Fatalf("endpoint = %q, want %q", client.endpoint, chatGPTCodexEndpoint)
	}
}

func TestCompleteChatGPTSendsOAuthHeadersAndResponsesBody(t *testing.T) {
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
		// The codex backend has no non-streaming mode and rejects chat-completions
		// fields: stream must be true, the system prompt goes in instructions, the
		// user prompt in structured input, and store must be explicit false.
		if req.Model != "codex-test-model" || !req.Stream {
			t.Fatalf("request = %+v, want stream=true Responses body", req)
		}
		if req.Instructions != "system" {
			t.Fatalf("instructions = %q, want the system prompt", req.Instructions)
		}
		if req.Store {
			t.Fatalf("store = %v, want explicit false for codex backend", req.Store)
		}
		if len(req.Input) != 1 || req.Input[0].Role != "user" || len(req.Input[0].Content) != 1 ||
			req.Input[0].Content[0].Type != "input_text" || req.Input[0].Content[0].Text != "user" {
			t.Fatalf("input = %+v, want a single user input_text message", req.Input)
		}
		writeResponsesSSE(w, sseTextComplete("chatgpt response", 11, 7)...)
	}))
	defer server.Close()
	client := NewClient(config.LLMConfig{Provider: config.ProviderChatGPT, Model: "codex-test-model", Endpoint: server.URL})
	client.authStorePath = func() (string, error) { return storePath, nil }

	// When
	got, usage, err := client.Complete(context.Background(), "system", "user")

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if got != "chatgpt response" {
		t.Fatalf("response = %q, want chatgpt response", got)
	}
	if usage.InputTokens != 11 || usage.OutputTokens != 7 {
		t.Fatalf("usage = %+v, want 11/7", usage)
	}
}

func TestCompleteChatGPTOmitsAccountHeaderWhenStoreHasNoAccountID(t *testing.T) {
	// Given
	storePath := filepath.Join(t.TempDir(), "auth.json")
	store := validChatGPTTokenStore()
	store.AccountID = ""
	if err := auth.SaveTokenStore(storePath, store); err != nil {
		t.Fatalf("SaveTokenStore: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("ChatGPT-Account-Id"); got != "" {
			t.Fatalf("ChatGPT-Account-Id = %q, want omitted", got)
		}
		writeResponsesSSE(w, sseTextComplete("ok", 0, 0)...)
	}))
	defer server.Close()
	client := NewClient(config.LLMConfig{Provider: config.ProviderChatGPT, Endpoint: server.URL})
	client.authStorePath = func() (string, error) { return storePath, nil }

	// When
	_, _, err := client.Complete(context.Background(), "system", "user")

	// Then
	if err != nil {
		t.Fatal(err)
	}
}

func TestResponsesRequestOmitsRejectedParams(t *testing.T) {
	// The codex backend rejects each of these with `{"detail":"Unsupported
	// parameter: …"}` / `Store must be set to false`; the request must never carry
	// a token cap or chat-completions fields, and store must be an explicit false.
	client := NewClient(config.LLMConfig{Provider: config.ProviderChatGPT, Model: "codex"})
	body, err := json.Marshal(client.newResponsesRequest("sys", "usr"))
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"max_output_tokens", "max_tokens", "messages"} {
		if _, ok := raw[banned]; ok {
			t.Fatalf("request carried rejected param %q: %s", banned, body)
		}
	}
	if store, ok := raw["store"]; !ok || string(store) != "false" {
		t.Fatalf("store = %s (present=%v), want explicit false", store, ok)
	}
	// The backend has no non-streaming mode; stream must always be true.
	if stream, ok := raw["stream"]; !ok || string(stream) != "true" {
		t.Fatalf("stream = %s (present=%v), want explicit true", stream, ok)
	}
}

func TestCompleteChatGPTMapsResponsesTerminalStates(t *testing.T) {
	cases := []struct {
		name    string
		frames  []string
		wantErr error
	}{
		{
			name: "truncated at max_output_tokens",
			frames: []string{
				`{"type":"response.output_text.delta","delta":"half a sen"}`,
				`{"type":"response.incomplete","response":{"incomplete_details":{"reason":"max_output_tokens"}}}`,
			},
			wantErr: ErrTruncated,
		},
		{
			name: "explicit refusal event",
			frames: []string{
				`{"type":"response.refusal.done","refusal":"I can't help with that."}`,
				`{"type":"response.completed","response":{}}`,
			},
			wantErr: ErrRefused,
		},
		{
			name:    "completed with no text",
			frames:  []string{`{"type":"response.completed","response":{}}`},
			wantErr: ErrEmptyResponse,
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

			_, _, err := client.Complete(context.Background(), "system", "user")
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestCompleteChatGPTReturnsContractChangedWhenSchemaDrifts(t *testing.T) {
	// Given
	storePath := filepath.Join(t.TempDir(), "auth.json")
	if err := auth.SaveTokenStore(storePath, validChatGPTTokenStore()); err != nil {
		t.Fatalf("SaveTokenStore: %v", err)
	}
	server := respondWith(`{"unexpected":"codex response shape","access_token":"fake-access-token-backend-leak"}`)
	defer server.Close()
	client := NewClient(config.LLMConfig{Provider: config.ProviderChatGPT, Endpoint: server.URL})
	client.authStorePath = func() (string, error) { return storePath, nil }

	// When
	_, _, err := client.Complete(context.Background(), "system", "user")

	// Then
	if err == nil || !strings.Contains(err.Error(), "ChatGPT backend contract changed") {
		t.Fatalf("err = %v, want ChatGPT backend contract changed", err)
	}
	if strings.Contains(err.Error(), "fake-access-token") {
		t.Fatalf("err leaked token: %q", err.Error())
	}
}

func TestCompleteOpenAISchemaErrorsRemainPublicOpenAIErrorsWhenChatGPTAuthStoreExists(t *testing.T) {
	// Given
	storePath := filepath.Join(t.TempDir(), "auth.json")
	if err := auth.SaveTokenStore(storePath, validChatGPTTokenStore()); err != nil {
		t.Fatalf("SaveTokenStore: %v", err)
	}
	server := respondWith(`{"unexpected":"public openai shape"}`)
	defer server.Close()
	client := NewClient(config.LLMConfig{Provider: config.ProviderOpenAI, APIKey: "sk-openai-api-key", Endpoint: server.URL})
	client.authStorePath = func() (string, error) { return storePath, nil }

	// When
	_, _, err := client.Complete(context.Background(), "system", "user")

	// Then
	if err == nil || !strings.Contains(err.Error(), "no choices") {
		t.Fatalf("err = %v, want public OpenAI no choices error", err)
	}
	if strings.Contains(err.Error(), "ChatGPT backend contract changed") {
		t.Fatalf("openai error used ChatGPT contract wrapper: %q", err.Error())
	}
}

func TestCompleteChatGPTRetries401WithRefreshedBearerAndAccountHeader(t *testing.T) {
	// Given
	storePath := filepath.Join(t.TempDir(), "auth.json")
	now := time.Date(2026, 7, 9, 15, 0, 0, 0, time.UTC)
	var refreshCalls int32
	issuer := newRefreshIssuer(t, &refreshCalls, http.StatusOK, `{"access_token":"fake-access-token-refreshed","expires_in":1800,"account_id":"acct-refreshed"}`)
	store := chatGPTRefreshTokenStore(issuer.URL, "fake-access-token-stale", "fake-refresh-token-old", now.Add(time.Hour))
	if err := auth.SaveTokenStore(storePath, store); err != nil {
		t.Fatalf("SaveTokenStore: %v", err)
	}
	var apiCalls int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch atomic.AddInt32(&apiCalls, 1) {
		case 1:
			if got := r.Header.Get("Authorization"); got != "Bearer fake-access-token-stale" {
				t.Fatalf("first Authorization = %q, want stale bearer", got)
			}
			if got := r.Header.Get("ChatGPT-Account-Id"); got != "acct-refresh-test" {
				t.Fatalf("first account = %q, want stored account", got)
			}
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"expired token"}}`))
		default:
			if got := r.Header.Get("Authorization"); got != "Bearer fake-access-token-refreshed" {
				t.Fatalf("retry Authorization = %q, want refreshed bearer", got)
			}
			if got := r.Header.Get("ChatGPT-Account-Id"); got != "acct-refreshed" {
				t.Fatalf("retry account = %q, want refreshed account", got)
			}
			writeResponsesSSE(w, sseTextComplete("refreshed ok", 0, 0)...)
		}
	}))
	defer api.Close()
	client := newChatGPTRefreshTestClient(storePath, now)
	client.endpoint = api.URL

	// When
	got, _, err := client.Complete(context.Background(), "system", "user")

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if got != "refreshed ok" {
		t.Fatalf("response = %q, want refreshed ok", got)
	}
	if n := atomic.LoadInt32(&apiCalls); n != 2 {
		t.Fatalf("api calls = %d, want initial + one retry", n)
	}
	if n := atomic.LoadInt32(&refreshCalls); n != 1 {
		t.Fatalf("refresh calls = %d, want 1", n)
	}
}

func TestCompleteChatGPTInvalidGrantAfter401ReturnsReconnectGuidance(t *testing.T) {
	// Given
	storePath := filepath.Join(t.TempDir(), "auth.json")
	now := time.Date(2026, 7, 9, 15, 0, 0, 0, time.UTC)
	var refreshCalls int32
	issuer := newRefreshIssuer(t, &refreshCalls, http.StatusBadRequest, `{"error":"invalid_grant","error_description":"revoked fake-refresh-token-old"}`)
	store := chatGPTRefreshTokenStore(issuer.URL, "fake-access-token-stale", "fake-refresh-token-old", now.Add(time.Hour))
	if err := auth.SaveTokenStore(storePath, store); err != nil {
		t.Fatalf("SaveTokenStore: %v", err)
	}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"expired token"}}`))
	}))
	defer api.Close()
	client := newChatGPTRefreshTestClient(storePath, now)
	client.endpoint = api.URL

	// When
	_, _, err := client.Complete(context.Background(), "system", "user")

	// Then
	if err == nil || !strings.Contains(err.Error(), "run :connect openai") {
		t.Fatalf("err = %v, want reconnect guidance", err)
	}
	if strings.Contains(err.Error(), "fake-refresh-token") || strings.Contains(err.Error(), "fake-access-token") {
		t.Fatalf("err leaked token: %q", err.Error())
	}
	if n := atomic.LoadInt32(&refreshCalls); n != 1 {
		t.Fatalf("refresh calls = %d, want 1", n)
	}
}

func TestChatGPTContractErrorIsComparable(t *testing.T) {
	// Given
	err := chatGPTContractError(errors.New("bad schema with fake-access-token-secret"))

	// Then
	if !errors.Is(err, errChatGPTContractChanged) {
		t.Fatalf("err = %v, want errChatGPTContractChanged", err)
	}
	if strings.Contains(err.Error(), "fake-access-token") {
		t.Fatalf("err leaked token: %q", err.Error())
	}
}
