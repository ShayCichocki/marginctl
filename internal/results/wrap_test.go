package results

import (
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"

	"github.com/ShayCichocki/marginctl/internal/command"
)

// TestErrorWrapsToMultipleLines guards the reported bug: a long provider error
// (e.g. a ChatGPT 400 body) used to render as one line truncated off-screen.
func TestErrorWrapsToMultipleLines(t *testing.T) {
	m := New()
	m.SetSize(30, 10)
	m.Show(command.Result{
		Type:    command.ResultError,
		Content: `API returned status 400: {"detail":"The request body is not valid for this endpoint."}`,
	})

	if len(m.lines) < 2 {
		t.Fatalf("error rendered as %d line(s), want it wrapped across several: %q", len(m.lines), m.lines)
	}
	for _, line := range m.lines {
		if w := runewidth.StringWidth(line); w > 30 {
			t.Fatalf("wrapped line %q width %d exceeds pane width 30", line, w)
		}
	}
	if !strings.Contains(strings.Join(m.lines, ""), "not valid for this endpoint") {
		t.Fatalf("wrapped error lost content: %q", m.lines)
	}
}

// TestDiffWrapsAndKeepsMarker guards that a long proposed/original line wraps
// within the pane and every wrapped segment keeps its +/- marker (View colors on
// that prefix, so a bare continuation line would render uncolored and overflow).
func TestDiffWrapsAndKeepsMarker(t *testing.T) {
	const width = 40
	m := New()
	m.SetSize(width, 12)
	m.Show(command.Result{
		Type:         command.ResultDiff,
		OriginalText: "We woowie woo.",
		ProposedText: "We woowie woo, the sound of it loose and bright across the water",
	})

	var proposed []string
	for _, line := range m.lines {
		// Diff content (the +/- lines) must fit the pane; static chrome like the
		// footer hint is left for View to truncate.
		if strings.HasPrefix(line, "+ ") || strings.HasPrefix(line, "- ") {
			if w := runewidth.StringWidth(line); w > width {
				t.Fatalf("diff line %q width %d exceeds pane width %d", line, w, width)
			}
		}
		if strings.HasPrefix(line, "+ ") {
			proposed = append(proposed, line)
		}
	}
	if len(proposed) < 2 {
		t.Fatalf("long proposed line did not wrap: %q", proposed)
	}
	// Every wrapped proposed segment must carry the marker and reconstruct the
	// marked-up text (added words grouped in a single {+...+} run).
	var joined strings.Builder
	for _, line := range proposed {
		joined.WriteString(strings.TrimPrefix(line, "+ "))
	}
	if joined.String() != "We woowie {+woo, the sound of it loose and bright across the water+}" {
		t.Fatalf("wrapped proposed text = %q, want marked text reconstructed", joined.String())
	}
}

// TestSetSizeRewrapsOnWidthChange covers the layout ordering where the pane is
// sized after Show (updateLayout runs post-Show) and on terminal resize.
func TestSetSizeRewrapsOnWidthChange(t *testing.T) {
	m := New()
	// Show while width is still stale/zero (as on the first result), then let a
	// later SetSize deliver the real pane width.
	m.Show(command.Result{
		Type:    command.ResultText,
		Content: "one two three four five six seven eight nine ten eleven twelve",
	})
	m.SetSize(12, 8)

	if len(m.lines) < 2 {
		t.Fatalf("content not rewrapped after SetSize: %q", m.lines)
	}
	for _, line := range m.lines {
		if w := runewidth.StringWidth(line); w > 12 {
			t.Fatalf("line %q width %d exceeds 12", line, w)
		}
	}
}
