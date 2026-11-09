package fsutil

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

func FuzzSanitizeName(f *testing.F) {
	f.Add("../../private\x1b[31m.md")
	f.Add("👩🏽‍🚀 chapter\r\nignored")
	f.Fuzz(func(t *testing.T, name string) {
		if !utf8.ValidString(name) || len(name) > 4096 {
			t.Skip()
		}
		got, err := SanitizeName(name)
		if err != nil {
			return
		}
		if got == "" || filepath.Base(got) != got || strings.HasPrefix(got, ".") || utf8.RuneCountInString(got) > maxNameLen {
			t.Fatalf("unsafe sanitized name %q", got)
		}
		if strings.ContainsAny(got, "/\\\r\n") || strings.ContainsFunc(got, unicode.IsControl) {
			t.Fatalf("sanitized name contains control or separator: %q", got)
		}
	})
}

func FuzzContainedPath(f *testing.F) {
	f.Add("chapters/001.md")
	f.Add("../../outside")
	f.Add("state/../chapters/new.md")
	f.Fuzz(func(t *testing.T, relative string) {
		if !utf8.ValidString(relative) || strings.IndexByte(relative, 0) >= 0 || len(relative) > 2048 {
			t.Skip()
		}
		root := t.TempDir()
		candidate := filepath.Join(root, relative)
		got, err := ContainedPath(root, candidate, true)
		if err != nil {
			return
		}
		rel, relErr := filepath.Rel(root, got)
		if relErr != nil || rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			t.Fatalf("accepted escaping path %q as %q", candidate, got)
		}
	})
}
