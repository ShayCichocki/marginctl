package config

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"github.com/ShayCichocki/marginctl/internal/fsutil"
)

const (
	ProviderAnthropic = "anthropic"
	ProviderOpenAI    = "openai"
	ProviderVercel    = "vercel"
	ProviderChatGPT   = "chatgpt"
)

// Config holds all configuration options
type Config struct {
	LLM   LLMConfig   `toml:"llm"`
	Theme ThemeConfig `toml:"theme"`
	// Notes carries non-fatal load warnings (e.g. unknown TOML keys) for the UI
	// to surface. Never serialized.
	Notes []string `toml:"-"`
}

// ThemeConfig selects a color preset and optionally overrides individual colors
// (hex strings like "#7C3AED"). An empty ThemeConfig means the default dark theme.
type ThemeConfig struct {
	Preset string `toml:"preset,omitempty"` // "dark" (default) or "light"
	// Individual overrides, applied on top of the preset when non-empty.
	Primary    string `toml:"primary,omitempty"`
	Accent     string `toml:"accent,omitempty"`
	Text       string `toml:"text,omitempty"`
	Background string `toml:"background,omitempty"`
	Heading    string `toml:"heading,omitempty"`
}

// LLMConfig holds LLM-related configuration. omitempty on the optional fields
// keeps a SaveSetup-written file minimal (no `model = ""` noise).
type LLMConfig struct {
	Provider string `toml:"provider"`
	APIKey   string `toml:"api_key,omitempty"`
	Model    string `toml:"model,omitempty"`
	Endpoint string `toml:"endpoint,omitempty"`
	// TimeoutSeconds bounds a single LLM request. Zero (the default) means
	// "use the built-in default"; the client applies it as a per-call context
	// deadline so a hung provider can't wedge the UI forever.
	TimeoutSeconds int    `toml:"timeout_seconds,omitzero"`
	ProviderSource Source `toml:"-"`
	APIKeySource   Source `toml:"-"`
	ModelSource    Source `toml:"-"`
	EndpointSource Source `toml:"-"`
}

type Source string

const (
	SourceDefault Source = "default"
	SourceFile    Source = "file"
	SourceEnv     Source = "environment"
)

// DefaultConfig returns the default configuration
func DefaultConfig() Config {
	return Config{}
}

// Load loads configuration from environment variables and config file.
// Priority: env vars > config file > defaults. The precedence rules below are
// deliberate so an explicit choice is never silently overridden:
//   - An explicit provider (from file or MARGINCTL_LLM_PROVIDER) is authoritative;
//     provider inference from a stray *_API_KEY only runs when none was chosen.
//   - When an env var switches the provider away from the file's, file-sourced
//     model/endpoint no longer apply and fall back to the new provider's defaults
//     (unless those are themselves overridden via env).
func Load() (Config, error) {
	cfg := DefaultConfig()

	// Try to load from config file
	configPath := getConfigPath()
	if configPath != "" {
		if _, err := os.Stat(configPath); err == nil {
			md, err := toml.DecodeFile(configPath, &cfg)
			if err != nil {
				return cfg, err
			}
			// Warn on typo'd / unknown keys rather than silently ignoring them.
			for _, key := range md.Undecoded() {
				cfg.Notes = append(cfg.Notes, "unknown config key: "+key.String())
			}
		}
	}
	cfg.LLM.Provider = normalizeProvider(cfg.LLM.Provider)
	fileProvider := cfg.LLM.Provider
	if cfg.LLM.Provider != "" {
		cfg.LLM.ProviderSource = SourceFile
	}
	if cfg.LLM.APIKey != "" {
		cfg.LLM.APIKeySource = SourceFile
	}
	if cfg.LLM.Model != "" {
		cfg.LLM.ModelSource = SourceFile
	}
	if cfg.LLM.Endpoint != "" {
		cfg.LLM.EndpointSource = SourceFile
	}

	// Override with environment variables
	if apiKey := os.Getenv("MARGINCTL_LLM_API_KEY"); apiKey != "" {
		cfg.LLM.APIKey = apiKey
		cfg.LLM.APIKeySource = SourceEnv
	}
	modelOverridden := false
	if model := os.Getenv("MARGINCTL_LLM_MODEL"); model != "" {
		cfg.LLM.Model = model
		cfg.LLM.ModelSource = SourceEnv
		modelOverridden = true
	}
	envProvider := ""
	// A blank/whitespace-only value is "no override", matching EnvProviderOverride;
	// it must not erase the provider chosen in the config file.
	if provider := normalizeProvider(os.Getenv("MARGINCTL_LLM_PROVIDER")); provider != "" {
		envProvider = provider
		cfg.LLM.Provider = envProvider
		cfg.LLM.ProviderSource = SourceEnv
	}
	endpointOverridden := false
	if endpoint := os.Getenv("MARGINCTL_LLM_ENDPOINT"); endpoint != "" {
		cfg.LLM.Endpoint = endpoint
		cfg.LLM.EndpointSource = SourceEnv
		endpointOverridden = true
	}
	if timeout := os.Getenv("MARGINCTL_LLM_TIMEOUT"); timeout != "" {
		if secs, err := strconv.Atoi(strings.TrimSpace(timeout)); err == nil && secs > 0 {
			cfg.LLM.TimeoutSeconds = secs
		}
	}

	// If env switched providers, file-sourced model/endpoint belong to the old
	// provider and would misroute requests; clear them so NewClient substitutes
	// the new provider's defaults (unless the env also set them explicitly).
	if envProvider != "" && envProvider != fileProvider {
		if cfg.LLM.APIKeySource == SourceFile {
			cfg.LLM.APIKey = ""
			cfg.LLM.APIKeySource = SourceDefault
		}
		if !modelOverridden {
			cfg.LLM.Model = ""
		}
		if !endpointOverridden {
			cfg.LLM.Endpoint = ""
		}
	}

	explicitProvider := cfg.LLM.Provider != ""

	if cfg.LLM.APIKey == "" {
		cfg.LLM.APIKey = providerAPIKey(cfg.LLM.Provider)
		if cfg.LLM.APIKey != "" {
			cfg.LLM.APIKeySource = SourceEnv
		}
	}

	// Only infer the provider from whichever *_API_KEY is present when the user
	// made no explicit choice. If a provider was chosen but its key is missing,
	// leave the key empty (unconfigured) rather than borrowing another
	// provider's key and POSTing to the wrong API.
	if cfg.LLM.APIKey == "" && !explicitProvider {
		inferProviderFromEnv(&cfg.LLM)
	}

	return cfg, nil
}

func normalizeProvider(provider string) string {
	return strings.ToLower(strings.TrimSpace(provider))
}

func providerAPIKey(provider string) string {
	switch normalizeProvider(provider) {
	case ProviderAnthropic:
		return os.Getenv("ANTHROPIC_API_KEY")
	case ProviderOpenAI:
		return os.Getenv("OPENAI_API_KEY")
	case ProviderVercel:
		if apiKey := os.Getenv("AI_GATEWAY_API_KEY"); apiKey != "" {
			return apiKey
		}
		return os.Getenv("VERCEL_AI_GATEWAY_API_KEY")
	default:
		return ""
	}
}

func inferProviderFromEnv(llm *LLMConfig) {
	inferProviderKeyFromEnv(llm)
	if llm.APIKey != "" {
		llm.ProviderSource = SourceEnv
		llm.APIKeySource = SourceEnv
	}
}

func inferProviderKeyFromEnv(llm *LLMConfig) {
	if apiKey := os.Getenv("ANTHROPIC_API_KEY"); apiKey != "" {
		llm.Provider = ProviderAnthropic
		llm.APIKey = apiKey
		return
	}
	if apiKey := os.Getenv("OPENAI_API_KEY"); apiKey != "" {
		llm.Provider = ProviderOpenAI
		llm.APIKey = apiKey
		return
	}
	if apiKey := os.Getenv("AI_GATEWAY_API_KEY"); apiKey != "" {
		llm.Provider = ProviderVercel
		llm.APIKey = apiKey
		return
	}
	if apiKey := os.Getenv("VERCEL_AI_GATEWAY_API_KEY"); apiKey != "" {
		llm.Provider = ProviderVercel
		llm.APIKey = apiKey
	}
}

// getConfigPath returns the path to the config file
func getConfigPath() string {
	// Check XDG_CONFIG_HOME first
	if xdgConfig := os.Getenv("XDG_CONFIG_HOME"); xdgConfig != "" {
		return filepath.Join(xdgConfig, "marginctl", "config.toml")
	}

	// Fall back to ~/.config
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	return filepath.Join(home, ".config", "marginctl", "config.toml")
}

// Path returns the config file location (for user-facing messages). Empty when
// the home directory can't be determined.
func Path() string {
	return getConfigPath()
}

// FileExists reports whether a config file is present on disk.
func FileExists() bool {
	path := getConfigPath()
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// SaveResult describes a completed config save.
type SaveResult struct {
	// Path is the config file written.
	Path string
	// Notice is non-empty when the user should be told something about the save:
	// the file had a structure that could not be edited in place, so it was
	// rewritten from scratch (comments and unknown keys may be lost) and the
	// original kept at Backup.
	Notice string
	// Backup is the path of the timestamped copy of the original file, if any.
	Backup string
}

// SaveSetup writes the in-app setup's provider + API key to the config file and
// returns the path written. It deliberately serializes only *file-sourced*
// values — never the env-merged Config from Load(), which could leak an
// env-sourced key to disk. Only the [llm] keys being set are touched (comments,
// layout, unknown keys and other tables survive byte-for-byte). An empty apiKey
// means "leave the file's key alone" when the provider is unchanged; switching
// provider always drops the old provider's api_key/model/endpoint, since they
// belong to the old provider and would misroute requests. The file is written
// atomically with 0600 permissions (it holds a secret).
func SaveSetup(provider, apiKey string) (string, error) {
	res, err := SaveSetupResult(provider, apiKey)
	return res.Path, err
}

// SaveSetupResult is SaveSetup plus the notice/backup details.
func SaveSetupResult(provider, apiKey string) (SaveResult, error) {
	return saveLLMMetadata(provider, strings.TrimSpace(apiKey))
}

// SaveProviderMetadata writes only the non-secret provider selection to the
// config file. OAuth-backed auth flows use this to activate their provider while
// keeping tokens in the auth store, never in config.toml. Switching away from a
// different provider drops that provider's api_key (a chosen provider never
// borrows another's key); re-activating the same provider leaves the file alone.
func SaveProviderMetadata(provider string) (string, error) {
	res, err := SaveProviderMetadataResult(provider)
	return res.Path, err
}

// SaveProviderMetadataResult is SaveProviderMetadata plus notice/backup details.
func SaveProviderMetadataResult(provider string) (SaveResult, error) {
	return saveLLMMetadata(provider, "")
}

// EnvProviderOverride returns the normalized env-selected provider, when set.
func EnvProviderOverride() (string, bool) {
	provider := normalizeProvider(os.Getenv("MARGINCTL_LLM_PROVIDER"))
	return provider, provider != ""
}

func saveLLMMetadata(provider, apiKey string) (SaveResult, error) {
	path := getConfigPath()
	if path == "" {
		return SaveResult{}, errors.New("cannot determine config path (no home directory)")
	}

	var file Config
	var original []byte
	exists := false
	if data, err := os.ReadFile(path); err == nil {
		exists, original = true, data
		if _, err := toml.Decode(string(data), &file); err != nil {
			return SaveResult{}, fmt.Errorf("existing config unreadable: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return SaveResult{}, fmt.Errorf("existing config unreadable: %w", err)
	}

	provider = normalizeProvider(provider)
	changed := normalizeProvider(file.LLM.Provider) != provider

	// Target state (file-sourced values only) and the minimal edits to reach it.
	want := file
	var edits []llmEdit
	if file.LLM.Provider != provider {
		want.LLM.Provider = provider
		edits = append(edits, setString("provider", provider))
	}
	if changed {
		want.LLM.APIKey, want.LLM.Model, want.LLM.Endpoint = "", "", ""
		for _, k := range []struct{ key, old string }{
			{"api_key", file.LLM.APIKey}, {"model", file.LLM.Model}, {"endpoint", file.LLM.Endpoint},
		} {
			if k.old != "" && (k.key != "api_key" || apiKey == "") { // a new key replaces in place
				edits = append(edits, removeKey(k.key))
			}
		}
	}
	if apiKey != "" && apiKey != want.LLM.APIKey {
		want.LLM.APIKey = apiKey
		edits = append(edits, setString("api_key", apiKey))
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return SaveResult{}, err
	}
	res := SaveResult{Path: path}
	if exists && len(edits) == 0 {
		// Nothing to change; rewrite the same bytes so permissions are still
		// tightened to 0600.
		if err := fsutil.WriteFilePrivate(path, original); err != nil {
			return SaveResult{}, err
		}
		return res, nil
	}

	edited, err := editLLMTable(string(original), edits)
	if err == nil {
		err = verifyEdited(edited, want)
	}
	if err == nil {
		if werr := fsutil.WriteFilePrivate(path, []byte(edited)); werr != nil {
			return SaveResult{}, werr
		}
		return res, nil
	}

	// Fallback: the structure is not safe to edit in place. Keep the original
	// and rewrite from the decoded struct.
	if exists {
		backup, berr := writeBackup(path, original)
		if berr != nil {
			return SaveResult{}, fmt.Errorf("cannot back up config before rewriting it: %w", berr)
		}
		res.Backup = backup
		res.Notice = fmt.Sprintf("config.toml could not be edited in place (%v); it was rewritten and comments/unknown keys may be lost. Original saved to %s", err, backup)
	}
	var buf bytes.Buffer
	if err := toml.NewEncoder(&buf).Encode(want); err != nil {
		return SaveResult{}, err
	}
	if err := fsutil.WriteFilePrivate(path, buf.Bytes()); err != nil {
		return SaveResult{}, err
	}
	return res, nil
}

// verifyEdited decodes the surgically edited text and checks it yields exactly
// the intended settings; any surprise sends the caller to the safe fallback.
func verifyEdited(edited string, want Config) error {
	var got Config
	if _, err := toml.Decode(edited, &got); err != nil {
		return unsafef("edited file does not parse: %v", err)
	}
	if got.LLM != want.LLM || got.Theme != want.Theme {
		return unsafef("edited file does not match the intended settings")
	}
	return nil
}

// writeBackup stores a private timestamped copy of the original config.
func writeBackup(path string, data []byte) (string, error) {
	base := path + ".bak-" + time.Now().Format("20060102T150405")
	name := base
	for i := 1; ; i++ {
		if _, err := os.Lstat(name); os.IsNotExist(err) {
			break
		}
		name = fmt.Sprintf("%s-%d", base, i)
	}
	if err := fsutil.WriteFilePrivate(name, data); err != nil {
		return "", err
	}
	return name, nil
}
