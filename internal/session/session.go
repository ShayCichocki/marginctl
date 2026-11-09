// Package session persists lightweight per-project editor state — the last-open
// file, cursor position, and left-pane layout — so reopening a project lands the
// user where they left off. It is intentionally best-effort: any read/write
// failure degrades to "no session" rather than blocking the editor.
package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/ShayCichocki/marginctl/internal/fsutil"
)

// maxEntries caps the sessions file so it can't grow without bound as the user
// opens many projects; the least-recently-updated entries are pruned.
const maxEntries = 50

// Entry is the saved state for one project (or single-file) key.
type Entry struct {
	File            string    `json:"file"`                // absolute path of the last-open file
	CursorRow       int       `json:"cursor_row"`          // rune-index row
	CursorCol       int       `json:"cursor_col"`          // rune-index column
	LeftPane        string    `json:"left_pane,omitempty"` // left-pane mode label (layout)
	LeftVisible     bool      `json:"left_visible"`
	DistractionFree bool      `json:"distraction_free,omitempty"`
	NavWidth        int       `json:"nav_width,omitempty"`
	ResultsWidth    int       `json:"results_width,omitempty"`
	Modified        time.Time `json:"modified"` // last-write time, for pruning
}

// store is the on-disk shape: a map from project key to its saved entry.
type store map[string]Entry

// Path returns the sessions file location (sessions.json alongside config.toml),
// honoring XDG_CONFIG_HOME. It is empty when the home directory can't be found.
func Path() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "marginctl", "sessions.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "marginctl", "sessions.json")
}

// load reads the whole sessions store. A missing or malformed file yields an
// empty store with no error, so a corrupt file self-heals on the next Save.
func load() store {
	path := Path()
	if path == "" {
		return store{}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return store{}
	}
	var s store
	if err := json.Unmarshal(data, &s); err != nil || s == nil {
		return store{}
	}
	return s
}

// Load returns the saved entry for key (a project root or single-file path),
// reporting whether one exists.
func Load(key string) (Entry, bool) {
	if key == "" {
		return Entry{}, false
	}
	s := load()
	e, ok := s[key]
	return e, ok
}

// Save merges e under key into the sessions file, atomically (0600). It prunes
// the store to the most-recently-modified maxEntries. A missing config dir is
// created; any failure is returned but is safe for the caller to ignore.
func Save(key string, e Entry) error {
	path := Path()
	if path == "" || key == "" {
		return nil
	}
	if e.Modified.IsZero() {
		e.Modified = time.Now()
	}
	s := load()
	s[key] = e
	prune(s)

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return fsutil.WriteFilePrivate(path, data)
}

// RewriteFilePath updates a remembered current file after a transactional
// chapter move. An empty newPath clears the stale file while retaining layout.
func RewriteFilePath(key, oldPath, newPath string) error {
	if key == "" || oldPath == "" {
		return nil
	}
	s := load()
	e, ok := s[key]
	if !ok || filepath.Clean(e.File) != filepath.Clean(oldPath) {
		return nil
	}
	e.File, e.Modified = newPath, time.Now()
	s[key] = e
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	path := Path()
	if path == "" {
		return nil
	}
	if err = os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	return fsutil.WriteFilePrivate(path, data)
}

// prune drops the least-recently-modified entries until at most maxEntries
// remain.
func prune(s store) {
	if len(s) <= maxEntries {
		return
	}
	type kv struct {
		key string
		mod time.Time
	}
	items := make([]kv, 0, len(s))
	for k, v := range s {
		items = append(items, kv{k, v.Modified})
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].mod.After(items[j].mod) // newest first
	})
	for _, it := range items[maxEntries:] {
		delete(s, it.key)
	}
}
