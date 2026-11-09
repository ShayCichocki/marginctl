package auth

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDefaultPathEnvReadsInjectedEnvironment(t *testing.T) {
	home := t.TempDir()
	xdg := filepath.Join(home, "xdg")
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_DATA_HOME", xdg)
	t.Setenv("APPDATA", filepath.Join(home, "roaming"))

	env := DefaultPathEnv()
	if env.GOOS != runtime.GOOS || env.HomeDir != home || env.XDGDataHome != xdg || env.AppData != filepath.Join(home, "roaming") {
		t.Fatalf("DefaultPathEnv = %+v", env)
	}

	got, err := AuthStorePath()
	if err != nil {
		t.Fatal(err)
	}
	var want string
	switch runtime.GOOS {
	case "windows":
		want = filepath.Join(home, "roaming", "marginctl", "auth.json")
	case "darwin":
		want = filepath.Join(home, "Library", "Application Support", "marginctl", "auth.json")
	default:
		want = filepath.Join(xdg, "marginctl", "auth.json")
	}
	if got != want {
		t.Fatalf("AuthStorePath = %q, want %q", got, want)
	}
}

func TestAuthStorePathForPrecedenceMatrix(t *testing.T) {
	cases := []struct {
		name string
		env  PathEnv
		want string
		err  bool
	}{
		{"linux xdg wins over home", PathEnv{GOOS: "linux", XDGDataHome: "/x", HomeDir: "/h"}, filepath.Join("/x", "marginctl", "auth.json"), false},
		{"linux home fallback", PathEnv{GOOS: "linux", HomeDir: "/h"}, filepath.Join("/h", ".local", "share", "marginctl", "auth.json"), false},
		{"freebsd uses unix rules", PathEnv{GOOS: "freebsd", HomeDir: "/h"}, filepath.Join("/h", ".local", "share", "marginctl", "auth.json"), false},
		{"darwin ignores xdg", PathEnv{GOOS: "darwin", XDGDataHome: "/x", HomeDir: "/h"}, filepath.Join("/h", "Library", "Application Support", "marginctl", "auth.json"), false},
		{"windows ignores xdg and home", PathEnv{GOOS: "windows", XDGDataHome: "/x", HomeDir: "/h", AppData: "/a"}, filepath.Join("/a", "marginctl", "auth.json"), false},
		{"windows needs appdata", PathEnv{GOOS: "windows", HomeDir: "/h"}, "", true},
		{"darwin needs home", PathEnv{GOOS: "darwin"}, "", true},
		{"linux needs something", PathEnv{GOOS: "linux"}, "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := AuthStorePathFor(tc.env)
			if (err != nil) != tc.err || got != tc.want {
				t.Fatalf("got %q, %v; want %q err=%v", got, err, tc.want, tc.err)
			}
		})
	}
}

func TestSaveTokenStoreCreatesPrivateDirAndFile(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes")
	}
	dir := filepath.Join(t.TempDir(), "a", "marginctl")
	path := filepath.Join(dir, "auth.json")
	if err := SaveTokenStore(path, fakeTokenStore()); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("file mode = %v err=%v, want 0600", st.Mode().Perm(), err)
	}
	if st, err := os.Stat(dir); err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("dir mode = %v err=%v, want 0700", st.Mode().Perm(), err)
	}
	// Overwrite keeps 0600 and leaves no temp siblings behind.
	if err := SaveTokenStore(path, fakeTokenStore()); err != nil {
		t.Fatal(err)
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Fatalf("leftover files after atomic write: %v", ents)
	}
	if st, _ := os.Stat(path); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode after overwrite = %v", st.Mode().Perm())
	}
}

func TestSaveTokenStoreTightensPreexistingLooseMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes")
	}
	path := filepath.Join(t.TempDir(), "auth.json")
	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := SaveTokenStore(path, fakeTokenStore()); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(path); st.Mode().Perm()&0o077 != 0 {
		t.Fatalf("credential store left group/world accessible: %v", st.Mode().Perm())
	}
}

func TestLoadTokenStoreCorruptInputs(t *testing.T) {
	dir := t.TempDir()
	for name, content := range map[string]string{
		"empty":     "",
		"truncated": `{"access_token":"abc`,
		"array":     `[1,2]`,
		"bad types": `{"access_token": 5}`,
		"garbage":   "\x00\x01\x02",
	} {
		t.Run(name, func(t *testing.T) {
			p := filepath.Join(dir, strings.ReplaceAll(name, " ", "_")+".json")
			if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := LoadTokenStore(p)
			if err == nil || !strings.Contains(err.Error(), "malformed") {
				t.Fatalf("err = %v, want malformed", err)
			}
			if strings.Contains(err.Error(), "abc") {
				t.Fatalf("error leaked file contents: %v", err)
			}
			st := TokenStoreStatus(p)
			if st.Connected || !strings.HasPrefix(st.Format(), "Not connected") {
				t.Fatalf("status = %+v", st)
			}
		})
	}
	if _, err := LoadTokenStore(filepath.Join(dir, "missing.json")); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("missing err = %v", err)
	}
	if _, err := LoadTokenStore(dir); err == nil {
		t.Fatal("directory path must error")
	}
}

func TestDeleteTokenStoreOnlyRemovesTheStoreAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	store := filepath.Join(dir, "auth.json")
	sibling := filepath.Join(dir, "config.toml")
	if err := SaveTokenStore(store, fakeTokenStore()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sibling, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := DeleteTokenStore(store); err != nil {
			t.Fatalf("delete #%d: %v", i, err)
		}
	}
	if _, err := os.Stat(store); !os.IsNotExist(err) {
		t.Fatalf("store still present: %v", err)
	}
	if b, err := os.ReadFile(sibling); err != nil || string(b) != "keep" {
		t.Fatalf("sibling damaged: %q %v", b, err)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Fatal("directory must survive disconnect")
	}
}

func TestDeleteTokenStoreRefusesNonEmptyDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "auth.json")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "child"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := DeleteTokenStore(dir); err == nil {
		t.Fatal("must not recursively delete")
	}
	if _, err := os.Stat(filepath.Join(dir, "child")); err != nil {
		t.Fatal("child removed")
	}
}

func TestRedactSecretsAndStatusFormatNeverLeakTokens(t *testing.T) {
	tok := fakeTokenStore()
	tok.AccessToken, tok.RefreshToken = "sk-abcdefghijk", "refresh_token_zzzzzzzz"
	p := filepath.Join(t.TempDir(), "auth.json")
	if err := SaveTokenStore(p, tok); err != nil {
		t.Fatal(err)
	}
	out := TokenStoreStatus(p).Format()
	for _, secret := range []string{tok.AccessToken, tok.RefreshToken} {
		if strings.Contains(out, secret) {
			t.Fatalf("status leaked %q: %s", secret, out)
		}
	}
	if got := RedactSecrets("Authorization: Bearer abcdefghijkl\n  x"); strings.Contains(got, "abcdefghijkl") || strings.Contains(got, "\n") {
		t.Fatalf("RedactSecrets = %q", got)
	}
}
