package app

import (
	"os"
	"strings"
	"testing"

	"github.com/ShayCichocki/marginctl/internal/auth"
)

func TestAuthStatusShowsRedactedStoreAndEnvOverride_whenFakeLoginExists(t *testing.T) {
	// Given
	m, authPath, _ := newConnectTestModel(t, newFakeOAuthIssuer(t))
	t.Setenv("MARGINCTL_LLM_PROVIDER", "openai")
	if err := auth.SaveTokenStore(authPath, auth.TokenStore{
		AuthMode:     auth.AuthModeChatGPT,
		Issuer:       "https://auth.example.test",
		AccessToken:  "fake-access-token-secret123",
		RefreshToken: "fake-refresh-token-secret123",
		AccountID:    "acct_123",
		Email:        "writer@example.test",
	}); err != nil {
		t.Fatal(err)
	}

	// When
	m.executeCommand("auth status")
	text := m.authStatusText()

	// Then
	if !m.results.Visible() {
		t.Fatal(":auth status should open the results pane")
	}
	for _, want := range []string{"Connected", "mode=chatgpt", "account=acct_123", "email=writer@example.test", "MARGINCTL_LLM_PROVIDER=openai", "blocks activation"} {
		if !strings.Contains(text, want) {
			t.Fatalf(":auth status output missing %q:\n%s", want, text)
		}
	}
	for _, forbidden := range []string{"fake-access-token-secret123", "fake-refresh-token-secret123"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf(":auth status leaked token material %q:\n%s", forbidden, text)
		}
	}
}

func TestDisconnectReportsNotConnected_whenAuthStoreMissing(t *testing.T) {
	// Given
	m, _, _ := newConnectTestModel(t, newFakeOAuthIssuer(t))

	// When
	m.executeCommand("disconnect")

	// Then
	if m.notice != "Not connected" || m.noticeIsErr {
		t.Fatalf(":disconnect notice = %q err=%v, want non-error Not connected", m.notice, m.noticeIsErr)
	}
}

func TestDisconnectDeletesLocalAuthStoreOnly_whenAuthStoreExists(t *testing.T) {
	// Given
	m, authPath, _ := newConnectTestModel(t, newFakeOAuthIssuer(t))
	if err := auth.SaveTokenStore(authPath, auth.TokenStore{AuthMode: auth.AuthModeChatGPT, AccessToken: "fake-access-token-secret123"}); err != nil {
		t.Fatal(err)
	}

	// When
	m.executeCommand("disconnect")

	// Then
	if m.noticeIsErr || !strings.Contains(m.notice, "Disconnected") {
		t.Fatalf(":disconnect notice = %q err=%v, want disconnected notice", m.notice, m.noticeIsErr)
	}
	if _, err := os.Stat(authPath); !os.IsNotExist(err) {
		t.Fatalf("auth store still exists after local logout: %v", err)
	}
}
