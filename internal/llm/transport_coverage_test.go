package llm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ShayCichocki/marginctl/internal/auth"
	"github.com/ShayCichocki/marginctl/internal/config"
)

func mustReq(t *testing.T, raw string) *http.Request {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Request{URL: u, Header: http.Header{}}
}

func TestSecureRedirectDecisionTable(t *testing.T) {
	cases := []struct {
		name     string
		from, to string
		refused  bool
	}{
		{"same host and path change", "https://api.example.com/v1/a", "https://api.example.com/v1/b", false},
		{"same host case-insensitive", "https://API.example.com/a", "https://api.EXAMPLE.com/b", false},
		{"explicit default port equals implicit", "https://api.example.com/a", "https://api.example.com:443/b", false},
		{"cross host", "https://api.example.com/a", "https://evil.example.net/a", true},
		{"subdomain is cross host", "https://api.example.com/a", "https://x.api.example.com/a", true},
		{"cross port", "http://127.0.0.1:8000/a", "http://127.0.0.1:9000/a", true},
		{"https to http downgrade same host", "https://api.example.com/a", "http://api.example.com/a", true},
		{"http to https upgrade changes scheme", "http://localhost/a", "https://localhost/a", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := secureRedirect(mustReq(t, tc.to), []*http.Request{mustReq(t, tc.from)})
			if (err != nil) != tc.refused {
				t.Fatalf("secureRedirect(%s -> %s) = %v, refused want %v", tc.from, tc.to, err, tc.refused)
			}
		})
	}
	if err := secureRedirect(mustReq(t, "https://a/"), nil); err != nil {
		t.Fatalf("no via must be allowed: %v", err)
	}
}

func TestCompleteRefusesCrossOriginRedirectAndNeverLeaksCredentials(t *testing.T) {
	var targetHits int32
	var leaked atomic.Value
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&targetHits, 1)
		leaked.Store(r.Header.Get("Authorization") + r.Header.Get("x-api-key"))
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"pwned"}}]}`))
	}))
	defer target.Close()
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/steal", http.StatusTemporaryRedirect)
	}))
	defer origin.Close()

	for _, provider := range []string{config.ProviderOpenAI, config.ProviderAnthropic} {
		client := NewClient(config.LLMConfig{Provider: provider, APIKey: "secret-key", Endpoint: origin.URL})
		if _, _, err := client.Complete(context.Background(), "s", "u"); err == nil {
			t.Fatalf("%s: cross-origin redirect must fail", provider)
		}
	}
	if n := atomic.LoadInt32(&targetHits); n != 0 {
		t.Fatalf("redirect target was contacted %d times", n)
	}
	if v, _ := leaked.Load().(string); v != "" {
		t.Fatalf("credentials leaked: %q", v)
	}
}

func TestCompleteFollowsSameOriginRedirect(t *testing.T) {
	var hits int32
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, srv.URL+"/final", http.StatusTemporaryRedirect)
			return
		}
		atomic.AddInt32(&hits, 1)
		if r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("same-origin redirect dropped Authorization: %q", r.Header.Get("Authorization"))
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer srv.Close()
	client := NewClient(config.LLMConfig{Provider: config.ProviderOpenAI, APIKey: "k", Endpoint: srv.URL + "/start"})
	got, _, err := client.Complete(context.Background(), "s", "u")
	if err != nil || got != "ok" || atomic.LoadInt32(&hits) != 1 {
		t.Fatalf("got=%q err=%v hits=%d", got, err, hits)
	}
}

func TestValidateEndpointMatrix(t *testing.T) {
	ok := []string{
		"https://api.openai.com/v1/chat/completions",
		"http://localhost:11434/v1/chat/completions",
		"http://127.0.0.1:8080/x",
		"http://[::1]:8080/x",
		"http://127.1.2.3/x",
	}
	bad := []string{
		"http://api.openai.com/v1",
		"http://example.com",
		"http://10.0.0.5:8080/x",
		"http://0.0.0.0/x",
		"http://localhost.evil.com/x",
		"ftp://localhost/x",
		"localhost:8080/x",
		"https://",
		"",
		"://bad",
	}
	for _, e := range ok {
		if err := validateEndpoint(e); err != nil {
			t.Errorf("validateEndpoint(%q) = %v, want ok", e, err)
		}
	}
	for _, e := range bad {
		if err := validateEndpoint(e); err == nil {
			t.Errorf("validateEndpoint(%q) accepted", e)
		}
	}
}

func TestChatGPT401RefreshRetryHappensAtMostOnce(t *testing.T) {
	storePath := filepath.Join(t.TempDir(), "auth.json")
	now := time.Date(2026, 7, 9, 15, 0, 0, 0, time.UTC)
	var refreshCalls, apiCalls int32
	issuer := newRefreshIssuer(t, &refreshCalls, http.StatusOK, `{"access_token":"new-access","refresh_token":"new-refresh","expires_in":1800}`)
	defer issuer.Close()
	if err := auth.SaveTokenStore(storePath, chatGPTRefreshTokenStore(issuer.URL, "old-access", "old-refresh", now.Add(time.Hour))); err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&apiCalls, 1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"detail":"still unauthorized"}`))
	}))
	defer api.Close()
	client := newChatGPTRefreshTestClient(storePath, now)
	client.endpoint = api.URL

	if _, _, err := client.Complete(context.Background(), "s", "u"); err == nil {
		t.Fatal("persistent 401 must surface as an error")
	}
	if n := atomic.LoadInt32(&apiCalls); n != 2 {
		t.Fatalf("api calls = %d, want exactly initial + one retry", n)
	}
	if n := atomic.LoadInt32(&refreshCalls); n != 1 {
		t.Fatalf("refresh calls = %d, want 1", n)
	}
}

func TestAPIKeyProvider401IsNotRetried(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()
	client := NewClient(config.LLMConfig{Provider: config.ProviderOpenAI, APIKey: "k", Endpoint: srv.URL})
	if _, _, err := client.Complete(context.Background(), "s", "u"); err == nil {
		t.Fatal("expected error")
	}
	if calls != 1 {
		t.Fatalf("calls = %d, want 1", calls)
	}
}

func TestRetryAfterHTTPDateAndEdgeValues(t *testing.T) {
	h := http.Header{}
	h.Set("Retry-After", time.Now().Add(30*time.Second).UTC().Format(http.TimeFormat))
	if d := retryAfter(h); d <= 20*time.Second || d > 30*time.Second {
		t.Fatalf("future HTTP-date = %v, want ~30s", d)
	}
	h.Set("Retry-After", time.Now().Add(-time.Hour).UTC().Format(http.TimeFormat))
	if d := retryAfter(h); d != 0 {
		t.Fatalf("past HTTP-date = %v, want 0", d)
	}
	for _, v := range []string{"-5", "abc", "1.5", ""} {
		h.Set("Retry-After", v)
		if d := retryAfter(h); d != 0 {
			t.Errorf("Retry-After %q = %v, want 0", v, d)
		}
	}
	h.Set("Retry-After", " 3 ")
	if d := retryAfter(h); d != 3*time.Second {
		t.Errorf("padded numeric = %v", d)
	}
}

// A Retry-After HTTP-date further out than the per-call timeout is clamped by
// the context: the caller sees a timeout, not the stale 429, and does not wait.
func TestRetryAfterHTTPDateBeyondTimeoutReportsTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", time.Now().Add(time.Hour).UTC().Format(http.TimeFormat))
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()
	client := NewClient(config.LLMConfig{Provider: config.ProviderOpenAI, APIKey: "k", Endpoint: srv.URL})
	client.timeout = 100 * time.Millisecond
	start := time.Now()
	_, _, err := client.Complete(context.Background(), "s", "u")
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("err = %v, want timeout", err)
	}
	var se apiStatusError
	if errors.As(err, &se) {
		t.Fatalf("must not surface HTTP status: %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("waited %v", time.Since(start))
	}
}

func TestRetryAfterNumericIsHonored(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer srv.Close()
	client := NewClient(config.LLMConfig{Provider: config.ProviderOpenAI, APIKey: "k", Endpoint: srv.URL})
	start := time.Now()
	if _, _, err := client.Complete(context.Background(), "s", "u"); err != nil {
		t.Fatal(err)
	}
	if el := time.Since(start); el < 900*time.Millisecond {
		t.Fatalf("Retry-After: 1 not honored, elapsed %v", el)
	}
}

func TestCtxErrorMapping(t *testing.T) {
	c, cancel := context.WithCancel(context.Background())
	cancel()
	if err := ctxError(c, time.Second); !errors.Is(err, context.Canceled) {
		t.Errorf("cancel => %v", err)
	}
	d, cancel2 := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel2()
	<-d.Done()
	if err := ctxError(d, 3*time.Second); err == nil || !strings.Contains(err.Error(), "timed out after 3s") {
		t.Errorf("deadline => %v", err)
	}
}

func TestConnectionErrorBackoffCancelReportsCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	endpoint := srv.URL
	srv.Close() // connection refused from here on
	old := retryBaseDelay
	retryBaseDelay = 5 * time.Second
	defer func() { retryBaseDelay = old }()
	client := NewClient(config.LLMConfig{Provider: config.ProviderOpenAI, APIKey: "k", Endpoint: endpoint})
	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(30 * time.Millisecond); cancel() }()
	_, _, err := client.Complete(ctx, "s", "u")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestDefaultModelsHavePricingAndTokenRouting(t *testing.T) {
	// Anthropic default must have a rate; OpenAI/Vercel defaults deliberately
	// have none (rates change often) and must say so rather than guess.
	if _, ok := EstimateCost(anthropicModel, Usage{InputTokens: 1e6, OutputTokens: 1e6}); !ok {
		t.Errorf("anthropic default %q has no pricing entry", anthropicModel)
	}
	if cost, _ := EstimateCost(anthropicModel, Usage{InputTokens: 1e6, OutputTokens: 1e6}); cost != 18 {
		t.Errorf("cost = %v, want 18", cost)
	}
	for _, m := range []string{openAIModel, vercelModel} {
		if _, ok := EstimateCost(m, Usage{InputTokens: 1}); ok {
			t.Errorf("%q unexpectedly priced; update this test and CLAUDE.md", m)
		}
	}
	if _, ok := EstimateCost("claude-haiku-4-5-20251001", Usage{}); !ok {
		t.Error("dated snapshot should resolve by prefix")
	}
	if _, ok := EstimateCost("", Usage{}); ok {
		t.Error("empty model must not price")
	}
	// Token-field routing for each default.
	want := map[string]bool{anthropicModel: false, openAIModel: true, vercelModel: true}
	for m, w := range want {
		if got := modelRequiresMaxCompletionTokens(m); got != w {
			t.Errorf("modelRequiresMaxCompletionTokens(default %q) = %v, want %v", m, got, w)
		}
	}
	// Provider -> default model/endpoint wiring.
	for provider, wm := range map[string]string{
		config.ProviderAnthropic: anthropicModel,
		config.ProviderOpenAI:    openAIModel,
		config.ProviderChatGPT:   openAIModel,
		config.ProviderVercel:    vercelModel,
		"":                       anthropicModel,
	} {
		if got := defaultModel(provider); got != wm {
			t.Errorf("defaultModel(%q) = %q, want %q", provider, got, wm)
		}
		if defaultEndpoint(provider) == "" {
			t.Errorf("defaultEndpoint(%q) empty", provider)
		}
	}
	// Prefix heuristic edges beyond the existing table.
	for m, w := range map[string]bool{
		"GPT-5.6-SOL": true, "  gpt-5 ": true, "gpt-10": true, "gpt-50-x": true,
		"o5": false, "o1-preview": true, "gpt-": false, "gpt-x5": false,
		"vercel/openai/gpt-5.6-sol": true, "chatgpt-4o-latest": false, "gpt-4.5-preview": false,
	} {
		if got := modelRequiresMaxCompletionTokens(m); got != w {
			t.Errorf("modelRequiresMaxCompletionTokens(%q) = %v, want %v", m, got, w)
		}
	}
}

func TestStreamToleratesCommentsEventLinesAndCRLF(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, ": keepalive\r\n\r\n")
		fmt.Fprint(w, "event: message\r\ndata: {\"choices\":[{\"delta\":{\"content\":\"Hi\"},\"finish_reason\":\"stop\"}]}\r\n\r\n")
		fmt.Fprint(w, "data: [DONE]\r\n\r\n")
	}))
	defer srv.Close()
	client := NewClient(config.LLMConfig{Provider: config.ProviderOpenAI, APIKey: "k", Endpoint: srv.URL})
	text, term := drainEvents(client.CompleteStream(context.Background(), "s", "u"))
	if text != "Hi" || !term.Done {
		t.Fatalf("text=%q term=%+v", text, term)
	}
}

func TestStreamMalformedAfterValidDeltaIsProtocolError(t *testing.T) {
	srv := sseServer(
		`{"choices":[{"delta":{"content":"ok"}}]}`,
		`{"choices":[{"delta":`,
		`[DONE]`,
	)
	defer srv.Close()
	client := NewClient(config.LLMConfig{Provider: config.ProviderOpenAI, APIKey: "k", Endpoint: srv.URL})
	_, term := drainEvents(client.CompleteStream(context.Background(), "s", "u"))
	if term.Done || !errors.Is(term.Err, ErrStreamProtocol) {
		t.Fatalf("term = %+v, want protocol error", term)
	}
}

func TestStreamDoneWithoutAnyTextIsNotSuccess(t *testing.T) {
	srv := sseServer(`[DONE]`)
	defer srv.Close()
	client := NewClient(config.LLMConfig{Provider: config.ProviderOpenAI, APIKey: "k", Endpoint: srv.URL})
	_, term := drainEvents(client.CompleteStream(context.Background(), "s", "u"))
	if term.Done || term.Err == nil {
		t.Fatalf("term = %+v, want error", term)
	}
}

func waitGoroutines(t *testing.T, base int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for {
		runtime.GC()
		if n := runtime.NumGoroutine(); n <= base {
			return
		}
		if time.Now().After(deadline) {
			buf := make([]byte, 1<<16)
			buf = buf[:runtime.Stack(buf, true)]
			t.Fatalf("goroutines %d > baseline %d\n%s", runtime.NumGoroutine(), base, buf)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A consumer that stops reading and cancels must not leave the producer
// goroutine (or its HTTP connection reader) behind, even with far more frames
// than the channel buffer holds.
func TestStreamAbandonedConsumerLeaksNoGoroutines(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for i := 0; i < 200; i++ {
			if _, err := fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n"); err != nil {
				return
			}
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
		<-r.Context().Done()
	}))
	client := NewClient(config.LLMConfig{Provider: config.ProviderOpenAI, APIKey: "k", Endpoint: srv.URL})
	srv.CloseClientConnections() // no-op warmup; keeps base measurement honest
	base := runtime.NumGoroutine()

	for i := 0; i < 5; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		ch := client.CompleteStream(ctx, "s", "u")
		<-ch // read one delta, then abandon
		cancel()
	}
	srv.Close()
	client.client.CloseIdleConnections()
	if tr, ok := http.DefaultTransport.(*http.Transport); ok {
		tr.CloseIdleConnections()
	}
	waitGoroutines(t, base)
}
