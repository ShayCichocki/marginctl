package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"unicode/utf8"

	"github.com/ShayCichocki/marginctl/internal/config"
)

func FuzzOpenAIStreamEventSequences(f *testing.F) {
	f.Add([]byte("hello"), true, false)
	f.Add([]byte("partial"), false, false)
	f.Add([]byte("bad"), false, true)
	type sequence struct {
		Delta     string `json:"delta"`
		Terminal  bool   `json:"terminal"`
		Malformed bool   `json:"malformed"`
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request openAIRequest
		if json.NewDecoder(r.Body).Decode(&request) != nil || len(request.Messages) == 0 {
			http.Error(w, "bad test request", http.StatusBadRequest)
			return
		}
		var input sequence
		if json.Unmarshal([]byte(request.Messages[0].Content), &input) != nil {
			http.Error(w, "bad test sequence", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if input.Malformed {
			_, _ = fmt.Fprint(w, "data: {\"broken\":\n\n")
			return
		}
		frame, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]string{"content": input.Delta}}}})
		_, _ = fmt.Fprintf(w, "data: %s\n\n", frame)
		if input.Terminal {
			_, _ = fmt.Fprint(w, "data: [DONE]\n\n")
		}
	}))
	defer server.Close()
	client := NewClient(config.LLMConfig{Provider: config.ProviderOpenAI, APIKey: "test", Endpoint: server.URL})

	f.Fuzz(func(t *testing.T, delta []byte, terminal, malformed bool) {
		if len(delta) > 4096 || !utf8.Valid(delta) {
			t.Skip()
		}
		encoded, _ := json.Marshal(sequence{Delta: string(delta), Terminal: terminal, Malformed: malformed})
		var terminals int
		var received string
		var successful bool
		for event := range client.CompleteStream(context.Background(), string(encoded), "user") {
			if event.Done || event.Err != nil {
				terminals++
				successful = event.Done
			} else {
				received += event.Delta
			}
		}
		if terminals != 1 {
			t.Fatalf("got %d terminal events", terminals)
		}
		if successful && (!terminal || malformed || len(delta) == 0) {
			t.Fatalf("incomplete or empty sequence succeeded")
		}
		if !utf8.ValidString(received) {
			t.Fatal("stream emitted invalid UTF-8")
		}
	})
}
