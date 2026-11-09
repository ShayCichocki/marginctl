package fsutil

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// WriteFilePrivate must force owner-only perms even over a pre-existing loose
// file (credential/sidecar invariant), unlike WriteFile which preserves mode.
func TestWriteFilePrivateEnforces0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits")
	}
	path := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(path, []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := WriteFilePrivate(path, []byte("new")); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Fatalf("perm = %o, want 0600", perm)
	}
	if data, _ := os.ReadFile(path); string(data) != "new" {
		t.Fatalf("content = %q, want new", data)
	}
}

// WriteFile must still preserve an existing file's mode (the manuscript
// "preserve modes" invariant) — the private variant must not have changed this.
func TestWriteFilePreservesExistingMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits")
	}
	path := filepath.Join(t.TempDir(), "doc.md")
	if err := os.WriteFile(path, []byte("x"), 0640); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(path, []byte("y"), 0600); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0640 {
		t.Fatalf("perm = %o, want preserved 0640", perm)
	}
}

func TestSanitizeNameWindowsHardening(t *testing.T) {
	cases := map[string]string{
		"a:b":      "ab",      // ':' (ADS / drive sep) dropped
		"a<b>c":    "abc",     // illegal chars dropped
		"na?me*":   "name",    // illegal chars dropped
		"trail. ":  "trail",   // trailing dot/space stripped
		"CON":      "_CON",    // reserved device name neutralized
		"con.md":   "_con.md", // reserved base, case-insensitive, extension kept
		"COM3":     "_COM3",   // COM1-9 reserved
		"LPT9.txt": "_LPT9.txt",
		"COM10":    "COM10", // not reserved (two digits)
		"normal":   "normal",
	}
	for in, want := range cases {
		got, err := SanitizeName(in)
		if err != nil {
			t.Errorf("SanitizeName(%q) error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("SanitizeName(%q) = %q, want %q", in, got, want)
		}
	}
}
