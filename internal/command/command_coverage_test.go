package command

import (
	"strings"
	"testing"
)

func defaultRegistry() *Registry {
	r := NewRegistry()
	r.RegisterDefaults()
	return r
}

func TestRegistryDefaultsUniqueAndComplete(t *testing.T) {
	r := defaultRegistry()
	seen := map[string]bool{}
	for _, c := range r.All() {
		if c.ID == "" || c.Name == "" || c.Execute == nil || c.Category == "" {
			t.Errorf("incomplete command %+v", c.ID)
		}
		if seen[c.ID] {
			t.Errorf("duplicate id %s", c.ID)
		}
		seen[c.ID] = true
		if r.Get(c.ID) == nil {
			t.Errorf("Get(%s) nil", c.ID)
		}
	}
	if r.Get("nope") != nil {
		t.Error("Get of unknown id must be nil")
	}
	if len(seen) < 10 {
		t.Errorf("suspiciously few commands: %d", len(seen))
	}
}

func TestRegistryExecuteNeverPanicsAcrossContexts(t *testing.T) {
	r := defaultRegistry()
	ctxs := map[string]Context{
		"empty":   {},
		"project": {IsProject: true},
		"full":    {IsProject: true, HasState: true, HasSelection: true, SelectedText: "x", CurrentFile: "a.md", EditorDirty: true},
	}
	for _, c := range r.All() {
		for name, ctx := range ctxs {
			ctx := ctx
			res := c.Execute(&ctx)
			if res.Type == ResultError && res.Content == "" {
				t.Errorf("%s/%s: error without message", c.ID, name)
			}
			if res.Type != ResultError && res.Intent == nil {
				t.Errorf("%s/%s: neither error nor intent: %+v", c.ID, name, res)
			}
		}
	}
}

func TestProjectOnlyCommandsGuardIsProject(t *testing.T) {
	r := defaultRegistry()
	for _, id := range []string{"chapter.new", "chapter.list", "manuscript.wordcount", "manuscript.compile", "state.init", "state.show"} {
		res := r.Get(id).Execute(&Context{HasState: true})
		if res.Type != ResultError || !strings.Contains(res.Content, "project") {
			t.Errorf("%s outside project = %+v", id, res)
		}
	}
	if res := r.Get("state.show").Execute(&Context{IsProject: true}); res.Type != ResultError {
		t.Errorf("state.show without state = %+v", res)
	}
	if res := r.Get("state.show").Execute(&Context{IsProject: true, HasState: true}); res.Intent != (PaneIntent{Mode: "state"}) {
		t.Errorf("state.show = %+v", res)
	}
}

func TestSelectionCommandsRequireNonEmptySelection(t *testing.T) {
	r := defaultRegistry()
	for _, id := range []string{"state.add-character", "state.add-location", "state.add-timeline"} {
		c := r.Get(id)
		for _, ctx := range []Context{
			{HasState: true},
			{HasState: true, HasSelection: true, SelectedText: ""},
			{HasState: true, HasSelection: true, SelectedText: "  \n\t"},
			{HasSelection: true, SelectedText: "Ann"},
		} {
			ctx := ctx
			if res := c.Execute(&ctx); res.Type != ResultError || res.Intent != nil {
				t.Errorf("%s %+v => %+v", id, ctx, res)
			}
		}
		if res := c.Execute(&Context{HasState: true, HasSelection: true, SelectedText: "Ann"}); res.Intent == nil {
			t.Errorf("%s valid selection gave no intent", id)
		}
	}
}

func TestDirtyGuards(t *testing.T) {
	r := defaultRegistry()
	for _, id := range []string{"project.new", "project.open", "file.close"} {
		if res := r.Get(id).Execute(&Context{EditorDirty: true}); res.Type != ResultError {
			t.Errorf("%s dirty = %+v", id, res)
		}
		if res := r.Get(id).Execute(&Context{}); res.Intent == nil {
			t.Errorf("%s clean = %+v", id, res)
		}
	}
	if res := r.Get("chapter.new").Execute(&Context{IsProject: true, EditorDirty: true}); res.Type != ResultError {
		t.Errorf("chapter.new dirty = %+v", res)
	}
	if res := r.Get("file.save").Execute(&Context{}); res.Intent != (PromptIntent{Kind: "save"}) {
		t.Errorf("unnamed save = %+v", res)
	}
	res := r.Get("file.save").Execute(&Context{CurrentFile: "a.md", EditorContent: "hi"})
	if res.Intent != (SaveIntent{Path: "a.md", Content: "hi"}) {
		t.Errorf("save = %+v", res)
	}
}

func TestFilterAndFilterByMode(t *testing.T) {
	r := defaultRegistry()
	all := r.All()
	if got := r.Filter(""); len(got) != len(all) {
		t.Errorf("Filter empty = %d", len(got))
	}
	if got := r.Filter("SAVE FILE"); len(got) != 1 || got[0].ID != "file.save" {
		t.Errorf("Filter case-insensitive = %+v", got)
	}
	if got := r.Filter("zzzzzz"); len(got) != 0 {
		t.Errorf("Filter nomatch = %d", len(got))
	}
	vis := r.FilterByMode("", ModeVisual)
	hasHelp, hasAdd := false, false
	for _, c := range vis {
		if !commandAvailableInMode(c, ModeVisual) {
			t.Errorf("%s not visual", c.ID)
		}
		if c.ID == "file.save" {
			t.Error("file.save leaked into visual mode")
		}
		hasHelp = hasHelp || c.ID == "help.keys"
		hasAdd = hasAdd || c.ID == "state.add-character"
	}
	if !hasHelp || !hasAdd {
		t.Error("visual mode should include mode-less help and visual add commands")
	}
	for _, c := range r.FilterByMode("character", ModeNormal) {
		if c.ID == "state.add-character" {
			t.Error("visual-only command in normal mode")
		}
	}
	if !commandAvailableInMode(Command{Modes: []int{ModeAny}}, 7) {
		t.Error("ModeAny must match every mode")
	}
	if commandAvailableInMode(Command{Modes: []int{ModeNormal}}, ModeVisual) {
		t.Error("normal-only must not match visual")
	}
}

func TestRegisterGetValidAfterGrowth(t *testing.T) {
	r := NewRegistry()
	for i := 0; i < 50; i++ {
		r.Register(Command{ID: string(rune('a'+i%26)) + string(rune('A'+i/26))})
	}
	if r.Get("aA") == nil || r.Get("bB") == nil {
		t.Error("Get lost entries after growth")
	}
}
