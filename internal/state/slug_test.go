package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ShayCichocki/marginctl/internal/fsutil"
)

func TestSlugTable(t *testing.T) {
	long := strings.Repeat("abcdefghij ", 15)
	cases := map[string]string{
		"Martin Luther King Jr.": "martin-luther-king-jr",
		"Washington, D.C.":       "washington-d-c",
		"Dr. Who?":               "dr-who",
		`"Doc" Holliday`:         "doc-holliday",
		"Station 7: Outpost":     "station-7-outpost",
		"Con":                    "con-entry",
		"Nul":                    "nul-entry",
		"COM1":                   "com1-entry",
		"O'Brien":                "o-brien",
		"J. R. Smith":            "j-r-smith",
		"Zoë":                    "zoë",
		"李小龍":                    "李小龍",
		"🐉🔥":                     "entry",
		"..":                     "entry",
		"  --  ":                 "entry",
	}
	for in, want := range cases {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
	if s := Slug(long); len(s) > maxSlugBytes || strings.HasSuffix(s, "-") {
		t.Errorf("long slug = %q", s)
	}
	cjk := Slug(strings.Repeat("龍", 150))
	if n := len([]rune(cjk)); n != maxSlugBytes/3 {
		t.Errorf("cjk slug has %d runes, want %d (byte cap)", n, maxSlugBytes/3)
	}
}

func TestCreateRealisticNames(t *testing.T) {
	m := newInitializedManager(t)
	names := []string{
		"Martin Luther King Jr.", "Washington, D.C.", "Dr. Who?", `"Doc" Holliday`,
		"Station 7: Outpost", "Con", "Nul", "O'Brien", "J. R. Smith", "Zoë", "李小龍",
		"🐉 Dragon 🔥", "🐉🔥", strings.Repeat("Long Name ", 15)[:150], "Wait...", "a/b", "../../etc/passwd",
	}
	seen := map[string]bool{}
	for _, n := range names {
		path, err := m.CreateStateFile(n, StateCharacter)
		if err != nil {
			t.Fatalf("%q: %v", n, err)
		}
		base := filepath.Base(path)
		if err := fsutil.PlainName(base); err != nil {
			t.Errorf("%q: unsafe filename %q", n, base)
		}
		if filepath.Dir(path) != filepath.Join(m.root, "state", "characters") {
			t.Errorf("%q: written outside characters/: %s", n, path)
		}
		if seen[base] {
			t.Errorf("%q: duplicate filename %q", n, base)
		}
		seen[base] = true
		t.Logf("%q -> %s", n, base)
		sf := m.FindEntry(StateCharacter, "", n)
		if sf == nil || sf.Name != strings.TrimSpace(n) || sf.Path != path {
			t.Errorf("%q: display name = %+v", n, sf)
		}
	}
}

func TestSameSlugGetsNumberedSuffix(t *testing.T) {
	m := newInitializedManager(t)
	a, err := m.CreateStateFile("Dr. Who", StateCharacter)
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.CreateStateFile("Dr Who?", StateCharacter)
	if err != nil {
		t.Fatal(err)
	}
	c, err := m.CreateStateFile("dr-who!", StateCharacter)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(a) != "dr-who.md" || filepath.Base(b) != "dr-who-2.md" || filepath.Base(c) != "dr-who-3.md" {
		t.Fatalf("got %s %s %s", a, b, c)
	}
	if _, err := m.CreateStateFile("DR. WHO", StateCharacter); err != os.ErrExist {
		t.Fatalf("same display name (case-insensitive) err = %v, want ErrExist", err)
	}
}

func TestFindEntryByHeadingStemAndHandMade(t *testing.T) {
	m := newInitializedManager(t)
	dir := filepath.Join(m.root, "state", "characters")
	if err := os.WriteFile(filepath.Join(dir, "Alice.md"), []byte("no heading here\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "bob.md"), []byte("---\nname: x\n---\n\n# Robert \"Bob\" Smith\n"), 0644); err != nil {
		t.Fatal(err)
	}
	_ = m.Refresh()
	if sf := m.FindEntry(StateCharacter, "", "alice"); sf == nil || sf.Name != "Alice" {
		t.Fatalf("hand-made Alice.md: %+v", sf)
	}
	if sf := m.FindEntry(StateCharacter, "", `robert "bob" smith`); sf == nil || sf.Stem != "bob" {
		t.Fatalf("heading lookup: %+v", sf)
	}
	if sf := m.FindEntry(StateCharacter, "", "bob"); sf != nil {
		t.Fatalf("a titled entry must not match by stem: %+v", sf)
	}
	if m.FindEntry(StateCharacter, "", "Carol") != nil || m.FindEntry(StateLocation, "", "Alice") != nil {
		t.Fatal("unexpected match")
	}
	var names []string
	for _, f := range m.ByType(StateCharacter) {
		names = append(names, f.Name)
	}
	if strings.Join(names, "|") != `Alice|Robert "Bob" Smith` {
		t.Fatalf("display names = %q", names)
	}
}
