package app

import (
	"context"
	"net"

	"github.com/ShayCichocki/marginctl/internal/auth"
	"github.com/ShayCichocki/marginctl/internal/config"
)

const (
	connectStepIdle = iota
	connectStepProvider
	connectStepMethod
	connectStepBrowserWait
	connectStepDeviceWait
)

const (
	connectProviderOpenAI = "openai"
	connectMethodBrowser  = "browser"
	connectMethodDevice   = "device"
	connectCallbackPath   = "/auth/callback"
)

var connectLoopbackPorts = []int{1455}

type connectState struct {
	step            int
	provider        string
	method          string
	buffer          string
	authorizeURL    string
	deviceCode      string
	verificationURL string
}

type connectDeps struct {
	newOAuthClient    func(auth.OAuthConfig) (*auth.OAuthClient, error)
	authStorePath     func() (string, error)
	writeProvider     auth.ProviderMetadataWriter
	openBrowser       func(string) error
	oauthConfig       auth.OAuthConfig
	devicePollOptions auth.DevicePollOptions
}

type connectBrowserReadyMsg struct {
	attemptID   int
	ctx         context.Context
	client      *auth.OAuthClient
	listener    net.Listener
	redirectURI string
	state       string
	verifier    string
	url         string
	err         error
}

type connectDeviceReadyMsg struct {
	attemptID int
	ctx       context.Context
	client    *auth.OAuthClient
	start     auth.DeviceStart
	err       error
}

type connectResultMsg struct {
	attemptID int
	provider  string
	result    auth.ActivationResult
	storePath string
	err       error
}

func defaultConnectDeps() connectDeps {
	return connectDeps{
		newOAuthClient: auth.NewOAuthClient,
		authStorePath:  auth.AuthStorePath,
		writeProvider:  config.SaveProviderMetadataResult,
		openBrowser:    openBrowserBestEffort,
	}
}

func (s connectState) active() bool {
	return s.step != connectStepIdle
}

func (s connectState) prompting() bool {
	return s.step == connectStepProvider || s.step == connectStepMethod
}

func (s connectState) waiting() bool {
	return s.step == connectStepBrowserWait || s.step == connectStepDeviceWait
}
