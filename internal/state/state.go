package state

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/ShayCichocki/marginctl/internal/fsutil"
)

const voiceGuideContent = `# Voice Guide

Use this file to teach marginctl your prose voice for LLM edits.

## Core Voice
- 

## Sentence Rhythm
- 

## Diction
- 

## Dialogue
- 

## Do
- 

## Don't
- 

## Examples

Bad:

Good:
`

// StateType indicates the category of state file
type StateType int

const (
	StateTimeline StateType = iota
	StateCharacter
	StateLocation
	StateThreads
	StateCustom // For user-created categories
)

func (t StateType) String() string {
	switch t {
	case StateTimeline:
		return "Timeline"
	case StateCharacter:
		return "Characters"
	case StateLocation:
		return "Locations"
	case StateThreads:
		return "Threads"
	case StateCustom:
		return "Custom"
	default:
		return "Unknown"
	}
}

// StateFile represents a single state file
type StateFile struct {
	Path         string    // Full path to the file
	Name         string    // Display name: the title heading, else front-matter name, else the filename stem
	NameFromStem bool      // Name is the filename stem (the file has no title of its own)
	Stem         string    // Filename without .md
	Type         StateType // Category of state
	Category     string    // Subdirectory name (e.g., "characters", "locations")
}

// Manager handles state file discovery and management
type Manager struct {
	root       string // Project root directory
	rootErr    error
	exists     bool        // Whether state/ directory exists
	files      []StateFile // Discovered state files
	categories []string    // All category directories (including empty)
}

// New creates a new state manager for the given project root
func New(projectRoot string) *Manager {
	root, err := fsutil.CanonicalRoot(projectRoot)
	m := &Manager{
		root:    root,
		rootErr: err,
		files:   make([]StateFile, 0),
	}
	_ = m.Refresh()
	return m
}

// Refresh rescans the state/ directory for state files
func (m *Manager) Refresh() error {
	if m.rootErr != nil {
		return m.rootErr
	}
	m.files = make([]StateFile, 0)
	m.categories = make([]string, 0)
	stateDir := filepath.Join(m.root, "state")

	info, err := os.Lstat(stateDir)
	if err != nil {
		m.exists = false
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		m.exists = false
		return fsutil.ErrUnsafePath
	}
	if !info.IsDir() {
		m.exists = false
		return nil
	}
	m.exists = true

	// Check for top-level state files
	if f := m.checkFile(stateDir, "timeline.md", StateTimeline, ""); f != nil {
		m.files = append(m.files, *f)
	}
	if f := m.checkFile(stateDir, "open_threads.md", StateThreads, ""); f != nil {
		m.files = append(m.files, *f)
	}

	// Scan characters/ subdirectory
	m.categories = append(m.categories, "characters")
	m.scanSubdir(stateDir, "characters", StateCharacter)

	// Scan locations/ subdirectory
	m.categories = append(m.categories, "locations")
	m.scanSubdir(stateDir, "locations", StateLocation)

	// Scan any other subdirectories as custom categories
	entries, err := os.ReadDir(stateDir)
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() && entry.Type()&os.ModeSymlink == 0 && entry.Name() != "characters" && entry.Name() != "locations" {
				m.categories = append(m.categories, entry.Name())
				m.scanSubdir(stateDir, entry.Name(), StateCustom)
			}
		}
	}

	// Sort files by type, then by name
	m.sortFiles()

	return nil
}

// sortFiles orders entries by type, then name, then path, so ties between
// entries sharing a display name resolve the same way every time.
func (m *Manager) sortFiles() {
	sort.SliceStable(m.files, func(i, j int) bool {
		a, b := m.files[i], m.files[j]
		if a.Type != b.Type {
			return a.Type < b.Type
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Path < b.Path
	})
}

func (m *Manager) checkFile(dir, name string, stateType StateType, category string) *StateFile {
	path := filepath.Join(dir, name)
	// Require a regular file: a *directory* named timeline.md must not be
	// surfaced as an openable state file.
	if info, err := os.Lstat(path); err == nil && info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
		stem := strings.TrimSuffix(name, ".md")
		return &StateFile{
			Path:         path,
			Name:         stem,
			NameFromStem: true,
			Stem:         stem,
			Type:         stateType,
			Category:     category,
		}
	}
	return nil
}

func (m *Manager) scanSubdir(stateDir, subdir string, stateType StateType) {
	subdirPath := filepath.Join(stateDir, subdir)
	if _, err := fsutil.ContainedPath(m.root, subdirPath, false); err != nil {
		return
	}
	entries, err := os.ReadDir(subdirPath)
	if err != nil {
		return
	}

	for _, entry := range entries {
		// Only regular .md files: skip subdirectories and non-regular files
		// (sockets/pipes/etc.) that would otherwise be treated as state files.
		if !entry.Type().IsRegular() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		stem := strings.TrimSuffix(entry.Name(), ".md")
		entryPath := filepath.Join(subdirPath, entry.Name())
		displayName, fromStem := stem, true
		// The style/ directory holds the voice guide, which is addressed by
		// its filename; every other entry shows its title when it has one.
		if subdir != "style" {
			if h := readTitle(entryPath); h != "" {
				displayName, fromStem = h, false
			}
		}
		m.files = append(m.files, StateFile{
			Path:         entryPath,
			Name:         displayName,
			NameFromStem: fromStem,
			Stem:         stem,
			Type:         stateType,
			Category:     subdir,
		})
	}
}

// Exists returns whether the state/ directory exists
func (m *Manager) Exists() bool {
	return m.exists
}

// All returns all discovered state files
func (m *Manager) All() []StateFile {
	return m.files
}

// ByType returns state files of a specific type
func (m *Manager) ByType(t StateType) []StateFile {
	var result []StateFile
	for _, f := range m.files {
		if f.Type == t {
			result = append(result, f)
		}
	}
	return result
}

// Categories returns all category names (including empty ones)
func (m *Manager) Categories() []string {
	return m.categories
}

// Timeline returns the timeline state file if it exists
func (m *Manager) Timeline() *StateFile {
	for i := range m.files {
		if m.files[i].Type == StateTimeline {
			return &m.files[i]
		}
	}
	return nil
}

// OpenThreads returns the open_threads state file if it exists
func (m *Manager) OpenThreads() *StateFile {
	for i := range m.files {
		if m.files[i].Type == StateThreads {
			return &m.files[i]
		}
	}
	return nil
}

// Read returns the content of a state file
func (m *Manager) Read(sf *StateFile) (string, error) {
	data, err := fsutil.ReadFileUnderRoot(m.root, sf.Path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// Initialize creates the state/ directory structure
func (m *Manager) Initialize() error {
	stateDir := filepath.Join(m.root, "state")

	// Create main state directory
	if err := fsutil.MkdirAllUnderRoot(m.root, stateDir, 0755); err != nil {
		return err
	}

	// Create subdirectories
	subdirs := []string{"characters", "locations", "style"}
	for _, subdir := range subdirs {
		path := filepath.Join(stateDir, subdir)
		if err := fsutil.MkdirAllUnderRoot(m.root, path, 0755); err != nil {
			return err
		}
	}

	// Create default files if they don't exist
	defaultFiles := []struct{ name, content string }{
		{"timeline.md", "# Timeline\n\n"},
		{"open_threads.md", "# Open Threads\n\n"},
		{"style/voice.md", voiceGuideContent},
	}

	for _, file := range defaultFiles {
		path := filepath.Join(stateDir, file.name)
		if info, err := os.Lstat(path); err == nil {
			if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("state path collision: %s", path)
			}
			continue
		} else if os.IsNotExist(err) {
			if err := fsutil.WriteFileUnderRoot(m.root, path, []byte(file.content), 0644, false); err != nil {
				return err
			}
		} else {
			return err
		}
	}

	m.exists = true
	return m.Refresh()
}

// DefaultVoiceGuideContent returns the starter voice guide for new projects.
func DefaultVoiceGuideContent() string {
	return voiceGuideContent
}

// ReadVoiceGuide returns the project voice guide if it exists.
func (m *Manager) ReadVoiceGuide() (string, error) {
	data, err := fsutil.ReadFileUnderRoot(m.root, filepath.Join(m.root, "state", "style", "voice.md"))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// WriteVoiceGuide saves the project voice guide.
func (m *Manager) WriteVoiceGuide(content string) error {
	path := filepath.Join(m.root, "state", "style", "voice.md")
	if err := fsutil.MkdirAllUnderRoot(m.root, filepath.Dir(path), 0755); err != nil {
		return err
	}
	// Keep a .bak of the prior voice guide so an LLM overwrite is recoverable.
	if err := fsutil.WriteFileUnderRoot(m.root, path, []byte(content), 0644, true); err != nil {
		return err
	}
	return m.Refresh()
}

// CreateStateFile creates a new state file of the given type
func (m *Manager) CreateStateFile(name string, stateType StateType) (string, error) {
	return m.CreateStateFileInCategory(name, stateType, "")
}

// maxDisplayNameRunes caps an entry's real name as stored in its heading.
const maxDisplayNameRunes = 200

// cleanDisplayName reduces a caller-supplied entry name to its first line with
// control characters removed and surrounding space trimmed. The result is what
// is written to the entry heading, so legitimate punctuation is kept verbatim.
func cleanDisplayName(name string) (string, error) {
	if i := strings.IndexAny(name, "\r\n"); i >= 0 {
		name = name[:i]
	}
	name = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name))
	if name == "" {
		return "", fmt.Errorf("invalid name: nothing usable")
	}
	if r := []rune(name); len(r) > maxDisplayNameRunes {
		name = strings.TrimSpace(string(r[:maxDisplayNameRunes]))
	}
	return name, nil
}

// entryDir returns the directory holding entries of (stateType, category), or ""
// when the combination is invalid (e.g. a custom type with no category).
func (m *Manager) entryDir(stateType StateType, category string) (string, error) {
	stateDir := filepath.Join(m.root, "state")
	switch stateType {
	case StateCharacter:
		return filepath.Join(stateDir, "characters"), nil
	case StateLocation:
		return filepath.Join(stateDir, "locations"), nil
	case StateCustom:
		if category == "" {
			return "", nil
		}
		category, err := fsutil.SanitizeName(category)
		if err != nil {
			return "", err
		}
		return filepath.Join(stateDir, category), nil
	default:
		return "", nil
	}
}

// entryTemplate returns the default content for a new entry; the real name is
// stored verbatim in the "# " heading.
func entryTemplate(name string, stateType StateType) string {
	switch stateType {
	case StateCharacter:
		return characterTemplate(name)
	case StateLocation:
		return locationTemplate(name)
	default:
		return "# " + name + "\n\n"
	}
}

// FindEntry returns the existing entry of (stateType, category) whose display
// name matches name after Unicode normalization (NFC, invisible format
// characters removed, case folded). An entry with no title of its own has its
// filename stem as display name, so it is matched by stem too; an entry with a
// title is never matched by its stem. Character and location lookups ignore
// category. It is a pure lookup over the last scan: call Refresh first to see
// files changed on disk. A path is never built from name.
func (m *Manager) FindEntry(stateType StateType, category, name string) *StateFile {
	if stateType != StateCharacter && stateType != StateLocation && stateType != StateCustom {
		return nil
	}
	display, err := cleanDisplayName(name)
	if err != nil {
		return nil
	}
	key := normalizeName(display)
	for i := range m.files {
		f := &m.files[i]
		if f.Type != stateType || (stateType == StateCustom && f.Category != category) {
			continue
		}
		if normalizeName(f.Name) == key {
			return f
		}
	}
	return nil
}

// EntryExists reports whether an entry with the same normalized display name is
// present in the last scan for (stateType, category). A different name that
// merely slugs to an existing filename is not a duplicate.
func (m *Manager) EntryExists(stateType StateType, category, name string) bool {
	return m.FindEntry(stateType, category, name) != nil
}

// CreateStateFileInCategory creates a new state file in a specific category
func (m *Manager) CreateStateFileInCategory(name string, stateType StateType, category string) (string, error) {
	return m.CreateStateFileWithBody(name, stateType, category, "")
}

// CreateStateFileWithBody rescans, then creates a new state file, appending an
// optional prose body (e.g. an extracted summary) after the template. It
// refuses to overwrite an existing entry (os.ErrExist).
func (m *Manager) CreateStateFileWithBody(name string, stateType StateType, category, body string) (string, error) {
	_ = m.Refresh()
	path, err := m.CreateStateFileFromScan(name, stateType, category, body)
	if err != nil || path == "" {
		return path, err
	}
	_ = m.Refresh()
	return path, nil
}

// CreateStateFileFromScan is CreateStateFileWithBody against the current
// in-memory scan, for batches that Refresh once up front. The new entry is
// recorded in memory so later creates in the batch see it; it does not rescan.
func (m *Manager) CreateStateFileFromScan(name string, stateType StateType, category, body string) (string, error) {
	display, err := cleanDisplayName(name)
	if err != nil {
		return "", err
	}
	dir, err := m.entryDir(stateType, category)
	if err != nil {
		return "", err
	}
	if dir == "" {
		return "", nil
	}
	if m.FindEntry(stateType, category, display) != nil {
		return "", os.ErrExist
	}
	content := entryTemplate(display, stateType)
	if strings.TrimSpace(body) != "" {
		content += strings.TrimSpace(body) + "\n"
	}

	// Create parent directory if needed
	if err := fsutil.MkdirAllUnderRoot(m.root, dir, 0755); err != nil {
		return "", err
	}

	path, err := freeSlugPath(dir, display, nil)
	if err != nil {
		return "", err
	}
	if err := fsutil.WriteFileUnderRoot(m.root, path, []byte(content), 0644, false); err != nil {
		return "", err
	}

	var cat string
	switch stateType {
	case StateCharacter:
		cat = "characters"
	case StateLocation:
		cat = "locations"
	default:
		cat, _ = fsutil.SanitizeName(category)
	}
	stem := strings.TrimSuffix(filepath.Base(path), ".md")
	m.files = append(m.files, StateFile{Path: path, Name: display, Stem: stem, Type: stateType, Category: cat})
	m.sortFiles()
	return path, nil
}

// PlannedEntryPath returns the file a create of name would write, without
// writing it. reserved holds paths already planned earlier in the same batch;
// the returned path is added to it.
func (m *Manager) PlannedEntryPath(name string, stateType StateType, category string, reserved map[string]bool) (string, error) {
	display, err := cleanDisplayName(name)
	if err != nil {
		return "", err
	}
	dir, err := m.entryDir(stateType, category)
	if err != nil || dir == "" {
		return "", err
	}
	path, err := freeSlugPath(dir, display, reserved)
	if err == nil && reserved != nil {
		reserved[path] = true
	}
	return path, err
}

// freeSlugPath picks the first free slug filename in dir: slug, slug-2, ...
func freeSlugPath(dir, display string, reserved map[string]bool) (string, error) {
	base := Slug(display)
	for i := 1; ; i++ {
		stem := base
		if i > 1 {
			stem = fmt.Sprintf("%s-%d", base, i)
		}
		if err := fsutil.PlainName(stem + ".md"); err != nil {
			return "", err
		}
		candidate := filepath.Join(dir, stem+".md")
		if reserved[candidate] {
			continue
		}
		if _, err := os.Lstat(candidate); err == nil {
			continue
		} else if !os.IsNotExist(err) {
			return "", err
		}
		return candidate, nil
	}
}

// CreateCategory creates a new category subdirectory in state/
func (m *Manager) CreateCategory(name string) error {
	name, err := fsutil.SanitizeName(name)
	if err != nil {
		return err
	}

	stateDir := filepath.Join(m.root, "state")
	// Preserve the author's capitalization ("QandA" ≠ "qanda"), as chapter
	// names do. (Entry files inside a category are lowercase state.Slug names.)
	categoryDir := filepath.Join(stateDir, name)

	if err := fsutil.MkdirAllUnderRoot(m.root, categoryDir, 0755); err != nil {
		return err
	}

	return m.Refresh()
}

// AppendToFile appends text to a state file (e.g., timeline.md)
func (m *Manager) AppendToFile(filename, text string) error {
	stateDir := filepath.Join(m.root, "state")
	if err := fsutil.RejectSymlinkDir(stateDir); err != nil {
		return err
	}
	path, err := plainStateFilePath(stateDir, filename)
	if err != nil {
		return err
	}

	// Read existing content (contained in state/, symlinks rejected)
	existing, err := fsutil.ReadFileUnderRoot(stateDir, path)
	if err != nil {
		return err
	}

	// Append the new text with a newline separator
	newContent := string(existing)
	if !strings.HasSuffix(newContent, "\n") {
		newContent += "\n"
	}
	newContent += "\n" + text + "\n"

	return fsutil.WriteFileUnderRoot(stateDir, path, []byte(newContent), 0644, false)
}

// plainStateFilePath validates that filename is a single plain path component
// (see fsutil.PlainName) and returns its path directly inside stateDir. It is
// only used for fixed top-level state files; entry names never reach it.
func plainStateFilePath(stateDir, filename string) (string, error) {
	if err := fsutil.PlainName(filename); err != nil {
		return "", fmt.Errorf("invalid state file name %q", filename)
	}
	return filepath.Join(stateDir, filename), nil
}
