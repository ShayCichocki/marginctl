package config

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// errUnsafeStructure means the surgical editor cannot prove it can edit the
// file in place without guessing; callers fall back to a full rewrite (after
// backing the original up).
var errUnsafeStructure = errors.New("unsupported TOML structure")

func unsafef(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errUnsafeStructure, fmt.Sprintf(format, args...))
}

// llmEdit is one change to a key of the [llm] table. A nil Value removes the
// key; otherwise Value is the TOML-encoded value text (e.g. `"abc"`).
type llmEdit struct {
	Key   string
	Value *string
}

func setString(key, value string) llmEdit {
	enc := tomlQuote(value)
	return llmEdit{Key: key, Value: &enc}
}

func removeKey(key string) llmEdit { return llmEdit{Key: key} }

// tomlQuote encodes s as a TOML basic string.
func tomlQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		case utf8.RuneError:
			b.WriteString(`�`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04X`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

type lineKind int

const (
	kOther lineKind = iota
	kHeaderLLM
	kHeaderOther
	kKeyLLM // key/value line inside [llm]
)

// editLLMTable applies edits to the [llm] table of a TOML document, touching
// only the affected lines and preserving every other byte. It returns an error
// wrapping errUnsafeStructure whenever the document uses a construct it cannot
// edit without guessing (multi-line strings/arrays, dotted or inline llm keys,
// [[llm]], duplicate tables, ...).
func editLLMTable(src string, edits []llmEdit) (string, error) {
	nl := "\n"
	if strings.Contains(src, "\r\n") {
		nl = "\r\n"
	}
	lines, kinds, names, hasLLM, err := scanLines(src)
	if err != nil {
		return "", err
	}
	if !hasLLM {
		needTable := false
		for _, e := range edits {
			if e.Value != nil {
				needTable = true
			}
		}
		if !needTable {
			return src, nil
		}
		// Append a fresh [llm] table, separated from prior content by a blank line.
		var sb strings.Builder
		sb.WriteString(src)
		if src != "" {
			if !strings.HasSuffix(src, "\n") {
				sb.WriteString(nl)
			}
			if strings.TrimSpace(lines[len(lines)-1]) != "" {
				sb.WriteString(nl)
			}
		}
		sb.WriteString("[llm]" + nl)
		src = sb.String()
		if lines, kinds, names, _, err = scanLines(src); err != nil {
			return "", err
		}
	}

	for _, e := range edits {
		idx := -1
		for i := range lines {
			if kinds[i] == kKeyLLM && names[i] == e.Key {
				idx = i
			}
		}
		switch {
		case idx >= 0 && e.Value == nil:
			lines[idx] = ""
			kinds[idx] = kOther
		case idx >= 0:
			nline, err := replaceValue(lines[idx], *e.Value)
			if err != nil {
				return "", err
			}
			lines[idx] = nline
		case e.Value == nil:
			// Nothing to remove.
		default:
			// Insert after the last key of [llm], or right under its header.
			insertAt := -1
			for i := range lines {
				if kinds[i] == kHeaderLLM {
					insertAt = i + 1
				}
			}
			for i := range lines {
				if kinds[i] == kKeyLLM {
					insertAt = i + 1
				}
			}
			if !strings.HasSuffix(lines[insertAt-1], "\n") {
				lines[insertAt-1] += nl
			}
			newLine := e.Key + " = " + *e.Value + nl
			lines = append(lines[:insertAt], append([]string{newLine}, lines[insertAt:]...)...)
			kinds = append(kinds[:insertAt], append([]lineKind{kKeyLLM}, kinds[insertAt:]...)...)
			names = append(names[:insertAt], append([]string{e.Key}, names[insertAt:]...)...)
		}
	}
	return strings.Join(lines, ""), nil
}

// scanLines splits src into lines (keeping terminators), classifies each, and
// rejects structures the editor can't handle safely.
func scanLines(src string) (lines []string, kinds []lineKind, names []string, hasLLM bool, err error) {
	lines = strings.SplitAfter(src, "\n")
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	kinds = make([]lineKind, len(lines))
	names = make([]string, len(lines))

	inLLM, seenHeader := false, false
	for i, raw := range lines {
		t := strings.TrimSpace(raw)
		if t == "" || t[0] == '#' {
			continue
		}
		if t[0] == '[' {
			name, array, herr := parseHeader(t)
			if herr != nil {
				return nil, nil, nil, false, herr
			}
			seenHeader = true
			if name == "llm" {
				if array {
					return nil, nil, nil, false, unsafef("[[llm]] array table")
				}
				if hasLLM {
					return nil, nil, nil, false, unsafef("duplicate [llm] table")
				}
				hasLLM, inLLM = true, true
				kinds[i] = kHeaderLLM
			} else {
				inLLM = false
				kinds[i] = kHeaderOther
			}
			continue
		}
		key, rest, kerr := splitKeyValue(t)
		if kerr != nil {
			return nil, nil, nil, false, kerr
		}
		if cerr := checkValueClosed(rest); cerr != nil {
			return nil, nil, nil, false, cerr
		}
		if !seenHeader && firstKeySegment(key) == "llm" {
			return nil, nil, nil, false, unsafef("top-level dotted or inline llm key")
		}
		if inLLM {
			if strings.ContainsAny(key, `"'.`) {
				return nil, nil, nil, false, unsafef("quoted or dotted key inside [llm]")
			}
			for j := range lines[:i] {
				if kinds[j] == kKeyLLM && names[j] == key {
					return nil, nil, nil, false, unsafef("duplicate key %s", key)
				}
			}
			kinds[i] = kKeyLLM
			names[i] = key
		}
	}
	return lines, kinds, names, hasLLM, nil
}

// parseHeader parses a `[name]` / `[[name]]` line (with optional trailing
// comment) and returns the name with surrounding quotes removed.
func parseHeader(t string) (name string, array bool, err error) {
	array = strings.HasPrefix(t, "[[")
	open, closer := "[", "]"
	if array {
		open, closer = "[[", "]]"
	}
	body := t[len(open):]
	end := indexUnquoted(body, func(i int) bool { return strings.HasPrefix(body[i:], closer) })
	if end < 0 {
		return "", array, unsafef("unparseable table header %q", t)
	}
	rest := strings.TrimSpace(body[end+len(closer):])
	if rest != "" && rest[0] != '#' {
		return "", array, unsafef("junk after table header %q", t)
	}
	inner := strings.TrimSpace(body[:end])
	if len(inner) >= 2 && (inner[0] == '"' || inner[0] == '\'') && inner[len(inner)-1] == inner[0] {
		inner = inner[1 : len(inner)-1]
	}
	return inner, array, nil
}

// splitKeyValue splits `key = value...` into the trimmed key text and the raw
// text after the first unquoted '='.
func splitKeyValue(t string) (key, rest string, err error) {
	if i := indexUnquoted(t, func(i int) bool { return t[i] == '=' }); i >= 0 {
		return strings.TrimSpace(t[:i]), t[i+1:], nil
	}
	return "", "", unsafef("line is not a key/value pair: %q", t)
}

// indexUnquoted returns the first index i of s, outside any basic ("..." with
// backslash escapes) or literal ('...') string, for which match(i) is true, or
// -1 when there is none.
func indexUnquoted(s string, match func(i int) bool) int {
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == '\\' && quote == '"' {
				i++
			} else if c == quote {
				quote = 0
			}
			continue
		}
		if c == '"' || c == '\'' {
			quote = c
			continue
		}
		if match(i) {
			return i
		}
	}
	return -1
}

func firstKeySegment(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	if key[0] == '"' || key[0] == '\'' {
		if j := strings.IndexByte(key[1:], key[0]); j >= 0 {
			return key[1 : 1+j]
		}
		return key
	}
	if j := strings.IndexByte(key, '.'); j >= 0 {
		key = key[:j]
	}
	return strings.TrimSpace(key)
}

// valueEnd returns the index in s (leading whitespace already skipped) where a
// single-line value ends. Strings, bare scalars and brackets that close on the
// same line are supported; multi-line strings and unclosed arrays/inline
// tables are reported as unsafe.
func valueEnd(s string) (int, error) {
	if s == "" {
		return 0, unsafef("missing value")
	}
	depth := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch c {
		case '"', '\'':
			if strings.HasPrefix(s[i:], strings.Repeat(string(c), 3)) {
				return 0, unsafef("multi-line string")
			}
			closed := false
			for j := i + 1; j < len(s); j++ {
				if s[j] == '\\' && c == '"' {
					j++
					continue
				}
				if s[j] == c {
					i, closed = j, true
					break
				}
			}
			if !closed {
				return 0, unsafef("unterminated string")
			}
			if depth == 0 {
				return i + 1, nil
			}
		case '[', '{':
			depth++
		case ']', '}':
			depth--
			if depth == 0 {
				return i + 1, nil
			}
		case ' ', '\t', '#':
			if depth == 0 {
				return i, nil
			}
			if c == '#' {
				return 0, unsafef("comment inside unclosed array")
			}
		}
	}
	if depth != 0 {
		return 0, unsafef("multi-line array or inline table")
	}
	return len(s), nil
}

func checkValueClosed(rest string) error {
	rest = strings.TrimRight(strings.TrimLeft(rest, " \t"), "\r\n")
	end, err := valueEnd(rest)
	if err != nil {
		return err
	}
	tail := strings.TrimSpace(rest[end:])
	if tail != "" && tail[0] != '#' {
		return unsafef("junk after value")
	}
	return nil
}

// replaceValue swaps the value of a key/value line for newVal, keeping the key
// text, spacing and any trailing comment.
func replaceValue(line, newVal string) (string, error) {
	body, eol := line, ""
	if strings.HasSuffix(body, "\r\n") {
		body, eol = body[:len(body)-2], "\r\n"
	} else if strings.HasSuffix(body, "\n") {
		body, eol = body[:len(body)-1], "\n"
	}
	eq := indexUnquoted(body, func(i int) bool { return body[i] == '=' })
	if eq < 0 {
		return "", unsafef("no '=' in key line")
	}
	i := eq + 1
	for i < len(body) && (body[i] == ' ' || body[i] == '\t') {
		i++
	}
	end, err := valueEnd(body[i:])
	if err != nil {
		return "", err
	}
	return body[:i] + newVal + body[i+end:] + eol, nil
}
