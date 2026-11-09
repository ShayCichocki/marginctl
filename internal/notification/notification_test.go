package notification

import "testing"

func TestErrorOutranksAndRemovesOperationSuccess(t *testing.T) {
	var m Model
	m.Push(Notification{Severity: Success, Summary: "saved", Operation: "save"})
	m.Push(Notification{Severity: Error, Summary: "reload failed", Operation: "save", Persistence: UntilAcknowledged})
	n, ok := m.Current()
	if !ok || n.Summary != "reload failed" || m.Count() != 1 {
		t.Fatalf("current=%+v count=%d", n, m.Count())
	}
	m.Acknowledge()
	if m.Count() != 0 {
		t.Fatal("ack failed")
	}
}
