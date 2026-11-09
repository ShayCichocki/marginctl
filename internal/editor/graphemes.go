package editor

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/rivo/uniseg"
)

// runeLen returns the number of extended grapheme clusters in s. The historical
// name is retained inside the package, but all editor columns are cluster indices.
func runeLen(s string) int {
	return len(graphemes(s))
}

// simpleASCII reports whether s[i] is a one-byte grapheme cluster: an ASCII byte
// that is not CR (CR LF is a single cluster) and is not followed by a non-ASCII
// byte (which could be a combining mark, ZWJ or variation selector extending
// it). Two adjacent ASCII bytes never join unless they are CR LF.
func simpleASCII(s string, i int) bool {
	return s[i] < utf8.RuneSelf && s[i] != '\r' && (i+1 == len(s) || s[i+1] < utf8.RuneSelf)
}

func isPlainASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= utf8.RuneSelf || s[i] == '\r' {
			return false
		}
	}
	return true
}

func graphemes(s string) []string {
	if s == "" {
		return nil
	}
	if isPlainASCII(s) {
		out := make([]string, len(s))
		for i := range out {
			out[i] = s[i : i+1]
		}
		return out
	}
	out := make([]string, 0, len(s)/2+1)
	state := -1
	for rest := s; rest != ""; {
		var c string
		c, rest, _, state = uniseg.FirstGraphemeClusterInString(rest, state)
		out = append(out, c)
	}
	return out
}

func joinGraphemes(g []string) string { return strings.Join(g, "") }

// graphemeCount returns the number of clusters in s without allocating.
func graphemeCount(s string) int {
	_, n, _ := graphemeOffset(s, len(s))
	return n
}

// graphemeOffset walks s once, without allocating, and returns the byte offset
// where cluster index col starts (clamped to len(s)), the number of clusters
// before that offset (== col unless s is shorter), and the byte offset where the
// last of those clusters starts (0 when n == 0).
func graphemeOffset(s string, col int) (off, n, lastStart int) {
	state := -1
	for n < col && off < len(s) {
		lastStart = off
		if simpleASCII(s, off) {
			off++
			state = -1
		} else {
			var c string
			c, _, _, state = uniseg.FirstGraphemeClusterInString(s[off:], state)
			off += len(c)
		}
		n++
	}
	return off, n, lastStart
}

// graphemeLen is the byte length of the first cluster of non-empty s.
func graphemeLen(s string) int {
	if simpleASCII(s, 0) {
		return 1
	}
	c, _, _, _ := uniseg.FirstGraphemeClusterInString(s, -1)
	return len(c)
}

// firstNonBlank returns the rune column of the first non-space/tab rune in s,
// or 0 if the line is blank.
func firstNonBlank(s string) int {
	for i, cluster := range graphemes(s) {
		if cluster != " " && cluster != "\t" {
			return i
		}
	}
	return 0
}

func clusterIsSpace(s string) bool {
	for _, r := range s {
		return unicode.IsSpace(r)
	}
	return false
}

func clusterDisplayWidth(s string, column int) int {
	if s == "\t" {
		return 4 - column%4
	}
	if len(s) == 1 && s[0] < utf8.RuneSelf {
		// ASCII fast path; must agree with uniseg (checked by a test over all
		// 128 bytes): printable bytes are one column, controls and DEL are zero.
		if s[0] >= 0x20 && s[0] < 0x7f {
			return 1
		}
		return 0
	}
	return uniseg.StringWidth(s)
}
