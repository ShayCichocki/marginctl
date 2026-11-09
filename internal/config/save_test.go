package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const richConfig = `# marginctl config -- hand edited
# keep this header

[llm]
# which provider
provider = "openai"   # trailing comment
api_key = "sk-old"
model = "gpt-5.1"  # pinned
endpoint = 'https://example.com/v1/chat/completions'
timeout_seconds = 42

# colors
[theme]
preset = "light"
primary = "#123456"

[custom]
mystery = [1, 2, 3]   # unknown but valid
`

func TestEditLLMTableTable(t *testing.T) {
	cases := []struct {
		name  string
		src   string
		edits []llmEdit
		want  string
	}{
		{"replace keeps comment and spacing",
			"[llm]\nprovider = \"a\"   # c\n",
			[]llmEdit{setString("provider", "b")},
			"[llm]\nprovider = \"b\"   # c\n"},
		{"literal string replaced",
			"[llm]\nendpoint = 'x'\n",
			[]llmEdit{setString("endpoint", "y")},
			"[llm]\nendpoint = \"y\"\n"},
		{"comment with quote chars and equals",
			"[llm]\nmodel = \"m\" # it's \"x\" = y\n",
			[]llmEdit{setString("model", "n")},
			"[llm]\nmodel = \"n\" # it's \"x\" = y\n"},
		{"escaped quote in old value",
			"[llm]\napi_key = \"a\\\"b#c\"  # k\n",
			[]llmEdit{setString("api_key", "z")},
			"[llm]\napi_key = \"z\"  # k\n"},
		{"remove key",
			"[llm]\nprovider = \"a\"\nmodel = \"m\"\n\n[x]\ny = 1\n",
			[]llmEdit{removeKey("model")},
			"[llm]\nprovider = \"a\"\n\n[x]\ny = 1\n"},
		{"remove missing key is no-op",
			"[llm]\nprovider = \"a\"\n",
			[]llmEdit{removeKey("model")},
			"[llm]\nprovider = \"a\"\n"},
		{"insert after last llm key before next table",
			"[llm]\nprovider = \"a\"\n\n[theme]\npreset = \"x\"\n",
			[]llmEdit{setString("api_key", "k")},
			"[llm]\nprovider = \"a\"\napi_key = \"k\"\n\n[theme]\npreset = \"x\"\n"},
		{"insert under empty llm header at EOF without newline",
			"[llm]",
			[]llmEdit{setString("provider", "a")},
			"[llm]\nprovider = \"a\"\n"},
		{"table absent: appended after blank line",
			"[theme]\npreset = \"x\"\n",
			[]llmEdit{setString("provider", "a")},
			"[theme]\npreset = \"x\"\n\n[llm]\nprovider = \"a\"\n"},
		{"table absent, no trailing newline",
			"# hi",
			[]llmEdit{setString("provider", "a")},
			"# hi\n\n[llm]\nprovider = \"a\"\n"},
		{"empty file",
			"",
			[]llmEdit{setString("provider", "a")},
			"[llm]\nprovider = \"a\"\n"},
		{"table absent, only removals: untouched",
			"[theme]\nx = 1\n",
			[]llmEdit{removeKey("api_key")},
			"[theme]\nx = 1\n"},
		{"CRLF preserved",
			"[llm]\r\nprovider = \"a\"\r\n",
			[]llmEdit{setString("provider", "b"), setString("api_key", "k")},
			"[llm]\r\nprovider = \"b\"\r\napi_key = \"k\"\r\n"},
		{"same key in other table untouched",
			"[theme]\nprovider = \"keep\"\n[llm]\nprovider = \"a\"\n",
			[]llmEdit{setString("provider", "b")},
			"[theme]\nprovider = \"keep\"\n[llm]\nprovider = \"b\"\n"},
		{"llm subtable does not confuse editing",
			"[llm.extra]\nk = 1\n[llm]\nprovider = \"a\"\n",
			[]llmEdit{setString("provider", "b")},
			"[llm.extra]\nk = 1\n[llm]\nprovider = \"b\"\n"},
		{"single-line array elsewhere is fine",
			"[x]\na = [1, \"]\", 2] # c\n[llm]\nprovider=\"a\"\n",
			[]llmEdit{setString("provider", "b")},
			"[x]\na = [1, \"]\", 2] # c\n[llm]\nprovider=\"b\"\n"},
		{"spaced header",
			"[ llm ]\nprovider = \"a\"\n",
			[]llmEdit{setString("provider", "b")},
			"[ llm ]\nprovider = \"b\"\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := editLLMTable(tc.src, tc.edits)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("got:\n%q\nwant:\n%q", got, tc.want)
			}
		})
	}
}

func TestEditLLMTableRefusesUnsafe(t *testing.T) {
	cases := map[string]string{
		"dotted top-level":     "llm.provider = \"a\"\n[theme]\nx = 1\n",
		"quoted dotted":        "\"llm\".provider = \"a\"\n",
		"inline table":         "llm = { provider = \"a\" }\n",
		"array table":          "[[llm]]\nprovider = \"a\"\n",
		"duplicate table":      "[llm]\nprovider = \"a\"\n[llm]\nmodel = \"b\"\n",
		"multiline string":     "[x]\ns = \"\"\"\n[llm]\n\"\"\"\n[llm]\nprovider = \"a\"\n",
		"multiline array":      "[x]\na = [\n 1,\n]\n[llm]\nprovider = \"a\"\n",
		"quoted key in llm":    "[llm]\n\"provider\" = \"a\"\n",
		"dotted key in llm":    "[llm]\na.b = 1\n",
		"duplicate key":        "[llm]\nmodel = \"a\"\nmodel = \"b\"\n",
		"junk after value":     "[llm]\nmodel = \"a\" b\n",
		"unterminated string":  "[llm]\nmodel = \"a\n",
		"junk after header":    "[llm] x\n",
		"literal multi-string": "[x]\ns = '''\n",
	}
	for name, src := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := editLLMTable(src, []llmEdit{setString("provider", "b")})
			if err == nil || !strings.Contains(err.Error(), errUnsafeStructure.Error()) {
				t.Fatalf("err = %v, want unsafe-structure", err)
			}
		})
	}
}

func TestTomlQuoteRoundTripsThroughLoad(t *testing.T) {
	keys := []string{
		`plain`, `has "quotes"`, `back\slash`, `trail\`, `a\"b`, "tab\there", "nl\nx", "cr\r",
		"ctrl\x01\x7f", `unicode é 日本`, `# not a comment`, `a = b`, ` spaced `, `'single'`,
		`A`, `]`, `"`,
	}
	for _, key := range keys {
		isolate(t)
		writeConfig(t, richConfig)
		// SaveSetup trims, so compare against the trimmed value.
		if _, err := SaveSetup("openai", key); err != nil {
			t.Fatalf("%q: %v", key, err)
		}
		cfg, err := Load()
		if err != nil {
			t.Fatalf("%q: load: %v", key, err)
		}
		if want := strings.TrimSpace(key); cfg.LLM.APIKey != want {
			t.Fatalf("round trip: got %q want %q", cfg.LLM.APIKey, want)
		}
		if cfg.LLM.Provider != ProviderOpenAI || cfg.Theme.Primary != "#123456" {
			t.Fatalf("collateral damage: %+v", cfg)
		}
	}
}

func TestSaveSetupSurgicalPreservesEverythingElse(t *testing.T) {
	isolate(t)
	writeConfig(t, richConfig)
	res, err := SaveSetupResult("openai", "sk-new")
	if err != nil {
		t.Fatal(err)
	}
	if res.Notice != "" || res.Backup != "" {
		t.Fatalf("unexpected fallback: %+v", res)
	}
	want := strings.Replace(richConfig, `api_key = "sk-old"`, `api_key = "sk-new"`, 1)
	if got := readCfgFile(t); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLM.APIKey != "sk-new" || cfg.LLM.Model != "gpt-5.1" || cfg.LLM.TimeoutSeconds != 42 {
		t.Fatalf("cfg = %+v", cfg.LLM)
	}
	// Unknown-key reporting still works after a save.
	found := false
	for _, n := range cfg.Notes {
		if strings.Contains(n, "custom") {
			found = true
		}
	}
	if !found {
		t.Fatalf("unknown key note lost: %v", cfg.Notes)
	}
}

func TestSaveSetupProviderSwitchSurgical(t *testing.T) {
	isolate(t)
	writeConfig(t, richConfig)
	if _, err := SaveSetup("anthropic", "sk-ant"); err != nil {
		t.Fatal(err)
	}
	want := `# marginctl config -- hand edited
# keep this header

[llm]
# which provider
provider = "anthropic"   # trailing comment
api_key = "sk-ant"
timeout_seconds = 42

# colors
[theme]
preset = "light"
primary = "#123456"

[custom]
mystery = [1, 2, 3]   # unknown but valid
`
	if got := readCfgFile(t); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestEmptyKeyKeepsFileKeyWhenProviderUnchanged(t *testing.T) {
	for _, call := range []struct {
		name string
		fn   func() error
	}{
		{"SaveSetup", func() error { _, err := SaveSetup("openai", ""); return err }},
		{"SaveSetup differing case", func() error { _, err := SaveSetup(" OpenAI ", "  "); return err }},
		{"SaveProviderMetadata", func() error { _, err := SaveProviderMetadata("openai"); return err }},
	} {
		t.Run(call.name, func(t *testing.T) {
			isolate(t)
			writeConfig(t, richConfig)
			if err := call.fn(); err != nil {
				t.Fatal(err)
			}
			cfg, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			if cfg.LLM.APIKey != "sk-old" || cfg.LLM.Model != "gpt-5.1" {
				t.Fatalf("file key/model lost: %+v", cfg.LLM)
			}
		})
	}
}

func TestEmptyKeyDropsOldProvidersKeyOnSwitch(t *testing.T) {
	isolate(t)
	writeConfig(t, richConfig)
	if _, err := SaveProviderMetadata(ProviderChatGPT); err != nil {
		t.Fatal(err)
	}
	body := readCfgFile(t)
	if strings.Contains(body, "sk-old") || strings.Contains(body, "api_key") || strings.Contains(body, "gpt-5.1") || strings.Contains(body, "example.com") {
		t.Fatalf("old provider data survived:\n%s", body)
	}
	if !strings.Contains(body, `provider = "chatgpt"   # trailing comment`) || !strings.Contains(body, "timeout_seconds = 42") {
		t.Fatalf("unexpected body:\n%s", body)
	}
	// Re-activating the same provider is a byte-for-byte no-op.
	if _, err := SaveProviderMetadata(ProviderChatGPT); err != nil {
		t.Fatal(err)
	}
	if again := readCfgFile(t); again != body {
		t.Fatalf("idempotent re-save changed file:\n%s", again)
	}
}

func TestSaveCreatesFileFromNothingAndAddsTable(t *testing.T) {
	isolate(t)
	if _, err := SaveSetup("vercel", "k"); err != nil {
		t.Fatal(err)
	}
	if got, want := readCfgFile(t), "[llm]\nprovider = \"vercel\"\napi_key = \"k\"\n"; got != want {
		t.Fatalf("got %q want %q", got, want)
	}
	isolate(t)
	writeConfig(t, "# only theme\n[theme]\npreset = \"light\"\n")
	if _, err := SaveSetup("vercel", "k"); err != nil {
		t.Fatal(err)
	}
	want := "# only theme\n[theme]\npreset = \"light\"\n\n[llm]\nprovider = \"vercel\"\napi_key = \"k\"\n"
	if got := readCfgFile(t); got != want {
		t.Fatalf("got %q want %q", got, want)
	}
}

func TestSaveFallbackBacksUpAndNotifies(t *testing.T) {
	isolate(t)
	const orig = "# my notes\nllm.provider = \"openai\"\nllm.api_key = \"sk-old\"\n\n[theme]\npreset = \"light\"\n"
	writeConfig(t, orig)
	res, err := SaveSetupResult("anthropic", "sk-new")
	if err != nil {
		t.Fatal(err)
	}
	if res.Backup == "" || res.Notice == "" || !strings.Contains(res.Notice, res.Backup) {
		t.Fatalf("missing backup/notice: %+v", res)
	}
	b, err := os.ReadFile(res.Backup)
	if err != nil || string(b) != orig {
		t.Fatalf("backup content = %q, err %v", b, err)
	}
	if st, _ := os.Stat(res.Backup); st.Mode().Perm() != 0o600 {
		t.Fatalf("backup perm = %v", st.Mode().Perm())
	}
	if filepath.Dir(res.Backup) != filepath.Dir(res.Path) {
		t.Fatalf("backup not next to config: %s", res.Backup)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.LLM.Provider != ProviderAnthropic || cfg.LLM.APIKey != "sk-new" || cfg.Theme.Preset != "light" {
		t.Fatalf("cfg after fallback = %+v", cfg)
	}
	// A second fallback in the same second must not clobber the first backup.
	writeBackupCollisionCheck(t, res.Path, res.Backup)
}

func writeBackupCollisionCheck(t *testing.T, path, first string) {
	t.Helper()
	second, err := writeBackup(path, []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if second == first {
		t.Fatal("backup overwritten")
	}
}

func TestSaveFallbackNoopKeepsDottedKeyFile(t *testing.T) {
	isolate(t)
	const orig = "llm.provider = \"openai\"\nllm.api_key = \"sk-old\"\n"
	writeConfig(t, orig)
	if _, err := SaveProviderMetadataResult("openai"); err != nil {
		t.Fatal(err)
	}
	// Nothing to change: no rewrite, no backup, bytes identical.
	if got := readCfgFile(t); got != orig {
		t.Fatalf("no-op rewrote file: %q", got)
	}
	ents, _ := os.ReadDir(filepath.Dir(Path()))
	if len(ents) != 1 {
		t.Fatalf("stray files: %v", ents)
	}
}

func TestSaveMalformedLeftUnmodifiedNoBackup(t *testing.T) {
	isolate(t)
	const bad = "[llm\nprovider = "
	writeConfig(t, bad)
	if _, err := SaveProviderMetadata("openai"); err == nil {
		t.Fatal("expected error")
	}
	if got := readCfgFile(t); got != bad {
		t.Fatalf("modified: %q", got)
	}
	ents, _ := os.ReadDir(filepath.Dir(Path()))
	if len(ents) != 1 {
		t.Fatalf("stray files: %v", ents)
	}
}

func TestSaveSetupOverwriteSameProviderRotatesKeyOnly(t *testing.T) {
	isolate(t)
	writeConfig(t, richConfig)
	if _, err := SaveSetup("openai", `new"key\`); err != nil {
		t.Fatal(err)
	}
	want := strings.Replace(richConfig, `api_key = "sk-old"`, `api_key = "new\"key\\"`, 1)
	if got := readCfgFile(t); got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
}

func TestSaveNeverPersistsEnvValuesWithRichFile(t *testing.T) {
	isolate(t)
	writeConfig(t, richConfig)
	t.Setenv("MARGINCTL_LLM_API_KEY", "ENV-SECRET")
	t.Setenv("MARGINCTL_LLM_MODEL", "env-model")
	t.Setenv("MARGINCTL_LLM_TIMEOUT", "77")
	if _, err := SaveProviderMetadata("openai"); err != nil {
		t.Fatal(err)
	}
	if got := readCfgFile(t); got != richConfig {
		t.Fatalf("no-op save changed file or leaked env:\n%s", got)
	}
}
