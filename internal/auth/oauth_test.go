package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestPKCEGenerateCreatesS256Challenge(t *testing.T) {
	// Given
	client := mustOAuthClient(t, "https://issuer.example")

	// When
	pkce, err := client.GeneratePKCE()

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if len(pkce.Verifier) < 43 {
		t.Fatalf("verifier length = %d, want at least 43", len(pkce.Verifier))
	}
	sum := sha256.Sum256([]byte(pkce.Verifier))
	wantChallenge := base64.RawURLEncoding.EncodeToString(sum[:])
	if pkce.Challenge != wantChallenge {
		t.Fatalf("challenge = %q, want S256 %q", pkce.Challenge, wantChallenge)
	}
	if pkce.Method != "S256" {
		t.Fatalf("method = %q, want S256", pkce.Method)
	}
	if strings.ContainsAny(pkce.Verifier, "+/=") || strings.ContainsAny(pkce.Challenge, "+/=") {
		t.Fatalf("pkce values must be raw base64url, got verifier=%q challenge=%q", pkce.Verifier, pkce.Challenge)
	}
}

func TestOAuthAuthorizeURLUsesCodexContract_whenConfigInjected(t *testing.T) {
	// Given
	client := mustOAuthClient(t, "https://issuer.example")
	pkce := PKCE{Verifier: "verifier", Challenge: "challenge", Method: "S256"}

	// When
	gotURL, err := client.AuthorizeURL("http://localhost:1455/auth/callback", "state-123", pkce)

	// Then
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(gotURL)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.String() != gotURL {
		t.Fatalf("authorize URL must be parseable, got %q", gotURL)
	}
	if parsed.Scheme != "https" || parsed.Host != "issuer.example" || parsed.Path != "/oauth/authorize" {
		t.Fatalf("authorize endpoint = %s://%s%s, want https://issuer.example/oauth/authorize", parsed.Scheme, parsed.Host, parsed.Path)
	}
	query := parsed.Query()
	for _, scope := range []string{"openid", "profile", "email", "offline_access", "api.connectors.read", "api.connectors.invoke"} {
		if !strings.Contains(" "+query.Get("scope")+" ", " "+scope+" ") {
			t.Fatalf("scope = %q, missing %q", query.Get("scope"), scope)
		}
	}
	want := map[string]string{
		"client_id":                  DefaultOAuthClientID,
		"redirect_uri":               "http://localhost:1455/auth/callback",
		"response_type":              "code",
		"scope":                      DefaultOAuthScope,
		"code_challenge":             "challenge",
		"code_challenge_method":      "S256",
		"id_token_add_organizations": "true",
		"codex_cli_simplified_flow":  "true",
		"originator":                 "marginctl",
		"state":                      "state-123",
	}
	for key, value := range want {
		if query.Get(key) != value {
			t.Fatalf("query[%s] = %q, want %q in %s", key, query.Get(key), value, gotURL)
		}
	}
}

func TestOAuthExchangeCodePostsFormFields_whenFakeIssuerReturnsTokens(t *testing.T) {
	// Given
	observed := make(chan formObservation, 1)
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/oauth/token" {
			t.Fatalf("path = %q, want /oauth/token", r.URL.Path)
		}
		observed <- readFormObservation(t, r)
		_, _ = w.Write([]byte(`{"access_token":"access.jwt","refresh_token":"refresh-token","id_token":"id.jwt","expires_in":3600}`))
	}))
	defer issuer.Close()
	client := mustOAuthClient(t, issuer.URL)

	// When
	tokens, err := client.ExchangeCode(context.Background(), CodeExchange{
		Code:        "browser-code",
		RedirectURI: "http://localhost:39241/auth/callback",
		Verifier:    "verifier-123",
	})

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if tokens.AccessToken != "access.jwt" || tokens.RefreshToken != "refresh-token" || tokens.IDToken != "id.jwt" {
		t.Fatalf("tokens = %+v, want fake issuer values", tokens)
	}
	got := <-observed
	t.Logf("browser token exchange observed method=%s path=/oauth/token content_type=%s body=%s", got.Method, got.ContentType, got.Form.Encode())
	if got.Method != http.MethodPost || got.ContentType != "application/x-www-form-urlencoded" {
		t.Fatalf("method/content-type = %s/%s, want POST application/x-www-form-urlencoded", got.Method, got.ContentType)
	}
	assertForm(t, got.Form, map[string]string{
		"grant_type":    "authorization_code",
		"client_id":     DefaultOAuthClientID,
		"code":          "browser-code",
		"redirect_uri":  "http://localhost:39241/auth/callback",
		"code_verifier": "verifier-123",
	})
}

func TestOAuthExchangeCodeReturnsActionableError_whenTokenResponseMalformed(t *testing.T) {
	// Given
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":`))
	}))
	defer issuer.Close()
	client := mustOAuthClient(t, issuer.URL)

	// When
	_, err := client.ExchangeCode(context.Background(), CodeExchange{Code: "bad", RedirectURI: "http://localhost:1/auth/callback", Verifier: "verifier"})

	// Then
	if err == nil || !strings.Contains(err.Error(), "decode token exchange response") {
		t.Fatalf("err = %v, want malformed token response error", err)
	}
}

func TestOAuthClientRejectsBadIssuerURL(t *testing.T) {
	// Given
	config := OAuthConfig{Issuer: "://bad-url"}

	// When
	_, err := NewOAuthClient(config)

	// Then
	if err == nil || !strings.Contains(err.Error(), "parse OAuth issuer") {
		t.Fatalf("err = %v, want bad issuer parse error", err)
	}
}

func TestOAuthExchangeCodeReturnsRedactedError_whenStateMismatchOrTokenNon200(t *testing.T) {
	// Given
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"bad code secret sk-testsecret123456 and Bearer leakedtoken123"}}`))
	}))
	defer issuer.Close()
	client := mustOAuthClient(t, issuer.URL)

	// When
	mismatch := client.ValidateCallbackState("want", "got")
	_, tokenErr := client.ExchangeCode(context.Background(), CodeExchange{Code: "bad", RedirectURI: "http://localhost:1/auth/callback", Verifier: "verifier"})

	// Then
	if mismatch == nil || !strings.Contains(mismatch.Error(), "OAuth callback state mismatch") {
		t.Fatalf("state mismatch err = %v, want actionable mismatch", mismatch)
	}
	if tokenErr == nil || !strings.Contains(tokenErr.Error(), "token exchange failed") {
		t.Fatalf("token err = %v, want token exchange failure", tokenErr)
	}
	if strings.Contains(tokenErr.Error(), "sk-testsecret") || strings.Contains(tokenErr.Error(), "leakedtoken") {
		t.Fatalf("token error leaked secret: %v", tokenErr)
	}
}

func TestOAuthExchangeCodeRedactsEchoedSecretFields_whenTokenNon200(t *testing.T) {
	// Given
	secrets := strings.Split("auth-code-secret-1234567890|verifier-secret-1234567890|access-token-secret-1234567890|refresh-token-secret-1234567890|header.payloadsignature.secretpart|authorization-code-secret-1234567890|Bearer bearer-secret-1234567890|sk-testsecret1234567890", "|")
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"exchange failed","code":"auth-code-secret-1234567890","code_verifier":"verifier-secret-1234567890","access_token":"access-token-secret-1234567890","refresh_token":"refresh-token-secret-1234567890","id_token":"header.payloadsignature.secretpart","authorization_code":"authorization-code-secret-1234567890","authorization":"Bearer bearer-secret-1234567890","message":"sk-testsecret1234567890"}`))
	}))
	defer issuer.Close()
	client := mustOAuthClient(t, issuer.URL)

	// When
	_, err := client.ExchangeCode(context.Background(), CodeExchange{Code: "bad", RedirectURI: "http://localhost:1/auth/callback", Verifier: "verifier"})

	// Then
	if err == nil {
		t.Fatal("err = nil, want redacted token exchange failure")
	}
	message := err.Error()
	if !strings.Contains(message, "[redacted]") {
		t.Fatalf("err = %q, want [redacted] marker", message)
	}
	for _, secret := range secrets {
		if strings.Contains(message, secret) {
			t.Fatalf("err leaked %q in %q", secret, message)
		}
	}
}

func TestRefreshPostsRefreshTokenForm(t *testing.T) {
	// Given
	observed := make(chan formObservation, 1)
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observed <- readFormObservation(t, r)
		_, _ = w.Write([]byte(`{"access_token":"new-access","refresh_token":"new-refresh","expires_in":1800}`))
	}))
	defer issuer.Close()
	client := mustOAuthClient(t, issuer.URL)

	// When
	tokens, err := client.Refresh(context.Background(), "old-refresh")

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if tokens.AccessToken != "new-access" || tokens.RefreshToken != "new-refresh" {
		t.Fatalf("tokens = %+v, want refreshed values", tokens)
	}
	got := <-observed
	t.Logf("refresh observed method=%s path=/oauth/token content_type=%s body=%s", got.Method, got.ContentType, got.Form.Encode())
	if got.Method != http.MethodPost || got.ContentType != "application/x-www-form-urlencoded" {
		t.Fatalf("method/content-type = %s/%s, want POST application/x-www-form-urlencoded", got.Method, got.ContentType)
	}
	assertForm(t, got.Form, map[string]string{
		"grant_type":    "refresh_token",
		"refresh_token": "old-refresh",
		"client_id":     DefaultOAuthClientID,
	})
}

func TestOAuthExtractAccountIDFromTokensReturnsEmptyForMalformedJWT(t *testing.T) {
	// Given
	idToken := jwtWithClaims(t, map[string]string{"https://api.openai.com/auth/account_id": "acct-id"})
	accessToken := jwtWithClaims(t, map[string]string{"account_id": "acct-access"})

	if got := ExtractAccountIDFromTokens(idToken, accessToken); got != "acct-id" {
		t.Fatalf("account ID = %q, want ID-token account", got)
	}
	if got := ExtractAccountIDFromTokens("malformed", accessToken); got != "acct-access" {
		t.Fatalf("fallback account ID = %q, want access-token account", got)
	}
	if got := ExtractAccountIDFromTokens("malformed", "also.malformed"); got != "" {
		t.Fatalf("malformed account ID = %q, want empty", got)
	}
}

type formObservation struct {
	Method      string
	ContentType string
	Form        url.Values
}

func TestOAuthRedirectGuardRefusesSchemeHostPortChange(t *testing.T) {
	client := mustOAuthClient(t, "https://issuer.example")
	mk := func(u string) *http.Request {
		r, err := http.NewRequest(http.MethodPost, u, nil)
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	cases := []struct {
		from, to string
		refused  bool
	}{
		{"https://issuer.example/a", "http://issuer.example/b", true},
		{"https://issuer.example/a", "https://other.example/b", true},
		{"https://issuer.example/a", "https://issuer.example:8443/b", true},
		{"https://issuer.example/a", "https://issuer.example:443/b", false},
		{"https://issuer.example/a", "https://issuer.example/b", false},
	}
	for _, tc := range cases {
		err := client.httpClient.CheckRedirect(mk(tc.to), []*http.Request{mk(tc.from)})
		if (err != nil) != tc.refused {
			t.Errorf("%s -> %s: err=%v, refused want %v", tc.from, tc.to, err, tc.refused)
		}
		if err != nil && !strings.Contains(err.Error(), "scheme/host/port") {
			t.Errorf("message does not say what mismatched: %v", err)
		}
	}
}

func mustOAuthClient(t *testing.T, issuer string) *OAuthClient {
	t.Helper()
	client, err := NewOAuthClient(OAuthConfig{Issuer: issuer, HTTPClient: http.DefaultClient})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func readFormObservation(t *testing.T, r *http.Request) formObservation {
	t.Helper()
	if err := r.ParseForm(); err != nil {
		t.Fatal(err)
	}
	return formObservation{Method: r.Method, ContentType: r.Header.Get("Content-Type"), Form: r.PostForm}
}

func assertForm(t *testing.T, got url.Values, want map[string]string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("form = %v, want exactly %v", got, want)
	}
	for key, value := range want {
		if got.Get(key) != value {
			t.Fatalf("form[%s] = %q, want %q in %v", key, got.Get(key), value, got)
		}
	}
}

func jwtWithClaims(t *testing.T, claims map[string]string) string {
	t.Helper()
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payloadBytes, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	payload := base64.RawURLEncoding.EncodeToString(payloadBytes)
	return fmt.Sprintf("%s.%s.", header, payload)
}
