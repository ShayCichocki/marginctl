package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ShayCichocki/marginctl/internal/auth"
)

func TestCompleteChatGPTRetriesOnceAfterExpiredToken401(t *testing.T) {
	// Given
	storePath := filepath.Join(t.TempDir(), "auth.json")
	now := time.Date(2026, 7, 9, 15, 0, 0, 0, time.UTC)
	var refreshCalls int32
	issuer := newRefreshIssuer(t, &refreshCalls, http.StatusOK, `{"access_token":"fake-access-token-refreshed","refresh_token":"fake-refresh-token-refreshed","expires_in":1800}`)
	store := chatGPTRefreshTokenStore(issuer.URL, "fake-access-token-stale", "fake-refresh-token-old", now.Add(time.Hour))
	if err := auth.SaveTokenStore(storePath, store); err != nil {
		t.Fatalf("SaveTokenStore: %v", err)
	}
	var apiCalls int32
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch atomic.AddInt32(&apiCalls, 1) {
		case 1:
			if got := r.Header.Get("Authorization"); got != "Bearer fake-access-token-stale" {
				t.Fatalf("first Authorization = %q, want stale bearer", got)
			}
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"error":{"message":"expired token"}}`))
		default:
			if got := r.Header.Get("Authorization"); got != "Bearer fake-access-token-refreshed" {
				t.Fatalf("retry Authorization = %q, want refreshed bearer", got)
			}
			writeResponsesSSE(w, sseTextComplete("refreshed ok", 0, 0)...)
		}
	}))
	defer api.Close()
	client := newChatGPTRefreshTestClient(storePath, now)
	client.endpoint = api.URL

	// When
	got, _, err := client.Complete(context.Background(), "system", "user")

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if got != "refreshed ok" {
		t.Fatalf("response = %q, want refreshed ok", got)
	}
	if n := atomic.LoadInt32(&apiCalls); n != 2 {
		t.Fatalf("api calls = %d, want initial + one retry", n)
	}
	if n := atomic.LoadInt32(&refreshCalls); n != 1 {
		t.Fatalf("refresh calls = %d, want 1", n)
	}
}
