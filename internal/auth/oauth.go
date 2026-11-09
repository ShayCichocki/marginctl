package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	DefaultOAuthClientID = "app_EMoamEEZ73f0CkXaXp7hrann"
	DefaultOAuthIssuer   = "https://auth.openai.com"
	DefaultOAuthScope    = "openid profile email offline_access api.connectors.read api.connectors.invoke"

	oauthAuthorizePath = "/oauth/authorize"
	oauthTokenPath     = "/oauth/token"
	deviceUserCodePath = "/api/accounts/deviceauth/usercode"
	deviceTokenPath    = "/api/accounts/deviceauth/token"
	maxOAuthBodyBytes  = 1 << 20
)

var oauthSecretPattern = regexp.MustCompile(`(?i)(sk-[A-Za-z0-9_-]{6,}|Bearer\s+[A-Za-z0-9._-]{6,}|fake-(?:access|refresh)-token[-_A-Za-z0-9]{6,}|[A-Za-z0-9_-]{6,}\.[A-Za-z0-9._-]{6,}\.[A-Za-z0-9._-]*)`)

var oauthJSONSecretFieldPattern = regexp.MustCompile(`(?i)"(code_verifier|access_token|refresh_token|id_token|authorization_code|authorization|code)"\s*:\s*"([^"\\]*(?:\\.[^"\\]*)*)"`)

var ErrOAuthInvalidGrant = errors.New("oauth invalid_grant")

type OAuthConfig struct {
	Issuer     string
	ClientID   string
	HTTPClient *http.Client
	Random     io.Reader
}

type OAuthClient struct {
	issuer     *url.URL
	clientID   string
	httpClient *http.Client
	random     io.Reader
}

type PKCE struct {
	Verifier  string
	Challenge string
	Method    string
}

type CodeExchange struct {
	Code        string
	RedirectURI string
	Verifier    string
}

type TokenSet struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	ExpiresIn    int    `json:"expires_in"`
	AccountID    string `json:"account_id,omitempty"`
}

func NewOAuthClient(config OAuthConfig) (*OAuthClient, error) {
	issuer := strings.TrimRight(config.Issuer, "/")
	if issuer == "" {
		issuer = DefaultOAuthIssuer
	}
	parsed, err := url.Parse(issuer)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, fmt.Errorf("parse OAuth issuer %q: %w", issuer, errors.New("issuer must be an absolute URL"))
	}
	ip := net.ParseIP(parsed.Hostname())
	loopbackHTTP := parsed.Scheme == "http" && (parsed.Hostname() == "localhost" || ip != nil && ip.IsLoopback())
	if parsed.Scheme != "https" && !loopbackHTTP {
		return nil, errors.New("OAuth issuer must use HTTPS (HTTP is allowed only for loopback development endpoints)")
	}
	clientID := config.ClientID
	if clientID == "" {
		clientID = DefaultOAuthClientID
	}
	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	} else {
		clone := *httpClient
		httpClient = &clone
	}
	httpClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) == 0 {
			return nil
		}
		prev := via[len(via)-1].URL
		if !strings.EqualFold(req.URL.Scheme, prev.Scheme) ||
			!strings.EqualFold(req.URL.Hostname(), prev.Hostname()) ||
			effectivePort(req.URL) != effectivePort(prev) {
			return errors.New("credentialed redirect refused: scheme/host/port changed")
		}
		return nil
	}
	random := config.Random
	if random == nil {
		random = rand.Reader
	}
	return &OAuthClient{issuer: parsed, clientID: clientID, httpClient: httpClient, random: random}, nil
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

func (c *OAuthClient) GeneratePKCE() (PKCE, error) {
	raw := make([]byte, 32)
	if _, err := io.ReadFull(c.random, raw); err != nil {
		return PKCE{}, fmt.Errorf("generate PKCE verifier: %w", err)
	}
	verifier := base64.RawURLEncoding.EncodeToString(raw)
	sum := sha256.Sum256([]byte(verifier))
	return PKCE{
		Verifier:  verifier,
		Challenge: base64.RawURLEncoding.EncodeToString(sum[:]),
		Method:    "S256",
	}, nil
}

func (c *OAuthClient) AuthorizeURL(redirectURI string, state string, pkce PKCE) (string, error) {
	endpoint := c.endpoint(oauthAuthorizePath)
	query := endpoint.Query()
	query.Set("client_id", c.clientID)
	query.Set("redirect_uri", redirectURI)
	query.Set("response_type", "code")
	query.Set("scope", DefaultOAuthScope)
	query.Set("code_challenge", pkce.Challenge)
	query.Set("code_challenge_method", pkce.Method)
	query.Set("id_token_add_organizations", "true")
	query.Set("codex_cli_simplified_flow", "true")
	query.Set("originator", "marginctl")
	query.Set("state", state)
	endpoint.RawQuery = query.Encode()
	return endpoint.String(), nil
}

func (c *OAuthClient) ValidateCallbackState(expected string, actual string) error {
	if expected == actual {
		return nil
	}
	return fmt.Errorf("OAuth callback state mismatch: expected current login state; restart :connect openai")
}

func (c *OAuthClient) ExchangeCode(ctx context.Context, exchange CodeExchange) (TokenSet, error) {
	return c.postTokenForm(ctx, c.codeExchangeForm(exchange), "token exchange")
}

func (c *OAuthClient) Refresh(ctx context.Context, refreshToken string) (TokenSet, error) {
	form := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {c.clientID},
	}
	return c.postTokenForm(ctx, form, "token refresh")
}

func (c *OAuthClient) postTokenForm(ctx context.Context, form url.Values, action string) (TokenSet, error) {
	body := strings.NewReader(form.Encode())
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpointString(oauthTokenPath), body)
	if err != nil {
		return TokenSet{}, fmt.Errorf("build %s request: %w", action, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return TokenSet{}, fmt.Errorf("%s request: %w", action, err)
	}
	defer func() { _ = resp.Body.Close() }()
	return decodeTokenResponse(resp, action)
}

func (c *OAuthClient) codeExchangeForm(exchange CodeExchange) url.Values {
	return url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {c.clientID},
		"code":          {exchange.Code},
		"redirect_uri":  {exchange.RedirectURI},
		"code_verifier": {exchange.Verifier},
	}
}

func decodeTokenResponse(resp *http.Response, action string) (TokenSet, error) {
	body, err := readOAuthBody(resp.Body)
	if err != nil {
		return TokenSet{}, fmt.Errorf("read %s response: %w", action, err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		message := sanitizeOAuthErrorText(string(body))
		if isOAuthInvalidGrant(body) {
			return TokenSet{}, fmt.Errorf("%s failed: HTTP %d: %w: %s", action, resp.StatusCode, ErrOAuthInvalidGrant, message)
		}
		return TokenSet{}, fmt.Errorf("%s failed: HTTP %d: %s", action, resp.StatusCode, message)
	}
	var tokens TokenSet
	if err := json.Unmarshal(body, &tokens); err != nil {
		return TokenSet{}, fmt.Errorf("decode %s response: %w", action, err)
	}
	if accountID := ExtractAccountIDFromTokens(tokens.IDToken, tokens.AccessToken); accountID != "" {
		tokens.AccountID = accountID
	}
	return tokens, nil
}

func readOAuthBody(r io.Reader) ([]byte, error) {
	b, err := io.ReadAll(io.LimitReader(r, maxOAuthBodyBytes+1))
	if err != nil {
		return nil, err
	}
	if len(b) > maxOAuthBodyBytes {
		return nil, errors.New("OAuth response exceeds size limit")
	}
	return b, nil
}

func isOAuthInvalidGrant(body []byte) bool {
	var parsed struct {
		Error            string `json:"error"`
		ErrorDescription string `json:"error_description"`
	}
	if json.Unmarshal(body, &parsed) == nil {
		return strings.EqualFold(strings.TrimSpace(parsed.Error), "invalid_grant")
	}
	return strings.Contains(strings.ToLower(string(body)), "invalid_grant")
}

func (c *OAuthClient) endpoint(path string) url.URL {
	endpoint := *c.issuer
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + path
	endpoint.RawQuery = ""
	return endpoint
}

func (c *OAuthClient) endpointString(path string) string {
	endpoint := c.endpoint(path)
	return endpoint.String()
}

func sanitizeOAuthErrorText(s string) string {
	s = redactOAuthSecrets(s)
	var parsed struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(s), &parsed) == nil && strings.TrimSpace(parsed.Error.Message) != "" {
		s = redactOAuthSecrets(parsed.Error.Message)
	}
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > 240 {
		s = string(r[:240])
	}
	return s
}

func redactOAuthSecrets(s string) string {
	s = oauthJSONSecretFieldPattern.ReplaceAllString(s, `"$1":"[redacted]"`)
	return oauthSecretPattern.ReplaceAllString(s, "[redacted]")
}
