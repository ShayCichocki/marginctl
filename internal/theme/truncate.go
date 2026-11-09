package theme

import "github.com/mattn/go-runewidth"

// Truncate shortens s so it fits within maxWidth terminal cells, appending an
// ellipsis ("…") when it has to cut. It is rune- and display-width-aware: it
// never slices through a multibyte rune (which byte slicing would corrupt) and
// it accounts for wide runes (CJK/emoji occupy two cells). A non-positive
// maxWidth yields the empty string.
func Truncate(s string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	if runewidth.StringWidth(s) <= maxWidth {
		return s
	}
	// Reserve one cell for the ellipsis; below that there's no room for even a
	// single content cell, so render the ellipsis alone.
	if maxWidth == 1 {
		return "…"
	}
	// runewidth.Truncate appends the tail ("…") itself, fitting the result to
	// maxWidth cells without splitting a rune.
	return runewidth.Truncate(s, maxWidth, "…")
}
