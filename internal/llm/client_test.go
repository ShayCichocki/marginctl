package llm

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ShayCichocki/marginctl/internal/config"
)

// TestMain zeroes the retry backoff so retry-exercising tests don't sleep.
func TestMain(m *testing.M) {
	retryBaseDelay = 0
	// Keep user-level directories (config, auth store) off the real home.
	dir, err := os.MkdirTemp("", "marginctl-test-home-")
	if err != nil {
		os.Exit(1)
	}
	for _, k := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "APPDATA", "LOCALAPPDATA"} {
		_ = os.Setenv(k, dir)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// newTestClient returns a client pointed at srv that always responds with body.
func respondWith(body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
}

func TestCompleteAnthropic(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("x-api-key"); got != "test-key" {
			t.Fatalf("x-api-key = %q, want test-key", got)
		}
		if got := r.Header.Get("anthropic-version"); got == "" {
			t.Fatal("missing anthropic-version header")
		}

		var req anthropicRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.System != "system" || req.Messages[0].Content != "user" {
			t.Fatalf("unexpected request: %#v", req)
		}

		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"anthropic response"}]}`))
	}))
	defer server.Close()

	client := NewClient(config.LLMConfig{
		Provider: config.ProviderAnthropic,
		APIKey:   "test-key",
		Endpoint: server.URL,
	})

	got, _, err := client.Complete(context.Background(), "system", "user")
	if err != nil {
		t.Fatal(err)
	}
	if got != "anthropic response" {
		t.Fatalf("response = %q, want anthropic response", got)
	}
}

func TestCompleteOpenAICompatible(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("Authorization = %q, want Bearer test-key", got)
		}

		var req openAIRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req.Model != openAIModel {
			t.Fatalf("model = %q, want %q", req.Model, openAIModel)
		}
		if req.Messages[0].Role != "system" || req.Messages[1].Role != "user" {
			t.Fatalf("unexpected messages: %#v", req.Messages)
		}

		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"openai response"}}]}`))
	}))
	defer server.Close()

	client := NewClient(config.LLMConfig{
		Provider: config.ProviderOpenAI,
		APIKey:   "test-key",
		Endpoint: server.URL,
	})

	got, _, err := client.Complete(context.Background(), "system", "user")
	if err != nil {
		t.Fatal(err)
	}
	if got != "openai response" {
		t.Fatalf("response = %q, want openai response", got)
	}
}

func TestCompleteResponseGuards(t *testing.T) {
	tests := []struct {
		name     string
		provider string
		body     string
		wantErr  error
	}{
		{
			name:     "anthropic empty text",
			provider: config.ProviderAnthropic,
			body:     `{"content":[{"type":"text","text":"   "}],"stop_reason":"end_turn"}`,
			wantErr:  ErrEmptyResponse,
		},
		{
			name:     "anthropic no content blocks",
			provider: config.ProviderAnthropic,
			body:     `{"content":[],"stop_reason":"end_turn"}`,
			wantErr:  ErrEmptyResponse,
		},
		{
			name:     "anthropic truncated",
			provider: config.ProviderAnthropic,
			body:     `{"content":[{"type":"text","text":"half a sen"}],"stop_reason":"max_tokens"}`,
			wantErr:  ErrTruncated,
		},
		{
			name:     "openai empty text",
			provider: config.ProviderOpenAI,
			body:     `{"choices":[{"message":{"role":"assistant","content":""},"finish_reason":"stop"}]}`,
			wantErr:  ErrEmptyResponse,
		},
		{
			name:     "openai truncated",
			provider: config.ProviderOpenAI,
			body:     `{"choices":[{"message":{"role":"assistant","content":"half a sen"},"finish_reason":"length"}]}`,
			wantErr:  ErrTruncated,
		},
		{
			name:     "anthropic null text block",
			provider: config.ProviderAnthropic,
			body:     `{"content":[{"type":"text","text":null}],"stop_reason":"end_turn"}`,
			wantErr:  ErrEmptyResponse,
		},
		{
			name:     "anthropic refusal",
			provider: config.ProviderAnthropic,
			body:     `{"content":[],"stop_reason":"refusal"}`,
			wantErr:  ErrRefused,
		},
		{
			name:     "openai null content",
			provider: config.ProviderOpenAI,
			body:     `{"choices":[{"message":{"role":"assistant","content":null},"finish_reason":"stop"}]}`,
			wantErr:  ErrEmptyResponse,
		},
		{
			name:     "openai refusal field",
			provider: config.ProviderOpenAI,
			body:     `{"choices":[{"message":{"role":"assistant","content":"","refusal":"I can't help with that."},"finish_reason":"stop"}]}`,
			wantErr:  ErrRefused,
		},
		{
			name:     "openai content filter",
			provider: config.ProviderOpenAI,
			body:     `{"choices":[{"message":{"role":"assistant","content":""},"finish_reason":"content_filter"}]}`,
			wantErr:  ErrRefused,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := respondWith(tt.body)
			defer server.Close()

			client := NewClient(config.LLMConfig{
				Provider: tt.provider,
				APIKey:   "test-key",
				Endpoint: server.URL,
			})

			got, _, err := client.Complete(context.Background(), "system", "user")
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("err = %v, want %v", err, tt.wantErr)
			}
			if got != "" {
				t.Fatalf("response = %q, want empty string on guard failure", got)
			}
		})
	}
}

// TestCompleteTransportErrors covers malformed and non-200 responses: each must
// return a non-nil error and an empty string (never silent prose).
func TestCompleteTransportErrors(t *testing.T) {
	tests := []struct {
		name       string
		provider   string
		status     int
		body       string
		wantSubstr string
	}{
		{
			name:       "anthropic non-JSON body",
			provider:   config.ProviderAnthropic,
			status:     http.StatusOK,
			body:       `not json at all`,
			wantSubstr: "parse response",
		},
		{
			name:       "openai non-JSON body",
			provider:   config.ProviderOpenAI,
			status:     http.StatusOK,
			body:       `<html>gateway error</html>`,
			wantSubstr: "parse response",
		},
		{
			name:       "openai empty choices",
			provider:   config.ProviderOpenAI,
			status:     http.StatusOK,
			body:       `{"choices":[]}`,
			wantSubstr: "no choices",
		},
		{
			name:       "non-200 status",
			provider:   config.ProviderAnthropic,
			status:     http.StatusTooManyRequests,
			body:       `{"error":{"type":"rate_limit","message":"slow down"}}`,
			wantSubstr: "status 429",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()

			client := NewClient(config.LLMConfig{
				Provider: tt.provider,
				APIKey:   "test-key",
				Endpoint: server.URL,
			})

			got, _, err := client.Complete(context.Background(), "system", "user")
			if err == nil {
				t.Fatalf("expected error, got nil (response %q)", got)
			}
			if !strings.Contains(err.Error(), tt.wantSubstr) {
				t.Fatalf("err = %q, want substring %q", err.Error(), tt.wantSubstr)
			}
			if got != "" {
				t.Fatalf("response = %q, want empty on error", got)
			}
		})
	}
}

// TestNewClientTimeout verifies the per-call timeout comes from config, with a
// default when unset.
func TestNewClientTimeout(t *testing.T) {
	custom := NewClient(config.LLMConfig{Provider: config.ProviderAnthropic, TimeoutSeconds: 30})
	if custom.timeout != 30*time.Second {
		t.Fatalf("timeout = %v, want 30s", custom.timeout)
	}
	def := NewClient(config.LLMConfig{Provider: config.ProviderAnthropic})
	if def.timeout != defaultTimeout {
		t.Fatalf("default timeout = %v, want %v", def.timeout, defaultTimeout)
	}
}

// TestCompleteTimeout verifies a hung provider fails with a timeout error rather
// than blocking forever.
func TestCompleteTimeout(t *testing.T) {
	block := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block // never respond until the test releases it
	}))
	defer server.Close()
	defer close(block)

	client := NewClient(config.LLMConfig{
		Provider: config.ProviderAnthropic,
		APIKey:   "test-key",
		Endpoint: server.URL,
	})
	client.timeout = 50 * time.Millisecond

	_, _, err := client.Complete(context.Background(), "system", "user")
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v, want a timeout error", err)
	}
}

// TestCompleteCancellation verifies a caller-cancelled context aborts the request
// and returns context.Canceled (the app uses this to drop the response).
func TestCompleteCancellation(t *testing.T) {
	started := make(chan struct{})
	block := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-block
	}))
	defer server.Close()
	defer close(block)

	client := NewClient(config.LLMConfig{
		Provider: config.ProviderAnthropic,
		APIKey:   "test-key",
		Endpoint: server.URL,
	})

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() {
		_, _, err := client.Complete(ctx, "system", "user")
		errCh <- err
	}()

	<-started // request is in flight
	cancel()
	if err := <-errCh; !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// TestCompleteRetriesTransientThenSucceeds verifies a 429/5xx is retried and a
// later success is returned (rather than surfacing the transient error).
func TestCompleteRetriesTransientThenSucceeds(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch atomic.AddInt32(&calls, 1) {
		case 1:
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"error":{"message":"slow down"}}`))
		case 2:
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"error":{"message":"overloaded"}}`))
		default:
			_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"recovered"}]}`))
		}
	}))
	defer server.Close()

	client := NewClient(config.LLMConfig{
		Provider: config.ProviderAnthropic,
		APIKey:   "test-key",
		Endpoint: server.URL,
	})

	got, _, err := client.Complete(context.Background(), "system", "user")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "recovered" {
		t.Fatalf("response = %q, want recovered", got)
	}
	if n := atomic.LoadInt32(&calls); n != 3 {
		t.Fatalf("server calls = %d, want 3 (initial + 2 retries)", n)
	}
}

// TestCompleteRetriesExhausted verifies a persistent transient failure retries
// exactly maxRetries times and then surfaces the last error.
func TestCompleteRetriesExhausted(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"slow down"}}`))
	}))
	defer server.Close()

	client := NewClient(config.LLMConfig{
		Provider: config.ProviderAnthropic,
		APIKey:   "test-key",
		Endpoint: server.URL,
	})

	_, _, err := client.Complete(context.Background(), "system", "user")
	if err == nil || !strings.Contains(err.Error(), "status 429") {
		t.Fatalf("err = %v, want a 429 error", err)
	}
	if n := atomic.LoadInt32(&calls); n != maxRetries+1 {
		t.Fatalf("server calls = %d, want %d (initial + %d retries)", n, maxRetries+1, maxRetries)
	}
}

// TestCompleteDoesNotRetryClientError verifies a non-retryable 4xx (e.g. 400)
// fails fast without burning retries.
func TestCompleteDoesNotRetryClientError(t *testing.T) {
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"bad model"}}`))
	}))
	defer server.Close()

	client := NewClient(config.LLMConfig{
		Provider: config.ProviderAnthropic,
		APIKey:   "test-key",
		Endpoint: server.URL,
	})

	_, _, err := client.Complete(context.Background(), "system", "user")
	if err == nil || !strings.Contains(err.Error(), "status 400") {
		t.Fatalf("err = %v, want a 400 error", err)
	}
	if n := atomic.LoadInt32(&calls); n != 1 {
		t.Fatalf("server calls = %d, want 1 (no retry on 4xx)", n)
	}
}

// 1h — provider error bodies are scrubbed before they reach the UI: structured
// messages are preferred, secrets redacted, whitespace collapsed, length capped.
func TestScrubErrorBody(t *testing.T) {
	if got := scrubErrorBody([]byte(`{"error":{"message":"rate limit exceeded"}}`)); got != "rate limit exceeded" {
		t.Fatalf("structured error message = %q, want %q", got, "rate limit exceeded")
	}

	redacted := sanitizeErrorText("auth failed for key sk-ant-abcdef123456 via Bearer sk-xyz7890123")
	if strings.Contains(redacted, "sk-ant-abcdef") || strings.Contains(redacted, "sk-xyz7890") {
		t.Fatalf("secret not redacted: %q", redacted)
	}
	if !strings.Contains(redacted, "[redacted]") {
		t.Fatalf("expected a [redacted] marker, got %q", redacted)
	}

	if got := sanitizeErrorText("line1\n\n  line2\tline3"); got != "line1 line2 line3" {
		t.Fatalf("whitespace not collapsed: %q", got)
	}

	long := scrubErrorBody([]byte(strings.Repeat("A", 5000)))
	if n := len([]rune(long)); n > maxErrorBodyLen+1 {
		t.Fatalf("long body not capped: %d runes", n)
	}
}
