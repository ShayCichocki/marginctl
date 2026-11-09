package session

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	want := Entry{File: "/proj/chapters/ch2.md", CursorRow: 12, CursorCol: 4, LeftPane: "state", LeftVisible: true, DistractionFree: true, NavWidth: 27, ResultsWidth: 55}
	if err := Save("/proj", want); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, ok := Load("/proj")
	if !ok {
		t.Fatal("Load: entry not found")
	}
	if got.File != want.File || got.CursorRow != want.CursorRow || got.CursorCol != want.CursorCol || got.LeftPane != want.LeftPane {
		t.Fatalf("Load = %+v, want %+v", got, want)
	}
	if got.LeftVisible != want.LeftVisible || got.DistractionFree != want.DistractionFree || got.NavWidth != want.NavWidth || got.ResultsWidth != want.ResultsWidth {
		t.Fatalf("layout = %+v, want %+v", got, want)
	}
	if got.Modified.IsZero() {
		t.Fatal("Modified should be stamped on Save")
	}
}

func TestLoadMissingKey(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if _, ok := Load("/nope"); ok {
		t.Fatal("expected no entry for unknown key")
	}
	if _, ok := Load(""); ok {
		t.Fatal("empty key should never match")
	}
}

func TestSaveIsAtomicAndPrivate(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	if err := Save("/proj", Entry{File: "a.md"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	path := filepath.Join(dir, "marginctl", "sessions.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Fatalf("perm = %o, want 600", perm)
	}
	// No leftover temp file next to it.
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("leftover .tmp file: %v", err)
	}
}

func TestCorruptFileSelfHeals(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	path := filepath.Join(dir, "marginctl", "sessions.json")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{not valid json"), 0600); err != nil {
		t.Fatal(err)
	}
	// Load treats a corrupt file as empty rather than erroring.
	if _, ok := Load("/proj"); ok {
		t.Fatal("corrupt file should yield no entries")
	}
	// A subsequent Save overwrites it cleanly.
	if err := Save("/proj", Entry{File: "b.md"}); err != nil {
		t.Fatalf("Save over corrupt: %v", err)
	}
	if got, ok := Load("/proj"); !ok || got.File != "b.md" {
		t.Fatalf("after heal Load = %+v ok=%v", got, ok)
	}
}

func TestPruneCapsEntries(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	base := time.Now()
	// Write maxEntries+10 entries with increasing timestamps so the oldest 10 are
	// pruned.
	for i := 0; i < maxEntries+10; i++ {
		key := filepath.Join("/proj", string(rune('a'+i%26)), time.Duration(i).String())
		e := Entry{File: "f.md", Modified: base.Add(time.Duration(i) * time.Minute)}
		if err := Save(key, e); err != nil {
			t.Fatalf("Save %d: %v", i, err)
		}
	}
	s := load()
	if len(s) > maxEntries {
		t.Fatalf("store has %d entries, want <= %d", len(s), maxEntries)
	}
}
