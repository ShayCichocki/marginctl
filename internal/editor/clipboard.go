package editor

import (
	"sync"

	"github.com/atotto/clipboard"
)

// osClipboardWrite and osClipboardRead bridge the editor's register to the OS
// clipboard. They are package variables so tests can stub them out and stay
// hermetic (a headless CI box has no clipboard). Both are best-effort: a
// failure never propagates, it just falls back to the in-app register.
var (
	osClipboardWrite = func(s string) {
		if clipboard.Unsupported {
			return
		}
		_ = clipboard.WriteAll(s)
	}

	osClipboardRead = func() (string, bool) {
		if clipboard.Unsupported {
			return "", false
		}
		s, err := clipboard.ReadAll()
		if err != nil {
			return "", false
		}
		return s, true
	}
)

// memClip is the process-local clipboard installed by UseMemoryClipboard.
var memClip struct {
	mu  sync.Mutex
	buf string
}

// UseMemoryClipboard replaces the OS clipboard bridge with a process-local
// buffer. TEST-ONLY: it must be called solely from a TestMain (this package's
// and any package whose tests drive the editor, e.g. internal/app) so `go test`
// never reads or overwrites the developer's real clipboard. Production code must
// never call it, and it is not safe to call concurrently with editor use.
func UseMemoryClipboard() {
	osClipboardWrite = func(s string) {
		memClip.mu.Lock()
		defer memClip.mu.Unlock()
		memClip.buf = s
	}
	osClipboardRead = func() (string, bool) {
		memClip.mu.Lock()
		defer memClip.mu.Unlock()
		return memClip.buf, memClip.buf != ""
	}
}

// resetMemoryClipboard empties the in-memory clipboard so one test or fuzz
// iteration's yank cannot become the next one's paste.
func resetMemoryClipboard() {
	memClip.mu.Lock()
	defer memClip.mu.Unlock()
	memClip.buf = ""
}
