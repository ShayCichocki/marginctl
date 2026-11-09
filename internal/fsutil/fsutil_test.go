package fsutil

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestFileFingerprintCapturesContentAndIdentity(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "chapter.md")
	if err := os.WriteFile(path, []byte("first"), 0644); err != nil {
		t.Fatal(err)
	}
	first, err := FileFingerprint(path)
	if err != nil {
		t.Fatal(err)
	}
	if !first.Exists || first.Size != 5 || first.CanonicalPath == "" || first.SHA256 == ([32]byte{}) {
		t.Fatalf("incomplete fingerprint: %+v", first)
	}

	alias := filepath.Join(dir, ".", "chapter.md")
	aliased, err := FileFingerprint(alias)
	if err != nil {
		t.Fatal(err)
	}
	if !first.SameFile(aliased) || !first.Equal(aliased) {
		t.Fatalf("path alias not recognized: %+v vs %+v", first, aliased)
	}

	mtime := first.ModTime
	if err := os.WriteFile(path, []byte("other"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	changed, err := FileFingerprint(path)
	if err != nil {
		t.Fatal(err)
	}
	if first.Equal(changed) || first.SHA256 == changed.SHA256 {
		t.Fatal("same-mtime content change was not detected")
	}
}

func TestFileFingerprintAliasesAndMissing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "chapter.md")
	if err := os.WriteFile(path, []byte("text"), 0644); err != nil {
		t.Fatal(err)
	}
	base, err := FileFingerprint(path)
	if err != nil {
		t.Fatal(err)
	}
	for name, makeAlias := range map[string]func(string) error{
		"symlink":  func(alias string) error { return os.Symlink(path, alias) },
		"hardlink": func(alias string) error { return os.Link(path, alias) },
	} {
		t.Run(name, func(t *testing.T) {
			alias := filepath.Join(dir, name+".md")
			if err := makeAlias(alias); err != nil {
				t.Fatal(err)
			}
			got, err := FileFingerprint(alias)
			if err != nil {
				t.Fatal(err)
			}
			if !base.SameFile(got) {
				t.Fatalf("%s not recognized as same file", name)
			}
		})
	}

	missing, err := FileFingerprint(filepath.Join(dir, "new.md"))
	if err != nil {
		t.Fatal(err)
	}
	canonicalDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if missing.Exists || missing.CanonicalPath != filepath.Join(canonicalDir, "new.md") {
		t.Fatalf("missing fingerprint = %+v", missing)
	}
}

func TestWriteFileOverwrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "chapter.md")

	if err := os.WriteFile(path, []byte("old contents"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := WriteFile(path, []byte("new contents"), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new contents" {
		t.Fatalf("content = %q, want %q", got, "new contents")
	}

	assertNoLeftovers(t, dir, "chapter.md")
}

func TestWriteFileNew(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "new.md")

	if err := WriteFile(path, []byte("hello"), 0600); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Fatalf("perm = %o, want 600", perm)
	}
	assertNoLeftovers(t, dir, "new.md")
}

func TestWriteFileWithBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "voice.md")

	if err := os.WriteFile(path, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := WriteFileWithBackup(path, []byte("updated"), 0644); err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "updated" {
		t.Fatalf("content = %q, want updated", got)
	}

	bak, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatalf("expected backup file: %v", err)
	}
	if string(bak) != "original" {
		t.Fatalf("backup = %q, want original", bak)
	}
}

func TestWriteFileWithBackupNoExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fresh.md")

	if err := WriteFileWithBackup(path, []byte("brand new"), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Fatalf("expected no .bak for a new file, stat err = %v", err)
	}
}

func TestSanitizeName(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{"plain", "Chapter One", "Chapter One", false},
		{"strips path separators", "../secrets", "secrets", false},
		{"strips nested traversal", "a/b/c", "abc", false},
		{"strips backslash", `a\b`, "ab", false},
		{"dot-dot alone rejected", "..", "", true},
		{"leading dots stripped", "...hidden", "hidden", false},
		{"first line only", "Villain\ndelete everything", "Villain", false},
		{"carriage return truncates", "Name\rrest", "Name", false},
		{"control chars dropped", "Ka\x00te\x07", "Kate", false},
		{"empty rejected", "   ", "", true},
		{"trims whitespace", "  Bob  ", "Bob", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SanitizeName(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("SanitizeName(%q) = %q, want error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("SanitizeName(%q) unexpected error: %v", tt.in, err)
			}
			if got != tt.want {
				t.Fatalf("SanitizeName(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestSanitizeNameClampsLength(t *testing.T) {
	long := strings.Repeat("é", 250) // multi-byte, longer than maxNameLen runes
	got, err := SanitizeName(long)
	if err != nil {
		t.Fatal(err)
	}
	if n := utf8.RuneCountInString(got); n > maxNameLen {
		t.Fatalf("length = %d runes, want <= %d", n, maxNameLen)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("clamp produced invalid UTF-8: %q", got)
	}
}

func TestContainedPathAllowsNormalContainedPaths(t *testing.T) {
	root, err := CanonicalRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "state")
	if err := MkdirAllUnderRoot(root, dir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "timeline.md")
	if err := WriteFileUnderRoot(root, path, []byte("timeline"), 0644, false); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadFileUnderRoot(root, path); err != nil || string(got) != "timeline" {
		t.Fatalf("ReadFileUnderRoot() = %q, %v", got, err)
	}
}

func TestContainedPathRejectsEscapeAndSymlinkParent(t *testing.T) {
	root, err := CanonicalRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if _, err := ContainedPath(root, filepath.Join(root, "..", "escape.md"), true); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("escape error = %v, want ErrUnsafePath", err)
	}
	link := filepath.Join(root, "linked")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileUnderRoot(root, filepath.Join(link, "escape.md"), []byte("no"), 0644, false); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("symlink error = %v, want ErrUnsafePath", err)
	}
}

// assertNoLeftovers fails if the directory contains anything other than the
// named files — in particular a leftover temp file from an aborted write.
func assertNoLeftovers(t *testing.T, dir string, allowed ...string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	ok := make(map[string]bool, len(allowed))
	for _, a := range allowed {
		ok[a] = true
	}
	for _, e := range entries {
		if !ok[e.Name()] {
			t.Fatalf("unexpected leftover file %q (contains .tmp fragment: %v)", e.Name(), strings.Contains(e.Name(), ".tmp"))
		}
	}
}

func TestExpandHome(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cases := map[string]string{
		"~": home, "~/x": filepath.Join(home, "x"), "~/a/b": filepath.Join(home, "a", "b"),
		"~user/x": "~user/x", "/abs/p": "/abs/p", "rel/p": "rel/p", "": "", "a/~/b": "a/~/b", "~x": "~x",
	}
	for in, want := range cases {
		got, err := ExpandHome(in)
		if err != nil || got != want {
			t.Errorf("ExpandHome(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}
