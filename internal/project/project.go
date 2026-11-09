package project

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/ShayCichocki/marginctl/internal/fsutil"
	"github.com/ShayCichocki/marginctl/internal/mdtitle"
)

// chapterPrefixRe matches a leading zero-padded numeric ordering prefix like
// "012_" so it can be parsed off an existing chapter filename.
var chapterPrefixRe = regexp.MustCompile(`^(\d+)_`)

type FileEntry struct {
	Name  string
	Path  string
	IsDir bool
	// Title is a chapter's own title heading (see mdtitle); empty when the
	// file has none or for non-chapter entries.
	Title string
}

// ReadChapterTitle returns the title heading of the chapter file at path, or
// "" when it has none. Only the first 2KB is read.
func ReadChapterTitle(path string) string { return mdtitle.ReadFile(path) }

type Project struct {
	Root         string
	CurrentFile  string
	IsProject    bool // True if this is a novel project (has chapters/ directory)
	entries      []FileEntry
	chapters     []FileEntry
	replaceFault func(phase, path string) error
}

func New() *Project {
	return &Project{}
}

func (p *Project) Open(path string) error {
	candidate := &Project{replaceFault: p.replaceFault}
	if err := candidate.open(path); err != nil {
		return err
	}
	*p = *candidate
	return nil
}

// open builds a complete project value. Open keeps the receiver unchanged until
// every path validation and directory read has succeeded.
func (p *Project) open(path string) error {
	abs, err := filepath.Abs(path)
	if err != nil {
		return err
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: %s", fsutil.ErrUnsafePath, path)
	}

	if info.IsDir() {
		p.Root = abs
	} else {
		// It's a file. Treat it as part of an enclosing project when one exists
		// (a parent dir with a chapters/ subdirectory), so that opening
		// `chapters/ch1.md` still gets nav/state/voice context. Otherwise fall
		// back to single-file mode rooted at the file's parent.
		p.CurrentFile = abs
		if root, ok := findProjectRoot(filepath.Dir(abs)); ok {
			p.Root = root
		} else {
			p.Root = filepath.Dir(abs)
		}
	}
	p.Root, err = fsutil.CanonicalRoot(p.Root)
	if err != nil {
		return err
	}
	if p.CurrentFile != "" {
		p.CurrentFile, err = fsutil.ContainedPath(p.Root, p.CurrentFile, false)
		if err != nil {
			return err
		}
	}

	// Detect if this is a novel project (has chapters/ directory)
	chaptersPath := filepath.Join(p.Root, "chapters")
	if chapInfo, statErr := os.Lstat(chaptersPath); statErr == nil && chapInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: %s", fsutil.ErrUnsafePath, chaptersPath)
	} else if statErr == nil && chapInfo.IsDir() {
		p.IsProject = true
		if err := p.refreshChapters(); err != nil {
			return err
		}
	}

	return p.refresh()
}

// findProjectRoot walks up from dir looking for the nearest ancestor that
// contains a chapters/ subdirectory. It returns that directory and true, or
// "", false when it reaches the filesystem root without finding one.
func findProjectRoot(dir string) (string, bool) {
	for {
		chaptersPath := filepath.Join(dir, "chapters")
		if info, err := os.Lstat(chaptersPath); err == nil && info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
			return dir, true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", false
		}
		dir = parent
	}
}

func (p *Project) refresh() error {
	if _, err := fsutil.ContainedPath(p.Root, p.Root, false); err != nil {
		return err
	}
	entries, err := os.ReadDir(p.Root)
	if err != nil {
		return err
	}

	p.entries = make([]FileEntry, 0, len(entries))
	for _, e := range entries {
		if e.Type()&os.ModeSymlink != 0 {
			continue
		}
		// Skip hidden files
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		// Only show directories and markdown files
		if !e.IsDir() && !strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
			continue
		}
		p.entries = append(p.entries, FileEntry{
			Name:  e.Name(),
			Path:  filepath.Join(p.Root, e.Name()),
			IsDir: e.IsDir(),
		})
	}

	// Sort: directories first, then alphabetically
	sort.Slice(p.entries, func(i, j int) bool {
		if p.entries[i].IsDir != p.entries[j].IsDir {
			return p.entries[i].IsDir
		}
		return p.entries[i].Name < p.entries[j].Name
	})

	return nil
}

func (p *Project) ListFiles() []FileEntry {
	return p.entries
}

func (p *Project) refreshChapters() error {
	chaptersPath := filepath.Join(p.Root, "chapters")
	if _, err := fsutil.ContainedPath(p.Root, chaptersPath, false); err != nil {
		return err
	}
	entries, err := os.ReadDir(chaptersPath)
	if err != nil {
		return err
	}

	p.chapters = make([]FileEntry, 0, len(entries))
	for _, e := range entries {
		// Skip hidden files
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		// Only regular markdown files: a directory (or other non-regular file)
		// named foo.md must not be listed as a chapter.
		if !e.Type().IsRegular() || !strings.HasSuffix(strings.ToLower(e.Name()), ".md") {
			continue
		}
		p.chapters = append(p.chapters, FileEntry{
			Name:  e.Name(),
			Path:  filepath.Join(chaptersPath, e.Name()),
			IsDir: false,
			Title: ReadChapterTitle(filepath.Join(chaptersPath, e.Name())),
		})
	}

	// Natural sort so "Chapter 10" follows "Chapter 9" (and 010_ after 002_)
	// rather than sorting lexically.
	sort.Slice(p.chapters, func(i, j int) bool {
		return naturalLess(p.chapters[i].Name, p.chapters[j].Name)
	})

	return nil
}

// naturalLess compares two strings so that embedded runs of digits sort by
// numeric value: "ch2" < "ch10", and "010_x" < "100_x". Non-digit runs compare
// byte-wise (case-insensitively) as usual.
func naturalLess(a, b string) bool {
	ai, bi := 0, 0
	for ai < len(a) && bi < len(b) {
		ad := a[ai] >= '0' && a[ai] <= '9'
		bd := b[bi] >= '0' && b[bi] <= '9'
		if ad && bd {
			// Compare two numeric runs by value, ignoring leading zeros.
			as, ae := ai, ai
			for ae < len(a) && a[ae] >= '0' && a[ae] <= '9' {
				ae++
			}
			bs, be := bi, bi
			for be < len(b) && b[be] >= '0' && b[be] <= '9' {
				be++
			}
			an := strings.TrimLeft(a[as:ae], "0")
			bn := strings.TrimLeft(b[bs:be], "0")
			if len(an) != len(bn) {
				return len(an) < len(bn)
			}
			if an != bn {
				return an < bn
			}
			ai, bi = ae, be
			continue
		}
		ca, cb := lowerByte(a[ai]), lowerByte(b[bi])
		if ca != cb {
			return ca < cb
		}
		ai++
		bi++
	}
	return len(a)-ai < len(b)-bi
}

func lowerByte(c byte) byte {
	if c >= 'A' && c <= 'Z' {
		return c + ('a' - 'A')
	}
	return c
}

func (p *Project) ListChapters() []FileEntry {
	return p.chapters
}

// Refresh re-reads the project's file/chapter listings from disk so that files
// created or deleted outside the app (or by a sibling pane) appear or vanish.
// Cheap enough to call on pane focus.
func (p *Project) Refresh() error {
	if p.IsProject {
		if err := p.refreshChapters(); err != nil {
			return err
		}
	}
	return p.refresh()
}

// nextChapterNumber returns one past the highest ordering prefix currently on
// disk in chapters/, or 1 when none carry a prefix. It reads the directory
// directly so a chapter added externally since the last refresh still can't
// collide.
func (p *Project) nextChapterNumber() int {
	max := 0
	chaptersPath := filepath.Join(p.Root, "chapters")
	if _, err := fsutil.ContainedPath(p.Root, chaptersPath, false); err != nil {
		return 1
	}
	entries, err := os.ReadDir(chaptersPath)
	if err != nil {
		return 1
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if m := chapterPrefixRe.FindStringSubmatch(e.Name()); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil && n > max {
				max = n
			}
		}
	}
	return max + 1
}

func (p *Project) ChapterRelativePath(path string) string {
	chaptersPath := filepath.Join(p.Root, "chapters")
	rel, err := filepath.Rel(chaptersPath, path)
	if err != nil {
		return path
	}
	return rel
}

func (p *Project) ReadFile(path string) (string, error) {
	content, err := fsutil.ReadFileUnderRoot(p.Root, path)
	if err != nil {
		return "", err
	}
	return string(content), nil
}

func (p *Project) SaveFile(path, content string) error {
	// Manuscript saves keep a .bak of the prior version — this is the primary
	// data-loss surface (`:w` over an existing chapter).
	return fsutil.WriteFileUnderRoot(p.Root, path, []byte(content), 0644, true)
}

func (p *Project) RelativePath(path string) string {
	if p.Root == "" {
		return path
	}
	rel, err := filepath.Rel(p.Root, path)
	if err != nil {
		return path
	}
	return rel
}

func (p *Project) SetCurrentFile(path string) {
	p.CurrentFile = path
}

// SearchMatch is one project-wide search hit.
type SearchMatch struct {
	Chapter string // chapter file name
	Path    string // absolute path
	Line    int    // 1-based line number
	Text    string // the matching line, trimmed
}

// SearchChapters returns every line across all chapters that contains pattern
// case-insensitively, ordered by chapter then line. A blank
// pattern returns no matches. This is the read-side of "whole-manuscript search";
// the destructive replace (ReplaceInChapters) is gated behind an explicit
// confirm flow in the app.
func (p *Project) SearchChapters(pattern string) ([]SearchMatch, error) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return nil, nil
	}
	needle := strings.ToLower(pattern)

	var matches []SearchMatch
	for _, ch := range p.chapters {
		content, err := p.ReadFile(ch.Path)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", ch.Name, err)
		}
		for i, line := range strings.Split(content, "\n") {
			haystack := strings.ToLower(line)
			if strings.Contains(haystack, needle) {
				matches = append(matches, SearchMatch{
					Chapter: ch.Name,
					Path:    ch.Path,
					Line:    i + 1,
					Text:    strings.TrimSpace(line),
				})
			}
		}
	}
	return matches, nil
}

// ReplaceResult summarizes a project-wide replace.
type ReplaceResult struct {
	Chapters     int // number of chapter files changed
	Replacements int // total substitutions made
	Files        []ReplaceFileStatus
}

// ReplaceFileStatus records the transaction outcome for one chapter.
type ReplaceFileStatus struct {
	Path     string
	Read     string
	Stage    string
	Commit   string
	Rollback string
}

// ReplaceError retains the complete per-file transaction ledger.
type ReplaceError struct {
	Cause error
	Files []ReplaceFileStatus
}

func (e *ReplaceError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "replace failed: %v", e.Cause)
	for _, file := range e.Files {
		fmt.Fprintf(&b, "; %s [read=%s stage=%s commit=%s rollback=%s]",
			filepath.Base(file.Path), file.Read, file.Stage, file.Commit, file.Rollback)
	}
	return b.String()
}

func (e *ReplaceError) Unwrap() error { return e.Cause }

type replaceJournalEntry struct {
	path         string
	original     []byte
	mode         os.FileMode
	stagedPath   string
	replacements int
	read         string
	stage        string
	commit       string
	rollback     string
}

// CountReplacements reports how many literal occurrences of old exist across all
// chapters (the preview count for :replace). old is matched case-sensitively.
func (p *Project) CountReplacements(old string) (int, error) {
	if old == "" {
		return 0, nil
	}
	total := 0
	for _, ch := range p.chapters {
		content, err := p.ReadFile(ch.Path)
		if err != nil {
			return 0, fmt.Errorf("read %s: %w", ch.Name, err)
		}
		total += strings.Count(content, old)
	}
	return total, nil
}

// ReplaceInChapters replaces every literal occurrence of old as one transaction.
// Every target is read and validated before outputs are staged; commits are
// rolled back from the in-memory journal if any later commit fails.
func (p *Project) ReplaceInChapters(old, replacement string) (ReplaceResult, error) {
	var res ReplaceResult
	if old == "" {
		return res, nil
	}
	journal := make([]replaceJournalEntry, len(p.chapters))
	for i, ch := range p.chapters {
		journal[i] = replaceJournalEntry{path: ch.Path, read: "not attempted", stage: "not needed", commit: "not needed", rollback: "not needed"}
	}
	fail := func(err error) (ReplaceResult, error) {
		res.Files = replaceStatuses(journal)
		return res, &ReplaceError{Cause: err, Files: res.Files}
	}
	for i, ch := range p.chapters {
		journal[i].read = "failed"
		if err := p.injectReplaceFault("read", ch.Path); err != nil {
			return fail(fmt.Errorf("read %s: %w", ch.Name, err))
		}
		path, err := fsutil.ContainedPath(p.Root, ch.Path, false)
		if err != nil {
			return fail(fmt.Errorf("read %s: %w", ch.Name, err))
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return fail(fmt.Errorf("read %s: %w", ch.Name, err))
		}
		info, err := os.Stat(path)
		if err != nil || !info.Mode().IsRegular() {
			if err == nil {
				err = fmt.Errorf("not a regular file")
			}
			return fail(fmt.Errorf("validate %s: %w", ch.Name, err))
		}
		journal[i].path = path
		journal[i].original = content
		journal[i].mode = info.Mode()
		journal[i].read = "ok"
		n := strings.Count(string(content), old)
		journal[i].replacements = n
		if n == 0 {
			continue
		}
	}

	for i := range journal {
		entry := &journal[i]
		if entry.replacements == 0 {
			continue
		}
		if err := p.injectReplaceFault("stage", entry.path); err != nil {
			entry.stage = "failed: " + err.Error()
			cleanupReplaceStages(journal)
			return fail(fmt.Errorf("stage %s: %w", filepath.Base(entry.path), err))
		}
		staged, err := stageReplacement(entry.path,
			[]byte(strings.ReplaceAll(string(entry.original), old, replacement)), entry.mode)
		if err != nil {
			entry.stage = "failed: " + err.Error()
			cleanupReplaceStages(journal)
			return fail(fmt.Errorf("stage %s: %w", filepath.Base(entry.path), err))
		}
		entry.stagedPath = staged
		entry.stage = "ok"
		entry.commit = "not attempted"
	}

	committed := make([]int, 0, len(journal))
	for i := range journal {
		entry := &journal[i]
		if entry.replacements == 0 {
			continue
		}
		if _, err := fsutil.ContainedPath(p.Root, entry.path, false); err == nil {
			err = p.injectReplaceFault("commit", entry.path)
			if err == nil {
				err = os.Rename(entry.stagedPath, entry.path)
			}
			if err == nil {
				entry.stagedPath = ""
				entry.commit = "committed"
				committed = append(committed, i)
				continue
			}
			cleanupReplaceStages(journal)
			entry.commit = "failed: " + err.Error()
			rollbackReplace(p, journal, committed)
			return fail(fmt.Errorf("commit %s: %w", filepath.Base(entry.path), err))
		} else {
			cleanupReplaceStages(journal)
			entry.commit = "failed: " + err.Error()
			rollbackReplace(p, journal, committed)
			return fail(fmt.Errorf("commit %s: %w", filepath.Base(entry.path), err))
		}
	}
	res.Chapters = len(committed)
	for _, i := range committed {
		res.Replacements += journal[i].replacements
	}
	res.Files = replaceStatuses(journal)
	return res, nil
}

func (p *Project) injectReplaceFault(phase, path string) error {
	if p.replaceFault != nil {
		return p.replaceFault(phase, path)
	}
	return nil
}

func stageReplacement(path string, data []byte, mode os.FileMode) (string, error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".replace-*")
	if err != nil {
		return "", err
	}
	name := tmp.Name()
	ok := false
	defer func() {
		_ = tmp.Close()
		if !ok {
			_ = os.Remove(name)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		return "", err
	}
	if err := tmp.Sync(); err != nil {
		return "", err
	}
	if err := tmp.Chmod(mode); err != nil {
		return "", err
	}
	if err := tmp.Close(); err != nil {
		return "", err
	}
	ok = true
	return name, nil
}

func cleanupReplaceStages(journal []replaceJournalEntry) {
	for _, entry := range journal {
		if entry.stagedPath != "" {
			_ = os.Remove(entry.stagedPath)
		}
	}
}

func rollbackReplace(p *Project, journal []replaceJournalEntry, committed []int) {
	for i := len(committed) - 1; i >= 0; i-- {
		entry := &journal[committed[i]]
		if err := p.injectReplaceFault("rollback", entry.path); err != nil {
			entry.rollback = "failed: " + err.Error()
			continue
		}
		if _, err := fsutil.ContainedPath(p.Root, entry.path, false); err == nil {
			err = fsutil.WriteFile(entry.path, entry.original, entry.mode)
			if err == nil {
				entry.rollback = "restored"
				continue
			}
			entry.rollback = "failed: " + err.Error()
		} else {
			entry.rollback = "failed: " + err.Error()
		}
	}
}

func replaceStatuses(journal []replaceJournalEntry) []ReplaceFileStatus {
	statuses := make([]ReplaceFileStatus, len(journal))
	for i, entry := range journal {
		statuses[i] = ReplaceFileStatus{Path: entry.path, Read: entry.read, Stage: entry.stage, Commit: entry.commit, Rollback: entry.rollback}
	}
	return statuses
}

// ChapterWords is a per-chapter word count.
type ChapterWords struct {
	Chapter string
	Words   int
}

// WordCount returns the total manuscript word count plus a per-chapter breakdown
// in chapter order. Words are whitespace-separated tokens (a rough goal metric,
// markdown syntax included).
func (p *Project) WordCount() (int, []ChapterWords) {
	total, per, _ := p.WordCountWithOverlay("", "")
	return total, per
}

// WordCountWithOverlay counts every chapter, substituting overlayContent for
// overlayPath. It fails rather than silently treating an unreadable chapter as
// empty. The overlay lets callers include a dirty in-memory current chapter.
func (p *Project) WordCountWithOverlay(overlayPath, overlayContent string) (int, []ChapterWords, error) {
	total := 0
	per := make([]ChapterWords, 0, len(p.chapters))
	for _, ch := range p.chapters {
		content := overlayContent
		if overlayPath == "" || !samePath(ch.Path, overlayPath) {
			var err error
			content, err = p.ReadFile(ch.Path)
			if err != nil {
				return 0, nil, fmt.Errorf("read %s: %w", ch.Name, err)
			}
		}
		n := len(strings.Fields(content))
		total += n
		per = append(per, ChapterWords{Chapter: ch.Name, Words: n})
	}
	return total, per, nil
}

// Compile concatenates all chapters (in order) into a single Markdown document,
// each separated by a blank line.
func (p *Project) Compile() (string, error) {
	return p.CompileWithOverlay("", "")
}

// CompileWithOverlay is Compile with an optional in-memory chapter overlay.
func (p *Project) CompileWithOverlay(overlayPath, overlayContent string) (string, error) {
	var b strings.Builder
	for i, ch := range p.chapters {
		content := overlayContent
		if overlayPath == "" || !samePath(ch.Path, overlayPath) {
			var err error
			content, err = p.ReadFile(ch.Path)
			if err != nil {
				return "", fmt.Errorf("read %s: %w", ch.Name, err)
			}
		}
		if i > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(strings.TrimRight(content, "\n"))
		b.WriteString("\n")
	}
	return b.String(), nil
}

func samePath(a, b string) bool {
	aa, errA := filepath.Abs(a)
	bb, errB := filepath.Abs(b)
	return errA == nil && errB == nil && filepath.Clean(aa) == filepath.Clean(bb)
}

// CompiledPath is where CompileToFile writes the combined manuscript. It lives in
// the project root (not chapters/), so recompiling never reads its own output.
func (p *Project) CompiledPath() string {
	return filepath.Join(p.Root, "manuscript.md")
}

// CompileToFile compiles all chapters and writes them to CompiledPath, keeping a
// .bak of any prior compile, and returns the path written.
func (p *Project) CompileToFile() (string, error) {
	return p.CompileToFileWithOverlay("", "")
}

// IsChapter reports whether path is one of the project's chapter files.
func (p *Project) IsChapter(path string) bool {
	if path == "" {
		return false
	}
	for _, ch := range p.chapters {
		if samePath(ch.Path, path) {
			return true
		}
	}
	return false
}

// CompileToFileWithOverlay is CompileToFile with an in-memory chapter overlay.
func (p *Project) CompileToFileWithOverlay(overlayPath, overlayContent string) (string, error) {
	text, err := p.CompileWithOverlay(overlayPath, overlayContent)
	if err != nil {
		return "", err
	}
	path := p.CompiledPath()
	if err := fsutil.WriteFileUnderRoot(p.Root, path, []byte(text), 0644, true); err != nil {
		return "", err
	}
	return path, nil
}

func (p *Project) CreateChapter(name string) (string, error) {
	// Reject path separators / traversal / control chars before the name ever
	// reaches a filesystem path.
	name, err := fsutil.SanitizeName(name)
	if err != nil {
		return "", err
	}

	// The app manages the ordering prefix and extension; strip any the user
	// typed so we don't end up with "005_007_Title.md" or "Title.md.md".
	title := strings.TrimSuffix(name, ".md")
	title = strings.TrimSuffix(title, ".MD")
	title = chapterPrefixRe.ReplaceAllString(title, "")
	if title == "" {
		return "", fmt.Errorf("invalid name: nothing usable after sanitization")
	}

	// Auto-assign a zero-padded ordering prefix (one past the current max) so
	// chapters have a stable, app-managed order regardless of title.
	filename := fmt.Sprintf("%03d_%s.md", p.nextChapterNumber(), title)

	chaptersPath := filepath.Join(p.Root, "chapters")
	filePath := filepath.Join(chaptersPath, filename)

	// Check if file already exists
	if _, err := os.Stat(filePath); err == nil {
		return "", os.ErrExist
	}

	// Create the file with a heading based on the (prefix-free) title
	content := "# " + title + "\n\n"
	if err := fsutil.WriteFileUnderRoot(p.Root, filePath, []byte(content), 0644, false); err != nil {
		return "", err
	}

	// Refresh chapter list
	if err := p.refreshChapters(); err != nil {
		return "", err
	}

	return filePath, nil
}
