package state

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/ShayCichocki/marginctl/internal/fsutil"
)

func covManager(t *testing.T) *Manager {
	t.Helper()
	m := New(t.TempDir())
	if err := m.Initialize(); err != nil {
		t.Fatal(err)
	}
	return m
}

func covNamesOf(files []StateFile) string {
	var n []string
	for _, f := range files {
		n = append(n, f.Name)
	}
	return strings.Join(n, ",")
}

func TestCovByTypeCategoriesTimelineThreads(t *testing.T) {
	m := covManager(t)
	for _, name := range []string{"Zed", "Alice"} {
		if _, err := m.CreateStateFile(name, StateCharacter); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.CreateStateFile("Paris", StateLocation); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateCategory("Magic"); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateCategory("Empty"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateStateFileInCategory("Fireball", StateCustom, "Magic"); err != nil {
		t.Fatal(err)
	}

	if got := covNamesOf(m.ByType(StateCharacter)); got != "Alice,Zed" {
		t.Fatalf("characters = %q (sorted by name)", got)
	}
	if got := covNamesOf(m.ByType(StateLocation)); got != "Paris" {
		t.Fatalf("locations = %q", got)
	}
	custom := m.ByType(StateCustom)
	if len(custom) == 0 {
		t.Fatal("no custom files")
	}
	var found bool
	for _, f := range custom {
		if f.Name == "Fireball" && f.Category == "Magic" {
			found = true
		}
	}
	if !found {
		t.Fatalf("custom = %+v", custom)
	}
	if got := m.ByType(StateType(99)); len(got) != 0 {
		t.Fatalf("unknown type returned %v", got)
	}

	cats := strings.Join(m.Categories(), ",")
	for _, want := range []string{"characters", "locations", "Magic", "Empty"} {
		if !strings.Contains(","+cats+",", ","+want+",") {
			t.Errorf("Categories() = %q missing %q", cats, want)
		}
	}

	if tl := m.Timeline(); tl == nil || tl.Type != StateTimeline || filepath.Base(tl.Path) != "timeline.md" {
		t.Fatalf("Timeline() = %+v", tl)
	}
	if th := m.OpenThreads(); th == nil || th.Type != StateThreads || filepath.Base(th.Path) != "open_threads.md" {
		t.Fatalf("OpenThreads() = %+v", th)
	}
	if got := m.All(); len(got) < 5 {
		t.Fatalf("All() = %d files", len(got))
	}
	// Sorted by type first: timeline, character, location, threads, custom.
	all := m.All()
	for i := 1; i < len(all); i++ {
		if all[i-1].Type > all[i].Type {
			t.Fatalf("All() not sorted by type: %+v", all)
		}
	}
}

func TestCovTimelineAndThreadsNilWhenAbsent(t *testing.T) {
	m := New(t.TempDir())
	if m.Exists() || m.Timeline() != nil || m.OpenThreads() != nil || len(m.Categories()) != 0 || len(m.All()) != 0 {
		t.Fatal("uninitialized manager should report nothing")
	}
	if got := m.ByType(StateCharacter); len(got) != 0 {
		t.Fatalf("ByType on empty = %v", got)
	}
}

func TestCovStateTypeString(t *testing.T) {
	want := map[StateType]string{StateTimeline: "Timeline", StateCharacter: "Characters", StateLocation: "Locations", StateThreads: "Threads", StateCustom: "Custom", StateType(42): "Unknown"}
	for ty, s := range want {
		if ty.String() != s {
			t.Errorf("%d.String() = %q, want %q", ty, ty.String(), s)
		}
	}
}

func TestCovEntryExists(t *testing.T) {
	m := covManager(t)
	if _, err := m.CreateStateFile("Alice", StateCharacter); err != nil {
		t.Fatal(err)
	}
	if err := m.CreateCategory("Magic"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateStateFileInCategory("Fireball", StateCustom, "Magic"); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		ty   StateType
		cat  string
		ent  string
		want bool
	}{
		{"existing character", StateCharacter, "", "Alice", true},
		{"missing character", StateCharacter, "", "Bob", false},
		{"wrong type", StateLocation, "", "Alice", false},
		{"existing custom", StateCustom, "Magic", "Fireball", true},
		{"custom wrong category", StateCustom, "Other", "Fireball", false},
		{"custom no category", StateCustom, "", "Fireball", false},
		{"unnamed", StateCharacter, "", "", false},
		{"traversal-looking name is just a different name", StateCharacter, "", "../../Alice", false},
		{"timeline type has no entries", StateTimeline, "", "timeline", false},
	}
	for _, tc := range tests {
		if got := m.EntryExists(tc.ty, tc.cat, tc.ent); got != tc.want {
			t.Errorf("%s: EntryExists = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestCovEntryExistsIgnoresDirectoryAndSymlink(t *testing.T) {
	m := covManager(t)
	dir := filepath.Join(m.root, "state", "characters")
	if err := os.Mkdir(filepath.Join(dir, "Dirlike.md"), 0755); err != nil {
		t.Fatal(err)
	}
	if m.EntryExists(StateCharacter, "", "Dirlike") {
		t.Fatal("a directory counts as an entry")
	}
	target := filepath.Join(t.TempDir(), "t.md")
	if err := os.WriteFile(target, []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "Linked.md")); err != nil {
		t.Skip("symlinks unsupported:", err)
	}
	if m.EntryExists(StateCharacter, "", "Linked") {
		t.Fatal("a symlink counts as an entry")
	}
}

func TestCovVoiceGuideReadWriteBackupAndMode(t *testing.T) {
	m := covManager(t)
	def, err := m.ReadVoiceGuide()
	if err != nil {
		t.Fatal(err)
	}
	if def != DefaultVoiceGuideContent() {
		t.Fatalf("initialized voice guide differs from default content")
	}
	path := filepath.Join(m.root, "state", "style", "voice.md")
	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, 0640); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.WriteVoiceGuide("# Voice v2\n"); err != nil {
		t.Fatal(err)
	}
	if got, _ := m.ReadVoiceGuide(); got != "# Voice v2\n" {
		t.Fatalf("voice = %q", got)
	}
	bak, err := os.ReadFile(path + ".bak")
	if err != nil {
		t.Fatalf("no .bak created: %v", err)
	}
	if string(bak) != def {
		t.Fatal(".bak does not hold the prior voice guide")
	}
	if runtime.GOOS != "windows" {
		for _, p := range []string{path, path + ".bak"} {
			info, err := os.Stat(p)
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0640 {
				t.Errorf("%s mode = %v, want preserved 0640", filepath.Base(p), info.Mode().Perm())
			}
		}
	}
	// A second write rotates the backup to the previous content.
	if err := m.WriteVoiceGuide("# Voice v3\n"); err != nil {
		t.Fatal(err)
	}
	if bak, _ := os.ReadFile(path + ".bak"); string(bak) != "# Voice v2\n" {
		t.Fatalf(".bak after second write = %q", bak)
	}
	// .bak files are not surfaced as state entries.
	for _, f := range m.All() {
		if strings.HasSuffix(f.Path, ".bak") {
			t.Fatalf("backup listed as state file: %s", f.Path)
		}
	}
}

func TestCovWriteVoiceGuideCreatesMissingStyleDir(t *testing.T) {
	m := New(t.TempDir())
	if err := m.WriteVoiceGuide("fresh"); err != nil {
		t.Fatal(err)
	}
	if got, err := m.ReadVoiceGuide(); err != nil || got != "fresh" {
		t.Fatalf("voice = %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(m.root, "state", "style", "voice.md.bak")); !os.IsNotExist(err) {
		t.Fatal("a .bak was created for a brand-new guide")
	}
}

func TestCovReadVoiceGuideMissing(t *testing.T) {
	m := New(t.TempDir())
	if _, err := m.ReadVoiceGuide(); err == nil {
		t.Fatal("expected error with no voice guide")
	}
}

func TestCovWriteVoiceGuideRejectsSymlinkedStyleDir(t *testing.T) {
	m := covManager(t)
	styleDir := filepath.Join(m.root, "state", "style")
	if err := os.RemoveAll(styleDir); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.Symlink(outside, styleDir); err != nil {
		t.Skip("symlinks unsupported:", err)
	}
	if err := m.WriteVoiceGuide("pwned"); !errors.Is(err, fsutil.ErrUnsafePath) {
		t.Fatalf("WriteVoiceGuide err = %v, want ErrUnsafePath", err)
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("wrote through symlink: %v", entries)
	}
	if _, err := m.ReadVoiceGuide(); !errors.Is(err, fsutil.ErrUnsafePath) {
		t.Fatalf("ReadVoiceGuide err = %v, want ErrUnsafePath", err)
	}
}

func TestCovWriteVoiceGuideRejectsSymlinkedVoiceFile(t *testing.T) {
	m := covManager(t)
	voice := filepath.Join(m.root, "state", "style", "voice.md")
	if err := os.Remove(voice); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "victim.md")
	if err := os.WriteFile(outside, []byte("victim"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, voice); err != nil {
		t.Skip("symlinks unsupported:", err)
	}
	if err := m.WriteVoiceGuide("pwned"); !errors.Is(err, fsutil.ErrUnsafePath) {
		t.Fatalf("err = %v, want ErrUnsafePath", err)
	}
	if b, _ := os.ReadFile(outside); string(b) != "victim" {
		t.Fatalf("victim modified: %q", b)
	}
}

func TestCovWriteVoiceGuideRejectsSymlinkedStateDir(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "state")); err != nil {
		t.Skip("symlinks unsupported:", err)
	}
	m := New(root)
	if err := m.WriteVoiceGuide("pwned"); err == nil {
		t.Fatal("write through symlinked state/ succeeded")
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("wrote through symlink: %v", entries)
	}
}
