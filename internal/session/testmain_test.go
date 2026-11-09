package session

import (
	"fmt"
	"os"
	"testing"
)

// TestMain points every user-level directory at a throwaway location so no test
// can read or write the developer's real config, sessions, auth or swap files.
func TestMain(m *testing.M) {
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
