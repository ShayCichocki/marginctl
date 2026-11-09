package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

var errChatGPTContractChanged = errors.New("ChatGPT backend contract changed")

func chatGPTContractError(err error) error {
	if err == nil {
		return fmt.Errorf("%w: %w", ErrStreamProtocol, errChatGPTContractChanged)
	}
	return fmt.Errorf("%w: %w: %s", ErrStreamProtocol, errChatGPTContractChanged, sanitizeErrorText(err.Error()))
}

// The ChatGPT codex backend (chatgpt.com/backend-api/codex/responses) speaks the
// OpenAI Responses API, not chat-completions: it rejects `messages`/`max_tokens`/
// `max_output_tokens`, requires `store:false`, and requires `stream:true` (it has
// no non-streaming mode — a one-shot request returns `Stream must be set to
// true`). So the chatgpt provider always streams; the one-shot Complete path
// consumes the same SSE stream and accumulates it before returning. The request
// carries only the exact params the backend accepts, reusing the shared transport
// (OAuth headers + 401 refresh) from the OpenAI-compatible path.

type responsesRequest struct {
	Model        string               `json:"model"`
	Instructions string               `json:"instructions,omitempty"`
	Input        []responsesInputItem `json:"input"`
	// Store must be sent explicitly as false (no omitempty): a request that would
	// persist server-side is rejected with `Store must be set to false`. Stream is
	// always true: the backend rejects non-streaming with `Stream must be set to
	// true`. No max_output_tokens/max_tokens — both are rejected as unsupported.
	Store  bool `json:"store"`
	Stream bool `json:"stream"`
}

type responsesInputItem struct {
	Type    string                 `json:"type"`
	Role    string                 `json:"role"`
	Content []responsesContentPart `json:"content"`
}

type responsesContentPart struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// responsesResponse is the payload carried on a `response.completed`/
// `response.incomplete`/`response.failed` SSE event (the codex backend never
// returns a non-streamed body).
type responsesResponse struct {
	Usage             *responsesUsage      `json:"usage"`
	IncompleteDetails *responsesIncomplete `json:"incomplete_details"`
	Error             *APIError            `json:"error"`
}

type responsesUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type responsesIncomplete struct {
	Reason string `json:"reason"`
}

func (r responsesResponse) usage() Usage {
	if r.Usage == nil {
		return Usage{}
	}
	return Usage{InputTokens: r.Usage.InputTokens, OutputTokens: r.Usage.OutputTokens}
}

// truncated reports whether the model stopped because it hit its output-token
// limit (the codex backend surfaces this as incomplete_details.reason).
func (r responsesResponse) truncated() bool {
	return r.incompleteReason() == "max_output_tokens"
}

// incompleteReason returns the backend's reason for an incomplete response
// (e.g. "max_output_tokens", "content_filter"), or "" when absent.
func (r responsesResponse) incompleteReason() string {
	if r.IncompleteDetails == nil {
		return ""
	}
	return r.IncompleteDetails.Reason
}

func (c *Client) newResponsesRequest(systemPrompt, userPrompt string) responsesRequest {
	return responsesRequest{
		Model:        c.model,
		Instructions: systemPrompt,
		Input: []responsesInputItem{{
			Type: "message",
			Role: "user",
			Content: []responsesContentPart{{
				Type: "input_text",
				Text: userPrompt,
			}},
		}},
		Store:  false,
		Stream: true,
	}
}

// responsesResult is the outcome of consuming a codex Responses SSE stream.
type responsesResult struct {
	text      string
	usage     Usage
	refused   bool
	truncated bool
	completed bool
}

// openResponsesStream marshals and sends a Responses request, returning the live
// SSE response for the caller to consume. Both the one-shot and streaming chatgpt
// paths go through here so they share the OAuth headers + 401→refresh retry.
func (c *Client) openResponsesStream(ctx context.Context, systemPrompt, userPrompt string) (*http.Response, error) {
	creds, err := c.requestCredentials(ctx)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(c.newResponsesRequest(systemPrompt, userPrompt))
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}
	return c.openOpenAICompatibleStream(ctx, body, creds)
}

// openResponsesStreamRetrying opens a codex Responses stream, retrying transient
// status failures (429/5xx) before any body is consumed. The one-shot Complete
// path (diff/voice-guide) needs this resilience to match the chat-completions
// retry; streamChatGPT keeps a single attempt like the other live streams, since
// a partially consumed stream can't be safely retried.
func (c *Client) openResponsesStreamRetrying(ctx context.Context, systemPrompt, userPrompt string) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		resp, err := c.openResponsesStream(ctx, systemPrompt, userPrompt)
		if err == nil {
			return resp, nil
		}
		var statusErr apiStatusError
		if attempt >= maxRetries || !errors.As(err, &statusErr) || !isRetryableStatus(statusErr.statusCode) {
			return nil, err
		}
		if !c.backoff(ctx, attempt, 0) {
			return nil, ctxError(ctx, c.timeout)
		}
	}
}

// readResponsesStream scans a codex Responses SSE body, invoking onDelta for each
// text delta (return a non-nil error to abort, e.g. a cancelled channel send),
// and returns the accumulated text plus terminal state. A stream that yields no
// recognized event is treated as a backend contract change.
func (c *Client) readResponsesStream(resp *http.Response, onDelta func(string) error) (responsesResult, error) {
	var full strings.Builder
	var res responsesResult
	var sawEvent bool
	sc := sseScanner(&streamLimitReader{r: resp.Body, remaining: maxStreamBytes})
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(line[len("data:"):])
		// Responses SSE terminates with `response.completed`; tolerate a stray
		// `[DONE]`/blank so a lenient gateway can't wedge the read.
		if data == "" || data == "[DONE]" {
			continue
		}
		var ev struct {
			Type     string             `json:"type"`
			Delta    string             `json:"delta"`
			Response *responsesResponse `json:"response"`
			Error    *APIError          `json:"error"`
		}
		if err := json.Unmarshal([]byte(data), &ev); err != nil {
			return res, chatGPTContractError(fmt.Errorf("parse stream chunk: %w", err))
		}
		// A top-level error object (with or without a type) is terminal.
		if ev.Error != nil {
			return res, fmt.Errorf("API error: %s", sanitizeErrorText(ev.Error.Message))
		}
		switch ev.Type {
		case "response.output_text.delta":
			sawEvent = true
			if ev.Delta != "" {
				full.WriteString(ev.Delta)
				if err := onDelta(ev.Delta); err != nil {
					return res, err
				}
			}
		case "response.refusal.delta", "response.refusal.done":
			sawEvent = true
			res.refused = true
		case "response.completed":
			sawEvent = true
			res.completed = true
			if ev.Response != nil {
				res.usage = ev.Response.usage()
				if ev.Response.truncated() {
					res.truncated = true
				}
			}
		case "response.incomplete":
			sawEvent = true
			// An incomplete response is still a terminal event; classify it by
			// reason rather than blanket-truncating — content_filter is a refusal,
			// not an output-cap truncation.
			res.completed = true
			if ev.Response != nil {
				res.usage = ev.Response.usage()
				if ev.Response.incompleteReason() == "content_filter" {
					res.refused = true
				} else {
					res.truncated = true
				}
			} else {
				res.truncated = true
			}
		case "response.failed":
			msg := "streaming failed"
			if ev.Response != nil && ev.Response.Error != nil {
				msg = sanitizeErrorText(ev.Response.Error.Message)
			}
			return res, providerError("API error: %s", msg)
		default:
			if ev.Type == "" {
				return res, chatGPTContractError(errors.New("stream event has no type"))
			}
		}
	}
	if err := sc.Err(); err != nil {
		if errors.Is(err, context.Canceled) {
			return res, context.Canceled
		}
		return res, fmt.Errorf("stream read failed: %w", err)
	}
	if !sawEvent {
		return res, chatGPTContractError(errors.New("stream produced no recognized events"))
	}
	if !res.completed && !res.truncated {
		return res, protocolError("ChatGPT stream ended before response.completed")
	}
	res.text = full.String()
	return res, nil
}

// completeChatGPT runs a one-shot completion over the streaming backend: it
// consumes the whole SSE stream and returns the accumulated text, since the codex
// backend has no non-streaming mode.
func (c *Client) completeChatGPT(ctx context.Context, systemPrompt, userPrompt string) (string, Usage, error) {
	resp, err := c.openResponsesStreamRetrying(ctx, systemPrompt, userPrompt)
	if err != nil {
		return "", Usage{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	res, err := c.readResponsesStream(resp, func(string) error { return nil })
	if err != nil {
		return "", res.usage, err
	}
	if res.refused {
		return "", res.usage, ErrRefused
	}
	if strings.TrimSpace(res.text) == "" {
		return "", res.usage, ErrEmptyResponse
	}
	if res.truncated {
		return "", res.usage, ErrTruncated
	}
	return res.text, res.usage, nil
}

func (c *Client) streamChatGPT(ctx context.Context, systemPrompt, userPrompt string, out chan<- StreamEvent) (Usage, error) {
	resp, err := c.openResponsesStream(ctx, systemPrompt, userPrompt)
	if err != nil {
		return Usage{}, err
	}
	defer func() { _ = resp.Body.Close() }()

	res, err := c.readResponsesStream(resp, func(delta string) error {
		if !sendStream(ctx, out, StreamEvent{Delta: delta}) {
			return context.Canceled
		}
		return nil
	})
	if err != nil {
		return res.usage, err
	}
	return res.usage, finishStream(res.text, res.refused, res.truncated)
}
