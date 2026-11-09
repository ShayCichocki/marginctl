package statusbar

import (
	"strings"
	"testing"
)

func TestViewSanitizesFileAndMode(t *testing.T) {
	m := New()
	m.FilePath = "draft\x1b]52;c;c2VjcmV0\x07.md"
	m.Mode = "NORMAL\r"
	m.SetWidth(80)
	if view := m.View(); strings.ContainsAny(view, "\x1b\x07\r") {
		t.Fatalf("view retained terminal controls: %q", view)
	}
}
