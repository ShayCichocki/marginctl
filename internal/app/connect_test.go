package app

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ShayCichocki/marginctl/internal/auth"
	"github.com/ShayCichocki/marginctl/internal/config"
)

func TestConnectBrowserWritesAuthStore_whenFakeOAuthCallbackCompletes(t *testing.T) {
	// Given
	m, authPath, opened := newConnectTestModel(t, newFakeOAuthIssuer(t))

	// When
	m, cmd := enterConnectCommand(m, "connect openai browser")
	ready := runCmd[connectBrowserReadyMsg](t, cmd)
	m, waitCmd := updateCmd(m, ready)
	if !strings.Contains(m.connectPromptLine(), ready.url) {
		t.Fatalf("browser URL prompt = %q, want %q", m.connectPromptLine(), ready.url)
	}
	if len(*opened) != 1 || (*opened)[0] != ready.url {
		t.Fatalf("opened URLs = %v, want best-effort browser launch with auth URL", *opened)
	}
	resultCh := runCmdAsync(waitCmd)
	callbackURL := ready.redirectURI + "?code=browser-code&state=" + url.QueryEscape(ready.state)
	resp, err := http.Get(callbackURL)
	if err != nil {
		t.Fatalf("callback GET: %v", err)
	}
	_ = resp.Body.Close()
	result := (<-resultCh).(connectResultMsg)
	m = update(m, result)

	// Then
	store := readTokenStore(t, authPath)
	if store.AuthMode != auth.AuthModeChatGPT || store.AccessToken != "fake-access-token-browser" || store.RefreshToken != "fake-refresh-token-browser" {
		t.Fatalf("store = %+v, want browser ChatGPT tokens", store)
	}
	if m.connect.active() || !strings.Contains(m.notice, "Connected OpenAI") || m.noticeIsErr {
		t.Fatalf("connect state/notice = active:%v notice:%q err:%v", m.connect.active(), m.notice, m.noticeIsErr)
	}
}

func TestConnectBrowserUsesRegisteredCodexRedirectURI(t *testing.T) {
	// Given
	m, _, _ := newConnectTestModel(t, newFakeOAuthIssuer(t))

	// When
	_, cmd := enterConnectCommand(m, "connect openai browser")
	ready := runCmd[connectBrowserReadyMsg](t, cmd)
	defer closeListener(ready.listener)

	// Then
	if ready.redirectURI != "http://localhost:1455/auth/callback" {
		t.Fatalf("redirect URI = %q, want registered Codex redirect URI", ready.redirectURI)
	}
	parsed, err := url.Parse(ready.url)
	if err != nil {
		t.Fatal(err)
	}
	if got := parsed.Query().Get("redirect_uri"); got != ready.redirectURI {
		t.Fatalf("authorize redirect_uri = %q, want %q", got, ready.redirectURI)
	}
}

func TestConnectDeviceWritesAuthStore_whenFakeDeviceEndpointsAuthorize(t *testing.T) {
	// Given
	m, authPath, _ := newConnectTestModel(t, newFakeOAuthIssuer(t))

	// When
	m, cmd := enterConnectCommand(m, "connect openai device")
	ready := runCmd[connectDeviceReadyMsg](t, cmd)
	m, waitCmd := updateCmd(m, ready)
	if !strings.Contains(m.connectPromptLine(), "USER-CODE") || !strings.Contains(m.connectPromptLine(), "/codex/device") {
		t.Fatalf("device prompt = %q, want user code and verification URL", m.connectPromptLine())
	}
	result := runCmd[connectResultMsg](t, waitCmd)
	m = update(m, result)

	// Then
	store := readTokenStore(t, authPath)
	if store.AuthMode != auth.AuthModeChatGPT || store.AccessToken != "fake-access-token-device" || store.RefreshToken != "fake-refresh-token-device" {
		t.Fatalf("store = %+v, want device ChatGPT tokens", store)
	}
	if m.connect.active() || m.noticeIsErr {
		t.Fatalf("connect state/notice = active:%v notice:%q err:%v", m.connect.active(), m.notice, m.noticeIsErr)
	}
}

func TestConnectDeviceKeepsPromptVisibleAndWritesAuthStore_whenAuthorizationInitiallyPending(t *testing.T) {
	// Given
	m, authPath, _ := newConnectTestModel(t, newPendingThenDeviceOAuthIssuer(t))
	m.connectDeps.devicePollOptions = auth.DevicePollOptions{InitialInterval: time.Second, Wait: func(ctx context.Context, interval time.Duration) error { return nil }}

	// When
	m, cmd := enterConnectCommand(m, "connect openai --device")
	ready := runCmd[connectDeviceReadyMsg](t, cmd)
	m, waitCmd := updateCmd(m, ready)
	prompt := m.connectPromptLine()
	result := runCmd[connectResultMsg](t, waitCmd)
	m = update(m, result)

	// Then
	if !strings.Contains(prompt, "USER-CODE") || !strings.Contains(prompt, "/codex/device") {
		t.Fatalf("device prompt = %q, want code visible while polling", prompt)
	}
	store := readTokenStore(t, authPath)
	if store.AccessToken != "fake-access-token-device" || store.RefreshToken != "fake-refresh-token-device" {
		t.Fatalf("store = %+v, want device tokens after pending then authorized", store)
	}
}

func TestConnectDeviceEscCancelsPollingWithoutWritingAuthStore(t *testing.T) {
	// Given
	m, authPath, _ := newConnectTestModel(t, newAlwaysPendingDeviceOAuthIssuer(t))
	waiting := make(chan struct{})
	m.connectDeps.devicePollOptions = auth.DevicePollOptions{
		InitialInterval: time.Second,
		Wait: func(ctx context.Context, interval time.Duration) error {
			close(waiting)
			<-ctx.Done()
			return ctx.Err()
		},
	}
	m, cmd := enterConnectCommand(m, "connect openai device")
	ready := runCmd[connectDeviceReadyMsg](t, cmd)
	m, waitCmd := updateCmd(m, ready)
	resultCh := runCmdAsync(waitCmd)
	<-waiting

	// When
	m = pressEsc(m)
	result := (<-resultCh).(connectResultMsg)
	m = update(m, result)

	// Then
	if !errors.Is(result.err, context.Canceled) {
		t.Fatalf("result err = %v, want context canceled", result.err)
	}
	if m.connect.active() || m.notice != "Connect cancelled" || m.noticeIsErr {
		t.Fatalf("cancel notice/state = active:%v notice:%q err:%v", m.connect.active(), m.notice, m.noticeIsErr)
	}
	if _, err := os.Stat(authPath); !os.IsNotExist(err) {
		t.Fatalf("cancelled device polling wrote auth store: stat err=%v", err)
	}
}

func TestConnectDeviceStarts_whenOpenAIFlagDeviceProvided(t *testing.T) {
	// Given
	m, _, _ := newConnectTestModel(t, newFakeOAuthIssuer(t))

	// When
	m, cmd := enterConnectCommand(m, "connect openai --device")
	ready := runCmd[connectDeviceReadyMsg](t, cmd)
	m, _ = updateCmd(m, ready)

	// Then
	if !strings.Contains(m.connectPromptLine(), "USER-CODE") || !strings.Contains(m.connectPromptLine(), "/codex/device") {
		t.Fatalf("device prompt = %q, want user code and verification URL", m.connectPromptLine())
	}
}

func TestConnectEscCancelsBrowserFlowAndSuppressesStaleCallback(t *testing.T) {
	// Given
	m, authPath, _ := newConnectTestModel(t, newFakeOAuthIssuer(t))
	m, cmd := enterConnectCommand(m, "connect openai browser")
	ready := runCmd[connectBrowserReadyMsg](t, cmd)
	m, waitCmd := updateCmd(m, ready)
	resultCh := runCmdAsync(waitCmd)

	// When
	m = pressEsc(m)
	_, _ = http.Get(ready.redirectURI + "?code=browser-code&state=" + url.QueryEscape(ready.state))
	result := (<-resultCh).(connectResultMsg)
	m = update(m, result)
	m = update(m, connectResultMsg{attemptID: ready.attemptID, provider: config.ProviderChatGPT, result: auth.ActivationResult{Notice: "stale"}})

	// Then
	if m.connect.active() || m.notice != "Connect cancelled" || m.noticeIsErr {
		t.Fatalf("cancel notice/state = active:%v notice:%q err:%v", m.connect.active(), m.notice, m.noticeIsErr)
	}
	if _, err := os.Stat(authPath); !os.IsNotExist(err) {
		t.Fatalf("cancelled connect wrote auth store: stat err=%v", err)
	}
}

func TestConnectSuppressesStaleResult_whenNewerAttemptStarts(t *testing.T) {
	// Given
	m, authPath, _ := newConnectTestModel(t, newFakeOAuthIssuer(t))
	m, firstCmd := enterConnectCommand(m, "connect openai device")
	firstReady := runCmd[connectDeviceReadyMsg](t, firstCmd)
	firstAttempt := firstReady.attemptID
	m, _ = updateCmd(m, firstReady)
	m, secondCmd := enterConnectCommand(m, "connect openai device")
	secondReady := runCmd[connectDeviceReadyMsg](t, secondCmd)
	m, secondWait := updateCmd(m, secondReady)

	// When
	m = update(m, connectResultMsg{attemptID: firstAttempt, provider: config.ProviderChatGPT, result: auth.ActivationResult{Notice: "stale"}})
	if _, err := os.Stat(authPath); !os.IsNotExist(err) {
		t.Fatalf("stale result wrote auth store before current attempt finished: %v", err)
	}
	update(m, runCmd[connectResultMsg](t, secondWait))

	// Then
	store := readTokenStore(t, authPath)
	if store.AccessToken != "fake-access-token-device" {
		t.Fatalf("store access token = %q, want current attempt token", store.AccessToken)
	}
}

func TestConnectCancelDuringAuthPersistDoesNotWriteStore(t *testing.T) {
	// Given
	m, authPath, _ := newConnectTestModel(t, newFakeOAuthIssuer(t))
	startedPersist := make(chan struct{})
	releasePersist := make(chan struct{})
	m.connectDeps.authStorePath = func() (string, error) {
		close(startedPersist)
		<-releasePersist
		return authPath, nil
	}
	m, cmd := enterConnectCommand(m, "connect openai device")
	ready := runCmd[connectDeviceReadyMsg](t, cmd)
	m, waitCmd := updateCmd(m, ready)
	resultCh := runCmdAsync(waitCmd)
	<-startedPersist

	// When
	m = pressEsc(m)
	close(releasePersist)
	result := (<-resultCh).(connectResultMsg)
	m = update(m, result)

	// Then
	if m.notice != "Connect cancelled" || m.noticeIsErr {
		t.Fatalf("notice = %q err=%v, want cancellation notice", m.notice, m.noticeIsErr)
	}
	if _, err := os.Stat(authPath); !os.IsNotExist(err) {
		t.Fatalf("cancelled connect wrote auth store during finalization: stat err=%v", err)
	}
}

func TestConnectSupersededAttemptCannotOverwriteNewerAuthStore(t *testing.T) {
	// Given
	m, authPath, _ := newConnectTestModel(t, newCountingDeviceOAuthIssuer(t))
	firstPersistStarted := make(chan struct{})
	releaseFirstPersist := make(chan struct{})
	var pathCalls int32
	m.connectDeps.authStorePath = func() (string, error) {
		if atomic.AddInt32(&pathCalls, 1) == 1 {
			close(firstPersistStarted)
			<-releaseFirstPersist
		}
		return authPath, nil
	}
	m, firstCmd := enterConnectCommand(m, "connect openai device")
	firstReady := runCmd[connectDeviceReadyMsg](t, firstCmd)
	m, firstWait := updateCmd(m, firstReady)
	firstResultCh := runCmdAsync(firstWait)
	<-firstPersistStarted

	// When
	m, secondCmd := enterConnectCommand(m, "connect openai device")
	secondReady := runCmd[connectDeviceReadyMsg](t, secondCmd)
	m, secondWait := updateCmd(m, secondReady)
	m = update(m, runCmd[connectResultMsg](t, secondWait))
	close(releaseFirstPersist)
	m = update(m, (<-firstResultCh).(connectResultMsg))

	// Then
	store := readTokenStore(t, authPath)
	if store.AccessToken != "fake-access-token-2" {
		t.Fatalf("store access token = %q, want newer attempt token", store.AccessToken)
	}
}

func TestConnectBrowserShowsDeviceCodeGuidance_whenRegisteredPortUnavailable(t *testing.T) {
	// Given
	occupied := occupyLoopbackPort(t, 1455)
	defer occupied.Close()
	m, _, _ := newConnectTestModel(t, newFakeOAuthIssuer(t))

	// When
	m, cmd := enterConnectCommand(m, "connect openai browser")
	ready := runCmd[connectBrowserReadyMsg](t, cmd)
	m = update(m, ready)

	// Then
	if ready.err == nil {
		closeListener(ready.listener)
		t.Fatal("browser ready err = nil, want registered port unavailable")
	}
	if !m.noticeIsErr || !strings.Contains(m.notice, "port unavailable") || !strings.Contains(m.notice, "device code") {
		t.Fatalf("notice = %q err=%v, want actionable port unavailable device-code guidance", m.notice, m.noticeIsErr)
	}
}

func TestConnectBrowserShowsDeviceCodeGuidance_whenCallbackPortUnavailable(t *testing.T) {
	// Given
	port1455 := occupyLoopbackPort(t, 1455)
	defer port1455.Close()
	m, _, _ := newConnectTestModel(t, newFakeOAuthIssuer(t))

	// When
	m, cmd := enterConnectCommand(m, "connect openai browser")
	ready := runCmd[connectBrowserReadyMsg](t, cmd)
	m = update(m, ready)

	// Then
	if !m.noticeIsErr || !strings.Contains(m.notice, "port unavailable") || !strings.Contains(m.notice, "device code") {
		t.Fatalf("notice = %q err=%v, want actionable port unavailable device-code guidance", m.notice, m.noticeIsErr)
	}
}
