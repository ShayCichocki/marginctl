package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolate clears every env var Load reads and points both config roots at a temp
// dir so nothing leaks from the developer's machine.
func isolate(t *testing.T) string {
	t.Helper()
	clearLLMEnv(t)
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "xdg"))
	return dir
}

func readCfgFile(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(Path())
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestLoadPrecedenceMatrix(t *testing.T) {
	const file = `[llm]
provider = "openai"
api_key = "file-key"
model = "file-model"
endpoint = "https://file.example/v1/x"
`
	cases := []struct {
		name                         string
		env                          map[string]string
		provider, key, model, endpnt string
	}{
		{"file only", nil, "openai", "file-key", "file-model", "https://file.example/v1/x"},
		{"env key beats file key", map[string]string{"MARGINCTL_LLM_API_KEY": "env-key"}, "openai", "env-key", "file-model", "https://file.example/v1/x"},
		{"provider key env fills only when file key empty", map[string]string{"OPENAI_API_KEY": "stray"}, "openai", "file-key", "file-model", "https://file.example/v1/x"},
		{"env model/endpoint override file", map[string]string{"MARGINCTL_LLM_MODEL": "m", "MARGINCTL_LLM_ENDPOINT": "https://e.example/x"}, "openai", "file-key", "m", "https://e.example/x"},
		{"same-provider env keeps file values", map[string]string{"MARGINCTL_LLM_PROVIDER": " OpenAI "}, "openai", "file-key", "file-model", "https://file.example/v1/x"},
		{"switch drops file key model endpoint", map[string]string{"MARGINCTL_LLM_PROVIDER": "anthropic"}, "anthropic", "", "", ""},
		{"switch uses new provider env key not old", map[string]string{"MARGINCTL_LLM_PROVIDER": "anthropic", "ANTHROPIC_API_KEY": "ant", "OPENAI_API_KEY": "oai"}, "anthropic", "ant", "", ""},
		{"switch keeps env-set model only", map[string]string{"MARGINCTL_LLM_PROVIDER": "vercel", "MARGINCTL_LLM_MODEL": "envm"}, "vercel", "", "envm", ""},
		{"switch keeps env-set endpoint only", map[string]string{"MARGINCTL_LLM_PROVIDER": "vercel", "MARGINCTL_LLM_ENDPOINT": "https://v.example/x"}, "vercel", "", "", "https://v.example/x"},
		{"switch keeps env key", map[string]string{"MARGINCTL_LLM_PROVIDER": "vercel", "MARGINCTL_LLM_API_KEY": "ek"}, "vercel", "ek", "", ""},
		{"explicit provider never borrows other key", map[string]string{"ANTHROPIC_API_KEY": "ant"}, "openai", "file-key", "file-model", "https://file.example/v1/x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			clearLLMEnv(t)
			writeConfig(t, file)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			cfg, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			l := cfg.LLM
			if l.Provider != tc.provider || l.APIKey != tc.key || l.Model != tc.model || l.Endpoint != tc.endpnt {
				t.Fatalf("got provider=%q key=%q model=%q endpoint=%q; want %q %q %q %q", l.Provider, l.APIKey, l.Model, l.Endpoint, tc.provider, tc.key, tc.model, tc.endpnt)
			}
		})
	}
}

func TestLoadExplicitProviderWithoutKeyStaysUnconfigured(t *testing.T) {
	others := map[string][]string{
		ProviderAnthropic: {"OPENAI_API_KEY", "AI_GATEWAY_API_KEY", "VERCEL_AI_GATEWAY_API_KEY"},
		ProviderOpenAI:    {"ANTHROPIC_API_KEY", "AI_GATEWAY_API_KEY", "VERCEL_AI_GATEWAY_API_KEY"},
		ProviderVercel:    {"ANTHROPIC_API_KEY", "OPENAI_API_KEY"},
		ProviderChatGPT:   {"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "AI_GATEWAY_API_KEY", "VERCEL_AI_GATEWAY_API_KEY"},
	}
	for p, keys := range others {
		t.Run(p, func(t *testing.T) {
			isolate(t)
			t.Setenv("MARGINCTL_LLM_PROVIDER", p)
			for _, k := range keys {
				t.Setenv(k, "other-"+k)
			}
			cfg, _ := Load()
			if cfg.LLM.Provider != p || cfg.LLM.APIKey != "" {
				t.Fatalf("provider=%q key=%q, want no borrowed key", cfg.LLM.Provider, cfg.LLM.APIKey)
			}
		})
	}
}

func TestLoadInferenceOrderAndSources(t *testing.T) {
	cases := []struct {
		env      map[string]string
		provider string
		key      string
	}{
		{map[string]string{"ANTHROPIC_API_KEY": "a", "OPENAI_API_KEY": "o", "AI_GATEWAY_API_KEY": "g"}, ProviderAnthropic, "a"},
		{map[string]string{"OPENAI_API_KEY": "o", "AI_GATEWAY_API_KEY": "g"}, ProviderOpenAI, "o"},
		{map[string]string{"AI_GATEWAY_API_KEY": "g", "VERCEL_AI_GATEWAY_API_KEY": "v"}, ProviderVercel, "g"},
		{map[string]string{"VERCEL_AI_GATEWAY_API_KEY": "v"}, ProviderVercel, "v"},
		{nil, "", ""},
	}
	for _, tc := range cases {
		isolate(t)
		for k, v := range tc.env {
			t.Setenv(k, v)
		}
		cfg, err := Load()
		if err != nil {
			t.Fatal(err)
		}
		if cfg.LLM.Provider != tc.provider || cfg.LLM.APIKey != tc.key {
			t.Errorf("env=%v => provider=%q key=%q", tc.env, cfg.LLM.Provider, cfg.LLM.APIKey)
		}
		if tc.key != "" && cfg.LLM.APIKeySource != SourceEnv {
			t.Errorf("APIKeySource = %q", cfg.LLM.APIKeySource)
		}
	}
}

func TestLoadSourcesTracked(t *testing.T) {
	isolate(t)
	writeConfig(t, "[llm]\nprovider = \"openai\"\napi_key = \"k\"\nmodel = \"m\"\n")
	t.Setenv("MARGINCTL_LLM_ENDPOINT", "https://x.example/e")
	cfg, _ := Load()
	l := cfg.LLM
	if l.ProviderSource != SourceFile || l.APIKeySource != SourceFile || l.ModelSource != SourceFile || l.EndpointSource != SourceEnv {
		t.Fatalf("sources = %+v", l)
	}
}

// A whitespace-only MARGINCTL_LLM_PROVIDER normalizes to "no override"; it must
// not erase the provider chosen in the config file.
func TestLoadBlankEnvProviderDoesNotClobberFileProvider(t *testing.T) {
	isolate(t)
	writeConfig(t, "[llm]\nprovider = \"openai\"\napi_key = \"k\"\n")
	t.Setenv("MARGINCTL_LLM_PROVIDER", "   ")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLM.Provider != ProviderOpenAI || cfg.LLM.ProviderSource != SourceFile {
		t.Fatalf("provider=%q source=%q, want openai/file", cfg.LLM.Provider, cfg.LLM.ProviderSource)
	}
	if _, ok := EnvProviderOverride(); ok {
		t.Fatal("blank env provider is not an override")
	}
}

func TestLoadMalformedTOMLIsAnError(t *testing.T) {
	isolate(t)
	writeConfig(t, "[llm\nprovider = ")
	if _, err := Load(); err == nil {
		t.Fatal("malformed config must return an error")
	}
}

func TestLoadReportsEveryUnknownKey(t *testing.T) {
	isolate(t)
	writeConfig(t, "[llm]\nprovider = \"openai\"\napikey = \"typo\"\n\n[bogus]\nx = 1\n")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(cfg.Notes, "|")
	if !strings.Contains(joined, "llm.apikey") || !strings.Contains(joined, "bogus") {
		t.Fatalf("notes = %v", cfg.Notes)
	}
}

func TestLoadInvalidTimeoutEnvIgnored(t *testing.T) {
	for _, v := range []string{"abc", "-5", "0", "1.5"} {
		isolate(t)
		writeConfig(t, "[llm]\nprovider = \"openai\"\ntimeout_seconds = 42\n")
		t.Setenv("MARGINCTL_LLM_TIMEOUT", v)
		cfg, _ := Load()
		if cfg.LLM.TimeoutSeconds != 42 {
			t.Errorf("timeout env %q => %d, want file value 42", v, cfg.LLM.TimeoutSeconds)
		}
	}
}

func TestPathResolution(t *testing.T) {
	isolate(t)
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	if got, want := Path(), filepath.Join(xdg, "marginctl", "config.toml"); got != want {
		t.Fatalf("Path = %q, want %q", got, want)
	}
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if got, want := Path(), filepath.Join(home, ".config", "marginctl", "config.toml"); got != want {
		t.Fatalf("Path fallback = %q, want %q", got, want)
	}
}

func TestEnvProviderOverride(t *testing.T) {
	isolate(t)
	if p, ok := EnvProviderOverride(); ok || p != "" {
		t.Fatalf("unset => %q,%v", p, ok)
	}
	t.Setenv("MARGINCTL_LLM_PROVIDER", " ChatGPT ")
	if p, ok := EnvProviderOverride(); !ok || p != ProviderChatGPT {
		t.Fatalf("set => %q,%v", p, ok)
	}
}

func TestSaveSetupNeverPersistsEnvSourcedValues(t *testing.T) {
	isolate(t)
	t.Setenv("MARGINCTL_LLM_API_KEY", "ENV-SECRET-1")
	t.Setenv("ANTHROPIC_API_KEY", "ENV-SECRET-2")
	t.Setenv("OPENAI_API_KEY", "ENV-SECRET-3")
	t.Setenv("MARGINCTL_LLM_MODEL", "env-model")
	t.Setenv("MARGINCTL_LLM_ENDPOINT", "https://env.example/x")
	t.Setenv("MARGINCTL_LLM_TIMEOUT", "99")

	if _, err := SaveSetup("openai", "typed-key"); err != nil {
		t.Fatal(err)
	}
	body := readCfgFile(t)
	for _, leak := range []string{"ENV-SECRET", "env-model", "env.example", "99"} {
		if strings.Contains(body, leak) {
			t.Fatalf("env-sourced value %q reached disk:\n%s", leak, body)
		}
	}
	if !strings.Contains(body, "typed-key") {
		t.Fatalf("typed key missing:\n%s", body)
	}

	// Provider-metadata-only writes must never put a key on disk either.
	if _, err := SaveProviderMetadata("chatgpt"); err != nil {
		t.Fatal(err)
	}
	body = readCfgFile(t)
	if strings.Contains(body, "ENV-SECRET") || strings.Contains(body, "typed-key") || strings.Contains(body, "api_key") {
		t.Fatalf("chatgpt metadata write carried a key:\n%s", body)
	}
	if !strings.Contains(body, `provider = "chatgpt"`) {
		t.Fatalf("provider missing:\n%s", body)
	}
}

func TestSaveSetupTightensPermissionsAtomicallyAndLeavesNoTemp(t *testing.T) {
	isolate(t)
	writeConfig(t, "[llm]\nprovider = \"openai\"\n") // written 0644 by helper
	path, err := SaveSetup("openai", "k")
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm()&0o077 != 0 {
		t.Fatalf("config holding a secret has mode %v", st.Mode().Perm())
	}
	ents, _ := os.ReadDir(filepath.Dir(path))
	if len(ents) != 1 {
		t.Fatalf("temp files left behind: %v", ents)
	}
}

func TestSaveSetupRefusesMalformedExistingConfigAndKeepsIt(t *testing.T) {
	isolate(t)
	const bad = "[llm\nprovider = "
	writeConfig(t, bad)
	if _, err := SaveSetup("openai", "k"); err == nil {
		t.Fatal("expected error")
	}
	if got := readCfgFile(t); got != bad {
		t.Fatalf("existing config modified: %q", got)
	}
}

func TestSaveSetupProviderSwitchClearsModelEndpointButKeepsThemeAndTimeout(t *testing.T) {
	isolate(t)
	writeConfig(t, "[llm]\nprovider = \"openai\"\napi_key = \"old\"\nmodel = \"m\"\nendpoint = \"https://o.example/x\"\ntimeout_seconds = 30\n\n[theme]\npreset = \"light\"\n")
	if _, err := SaveSetup("Vercel", "newkey"); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	l := cfg.LLM
	if l.Provider != ProviderVercel || l.APIKey != "newkey" || l.Model != "" || l.Endpoint != "" || l.TimeoutSeconds != 30 {
		t.Fatalf("llm = %+v", l)
	}
	if cfg.Theme.Preset != "light" {
		t.Fatalf("theme lost: %+v", cfg.Theme)
	}
	body := readCfgFile(t)
	if strings.Contains(body, "old") || strings.Contains(body, "o.example") {
		t.Fatalf("stale provider data persisted:\n%s", body)
	}
}

func TestSaveSetupNoHomeReturnsError(t *testing.T) {
	clearLLMEnv(t)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	t.Setenv("home", "")
	if Path() != "" {
		t.Skip("platform resolves a home directory without HOME")
	}
	if _, err := SaveSetup("openai", "k"); err == nil {
		t.Fatal("expected error with no config path")
	}
	if FileExists() {
		t.Fatal("FileExists with no path must be false")
	}
}
