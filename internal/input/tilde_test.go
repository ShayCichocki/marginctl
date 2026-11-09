package input

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCompletePathTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if err := os.WriteFile(filepath.Join(home, "papers.md"), nil, 0600); err != nil {
		t.Fatal(err)
	}

	m := New()
	m.SetValue("~/pa")
	if !m.CompletePath() || m.Value() != filepath.Join(home, "papers.md") {
		t.Fatalf("~/pa -> %q", m.Value())
	}

	// A trailing separator lists the home directory itself.
	m.SetValue("~/")
	if !m.CompletePath() || m.Value() != filepath.Join(home, "papers.md") {
		t.Fatalf("~/ -> %q", m.Value())
	}

	// Bare ~ lists the home directory's contents (never its siblings).
	m.SetValue("~")
	want := home + string(filepath.Separator)
	if !m.CompletePath() || !strings.HasPrefix(m.Value(), want) {
		t.Fatalf("~ -> %q, want prefix %q", m.Value(), want)
	}
}

func TestCompletePathTildeHomeUnresolvable(t *testing.T) {
	t.Setenv("HOME", "")
	t.Setenv("USERPROFILE", "")
	t.Setenv("home", "")
	m := New()
	m.SetValue("~/pa")
	if m.CompletePath() {
		t.Fatalf("completion should fail without a home directory, got %q", m.Value())
	}
	if m.Value() != "~/pa" {
		t.Fatalf("value changed: %q", m.Value())
	}
}
