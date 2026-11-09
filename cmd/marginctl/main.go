package main

import (
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ShayCichocki/marginctl/internal/app"
)

// version is stamped at release-build time via
// -ldflags "-X main.version=v1.2.3"; resolveVersion falls back to the module
// version embedded by `go install`, then to "dev".
var version = ""

func usage() string {
	return `marginctl — a terminal novel editor with vim-like keybindings

Usage:
  marginctl              open an empty editor
  marginctl <dir>        open a project (a directory containing chapters/)
  marginctl <file.md>    open a single file

Flags:
  -h, --help       show this help
  -v, --version    print the version

	Inside the editor, :help shows the full keybinding reference. :setup
	walks through API-key providers, with config at
	~/.config/marginctl/config.toml. :connect openai starts ChatGPT OAuth
	using the opencode/Codex-style private contract, or use
	:connect openai --device for device login. Use :auth for local status
	and :disconnect to delete the local auth store. The editor works
	without an LLM, and ChatGPT OAuth may break if that private contract
	changes.
`
}

func resolveVersion() string {
	if version != "" {
		return version
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		if v := bi.Main.Version; v != "" && v != "(devel)" {
			return v
		}
	}
	return "dev"
}

// prepare handles flags and the optional path argument without starting the
// TUI. When done is true the process should exit with code; otherwise the
// returned model is ready to run.
func prepare(args []string, stdout, stderr io.Writer) (m *app.Model, done bool, code int) {
	if len(args) > 0 {
		switch args[0] {
		case "-h", "--help":
			_, _ = fmt.Fprint(stdout, usage())
			return nil, true, 0
		case "-v", "--version":
			_, _ = fmt.Fprintln(stdout, "marginctl "+resolveVersion())
			return nil, true, 0
		}
		// Any other flag-looking argument is a mistake, not a path.
		if strings.HasPrefix(args[0], "-") {
			_, _ = fmt.Fprintf(stderr, "unknown flag: %s\n\n%s", args[0], usage())
			return nil, true, 2
		}
	}

	model := app.New()

	// Check for path argument
	if len(args) > 0 {
		path := args[0]
		if err := model.OpenPath(path); err != nil {
			_, _ = fmt.Fprintf(stderr, "Error opening %s: %v\n", path, err)
			return nil, true, 1
		}
	}
	return &model, false, 0
}

func main() {
	m, done, code := prepare(os.Args[1:], os.Stdout, os.Stderr)
	if done {
		os.Exit(code)
	}

	p := tea.NewProgram(m, tea.WithAltScreen())

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}
