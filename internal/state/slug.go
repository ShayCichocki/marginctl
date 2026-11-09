package state

import (
	"strings"
	"unicode"

	"github.com/rivo/uniseg"
	"golang.org/x/text/unicode/norm"

	"github.com/ShayCichocki/marginctl/internal/mdtitle"
)

const (
	maxSlugGraphemes = 80
	maxSlugBytes     = 200
)

var windowsReserved = func() map[string]bool {
	m := map[string]bool{"con": true, "prn": true, "aux": true, "nul": true}
	for c := '1'; c <= '9'; c++ {
		m["com"+string(c)] = true
		m["lpt"+string(c)] = true
	}
	return m
}()

// Slug turns an entry's real name into a safe filename stem: lowercase, Unicode
// letters, digits and combining marks kept, every run of other characters
// (separators, punctuation, whitespace, emoji) collapsed to a single "-",
// trimmed, and cut at a grapheme boundary. An empty result becomes "entry" and
// Windows-reserved device names get a suffix, so the result always passes
// fsutil.PlainName once ".md" is appended.
func Slug(name string) string {
	var b strings.Builder
	pendingSep := false
	for _, r := range name {
		if unicode.Is(unicode.Cf, r) {
			continue // invisible format characters neither join nor split words
		}
		if unicode.IsLetter(r) || unicode.IsDigit(r) || unicode.IsMark(r) {
			if pendingSep && b.Len() > 0 {
				b.WriteByte('-')
			}
			pendingSep = false
			b.WriteRune(unicode.ToLower(r))
		} else {
			pendingSep = true
		}
	}

	var out strings.Builder
	g := uniseg.NewGraphemes(b.String())
	n := 0
	for g.Next() {
		if n >= maxSlugGraphemes || out.Len()+len(g.Str()) > maxSlugBytes {
			break
		}
		out.WriteString(g.Str())
		n++
	}
	slug := strings.Trim(out.String(), "-")
	if slug == "" {
		slug = "entry"
	}
	if windowsReserved[slug] {
		slug += "-entry"
	}
	return slug
}

// readTitle returns an entry's own title; see mdtitle.ReadFile for the rule.
func readTitle(path string) string { return mdtitle.ReadFile(path) }

// normalizeName is the comparison key for entry display names: Unicode NFC,
// invisible format characters (ZWSP, ZWNJ, ZWJ, BOM, soft hyphen) removed, and
// case folded, so visually identical names compare equal.
func normalizeName(s string) string {
	s = norm.NFC.String(s)
	var b strings.Builder
	for _, r := range s {
		if unicode.Is(unicode.Cf, r) {
			continue
		}
		fold := r
		for c := unicode.SimpleFold(r); c != r; c = unicode.SimpleFold(c) {
			if c < fold {
				fold = c
			}
		}
		b.WriteRune(fold)
	}
	return b.String()
}
