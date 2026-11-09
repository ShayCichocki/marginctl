package app

import (
	"fmt"
	"os"
	"testing"

	"github.com/ShayCichocki/marginctl/internal/editor"
)

// TestMain points every user-level directory at a throwaway location so no test
// can read or write the developer's real config, sessions, auth or swap files,
// and swaps the editor's OS clipboard bridge for an in-memory one so yank/paste
// tests never touch the developer's real clipboard.
func TestMain(m *testing.M) {
	editor.UseMemoryClipboard()
	dir, err := os.MkdirTemp("", "marginctl-test-home-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	for _, k := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_CACHE_HOME", "XDG_STATE_HOME", "APPDATA", "LOCALAPPDATA"} {
		_ = os.Setenv(k, dir)
	}
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
