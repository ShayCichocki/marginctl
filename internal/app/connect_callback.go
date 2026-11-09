package app

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"time"

	"github.com/ShayCichocki/marginctl/internal/auth"
)

func listenConnectCallback() (net.Listener, int, error) {
	for _, port := range connectLoopbackPorts {
		listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			return listener, port, nil
		}
	}
	return nil, 0, errors.New("OpenAI browser auth port unavailable; use device code with :connect openai device")
}

func waitForBrowserCallback(ctx context.Context, listener net.Listener) (string, string, error) {
	defer closeListener(listener)
	result := make(chan browserCallbackResult, 1)
	server := &http.Server{Handler: browserCallbackHandler(result), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
	}()
	serveErr := make(chan error, 1)
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()
	select {
	case got := <-result:
		shutdownCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		if got.err != nil {
			return "", "", got.err
		}
		return got.code, got.state, nil
	case err := <-serveErr:
		if err != nil {
			return "", "", fmt.Errorf("OpenAI browser callback server: %w", err)
		}
		return "", "", context.Canceled
	case <-ctx.Done():
		return "", "", ctx.Err()
	}
}

type browserCallbackResult struct {
	code  string
	state string
	err   error
}

func browserCallbackHandler(result chan<- browserCallbackResult) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != connectCallbackPath {
			http.NotFound(w, r)
			return
		}
		query := r.URL.Query()
		if oauthErr := query.Get("error"); oauthErr != "" {
			result <- browserCallbackResult{err: fmt.Errorf("OpenAI browser auth failed: %s", auth.RedactSecrets(oauthErr))}
			_, _ = w.Write([]byte("marginctl received the failed sign-in. Return to the terminal."))
			return
		}
		code := query.Get("code")
		if code == "" {
			result <- browserCallbackResult{err: errors.New("OpenAI browser callback missing code; restart :connect openai")}
			_, _ = w.Write([]byte("marginctl could not complete sign-in. Return to the terminal."))
			return
		}
		result <- browserCallbackResult{code: code, state: query.Get("state")}
		_, _ = w.Write([]byte("marginctl connected. You can close this browser tab."))
	})
}

func randomConnectState() (string, error) {
	raw := make([]byte, 18)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate OAuth state: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func closeListener(listener net.Listener) {
	if listener != nil {
		_ = listener.Close()
	}
}

func openBrowserBestEffort(target string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", target)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	default:
		cmd = exec.Command("xdg-open", target)
	}
	return cmd.Start()
}
