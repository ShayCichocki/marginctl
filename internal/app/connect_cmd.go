package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ShayCichocki/marginctl/internal/auth"
	"github.com/ShayCichocki/marginctl/internal/config"
)

func (m *Model) startBrowserConnectCmd(ctx context.Context, attemptID int) tea.Cmd {
	deps := m.connectDeps
	return func() tea.Msg {
		client, err := deps.newOAuthClient(deps.oauthConfig)
		if err != nil {
			return connectBrowserReadyMsg{attemptID: attemptID, err: fmt.Errorf("start OpenAI browser auth: %w", err)}
		}
		listener, port, err := listenConnectCallback()
		if err != nil {
			return connectBrowserReadyMsg{attemptID: attemptID, err: err}
		}
		pkce, err := client.GeneratePKCE()
		if err != nil {
			closeListener(listener)
			return connectBrowserReadyMsg{attemptID: attemptID, err: err}
		}
		state, err := randomConnectState()
		if err != nil {
			closeListener(listener)
			return connectBrowserReadyMsg{attemptID: attemptID, err: err}
		}
		redirectURI := fmt.Sprintf("http://localhost:%d%s", port, connectCallbackPath)
		url, err := client.AuthorizeURL(redirectURI, state, pkce)
		if err != nil {
			closeListener(listener)
			return connectBrowserReadyMsg{attemptID: attemptID, err: err}
		}
		if deps.openBrowser != nil {
			_ = deps.openBrowser(url)
		}
		return connectBrowserReadyMsg{attemptID: attemptID, ctx: ctx, client: client, listener: listener, redirectURI: redirectURI, state: state, verifier: pkce.Verifier, url: url}
	}
}

func (m *Model) waitBrowserConnectCmd(ready connectBrowserReadyMsg) tea.Cmd {
	deps := m.connectDeps
	return func() tea.Msg {
		code, state, err := waitForBrowserCallback(ready.ctx, ready.listener)
		if err != nil {
			return connectResultMsg{attemptID: ready.attemptID, err: err}
		}
		if err := ready.client.ValidateCallbackState(ready.state, state); err != nil {
			return connectResultMsg{attemptID: ready.attemptID, err: err}
		}
		tokens, err := ready.client.ExchangeCode(ready.ctx, auth.CodeExchange{Code: code, RedirectURI: ready.redirectURI, Verifier: ready.verifier})
		if err != nil {
			return connectResultMsg{attemptID: ready.attemptID, err: err}
		}
		return finishConnectAuth(ready.ctx, deps, ready.attemptID, tokens)
	}
}

func (m *Model) startDeviceConnectCmd(ctx context.Context, attemptID int) tea.Cmd {
	deps := m.connectDeps
	return func() tea.Msg {
		client, err := deps.newOAuthClient(deps.oauthConfig)
		if err != nil {
			return connectDeviceReadyMsg{attemptID: attemptID, err: fmt.Errorf("start OpenAI device auth: %w", err)}
		}
		start, err := client.StartDevice(ctx)
		if err != nil {
			return connectDeviceReadyMsg{attemptID: attemptID, err: err}
		}
		return connectDeviceReadyMsg{attemptID: attemptID, ctx: ctx, client: client, start: start}
	}
}

func (m *Model) waitDeviceConnectCmd(ready connectDeviceReadyMsg) tea.Cmd {
	deps := m.connectDeps
	return func() tea.Msg {
		authorization, err := ready.client.PollDeviceWithOptions(ready.ctx, ready.start, deps.devicePollOptions)
		if err != nil {
			return connectResultMsg{attemptID: ready.attemptID, err: err}
		}
		tokens, err := ready.client.CompleteDevice(ready.ctx, authorization)
		if err != nil {
			return connectResultMsg{attemptID: ready.attemptID, err: err}
		}
		return finishConnectAuth(ready.ctx, deps, ready.attemptID, tokens)
	}
}

func finishConnectAuth(ctx context.Context, deps connectDeps, attemptID int, tokens auth.TokenSet) tea.Msg {
	if err := ctx.Err(); err != nil {
		return connectResultMsg{attemptID: attemptID, err: err}
	}
	storePath, err := deps.authStorePath()
	if err != nil {
		return connectResultMsg{attemptID: attemptID, err: err}
	}
	if err := ctx.Err(); err != nil {
		return connectResultMsg{attemptID: attemptID, err: err}
	}
	now := time.Now().UTC()
	store := auth.TokenStore{
		AuthMode:     auth.AuthModeChatGPT,
		Issuer:       connectIssuer(deps.oauthConfig),
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		ExpiresAt:    now.Add(time.Duration(tokens.ExpiresIn) * time.Second),
		LastRefresh:  now,
		AccountID:    tokens.AccountID,
	}
	if err := auth.SaveTokenStore(storePath, store); err != nil {
		return connectResultMsg{attemptID: attemptID, err: err}
	}
	if err := ctx.Err(); err != nil {
		return connectResultMsg{attemptID: attemptID, err: err}
	}
	envProvider, _ := config.EnvProviderOverride()
	activation, err := auth.ActivateChatGPT(deps.writeProvider, envProvider)
	if err != nil {
		return connectResultMsg{attemptID: attemptID, err: err}
	}
	return connectResultMsg{attemptID: attemptID, provider: config.ProviderChatGPT, result: activation, storePath: storePath}
}

func connectIssuer(oauthConfig auth.OAuthConfig) string {
	if strings.TrimSpace(oauthConfig.Issuer) == "" {
		return auth.DefaultOAuthIssuer
	}
	return strings.TrimRight(oauthConfig.Issuer, "/")
}
