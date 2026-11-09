package notification

import (
	"testing"
	"time"
)

func covSummaries(m Model) []string {
	var s []string
	for _, n := range m.items {
		s = append(s, n.Summary)
	}
	return s
}

func TestCovEmptyModel(t *testing.T) {
	var m Model
	if _, ok := m.Current(); ok || m.Count() != 0 {
		t.Fatal("empty model should have nothing")
	}
	m.Acknowledge() // must not panic
	if m.Count() != 0 {
		t.Fatal("ack on empty changed count")
	}
}

func TestCovPushStampsCreatedOnlyWhenZero(t *testing.T) {
	var m Model
	fixed := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	before := time.Now()
	m.Push(Notification{Summary: "auto"})
	m.Push(Notification{Summary: "fixed", Created: fixed})
	if !m.items[0].Created.After(before.Add(-time.Second)) {
		t.Fatalf("auto Created = %v", m.items[0].Created)
	}
	if !m.items[1].Created.Equal(fixed) {
		t.Fatalf("explicit Created overwritten: %v", m.items[1].Created)
	}
}

func TestCovPriorityOrderingAcrossSeverities(t *testing.T) {
	order := []Severity{Info, Success, Warning, Partial, Error}
	// Whatever order they arrive in, the highest severity is current.
	perms := [][]int{{0, 1, 2, 3, 4}, {4, 3, 2, 1, 0}, {2, 4, 0, 3, 1}, {1, 0, 3, 2, 4}}
	for _, perm := range perms {
		var m Model
		for _, i := range perm {
			m.Push(Notification{Severity: order[i], Summary: string(rune('a' + i))})
		}
		n, ok := m.Current()
		if !ok || n.Severity != Error {
			t.Fatalf("perm %v current = %+v", perm, n)
		}
	}
	// Strictly descending priority: remove the winner each time.
	want := []Severity{Error, Partial, Warning, Success, Info}
	var m Model
	for _, s := range order {
		m.Push(Notification{Severity: s})
	}
	for _, w := range want {
		n, _ := m.Current()
		if n.Severity != w {
			t.Fatalf("current = %v, want %v", n.Severity, w)
		}
		for i := range m.items {
			if m.items[i].Severity == w {
				m.items = append(m.items[:i], m.items[i+1:]...)
				break
			}
		}
	}
}

func TestCovEqualPriorityKeepsOldest(t *testing.T) {
	var m Model
	m.Push(Notification{Severity: Warning, Summary: "first"})
	m.Push(Notification{Severity: Warning, Summary: "second"})
	if n, _ := m.Current(); n.Summary != "first" {
		t.Fatalf("current = %q, want the oldest of equal priority", n.Summary)
	}
}

func TestCovUnknownSeverityRanksAsInfo(t *testing.T) {
	var m Model
	m.Push(Notification{Severity: Severity(99), Summary: "odd"})
	m.Push(Notification{Severity: Success, Summary: "ok"})
	if n, _ := m.Current(); n.Summary != "ok" {
		t.Fatalf("current = %q", n.Summary)
	}
}

func TestCovAcknowledgeRemovesTheDisplayedNotification(t *testing.T) {
	var m Model
	m.Push(Notification{Severity: Info, Summary: "fyi"})
	m.Push(Notification{Severity: Error, Summary: "boom", Persistence: UntilAcknowledged})
	shown, _ := m.Current()
	m.Acknowledge()
	if now, ok := m.Current(); ok && now.Summary == shown.Summary {
		t.Fatalf("acknowledge left %q displayed", shown.Summary)
	}
}

func TestCovAcknowledgeOrderWithEqualSeverityIsFIFO(t *testing.T) {
	var m Model
	for _, s := range []string{"a", "b", "c"} {
		m.Push(Notification{Severity: Error, Summary: s})
	}
	for _, want := range []string{"a", "b", "c"} {
		n, ok := m.Current()
		if !ok || n.Summary != want {
			t.Fatalf("current = %+v, want %s", n, want)
		}
		m.Acknowledge()
	}
	if m.Count() != 0 {
		t.Fatal("not drained")
	}
}

func TestCovOperationSuccessReplacement(t *testing.T) {
	cases := []struct {
		name      string
		incoming  Notification
		wantAfter []string
	}{
		{"error same op replaces success", Notification{Severity: Error, Summary: "E", Operation: "save"}, []string{"other-op", "info", "E"}},
		{"partial same op replaces success", Notification{Severity: Partial, Summary: "P", Operation: "save"}, []string{"other-op", "info", "P"}},
		{"warning does not replace", Notification{Severity: Warning, Summary: "W", Operation: "save"}, []string{"ok", "other-op", "info", "W"}},
		{"error with different op keeps success", Notification{Severity: Error, Summary: "E", Operation: "export2"}, []string{"ok", "other-op", "info", "E"}},
		{"error with no op keeps success", Notification{Severity: Error, Summary: "E"}, []string{"ok", "other-op", "info", "E"}},
		{"success does not replace success", Notification{Severity: Success, Summary: "S2", Operation: "save"}, []string{"ok", "other-op", "info", "S2"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var m Model
			m.Push(Notification{Severity: Success, Summary: "ok", Operation: "save"})
			m.Push(Notification{Severity: Success, Summary: "other-op", Operation: "export"})
			m.Push(Notification{Severity: Info, Summary: "info", Operation: "save"})
			m.Push(tc.incoming)
			got := covSummaries(m)
			if len(got) != len(tc.wantAfter) {
				t.Fatalf("items = %v, want %v", got, tc.wantAfter)
			}
			for i := range got {
				if got[i] != tc.wantAfter[i] {
					t.Fatalf("items = %v, want %v", got, tc.wantAfter)
				}
			}
		})
	}
}

func TestCovReplacementRemovesEverySuccessOfThatOperation(t *testing.T) {
	var m Model
	for i := 0; i < 3; i++ {
		m.Push(Notification{Severity: Success, Summary: "ok", Operation: "sync"})
	}
	m.Push(Notification{Severity: Error, Summary: "fail", Operation: "sync"})
	if m.Count() != 1 {
		t.Fatalf("items = %v", covSummaries(m))
	}
}
