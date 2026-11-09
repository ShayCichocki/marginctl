package app

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ShayCichocki/marginctl/internal/auth"
	"github.com/ShayCichocki/marginctl/internal/command"
	"github.com/ShayCichocki/marginctl/internal/config"
)

func (m *Model) disconnectAuth() {
	path, err := m.connectDeps.authStorePath()
	if err != nil {
		m.setErrorNotice(auth.RedactSecrets(err.Error()))
		return
	}
	status := auth.TokenStoreStatus(path)
	if !status.Connected {
		m.setNotice("Not connected")
		return
	}
	if err := auth.DeleteTokenStore(path); err != nil {
		m.setErrorNotice(auth.RedactSecrets(err.Error()))
		return
	}
	m.setNotice("Disconnected ChatGPT auth locally")
}

func (m *Model) showAuthStatus(arg string) tea.Cmd {
	arg = strings.ToLower(strings.TrimSpace(arg))
	if arg != "" && arg != "status" {
		m.setErrorNotice("usage: :auth [status]")
		return nil
	}
	m.results.Show(command.Result{Type: command.ResultText, Content: m.authStatusText()})
	m.focus = FocusResults
	m.editor.Blur()
	m.updateLayout()
	m.updateStatusBar()
	return nil
}

func (m *Model) authStatusText() string {
	path, err := m.connectDeps.authStorePath()
	if err != nil {
		return auth.RedactSecrets(err.Error())
	}
	parts := []string{auth.TokenStoreStatus(path).Format()}
	if provider, ok := config.EnvProviderOverride(); ok {
		parts = append(parts, "MARGINCTL_LLM_PROVIDER="+provider+" blocks activation until unset")
	} else {
		parts = append(parts, "No MARGINCTL_LLM_PROVIDER override")
	}
	return auth.RedactSecrets(strings.Join(parts, "\n"))
}
