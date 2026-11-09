package llm

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/ShayCichocki/marginctl/internal/auth"
)

func TestChatGPTCredentialsRefreshReturnsPromptly_whenContextCanceled(t *testing.T) {
	// Given
	storePath := filepath.Join(t.TempDir(), "auth.json")
	now := time.Date(2026, 7, 9, 15, 0, 0, 0, time.UTC)
	started := make(chan struct{})
	release := make(chan struct{})
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
	}))
	defer issuer.Close()
	defer close(release)
	store := chatGPTRefreshTokenStore(issuer.URL, "fake-access-token-old", "fake-refresh-token-old", now.Add(time.Minute))
	if err := auth.SaveTokenStore(storePath, store); err != nil {
		t.Fatalf("SaveTokenStore: %v", err)
	}
	client := newChatGPTRefreshTestClient(storePath, now)
	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)

	// When
	go func() {
		_, err := client.requestCredentials(ctx)
		errCh <- err
	}()
	<-started
	cancel()

	// Then
	select {
	case err := <-errCh:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("refresh did not return promptly after context cancellation")
	}
}
