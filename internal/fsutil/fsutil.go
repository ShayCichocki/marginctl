// Package fsutil provides crash-safe file writing helpers shared across the
// packages that persist manuscript and story-state files.
package fsutil

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

var ErrUnsafePath = errors.New("path is outside root or contains a symlink")

// Fingerprint describes a file strongly enough to detect replacement, deletion,
// and content changes hidden by coarse or deliberately preserved timestamps.
type Fingerprint struct {
	CanonicalPath string
	Exists        bool
	Identity      FileIdentity
	Size          int64
	ModTime       time.Time
	SHA256        [sha256.Size]byte
	info          os.FileInfo
}

// FileFingerprint returns the target identity and content fingerprint for path.
// A missing target is represented by Exists=false rather than an error.
func FileFingerprint(path string) (Fingerprint, error) {
	canonical, err := canonicalPath(path)
	if err != nil {
		return Fingerprint{}, err
	}
	fp := Fingerprint{CanonicalPath: canonical}
	info, err := os.Stat(canonical)
	if os.IsNotExist(err) {
		return fp, nil
	}
	if err != nil {
		return Fingerprint{}, err
	}
	if !info.Mode().IsRegular() {
		return Fingerprint{}, fmt.Errorf("not a regular file: %s", path)
	}
	data, err := os.ReadFile(canonical)
	if err != nil {
		return Fingerprint{}, err
	}
	fp.Exists = true
	fp.info = info
	fp.Identity = fileIdentity(info)
	fp.Size = info.Size()
	fp.ModTime = info.ModTime()
	fp.SHA256 = sha256.Sum256(data)
	return fp, nil
}

// SameFile reports whether two fingerprints name the same existing file. It
// uses platform identity when available and canonical path as the fallback.
func (f Fingerprint) SameFile(other Fingerprint) bool {
	if !f.Exists || !other.Exists {
		return false
	}
	if f.info != nil && other.info != nil {
		return os.SameFile(f.info, other.info)
	}
	if f.Identity.Valid && other.Identity.Valid {
		return f.Identity == other.Identity
	}
	return f.CanonicalPath == other.CanonicalPath
}

// Equal reports whether two paths currently describe the same unchanged file.
// Canonical aliases may differ, but an object replacement at the same path does
// not compare equal when platform identity is available.
func (f Fingerprint) Equal(other Fingerprint) bool {
	sameTarget := f.CanonicalPath == other.CanonicalPath
	if f.Exists && other.Exists {
		sameTarget = f.SameFile(other)
	}
	return sameTarget &&
		f.Exists == other.Exists &&
		f.Identity == other.Identity &&
		f.Size == other.Size &&
		f.ModTime.Equal(other.ModTime) &&
		f.SHA256 == other.SHA256
}

// canonicalPath resolves aliases for existing targets. For a new target it
// resolves the nearest existing ancestor, then appends the missing components.
func canonicalPath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return filepath.Clean(resolved), nil
	} else if !os.IsNotExist(err) {
		return "", err
	}

	ancestor := abs
	var missing []string
	for {
		if _, err := os.Lstat(ancestor); err == nil {
			resolved, err := filepath.EvalSymlinks(ancestor)
			if err != nil {
				return "", err
			}
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return filepath.Clean(resolved), nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(ancestor)
		if parent == ancestor {
			return "", fmt.Errorf("cannot canonicalize path: %s", path)
		}
		missing = append(missing, filepath.Base(ancestor))
		ancestor = parent
	}
}

// CanonicalRoot returns the absolute, symlink-resolved form of an existing
// directory. A caller may open a project through an alias, but all paths stored
// after that point are anchored to this canonical root.
func CanonicalRoot(root string) (string, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	root, err = filepath.EvalSymlinks(abs)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(root)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("root is not a directory: %s", root)
	}
	return filepath.Clean(root), nil
}

// ContainedPath validates that path is lexically beneath canonicalRoot and
// that every existing project-controlled component is not a symlink.
// allowMissing permits a missing final path (and missing descendants of an
// existing parent) for creation operations.
func ContainedPath(canonicalRoot, path string, allowMissing bool) (string, error) {
	root, err := filepath.Abs(canonicalRoot)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(root, abs)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) || filepath.IsAbs(rel) {
		// The OS may expose an alias above the project root (notably /var ->
		// /private/var on macOS). Find that root boundary, but never resolve a
		// component controlled by the project.
		ancestor := abs
		for {
			resolved, resolveErr := filepath.EvalSymlinks(ancestor)
			if resolveErr == nil && filepath.Clean(resolved) == root {
				rel, _ = filepath.Rel(ancestor, abs)
				abs = filepath.Join(root, rel)
				break
			}
			parent := filepath.Dir(ancestor)
			if parent == ancestor {
				return "", fmt.Errorf("%w: %s", ErrUnsafePath, path)
			}
			ancestor = parent
		}
	}

	current := root
	if rel == "." {
		return root, nil
	}
	parts := strings.Split(rel, string(os.PathSeparator))
	for i, part := range parts {
		current = filepath.Join(current, part)
		info, statErr := os.Lstat(current)
		if statErr != nil {
			if allowMissing && os.IsNotExist(statErr) {
				return abs, nil
			}
			return "", statErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("%w: %s", ErrUnsafePath, current)
		}
		if i < len(parts)-1 && !info.IsDir() {
			return "", fmt.Errorf("path parent is not a directory: %s", current)
		}
	}
	return abs, nil
}

// RejectSymlinkDir fails with ErrUnsafePath when dir itself is a symlink.
// ContainedPath only inspects components below its root, so callers that pass
// a subdirectory (e.g. <project>/state) as the root must check that directory
// separately. A missing dir is not an error here; later operations report it.
func RejectSymlinkDir(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: %s", ErrUnsafePath, dir)
	}
	return nil
}

func ReadFileUnderRoot(root, path string) ([]byte, error) {
	path, err := ContainedPath(root, path, false)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}

func WriteFileUnderRoot(root, path string, data []byte, perm os.FileMode, backup bool) error {
	path, err := ContainedPath(root, path, true)
	if err != nil {
		return err
	}
	if backup {
		return WriteFileWithBackup(path, data, perm)
	}
	return WriteFile(path, data, perm)
}

func MkdirAllUnderRoot(root, path string, perm os.FileMode) error {
	path, err := ContainedPath(root, path, true)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(path, perm); err != nil {
		return err
	}
	_, err = ContainedPath(root, path, false)
	return err
}

// maxNameLen bounds a sanitized filename component. It is well under the common
// 255-byte filesystem limit and leaves room for a ".md" suffix and a ".bak".
const maxNameLen = 100

// SanitizeName cleans a user-supplied name for safe use as a single path
// component. It takes only the first line (multi-line selections must not
// become multi-line filenames), drops control characters and path separators,
// strips leading dots so a name can neither escape its directory ("..") nor
// create a hidden file, collapses whitespace, and clamps the length. It returns
// an error when nothing usable remains, so callers can surface it instead of
// writing a garbage or traversing path.
func SanitizeName(name string) (string, error) {
	// Only the first line survives: a pasted "Add as Character" selection can
	// span many lines, and \r/\n must never reach a filename.
	if i := strings.IndexAny(name, "\r\n"); i >= 0 {
		name = name[:i]
	}

	var b strings.Builder
	for _, r := range name {
		switch {
		case unicode.IsControl(r):
			continue
		case r == '/' || r == '\\' || r == os.PathSeparator:
			continue
		// Windows-illegal filename characters. ':' additionally denotes an NTFS
		// alternate data stream, so it must never reach a filename on any platform.
		case strings.ContainsRune(`<>:"|?*`, r):
			continue
		default:
			b.WriteRune(r)
		}
	}

	cleaned := strings.TrimSpace(b.String())
	// Re-run after trimming because input such as ". ." can expose another
	// leading dot once surrounding dots and whitespace are removed.
	for strings.HasPrefix(cleaned, ".") {
		cleaned = strings.TrimSpace(strings.TrimLeft(cleaned, "."))
	}

	if utf8.RuneCountInString(cleaned) > maxNameLen {
		cleaned = strings.TrimSpace(string([]rune(cleaned)[:maxNameLen]))
	}
	// Windows silently drops trailing dots and spaces ("foo." opens "foo"), which
	// would desync the returned name from the file actually created. Strip them
	// last, after any length clamp could have exposed a new trailing dot/space.
	cleaned = strings.TrimRight(cleaned, ". ")

	if cleaned == "" {
		return "", fmt.Errorf("invalid name: nothing usable after sanitization")
	}
	// Windows reserved device names are invalid even with an extension; prefix an
	// underscore to neutralize rather than reject an otherwise-reasonable title.
	if isWindowsReservedName(cleaned) {
		cleaned = "_" + cleaned
	}
	return cleaned, nil
}

// isWindowsReservedName reports whether the name (ignoring any extension, case
// insensitively) is a reserved Windows device name: CON, PRN, AUX, NUL, COM1-9,
// LPT1-9. Such a name cannot be created as a file on Windows.
func isWindowsReservedName(name string) bool {
	base := name
	if i := strings.IndexByte(base, '.'); i >= 0 {
		base = base[:i]
	}
	// Windows ignores trailing spaces/dots on the base too, so "CON .txt" and
	// "CON." both resolve to the CON device.
	base = strings.TrimRight(base, " .")
	up := strings.ToUpper(base)
	switch up {
	case "CON", "PRN", "AUX", "NUL":
		return true
	}
	if len(up) == 4 && (strings.HasPrefix(up, "COM") || strings.HasPrefix(up, "LPT")) {
		return up[3] >= '1' && up[3] <= '9'
	}
	return false
}

// WriteFile atomically writes data to path. It writes to a temporary file in
// the same directory, fsyncs it to durable storage, and renames it over the
// target. Because rename is atomic, a crash (or a full disk mid-write) can
// never leave a truncated or zero-length file at path: either the old contents
// remain intact or the new contents are fully present. An existing file's mode
// is preserved (the manuscript "preserve modes" invariant).
func WriteFile(path string, data []byte, perm os.FileMode) error {
	return writeFile(path, data, perm, true)
}

// WriteFilePrivate atomically writes data to path with mode 0600, enforced even
// when a file already exists there with looser permissions. Unlike WriteFile it
// never inherits the existing mode: credential stores and private sidecars must
// stay owner-only regardless of how a prior (or externally created) file was set.
func WriteFilePrivate(path string, data []byte) error {
	return writeFile(path, data, 0o600, false)
}

// writeFile is the shared atomic-write core. When inheritMode is true it adopts
// an existing target's permissions; when false it writes exactly perm.
func writeFile(path string, data []byte, perm os.FileMode, inheritMode bool) error {
	dir := filepath.Dir(path)
	if inheritMode {
		if info, err := os.Stat(path); err == nil {
			perm = info.Mode().Perm()
		} else if !os.IsNotExist(err) {
			return err
		}
	}

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// Best-effort cleanup: harmless no-op once the rename below succeeds.
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, perm); err != nil {
		return err
	}

	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = d.Sync()
	closeErr := d.Close()
	if err != nil {
		return err
	}
	return closeErr
}

// WriteFileWithBackup behaves like WriteFile but, when a file already exists at
// path, first copies its current contents to path+".bak" (also atomically) so
// the prior version survives a bad overwrite. A missing target is not an error.
func WriteFileWithBackup(path string, data []byte, perm os.FileMode) error {
	existing, err := os.ReadFile(path)
	switch {
	case err == nil:
		mode := perm
		if info, statErr := os.Stat(path); statErr == nil {
			mode = info.Mode().Perm()
		}
		if err := WriteFile(path+".bak", existing, mode); err != nil {
			return err
		}
	case !os.IsNotExist(err):
		return err
	}

	return WriteFile(path, data, perm)
}

// ExpandHome expands a user-typed path that is exactly "~" or begins with "~/"
// (also "~\" on Windows) to the user's home directory. "~user" forms and every
// other path are returned unchanged. It errors only when expansion is needed and
// the home directory cannot be resolved. Use it only for paths the user types
// at a prompt, never for project-relative or model-supplied paths.
func ExpandHome(path string) (string, error) {
	rest, ok := "", false
	switch {
	case path == "~":
		ok = true
	case strings.HasPrefix(path, "~/"):
		rest, ok = path[2:], true
	case os.PathSeparator == '\\' && strings.HasPrefix(path, `~\`):
		rest, ok = path[2:], true
	}
	if !ok {
		return path, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot expand ~: %w", err)
	}
	if rest == "" {
		return home, nil
	}
	return filepath.Join(home, rest), nil
}
