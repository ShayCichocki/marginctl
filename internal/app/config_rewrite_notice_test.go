package app

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/ShayCichocki/marginctl/internal/auth"
	"github.com/ShayCichocki/marginctl/internal/config"
)

var backupNameRE = regexp.MustCompile(`config\.toml\.bak-[0-9T]+`)

// writeDottedConfig seeds a config whose top-level dotted llm.* key cannot be
// edited in place, forcing the rewrite-with-backup fallback.
func writeDottedConfig(t *testing.T, cfgPath string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte("# my notes\nllm.provider = \"openai\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func stripWS(s string) string {
	return strings.Join(strings.Fields(s), "")
}

// assertBackupVisibleAndInspectable renders the real View at 80 and 120 columns
// and checks the backup file name is on screen, then that :notifications shows
// the absolute backup path.
func assertBackupVisibleAndInspectable(t *testing.T, m Model, cfgPath string) {
	t.Helper()
	for _, w := range []int{80, 120} {
		m.width, m.height = w, 24
		m.updateLayout()
		view := m.View()
		if !backupNameRE.MatchString(view) {
			t.Fatalf("width %d: backup file name not visible in View:\n%s", w, view)
		}
	}
	matches, _ := filepath.Glob(cfgPath + ".bak-*")
	if len(matches) != 1 {
		t.Fatalf("backups = %v, want exactly one", matches)
	}
	m.executeCommand("notifications")
	if got := stripWS(m.results.View()); !strings.Contains(got, stripWS(matches[0])) {
		t.Fatalf(":notifications missing absolute backup path %q in:\n%s", matches[0], m.results.View())
	}
}

func TestSetupFallbackBackupNoticeVisibleInBar(t *testing.T) {
	cfgPath := isolateConfigEnv(t)
	writeDottedConfig(t, cfgPath)
	m := New()
	m.width, m.height = 80, 24
	m = update(m, keyRunes(":"))
	m = update(m, keyRunes("setup!"))
	m = pressEnter(m)
	m = update(m, keyRunes("1"))
	m = pressEnter(m)
	m = update(m, keyRunes("sk-test-key"))
	m = pressEnter(m)

	assertBackupVisibleAndInspectable(t, m, cfgPath)
}

func TestConnectActivationFallbackBackupNoticeVisible(t *testing.T) {
	cfgPath := isolateConfigEnv(t)
	writeDottedConfig(t, cfgPath)
	m := New()
	activation, err := auth.ActivateChatGPT(config.SaveProviderMetadataResult, "")
	if err != nil {
		t.Fatal(err)
	}
	if activation.Backup == "" {
		t.Fatalf("activation dropped backup: %+v", activation)
	}
	next, _ := m.handleConnectResult(connectResultMsg{attemptID: m.connectAttemptID, provider: config.ProviderChatGPT, result: activation})
	m = asModel(next)

	assertBackupVisibleAndInspectable(t, m, cfgPath)
}

func TestConfigRewriteNotificationDoesNotStickInBar(t *testing.T) {
	cfgPath := isolateConfigEnv(t)
	writeDottedConfig(t, cfgPath)
	m := New()
	activation, err := auth.ActivateChatGPT(config.SaveProviderMetadataResult, "")
	if err != nil {
		t.Fatal(err)
	}
	next, _ := m.handleConnectResult(connectResultMsg{attemptID: m.connectAttemptID, provider: config.ProviderChatGPT, result: activation})
	m = asModel(next)
	m.width, m.height = 80, 24
	m.updateLayout()
	// Next key clears the transient notice; the warning must not linger in the
	// bar, and a later message must show normally.
	m = update(m, keyRunes("j"))
	if v := m.View(); backupNameRE.MatchString(v) {
		t.Fatalf("rewrite warning pinned the bar after a keypress:\n%s", v)
	}
	m.setNotice("later message")
	if v := m.View(); !strings.Contains(v, "later message") {
		t.Fatalf("later notice masked:\n%s", v)
	}
}
