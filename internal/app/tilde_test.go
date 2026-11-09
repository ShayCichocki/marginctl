package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewProjectPromptExpandsTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	wd := t.TempDir()
	t.Chdir(wd)

	m := New()
	m = openPathPrompt(m, "new")
	m = typeText(m, "~/novel")
	m = pressEnter(m)
	if m.err != nil {
		t.Fatalf("new project: %v", m.err)
	}
	if info, err := os.Stat(filepath.Join(home, "novel", "chapters")); err != nil || !info.IsDir() {
		t.Fatalf("project not created under HOME: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(wd, "~")); err == nil {
		t.Fatal("literal ~ directory created in working directory")
	}
}

func TestWriteCommandExpandsTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Chdir(t.TempDir())

	m := New()
	m.editor.SetContent("hello tilde\n")
	_ = m.executeCommand("w ~/x.md")
	if m.err != nil {
		t.Fatalf("save: %v", m.err)
	}
	data, err := os.ReadFile(filepath.Join(home, "x.md"))
	if err != nil || string(data) != "hello tilde\n" {
		t.Fatalf("not saved to HOME: %q %v", data, err)
	}
	if _, err := os.Lstat("~"); err == nil {
		t.Fatal("literal ~ created in working directory")
	}
}
