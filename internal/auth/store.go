package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/ShayCichocki/marginctl/internal/fsutil"
)

const redactionMarker = "[redacted]"

var tokenPattern = regexp.MustCompile(`(?i)(sk-[A-Za-z0-9_-]{6,}|Bearer\s+[A-Za-z0-9._-]{6,}|vck_[A-Za-z0-9_-]{6,}|AI[A-Za-z0-9_-]{20,}|(?:access|refresh)[-_]token[-_A-Za-z0-9]{6,}|fake-(?:access|refresh)-token[-_A-Za-z0-9]{6,})`)

type TokenStore struct {
	AuthMode     string    `json:"auth_mode"`
	Issuer       string    `json:"issuer"`
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	ExpiresAt    time.Time `json:"expires_at"`
	LastRefresh  time.Time `json:"last_refresh"`
	AccountID    string    `json:"account_id"`
	Email        string    `json:"email"`
	ModelHint    string    `json:"model_hint,omitempty"`
}

type TokenStatus struct {
	Connected   bool
	Path        string
	AuthMode    string
	Issuer      string
	AccountID   string
	Email       string
	ModelHint   string
	ExpiresAt   time.Time
	LastRefresh time.Time
	Action      string
	Problem     string
}

func SaveTokenStore(path string, token TokenStore) error {
	safePath := RedactSecrets(path)
	data, err := json.MarshalIndent(token, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal auth token store for %s: %s", safePath, RedactSecrets(err.Error()))
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return fmt.Errorf("create auth store directory for %s: %s", safePath, RedactSecrets(err.Error()))
	}
	if err := fsutil.WriteFilePrivate(path, data); err != nil {
		return fmt.Errorf("write auth token store %s: %s", safePath, RedactSecrets(err.Error()))
	}
	return nil
}

func LoadTokenStore(path string) (TokenStore, error) {
	safePath := RedactSecrets(path)
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return TokenStore{}, fmt.Errorf("auth token store %s is missing; run :connect openai", safePath)
		}
		return TokenStore{}, fmt.Errorf("read auth token store %s: %s", safePath, RedactSecrets(err.Error()))
	}
	var token TokenStore
	if err := json.Unmarshal(data, &token); err != nil {
		return TokenStore{}, fmt.Errorf("auth token store %s is malformed; run :connect openai", safePath)
	}
	return token, nil
}

func DeleteTokenStore(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("delete auth token store %s: %s", RedactSecrets(path), RedactSecrets(err.Error()))
	}
	return nil
}

func TokenStoreStatus(path string) TokenStatus {
	token, err := LoadTokenStore(path)
	if err != nil {
		return TokenStatus{
			Connected: false,
			Path:      path,
			Action:    "run :connect openai",
			Problem:   RedactSecrets(err.Error()),
		}
	}
	return TokenStatus{
		Connected:   true,
		Path:        path,
		AuthMode:    token.AuthMode,
		Issuer:      token.Issuer,
		AccountID:   token.AccountID,
		Email:       token.Email,
		ModelHint:   token.ModelHint,
		ExpiresAt:   token.ExpiresAt,
		LastRefresh: token.LastRefresh,
	}
}

func (s TokenStatus) Format() string {
	if !s.Connected {
		parts := []string{"Not connected"}
		if s.Problem != "" {
			parts = append(parts, s.Problem)
		}
		if s.Action != "" {
			parts = append(parts, s.Action)
		}
		return RedactSecrets(strings.Join(parts, "; "))
	}
	parts := []string{"Connected", "mode=" + s.AuthMode}
	if s.Email != "" {
		parts = append(parts, "email="+s.Email)
	}
	if s.AccountID != "" {
		parts = append(parts, "account="+s.AccountID)
	}
	if s.Issuer != "" {
		parts = append(parts, "issuer="+s.Issuer)
	}
	if s.ModelHint != "" {
		parts = append(parts, "model="+s.ModelHint)
	}
	if !s.ExpiresAt.IsZero() {
		parts = append(parts, "expires="+s.ExpiresAt.UTC().Format(time.RFC3339))
	}
	return RedactSecrets(strings.Join(parts, "; "))
}

func RedactSecrets(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	text = tokenPattern.ReplaceAllString(text, redactionMarker)
	return strings.TrimSpace(text)
}
