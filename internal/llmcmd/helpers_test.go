package llmcmd

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/ShayCichocki/marginctl/internal/llm"
)

// TestMain keeps user-level directories (config, auth store) off the real home.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "marginctl-test-home-")
	if err != nil {
		os.Exit(1)
	}
	for _, k := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "APPDATA", "LOCALAPPDATA"} {
		_ = os.Setenv(k, dir)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// respondWith returns a server that always responds with body.
func respondWith(body string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
}

// sseServer replies to any request with the given SSE data frames.
func sseServer(frames ...string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, f := range frames {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", f)
		}
	}))
}

// drain reads a stream to its terminal event and returns the accumulated text.
func drain(ch <-chan llm.StreamEvent) string {
	var b strings.Builder
	for ev := range ch {
		if ev.Done || ev.Err != nil {
			break
		}
		b.WriteString(ev.Delta)
	}
	return b.String()
}
