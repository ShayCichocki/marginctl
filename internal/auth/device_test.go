package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestDeviceStartPostsClientIDAndExposesVerificationURL(t *testing.T) {
	// Given
	observed := make(chan map[string]string, 1)
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/accounts/deviceauth/usercode" {
			t.Fatalf("path = %q, want usercode endpoint", r.URL.Path)
		}
		observed <- readJSONMap(t, r)
		_, _ = w.Write([]byte(`{"device_auth_id":"device-1","user_code":"USER-CODE"}`))
	}))
	defer issuer.Close()
	client := mustOAuthClient(t, issuer.URL)

	// When
	start, err := client.StartDevice(context.Background())

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if start.VerificationURI != issuer.URL+"/codex/device" || start.UserCode != "USER-CODE" || start.DeviceAuthID != "device-1" {
		t.Fatalf("device start = %+v, want issuer verification URL and fake code", start)
	}
	got := <-observed
	t.Logf("device usercode observed method=POST path=/api/accounts/deviceauth/usercode body=%v", got)
	if got["client_id"] != DefaultOAuthClientID || len(got) != 1 {
		t.Fatalf("usercode JSON = %v, want only client_id", got)
	}
}

func TestDevicePollAndCompleteUseReturnedAuthorizationCodeAndVerifier(t *testing.T) {
	// Given
	observedPoll := make(chan map[string]string, 1)
	observedToken := make(chan formObservation, 1)
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/token":
			observedPoll <- readJSONMap(t, r)
			_, _ = w.Write([]byte(`{"status":"authorized","authorization_code":"device-code","code_verifier":"device-verifier","code_challenge":"device-challenge"}`))
		case "/oauth/token":
			observedToken <- readFormObservation(t, r)
			_, _ = w.Write([]byte(`{"access_token":"access","refresh_token":"refresh","id_token":"id"}`))
		default:
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
	}))
	defer issuer.Close()
	client := mustOAuthClient(t, issuer.URL)

	// When
	authorization, err := client.PollDevice(context.Background(), DeviceStart{DeviceAuthID: "device-1", UserCode: "USER-CODE"})
	if err != nil {
		t.Fatal(err)
	}
	tokens, err := client.CompleteDevice(context.Background(), authorization)

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if tokens.AccessToken != "access" || tokens.RefreshToken != "refresh" {
		t.Fatalf("tokens = %+v, want device exchange tokens", tokens)
	}
	pollBody := <-observedPoll
	t.Logf("device poll observed method=POST path=/api/accounts/deviceauth/token body=%v", pollBody)
	if pollBody["device_auth_id"] != "device-1" || pollBody["user_code"] != "USER-CODE" || len(pollBody) != 2 {
		t.Fatalf("poll JSON = %v, want device_auth_id and user_code", pollBody)
	}
	tokenPost := <-observedToken
	t.Logf("device final token exchange observed method=%s path=/oauth/token content_type=%s body=%s", tokenPost.Method, tokenPost.ContentType, tokenPost.Form.Encode())
	if tokenPost.Method != http.MethodPost || tokenPost.ContentType != "application/x-www-form-urlencoded" {
		t.Fatalf("method/content-type = %s/%s, want POST application/x-www-form-urlencoded", tokenPost.Method, tokenPost.ContentType)
	}
	assertForm(t, tokenPost.Form, map[string]string{
		"grant_type":    "authorization_code",
		"client_id":     DefaultOAuthClientID,
		"code":          "device-code",
		"redirect_uri":  issuer.URL + "/deviceauth/callback",
		"code_verifier": "device-verifier",
	})
}

func TestDevicePollContinuesUntilAuthorized_whenAuthorizationPending(t *testing.T) {
	// Given
	pollCalls := 0
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/accounts/deviceauth/token" {
			t.Fatalf("unexpected path %q", r.URL.Path)
		}
		pollCalls++
		switch pollCalls {
		case 1:
			_, _ = w.Write([]byte(`{"status":"pending"}`))
		case 2:
			_, _ = w.Write([]byte(`{"status":"authorization_pending"}`))
		default:
			_, _ = w.Write([]byte(`{"status":"authorized","authorization_code":"device-code","code_verifier":"device-verifier"}`))
		}
	}))
	defer issuer.Close()
	client := mustOAuthClient(t, issuer.URL)
	options := DevicePollOptions{InitialInterval: time.Second, Wait: instantDevicePollWait}

	// When
	authorization, err := client.PollDeviceWithOptions(context.Background(), DeviceStart{DeviceAuthID: "device-1", UserCode: "USER-CODE"}, options)

	// Then
	if err != nil {
		t.Fatal(err)
	}
	if authorization.AuthorizationCode != "device-code" || authorization.CodeVerifier != "device-verifier" {
		t.Fatalf("authorization = %+v, want authorized device code", authorization)
	}
	if pollCalls != 3 {
		t.Fatalf("poll calls = %d, want pending, authorization_pending, then authorized", pollCalls)
	}
}

func TestDevicePollIncreasesInterval_whenSlowDownReturned(t *testing.T) {
	// Given
	pollCalls := 0
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pollCalls++
		switch pollCalls {
		case 1:
			_, _ = w.Write([]byte(`{"status":"slow_down"}`))
		case 2:
			_, _ = w.Write([]byte(`{"status":"pending"}`))
		default:
			_, _ = w.Write([]byte(`{"status":"authorized","authorization_code":"device-code","code_verifier":"device-verifier"}`))
		}
	}))
	defer issuer.Close()
	client := mustOAuthClient(t, issuer.URL)
	waits := []time.Duration{}
	options := DevicePollOptions{
		InitialInterval:   10 * time.Second,
		SlowDownIncrement: 5 * time.Second,
		Wait: func(ctx context.Context, interval time.Duration) error {
			waits = append(waits, interval)
			return nil
		},
	}

	// When
	_, err := client.PollDeviceWithOptions(context.Background(), DeviceStart{DeviceAuthID: "device-1", UserCode: "USER-CODE"}, options)

	// Then
	if err != nil {
		t.Fatal(err)
	}
	want := []time.Duration{15 * time.Second, 15 * time.Second}
	if len(waits) != len(want) || waits[0] != want[0] || waits[1] != want[1] {
		t.Fatalf("waits = %v, want slow_down-adjusted waits %v", waits, want)
	}
}

func TestDevicePollReturnsDeadlineExceeded_whenPollingTimesOut(t *testing.T) {
	// Given
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"pending"}`))
	}))
	defer issuer.Close()
	client := mustOAuthClient(t, issuer.URL)
	options := DevicePollOptions{
		Timeout:         time.Nanosecond,
		InitialInterval: time.Second,
		Wait: func(ctx context.Context, interval time.Duration) error {
			<-ctx.Done()
			return ctx.Err()
		},
	}

	// When
	_, err := client.PollDeviceWithOptions(context.Background(), DeviceStart{DeviceAuthID: "device-1", UserCode: "USER-CODE"}, options)

	// Then
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
}

func TestDevicePollReturnsCanceled_whenContextCanceledDuringWait(t *testing.T) {
	// Given
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"status":"pending"}`))
	}))
	defer issuer.Close()
	client := mustOAuthClient(t, issuer.URL)
	ctx, cancel := context.WithCancel(context.Background())
	options := DevicePollOptions{
		InitialInterval: time.Second,
		Wait: func(ctx context.Context, interval time.Duration) error {
			cancel()
			<-ctx.Done()
			return ctx.Err()
		},
	}

	// When
	_, err := client.PollDeviceWithOptions(ctx, DeviceStart{DeviceAuthID: "device-1", UserCode: "USER-CODE"}, options)

	// Then
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want canceled", err)
	}
}

func TestDevicePollReturnsBoundedStaleStateError_whenPendingOrDenied(t *testing.T) {
	// Given
	tests := []struct {
		name string
		body string
		want string
	}{
		{name: "denied", body: `{"status":"denied"}`, want: "device authorization denied"},
		{name: "malformed", body: `{"status":"authorized"}`, want: "missing authorization_code"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(tt.body))
			}))
			defer issuer.Close()
			client := mustOAuthClient(t, issuer.URL)

			// When
			_, err := client.PollDevice(context.Background(), DeviceStart{DeviceAuthID: "device-1", UserCode: "USER-CODE"})

			// Then
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("err = %v, want %q", err, tt.want)
			}
		})
	}
}

func instantDevicePollWait(ctx context.Context, interval time.Duration) error {
	return nil
}

func readJSONMap(t *testing.T, r *http.Request) map[string]string {
	t.Helper()
	if r.Method != http.MethodPost {
		t.Fatalf("method = %s, want POST", r.Method)
	}
	if r.Header.Get("Content-Type") != "application/json" {
		t.Fatalf("content-type = %q, want application/json", r.Header.Get("Content-Type"))
	}
	var body map[string]string
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body
}
