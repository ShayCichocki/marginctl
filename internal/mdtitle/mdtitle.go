// Package mdtitle extracts a markdown file's title (state entries, chapters).
// It is a dependency-free leaf so project and state can share one rule.
package mdtitle

import (
	"bufio"
	"io"
	"os"
	"strings"
)

// readLimit bounds how much of a file is read to find its heading.
const readLimit = 2048

// ReadFile returns a markdown file's own title: the "# " heading when it is the first
// non-blank line of the body (after an optional front-matter block), else the
// front-matter "name:" value, else "". A "# " line further down (a section
// heading in a hand-made file) is never a title. Only the first 2KB is read.
func ReadFile(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	buf, err := io.ReadAll(io.LimitReader(f, readLimit))
	if err != nil {
		return ""
	}
	text := strings.TrimPrefix(string(buf), "\xEF\xBB\xBF")
	truncated := len(buf) == readLimit
	sc := bufio.NewScanner(strings.NewReader(text))
	var lines []string
	for sc.Scan() {
		lines = append(lines, strings.TrimRight(sc.Text(), "\r"))
	}
	// A cut-off final line may hold a partial heading; ignore it.
	if truncated && len(lines) > 0 && !strings.HasSuffix(text, "\n") {
		lines = lines[:len(lines)-1]
	}
	i := 0
	fmName := ""
	if len(lines) > 0 && strings.TrimSpace(lines[0]) == "---" {
		i = 1
		for i < len(lines) && strings.TrimSpace(lines[i]) != "---" {
			if k, v, ok := strings.Cut(lines[i], ":"); ok && strings.TrimSpace(k) == "name" {
				fmName = strings.TrimSpace(v)
			}
			i++
		}
		if i >= len(lines) {
			return ""
		}
		i++
	}
	for ; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		if strings.HasPrefix(lines[i], "# ") {
			if h := strings.TrimSpace(lines[i][2:]); h != "" {
				return h
			}
		}
		break
	}
	return fmName
}
