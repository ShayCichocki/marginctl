package state

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// state/ swapped for a symlink after the manager was created must be refused,
// whether it points outside the project or at chapters/.
func TestAppendToFileRejectsSymlinkedStateDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlinks")
	}
	for _, name := range []string{"outside", "chapters"} {
		t.Run(name, func(t *testing.T) {
			m, root := appendFixture(t)
			target := t.TempDir()
			if name == "chapters" {
				target = filepath.Join(root, "chapters")
			}
			victim := filepath.Join(target, "timeline.md")
			if err := os.WriteFile(victim, []byte("precious\n"), 0644); err != nil {
				t.Fatal(err)
			}
			if err := os.RemoveAll(filepath.Join(root, "state")); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, filepath.Join(root, "state")); err != nil {
				t.Fatal(err)
			}
			if err := m.AppendToFile("timeline.md", "PWNED"); err == nil {
				t.Fatal("append through symlinked state dir succeeded")
			}
			if b, _ := os.ReadFile(victim); string(b) != "precious\n" {
				t.Fatalf("victim modified: %q", b)
			}
		})
	}
}
