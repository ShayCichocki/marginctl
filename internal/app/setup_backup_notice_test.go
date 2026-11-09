package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ShayCichocki/marginctl/internal/config"
)

// When the existing config.toml has a shape the surgical [llm] editor will not
// touch (here a top-level dotted llm.* key), :setup falls back to a full
// rewrite and keeps a backup. The user must be told where the original went.
func TestSetupFallbackSurfacesBackupNotice(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	for _, k := range []string{"MARGINCTL_LLM_PROVIDER", "MARGINCTL_LLM_API_KEY", "MARGINCTL_LLM_MODEL", "MARGINCTL_LLM_ENDPOINT", "ANTHROPIC_API_KEY", "OPENAI_API_KEY", "AI_GATEWAY_API_KEY", "VERCEL_AI_GATEWAY_API_KEY"} {
		t.Setenv(k, "")
	}
	path := config.Path()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# mine\nllm.provider = \"openai\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := New()
	m.finishSetup(config.ProviderAnthropic, "sk-test")
	if m.err != nil {
		t.Fatalf("setup failed: %v", m.err)
	}
	if !strings.Contains(m.notice, "Config rewritten") || !strings.Contains(m.notice, ".bak") {
		t.Fatalf("notice does not mention the backup: %q", m.notice)
	}
	if n, ok := m.notifications.Current(); !ok || !strings.Contains(n.Details, "Original saved to") || !strings.Contains(n.Details, path+".bak") {
		t.Fatalf("notification details lack the full backup path: %+v", n)
	}
	if matches, _ := filepath.Glob(path + ".bak*"); len(matches) != 1 {
		t.Fatalf("expected exactly one backup next to config, got %v", matches)
	}
}
