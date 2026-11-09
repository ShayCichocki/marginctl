package auth

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTokenStoreSaveLoadRoundTrip_writesPrivateSchema(t *testing.T) {
	// Given
	path := filepath.Join(t.TempDir(), "nested", "auth.json")
	token := fakeTokenStore()

	// When
	if err := SaveTokenStore(path, token); err != nil {
		t.Fatalf("SaveTokenStore: %v", err)
	}
	got, err := LoadTokenStore(path)

	// Then
	if err != nil {
		t.Fatalf("LoadTokenStore: %v", err)
	}
	if got != token {
		t.Fatalf("token = %+v, want %+v", got, token)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Fatalf("perm = %o, want 600", perm)
	}
	assertJSONFields(t, path)
}

func TestTokenStoreDelete_removesFileAndStatusIsDisconnected(t *testing.T) {
	// Given
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := SaveTokenStore(path, fakeTokenStore()); err != nil {
		t.Fatalf("SaveTokenStore: %v", err)
	}

	// When
	if err := DeleteTokenStore(path); err != nil {
		t.Fatalf("DeleteTokenStore: %v", err)
	}
	status := TokenStoreStatus(path)

	// Then
	if status.Connected {
		t.Fatalf("Connected = true, want false after delete")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("stat after delete = %v, want missing file", err)
	}
	if !strings.Contains(status.Format(), "Not connected") {
		t.Fatalf("status = %q, want Not connected", status.Format())
	}
}

func TestTokenStoreStatus_reportsNotConnected_whenFileMissing(t *testing.T) {
	// Given
	path := filepath.Join(t.TempDir(), "auth.json")

	// When
	status := TokenStoreStatus(path)

	// Then
	if status.Connected {
		t.Fatalf("Connected = true, want false for missing auth store")
	}
	if status.Action == "" || !strings.Contains(status.Format(), "run :connect openai") {
		t.Fatalf("status = %+v formatted %q, want actionable connect guidance", status, status.Format())
	}
}

func TestTokenStoreStatus_reportsMalformedJSONWithoutLeakingContents(t *testing.T) {
	// Given
	path := filepath.Join(t.TempDir(), "auth.json")
	leakedToken := "sk-test-malformed-token"
	if err := os.WriteFile(path, []byte(`{"access_token":"`+leakedToken+`",`), 0600); err != nil {
		t.Fatalf("write malformed auth: %v", err)
	}

	// When
	status := TokenStoreStatus(path)
	_, err := LoadTokenStore(path)

	// Then
	if status.Connected {
		t.Fatalf("Connected = true, want false for malformed auth store")
	}
	if err == nil {
		t.Fatal("LoadTokenStore err = nil, want malformed JSON error")
	}
	for _, text := range []string{status.Format(), err.Error()} {
		if !strings.Contains(text, path) {
			t.Fatalf("text = %q, want auth path context", text)
		}
		if strings.Contains(text, leakedToken) || strings.Contains(text, "access_token") {
			t.Fatalf("text leaked auth contents: %q", text)
		}
	}
}

func TestTokenStoreStatus_redactsTokenLikeValues(t *testing.T) {
	// Given
	path := filepath.Join(t.TempDir(), "auth.json")
	token := fakeTokenStore()
	if err := SaveTokenStore(path, token); err != nil {
		t.Fatalf("SaveTokenStore: %v", err)
	}

	// When
	status := TokenStoreStatus(path)
	formatted := status.Format()
	redacted := RedactSecrets("Bearer " + token.AccessToken + " refresh " + token.RefreshToken)

	// Then
	for _, secret := range []string{token.AccessToken, token.RefreshToken} {
		if strings.Contains(formatted, secret) || strings.Contains(redacted, secret) {
			t.Fatalf("secret %q leaked in formatted=%q redacted=%q", secret, formatted, redacted)
		}
	}
	if !strings.Contains(formatted, token.Email) || !strings.Contains(formatted, token.AccountID) {
		t.Fatalf("formatted status = %q, want safe account metadata", formatted)
	}
	if !strings.Contains(redacted, "[redacted]") {
		t.Fatalf("redacted = %q, want redaction marker", redacted)
	}
}

func TestTokenStoreErrors_redactTokenLikePathComponents(t *testing.T) {
	// Given
	secretPathPart := "sk-test-path-token"
	path := filepath.Join(t.TempDir(), secretPathPart, "auth.json")

	// When
	_, err := LoadTokenStore(path)

	// Then
	if err == nil {
		t.Fatal("LoadTokenStore err = nil, want missing file error")
	}
	if strings.Contains(err.Error(), secretPathPart) {
		t.Fatalf("err leaked token-like path component: %q", err.Error())
	}
	if !strings.Contains(err.Error(), "[redacted]") {
		t.Fatalf("err = %q, want redaction marker", err.Error())
	}
}

func assertJSONFields(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read auth json: %v", err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal auth json: %v", err)
	}
	for _, field := range []string{"auth_mode", "issuer", "access_token", "refresh_token", "expires_at", "last_refresh", "account_id", "email", "model_hint"} {
		if _, ok := raw[field]; !ok {
			t.Fatalf("missing json field %q in %s", field, string(data))
		}
	}
}

func fakeTokenStore() TokenStore {
	return TokenStore{
		AuthMode:     AuthModeChatGPT,
		Issuer:       "https://auth.example.invalid",
		AccessToken:  "fake-access-token-123456",
		RefreshToken: "fake-refresh-token-abcdef",
		ExpiresAt:    time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC),
		LastRefresh:  time.Date(2026, 7, 9, 11, 0, 0, 0, time.UTC),
		AccountID:    "acct-test-123",
		Email:        "writer@example.invalid",
		ModelHint:    "gpt-5.1",
	}
}
