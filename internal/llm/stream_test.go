package llm

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/ShayCichocki/marginctl/internal/config"
)

// sseServer returns a test server that writes each arg as an SSE `data:` frame.
func sseServer(frames ...string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, f := range frames {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", f)
		}
	}))
}

// drainEvents returns the deltas joined plus the terminal event.
func drainEvents(ch <-chan StreamEvent) (string, StreamEvent) {
	var b strings.Builder
	var term StreamEvent
	for ev := range ch {
		if ev.Done || ev.Err != nil {
			term = ev
			break
		}
		b.WriteString(ev.Delta)
	}
	return b.String(), term
}

func TestCompleteStreamAnthropic(t *testing.T) {
	server := sseServer(
		`{"type":"message_start","message":{"usage":{"input_tokens":12}}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello "}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"world"}}`,
		`{"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":5}}`,
		`{"type":"message_stop"}`,
	)
	defer server.Close()

	client := NewClient(config.LLMConfig{Provider: config.ProviderAnthropic, APIKey: "k", Endpoint: server.URL})
	text, term := drainEvents(client.CompleteStream(context.Background(), "sys", "user"))
	if text != "Hello world" {
		t.Fatalf("text = %q, want %q", text, "Hello world")
	}
	if !term.Done || term.Err != nil {
		t.Fatalf("terminal = %+v, want Done", term)
	}
	if term.Usage.InputTokens != 12 || term.Usage.OutputTokens != 5 {
		t.Fatalf("usage = %+v, want 12/5", term.Usage)
	}
}

func TestCompleteStreamOpenAI(t *testing.T) {
	server := sseServer(
		`{"choices":[{"delta":{"content":"Hel"},"finish_reason":null}]}`,
		`{"choices":[{"delta":{"content":"lo"},"finish_reason":"stop"}]}`,
		`{"choices":[],"usage":{"prompt_tokens":8,"completion_tokens":2}}`,
		`[DONE]`,
	)
	defer server.Close()

	client := NewClient(config.LLMConfig{Provider: config.ProviderOpenAI, APIKey: "k", Endpoint: server.URL})
	text, term := drainEvents(client.CompleteStream(context.Background(), "sys", "user"))
	if text != "Hello" {
		t.Fatalf("text = %q, want Hello", text)
	}
	if !term.Done || term.Usage.InputTokens != 8 || term.Usage.OutputTokens != 2 {
		t.Fatalf("terminal = %+v, want Done + 8/2 usage", term)
	}
}

func TestCompleteStreamRejectsDeltaThenEOF(t *testing.T) {
	cases := []struct {
		name     string
		provider string
		frame    string
	}{
		{"anthropic", config.ProviderAnthropic, `{"type":"content_block_delta","delta":{"type":"text_delta","text":"partial"}}`},
		{"openai", config.ProviderOpenAI, `{"choices":[{"delta":{"content":"partial"}}]}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := sseServer(tc.frame)
			defer server.Close()
			client := NewClient(config.LLMConfig{Provider: tc.provider, APIKey: "k", Endpoint: server.URL})
			text, term := drainEvents(client.CompleteStream(context.Background(), "s", "u"))
			if text != "partial" || !errors.Is(term.Err, ErrStreamProtocol) {
				t.Fatalf("text = %q, terminal = %+v, want partial protocol failure", text, term)
			}
		})
	}
}

func TestCompleteStreamRejectsMalformedFrames(t *testing.T) {
	for _, provider := range []string{config.ProviderAnthropic, config.ProviderOpenAI} {
		t.Run(provider, func(t *testing.T) {
			server := sseServer(`{"broken":`)
			defer server.Close()
			client := NewClient(config.LLMConfig{Provider: provider, APIKey: "k", Endpoint: server.URL})
			_, term := drainEvents(client.CompleteStream(context.Background(), "s", "u"))
			if !errors.Is(term.Err, ErrStreamProtocol) || ClassifyStreamError(term.Err) != StreamErrorProtocol {
				t.Fatalf("terminal err = %v, want classified protocol failure", term.Err)
			}
		})
	}
}

// A stream that yields no text ends as ErrEmptyResponse (never a silent success).
func TestCompleteStreamEmptyIsError(t *testing.T) {
	server := sseServer(`{"type":"message_stop"}`)
	defer server.Close()
	client := NewClient(config.LLMConfig{Provider: config.ProviderAnthropic, APIKey: "k", Endpoint: server.URL})
	_, term := drainEvents(client.CompleteStream(context.Background(), "s", "u"))
	if !errors.Is(term.Err, ErrEmptyResponse) {
		t.Fatalf("terminal err = %v, want ErrEmptyResponse", term.Err)
	}
}

// A truncated stream (stop_reason max_tokens) ends as ErrTruncated.
func TestCompleteStreamTruncatedIsError(t *testing.T) {
	server := sseServer(
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"half a sen"}}`,
		`{"type":"message_delta","delta":{"stop_reason":"max_tokens"}}`,
		`{"type":"message_stop"}`,
	)
	defer server.Close()
	client := NewClient(config.LLMConfig{Provider: config.ProviderAnthropic, APIKey: "k", Endpoint: server.URL})
	_, term := drainEvents(client.CompleteStream(context.Background(), "s", "u"))
	if !errors.Is(term.Err, ErrTruncated) {
		t.Fatalf("terminal err = %v, want ErrTruncated", term.Err)
	}
}

// A non-200 surfaces as a terminal error, not a hang.
func TestCompleteStreamNon200(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"slow down"}}`))
	}))
	defer server.Close()
	client := NewClient(config.LLMConfig{Provider: config.ProviderAnthropic, APIKey: "k", Endpoint: server.URL})
	_, term := drainEvents(client.CompleteStream(context.Background(), "s", "u"))
	if term.Err == nil || !strings.Contains(term.Err.Error(), "status 429") {
		t.Fatalf("terminal err = %v, want status 429", term.Err)
	}
}

func TestCompleteStreamAnthropicRedactsSSEErrorMessage(t *testing.T) {
	server := sseServer(`{"type":"error","error":{"message":"bearer Bearer sk-ant-fake-123456 refresh_token fake-refresh-token-abcdef123"}}`)
	defer server.Close()

	client := NewClient(config.LLMConfig{Provider: config.ProviderAnthropic, APIKey: "k", Endpoint: server.URL})
	_, term := drainEvents(client.CompleteStream(context.Background(), "s", "u"))
	if term.Err == nil || !strings.Contains(term.Err.Error(), "API error:") {
		t.Fatalf("terminal err = %v, want API error", term.Err)
	}
	if strings.Contains(term.Err.Error(), "sk-ant-fake-123456") || strings.Contains(term.Err.Error(), "fake-refresh-token-abcdef123") {
		t.Fatalf("terminal err leaked secret: %q", term.Err.Error())
	}
}

// Cancelling ctx mid-stream must terminate the producer (channel closes) rather
// than leak a goroutine blocked on send or read.
func TestCompleteStreamCancellation(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\n")
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-release // hold the connection open until the test ends
	}))
	defer server.Close()
	defer close(release)

	client := NewClient(config.LLMConfig{Provider: config.ProviderAnthropic, APIKey: "k", Endpoint: server.URL})
	ctx, cancel := context.WithCancel(context.Background())
	ch := client.CompleteStream(ctx, "s", "u")

	if ev := <-ch; ev.Delta != "hi" {
		t.Fatalf("first delta = %+v, want text 'hi'", ev)
	}
	cancel()
	select {
	case term := <-ch:
		if !errors.Is(term.Err, context.Canceled) || ClassifyStreamError(term.Err) != StreamErrorCanceled {
			t.Fatalf("terminal = %+v, want cancellation error", term)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not deliver terminal cancellation")
	}
}

func TestCompleteStreamTimeoutAfterDeltaDeliversTerminalError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"partial\"}}\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()

	client := NewClient(config.LLMConfig{Provider: config.ProviderAnthropic, APIKey: "k", Endpoint: server.URL})
	client.timeout = 20 * time.Millisecond
	text, term := drainEvents(client.CompleteStream(context.Background(), "s", "u"))
	if text != "partial" || !errors.Is(term.Err, context.DeadlineExceeded) || ClassifyStreamError(term.Err) != StreamErrorTimeout {
		t.Fatalf("text = %q, terminal = %+v, want partial then timeout", text, term)
	}
}

// An unconfigured client yields a terminal error rather than dialing out.
func TestCompleteStreamUnconfigured(t *testing.T) {
	client := NewClient(config.LLMConfig{Provider: config.ProviderAnthropic})
	_, term := drainEvents(client.CompleteStream(context.Background(), "s", "u"))
	if term.Err == nil || !strings.Contains(term.Err.Error(), "API key not configured") {
		t.Fatalf("terminal err = %v, want not-configured", term.Err)
	}
}
