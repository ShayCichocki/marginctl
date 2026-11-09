package llm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ShayCichocki/marginctl/internal/config"
)

// GPT-5+/o-series reject max_tokens on chat-completions; legacy models still use
// it. The router must pick the right field purely from the model id.
func TestModelRequiresMaxCompletionTokens(t *testing.T) {
	cases := map[string]bool{
		"gpt-5.6-sol":        true,
		"openai/gpt-5.6-sol": true,
		"gpt-6-sol":          true,
		"gpt-5-chat-latest":  true,
		"o1":                 true,
		"o3-mini":            true,
		"o4":                 true,
		"gpt-4o":             false,
		"gpt-4.1":            false,
		"gpt-3.5-turbo":      false,
		"openai/gpt-4o-mini": false,
		"claude-sonnet-5":    false,
		"":                   false,
	}
	for model, want := range cases {
		if got := modelRequiresMaxCompletionTokens(model); got != want {
			t.Errorf("modelRequiresMaxCompletionTokens(%q) = %v, want %v", model, got, want)
		}
	}
}

// The request put on the wire must carry exactly one output-cap field, matching
// the model — a GPT-5+ default would 400 if it sent max_tokens.
func TestOpenAICompatibleWireSendsCorrectTokenField(t *testing.T) {
	for _, tc := range []struct {
		model       string
		wantField   string
		rejectField string
	}{
		{"gpt-5.6-sol", `"max_completion_tokens"`, `"max_tokens"`},
		{"gpt-4o", `"max_tokens"`, `"max_completion_tokens"`},
	} {
		var body string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b := make([]byte, r.ContentLength)
			_, _ = r.Body.Read(b)
			body = string(b)
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
		}))
		client := NewClient(config.LLMConfig{Provider: config.ProviderOpenAI, APIKey: "k", Endpoint: server.URL, Model: tc.model})
		if _, _, err := client.Complete(context.Background(), "s", "u"); err != nil {
			server.Close()
			t.Fatalf("%s: %v", tc.model, err)
		}
		server.Close()
		if !strings.Contains(body, tc.wantField) {
			t.Errorf("%s: body missing %s: %s", tc.model, tc.wantField, body)
		}
		if strings.Contains(body, tc.rejectField) {
			t.Errorf("%s: body must not contain %s: %s", tc.model, tc.rejectField, body)
		}
	}
}

func TestRetryAfterParsing(t *testing.T) {
	h := http.Header{}
	h.Set("Retry-After", "7")
	if got := retryAfter(h); got != 7*time.Second {
		t.Fatalf("retryAfter(7) = %v, want 7s", got)
	}
	h.Set("Retry-After", "Wed, 21 Oct 2026 07:28:00 GMT") // HTTP-date form: ignored
	if got := retryAfter(h); got != 0 {
		t.Fatalf("retryAfter(date) = %v, want 0", got)
	}
	if got := retryAfter(http.Header{}); got != 0 {
		t.Fatalf("retryAfter(absent) = %v, want 0", got)
	}
}

// A cancel while waiting out a retry backoff must be reported as a cancellation,
// not as the last HTTP status (a spurious 429) that triggered the retry.
func TestCancelDuringBackoffReportsCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "30") // long enough that the cancel wins the wait
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"slow down"}}`))
	}))
	defer server.Close()

	client := NewClient(config.LLMConfig{Provider: config.ProviderOpenAI, APIKey: "k", Endpoint: server.URL})
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(20 * time.Millisecond); cancel() }()

	_, _, err := client.Complete(ctx, "s", "u")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled (not the stale 429)", err)
	}
	var statusErr apiStatusError
	if errors.As(err, &statusErr) {
		t.Fatalf("err must not surface the HTTP status: %v", err)
	}
}
