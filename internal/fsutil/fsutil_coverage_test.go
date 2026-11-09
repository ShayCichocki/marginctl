package fsutil

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func covWrite(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func covNoTemps(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Fatalf("leftover temp file %s", e.Name())
		}
	}
}

func TestCovWriteIntoReadOnlyDirLeavesOriginalUntouched(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dir := t.TempDir()
	target := filepath.Join(dir, "ch.md")
	covWrite(t, target, "original", 0644)
	if err := os.Chmod(dir, 0555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0755) })

	if err := WriteFile(target, []byte("new"), 0644); err == nil {
		t.Fatal("WriteFile into read-only dir succeeded")
	}
	if err := WriteFilePrivate(target, []byte("new")); err == nil {
		t.Fatal("WriteFilePrivate into read-only dir succeeded")
	}
	if err := WriteFileWithBackup(target, []byte("new"), 0644); err == nil {
		t.Fatal("WriteFileWithBackup into read-only dir succeeded")
	}
	if b, _ := os.ReadFile(target); string(b) != "original" {
		t.Fatalf("original changed: %q", b)
	}
	if _, err := os.Stat(target + ".bak"); !os.IsNotExist(err) {
		t.Fatalf("backup appeared despite failed write: %v", err)
	}
	covNoTemps(t, dir)
}

func TestCovWriteFileFailedRenameLeavesNoTemp(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "occupied")
	if err := os.MkdirAll(filepath.Join(target, "child"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := WriteFile(target, []byte("x"), 0644); err == nil {
		t.Fatal("renaming a file over a non-empty directory succeeded")
	}
	covNoTemps(t, dir)
}

func TestCovWriteFileBackupPreservesModeAndOriginalContent(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "ch.md")
	covWrite(t, target, "v1", 0640)
	if err := WriteFileWithBackup(target, []byte("v2"), 0644); err != nil {
		t.Fatal(err)
	}
	for path, want := range map[string]string{target: "v2", target + ".bak": "v1"} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0640 {
			t.Errorf("%s mode = %v, want 0640", filepath.Base(path), info.Mode().Perm())
		}
		if b, _ := os.ReadFile(path); string(b) != want {
			t.Errorf("%s = %q, want %q", filepath.Base(path), b, want)
		}
	}
	covNoTemps(t, dir)
}

func TestCovWriteFileMissingParentFails(t *testing.T) {
	if err := WriteFile(filepath.Join(t.TempDir(), "nope", "f"), []byte("x"), 0644); err == nil {
		t.Fatal("expected error for missing parent directory")
	}
}

func TestCovFingerprintSameFileHardlink(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	covWrite(t, a, "data", 0644)
	if err := os.Link(a, b); err != nil {
		t.Skip("hard links unsupported:", err)
	}
	fa, err := FileFingerprint(a)
	if err != nil {
		t.Fatal(err)
	}
	fb, err := FileFingerprint(b)
	if err != nil {
		t.Fatal(err)
	}
	if !fa.SameFile(fb) {
		t.Fatal("hard links should be the same file")
	}
	if fa.CanonicalPath == fb.CanonicalPath {
		t.Fatal("hard links must keep distinct canonical paths")
	}
	other := filepath.Join(dir, "c")
	covWrite(t, other, "data", 0644)
	fc, _ := FileFingerprint(other)
	if fa.SameFile(fc) {
		t.Fatal("identical content in a different inode must not be the same file")
	}
}

func TestCovFingerprintSameFileSymlinkAndMissing(t *testing.T) {
	dir := t.TempDir()
	real, link := filepath.Join(dir, "real"), filepath.Join(dir, "link")
	covWrite(t, real, "data", 0644)
	if err := os.Symlink(real, link); err != nil {
		t.Skip("symlinks unsupported:", err)
	}
	fr, _ := FileFingerprint(real)
	fl, err := FileFingerprint(link)
	if err != nil {
		t.Fatal(err)
	}
	if !fr.SameFile(fl) || fr.CanonicalPath != fl.CanonicalPath {
		t.Fatalf("symlink should resolve to the same file: %q vs %q", fr.CanonicalPath, fl.CanonicalPath)
	}

	m1, err := FileFingerprint(filepath.Join(dir, "missing1"))
	if err != nil {
		t.Fatal(err)
	}
	m2, _ := FileFingerprint(filepath.Join(dir, "missing1"))
	if m1.Exists || m1.SameFile(m2) || m1.SameFile(fr) || fr.SameFile(m1) {
		t.Fatal("a nonexistent target is never the same file")
	}
	if !m1.Equal(m2) {
		t.Fatal("two views of the same missing path should be Equal")
	}
	m3, _ := FileFingerprint(filepath.Join(dir, "missing2"))
	if m1.Equal(m3) {
		t.Fatal("different missing paths must not be Equal")
	}
	if m1.Equal(fr) {
		t.Fatal("missing vs existing must not be Equal")
	}
}

func TestCovFingerprintEqualDetectsReplacementWithSameContent(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f")
	covWrite(t, p, "same", 0644)
	before, _ := FileFingerprint(p)
	if !before.Equal(before) {
		t.Fatal("fingerprint should equal itself")
	}
	// Replace the file atomically with identical content and mtime.
	if err := WriteFile(p, []byte("same"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, before.ModTime, before.ModTime); err != nil {
		t.Fatal(err)
	}
	after, _ := FileFingerprint(p)
	if after.Identity.Valid && before.Identity == after.Identity {
		t.Skip("inode reused; replacement not distinguishable here")
	}
	if after.Identity.Valid && before.Equal(after) {
		t.Fatal("replacement of the inode went undetected")
	}
	// A content change with preserved size and mtime is caught by the hash.
	covWrite(t, p, "diff", 0644)
	if err := os.Chtimes(p, after.ModTime, after.ModTime); err != nil {
		t.Fatal(err)
	}
	changed, _ := FileFingerprint(p)
	if after.Equal(changed) {
		t.Fatal("same-size same-mtime content change went undetected")
	}
}

func TestCovFingerprintRejectsDirectory(t *testing.T) {
	if _, err := FileFingerprint(t.TempDir()); err == nil {
		t.Fatal("fingerprinting a directory should fail")
	}
}

func TestCovUnderRootHelpers(t *testing.T) {
	root, err := CanonicalRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Write with backup, read back, mkdir nested.
	file := filepath.Join(root, "sub", "a.md")
	if err := MkdirAllUnderRoot(root, filepath.Dir(file), 0755); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileUnderRoot(root, file, []byte("1"), 0644, true); err != nil {
		t.Fatal(err)
	}
	if err := WriteFileUnderRoot(root, file, []byte("2"), 0644, true); err != nil {
		t.Fatal(err)
	}
	if b, err := ReadFileUnderRoot(root, file); err != nil || string(b) != "2" {
		t.Fatalf("read = %q, %v", b, err)
	}
	if b, _ := os.ReadFile(file + ".bak"); string(b) != "1" {
		t.Fatalf("backup = %q", b)
	}

	outside := t.TempDir()
	if err := WriteFileUnderRoot(root, filepath.Join(outside, "x"), []byte("x"), 0644, false); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("write outside root err = %v", err)
	}
	if err := MkdirAllUnderRoot(root, filepath.Join(outside, "d"), 0755); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("mkdir outside root err = %v", err)
	}
	if _, err := ReadFileUnderRoot(root, filepath.Join(root, "..", "etc")); err == nil {
		t.Fatal("read via .. succeeded")
	}
	if _, err := CanonicalRoot(file); err == nil {
		t.Fatal("CanonicalRoot accepted a regular file")
	}
	if _, err := CanonicalRoot(filepath.Join(root, "missing")); err == nil {
		t.Fatal("CanonicalRoot accepted a missing dir")
	}
}

func TestCovUnderRootRejectsSymlinkedLeafAndMkdirThroughLink(t *testing.T) {
	root, err := CanonicalRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	covWrite(t, filepath.Join(outside, "t"), "orig", 0644)
	if err := os.Symlink(filepath.Join(outside, "t"), filepath.Join(root, "leaf")); err != nil {
		t.Skip("symlinks unsupported:", err)
	}
	if err := WriteFileUnderRoot(root, filepath.Join(root, "leaf"), []byte("evil"), 0644, true); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("write through symlinked leaf err = %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(outside, "t")); string(b) != "orig" {
		t.Fatalf("symlink target modified: %q", b)
	}
	if err := os.Symlink(outside, filepath.Join(root, "dirlink")); err != nil {
		t.Fatal(err)
	}
	if err := MkdirAllUnderRoot(root, filepath.Join(root, "dirlink", "new"), 0755); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("mkdir through symlinked dir err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "new")); err == nil {
		t.Fatal("directory created outside root")
	}
}

func TestCovContainedPathMissingSemantics(t *testing.T) {
	root, _ := CanonicalRoot(t.TempDir())
	missing := filepath.Join(root, "a", "b", "c.md")
	if _, err := ContainedPath(root, missing, false); err == nil {
		t.Fatal("missing path should fail when allowMissing=false")
	}
	if got, err := ContainedPath(root, missing, true); err != nil || got != missing {
		t.Fatalf("allowMissing = %q, %v", got, err)
	}
	// A sibling directory sharing the root as a string prefix is outside.
	sibling := root + "-evil"
	if err := os.MkdirAll(sibling, 0755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sibling) })
	if _, err := ContainedPath(root, filepath.Join(sibling, "x"), true); !errors.Is(err, ErrUnsafePath) {
		t.Fatalf("prefix-sibling err = %v, want ErrUnsafePath", err)
	}
	if got, err := ContainedPath(root, root, false); err != nil || got != root {
		t.Fatalf("root itself = %q, %v", got, err)
	}
	// File used as a directory component.
	f := filepath.Join(root, "file")
	covWrite(t, f, "x", 0644)
	if _, err := ContainedPath(root, filepath.Join(f, "child"), true); err == nil {
		t.Fatal("path through a regular file should fail")
	}
}
