package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ShayCichocki/marginctl/internal/auth"
	"github.com/ShayCichocki/marginctl/internal/config"
	"github.com/ShayCichocki/marginctl/internal/llmcmd"
)

func TestChatGPTConnectEndToEndRefreshLLMAuthStatusDisconnect(t *testing.T) {
	// Given
	dataDir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dataDir)
	t.Setenv("HOME", dataDir)
	authPath, err := auth.AuthStorePath()
	if err != nil {
		t.Fatalf("AuthStorePath: %v", err)
	}
	backend := newEndToEndChatGPTBackend(t)
	defer backend.server.Close()
	issuer := newEndToEndOAuthIssuer(t)
	defer issuer.server.Close()
	_ = isolateConfigEnv(t)
	t.Setenv("MARGINCTL_LLM_ENDPOINT", backend.server.URL)

	m := New()
	root, _, _ := newTestProject(t)
	if err := m.OpenPath(root); err != nil {
		t.Fatalf("OpenPath: %v", err)
	}
	m.width = 100
	m.height = 30
	m.updateLayout()
	m.connectDeps.oauthConfig = auth.OAuthConfig{Issuer: issuer.server.URL, HTTPClient: issuer.server.Client()}
	m.connectDeps.authStorePath = auth.AuthStorePath
	m.connectDeps.writeProvider = config.SaveProviderMetadataResult

	// When: connect via the deterministic device flow.
	m, cmd := enterConnectCommand(m, "connect openai device")
	ready := runCmd[connectDeviceReadyMsg](t, cmd)
	m, waitCmd := updateCmd(m, ready)
	m = update(m, runCmd[connectResultMsg](t, waitCmd))

	// Then: connect stored OAuth credentials, activated the live ChatGPT client, and leaked no token in notices.
	store := readTokenStore(t, authPath)
	if store.AuthMode != auth.AuthModeChatGPT || store.AccessToken != e2eAccessTokenInitial || store.RefreshToken != e2eRefreshTokenInitial {
		t.Fatalf("store = %+v, want initial ChatGPT token store", store)
	}
	if m.llmClient.Provider() != config.ProviderChatGPT {
		t.Fatalf("live client provider = %q, want chatgpt after connect", m.llmClient.Provider())
	}
	assertNoE2ETokenLeak(t, "connect notice", m.notice)

	// When: a real app LLM command preflights refresh, calls the fake ChatGPT backend, and returns a proposal.
	m = runVoiceGuideCommand(t, m)

	// Then
	if atomic.LoadInt32(&issuer.refreshCalls) != 1 {
		t.Fatalf("refresh calls = %d, want 1 before LLM request", atomic.LoadInt32(&issuer.refreshCalls))
	}
	if atomic.LoadInt32(&backend.calls) != 1 {
		t.Fatalf("backend calls = %d, want one OAuth-backed LLM request", atomic.LoadInt32(&backend.calls))
	}
	if backend.authorization.Load() != "Bearer "+e2eAccessTokenRefreshed {
		t.Fatalf("backend Authorization = %q, want refreshed ChatGPT bearer", backend.authorization.Load())
	}
	if backend.accountID.Load() != e2eAccountRefreshed {
		t.Fatalf("backend account = %q, want refreshed account header", backend.accountID.Load())
	}
	if !m.results.Visible() || !strings.Contains(m.results.View(), "e2e voice guide") {
		t.Fatalf("results visible=%v view=%q, want voice guide proposal", m.results.Visible(), m.results.View())
	}
	assertNoE2ETokenLeak(t, "LLM results", m.results.View())
	assertNoE2ETokenLeak(t, "LLM notice", m.notice)

	// When: auth status is shown through the app command path.
	m.executeCommand("auth")
	status := m.authStatusText()

	// Then
	if !m.results.Visible() || !strings.Contains(status, "Connected") || !strings.Contains(status, e2eAccountRefreshed) {
		t.Fatalf("auth status = %q, want connected refreshed account", status)
	}
	assertNoE2ETokenLeak(t, "auth status", status)

	// When: disconnect logs out locally.
	m.executeCommand("disconnect")

	// Then
	if m.noticeIsErr || !strings.Contains(m.notice, "Disconnected") {
		t.Fatalf("disconnect notice = %q err=%v, want local logout", m.notice, m.noticeIsErr)
	}
	if _, err := os.Stat(authPath); !os.IsNotExist(err) {
		t.Fatalf("auth store stat after disconnect = %v, want removed", err)
	}
	if status := auth.TokenStoreStatus(authPath); status.Connected {
		t.Fatalf("status after disconnect = %+v, want disconnected", status)
	}
	assertNoE2ETokenLeak(t, "disconnect notice", m.notice)
}

const (
	e2eAccessTokenInitial   = "fake-access-token-e2e-initial"
	e2eRefreshTokenInitial  = "fake-refresh-token-e2e-initial"
	e2eAccessTokenRefreshed = "fake-access-token-e2e-refreshed"
	e2eRefreshTokenNew      = "fake-refresh-token-e2e-new"
	e2eAccountInitial       = "acct-e2e-initial"
	e2eAccountRefreshed     = "acct-e2e-refreshed"
)

type endToEndOAuthIssuer struct {
	server       *httptest.Server
	refreshCalls int32
}

func newEndToEndOAuthIssuer(t *testing.T) *endToEndOAuthIssuer {
	t.Helper()
	issuer := &endToEndOAuthIssuer{}
	issuer.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			_ = json.NewEncoder(w).Encode(map[string]string{"device_auth_id": "device-e2e", "user_code": "E2E-CODE"})
		case "/api/accounts/deviceauth/token":
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "authorized", "authorization_code": "device-e2e-code", "code_verifier": "device-e2e-verifier"})
		case "/oauth/token":
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			grantType := r.Form.Get("grant_type")
			if grantType == "refresh_token" {
				atomic.AddInt32(&issuer.refreshCalls, 1)
				if r.Form.Get("refresh_token") != e2eRefreshTokenInitial {
					t.Fatalf("refresh_token form = %q, want initial refresh token", r.Form.Get("refresh_token"))
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"access_token": e2eAccessTokenRefreshed, "refresh_token": e2eRefreshTokenNew, "expires_in": 1800, "account_id": e2eAccountRefreshed})
				return
			}
			if grantType != "authorization_code" {
				t.Fatalf("grant_type = %q, want authorization_code or refresh_token", grantType)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": e2eAccessTokenInitial, "refresh_token": e2eRefreshTokenInitial, "expires_in": 0, "account_id": e2eAccountInitial})
		default:
			http.NotFound(w, r)
		}
	}))
	return issuer
}

type endToEndChatGPTBackend struct {
	server        *httptest.Server
	calls         int32
	authorization atomic.Value
	accountID     atomic.Value
}

func newEndToEndChatGPTBackend(t *testing.T) *endToEndChatGPTBackend {
	t.Helper()
	backend := &endToEndChatGPTBackend{}
	backend.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&backend.calls, 1)
		backend.authorization.Store(r.Header.Get("Authorization"))
		backend.accountID.Store(r.Header.Get("ChatGPT-Account-Id"))
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		// The codex backend speaks the Responses API: a structured `input` array
		// and system prompt in `instructions`, never chat-completions `messages`.
		if _, ok := body["input"].([]any); !ok {
			t.Fatalf("request body = %v, want Responses-API input", body)
		}
		if _, ok := body["messages"]; ok {
			t.Fatalf("request body = %v, must not send chat-completions messages", body)
		}
		// The codex backend requires stream:true and replies over SSE — even the
		// one-shot Complete path (e.g. VoiceGuide) consumes a stream and accumulates.
		if stream, _ := body["stream"].(bool); !stream {
			t.Fatalf("request body = %v, want stream=true", body)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"# e2e voice guide\"}\n\n")
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":17,\"output_tokens\":5}}}\n\n")
	}))
	return backend
}

func runVoiceGuideCommand(t *testing.T, m Model) Model {
	t.Helper()
	m = update(m, keyRunes(":"))
	m = update(m, keyRunes("VoiceGuide"))
	m, cmd := updateCmd(m, tea.KeyMsg{Type: tea.KeyEnter})
	response := runFirstBatchCmd[llmcmd.ResponseMsg](t, cmd)
	return update(m, response)
}

func runFirstBatchCmd[T tea.Msg](t *testing.T, cmd tea.Cmd) T {
	t.Helper()
	if cmd == nil {
		t.Fatal("cmd = nil")
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok || len(batch) == 0 {
		t.Fatalf("msg type = %T, want non-empty tea.BatchMsg", msg)
	}
	got, ok := batch[0]().(T)
	if !ok {
		t.Fatalf("first batch msg type = %T, want requested message", got)
	}
	return got
}

func assertNoE2ETokenLeak(t *testing.T, label string, text string) {
	t.Helper()
	for _, token := range []string{e2eAccessTokenInitial, e2eRefreshTokenInitial, e2eAccessTokenRefreshed, e2eRefreshTokenNew} {
		if strings.Contains(text, token) {
			t.Fatalf("%s leaked token substring %q in %q", label, token, text)
		}
	}
}
