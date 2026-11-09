package state

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ShayCichocki/marginctl/internal/fsutil"
)

func TestStateRejectsSymlinkedRoot(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	if err := os.Symlink(outside, filepath.Join(root, "state")); err != nil {
		t.Fatal(err)
	}
	m := New(root)
	if err := m.Refresh(); !errors.Is(err, fsutil.ErrUnsafePath) {
		t.Fatalf("Refresh() error = %v, want ErrUnsafePath", err)
	}
}

func TestStateRejectsSymlinkedFileAndParent(t *testing.T) {
	root, outside := t.TempDir(), t.TempDir()
	m := New(root)
	if err := m.Initialize(); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(outside, "private.md")
	if err := os.WriteFile(target, []byte("private"), 0644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "state", "timeline-link.md")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Read(&StateFile{Path: link}); !errors.Is(err, fsutil.ErrUnsafePath) {
		t.Fatalf("Read() error = %v, want ErrUnsafePath", err)
	}
	parent := filepath.Join(root, "state", "linked-category")
	if err := os.Symlink(outside, parent); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateStateFileInCategory("escape", StateCustom, "linked-category"); !errors.Is(err, fsutil.ErrUnsafePath) {
		t.Fatalf("CreateStateFileInCategory() error = %v, want ErrUnsafePath", err)
	}
}

func TestStateNormalContainedReadWrite(t *testing.T) {
	root := t.TempDir()
	m := New(root)
	if err := m.Initialize(); err != nil {
		t.Fatal(err)
	}
	path, err := m.CreateStateFileWithBody("Ada", StateCharacter, "", "Inventor")
	if err != nil {
		t.Fatal(err)
	}
	got, err := m.Read(&StateFile{Path: path})
	if err != nil || !strings.Contains(got, "Inventor") {
		t.Fatalf("Read() = %q, %v", got, err)
	}
}

func TestInitializeIsAdditiveIdempotentAndRejectsCollisions(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "state", "style"), 0755); err != nil {
		t.Fatal(err)
	}
	custom := filepath.Join(root, "state", "timeline.md")
	if err := os.WriteFile(custom, []byte("keep me"), 0600); err != nil {
		t.Fatal(err)
	}
	m := New(root)
	if err := m.Initialize(); err != nil {
		t.Fatal(err)
	}
	if err := m.Initialize(); err != nil {
		t.Fatalf("repeat initialize: %v", err)
	}
	if got, _ := os.ReadFile(custom); string(got) != "keep me" {
		t.Fatalf("existing state overwritten: %q", got)
	}
	if _, err := os.Stat(filepath.Join(root, "state", "open_threads.md")); err != nil {
		t.Fatal("missing starter file:", err)
	}

	badRoot := t.TempDir()
	if err := os.MkdirAll(filepath.Join(badRoot, "state", "timeline.md"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := New(badRoot).Initialize(); err == nil {
		t.Fatal("directory collision must fail")
	}
}

// 4.2 — ParseFrontMatter splits a YAML-ish header into fields + body; a file with
// no header returns nil fields and the whole content.
func TestParseFrontMatter(t *testing.T) {
	fields, body := ParseFrontMatter("---\nname: York\nrole: duke\n---\n\n# York\n\nProse.\n")
	if fields["name"] != "York" || fields["role"] != "duke" {
		t.Fatalf("fields = %v", fields)
	}
	if !strings.HasPrefix(body, "# York") {
		t.Fatalf("body = %q", body)
	}

	if f, b := ParseFrontMatter("# Plain\n\nno header"); f != nil || !strings.HasPrefix(b, "# Plain") {
		t.Fatalf("plain file: fields=%v body=%q", f, b)
	}
	// Unterminated header is not treated as front-matter.
	if f, _ := ParseFrontMatter("---\nname: X\n(no close)\n"); f != nil {
		t.Fatalf("unterminated header parsed as front-matter: %v", f)
	}
}

// 4.2 — a created character carries structured front-matter the app can read back.
func TestCreatedCharacterHasFrontMatter(t *testing.T) {
	m := newInitializedManager(t)
	path, err := m.CreateStateFile("york", StateCharacter)
	if err != nil {
		t.Fatal(err)
	}
	sf := &StateFile{Path: path, Name: "york", Type: StateCharacter, Category: "characters"}
	fields, err := m.Fields(sf)
	if err != nil {
		t.Fatal(err)
	}
	if fields["name"] != "york" {
		t.Fatalf("name field = %q, want york", fields["name"])
	}
	for _, key := range []string{"role", "appearance", "first_appears", "status"} {
		if _, ok := fields[key]; !ok {
			t.Fatalf("character template missing field %q: %v", key, fields)
		}
	}
}

func newInitializedManager(t *testing.T) *Manager {
	t.Helper()
	m := New(t.TempDir())
	if err := m.Initialize(); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestCreateStateFileSanitizesTraversal(t *testing.T) {
	m := newInitializedManager(t)

	path, err := m.CreateStateFile("../escape", StateCharacter)
	if err != nil {
		t.Fatalf("CreateStateFile: %v", err)
	}

	wantDir := filepath.Join(m.root, "state", "characters")
	if got := filepath.Dir(path); got != wantDir {
		t.Fatalf("state file written to %q, want inside %q", path, wantDir)
	}
}

func TestCreateStateFileFirstLineOnly(t *testing.T) {
	m := newInitializedManager(t)

	// A multi-line "Add as Character" selection must collapse to its first line.
	path, err := m.CreateStateFile("Villain\nignore this line", StateLocation)
	if err != nil {
		t.Fatalf("CreateStateFile: %v", err)
	}
	if got := filepath.Base(path); got != "villain.md" {
		t.Fatalf("filename = %q, want villain.md", got)
	}
}

func TestCreateStateFileRejectsEmpty(t *testing.T) {
	m := newInitializedManager(t)
	if _, err := m.CreateStateFile("\n  \t", StateCharacter); err == nil {
		t.Fatal("expected error for a name that sanitizes to empty")
	}
}

// TestCreateStateFileCollisionErrors verifies that creating a state file whose
// name already exists fails loudly instead of silently pretending success.
func TestCreateStateFileCollisionErrors(t *testing.T) {
	m := newInitializedManager(t)

	if _, err := m.CreateStateFile("Kate", StateCharacter); err != nil {
		t.Fatalf("first create: %v", err)
	}
	_, err := m.CreateStateFile("Kate", StateCharacter)
	if !errors.Is(err, os.ErrExist) {
		t.Fatalf("second create err = %v, want os.ErrExist", err)
	}
}

// TestRefreshSkipsDirectoryNamedMd verifies a directory ending in .md under a
// category is not surfaced as a state file, and a directory named timeline.md
// at the top level is likewise ignored.
func TestRefreshSkipsDirectoryNamedMd(t *testing.T) {
	m := newInitializedManager(t)

	// A directory masquerading as a character file.
	if err := os.MkdirAll(filepath.Join(m.root, "state", "characters", "bogus.md"), 0755); err != nil {
		t.Fatal(err)
	}
	// A directory masquerading as the top-level timeline file would shadow the
	// real one; Initialize already wrote a real timeline.md, so build a fresh
	// manager where timeline.md is a directory instead.
	dir2 := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir2, "state", "timeline.md"), 0755); err != nil {
		t.Fatal(err)
	}
	m2 := New(dir2)

	if err := m.Refresh(); err != nil {
		t.Fatal(err)
	}
	for _, f := range m.All() {
		if f.Name == "bogus" {
			t.Fatalf("directory bogus.md surfaced as a state file: %+v", f)
		}
	}
	if tl := m2.Timeline(); tl != nil {
		t.Fatalf("directory timeline.md surfaced as timeline state file: %+v", tl)
	}
}

func TestCreateCategorySanitizes(t *testing.T) {
	m := newInitializedManager(t)

	if err := m.CreateCategory("../factions"); err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	// Directory must land under state/, not escape it.
	if _, err := os.Stat(filepath.Join(m.root, "state", "factions")); err != nil {
		t.Fatalf("expected sanitized category dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(m.root, "factions")); !os.IsNotExist(err) {
		t.Fatalf("category escaped state dir: %v", err)
	}
}

// v2 #60 — a category dir preserves the author's capitalization ("QandA" must
// not become "qanda"), matching how files and chapters are named.
func TestCreateCategoryPreservesCapitalization(t *testing.T) {
	m := newInitializedManager(t)

	if err := m.CreateCategory("QandA"); err != nil {
		t.Fatalf("CreateCategory: %v", err)
	}
	if _, err := os.Stat(filepath.Join(m.root, "state", "QandA")); err != nil {
		t.Fatalf("expected case-preserved category dir state/QandA: %v", err)
	}
}
