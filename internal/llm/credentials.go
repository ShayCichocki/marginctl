package llm

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ShayCichocki/marginctl/internal/auth"
	"github.com/ShayCichocki/marginctl/internal/config"
)

var errAPIKeyNotConfigured = errors.New("API key not configured. Set MARGINCTL_LLM_API_KEY or configure in ~/.config/marginctl/config.toml")

const chatGPTRefreshSafetyWindow = 5 * time.Minute

type credentialSource interface {
	Credentials(context.Context) (requestCredentials, error)
	Refresh(context.Context) (requestCredentials, error)
}

type requestCredentials struct {
	APIKey      string
	BearerToken string
	AccountID   string
}

type apiKeyCredentialSource struct {
	apiKey string
}

func (s apiKeyCredentialSource) Credentials(context.Context) (requestCredentials, error) {
	apiKey := strings.TrimSpace(s.apiKey)
	if apiKey == "" {
		return requestCredentials{}, errAPIKeyNotConfigured
	}
	return requestCredentials{APIKey: apiKey, BearerToken: apiKey}, nil
}

func (s apiKeyCredentialSource) Refresh(ctx context.Context) (requestCredentials, error) {
	return s.Credentials(ctx)
}

type oauthRefresher interface {
	Refresh(context.Context, string) (auth.TokenSet, error)
}

type oauthRefreshClientFactory func(auth.OAuthConfig) (oauthRefresher, error)

type chatGPTCredentialSource struct {
	authStorePath     func() (string, error)
	now               func() time.Time
	oauthClient       oauthRefreshClientFactory
	refreshWindow     time.Duration
	mu                sync.Mutex
	inFlight          <-chan refreshResult
	lastRefreshResult refreshResult
}

type refreshResult struct {
	credentials requestCredentials
	err         error
}

func (s *chatGPTCredentialSource) Credentials(ctx context.Context) (requestCredentials, error) {
	return s.credentials(ctx, false)
}

func (s *chatGPTCredentialSource) Refresh(ctx context.Context) (requestCredentials, error) {
	return s.credentials(ctx, true)
}

func (s *chatGPTCredentialSource) credentials(ctx context.Context, forceRefresh bool) (requestCredentials, error) {
	path, err := s.authStorePath()
	if err != nil {
		return requestCredentials{}, fmt.Errorf("chatgpt auth unavailable; run :connect openai: %w", err)
	}
	token, err := auth.LoadTokenStore(path)
	if err != nil {
		return requestCredentials{}, fmt.Errorf("chatgpt auth unavailable: %w", err)
	}
	accessToken := strings.TrimSpace(token.AccessToken)
	if token.AuthMode != auth.AuthModeChatGPT || accessToken == "" {
		return requestCredentials{}, errors.New("chatgpt auth store is not usable; run :connect openai")
	}
	if !forceRefresh && !s.shouldRefresh(token) {
		return requestCredentials{BearerToken: accessToken, AccountID: strings.TrimSpace(token.AccountID)}, nil
	}
	if strings.TrimSpace(token.RefreshToken) == "" {
		return requestCredentials{}, errors.New("chatgpt auth expired and cannot refresh; run :connect openai")
	}
	return s.refreshShared(ctx, path, token)
}

func (s *chatGPTCredentialSource) shouldRefresh(token auth.TokenStore) bool {
	if token.ExpiresAt.IsZero() {
		return false
	}
	return !token.ExpiresAt.After(s.now().Add(s.refreshWindow))
}

func (s *chatGPTCredentialSource) refreshShared(ctx context.Context, path string, token auth.TokenStore) (requestCredentials, error) {
	s.mu.Lock()
	if s.inFlight != nil {
		done := s.inFlight
		s.mu.Unlock()
		select {
		case <-done:
			return s.lastResult()
		case <-ctx.Done():
			return requestCredentials{}, ctx.Err()
		}
	}
	done := make(chan refreshResult)
	s.inFlight = done
	s.mu.Unlock()

	result := s.refresh(ctx, path, token)

	s.mu.Lock()
	s.lastRefreshResult = result
	s.inFlight = nil
	close(done)
	s.mu.Unlock()
	return result.credentials, result.err
}

func (s *chatGPTCredentialSource) lastResult() (requestCredentials, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastRefreshResult.credentials, s.lastRefreshResult.err
}

func (s *chatGPTCredentialSource) refresh(ctx context.Context, path string, token auth.TokenStore) refreshResult {
	client, err := s.oauthClient(auth.OAuthConfig{Issuer: token.Issuer})
	if err != nil {
		return refreshResult{err: fmt.Errorf("chatgpt auth refresh unavailable: %w", err)}
	}
	tokens, err := client.Refresh(ctx, token.RefreshToken)
	if err != nil {
		if errors.Is(err, auth.ErrOAuthInvalidGrant) {
			if deleteErr := auth.DeleteTokenStore(path); deleteErr != nil {
				return refreshResult{err: fmt.Errorf("chatgpt auth refresh revoked; run :connect openai: %w", deleteErr)}
			}
			return refreshResult{err: errors.New("chatgpt auth refresh revoked; run :connect openai")}
		}
		return refreshResult{err: fmt.Errorf("chatgpt auth refresh failed: %w", err)}
	}
	if err := ctx.Err(); err != nil {
		return refreshResult{err: err}
	}
	accessToken := strings.TrimSpace(tokens.AccessToken)
	if accessToken == "" {
		return refreshResult{err: errors.New("chatgpt auth refresh returned no access token; run :connect openai")}
	}
	now := s.now().UTC()
	updated := token
	updated.AccessToken = accessToken
	if refreshToken := strings.TrimSpace(tokens.RefreshToken); refreshToken != "" {
		updated.RefreshToken = refreshToken
	}
	updated.ExpiresAt = now.Add(time.Duration(tokens.ExpiresIn) * time.Second)
	updated.LastRefresh = now
	if strings.TrimSpace(tokens.AccountID) != "" {
		updated.AccountID = tokens.AccountID
	}
	if err := auth.SaveTokenStore(path, updated); err != nil {
		return refreshResult{err: fmt.Errorf("persist refreshed chatgpt auth: %w", err)}
	}
	return refreshResult{credentials: requestCredentials{BearerToken: accessToken, AccountID: strings.TrimSpace(updated.AccountID)}}
}

func newCredentialSource(provider, apiKey string, authStorePath func() (string, error), now func() time.Time, oauthClient oauthRefreshClientFactory) credentialSource {
	if provider == config.ProviderChatGPT {
		return &chatGPTCredentialSource{authStorePath: authStorePath, now: now, oauthClient: oauthClient, refreshWindow: chatGPTRefreshSafetyWindow}
	}
	return apiKeyCredentialSource{apiKey: apiKey}
}
