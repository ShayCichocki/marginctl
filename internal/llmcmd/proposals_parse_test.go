package llmcmd

import (
	"strings"
	"testing"
)

// Names are entry titles, never paths, so path-looking names are kept verbatim
// (the app slugs them); only malformed proposals and unsafe explicit targets
// are dropped, and each drop is counted.
func TestParseStateProposalsDropsOnlyMalformed(t *testing.T) {
	in := `[
	 {"category":"characters","name":"../../chapters/001_x","summary":"s"},
	 {"category":"characters","name":"a/b","summary":"s"},
	 {"category":"characters","name":"..","summary":"s"},
	 {"category":"characters","name":"nul\u0000x","summary":"s"},
	 {"category":"characters","name":"line\nbreak","summary":"s"},
	 {"category":"characters","name":"x","action":"update","target":"../chapters/001_x.md","proposed":"p"},
	 {"category":"characters","name":"x","action":"update","target":"characters/../../chapters/a.md","proposed":"p"},
	 {"category":"characters","name":"x","action":"update","target":"/etc/passwd","proposed":"p"},
	 {"category":"characters","name":"x","action":"update","target":"characters\\alice.md","proposed":"p"},
	 {"category":"characters","name":"x","action":"update","target":"a\u0000.md","proposed":"p"},
	 {"category":"characters","name":"x","action":"frobnicate"},
	 {"category":"characters","name":"Alice","summary":"ok"},
	 {"category":"characters","name":"Alice","action":"update","target":"characters/alice.md","proposed":"p"},
	 {"category":"timeline","name":"t","action":"append","proposed":"p"}
	]`
	got, skipped, err := parseStateProposals(in)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 6 || skipped != 8 {
		t.Fatalf("kept %d skipped %d, want 6 and 8: %+v", len(got), skipped, got)
	}
	if got[0].Name != "../../chapters/001_x" || got[3].Name != "Alice" || got[4].Target != "characters/alice.md" || got[5].Category != "timeline" {
		t.Fatalf("unexpected survivors: %+v", got)
	}
}

func TestParseStateProposalsKeepsRealisticNames(t *testing.T) {
	names := []string{"Martin Luther King Jr.", "Washington, D.C.", "Dr. Who?", `"Doc" Holliday`, "Station 7: Outpost", "Con", "O'Brien", "Wait...", "Mr.. X"}
	var parts []string
	for _, n := range names {
		b := strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(n)
		parts = append(parts, `{"category":"characters","name":"`+b+`","summary":"s"}`)
	}
	got, skipped, err := parseStateProposals("[" + strings.Join(parts, ",") + "]")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(names) || skipped != 0 {
		t.Fatalf("kept %d skipped %d, want %d and 0", len(got), skipped, len(names))
	}
	for i, n := range names {
		if got[i].Name != n {
			t.Errorf("name %d = %q, want %q", i, got[i].Name, n)
		}
	}
}
