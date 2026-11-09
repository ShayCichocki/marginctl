package llmcmd

import (
	"github.com/ShayCichocki/marginctl/internal/command"
	"github.com/ShayCichocki/marginctl/internal/fsutil"
	"github.com/ShayCichocki/marginctl/internal/llm"
)

// Operation identifies the immutable context in which asynchronous output was
// requested. The app creates it at dispatch and validates it again before both
// display and acceptance.
type Operation struct {
	RequestID         int
	Kind              string
	ProjectRoot       string
	SourceFile        string
	BufferRevision    int
	Target            string
	TargetFingerprint fsutil.Fingerprint
	Provider          string
	Model             string
}

// ResponseMsg is sent when an LLM API call completes. File and BufferGen record
// the editor buffer identity the request was dispatched against, so a splice
// (diff) result produced for a file/buffer that has since changed can be dropped
// instead of corrupting the current buffer.
type ResponseMsg struct {
	Operation Operation
	CommandID string
	Result    command.Result
	File      string
	BufferGen int
	// Usage carries token accounting for the call (zero when the provider didn't
	// report it) so the app can surface tokens/cost.
	Usage llm.Usage
	// ReqID identifies the dispatch this response belongs to. The app bumps a
	// monotonic counter per request and on cancellation; a response whose ReqID
	// no longer matches the app's counter is stale (its request was cancelled or
	// superseded) and is dropped instead of being applied.
	ReqID int
}

// StreamStartMsg is returned by a streaming command runner to hand the app the
// event channel for an in-flight streaming completion. The app subscribes to Ch,
// rendering deltas incrementally. Prefix seeds the rendered text (e.g. continuity
// budget notes) before the first token. ReqID is stamped by app.startLLM.
type StreamStartMsg struct {
	Operation Operation
	CommandID string
	Ch        <-chan llm.StreamEvent
	Prefix    string
	ReqID     int
}
