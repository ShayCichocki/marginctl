package llm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ShayCichocki/marginctl/internal/auth"
	"github.com/ShayCichocki/marginctl/internal/config"
)

func TestChatGPTCredentialsRefresh_whenTokenExpiresInsideSafetyWindow(t *testing.T) {
	// Given
	storePath := filepath.Join(t.TempDir(), "auth.json")
	now := time.Date(2026, 7, 9, 15, 0, 0, 0, time.UTC)
	var refreshCalls int32
	issuer := newRefreshIssuer(t, &refreshCalls, http.StatusOK, `{"access_token":"fake-access-token-refreshed","refresh_token":"fake-refresh-token-refreshed","expires_in":1800}`)
	store := chatGPTRefreshTokenStore(issuer.URL, "fake-access-token-old", "fake-refresh-token-old", now.Add(time.Minute))
	if err := auth.SaveTokenStore(storePath, store); err != nil {
		t.Fatalf("SaveTokenStore: %v", err)
	}
	client := newChatGPTRefreshTestClient(storePath, now)

	// When
	creds, err := client.requestCredentials(context.Background())

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if creds.BearerToken != "fake-access-token-refreshed" {
		t.Fatalf("BearerToken = %q, want refreshed token", creds.BearerToken)
	}
	if n := atomic.LoadInt32(&refreshCalls); n != 1 {
		t.Fatalf("refresh calls = %d, want 1", n)
	}
	assertStoredRefresh(t, storePath, "fake-access-token-refreshed", "fake-refresh-token-refreshed", now)
}

func TestChatGPTCredentialsPreserveRefreshToken_whenRefreshResponseOmitsIt(t *testing.T) {
	// Given
	storePath := filepath.Join(t.TempDir(), "auth.json")
	now := time.Date(2026, 7, 9, 15, 0, 0, 0, time.UTC)
	var refreshCalls int32
	issuer := newRefreshIssuer(t, &refreshCalls, http.StatusOK, `{"access_token":"fake-access-token-refreshed","expires_in":1800}`)
	store := chatGPTRefreshTokenStore(issuer.URL, "fake-access-token-old", "fake-refresh-token-preserved", now.Add(time.Minute))
	if err := auth.SaveTokenStore(storePath, store); err != nil {
		t.Fatalf("SaveTokenStore: %v", err)
	}
	client := newChatGPTRefreshTestClient(storePath, now)

	// When
	_, err := client.requestCredentials(context.Background())

	// Then
	if err != nil {
		t.Fatal(err)
	}
	got, err := auth.LoadTokenStore(storePath)
	if err != nil {
		t.Fatal(err)
	}
	if got.RefreshToken != "fake-refresh-token-preserved" {
		t.Fatalf("stored refresh token = %q, want preserved old token", got.RefreshToken)
	}
	if n := atomic.LoadInt32(&refreshCalls); n != 1 {
		t.Fatalf("refresh calls = %d, want 1", n)
	}
}

func TestChatGPTCredentialsInvalidGrantDeletesStoreAndRequiresReconnect(t *testing.T) {
	// Given
	storePath := filepath.Join(t.TempDir(), "auth.json")
	now := time.Date(2026, 7, 9, 15, 0, 0, 0, time.UTC)
	var refreshCalls int32
	issuer := newRefreshIssuer(t, &refreshCalls, http.StatusBadRequest, `{"error":"invalid_grant","error_description":"revoked fake-refresh-token-old"}`)
	store := chatGPTRefreshTokenStore(issuer.URL, "fake-access-token-old", "fake-refresh-token-old", now.Add(time.Minute))
	if err := auth.SaveTokenStore(storePath, store); err != nil {
		t.Fatalf("SaveTokenStore: %v", err)
	}
	client := newChatGPTRefreshTestClient(storePath, now)

	// When
	_, err := client.requestCredentials(context.Background())

	// Then
	if err == nil || !strings.Contains(err.Error(), "run :connect openai") {
		t.Fatalf("err = %v, want reconnect guidance", err)
	}
	if strings.Contains(err.Error(), "fake-refresh-token-old") {
		t.Fatalf("err leaked refresh token: %q", err.Error())
	}
	if _, statErr := os.Stat(storePath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("store stat err = %v, want deleted auth store", statErr)
	}
	if n := atomic.LoadInt32(&refreshCalls); n != 1 {
		t.Fatalf("refresh calls = %d, want 1", n)
	}
}

func TestChatGPTCredentialsTransientRefreshFailurePreservesStore(t *testing.T) {
	// Given
	storePath := filepath.Join(t.TempDir(), "auth.json")
	now := time.Date(2026, 7, 9, 15, 0, 0, 0, time.UTC)
	var refreshCalls int32
	issuer := newRefreshIssuer(t, &refreshCalls, http.StatusInternalServerError, `{"error":"server_error"}`)
	store := chatGPTRefreshTokenStore(issuer.URL, "fake-access-token-old", "fake-refresh-token-old", now.Add(time.Minute))
	if err := auth.SaveTokenStore(storePath, store); err != nil {
		t.Fatalf("SaveTokenStore: %v", err)
	}
	client := newChatGPTRefreshTestClient(storePath, now)

	// When
	_, err := client.requestCredentials(context.Background())

	// Then
	if err == nil || !strings.Contains(err.Error(), "refresh") {
		t.Fatalf("err = %v, want transient refresh error", err)
	}
	got, loadErr := auth.LoadTokenStore(storePath)
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if got.RefreshToken != "fake-refresh-token-old" || got.AccessToken != "fake-access-token-old" {
		t.Fatalf("stored token = %+v, want original tokens preserved", got)
	}
	if n := atomic.LoadInt32(&refreshCalls); n != 1 {
		t.Fatalf("refresh calls = %d, want 1", n)
	}
}

func TestChatGPTCredentialsConcurrentRefreshUsesSingleflight(t *testing.T) {
	// Given
	storePath := filepath.Join(t.TempDir(), "auth.json")
	now := time.Date(2026, 7, 9, 15, 0, 0, 0, time.UTC)
	var refreshCalls int32
	release := make(chan struct{})
	started := make(chan struct{})
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&refreshCalls, 1) == 1 {
			close(started)
		}
		<-release
		_, _ = w.Write([]byte(`{"access_token":"fake-access-token-refreshed","refresh_token":"fake-refresh-token-refreshed","expires_in":1800}`))
	}))
	defer issuer.Close()
	store := chatGPTRefreshTokenStore(issuer.URL, "fake-access-token-old", "fake-refresh-token-old", now.Add(time.Minute))
	if err := auth.SaveTokenStore(storePath, store); err != nil {
		t.Fatalf("SaveTokenStore: %v", err)
	}
	client := newChatGPTRefreshTestClient(storePath, now)

	// When
	var wg sync.WaitGroup
	errCh := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			creds, err := client.requestCredentials(context.Background())
			if err != nil {
				errCh <- err
				return
			}
			if creds.BearerToken != "fake-access-token-refreshed" {
				errCh <- errors.New("unexpected bearer token")
			}
		}()
	}
	<-started
	close(release)
	wg.Wait()
	close(errCh)

	// Then
	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	if n := atomic.LoadInt32(&refreshCalls); n != 1 {
		t.Fatalf("refresh calls = %d, want exactly 1", n)
	}
}

func newRefreshIssuer(t *testing.T, calls *int32, status int, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(calls, 1)
		if r.URL.Path != "/oauth/token" {
			t.Fatalf("path = %q, want /oauth/token", r.URL.Path)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.PostForm.Get("refresh_token") == "" {
			t.Fatalf("refresh_token form value missing: %v", r.PostForm)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

func newChatGPTRefreshTestClient(storePath string, now time.Time) *Client {
	client := NewClient(config.LLMConfig{Provider: config.ProviderChatGPT})
	client.authStorePath = func() (string, error) { return storePath, nil }
	client.chatGPTNow = func() time.Time { return now }
	return client
}

func chatGPTRefreshTokenStore(issuer, accessToken, refreshToken string, expiresAt time.Time) auth.TokenStore {
	return auth.TokenStore{
		AuthMode:     auth.AuthModeChatGPT,
		Issuer:       issuer,
		AccessToken:  accessToken,
		RefreshToken: refreshToken,
		ExpiresAt:    expiresAt,
		LastRefresh:  expiresAt.Add(-time.Hour),
		AccountID:    "acct-refresh-test",
		Email:        "writer@example.invalid",
		ModelHint:    openAIModel,
	}
}

func assertStoredRefresh(t *testing.T, path, accessToken, refreshToken string, lastRefresh time.Time) {
	t.Helper()
	got, err := auth.LoadTokenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.AccessToken != accessToken || got.RefreshToken != refreshToken {
		t.Fatalf("stored tokens = %q/%q, want %q/%q", got.AccessToken, got.RefreshToken, accessToken, refreshToken)
	}
	if !got.LastRefresh.Equal(lastRefresh) {
		t.Fatalf("LastRefresh = %s, want %s", got.LastRefresh, lastRefresh)
	}
	if !got.ExpiresAt.Equal(lastRefresh.Add(30 * time.Minute)) {
		t.Fatalf("ExpiresAt = %s, want now + expires_in", got.ExpiresAt)
	}
}
