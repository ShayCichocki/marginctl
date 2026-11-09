package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ShayCichocki/marginctl/internal/auth"
	"github.com/ShayCichocki/marginctl/internal/config"
)

// maxErrorBodyLen bounds how much of a provider error body reaches the results
// pane, so a giant HTML gateway page can't flood it.
const maxErrorBodyLen = 240

const (
	maxSuccessBodyBytes = 16 << 20
	maxErrorBodyBytes   = 64 << 10
)

// secretPattern matches API-key/bearer-token shapes that must never be echoed
// back to the UI even if a provider reflects them in an error body.
var secretPattern = regexp.MustCompile(`(?i)(sk-[A-Za-z0-9_-]{6,}|Bearer\s+[A-Za-z0-9._-]{6,}|vck_[A-Za-z0-9_-]{6,}|AI[A-Za-z0-9_-]{20,}|fake-(?:access|refresh)-token[-_A-Za-z0-9]{6,})`)

// sanitizeErrorText collapses whitespace to single spaces, redacts secrets, and
// rune-safely caps the length — used on any provider error text before display.
func sanitizeErrorText(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	s = secretPattern.ReplaceAllString(s, "[redacted]")
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > maxErrorBodyLen {
		s = string(r[:maxErrorBodyLen]) + "…"
	}
	return s
}

// scrubErrorBody reduces a non-200 response body to a short, safe, single-line
// summary: it prefers the provider's structured error message, else a truncated
// snippet of the raw body — always secret-redacted and length-capped.
func scrubErrorBody(body []byte) string {
	var probe struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &probe) == nil && strings.TrimSpace(probe.Error.Message) != "" {
		return sanitizeErrorText(probe.Error.Message)
	}
	return sanitizeErrorText(string(body))
}

const (
	anthropicEndpoint    = "https://api.anthropic.com/v1/messages"
	openAIEndpoint       = "https://api.openai.com/v1/chat/completions"
	vercelEndpoint       = "https://ai-gateway.vercel.sh/v1/chat/completions"
	chatGPTCodexEndpoint = "https://chatgpt.com/backend-api/codex/responses"
	// Default models. Keep these current — a fresh user with no configured
	// model falls back to these, so a stale ID means the tool 404s out of the
	// box. Balanced/mid-tier per provider (the prose tasks don't need the
	// flagship). See CLAUDE.md "LLM configuration".
	anthropicModel = "claude-sonnet-5"
	openAIModel    = "gpt-5.6-sol"
	vercelModel    = "openai/gpt-5.6-sol"
	apiVersion     = "2023-06-01"
	// maxResponseTokens caps the completion length. Kept generous so prose
	// tasks (tighten, voice guide, continuity) rarely hit the ceiling; when
	// they do, the truncation guards below turn it into a visible error rather
	// than a silently half-finished sentence.
	maxResponseTokens = 8192
	// defaultTimeout bounds a single request when the config sets none.
	defaultTimeout = 60 * time.Second
	// maxRetries is the number of *additional* attempts after the first on a
	// retryable failure (429/5xx/529 or a connection error). All attempts share
	// the single per-call deadline from Complete, so retries can't outlive the
	// configured timeout.
	maxRetries = 2
)

// retryBaseDelay is the base backoff between retries; exponential per attempt
// (base, 2×base, …). A package var so tests can shrink it to zero.
var retryBaseDelay = 500 * time.Millisecond

// ErrEmptyResponse is returned when the model produces no usable text (e.g. a
// refusal rendered as empty content). Callers must never treat this as prose.
var ErrEmptyResponse = errors.New("model returned no text")

// ErrTruncated is returned when the model stopped because it hit the token
// limit, leaving a partial (often mid-sentence) completion.
var ErrTruncated = errors.New("model response was truncated (hit max_tokens); shorten the input or raise max_tokens")

// ErrRefused is returned when the provider declined to answer — an explicit
// refusal (OpenAI `message.refusal`, Anthropic `stop_reason:"refusal"`) or a
// content-filter stop. Callers must never treat this as prose.
var ErrRefused = errors.New("model refused to respond")

// Client sends completion requests to the configured LLM provider.
type Client struct {
	mu            sync.RWMutex
	provider      string
	apiKey        string
	model         string
	endpoint      string
	timeout       time.Duration
	client        *http.Client
	credentials   credentialSource
	authStorePath func() (string, error)
	chatGPTNow    func() time.Time
	configErr     error
}

type anthropicRequest struct {
	Model     string    `json:"model"`
	MaxTokens int       `json:"max_tokens"`
	System    string    `json:"system,omitempty"`
	Messages  []Message `json:"messages"`
	Stream    bool      `json:"stream,omitempty"`
}

// Message represents a conversation message. Refusal is response-only (OpenAI
// sets it when the model declines); omitempty keeps it out of outbound requests.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	Refusal string `json:"refusal,omitempty"`
}

type anthropicResponse struct {
	ID         string         `json:"id"`
	Type       string         `json:"type"`
	Role       string         `json:"role"`
	Content    []ContentBlock `json:"content"`
	StopReason string         `json:"stop_reason"`
	Usage      Usage          `json:"usage"`
	Error      *APIError      `json:"error,omitempty"`
}

type openAIRequest struct {
	Model string `json:"model"`
	// Exactly one of these carries the output cap, chosen by model via
	// setMaxTokens: legacy chat models take max_tokens; GPT-5+/o-series reject it
	// and require max_completion_tokens. omitempty keeps the unused one off the wire.
	MaxTokens           int       `json:"max_tokens,omitempty"`
	MaxCompletionTokens int       `json:"max_completion_tokens,omitempty"`
	Messages            []Message `json:"messages"`
	Stream              bool      `json:"stream,omitempty"`
	// StreamOptions asks OpenAI-compatible providers to include a usage block in
	// the final chunk when streaming (they omit it otherwise).
	StreamOptions *streamOptions `json:"stream_options,omitempty"`
}

// setMaxTokens writes the output cap into the field the model accepts. The Chat
// Completions endpoint returns 400 "Unsupported parameter: 'max_tokens'" for
// GPT-5+/o-series models, which require max_completion_tokens instead, while
// gpt-4o/4.1/3.5-era models still take the legacy max_tokens.
func (r *openAIRequest) setMaxTokens(model string, n int) {
	if modelRequiresMaxCompletionTokens(model) {
		r.MaxCompletionTokens = n
	} else {
		r.MaxTokens = n
	}
}

// modelRequiresMaxCompletionTokens reports whether an OpenAI-compatible model id
// needs max_completion_tokens rather than the legacy max_tokens. It recognizes
// the o-series reasoning models and the gpt-N families with major version >= 5
// (gpt-5*, gpt-6*, …), tolerating a Vercel "openai/" namespace prefix and alias
// suffixes like "-chat-latest" or "-sol". Unknown ids fall back to legacy
// max_tokens, matching older self-hosted/compatible endpoints.
func modelRequiresMaxCompletionTokens(model string) bool {
	m := strings.ToLower(strings.TrimSpace(model))
	if i := strings.LastIndex(m, "/"); i >= 0 {
		m = m[i+1:] // strip a provider namespace (e.g. Vercel's "openai/")
	}
	if strings.HasPrefix(m, "o1") || strings.HasPrefix(m, "o3") || strings.HasPrefix(m, "o4") {
		return true
	}
	rest, ok := strings.CutPrefix(m, "gpt-")
	if !ok {
		return false
	}
	major := 0
	for _, ch := range rest {
		if ch < '0' || ch > '9' {
			break
		}
		major = major*10 + int(ch-'0')
	}
	return major >= 5
}

type streamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type openAIResponse struct {
	Choices []openAIChoice `json:"choices"`
	Usage   *openAIUsage   `json:"usage,omitempty"`
	Error   *APIError      `json:"error,omitempty"`
}

// openAIUsage is the OpenAI-compatible token accounting; normalized to Usage.
type openAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

type openAIChoice struct {
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

// ContentBlock represents a content block in the response
type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// Usage represents token usage
type Usage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

// APIError represents an API error
type APIError struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}

type apiStatusError struct {
	statusCode int
	body       string
}

func (e apiStatusError) Error() string {
	return fmt.Sprintf("API returned status %d: %s", e.statusCode, e.body)
}

// NewClient creates a new LLM client from config
func NewClient(cfg config.LLMConfig) *Client {
	c := &Client{
		// No http.Client.Timeout: the per-call context deadline (applied in
		// Complete) is authoritative and, unlike the client timeout, is also
		// user-cancellable via the caller's context.
		client: &http.Client{CheckRedirect: secureRedirect},
	}
	c.configure(cfg)
	return c
}

// configure (re)derives the client's provider/model/endpoint/timeout from cfg,
// substituting provider defaults for anything unset.
func (c *Client) configure(cfg config.LLMConfig) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.configureLocked(cfg)
}

func (c *Client) configureLocked(cfg config.LLMConfig) {
	if c.authStorePath == nil {
		c.authStorePath = auth.AuthStorePath
	}
	if c.chatGPTNow == nil {
		c.chatGPTNow = time.Now
	}
	provider := strings.ToLower(strings.TrimSpace(cfg.Provider))
	if provider == "" {
		provider = config.ProviderAnthropic
	}

	model := cfg.Model
	if model == "" {
		model = defaultModel(provider)
	}

	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = defaultEndpoint(provider)
	}

	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = defaultTimeout
	}

	c.provider = provider
	c.apiKey = cfg.APIKey
	c.model = model
	c.endpoint = endpoint
	c.configErr = validateEndpoint(endpoint)
	c.timeout = timeout
	c.credentials = newCredentialSource(provider, cfg.APIKey, func() (string, error) {
		return c.authStorePath()
	}, func() time.Time {
		return c.chatGPTNow()
	}, c.newOAuthRefreshClient)
}

// Reconfigure re-points the client at a (possibly different) provider and key.
// The command registry captures the *Client at registration, so the in-app
// setup flow mutates the live client in place rather than swapping it out.
func (c *Client) Reconfigure(cfg config.LLMConfig) {
	c.configure(cfg)
}

// IsConfigured returns whether the client has an API key
func (c *Client) IsConfigured() bool {
	return c.IsConfiguredContext(context.Background())
}

func (c *Client) IsConfiguredContext(ctx context.Context) bool {
	return c.configurationError(ctx) == nil
}

func (c *Client) configurationError(ctx context.Context) error {
	s := c.snapshot()
	if s.configErr != nil {
		return s.configErr
	}
	_, err := s.requestCredentials(ctx)
	return err
}

// UnconfiguredMessage returns a user-facing explanation when the client is not
// usable, or the empty string when it is configured. Exported for llmcmd.
func (c *Client) UnconfiguredMessage(ctx context.Context) string {
	if err := c.configurationError(ctx); err != nil {
		return err.Error()
	}
	return ""
}

func (c *Client) requestCredentials(ctx context.Context) (requestCredentials, error) {
	source := c.credentials
	if source == nil {
		return requestCredentials{}, errAPIKeyNotConfigured
	}
	return source.Credentials(ctx)
}

func (c *Client) refreshCredentials(ctx context.Context) (requestCredentials, error) {
	if c.provider != config.ProviderChatGPT {
		return c.requestCredentials(ctx)
	}
	if c.credentials == nil {
		return requestCredentials{}, errAPIKeyNotConfigured
	}
	return c.credentials.Refresh(ctx)
}

func (c *Client) newOAuthRefreshClient(config auth.OAuthConfig) (oauthRefresher, error) {
	client, err := auth.NewOAuthClient(config)
	if err != nil {
		return nil, err
	}
	return client, nil
}

// Model returns the model ID the client sends requests with.
func (c *Client) Model() string {
	return c.snapshot().model
}

// SetModel changes the model ID for subsequent requests (the in-app model
// picker). Blank input is ignored.
func (c *Client) SetModel(model string) {
	if m := strings.TrimSpace(model); m != "" {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.model = m
	}
}

// Provider returns the client's provider ("anthropic"/"openai"/"vercel").
func (c *Client) Provider() string {
	return c.snapshot().provider
}

func (c *Client) snapshot() *Client {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return &Client{provider: c.provider, apiKey: c.apiKey, model: c.model, endpoint: c.endpoint,
		timeout: c.timeout, client: c.client, credentials: c.credentials, authStorePath: c.authStorePath,
		chatGPTNow: c.chatGPTNow, configErr: c.configErr}
}

// Complete sends a completion request to the API and returns the text plus token
// usage. It applies the client's configured timeout as a deadline on top of ctx,
// so an unresponsive provider fails fast; ctx itself may also be cancelled by the
// caller to abort early.
func (c *Client) Complete(ctx context.Context, systemPrompt, userPrompt string) (string, Usage, error) {
	c = c.snapshot()
	if c.configErr != nil {
		return "", Usage{}, c.configErr
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	switch c.provider {
	case config.ProviderAnthropic:
		return c.completeAnthropic(ctx, systemPrompt, userPrompt)
	case config.ProviderChatGPT:
		// ChatGPT's codex backend speaks the Responses API, not chat-completions.
		return c.completeChatGPT(ctx, systemPrompt, userPrompt)
	case config.ProviderOpenAI, config.ProviderVercel:
		return c.completeOpenAICompatible(ctx, systemPrompt, userPrompt)
	default:
		return "", Usage{}, fmt.Errorf("unsupported LLM provider %q", c.provider)
	}
}

func (c *Client) completeAnthropic(ctx context.Context, systemPrompt, userPrompt string) (string, Usage, error) {
	creds, err := c.requestCredentials(ctx)
	if err != nil {
		return "", Usage{}, err
	}
	req := anthropicRequest{
		Model:     c.model,
		MaxTokens: maxResponseTokens,
		System:    systemPrompt,
		Messages: []Message{
			{Role: "user", Content: userPrompt},
		},
	}

	body, err := json.Marshal(req)
	if err != nil {
		return "", Usage{}, fmt.Errorf("failed to marshal request: %w", err)
	}

	respBody, err := c.send(ctx, body, map[string]string{
		"Content-Type":      "application/json",
		"x-api-key":         creds.APIKey,
		"anthropic-version": apiVersion,
	})
	if err != nil {
		return "", Usage{}, err
	}

	var apiResp anthropicResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return "", Usage{}, c.openAIContractError(fmt.Errorf("failed to parse response: %w", err))
	}

	if apiResp.Error != nil {
		return "", Usage{}, fmt.Errorf("API error: %s", sanitizeErrorText(apiResp.Error.Message))
	}

	// An explicit refusal (Claude 4+) is signalled via stop_reason, independent
	// of whatever partial text may be present; treat it as a hard failure.
	if apiResp.StopReason == "refusal" {
		return "", Usage{}, ErrRefused
	}

	var result string
	for _, block := range apiResp.Content {
		if block.Type == "text" {
			result += block.Text
		}
	}

	if strings.TrimSpace(result) == "" {
		return "", Usage{}, ErrEmptyResponse
	}
	if apiResp.StopReason == "max_tokens" {
		return "", Usage{}, ErrTruncated
	}

	return result, apiResp.Usage, nil
}

func (c *Client) completeOpenAICompatible(ctx context.Context, systemPrompt, userPrompt string) (string, Usage, error) {
	creds, err := c.requestCredentials(ctx)
	if err != nil {
		return "", Usage{}, err
	}
	req := openAIRequest{
		Model: c.model,
		Messages: []Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
	}
	req.setMaxTokens(c.model, maxResponseTokens)
	body, err := json.Marshal(req)
	if err != nil {
		return "", Usage{}, fmt.Errorf("failed to marshal request: %w", err)
	}

	respBody, err := c.sendOpenAICompatible(ctx, body, creds)
	if err != nil {
		return "", Usage{}, err
	}

	var apiResp openAIResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return "", Usage{}, c.openAIContractError(fmt.Errorf("failed to parse response: %w", err))
	}

	if apiResp.Error != nil {
		return "", Usage{}, fmt.Errorf("API error: %s", sanitizeErrorText(apiResp.Error.Message))
	}
	if len(apiResp.Choices) == 0 {
		return "", Usage{}, c.openAIContractError(errors.New("API returned no choices"))
	}

	choice := apiResp.Choices[0]
	// Refusal / content-filter first: these carry empty or partial content but
	// mean "declined", not "here is prose".
	if strings.TrimSpace(choice.Message.Refusal) != "" || choice.FinishReason == "content_filter" {
		return "", Usage{}, ErrRefused
	}
	if strings.TrimSpace(choice.Message.Content) == "" {
		return "", Usage{}, ErrEmptyResponse
	}
	if choice.FinishReason == "length" {
		return "", Usage{}, ErrTruncated
	}

	var usage Usage
	if apiResp.Usage != nil {
		usage = Usage{InputTokens: apiResp.Usage.PromptTokens, OutputTokens: apiResp.Usage.CompletionTokens}
	}
	return choice.Message.Content, usage, nil
}

func (c *Client) sendOpenAICompatible(ctx context.Context, body []byte, creds requestCredentials) ([]byte, error) {
	respBody, err := c.sendWithCredentials(ctx, body, creds)
	if err == nil || c.provider != config.ProviderChatGPT {
		return respBody, err
	}
	var statusErr apiStatusError
	if !errors.As(err, &statusErr) || statusErr.statusCode != http.StatusUnauthorized {
		return nil, err
	}
	refreshed, refreshErr := c.refreshCredentials(ctx)
	if refreshErr != nil {
		return nil, refreshErr
	}
	return c.sendWithCredentials(ctx, body, refreshed)
}

func (c *Client) sendWithCredentials(ctx context.Context, body []byte, creds requestCredentials) ([]byte, error) {
	return c.send(ctx, body, c.openAICompatibleHeaders(creds))
}

func (c *Client) openAICompatibleHeaders(creds requestCredentials) map[string]string {
	headers := map[string]string{
		"Content-Type":  "application/json",
		"Authorization": "Bearer " + creds.BearerToken,
	}
	if c.provider == config.ProviderChatGPT && strings.TrimSpace(creds.AccountID) != "" {
		headers["ChatGPT-Account-Id"] = strings.TrimSpace(creds.AccountID)
	}
	return headers
}

func (c *Client) openAIContractError(err error) error {
	if c.provider == config.ProviderChatGPT {
		return chatGPTContractError(err)
	}
	return err
}

// send POSTs body to the client's endpoint with the given headers and returns
// the 200 response body. Transient failures (429/500/502/503/529 or a
// connection error) are retried up to maxRetries with exponential backoff,
// honoring a server-sent Retry-After. Context cancellation/timeout is never
// retried and is mapped to a stable error the caller (app.startLLM) recognizes.
func (c *Client) send(ctx context.Context, body []byte, headers map[string]string) ([]byte, error) {
	var lastErr error
	for attempt := 0; ; attempt++ {
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
				return nil, fmt.Errorf("LLM request timed out after %s", c.timeout)
			}
			// Connection-level failure: retryable.
			lastErr = fmt.Errorf("request failed: %w", err)
			if attempt < maxRetries {
				if c.backoff(ctx, attempt, 0) {
					continue
				}
				// Backoff aborted because ctx ended: report the cancel/timeout, not
				// the connection error that triggered the (now-abandoned) retry.
				return nil, ctxError(ctx, c.timeout)
			}
			return nil, lastErr
		}

		limit := int64(maxErrorBodyBytes)
		if resp.StatusCode == http.StatusOK {
			limit = maxSuccessBodyBytes
		}
		respBody, readErr := readLimited(resp.Body, limit)
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, fmt.Errorf("failed to read response: %w", readErr)
		}

		if resp.StatusCode == http.StatusOK {
			return respBody, nil
		}

		lastErr = apiStatusError{statusCode: resp.StatusCode, body: scrubErrorBody(respBody)}
		if isRetryableStatus(resp.StatusCode) && attempt < maxRetries {
			if c.backoff(ctx, attempt, retryAfter(resp.Header)) {
				continue
			}
			// Backoff aborted because ctx ended (cancel, or a Retry-After longer
			// than the remaining deadline): report that, not the last HTTP status.
			return nil, ctxError(ctx, c.timeout)
		}
		return nil, lastErr
	}
}

// ctxError maps a finished request context to the stable error app.startLLM
// recognizes, so a cancel/timeout that ends a retry backoff is reported as a
// cancel/timeout rather than as the last provider status (e.g. a stale 429).
func ctxError(ctx context.Context, timeout time.Duration) error {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("LLM request timed out after %s", timeout)
	}
	return context.Canceled
}

func readLimited(r io.Reader, limit int64) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("provider response exceeds %d byte limit", limit)
	}
	return b, nil
}

func validateEndpoint(endpoint string) error {
	u, err := url.Parse(endpoint)
	if err != nil || u.Host == "" {
		return fmt.Errorf("invalid LLM endpoint")
	}
	if u.Scheme == "https" {
		return nil
	}
	host := u.Hostname()
	if u.Scheme == "http" && (host == "localhost" || net.ParseIP(host) != nil && net.ParseIP(host).IsLoopback()) {
		return nil
	}
	return fmt.Errorf("LLM endpoint must use HTTPS (HTTP is allowed only for loopback development endpoints)")
}

func secureRedirect(req *http.Request, via []*http.Request) error {
	if len(via) == 0 {
		return nil
	}
	previous := via[len(via)-1]
	// Scheme must match too: an https -> http redirect on the same host has the
	// same hostname and (implicit) port, yet would send credentials in clear.
	if !strings.EqualFold(req.URL.Scheme, previous.URL.Scheme) ||
		!strings.EqualFold(req.URL.Hostname(), previous.URL.Hostname()) ||
		effectivePort(req.URL) != effectivePort(previous.URL) {
		return errors.New("credentialed redirect refused: scheme/host/port changed")
	}
	return nil
}

// effectivePort returns the URL's explicit port or the scheme default.
func effectivePort(u *url.URL) string {
	if p := u.Port(); p != "" {
		return p
	}
	switch strings.ToLower(u.Scheme) {
	case "https":
		return "443"
	case "http":
		return "80"
	}
	return ""
}

// backoff waits before the next retry, preferring a server-supplied Retry-After
// over the exponential default. It returns false if ctx is cancelled/expires
// while waiting, so the caller stops retrying and surfaces the last error.
func (c *Client) backoff(ctx context.Context, attempt int, retryAfter time.Duration) bool {
	delay := retryAfter
	if delay <= 0 {
		delay = retryBaseDelay << attempt // base, 2×base, 4×base, …
	}
	if delay <= 0 {
		return true
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

// isRetryableStatus reports whether an HTTP status is a transient provider
// condition worth retrying: rate limits (429), overload (529, Anthropic), and
// the retryable 5xx family.
func isRetryableStatus(code int) bool {
	switch code {
	case http.StatusTooManyRequests, // 429
		http.StatusInternalServerError, // 500
		http.StatusBadGateway,          // 502
		http.StatusServiceUnavailable,  // 503
		529:                            // overloaded (Anthropic)
		return true
	}
	return false
}

// retryAfter parses a Retry-After header expressed as a non-negative number of
// seconds (the form both providers use) or an HTTP-date; malformed values return
// 0 so the caller falls back to exponential backoff.
func retryAfter(h http.Header) time.Duration {
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs < 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	// HTTP-date form. A past date yields 0; backoff bounds the wait by ctx, so
	// an oversized value cannot outlive the per-call timeout.
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

func defaultEndpoint(provider string) string {
	switch provider {
	case config.ProviderOpenAI:
		return openAIEndpoint
	case config.ProviderVercel:
		return vercelEndpoint
	case config.ProviderChatGPT:
		return chatGPTCodexEndpoint
	default:
		return anthropicEndpoint
	}
}

func defaultModel(provider string) string {
	switch provider {
	case config.ProviderOpenAI, config.ProviderChatGPT:
		return openAIModel
	case config.ProviderVercel:
		return vercelModel
	default:
		return anthropicModel
	}
}
