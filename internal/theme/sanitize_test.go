package theme

import (
	"strings"
	"testing"
	"unicode"
)

func TestSanitizeTerminalControls(t *testing.T) {
	tests := []struct {
		name string
		in   string
	}{
		{"CSI", "before\x1b[31mafter"},
		{"OSC8", "\x1b]8;;https://evil.example\x07click\x1b]8;;\x07"},
		{"OSC52", "\x1b]52;c;c2VjcmV0\x07"},
		{"BEL", "ring\x07"},
		{"CRLF", "one\r\ntwo"},
		{"C0", "\x00\x01\x08\x09\x0b\x0c\x1f"},
		{"embedded ESC", "a\x1bb"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Sanitize(tt.in)
			if strings.ContainsRune(got, '\x1b') {
				t.Fatalf("Sanitize(%q) retained ESC: %q", tt.in, got)
			}
			for _, r := range got {
				if r != '\n' && (unicode.IsControl(r) || (unicode.In(r, unicode.Cf) && r != '\u200c' && r != '\u200d')) {
					t.Fatalf("Sanitize(%q) retained control U+%04X: %q", tt.in, r, got)
				}
			}
			if len([]rune(got)) != len([]rune(tt.in)) {
				t.Fatalf("rune count changed: got %d want %d", len([]rune(got)), len([]rune(tt.in)))
			}
		})
	}
}

func TestSanitizePreservesDisplayTextAndNewlines(t *testing.T) {
	in := "plain café 👩‍💻\nsecond line"
	if got := Sanitize(in); got != in {
		t.Fatalf("Sanitize(%q) = %q", in, got)
	}
}
