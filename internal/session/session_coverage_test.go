package session

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// isolate points every config-location variable at a fresh temp dir and
// returns the XDG config dir.
func isolate(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("HOME", filepath.Join(dir, "home"))
	t.Setenv("USERPROFILE", filepath.Join(dir, "home"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(dir, "data"))
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(dir, "cfg"))
	return filepath.Join(dir, "cfg")
}

func TestCovPathHonorsXDGThenHome(t *testing.T) {
	cfg := isolate(t)
	if got, want := Path(), filepath.Join(cfg, "marginctl", "sessions.json"); got != want {
		t.Fatalf("Path() = %q, want %q", got, want)
	}
	t.Setenv("XDG_CONFIG_HOME", "")
	home := os.Getenv("HOME")
	if got, want := Path(), filepath.Join(home, ".config", "marginctl", "sessions.json"); got != want {
		t.Fatalf("Path() without XDG = %q, want %q", got, want)
	}
}

func TestCovNoHomeMakesSaveAndRewriteNoOps(t *testing.T) {
	if runtime.GOOS == "windows" || runtime.GOOS == "plan9" {
		t.Skip("HOME-based lookup only")
	}
	isolate(t)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("HOME", "")
	if p := Path(); p != "" {
		t.Skipf("home dir still resolvable (%q) on this platform", p)
	}
	if err := Save("/proj", Entry{File: "a.md"}); err != nil {
		t.Fatalf("Save without a home = %v, want nil", err)
	}
	if err := RewriteFilePath("/proj", "a.md", "b.md"); err != nil {
		t.Fatalf("RewriteFilePath without a home = %v, want nil", err)
	}
	if _, ok := Load("/proj"); ok {
		t.Fatal("Load found an entry without a sessions path")
	}
}

func TestCovSaveEmptyKeyIsIgnored(t *testing.T) {
	cfg := isolate(t)
	if err := Save("", Entry{File: "a.md"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cfg, "marginctl", "sessions.json")); !os.IsNotExist(err) {
		t.Fatalf("empty key created a sessions file: %v", err)
	}
}

func TestCovRewriteFilePathRenames(t *testing.T) {
	isolate(t)
	if err := Save("/proj", Entry{File: "/proj/chapters/001_A.md", CursorRow: 3, CursorCol: 4, LeftPane: "state", NavWidth: 30}); err != nil {
		t.Fatal(err)
	}
	if err := Save("/other", Entry{File: "/proj/chapters/001_A.md"}); err != nil {
		t.Fatal(err)
	}
	before, _ := Load("/proj")
	time.Sleep(2 * time.Millisecond)
	// Unclean spelling of the old path must still match.
	if err := RewriteFilePath("/proj", "/proj/chapters/../chapters/001_A.md", "/proj/chapters/001_B.md"); err != nil {
		t.Fatal(err)
	}
	got, ok := Load("/proj")
	if !ok || got.File != "/proj/chapters/001_B.md" {
		t.Fatalf("after rewrite = %+v ok=%v", got, ok)
	}
	if got.CursorRow != 3 || got.CursorCol != 4 || got.LeftPane != "state" || got.NavWidth != 30 {
		t.Fatalf("rewrite lost layout: %+v", got)
	}
	if !got.Modified.After(before.Modified) {
		t.Fatalf("Modified not refreshed: %v <= %v", got.Modified, before.Modified)
	}
	if other, _ := Load("/other"); other.File != "/proj/chapters/001_A.md" {
		t.Fatalf("other project's entry touched: %+v", other)
	}
}

func TestCovRewriteFilePathClearsWithEmptyKeepingLayout(t *testing.T) {
	isolate(t)
	if err := Save("/proj", Entry{File: "/proj/chapters/x.md", LeftPane: "project", LeftVisible: true, NavWidth: 22}); err != nil {
		t.Fatal(err)
	}
	if err := RewriteFilePath("/proj", "/proj/chapters/x.md", ""); err != nil {
		t.Fatal(err)
	}
	got, ok := Load("/proj")
	if !ok || got.File != "" || got.LeftPane != "project" || !got.LeftVisible || got.NavWidth != 22 {
		t.Fatalf("after clear = %+v ok=%v", got, ok)
	}
}

func TestCovRewriteFilePathNoMatchDoesNotTouchDisk(t *testing.T) {
	cfg := isolate(t)
	path := filepath.Join(cfg, "marginctl", "sessions.json")

	// No sessions file at all: a non-matching rewrite must not create one.
	if err := RewriteFilePath("/proj", "/a.md", "/b.md"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("no-op rewrite created %s: %v", path, err)
	}
	if err := Save("/proj", Entry{File: "/a.md"}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	for _, tc := range []struct{ key, old string }{
		{"/proj", "/different.md"}, {"/unknown", "/a.md"}, {"", "/a.md"}, {"/proj", ""},
	} {
		if err := RewriteFilePath(tc.key, tc.old, "/z.md"); err != nil {
			t.Fatal(err)
		}
	}
	after, _ := os.ReadFile(path)
	if string(before) != string(after) {
		t.Fatalf("non-matching rewrite changed the file:\n%s\n---\n%s", before, after)
	}
}

func TestCovRewriteTightensLoosePermissionsTo0600(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes only")
	}
	cfg := isolate(t)
	path := filepath.Join(cfg, "marginctl", "sessions.json")
	if err := Save("/proj", Entry{File: "/a.md"}); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if err := RewriteFilePath("/proj", "/a.md", "/b.md"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("mode after rewrite = %v, want 0600", info.Mode().Perm())
	}
	// Save must enforce it too.
	if err := os.Chmod(path, 0666); err != nil {
		t.Fatal(err)
	}
	if err := Save("/proj", Entry{File: "/c.md"}); err != nil {
		t.Fatal(err)
	}
	if info, _ = os.Stat(path); info.Mode().Perm() != 0600 {
		t.Fatalf("mode after Save = %v, want 0600", info.Mode().Perm())
	}
	entries, _ := os.ReadDir(filepath.Dir(path))
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Fatalf("leftover temp file %s", e.Name())
		}
	}
}

func TestCovStoredFilePathsAreReturnedVerbatimForCallerValidation(t *testing.T) {
	// The session package is a dumb store: it must round-trip a hostile path
	// without interpreting it. internal/app.restoreSession is the trust
	// boundary and re-validates with fsutil.ContainedPath.
	isolate(t)
	evil := "../../../../etc/passwd"
	if err := Save("/proj", Entry{File: evil}); err != nil {
		t.Fatal(err)
	}
	got, ok := Load("/proj")
	if !ok || got.File != evil {
		t.Fatalf("Load = %+v ok=%v", got, ok)
	}
}

func TestCovCorruptFileSelfHealsWithoutDuplicatingEntries(t *testing.T) {
	cfg := isolate(t)
	path := filepath.Join(cfg, "marginctl", "sessions.json")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	for _, junk := range []string{"", "null", "[1,2]", `{"/proj": 5}`, "{not json"} {
		if err := os.WriteFile(path, []byte(junk), 0600); err != nil {
			t.Fatal(err)
		}
		if _, ok := Load("/proj"); ok {
			t.Fatalf("junk %q produced an entry", junk)
		}
		if err := RewriteFilePath("/proj", "/a.md", "/b.md"); err != nil {
			t.Fatalf("rewrite over %q: %v", junk, err)
		}
		if err := Save("/proj", Entry{File: "/ok.md"}); err != nil {
			t.Fatalf("save over %q: %v", junk, err)
		}
		if err := Save("/proj", Entry{File: "/ok2.md"}); err != nil {
			t.Fatal(err)
		}
		s := load()
		if len(s) != 1 || s["/proj"].File != "/ok2.md" {
			t.Fatalf("after healing %q store = %+v", junk, s)
		}
		data, _ := os.ReadFile(path)
		if strings.Count(string(data), `"/proj"`) != 1 {
			t.Fatalf("duplicated key in file:\n%s", data)
		}
	}
}
