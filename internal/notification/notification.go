package notification

import "time"

type Severity int

const (
	Info Severity = iota
	Success
	Warning
	Error
	Partial
)

type Persistence int

const (
	Transient Persistence = iota
	UntilAcknowledged
)

type Action struct{ Label, Command string }
type Notification struct {
	Severity                    Severity
	Summary, Details, Operation string
	Persistence                 Persistence
	Actions                     []Action
	Created                     time.Time
}
type Model struct{ items []Notification }

func (m *Model) Push(n Notification) {
	if n.Created.IsZero() {
		n.Created = time.Now()
	}
	if n.Operation != "" && (n.Severity == Error || n.Severity == Partial) {
		out := m.items[:0]
		for _, old := range m.items {
			if old.Operation != n.Operation || old.Severity != Success {
				out = append(out, old)
			}
		}
		m.items = out
	}
	m.items = append(m.items, n)
}

// Acknowledge removes the notification Current reports.
func (m *Model) Acknowledge() {
	if i := m.currentIndex(); i >= 0 {
		m.items = append(m.items[:i], m.items[i+1:]...)
	}
}

// currentIndex returns the index of the highest-priority notification (oldest
// wins ties), or -1 when empty.
func (m Model) currentIndex() int {
	if len(m.items) == 0 {
		return -1
	}
	best := 0
	for i := range m.items {
		if priority(m.items[i].Severity) > priority(m.items[best].Severity) {
			best = i
		}
	}
	return best
}
func (m Model) Current() (Notification, bool) {
	i := m.currentIndex()
	if i < 0 {
		return Notification{}, false
	}
	return m.items[i], true
}
func (m Model) Count() int { return len(m.items) }
func priority(s Severity) int {
	if s == Error {
		return 5
	}
	if s == Partial {
		return 4
	}
	if s == Warning {
		return 3
	}
	if s == Success {
		return 2
	}
	return 1
}
