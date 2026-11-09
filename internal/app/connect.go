package app

import (
	"context"
	"fmt"
	"os"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ShayCichocki/marginctl/internal/auth"
)

func (m *Model) startConnect(arg string) tea.Cmd {
	m.cancelConnect()
	m.prompt.Reset()
	m.connect = connectState{step: connectStepProvider}
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(arg)))
	if len(fields) == 0 {
		return nil
	}
	provider, ok := parseConnectProvider(fields[0])
	if !ok {
		m.connect = connectState{}
		m.setErrorNotice("Connect provider must be openai")
		return nil
	}
	m.connect.provider = provider
	m.connect.step = connectStepMethod
	if len(fields) < 2 {
		return nil
	}
	method, ok := parseConnectMethodArg(fields[1:])
	if !ok {
		m.setErrorNotice("Connect method: 1 browser · 2 device (esc to cancel)")
		return nil
	}
	return m.beginConnectMethod(method)
}

func (m *Model) handleConnectInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEscape:
		m.cancelConnect()
		m.setNotice("Connect cancelled")
		return m, nil
	case tea.KeyEnter:
		return m.submitConnectInput()
	default:
		m.editPrompt(&m.connect.buffer, msg, nil)
		return m, nil
	}
}

func (m *Model) submitConnectInput() (tea.Model, tea.Cmd) {
	input := m.connect.buffer
	m.connect.buffer = ""
	m.prompt.Reset()
	switch m.connect.step {
	case connectStepProvider:
		provider, ok := parseConnectProvider(input)
		if !ok {
			m.setErrorNotice("Connect provider: 1 openai (esc to cancel)")
			return m, nil
		}
		m.connect.provider = provider
		m.connect.step = connectStepMethod
		return m, nil
	case connectStepMethod:
		method, ok := parseConnectMethod(input)
		if !ok {
			m.setErrorNotice("Connect method: 1 browser · 2 device (esc to cancel)")
			return m, nil
		}
		return m, m.beginConnectMethod(method)
	default:
		return m, nil
	}
}

func (m *Model) beginConnectMethod(method string) tea.Cmd {
	m.connect.method = method
	m.connect.buffer = ""
	m.connectAttemptID++
	attemptID := m.connectAttemptID
	ctx, cancel := context.WithCancel(context.Background())
	m.connectCancel = cancel
	switch method {
	case connectMethodBrowser:
		return m.startBrowserConnectCmd(ctx, attemptID)
	case connectMethodDevice:
		return m.startDeviceConnectCmd(ctx, attemptID)
	default:
		m.setErrorNotice("Connect method: 1 browser · 2 device (esc to cancel)")
		return nil
	}
}

func (m *Model) handleConnectBrowserReady(msg connectBrowserReadyMsg) (tea.Model, tea.Cmd) {
	if msg.attemptID != m.connectAttemptID || !m.connect.active() {
		closeListener(msg.listener)
		return m, nil
	}
	if msg.err != nil {
		m.finishConnectError(msg.err)
		return m, nil
	}
	m.connect.step = connectStepBrowserWait
	m.connect.authorizeURL = msg.url
	m.clearNotice()
	return m, m.waitBrowserConnectCmd(msg)
}

func (m *Model) handleConnectDeviceReady(msg connectDeviceReadyMsg) (tea.Model, tea.Cmd) {
	if msg.attemptID != m.connectAttemptID || !m.connect.active() {
		return m, nil
	}
	if msg.err != nil {
		m.finishConnectError(msg.err)
		return m, nil
	}
	m.connect.step = connectStepDeviceWait
	m.connect.deviceCode = msg.start.UserCode
	m.connect.verificationURL = msg.start.VerificationURI
	m.clearNotice()
	return m, m.waitDeviceConnectCmd(msg)
}

func (m *Model) handleConnectResult(msg connectResultMsg) (tea.Model, tea.Cmd) {
	if msg.attemptID != m.connectAttemptID {
		return m, nil
	}
	m.connectCancel = nil
	m.connect = connectState{}
	if msg.err != nil {
		m.setErrorNotice(auth.RedactSecrets(msg.err.Error()))
		m.updateStatusBar()
		return m, nil
	}
	if msg.result.Activated {
		model := ""
		if strings.TrimSpace(os.Getenv("MARGINCTL_LLM_MODEL")) != "" {
			model = m.config.LLM.Model
		}
		endpoint := ""
		if strings.TrimSpace(os.Getenv("MARGINCTL_LLM_ENDPOINT")) != "" {
			endpoint = m.config.LLM.Endpoint
		}
		m.config.LLM.Provider = msg.provider
		m.config.LLM.APIKey = ""
		m.config.LLM.Model = model
		m.config.LLM.Endpoint = endpoint
		m.llmClient.Reconfigure(m.config.LLM)
	}
	connected := auth.RedactSecrets("Connected OpenAI via ChatGPT auth. " + msg.result.Notice)
	if !m.reportConfigRewrite(msg.result.Backup, msg.result.SaveNotice, connected) {
		m.setNotice(connected)
	}
	m.updateStatusBar()
	return m, nil
}

func (m *Model) finishConnectError(err error) {
	m.cancelConnect()
	m.setErrorNotice(auth.RedactSecrets(err.Error()))
	m.updateStatusBar()
}

func (m *Model) cancelConnect() {
	if m.connectCancel != nil {
		m.connectCancel()
		m.connectCancel = nil
	}
	m.connect = connectState{}
	m.connectAttemptID++
}

func (m *Model) connectPromptLine() string {
	switch m.connect.step {
	case connectStepProvider:
		return "Connect provider — 1 openai: " + m.connect.buffer
	case connectStepMethod:
		return "Connect openai method — 1 browser · 2 device: " + m.connect.buffer
	case connectStepBrowserWait:
		return "Open this URL to connect (esc to cancel): " + m.connect.authorizeURL
	case connectStepDeviceWait:
		return fmt.Sprintf("Device code %s at %s (esc to cancel)", m.connect.deviceCode, m.connect.verificationURL)
	default:
		return ""
	}
}

func parseConnectProvider(s string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", connectProviderOpenAI:
		return connectProviderOpenAI, true
	default:
		return "", false
	}
}

func parseConnectMethod(s string) (string, bool) {
	switch strings.TrimPrefix(strings.ToLower(strings.TrimSpace(s)), "--") {
	case "1", connectMethodBrowser:
		return connectMethodBrowser, true
	case "2", connectMethodDevice, "device-code", "devicecode":
		return connectMethodDevice, true
	default:
		return "", false
	}
}

func parseConnectMethodArg(fields []string) (string, bool) {
	for _, field := range fields {
		if method, ok := parseConnectMethod(field); ok {
			return method, true
		}
	}
	return "", false
}
