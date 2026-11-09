package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// isolateEnv keeps session/config/auth state out of the developer's real home.
func isolateEnv(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	for _, k := range []string{"HOME", "USERPROFILE", "XDG_CONFIG_HOME", "XDG_DATA_HOME", "XDG_STATE_HOME", "APPDATA"} {
		t.Setenv(k, home)
	}
	for _, k := range []string{
		"MARGINCTL_LLM_PROVIDER", "MARGINCTL_LLM_API_KEY", "MARGINCTL_LLM_MODEL",
		"MARGINCTL_LLM_ENDPOINT", "MARGINCTL_LLM_TIMEOUT", "ANTHROPIC_API_KEY",
		"OPENAI_API_KEY", "AI_GATEWAY_API_KEY", "VERCEL_AI_GATEWAY_API_KEY",
	} {
		t.Setenv(k, "")
	}
}

func run(t *testing.T, args ...string) (done bool, code int, out, errOut string) {
	t.Helper()
	var o, e bytes.Buffer
	m, done, code := prepare(args, &o, &e)
	if !done && m == nil {
		t.Fatal("prepare returned neither done nor a model")
	}
	return done, code, o.String(), e.String()
}

func TestPrepareHelpAndVersion(t *testing.T) {
	isolateEnv(t)
	for _, flag := range []string{"-h", "--help"} {
		done, code, out, errOut := run(t, flag)
		if !done || code != 0 || out != usage() || errOut != "" {
			t.Errorf("%s: done=%v code=%d err=%q", flag, done, code, errOut)
		}
	}
	orig := version
	defer func() { version = orig }()
	version = "v9.9.9"
	for _, flag := range []string{"-v", "--version"} {
		done, code, out, _ := run(t, flag)
		if !done || code != 0 || out != "marginctl v9.9.9\n" {
			t.Errorf("%s: done=%v code=%d out=%q", flag, done, code, out)
		}
	}
}

func TestPrepareUnknownFlagExits2WithUsageOnStderr(t *testing.T) {
	isolateEnv(t)
	for _, flag := range []string{"--bogus", "-x", "-"} {
		done, code, out, errOut := run(t, flag)
		if !done || code != 2 || out != "" || !strings.Contains(errOut, "unknown flag: "+flag) || !strings.Contains(errOut, "Usage:") {
			t.Errorf("%s: done=%v code=%d out=%q err=%q", flag, done, code, out, errOut)
		}
	}
}

func TestPrepareOpensEmptyDirFileAndProject(t *testing.T) {
	isolateEnv(t)

	// No argument: empty editor.
	if done, _, _, _ := run(t); done {
		t.Fatal("no-arg must start the editor")
	}

	root := t.TempDir()

	// Directory without chapters/: not a project, but a valid open.
	plain := filepath.Join(root, "plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	if done, code, _, errOut := run(t, plain); done {
		t.Fatalf("plain dir rejected: code=%d err=%q", code, errOut)
	}

	// Directory with chapters/: project.
	proj := filepath.Join(root, "novel")
	if err := os.MkdirAll(filepath.Join(proj, "chapters"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "chapters", "01.md"), []byte("# One\n\nHello.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if done, code, _, errOut := run(t, proj); done {
		t.Fatalf("project rejected: code=%d err=%q", code, errOut)
	}

	// Single file.
	file := filepath.Join(root, "note.md")
	if err := os.WriteFile(file, []byte("# Note\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if done, code, _, errOut := run(t, file); done {
		t.Fatalf("file rejected: code=%d err=%q", code, errOut)
	}
	// Opening must not modify the file.
	if b, _ := os.ReadFile(file); string(b) != "# Note\n" {
		t.Fatalf("file modified by open: %q", b)
	}
}

func TestPrepareOpenFailureExits1(t *testing.T) {
	isolateEnv(t)
	root := t.TempDir()
	// A project whose chapters/ is a symlink is rejected by containment rules.
	target := t.TempDir()
	proj := filepath.Join(root, "evil")
	if err := os.MkdirAll(proj, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(proj, "chapters")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	done, code, out, errOut := run(t, proj)
	if !done || code != 1 || out != "" || !strings.HasPrefix(errOut, "Error opening "+proj) {
		t.Fatalf("done=%v code=%d out=%q err=%q", done, code, out, errOut)
	}
}

func TestPrepareMissingPath(t *testing.T) {
	isolateEnv(t)
	missing := filepath.Join(t.TempDir(), "does-not-exist.md")
	done, code, _, errOut := run(t, missing)
	if !done || code != 1 || !strings.Contains(errOut, "Error opening") {
		t.Fatalf("code=%d err=%q", code, errOut)
	}
	// Opening a missing path must not create it.
	if _, err := os.Stat(missing); err == nil {
		t.Fatal("open created the missing file")
	}
}

func TestResolveVersionFallbackNeverEmptyOrDevelParen(t *testing.T) {
	orig := version
	defer func() { version = orig }()
	version = ""
	if v := resolveVersion(); v == "" || v == "(devel)" {
		t.Fatalf("resolveVersion = %q", v)
	}
}
