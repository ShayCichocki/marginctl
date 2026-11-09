package app

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ShayCichocki/marginctl/internal/auth"
	"github.com/ShayCichocki/marginctl/internal/config"
)

type fakeOAuthIssuer struct {
	server *httptest.Server
}

func newFakeOAuthIssuer(t *testing.T) fakeOAuthIssuer {
	t.Helper()
	issuer := fakeOAuthIssuer{}
	issuer.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			_ = json.NewEncoder(w).Encode(map[string]string{"device_auth_id": "device-1", "user_code": "USER-CODE"})
		case "/api/accounts/deviceauth/token":
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "authorized", "authorization_code": "device-code", "code_verifier": "device-verifier"})
		case "/oauth/token":
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			kind := "browser"
			if r.Form.Get("code") == "device-code" {
				kind = "device"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "fake-access-token-" + kind, "refresh_token": "fake-refresh-token-" + kind, "id_token": "fake-id-token", "expires_in": 3600})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(issuer.server.Close)
	return issuer
}

func newCountingDeviceOAuthIssuer(t *testing.T) fakeOAuthIssuer {
	t.Helper()
	issuer := fakeOAuthIssuer{}
	var tokenCalls int32
	issuer.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			_ = json.NewEncoder(w).Encode(map[string]string{"device_auth_id": "device-1", "user_code": "USER-CODE"})
		case "/api/accounts/deviceauth/token":
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "authorized", "authorization_code": "device-code", "code_verifier": "device-verifier"})
		case "/oauth/token":
			n := atomic.AddInt32(&tokenCalls, 1)
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "fake-access-token-" + strconv.Itoa(int(n)), "refresh_token": "fake-refresh-token-" + strconv.Itoa(int(n)), "id_token": "fake-id-token", "expires_in": 3600})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(issuer.server.Close)
	return issuer
}

func newPendingThenDeviceOAuthIssuer(t *testing.T) fakeOAuthIssuer {
	t.Helper()
	issuer := fakeOAuthIssuer{}
	var tokenPolls int32
	issuer.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			_ = json.NewEncoder(w).Encode(map[string]string{"device_auth_id": "device-1", "user_code": "USER-CODE"})
		case "/api/accounts/deviceauth/token":
			if atomic.AddInt32(&tokenPolls, 1) == 1 {
				_ = json.NewEncoder(w).Encode(map[string]string{"status": "pending"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "authorized", "authorization_code": "device-code", "code_verifier": "device-verifier"})
		case "/oauth/token":
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "fake-access-token-device", "refresh_token": "fake-refresh-token-device", "id_token": "fake-id-token", "expires_in": 3600})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(issuer.server.Close)
	return issuer
}

func newAlwaysPendingDeviceOAuthIssuer(t *testing.T) fakeOAuthIssuer {
	t.Helper()
	issuer := fakeOAuthIssuer{}
	issuer.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			_ = json.NewEncoder(w).Encode(map[string]string{"device_auth_id": "device-1", "user_code": "USER-CODE"})
		case "/api/accounts/deviceauth/token":
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "pending"})
		case "/oauth/token":
			t.Fatalf("unexpected token exchange while device authorization is pending")
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(issuer.server.Close)
	return issuer
}

func newConnectTestModel(t *testing.T, issuer fakeOAuthIssuer) (Model, string, *[]string) {
	t.Helper()
	cfgPath := isolateConfigEnv(t)
	authPath := filepath.Join(t.TempDir(), "auth.json")
	opened := []string{}
	m := New()
	m.connectDeps.oauthConfig = auth.OAuthConfig{Issuer: issuer.server.URL, HTTPClient: issuer.server.Client()}
	m.connectDeps.authStorePath = func() (string, error) { return authPath, nil }
	m.connectDeps.writeProvider = func(provider string) (config.SaveResult, error) {
		if provider != config.ProviderChatGPT {
			t.Fatalf("provider = %q, want chatgpt", provider)
		}
		return config.SaveResult{Path: cfgPath}, nil
	}
	m.connectDeps.openBrowser = func(target string) error {
		opened = append(opened, target)
		return nil
	}
	return m, authPath, &opened
}

func enterConnectCommand(m Model, command string) (Model, tea.Cmd) {
	m = update(m, keyRunes(":"))
	m = update(m, keyRunes(command))
	return updateCmd(m, tea.KeyMsg{Type: tea.KeyEnter})
}

func runCmd[T tea.Msg](t *testing.T, cmd tea.Cmd) T {
	t.Helper()
	if cmd == nil {
		t.Fatal("cmd = nil")
	}
	msg := cmd()
	got, ok := msg.(T)
	if !ok {
		t.Fatalf("msg type = %T, want requested message", msg)
	}
	return got
}

func runCmdAsync(cmd tea.Cmd) <-chan tea.Msg {
	ch := make(chan tea.Msg, 1)
	go func() { ch <- cmd() }()
	return ch
}

func readTokenStore(t *testing.T, path string) auth.TokenStore {
	t.Helper()
	store, err := auth.LoadTokenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func occupyLoopbackPort(t *testing.T, port int) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if err != nil {
		t.Fatalf("occupy port %d: %v", port, err)
	}
	return listener
}
