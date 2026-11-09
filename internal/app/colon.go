package app

import (
	"sort"
	"strings"
)

// builtinColonVerbs are the verbs of the builtinCommands table (commands.go),
// derived from it. It is the completion source for the builtins.
var builtinColonVerbs = func() []string {
	var verbs []string
	for _, bc := range builtinCommands {
		verbs = append(verbs, bc.verbs...)
	}
	return verbs
}()

// colonAliases maps a PascalCase ":" command (vim user-command style) to the
// registry command ID it dispatches to via executeRegisteredCommandCmd. These
// surface registry commands at the ":" prompt so they're typeable and
// tab-completable. Selection commands (state.add-*, the selection-edit llm.*)
// are included too: ":" works from Visual mode (keeping the selection), so they
// run on the selection; without one they surface a notice.
var colonAliases = map[string]string{
	"NewProject":   "project.new",
	"OpenProject":  "project.open",
	"CloseFile":    "file.close",
	"NewChapter":   "chapter.new",
	"ShowChapters": "chapter.list",
	"WordCount":    "manuscript.wordcount",
	"Compile":      "manuscript.compile",
	"ShowOutline":  "outline.show",
	"ShowState":    "state.show",
	"InitState":    "state.init",
	"Continuity":   "llm.continuity",
	"Summarize":    "llm.summarize",
	"VoiceGuide":   "llm.voice-guide",
	"ExtractState": "llm.extract-state",
	// Selection commands — reachable via ":" from Visual mode (which keeps the
	// selection). They no-op with a notice if run without one.
	"Tighten":      "llm.tighten",
	"ShowDontTell": "llm.show-dont-tell",
	"Dialogue":     "llm.dialogue",
	"Expand":       "llm.expand",
	"RewriteVoice": "llm.rewrite-voice",
	"AddCharacter": "state.add-character",
	"AddLocation":  "state.add-location",
	"AddTimeline":  "state.add-timeline",
}

// hiddenColonAliases are back-compat lowercase aliases that keep working but are
// omitted from completion/help (the PascalCase names are canonical).
var hiddenColonAliases = map[string]string{
	"words":   "manuscript.wordcount",
	"compile": "manuscript.compile",
}

// allColonVerbs returns the sorted, de-duped set of completable ":" verbs
// (builtins + PascalCase aliases). Hidden back-compat aliases are excluded.
func allColonVerbs() []string {
	seen := make(map[string]bool)
	var out []string
	add := func(v string) {
		if !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	for _, v := range builtinColonVerbs {
		add(v)
	}
	for v := range colonAliases {
		add(v)
	}
	sort.Strings(out)
	return out
}

// matchColonPrefix returns the completable verbs starting with prefix
// (case-sensitive, as vim user commands require their leading capital), sorted.
// An empty prefix returns every verb.
func matchColonPrefix(prefix string) []string {
	var out []string
	for _, v := range allColonVerbs() {
		if strings.HasPrefix(v, prefix) {
			out = append(out, v)
		}
	}
	return out
}
