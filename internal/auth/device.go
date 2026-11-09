package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

const (
	defaultDevicePollInterval          = 5 * time.Second
	defaultDevicePollSlowDownIncrement = 5 * time.Second
	defaultDevicePollTimeout           = 10 * time.Minute

	devicePollStatusAuthorized = "authorized"
	devicePollStatusPending    = "pending"
	devicePollStatusSlowDown   = "slow_down"
)

type DeviceStart struct {
	DeviceAuthID    string
	UserCode        string
	VerificationURI string
}

type DeviceAuthorization struct {
	AuthorizationCode string
	CodeVerifier      string
	CodeChallenge     string
}

type DevicePollOptions struct {
	InitialInterval   time.Duration
	SlowDownIncrement time.Duration
	Timeout           time.Duration
	Wait              func(context.Context, time.Duration) error
}

type deviceJSONRequest struct {
	Path    string
	Action  string
	Payload map[string]string
}

func (c *OAuthClient) StartDevice(ctx context.Context) (DeviceStart, error) {
	var response struct {
		DeviceAuthID string `json:"device_auth_id"`
		UserCode     string `json:"user_code"`
	}
	responseBody, err := c.postDeviceJSON(ctx, deviceJSONRequest{
		Path:    deviceUserCodePath,
		Action:  "device user code",
		Payload: map[string]string{"client_id": c.clientID},
	})
	if err != nil {
		return DeviceStart{}, err
	}
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return DeviceStart{}, fmt.Errorf("decode device user code response: %w", err)
	}
	return DeviceStart{
		DeviceAuthID:    response.DeviceAuthID,
		UserCode:        response.UserCode,
		VerificationURI: c.endpointString("/codex/device"),
	}, nil
}

func (c *OAuthClient) PollDevice(ctx context.Context, start DeviceStart) (DeviceAuthorization, error) {
	return c.PollDeviceWithOptions(ctx, start, DevicePollOptions{})
}

func (c *OAuthClient) PollDeviceWithOptions(ctx context.Context, start DeviceStart, options DevicePollOptions) (DeviceAuthorization, error) {
	options = options.withDefaults()
	pollCtx, cancel := context.WithTimeout(ctx, options.Timeout)
	defer cancel()
	interval := options.InitialInterval
	for {
		if err := pollCtx.Err(); err != nil {
			return DeviceAuthorization{}, fmt.Errorf("device authorization polling stopped: %w", err)
		}
		authorization, status, err := c.pollDeviceOnce(pollCtx, start)
		if err != nil {
			return DeviceAuthorization{}, err
		}
		switch status {
		case devicePollStatusAuthorized:
			return authorization, nil
		case devicePollStatusSlowDown:
			interval += options.SlowDownIncrement
			if err := options.Wait(pollCtx, interval); err != nil {
				return DeviceAuthorization{}, fmt.Errorf("device authorization polling stopped: %w", err)
			}
		case devicePollStatusPending:
			if err := options.Wait(pollCtx, interval); err != nil {
				return DeviceAuthorization{}, fmt.Errorf("device authorization polling stopped: %w", err)
			}
		default:
			return DeviceAuthorization{}, fmt.Errorf("device authorization returned status %q; restart :connect openai", sanitizeOAuthErrorText(status))
		}
	}
}

func (options DevicePollOptions) withDefaults() DevicePollOptions {
	if options.InitialInterval <= 0 {
		options.InitialInterval = defaultDevicePollInterval
	}
	if options.SlowDownIncrement <= 0 {
		options.SlowDownIncrement = defaultDevicePollSlowDownIncrement
	}
	if options.Timeout <= 0 {
		options.Timeout = defaultDevicePollTimeout
	}
	if options.Wait == nil {
		options.Wait = waitDevicePollInterval
	}
	return options
}

func waitDevicePollInterval(ctx context.Context, interval time.Duration) error {
	timer := time.NewTimer(interval)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *OAuthClient) pollDeviceOnce(ctx context.Context, start DeviceStart) (DeviceAuthorization, string, error) {
	var response struct {
		Status            string `json:"status"`
		AuthorizationCode string `json:"authorization_code"`
		CodeVerifier      string `json:"code_verifier"`
		CodeChallenge     string `json:"code_challenge"`
	}
	responseBody, err := c.postDeviceJSON(ctx, deviceJSONRequest{
		Path:    deviceTokenPath,
		Action:  "device token poll",
		Payload: map[string]string{"device_auth_id": start.DeviceAuthID, "user_code": start.UserCode},
	})
	if err != nil {
		return DeviceAuthorization{}, "", err
	}
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return DeviceAuthorization{}, "", fmt.Errorf("decode device token poll response: %w", err)
	}
	switch response.Status {
	case "authorized", "success", "complete", "":
		if response.AuthorizationCode == "" {
			return DeviceAuthorization{}, "", fmt.Errorf("device token poll missing authorization_code; restart :connect openai")
		}
		if response.CodeVerifier == "" {
			return DeviceAuthorization{}, "", fmt.Errorf("device token poll missing code_verifier; restart :connect openai")
		}
		return DeviceAuthorization{
			AuthorizationCode: response.AuthorizationCode,
			CodeVerifier:      response.CodeVerifier,
			CodeChallenge:     response.CodeChallenge,
		}, devicePollStatusAuthorized, nil
	case "pending", "authorization_pending":
		return DeviceAuthorization{}, devicePollStatusPending, nil
	case "slow_down":
		return DeviceAuthorization{}, devicePollStatusSlowDown, nil
	case "denied", "declined", "expired", "access_denied":
		return DeviceAuthorization{}, "", fmt.Errorf("device authorization denied or expired; restart :connect openai")
	default:
		return DeviceAuthorization{}, "", fmt.Errorf("device authorization returned status %q; restart :connect openai", sanitizeOAuthErrorText(response.Status))
	}
}

func (c *OAuthClient) CompleteDevice(ctx context.Context, authorization DeviceAuthorization) (TokenSet, error) {
	form := c.codeExchangeForm(CodeExchange{
		Code:        authorization.AuthorizationCode,
		RedirectURI: c.endpointString("/deviceauth/callback"),
		Verifier:    authorization.CodeVerifier,
	})
	return c.postTokenForm(ctx, form, "device token exchange")
}

func (c *OAuthClient) postDeviceJSON(ctx context.Context, request deviceJSONRequest) ([]byte, error) {
	body, err := json.Marshal(request.Payload)
	if err != nil {
		return nil, fmt.Errorf("encode %s request: %w", request.Action, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpointString(request.Path), bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("build %s request: %w", request.Action, err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s request: %w", request.Action, err)
	}
	defer func() { _ = resp.Body.Close() }()
	responseBody, err := readOAuthBody(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read %s response: %w", request.Action, err)
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("%s failed: HTTP %d: %s", request.Action, resp.StatusCode, sanitizeOAuthErrorText(string(responseBody)))
	}
	return responseBody, nil
}
