package main

import (
	"strings"
	"testing"
)

// 5.2 — the usage text documents every accepted flag and invocation form.
func TestUsageMentionsFlagsAndForms(t *testing.T) {
	u := usage()
	for _, want := range []string{"--help", "--version", "<dir>", "<file.md>", ":setup", ":help", ":connect openai", ":connect openai --device", ":auth", ":disconnect", "ChatGPT OAuth", "private", "opencode"} {
		if !strings.Contains(u, want) {
			t.Errorf("usage missing %q", want)
		}
	}
}

// 5.2 — an ldflags-stamped version wins; otherwise a sane fallback, never empty.
func TestResolveVersion(t *testing.T) {
	orig := version
	defer func() { version = orig }()

	version = "v1.2.3"
	if got := resolveVersion(); got != "v1.2.3" {
		t.Fatalf("resolveVersion = %q, want stamped v1.2.3", got)
	}

	version = ""
	if got := resolveVersion(); got == "" {
		t.Fatal("resolveVersion must never be empty")
	}
}
