package results

import (
	"reflect"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/ShayCichocki/marginctl/internal/command"
	"github.com/ShayCichocki/marginctl/internal/llmcmd"
)

var resultANSI = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func TestIncrementalStreamWrappingMatchesFullBuild(t *testing.T) {
	const content = "alpha beta gamma delta\nsecond line wraps too\nfinal"
	full := New()
	full.SetSize(12, 10)
	full.StreamText(content)

	incremental := New()
	incremental.SetSize(12, 10)
	incremental.StreamText("alpha beta")
	for _, chunk := range []string{" gamma", " delta\nsec", "ond line wraps too\n", "final"} {
		incremental.AppendStreamText(chunk)
	}
	if !reflect.DeepEqual(incremental.lines, full.lines) {
		t.Fatalf("incremental lines = %#v, full = %#v", incremental.lines, full.lines)
	}
}

func TestViewSanitizesLLMResultsAndErrors(t *testing.T) {
	for _, result := range []command.Result{
		{Type: command.ResultText, Content: "answer\x1b]52;c;c2VjcmV0\x07"},
		{Type: command.ResultError, Content: "provider\rerror\x1b[2J"},
		{Type: command.ResultList, Items: []string{"note: unsafe\x1b]8;;https://evil\x07link\x1b]8;;\x07"}},
	} {
		m := New()
		m.SetSize(80, 5)
		m.Show(result)
		if view := m.View(); strings.ContainsAny(view, "\x1b\x07\r") {
			t.Fatalf("view retained terminal controls: %q", view)
		}
		if m.result.Content != result.Content {
			t.Fatalf("rendering changed result content")
		}
	}
}

func TestStreamFollowOnlyAtBottomAndResume(t *testing.T) {
	m := New()
	m.SetSize(20, 4)
	m.StreamText("1\n2\n3\n4\n5\n6")
	m.Focus()
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("k")})
	away := m.scrollPos
	m.StreamText("1\n2\n3\n4\n5\n6\n7")
	if m.scrollPos != away || !m.newOutput {
		t.Fatalf("stream forced follow: pos=%d marker=%v", m.scrollPos, m.newOutput)
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("G")})
	if m.newOutput {
		t.Fatal("G must clear new-output marker")
	}
	m.StreamText("1\n2\n3\n4\n5\n6\n7\n8")
	if !m.atBottom() {
		t.Fatal("stream should follow after G resumes it")
	}
}

func TestProjectSearchRowWrapKeepsIdentityVisible(t *testing.T) {
	m := New()
	m.SetSize(24, 8)
	m.Show(command.Result{Type: command.ResultList, Items: []string{"chapter.md:42  " + strings.Repeat("word ", 12)}, SearchTargets: []command.SearchTarget{{Path: "/p/chapter.md", Line: 42}}})
	if len(m.lines) < 2 {
		t.Fatalf("long row did not wrap: %#v", m.lines)
	}
	if !strings.HasPrefix(m.lines[0], "chapter.md:42  ") {
		t.Fatalf("first row lost identity: %q", m.lines[0])
	}
	if m.displayTargets[0].Path == "" || m.displayTargets[1].Path != "" {
		t.Fatalf("targets must attach only to identity row: %#v", m.displayTargets)
	}
}

// TestViewSmallWidthsNoPanic exercises the byte-slice truncation at narrow and
// non-positive widths, which used to slice with a negative bound.
func TestViewSmallWidthsNoPanic(t *testing.T) {
	m := New()
	m.Show(command.Result{
		Type:    command.ResultText,
		Content: "a long line of text that certainly exceeds a narrow pane width",
	})

	for w := -2; w <= 10; w++ {
		for h := 0; h <= 3; h++ {
			m.SetSize(w, h)
			_ = m.View() // must not panic
		}
	}
}

func TestConfirmationMessagesOwnActionID(t *testing.T) {
	m := New()
	m.SetSize(80, 10)
	m.ShowAction(command.Result{Type: command.ResultConfirm, Content: "Apply?"}, 42, llmcmd.Operation{})
	next, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}})
	_ = next
	msg := cmd().(ConfirmAcceptedMsg)
	if msg.ActionID != 42 {
		t.Fatalf("action ID = %d, want 42", msg.ActionID)
	}
}

func TestViewPaintsEveryBodyCellWithoutTextOnlyBackgrounds(t *testing.T) {
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(termenv.Ascii) })

	const width, height = 14, 7
	for _, tc := range []struct {
		name string
		show func(*Model)
	}{
		{name: "completed", show: func(m *Model) { m.Show(command.Result{Type: command.ResultText, Content: "alpha beta gamma\n\nlast"}) }},
		{name: "streamed", show: func(m *Model) {
			m.StreamText("alpha")
			m.AppendStreamText(" beta gamma\n\nlast")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := New()
			m.SetSize(width, height)
			tc.show(&m)
			view := m.View()
			lines := strings.Split(view, "\n")
			if len(lines) != height {
				t.Fatalf("height = %d, want %d\n%q", len(lines), height, view)
			}
			for i, line := range lines {
				if got := lipgloss.Width(line); got != width {
					t.Fatalf("row %d width = %d, want %d: %q", i, got, width, line)
				}
				if !strings.Contains(line, "48;2;31;40;55m") {
					t.Fatalf("row %d has an unpainted canvas: %q", i, line)
				}
			}
			for i, line := range lines[1 : len(lines)-1] {
				plain := resultANSI.ReplaceAllString(line, "")
				if len([]rune(plain)) != width {
					t.Fatalf("body row %d has trailing unpainted cells: %q", i, line)
				}
				if strings.Contains(line, "48;2;124;58;237m") {
					t.Fatalf("body row %d applies selection background to report text: %q", i, line)
				}
			}
			wantFirstBody := "\x1b[38;2;249;250;251;48;2;31;40;55malpha beta    \x1b[0m"
			if lines[1] != wantFirstBody {
				t.Fatalf("first body row ANSI snapshot:\n got %q\nwant %q", lines[1], wantFirstBody)
			}
		})
	}
}

func TestStreamedAndCompletedViewsMatchAcrossResize(t *testing.T) {
	const content = "wide 漢字 content wraps consistently\n\nfinal line"
	for _, width := range []int{8, 17, 40} {
		full := New()
		full.SetSize(width, 8)
		full.StreamText(content)
		full.streaming = false

		incremental := New()
		incremental.SetSize(40, 8)
		incremental.StreamText("wide 漢")
		incremental.AppendStreamText("字 content wraps consistently\n\nfinal line")
		incremental.SetSize(width, 8)
		incremental.streaming = false
		full.scrollPos, incremental.scrollPos = 0, 0

		if got, want := incremental.View(), full.View(); got != want {
			t.Fatalf("width %d: incremental view differs from completed\ngot  %q\nwant %q", width, got, want)
		}
		for i, line := range strings.Split(full.View(), "\n") {
			if got := lipgloss.Width(line); got != width {
				t.Fatalf("width %d row %d rendered width %d", width, i, got)
			}
		}
	}
}
