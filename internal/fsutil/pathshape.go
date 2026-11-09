package fsutil

import (
	"fmt"
	"path/filepath"
	"strings"
)

// PlainName validates the pure string shape of a single path component: it must
// be non-empty, contain no path separator, NUL, or volume name, and must not
// consist only of dots and spaces or end in a dot or space (Windows strips
// those, so "..." or "x." could alias "." or ".." or another file). A ".."
// embedded in a longer name ("a..b") is allowed since it cannot traverse.
// It does no filesystem access; callers still apply
// ContainedPath/RejectSymlinkDir.
func PlainName(s string) error {
	if s == "" || badDotSpaceShape(s) {
		return fmt.Errorf("invalid name %q", s)
	}
	if strings.ContainsAny(s, "/\\\x00") || filepath.VolumeName(s) != "" {
		return fmt.Errorf("invalid name %q", s)
	}
	return nil
}

// SafeRelPath validates the pure string shape of a forward-slash relative path:
// non-empty, no NUL or backslash, not absolute or rooted, no volume name, and no
// segment that is only dots and spaces or ends in a dot or space (Windows strips
// those, so "..." or ".. " could alias "." or ".."). It does no filesystem access; callers still apply
// ContainedPath/RejectSymlinkDir.
func SafeRelPath(s string) error {
	if s == "" {
		return fmt.Errorf("invalid path %q", s)
	}
	if strings.ContainsAny(s, "\\\x00") || strings.HasPrefix(s, "/") || filepath.IsAbs(s) || filepath.VolumeName(s) != "" {
		return fmt.Errorf("invalid path %q", s)
	}
	for _, seg := range strings.Split(s, "/") {
		if seg != "" && badDotSpaceShape(seg) {
			return fmt.Errorf("invalid path %q", s)
		}
	}
	return nil
}

// badDotSpaceShape reports whether a single component consists only of dots and
// spaces, or ends in a dot or space.
func badDotSpaceShape(seg string) bool {
	return strings.TrimLeft(seg, ". ") == "" || strings.HasSuffix(seg, ".") || strings.HasSuffix(seg, " ")
}
