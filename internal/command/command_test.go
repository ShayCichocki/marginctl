package command

import "testing"

func TestInitStateRegisteredAndSafeGuards(t *testing.T) {
	r := NewRegistry()
	r.RegisterDefaults()
	cmd := r.Get("state.init")
	if cmd == nil {
		t.Fatal("state.init not registered")
	}
	if got := cmd.Execute(&Context{}); got.Type != ResultError {
		t.Fatalf("outside project result = %v", got.Type)
	}
	got := cmd.Execute(&Context{IsProject: true})
	if _, ok := got.Intent.(InitializeStateIntent); !ok {
		t.Fatalf("execute did not return initialize intent: %#v", got)
	}
}
