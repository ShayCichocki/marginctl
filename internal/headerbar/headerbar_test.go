package headerbar

import (
	"strings"
	"testing"
)

func TestViewSanitizesTitle(t *testing.T) {
	m := New()
	m.Title = "title\x1b]0;owned\x07"
	m.SetWidth(40)
	if view := m.View(); strings.ContainsAny(view, "\x1b\x07") {
		t.Fatalf("view retained terminal controls: %q", view)
	}
}
