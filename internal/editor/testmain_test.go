package editor

import (
	"os"
	"testing"
)

// TestMain swaps the OS clipboard bridge for an in-memory one so no test in this
// package reads or overwrites the developer's real clipboard. Tests that need
// specific clipboard behavior still override the seam per test.
func TestMain(m *testing.M) {
	UseMemoryClipboard()
	os.Exit(m.Run())
}
