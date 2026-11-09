package theme

import (
	"testing"
	"unicode/utf8"

	"github.com/mattn/go-runewidth"
)

func TestTruncate(t *testing.T) {
	tests := []struct {
		name     string
		in       string
		width    int
		want     string
		maxCells int
	}{
		{"fits exactly", "hello", 5, "hello", 5},
		{"shorter than width", "hi", 10, "hi", 10},
		{"truncates ascii", "hello world", 7, "hello …", 7},
		{"zero width", "hello", 0, "", 0},
		{"negative width", "hello", -3, "", 0},
		{"width one", "hello", 1, "…", 1},
		{"multibyte fits", "café", 4, "café", 4},
		{"em dash fits", "a—b", 3, "a—b", 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Truncate(tt.in, tt.width)
			if got != tt.want {
				t.Fatalf("Truncate(%q, %d) = %q, want %q", tt.in, tt.width, got, tt.want)
			}
			if !utf8.ValidString(got) {
				t.Fatalf("Truncate(%q, %d) = %q is not valid UTF-8", tt.in, tt.width, got)
			}
			if w := runewidth.StringWidth(got); tt.width > 0 && w > tt.width {
				t.Fatalf("Truncate(%q, %d) width %d exceeds max %d", tt.in, tt.width, w, tt.width)
			}
		})
	}
}

// A truncation that lands on a multibyte boundary must never split the rune.
func TestTruncateNeverSplitsRune(t *testing.T) {
	// "café" then more; cutting at 3 cells must keep whole runes.
	got := Truncate("café society", 4)
	if !utf8.ValidString(got) {
		t.Fatalf("result %q not valid UTF-8", got)
	}
	if runewidth.StringWidth(got) > 4 {
		t.Fatalf("result %q wider than 4 cells", got)
	}
}
