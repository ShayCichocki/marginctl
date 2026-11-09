package app

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ShayCichocki/marginctl/internal/config"
)

// Setup steps: choose a provider, then paste its API key.
const (
	setupStepProvider = iota
	setupStepKey
)

// startSetup begins the interactive LLM provider setup (:setup, the
// "Configure LLM Provider" command, or the first-run hint). It refuses to overwrite an
// existing config file unless forced (:setup!), so a hand-maintained config
// can't be clobbered by muscle memory.
func (m *Model) startSetup(force bool) tea.Cmd {
	if config.FileExists() && !force {
		m.setErrorNotice("config already exists at " + config.Path() + " — :setup! to overwrite, or edit the file directly")
		return nil
	}
	m.setPrompt(promptSetup)
	m.setupStep = setupStepProvider
	m.setupProvider = ""
	m.setupBuffer = ""
	m.prompt.Reset()
	return nil
}

func (m *Model) resetSetup() {
	m.setPrompt(promptNone)
	m.setupStep = setupStepProvider
	m.setupProvider = ""
	m.setupBuffer = ""
}

// parseProviderChoice maps setup input (a menu number or a provider name) to a
// canonical provider ID.
func parseProviderChoice(s string) (string, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", config.ProviderAnthropic:
		return config.ProviderAnthropic, true
	case "2", config.ProviderOpenAI:
		return config.ProviderOpenAI, true
	case "3", config.ProviderVercel:
		return config.ProviderVercel, true
	}
	return "", false
}

func (m *Model) handleSetupInput(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEscape:
		m.resetSetup()
		m.setNotice("Setup cancelled")
		return m, nil

	case tea.KeyEnter:
		if m.setupStep == setupStepProvider {
			provider, ok := parseProviderChoice(m.setupBuffer)
			if !ok {
				m.setupBuffer = ""
				m.setErrorNotice("Choose a provider: 1 anthropic · 2 openai · 3 vercel (esc to cancel)")
				return m, nil
			}
			m.setupProvider = provider
			m.setupStep = setupStepKey
			m.setupBuffer = ""
			m.prompt.Reset()
			return m, nil
		}
		key := strings.TrimSpace(m.setupBuffer)
		if key == "" {
			m.setErrorNotice("API key required (esc to cancel)")
			return m, nil
		}
		provider := m.setupProvider
		m.resetSetup()
		return m, m.finishSetup(provider, key)

	default:
		m.editPrompt(&m.setupBuffer, msg, nil)
		return m, nil
	}
}

// finishSetup persists the chosen provider + key (0600, file-sourced values
// only) and reconfigures the live client so LLM commands work immediately —
// no restart, no hand-edited TOML.
func (m *Model) finishSetup(provider, key string) tea.Cmd {
	res, err := config.SaveSetupResult(provider, key)
	if err != nil {
		m.err = err
		return nil
	}
	// Mirror SaveSetup's semantics on the in-memory config: a provider switch
	// invalidates the old provider's model/endpoint.
	if m.config.LLM.Provider != provider {
		m.config.LLM.Model = ""
		m.config.LLM.Endpoint = ""
	}
	m.config.LLM.Provider = provider
	m.config.LLM.APIKey = key
	m.llmClient.Reconfigure(m.config.LLM)
	msg := fmt.Sprintf("LLM configured: %s (%s) — saved to %s", provider, m.llmClient.Model(), res.Path)
	// If the file could not be edited in place it was rewritten; the user must
	// learn that their original (comments, unknown keys) is in Backup. The bar
	// is one row, so that message leads instead of trailing the success line.
	if !m.reportConfigRewrite(res.Backup, res.Notice, msg) {
		m.setNotice(msg)
	}
	return nil
}

// setupPromptLine renders the bottom-bar prompt for the current setup step.
// The API key is masked; only its length shows.
func (m *Model) setupPromptLine() string {
	m.syncPrompt(&m.setupBuffer)
	width := m.width
	if width <= 0 {
		width = 120
	}
	m.prompt.SetWidth(width)
	if m.setupStep == setupStepProvider {
		return "LLM provider - 1 anthropic / 2 openai / 3 vercel: " + m.prompt.View()
	}
	m.prompt.SetMasked(true)
	defer m.prompt.SetMasked(false)
	return fmt.Sprintf("%s API key (enter to save / esc to cancel): %s", m.setupProvider, m.prompt.View())
}
