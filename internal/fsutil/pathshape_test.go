package fsutil

import "testing"

func TestPlainName(t *testing.T) {
	cases := []struct {
		in string
		ok bool
	}{
		{"Alice", true}, {"Wait", true}, {"Mr. X", true}, {"a.b", true}, {"Mr.. X", true}, {"a..b", true}, {".hidden", true},
		{"", false}, {".", false}, {"..", false}, {"...", false}, {".. ", false}, {" ", false}, {" . ", false}, {"Wait...", false}, {"Wait.", false}, {"Wait ", false}, {"a/b", false}, {"a\\b", false},
		{"a\x00b", false}, {"../x", false}, {"/abs", false},
	}
	for _, c := range cases {
		if err := PlainName(c.in); (err == nil) != c.ok {
			t.Errorf("PlainName(%q) err=%v, want ok=%v", c.in, err, c.ok)
		}
	}
}

func TestSafeRelPath(t *testing.T) {
	cases := []struct {
		in string
		ok bool
	}{
		{"characters/alice.md", true}, {"timeline.md", true}, {"a/b/c.md", true}, {"a..b/c", true}, {"Mr. X/y.md", true},
		{"", false}, {"/etc/passwd", false}, {"../x", false}, {"a/../b", false}, {"a/..", false}, {"..", false}, {".", false}, {"a/./b", false}, {"...", false}, {"a/.../b", false}, {"a/.. /b", false}, {"a/ /b", false}, {"Wait.../x", false}, {"a/b.", false}, {"a/b ", false},
		{"a\\b", false}, {"a\x00b", false},
	}
	for _, c := range cases {
		if err := SafeRelPath(c.in); (err == nil) != c.ok {
			t.Errorf("SafeRelPath(%q) err=%v, want ok=%v", c.in, err, c.ok)
		}
	}
}
