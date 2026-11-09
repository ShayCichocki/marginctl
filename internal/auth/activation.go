package auth

import (
	"fmt"
	"strings"

	"github.com/ShayCichocki/marginctl/internal/config"
)

const AuthModeChatGPT = "chatgpt"

// ProviderMetadataWriter persists the provider selection and reports the
// resulting save details (path, and any rewrite notice/backup).
type ProviderMetadataWriter func(provider string) (config.SaveResult, error)

type ActivationResult struct {
	Activated bool
	Provider  string
	Path      string
	Notice    string
	// Backup and SaveNotice carry the config save fallback details: when the
	// config could not be edited in place it was rewritten and the original
	// kept at Backup.
	Backup     string
	SaveNotice string
}

func ActivateChatGPT(writeProvider ProviderMetadataWriter, envProvider string) (ActivationResult, error) {
	envProvider = strings.ToLower(strings.TrimSpace(envProvider))
	if envProvider != "" {
		return ActivationResult{
			Activated: false,
			Provider:  config.ProviderChatGPT,
			Notice: fmt.Sprintf(
				"ChatGPT auth stored, but MARGINCTL_LLM_PROVIDER=%s prevents activation; unset it to use :connect openai.",
				envProvider,
			),
		}, nil
	}

	saved, err := writeProvider(config.ProviderChatGPT)
	if err != nil {
		return ActivationResult{}, fmt.Errorf("activate chatgpt provider metadata: %w", err)
	}
	return ActivationResult{
		Activated:  true,
		Provider:   config.ProviderChatGPT,
		Path:       saved.Path,
		Notice:     fmt.Sprintf("chatgpt auth stored and provider metadata activated in %s.", saved.Path),
		Backup:     saved.Backup,
		SaveNotice: saved.Notice,
	}, nil
}
