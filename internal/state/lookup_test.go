package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeEntry(t *testing.T, m *Manager, rel, content string) string {
	t.Helper()
	p := filepath.Join(m.root, "state", filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestHeadinglessFilesWithSectionHeadingsUseStems(t *testing.T) {
	m := newInitializedManager(t)
	for _, n := range []string{"Alice", "Bob", "Carol"} {
		writeEntry(t, m, "characters/"+n+".md", n+" is a character.\n\n# Appearance\ntall\n# Backstory\nlong\n")
	}
	_ = m.Refresh()
	var names []string
	for _, f := range m.ByType(StateCharacter) {
		names = append(names, f.Name)
	}
	if got := strings.Join(names, "|"); got != "Alice|Bob|Carol" {
		t.Fatalf("display names = %q", got)
	}
	if sf := m.FindEntry(StateCharacter, "", "Appearance"); sf != nil {
		t.Fatalf("section heading matched an entry: %+v", sf)
	}
	if m.EntryExists(StateCharacter, "", "Appearance") {
		t.Fatal("section heading counted as an existing entry")
	}
}

func TestTitleHeadingMustBeFirstNonBlankLine(t *testing.T) {
	m := newInitializedManager(t)
	writeEntry(t, m, "characters/a.md", "\n\n# Real Title\n\n# Appearance\n")
	writeEntry(t, m, "characters/b.md", "---\nname: Barnaby\n---\n\nintro\n\n# Appearance\n")
	writeEntry(t, m, "characters/c.md", "---\nname: Ignored\n---\n\n# Cecilia\n")
	_ = m.Refresh()
	want := map[string]string{"a": "Real Title", "b": "Barnaby", "c": "Cecilia"}
	for _, f := range m.ByType(StateCharacter) {
		if w, ok := want[f.Stem]; ok && f.Name != w {
			t.Errorf("%s: name %q, want %q", f.Stem, f.Name, w)
		}
	}
}

func TestNormalizedDuplicateDetection(t *testing.T) {
	m := newInitializedManager(t)
	nfc, nfd := "Zo\u00eb", "Zoe\u0308"
	if _, err := m.CreateStateFile(nfc, StateCharacter); err != nil {
		t.Fatal(err)
	}
	if _, err := m.CreateStateFile("Alice", StateCharacter); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{nfd, "ZOË", "Al\u200bice", "Al\u00adice", "\ufeffalice", "Al\u200dic\u200ce"} {
		if !m.EntryExists(StateCharacter, "", n) {
			t.Errorf("%q not detected as duplicate", n)
		}
		if _, err := m.CreateStateFile(n, StateCharacter); !os.IsExist(err) {
			t.Errorf("%q create err = %v, want ErrExist", n, err)
		}
	}
}

func TestSlugCollisionOnDifferentNameGetsSuffix(t *testing.T) {
	m := newInitializedManager(t)
	if _, err := m.CreateStateFile("O'Brien", StateCharacter); err != nil {
		t.Fatal(err)
	}
	if m.EntryExists(StateCharacter, "", "O-Brien") {
		t.Fatal("different name reported as existing")
	}
	path, err := m.CreateStateFile("O-Brien", StateCharacter)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(path) != "o-brien-2.md" {
		t.Fatalf("path = %s", path)
	}
}

func TestFindEntryStemFallbackOnlyForUntitled(t *testing.T) {
	m := newInitializedManager(t)
	writeEntry(t, m, "characters/carol.md", "# Caroline Ashby\n")
	writeEntry(t, m, "characters/dave.md", "just prose\n")
	_ = m.Refresh()
	if m.FindEntry(StateCharacter, "", "Carol") != nil {
		t.Fatal("titled entry matched by stem")
	}
	if sf := m.FindEntry(StateCharacter, "", "Dave"); sf == nil || sf.Stem != "dave" {
		t.Fatalf("untitled entry not matched by stem: %+v", sf)
	}
}

func TestFindEntryIsPureLookup(t *testing.T) {
	m := newInitializedManager(t)
	writeEntry(t, m, "characters/eve.md", "# Eve\n")
	if m.FindEntry(StateCharacter, "", "Eve") != nil {
		t.Fatal("FindEntry rescanned the disk")
	}
	_ = m.Refresh()
	if m.FindEntry(StateCharacter, "", "Eve") == nil {
		t.Fatal("not found after Refresh")
	}
}

func TestDuplicateNamesTieBreakByPath(t *testing.T) {
	m := newInitializedManager(t)
	writeEntry(t, m, "characters/zed-copy.md", "# Alice\n")
	writeEntry(t, m, "characters/alice.md", "# Alice\n")
	writeEntry(t, m, "characters/mid.md", "# Alice\n")
	for i := 0; i < 20; i++ {
		_ = m.Refresh()
		if sf := m.FindEntry(StateCharacter, "", "alice"); sf == nil || sf.Stem != "alice" {
			t.Fatalf("iteration %d: %+v", i, sf)
		}
	}
}
