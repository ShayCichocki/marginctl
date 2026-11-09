package input

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompletePathBareTildeIgnoresSiblings(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "alice")
	for _, d := range []string{home, filepath.Join(root, "alice-old")} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	m := New()
	m.SetValue("~")
	m.CompletePath()
	if strings.Contains(m.Value(), "alice-old") {
		t.Fatalf("~ completed to sibling: %q", m.Value())
	}
}
