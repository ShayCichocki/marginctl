package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/ShayCichocki/marginctl/internal/config"
)

const maxStreamBytes = 16 << 20

// StreamEvent is one item from a streaming completion. Exactly one terminal event
// (Done or Err) is delivered per stream, after zero or more Delta events.
type StreamEvent struct {
	Delta string // incremental text (non-terminal)
	Usage Usage  // cumulative token usage, populated on the Done event
	Done  bool   // stream completed successfully
	Err   error  // terminal failure
}

// StreamErrorKind classifies terminal stream failures for presentation and policy.
type StreamErrorKind uint8

const (
	StreamErrorUnknown StreamErrorKind = iota
	StreamErrorCanceled
	StreamErrorTimeout
	StreamErrorProtocol
	StreamErrorProvider
)

var (
	// ErrStreamProtocol means the provider stream was malformed or ended without
	// its required terminal event. Partial text must not be treated as complete.
	ErrStreamProtocol = errors.New("LLM stream protocol failure")
	// ErrStreamProvider means the provider explicitly failed or rejected a stream.
	ErrStreamProvider = errors.New("LLM provider failure")
)

// ClassifyStreamError gives callers a stable distinction between cancellation,
// timeout, protocol, and provider failures.
func ClassifyStreamError(err error) StreamErrorKind {
	switch {
	case err == nil:
		return StreamErrorUnknown
	case errors.Is(err, context.Canceled):
		return StreamErrorCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return StreamErrorTimeout
	case errors.Is(err, ErrStreamProtocol):
		return StreamErrorProtocol
	default:
		return StreamErrorProvider
	}
}

func protocolError(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrStreamProtocol, fmt.Sprintf(format, args...))
}

func providerError(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrStreamProvider, fmt.Sprintf(format, args...))
}

// CompleteStream streams a completion. It returns a channel that yields Delta
// events as tokens arrive, then exactly one terminal event (Done on success, Err
// on failure), then closes. The producing goroutine honors ctx: cancelling it
// aborts the request and unblocks any pending send, so a cancelled/superseded
// stream never leaks a goroutine. Streaming suits text results (continuity,
// summarize); diff/voice-guide flows stay on the one-shot Complete since they
// can't render until the full text is in hand.
func (c *Client) CompleteStream(ctx context.Context, systemPrompt, userPrompt string) <-chan StreamEvent {
	c = c.snapshot()
	out := make(chan StreamEvent, 8)
	go func() {
		defer close(out)
		ctx, cancel := context.WithTimeout(ctx, c.timeout)
		defer cancel()

		var usage Usage
		var err error
		if c.configErr != nil {
			out <- StreamEvent{Err: c.configErr}
			return
		}
		switch c.provider {
		case config.ProviderAnthropic:
			usage, err = c.streamAnthropic(ctx, systemPrompt, userPrompt, out)
		case config.ProviderChatGPT:
			usage, err = c.streamChatGPT(ctx, systemPrompt, userPrompt, out)
		case config.ProviderOpenAI, config.ProviderVercel:
			usage, err = c.streamOpenAI(ctx, systemPrompt, userPrompt, out)
		default:
			err = fmt.Errorf("unsupported LLM provider %q", c.provider)
		}
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				err = ctxErr
			}
			sendTerminal(ctx, out, StreamEvent{Err: err})
			return
		}
		sendTerminal(ctx, out, StreamEvent{Done: true, Usage: usage})
	}()
	return out
}

// sendStream delivers ev unless ctx is cancelled first, so a stopped reader can't
// wedge the producer. Returns false if the send was abandoned.
func sendStream(ctx context.Context, out chan<- StreamEvent, ev StreamEvent) bool {
	select {
	case out <- ev:
		return true
	case <-ctx.Done():
		return false
	}
}

// sendTerminal delivers the single terminal event (Done/Err). Unlike a delta it
// must reach a consumer that is still draining even after ctx is already done —
// that is how a cancellation or timeout surfaces — yet it must not block forever
// on a consumer that stopped reading. So it first tries a non-blocking send
// (reader ready, or buffer room — neither can leak), and only then blocks until
// either the consumer receives or ctx ends.
func sendTerminal(ctx context.Context, out chan<- StreamEvent, ev StreamEvent) {
	select {
	case out <- ev:
		return
	default:
	}
	select {
	case out <- ev:
	case <-ctx.Done():
	}
}

// openStream performs the streaming POST and returns the live response for the
// caller to scan. Non-200s are drained and returned as an error. Unlike send(),
// streaming makes a single attempt — retrying a partially consumed stream is not
// worth the complexity; the one-shot Complete path keeps the retry/backoff.
func (c *Client) openStream(ctx context.Context, body []byte, headers map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "POST", c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.client.Do(req)
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled):
			return nil, context.Canceled
		case errors.Is(err, context.DeadlineExceeded):
			return nil, fmt.Errorf("LLM request timed out after %s: %w", c.timeout, context.DeadlineExceeded)
		default:
			return nil, fmt.Errorf("request failed: %w", err)
		}
	}
	if resp.StatusCode != http.StatusOK {
		b, readErr := readLimited(resp.Body, maxErrorBodyBytes)
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, readErr
		}
		return nil, apiStatusError{statusCode: resp.StatusCode, body: scrubErrorBody(b)}
	}
	return resp, nil
}

func (c *Client) openOpenAICompatibleStream(ctx context.Context, body []byte, creds requestCredentials) (*http.Response, error) {
	resp, err := c.openStream(ctx, body, c.openAICompatibleHeaders(creds))
	if err == nil || c.provider != config.ProviderChatGPT {
		return resp, err
	}
	var statusErr apiStatusError
	if !errors.As(err, &statusErr) || statusErr.statusCode != http.StatusUnauthorized {
		return nil, err
	}
	refreshed, refreshErr := c.refreshCredentials(ctx)
	if refreshErr != nil {
		return nil, refreshErr
	}
	return c.openStream(ctx, body, c.openAICompatibleHeaders(refreshed))
}

// sseScanner returns a line scanner sized for SSE (data lines can be large).
func sseScanner(r io.Reader) *bufio.Scanner {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	return sc
}

type streamLimitReader struct {
	r         io.Reader
	remaining int64
}

func (r *streamLimitReader) Read(p []byte) (int, error) {
	if r.remaining <= 0 {
		return 0, errors.New("provider stream exceeds size limit")
	}
	if int64(len(p)) > r.remaining+1 {
		p = p[:r.remaining+1]
	}
	n, err := r.r.Read(p)
	r.remaining -= int64(n)
	if r.remaining < 0 {
		return n, errors.New("provider stream exceeds size limit")
	}
	return n, err
}

// finishStream applies the same terminal guards as the non-streaming path so a
// streamed completion can't end as a silent empty/truncated/refused result.
func finishStream(text string, refused, truncated bool) error {
	if refused {
		return ErrRefused
	}
	if strings.TrimSpace(text) == "" {
		return ErrEmptyResponse
	}
	if truncated {
		return ErrTruncated
	}
	return nil
}

func (c *Client) streamAnthropic(ctx context.Context, systemPrompt, userPrompt string, out chan<- StreamEvent) (Usage, error) {
	creds, err := c.requestCredentials(ctx)
	if err != nil {
		return Usage{}, err
	}
	req := anthropicRequest{
		Model:     c.model,
		MaxTokens: maxResponseTokens,
		System:    systemPrompt,
		Messages:  []Message{{Role: "user", Content: userPrompt}},
		Stream:    true,
	}
	body, err := json.Marshal(req)
	if err != nil {
		return Usage{}, fmt.Errorf("failed to marshal request: %w", err)
	}
	resp, err := c.openStream(ctx, body, map[string]string{
		"Content-Type":      "application/json",
		"x-api-key":         creds.APIKey,
		"anthropic-version": apiVersion,
	})
	if err != nil {
		return Usage{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	var full strings.Builder
	var stopReason string
	var usage Usage
	var completed bool
	sc := sseScanner(&streamLimitReader{r: resp.Body, remaining: maxStreamBytes})
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(line[len("data:"):])
		if data == "" {
			continue
		}
		var ev struct {
			Type    string `json:"type"`
			Message struct {
				Usage Usage `json:"usage"`
			} `json:"message"`
			Delta struct {
				Type       string `json:"type"`
				Text       string `json:"text"`
				StopReason string `json:"stop_reason"`
			} `json:"delta"`
			Usage Usage     `json:"usage"`
			Error *APIError `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return usage, protocolError("invalid Anthropic SSE frame: %v", err)
		}
		switch ev.Type {
		case "message_start":
			if ev.Message.Usage.InputTokens > 0 {
				usage.InputTokens = ev.Message.Usage.InputTokens
			}
		case "content_block_delta":
			if ev.Delta.Type == "text_delta" && ev.Delta.Text != "" {
				full.WriteString(ev.Delta.Text)
				if !sendStream(ctx, out, StreamEvent{Delta: ev.Delta.Text}) {
					return usage, context.Canceled
				}
			}
		case "message_delta":
			if ev.Delta.StopReason != "" {
				stopReason = ev.Delta.StopReason
			}
			if ev.Usage.OutputTokens > 0 {
				usage.OutputTokens = ev.Usage.OutputTokens
			}
		case "error":
			if ev.Error != nil {
				return usage, providerError("API error: %s", sanitizeErrorText(ev.Error.Message))
			}
			return usage, providerError("streaming error")
		case "message_stop":
			completed = true
		default:
			if ev.Type == "" {
				return usage, protocolError("unrecognized Anthropic SSE frame")
			}
		}
	}
	if err := sc.Err(); err != nil {
		if errors.Is(err, context.Canceled) {
			return usage, context.Canceled
		}
		return usage, fmt.Errorf("stream read failed: %w", err)
	}
	if !completed {
		return usage, protocolError("Anthropic stream ended before message_stop")
	}
	return usage, finishStream(full.String(), stopReason == "refusal", stopReason == "max_tokens")
}

func (c *Client) streamOpenAI(ctx context.Context, systemPrompt, userPrompt string, out chan<- StreamEvent) (Usage, error) {
	creds, err := c.requestCredentials(ctx)
	if err != nil {
		return Usage{}, err
	}
	req := openAIRequest{
		Model:    c.model,
		Messages: []Message{{Role: "system", Content: systemPrompt}, {Role: "user", Content: userPrompt}},
		Stream:   true,
		// Ask for a usage block in the final chunk (omitted by default when streaming).
		StreamOptions: &streamOptions{IncludeUsage: true},
	}
	req.setMaxTokens(c.model, maxResponseTokens)
	body, err := json.Marshal(req)
	if err != nil {
		return Usage{}, fmt.Errorf("failed to marshal request: %w", err)
	}
	resp, err := c.openOpenAICompatibleStream(ctx, body, creds)
	if err != nil {
		return Usage{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	var full strings.Builder
	var finish, refusal string
	var usage Usage
	var completed bool
	sc := sseScanner(&streamLimitReader{r: resp.Body, remaining: maxStreamBytes})
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(line[len("data:"):])
		if data == "" {
			continue
		}
		if data == "[DONE]" {
			completed = true
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
					Refusal string `json:"refusal"`
				} `json:"delta"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage *openAIUsage `json:"usage"`
			Error *APIError    `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			return usage, protocolError("invalid OpenAI SSE frame: %v", err)
		}
		if chunk.Error != nil {
			return usage, providerError("API error: %s", sanitizeErrorText(chunk.Error.Message))
		}
		// Tolerate empty keep-alive/role-only frames (no choices, no usage) that some
		// OpenAI-compatible gateways emit: they carry no text, so skipping one can't
		// turn a partial result into a "complete" one — the [DONE] guard still gates.
		if len(chunk.Choices) == 0 && chunk.Usage == nil {
			continue
		}
		if chunk.Usage != nil {
			usage = Usage{InputTokens: chunk.Usage.PromptTokens, OutputTokens: chunk.Usage.CompletionTokens}
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		ch := chunk.Choices[0]
		if ch.Delta.Refusal != "" {
			refusal = ch.Delta.Refusal
		}
		if ch.FinishReason != "" {
			finish = ch.FinishReason
		}
		if ch.Delta.Content != "" {
			full.WriteString(ch.Delta.Content)
			if !sendStream(ctx, out, StreamEvent{Delta: ch.Delta.Content}) {
				return usage, context.Canceled
			}
		}
	}
	if err := sc.Err(); err != nil {
		if errors.Is(err, context.Canceled) {
			return usage, context.Canceled
		}
		return usage, fmt.Errorf("stream read failed: %w", err)
	}
	if !completed {
		return usage, protocolError("OpenAI stream ended before [DONE]")
	}
	refused := refusal != "" || finish == "content_filter"
	return usage, finishStream(full.String(), refused, finish == "length")
}
