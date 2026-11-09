package app

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"
	"testing"
)

// isolateAppEnv redirects every location the app may touch (config, session,
// auth store, swap/unnamed-buffer data) plus all LLM env vars into temp dirs.
func isolateAppEnv(t *testing.T) {
	t.Helper()
	isolateConfigEnv(t)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	t.Setenv("AppData", t.TempDir())
}

// tableVerbs returns every verb in the builtinCommands table, failing on
// duplicate verbs or rows without a handler.
func tableVerbs(t *testing.T) map[string]bool {
	t.Helper()
	verbs := map[string]bool{}
	for _, bc := range builtinCommands {
		if bc.run == nil || len(bc.verbs) == 0 {
			t.Fatalf("builtinCommands row %v has no handler or no verbs", bc.verbs)
		}
		for _, v := range bc.verbs {
			if verbs[v] {
				t.Errorf("builtinCommands lists %q more than once", v)
			}
			verbs[v] = true
		}
	}
	return verbs
}

// The table is the single source: every handler verb is completable (no
// builtin is deliberately hidden; hidden aliases live in hiddenColonAliases)
// and every completable builtin verb dispatches to a handler.
func TestColonParityBuiltinVerbsMatchCommandTable(t *testing.T) {
	cases := tableVerbs(t)
	listed := map[string]bool{}
	for _, v := range builtinColonVerbs {
		if listed[v] {
			t.Errorf("builtinColonVerbs lists %q more than once", v)
		}
		listed[v] = true
		if _, ok := builtinCommandByVerb[v]; !ok {
			t.Errorf("%q is completable but has no handler (would be 'unknown command')", v)
		}
	}
	completable := map[string]bool{}
	for _, v := range allColonVerbs() {
		completable[v] = true
	}
	var missing []string
	for v := range cases {
		if !listed[v] || !completable[v] {
			missing = append(missing, v)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("handled by the command table but not completable: %v", missing)
	}
	if len(listed) != len(cases) {
		t.Errorf("builtinColonVerbs has %d verbs, table has %d", len(listed), len(cases))
	}
}

func TestColonParityAliasesResolveToRegisteredCommands(t *testing.T) {
	isolateAppEnv(t)
	m := New()
	for verb, id := range colonAliases {
		if m.registry.Get(id) == nil {
			t.Errorf("colonAliases[%q] = %q is not a registered command ID", verb, id)
		}
	}
	for verb, id := range hiddenColonAliases {
		if m.registry.Get(id) == nil {
			t.Errorf("hiddenColonAliases[%q] = %q is not a registered command ID", verb, id)
		}
	}
}

func TestColonParityAliasesDoNotShadowBuiltinsOrEachOther(t *testing.T) {
	cases := tableVerbs(t)
	for verb := range colonAliases {
		if cases[verb] {
			t.Errorf("colonAliases[%q] is shadowed by a builtin command", verb)
		}
		if _, dup := hiddenColonAliases[verb]; dup {
			t.Errorf("%q is both a canonical and a hidden alias", verb)
		}
	}
	for verb := range hiddenColonAliases {
		if cases[verb] {
			t.Errorf("hiddenColonAliases[%q] is shadowed by a builtin command", verb)
		}
		for _, v := range allColonVerbs() {
			if v == verb {
				t.Errorf("hidden alias %q leaked into completion", verb)
			}
		}
	}
}

// Every builtin verb that is safe to run for real must be recognized (never the
// "unknown command" notice). Quit verbs are skipped; everything else runs on a
// fresh model with HOME/XDG_* and all LLM env redirected into temp dirs.
func TestColonParitySafeBuiltinVerbsAreRecognized(t *testing.T) {
	skip := map[string]string{
		"q": "quits", "q!": "quits", "wq": "quits", "wq!": "quits",
	}
	for _, verb := range builtinColonVerbs {
		verb := verb
		t.Run(verb, func(t *testing.T) {
			if why, ok := skip[verb]; ok {
				t.Skipf("not executed: %s", why)
			}
			isolateAppEnv(t)
			m, _, _ := openTestModel(t)
			m.notice, m.noticeIsErr = "", false
			_ = m.executeCommand(verb)
			if strings.Contains(strings.ToLower(m.notice), "unknown command") {
				t.Fatalf(":%s produced %q", verb, m.notice)
			}
		})
	}
}

func TestColonParityUnknownVerbReportsNotice(t *testing.T) {
	isolateAppEnv(t)
	m, _, _ := openTestModel(t)
	if cmd := m.executeCommand("definitelynotacommand"); cmd != nil {
		t.Fatal("unknown verb returned a command")
	}
	if !m.noticeIsErr || !strings.Contains(m.notice, "unknown command") {
		t.Fatalf("notice = %q err=%v", m.notice, m.noticeIsErr)
	}
}

func TestColonParityHiddenAliasesDispatchWithoutUnknownNotice(t *testing.T) {
	isolateAppEnv(t)
	for verb := range hiddenColonAliases {
		m, _, _ := openTestModel(t)
		m.notice = ""
		_ = m.executeCommand(verb)
		if strings.Contains(m.notice, "unknown command") {
			t.Errorf(":%s -> %q", verb, m.notice)
		}
	}
}

// executeCommand must reach every builtin verb only through the table: a verb
// special-cased in its body (e.g. `if verb == "w"`) would bypass the parity
// checks above. Assert no builtin verb appears as a string literal there.
func TestColonParityNoVerbSpecialCasedInExecuteCommand(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "commands.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	verbs := tableVerbs(t)
	found := false
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "executeCommand" {
			continue
		}
		found = true
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				if s := strings.Trim(lit.Value, "\"`"); verbs[s] {
					t.Errorf("executeCommand special-cases builtin verb %q outside the table", s)
				}
			}
			return true
		})
	}
	if !found {
		t.Fatal("executeCommand not found in commands.go")
	}
}
