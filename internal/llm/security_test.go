package llm

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/ShayCichocki/marginctl/internal/config"
)

func TestRejectsRemoteHTTP(t *testing.T) {
	c := NewClient(config.LLMConfig{Provider: config.ProviderOpenAI, APIKey: "k", Endpoint: "http://example.com/v1"})
	if _, _, err := c.Complete(context.Background(), "s", "u"); err == nil || !strings.Contains(err.Error(), "HTTPS") {
		t.Fatalf("error = %v", err)
	}
}

func TestRejectsOversizedSuccessAndError(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusBadRequest} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
				_, _ = w.Write([]byte(strings.Repeat("x", maxSuccessBodyBytes+1)))
			}))
			defer s.Close()
			c := NewClient(config.LLMConfig{Provider: config.ProviderOpenAI, APIKey: "k", Endpoint: s.URL})
			if _, _, err := c.Complete(context.Background(), "s", "u"); err == nil || !strings.Contains(err.Error(), "exceeds") {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestRequestUsesImmutableConfigurationSnapshot(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		if r.Header.Get("x-api-key") != "old-key" {
			t.Errorf("key = %q", r.Header.Get("x-api-key"))
		}
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn"}`))
	}))
	defer s.Close()
	c := NewClient(config.LLMConfig{Provider: config.ProviderAnthropic, APIKey: "old-key", Model: "old-model", Endpoint: s.URL})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); _, _, _ = c.Complete(context.Background(), "s", "u") }()
	<-started
	c.Reconfigure(config.LLMConfig{Provider: config.ProviderOpenAI, APIKey: "new-key", Model: "new-model", Endpoint: s.URL})
	c.SetModel("newer-model")
	close(release)
	wg.Wait()
}
