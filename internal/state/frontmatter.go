package state

import "strings"

// ParseFrontMatter splits a leading YAML-ish front-matter block — delimited by a
// line of exactly "---" at the top and a matching "---" below — into flat
// key→value fields plus the remaining body. Only simple "key: value" pairs are
// recognized (no nesting or lists). A file with no well-formed front-matter
// returns nil fields and the whole content as the body, so callers can treat
// structured and unstructured state files uniformly.
func ParseFrontMatter(content string) (map[string]string, string) {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return nil, content
	}

	fields := make(map[string]string)
	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			end = i
			break
		}
		if idx := strings.IndexByte(lines[i], ':'); idx > 0 {
			key := strings.TrimSpace(lines[i][:idx])
			val := strings.TrimSpace(lines[i][idx+1:])
			if key != "" {
				fields[key] = val
			}
		}
	}
	if end < 0 { // no closing delimiter → not front-matter
		return nil, content
	}

	body := strings.TrimLeft(strings.Join(lines[end+1:], "\n"), "\n")
	return fields, body
}

// characterTemplate scaffolds a structured character entry: front-matter with the
// fields continuity/auto-linking reason over, plus a prose body for freeform notes.
func characterTemplate(name string) string {
	d := name
	return "---\n" +
		"name: " + d + "\n" +
		"aliases: \n" +
		"role: \n" +
		"age: \n" +
		"appearance: \n" +
		"first_appears: \n" +
		"status: alive\n" +
		"---\n\n# " + d + "\n\n"
}

// locationTemplate scaffolds a structured location entry.
func locationTemplate(name string) string {
	d := name
	return "---\n" +
		"name: " + d + "\n" +
		"region: \n" +
		"kind: \n" +
		"first_appears: \n" +
		"---\n\n# " + d + "\n\n"
}

// Fields reads a state file and returns its parsed front-matter fields (nil when
// the file has none), so LLM commands can consume structured metadata.
func (m *Manager) Fields(sf *StateFile) (map[string]string, error) {
	content, err := m.Read(sf)
	if err != nil {
		return nil, err
	}
	fields, _ := ParseFrontMatter(content)
	return fields, nil
}
