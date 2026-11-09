package fsutil

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteFilePreservesRestrictiveModes(t *testing.T) {
	for _, mode := range []os.FileMode{0600, 0640} {
		t.Run(mode.String(), func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "chapter.md")
			if err := os.WriteFile(path, []byte("old"), mode); err != nil {
				t.Fatal(err)
			}
			if err := WriteFileWithBackup(path, []byte("new"), 0644); err != nil {
				t.Fatal(err)
			}
			for _, p := range []string{path, path + ".bak"} {
				info, err := os.Stat(p)
				if err != nil {
					t.Fatal(err)
				}
				if got := info.Mode().Perm(); got != mode {
					t.Fatalf("%s mode = %o, want %o", p, got, mode)
				}
			}
		})
	}
}
