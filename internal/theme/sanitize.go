package theme

import (
	"strings"
	"unicode"
)

// Sanitize makes untrusted text safe to pass to terminal renderers. Newlines
// remain layout separators; every other control rune becomes one visible cell.
// The one-rune replacement is important to editor rendering, where source rune
// indexes must continue to identify the same displayed cell.
func Sanitize(s string) string {
	if !strings.ContainsFunc(s, unsafeDisplayRune) {
		return s
	}

	return strings.Map(func(r rune) rune {
		if r == '\n' {
			return r
		}
		if r >= 0 && r <= 0x1f {
			return 0x2400 + r // Unicode control pictures: NUL through US.
		}
		if r == 0x7f {
			return 0x2421 // SYMBOL FOR DELETE
		}
		if unicode.IsControl(r) || (unicode.In(r, unicode.Cf) && !safeFormatRune(r)) {
			return '\ufffd'
		}
		return r
	}, s)
}

func unsafeDisplayRune(r rune) bool {
	return r != '\n' && (unicode.IsControl(r) || (unicode.In(r, unicode.Cf) && !safeFormatRune(r)))
}

func safeFormatRune(r rune) bool {
	// Join controls are part of user-visible grapheme clusters, notably emoji
	// ZWJ sequences. They cannot initiate terminal control protocols.
	return r == '\u200c' || r == '\u200d'
}
