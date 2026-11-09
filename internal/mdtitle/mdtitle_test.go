package mdtitle

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadFile(t *testing.T) {
	cases := map[string]string{
		"# Chapter 1\n\nText":                     "Chapter 1",
		"\n\n# After Blanks\ntext":                "After Blanks",
		"No heading here\n# Later\n":              "",
		"text first\n\n# Not a title":             "",
		"---\nname: FM Name\n---\n# Body Title\n": "Body Title",
		"---\nname: FM Name\n---\nplain\n":        "FM Name",
		"---\nstatus: x\n---\nplain\n":            "",
		"---\nname: unterminated\n# T\n":          "",
		"\xEF\xBB\xBF# BOM Title\r\nx":            "BOM Title",
		"# \nbody":                                "",
		"#NoSpace\n":                              "",
	}
	for in, want := range cases {
		p := filepath.Join(t.TempDir(), "f.md")
		if err := os.WriteFile(p, []byte(in), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := ReadFile(p); got != want {
			t.Errorf("ReadFile(%q) = %q, want %q", in, got, want)
		}
	}
	if ReadFile(filepath.Join(t.TempDir(), "missing.md")) != "" {
		t.Error("missing file should yield empty title")
	}
}

func TestReadFileBoundedAndPartialLine(t *testing.T) {
	p := filepath.Join(t.TempDir(), "f.md")
	// Heading line is cut off by the 2KB limit: ignored, not truncated.
	if err := os.WriteFile(p, []byte("# "+strings.Repeat("x", 5000)), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := ReadFile(p); got != "" {
		t.Errorf("partial heading = %q, want empty", got)
	}
}
