package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOAuthRefreshReturnsInvalidGrant_whenRefreshTokenRevoked(t *testing.T) {
	// Given
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"revoked refresh token fake-refresh-token-secret123456"}`))
	}))
	defer issuer.Close()
	client := mustOAuthClient(t, issuer.URL)

	// When
	_, err := client.Refresh(context.Background(), "fake-refresh-token-secret123456")

	// Then
	if !errors.Is(err, ErrOAuthInvalidGrant) {
		t.Fatalf("err = %v, want ErrOAuthInvalidGrant", err)
	}
	if !strings.Contains(err.Error(), "invalid_grant") {
		t.Fatalf("err = %q, want invalid_grant context", err.Error())
	}
	if strings.Contains(err.Error(), "fake-refresh-token-secret123456") {
		t.Fatalf("err leaked refresh token: %q", err.Error())
	}
}
