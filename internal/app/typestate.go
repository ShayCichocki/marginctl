package app

// promptKind is the single root input mode, held in Model.promptState and
// changed only through setPrompt. promptConnect is never stored: it is derived
// from connect state by activePrompt.
type promptKind uint8

const (
	promptNone promptKind = iota
	promptCommand
	promptSearch
	promptChapter
	promptStateName
	promptCategory
	promptPath
	promptSetup
	promptConnect
)

func (m *Model) setPrompt(kind promptKind) {
	m.promptState = kind
	if kind != promptCommand {
		m.commandFromResults = false
	}
}

// activePrompt reports which prompt owns keyboard input. It is a pure getter.
// The connect picker is derived from connect state rather than stored, so it
// can never go stale when the connect flow ends.
func (m *Model) activePrompt() promptKind {
	if m.promptState != promptNone && m.promptState != promptConnect {
		return m.promptState
	}
	if m.connect.prompting() {
		return promptConnect
	}
	return promptNone
}
