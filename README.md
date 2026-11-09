# marginctl

marginctl is a terminal editor for writing novels. It combines Vim-style modal editing with a
chapter-based project layout, story state (a story bible of characters, locations, timeline, and
open threads), and optional LLM assistance for revision and continuity checks.

- **Vim-style editing.** Normal, Insert, and Visual modes with familiar motions and operators.
- **Chapter projects.** Chapters live in `chapters/`, and the navigator shows each chapter's title.
- **Story state.** Characters, locations, timeline, and open threads live as Markdown files in
  `state/`.
- **Outline navigation.** Jump between Markdown headings in the current chapter.
- **Command prompt.** File, project, pane, and LLM features run as `:` commands with Tab
  completion.
- **LLM assistance.** Anthropic, OpenAI, Vercel AI Gateway, or a ChatGPT account.
- **Data safety.** Crash recovery, atomic saves with backups, and external-change detection.

## Quick start

```bash
go install github.com/ShayCichocki/marginctl/cmd/marginctl@latest
marginctl
```

Inside the editor, run `:NewProject`, type a path for a new directory, such as `~/my-novel`, and
press `Enter`. marginctl creates the project with a first chapter and opens it. Press `i` to
start typing, press `Esc` to return to Normal mode, and run `:w` to save. Run `:NewChapter` to
add a chapter, `:help` for the in-app reference, and `:setup` to enable LLM features.

## Install

### Prebuilt binaries

Each [GitHub Release](https://github.com/ShayCichocki/marginctl/releases) has archives for Linux
and macOS (amd64 and arm64) and Windows (amd64), plus a `sha256sums.txt` file. Download the
archive for your system, check it against `sha256sums.txt`, extract it, and move `marginctl` to
a directory on your `PATH`:

```bash
tar -xzf marginctl-<os>-<arch>-v1.0.0.tar.gz
sudo mv marginctl /usr/local/bin/
```

On Windows, extract the `.zip` archive instead.

### With Go

Building marginctl requires Go 1.26.10 or later.

```bash
go install github.com/ShayCichocki/marginctl/cmd/marginctl@latest
```

Go installs the binary in `$GOBIN` if it is set, and otherwise in `$(go env GOPATH)/bin`, which
is usually `~/go/bin`. Make sure that directory is on your `PATH`. To update, run the same
command again.

To build from a checkout:

```bash
git clone https://github.com/ShayCichocki/marginctl.git
cd marginctl
go install ./cmd/marginctl
```

To update a checkout build, run `git pull` and then `go install ./cmd/marginctl` again.
`marginctl --version` prints the installed version.

New releases are published by pushing a `vX.Y.Z` tag; CI builds the archives and attaches them
to the release.

## Usage

```bash
marginctl                     # Open an empty buffer
marginctl ~/my-novel          # Open a project
marginctl notes.md            # Open a single file
marginctl --help              # Show usage
marginctl --version           # Show the version
```

A directory is a **project** when it contains a `chapters/` subdirectory. Opening a file inside
a project opens that project. When you quit, marginctl records the open file's path, cursor
position, and pane layout for the project and restores that session the next time you open it.

## Editing

marginctl uses Vim-style keys for editing. File, project, pane, and LLM features run from the
`:` prompt, and the navigators and results pane have a few keys of their own.

### Modes

| Key | Action |
|---|---|
| `i` / `a` | Insert before / after the cursor |
| `I` / `A` | Insert before the first non-blank character / at the end of the line |
| `o` / `O` | Open a new line below / above |
| `v` | Start Visual mode (character selection) |
| `Esc` | Return to Normal mode |

In Insert mode, the arrow keys move the cursor and `Tab` inserts four spaces.

### Motions

| Key | Action |
|---|---|
| `h` `j` `k` `l` or arrow keys | Left, down, up, right |
| `w` / `b` / `e` | Next word, previous word, end of word |
| `0` / `$` | Start / end of the screen line |
| `{` / `}` | Previous / next paragraph |
| `%` | Matching bracket |
| `gg` / `G` | First / last line (`5G` or `5gg` goes to line 5) |
| `Ctrl+F` / `Ctrl+B` | Scroll down / up a full page |
| `Ctrl+D` / `Ctrl+U` | Scroll down / up half a page |

Long lines soft-wrap. In Normal mode, `0` and `$` move within the wrapped row on screen, while
`I` and `A` use the whole line. The editor does not support `Home`, `End`, or `^`.

### Editing commands

| Key | Action |
|---|---|
| `x` | Delete the character under the cursor |
| `r<char>` | Replace the character under the cursor (`3r<char>` replaces three) |
| `dd` / `cc` / `yy` | Delete / change / yank the line |
| `d` `c` `y` + motion | Delete / change / yank over a motion, such as `dw`, `c$`, or `y}` |
| `p` / `P` | Paste after / before the cursor |
| `u` / `Ctrl+R` | Undo / redo |

Counts work with Normal-mode motions and operators, for example `3w`, `2dd`, `3dw`, and `d3w`.
An entire Insert session undoes as one step. When the system clipboard is available, yanks and
deletes are copied to it, and `p` pastes text copied from another app; otherwise, marginctl
uses its internal register.

In Visual mode, move with the same motions (without counts), then press `y` to yank, `d` or `x`
to delete, or `c` to change the selection. In Visual mode, `$` moves to the end of the whole
line. Press `:` to run a command on the selection.

### Search

Press `/` in Normal mode, type text, and press `Enter` to search forward in the current file.
Search is case-insensitive and matches literal text. All matches are highlighted, the status
bar shows the current match and total count, and `n` / `N` jump to the next / previous match.

To search or replace across all chapters, see [Chapters](#chapters).

## Commands

Type `:` in Normal or Visual mode to open the command prompt. (It also opens from the results
pane, but only while state proposals are pending, and only for the proposal commands and
`:w`/`:q`.) Press
`Tab` or `Shift+Tab` to cycle through matching commands. Command names are case-sensitive, so
type them as shown. The prompt supports Left/Right, `Home`/`End`, `Ctrl+W` to delete a word,
and Up/Down for command history.

### Files and app

| Command | Action |
|---|---|
| `:w [path]` | Save, or save as `path` |
| `:w! [path]` | Save even if the file changed on disk, or overwrite an existing `path` |
| `:q` / `:q!` | Quit / quit and discard unsaved changes |
| `:wq [path]` / `:wq!` | Save and quit |
| `:NewProject` | Prompt for a new or empty directory and create a project there |
| `:OpenProject` | Prompt for a project directory or file and open it |
| `:recover` / `:discard` | Restore or delete unsaved changes recovered after a crash |
| `:notifications` / `:ack` | Show / dismiss the current notification |
| `:help` | Show the in-app reference |

`:NewProject` and `:OpenProject` require saved changes, and `Tab` completes paths at their
prompt. Typed paths expand a leading `~` to your home directory. `:CloseFile` is the same as
`:q`.

`Ctrl+C` acts like `Esc` and never quits. If the terminal is smaller than 40×10, editing pauses
until you resize it; `Ctrl+Q` quits from that screen.

### Panes and layout

| Command | Action |
|---|---|
| `:ShowChapters` | Show the chapter list |
| `:ShowOutline` | Show the headings in the current chapter |
| `:ShowState` | Show the story state |
| `:ToggleNav` or `:panes` | Hide or restore the left pane |
| `:DistractionFree` | Toggle a full-screen editor |
| `:navwidth N` / `:resultswidth N` | Set the left pane / results pane width for this project (minimum 20 / 36) |

Panes hide automatically when the terminal is too narrow to show them beside the editor.

In a navigator, `j`/`k` (or the arrow keys) move the selection, `g`/`G` jump to the top/bottom,
`Enter` opens the selected item, and `Esc` returns to the editor. Moving the selection never
opens a file. In the chapter list, `n` creates a chapter. In the state pane, `n` creates an entry
in the current category, and `N` creates a category.

The results pane shows reports, diffs, and search results. Use `j`/`k` to scroll or select,
`Enter` to jump to a search result or note, `y`/`n` to accept or reject a change, and `q` or
`Esc` to close it.

## Projects

A project has this layout:

```
my-novel/
├── chapters/
│   ├── 001_chapter_1.md
│   ├── 002_The Middle.md
│   └── ...
└── state/
    ├── characters/
    │   └── ada-varga.md
    ├── locations/
    ├── style/
    │   └── voice.md
    ├── timeline.md
    └── open_threads.md
```

### Chapters

`:NewChapter` creates a chapter with the next three-digit prefix, so chapters stay in order. The
chapter list sorts naturally. Each chapter's title comes from a `# ` heading on its first
non-blank line (after any front matter), then from a front-matter `name:` field, and finally
from the filename.

| Command | Action |
|---|---|
| `:NewChapter` | Create a chapter |
| `:ChapterRename <name>` | Rename the current chapter's file (the `# ` heading is unchanged) |
| `:ChapterReorder <N>` | Move the current chapter to position N |
| `:ChapterInsert <N> <name>` | Create a chapter at position N |
| `:ChapterDuplicate [name]` | Copy the current chapter |
| `:ChapterArchive` | Move the current chapter to `archive/` |
| `:ChapterTrash` | Move the current chapter to `.trash/chapters/` |
| `:ChapterRestore <path>` | Restore a chapter by its project-relative path, such as `.trash/chapters/003_Draft.md` |
| `:WordCount` | Show total and per-chapter word counts |
| `:Compile` | Join all chapters, in order, into `manuscript.md` |
| `:search <text>` | Search all chapters and list matches as `chapter:line` |
| `:replace old/new` | Replace text across all chapters |

Commands that create, move, or rename chapters require saved changes, and they move the
chapter's notes with it. Trash is used instead of permanent deletion.

Word count and compile include unsaved changes in the current chapter, and compile keeps a
`.bak` of the previous `manuscript.md`. Search and replace read the chapter files on disk, so
save first (`:w`) to include your latest changes.

`:search` ignores case. `:replace` matches exact, case-sensitive text and requires saved
changes. It shows the match count and waits for `y` before changing any file. It does not create
`.bak` files. If a file fails to update, marginctl tries to restore the files it already
changed and reports any that it could not restore.

### Story state

The `state/` directory holds your story state as plain Markdown files. Each character,
location, or custom-category entry is one file. New entries get a lowercase filename derived
from the name, such as `ada-varga.md`, and keep the real name in a `# ` heading. Entry names
follow the same title rule as chapters, falling back to the filename.

| Command | Action |
|---|---|
| `:InitState` | Create any missing state directories and starter files |
| `:AddCharacter` / `:AddLocation` | Create a character or location entry named by the selected text |
| `:AddTimeline` | Append the selected text to `state/timeline.md` |

The `:Add` commands need a Visual-mode selection and an existing `state/` directory. `:InitState`
never overwrites existing files. The voice guide in `state/style/voice.md` describes your prose
style, and LLM rewrites follow it.

## Notes

To attach a note to text, select it in Visual mode and run `:note <text>`. Notes are anchored to
the text, so they follow it as you edit. A `●` in the gutter marks a line with a note. For a
named file, notes are stored next to it in `.<filename>.notes.json`; keep that file when you
copy the chapter. Notes on an unnamed buffer are not saved until you save the buffer to a file.

| Command | Action |
|---|---|
| `:note <text>` | Add a note to the selection |
| `:notes [open\|resolved\|orphans]` | List notes in the current file |
| `:notes-project [filter]` | List notes across all chapters |
| `:note-edit N <text>` | Change note N |
| `:note-resolve N` / `:note-delete N` | Resolve or delete note N |

Press `Enter` on a listed note to jump to it. If the anchored text is deleted, the note is kept
and marked `[orphaned]`.

## LLM assistance

LLM features need a provider; run `:setup` to configure one (see [Configuration](#configuration)).

### Selection commands

Select text in Visual mode, press `:`, and run one of these. The result opens as a diff that you
accept with `y` or reject with `n`.

| Command | Action |
|---|---|
| `:Tighten` | Make the selection more concise |
| `:ShowDontTell` | Replace telling with action and sensory detail |
| `:Dialogue` | Make dialogue more natural and distinct per character |
| `:Expand` | Expand the selection with concrete detail |
| `:RewriteVoice` | Rewrite the selection to match the voice guide |

### Chapter commands

| Command | Action |
|---|---|
| `:Continuity` | Check the chapter against your story state for inconsistencies |
| `:Summarize` | Summarize the chapter as bullet points |
| `:VoiceGuide` | Create or update `state/style/voice.md` from the chapter or selection; accept with `y` |
| `:ExtractState` | Propose story-state changes from the chapter or selection |

Continuity and summary reports stream into the results pane as the model writes them. If you
scroll up, new output is marked in the footer, and `G` resumes following.

To fit the model's input limit, `:Continuity` may leave out the least relevant state entries and
shorten a very long chapter. The report says how many entries it checked and what it left out.
If any state file other than the voice guide cannot be read, the check stops before anything is
sent. The voice guide is not part of the check; if it is unreadable, marginctl only shows a
notice.

`:ExtractState` asks the model for new characters and locations, but a proposal can also update
or append to an existing state file. Proposals to create an entry that already exists are
skipped. The numbered proposals appear in the results pane with their actions and target files.
Press `y` to apply all of them or `n` to discard them, or press `:` to decide on one at a time.
Only the commands below, `:proposals`, and `:w`/`:q` run from the pane while proposals are
pending; any other command is refused and the proposals stay as they were:

| Command | Action |
|---|---|
| `:proposal-review N` | Show the change proposal N would make (`Esc`, `q`, or `:proposals` returns to the list) |
| `:proposal-accept N` | Apply proposal N |
| `:proposal-edit N <content>` | Replace proposal N's content before you accept it |
| `:proposal-merge N` | Append proposal N to the existing entry |
| `:proposal-skip N` | Drop proposal N |

While you review a proposal, `y` applies it, `n` skips it, and `Esc` or `q` goes back to the
list without discarding anything. The list renumbers after each decision and closes when no
proposals remain. Closing the pane with `Esc` or `q` while the list is showing discards the
remaining proposals.

While a request runs, the status bar shows a spinner and the elapsed time, and `Esc` cancels.
When it finishes, the status bar shows token usage, plus an estimated cost for known Claude
models, for example `✓ 1.5k in / 340 out · ~$0.0123`.

Run `:model <id>` to switch models for the current session, or `:model` to show the current
model. Set `model` in the config file to change the default.

## Data safety

- **Crash recovery.** While a file has unsaved changes, marginctl writes them to a hidden swap
  file (`.<name>.swp`) every two seconds. After a crash, reopening the file offers `:recover` or
  `:discard`. Unnamed buffers are recovered too: start marginctl without a path, run `:recover`,
  and save with `:w <path>`.
- **External-change detection.** If the file changed on disk since you opened it, `:w` refuses
  to overwrite it. Use `:w!` to overwrite anyway. `:w <path>` also refuses to replace an
  existing file unless you use `:w! <path>`.
- **Atomic saves.** Saves write a temporary file and rename it into place, so a crash during a
  save cannot leave a truncated file. Saving over an existing file keeps a `.bak` copy of the
  previous version.
- **Safe chapter changes.** Archive and trash keep chapters inside the project instead of
  deleting them.

## Configuration

Run `:setup` to choose a provider and enter an API key. marginctl saves them to
`~/.config/marginctl/config.toml` (or `$XDG_CONFIG_HOME/marginctl/config.toml`) with owner-only
(`0600`) permissions. If a config file already exists, `:setup` stops; run `:setup!` to change
the provider or key.

You can also edit the file directly:

```toml
[llm]
provider = "anthropic"   # anthropic, openai, or vercel
api_key = "your API key"
model = "claude-sonnet-5"
timeout_seconds = 60
```

| Provider | Default model |
|---|---|
| `anthropic` | `claude-sonnet-5` |
| `openai` | `gpt-5.6-sol` |
| `vercel` (Vercel AI Gateway) | `openai/gpt-5.6-sol` |

`model`, `endpoint`, and `timeout_seconds` are optional. `endpoint` is the full request URL, not
a base URL; marginctl sends every request, including your API key, to it verbatim. Each request
times out after `timeout_seconds` (default 60). Non-streaming requests retry rate-limit and
server errors (429, 500, 502, 503, and 529) up to two times within that timeout. Streamed
reports (`:Continuity` and `:Summarize`) are not retried after rate-limit, server, or connection
failures. ChatGPT requests retry once after refreshing credentials when the server returns HTTP
401.

When `:setup` or `:connect` updates an existing file, it changes only the `[llm]` keys and keeps
your comments and other settings. If the file's layout can't be updated safely, marginctl
rewrites it, saves the old version as `config.toml.bak-<timestamp>`, and shows a notice.

### Environment variables

Environment variables override the config file:

| Variable | Overrides |
|---|---|
| `MARGINCTL_LLM_PROVIDER` | `provider` |
| `MARGINCTL_LLM_API_KEY` | `api_key` |
| `MARGINCTL_LLM_MODEL` | `model` |
| `MARGINCTL_LLM_ENDPOINT` | `endpoint` |
| `MARGINCTL_LLM_TIMEOUT` | `timeout_seconds` |

Provider-specific keys also work: `ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `AI_GATEWAY_API_KEY`,
and `VERCEL_AI_GATEWAY_API_KEY`. When a provider is set, marginctl uses only that provider's key.

### ChatGPT account

Instead of an API key, you can sign in with a ChatGPT account:

| Command | Action |
|---|---|
| `:connect openai` | Sign in; choose the browser or a device code |
| `:connect openai --browser` / `--device` | Sign in with that method directly |
| `:auth` | Show sign-in status |
| `:disconnect` | Delete marginctl's stored sign-in |

Signing in sets `provider = "chatgpt"` in the config file. Tokens are stored separately, with
owner-only permissions:

- macOS: `~/Library/Application Support/marginctl/auth.json`
- Linux: `${XDG_DATA_HOME:-~/.local/share}/marginctl/auth.json`
- Windows: `%AppData%\marginctl\auth.json`

`:disconnect` deletes only the local file; it does not sign you out of ChatGPT.

ChatGPT sign-in uses the same private interface as the Codex CLI, not a public OpenAI API, so it
can break if that interface changes. If it does, reconnect with `:connect openai` or use an
OpenAI API key through `:setup`. If `MARGINCTL_LLM_PROVIDER` is set, it overrides the config
file, so unset it to use ChatGPT sign-in.

### Theme

Choose the `dark` (default) or `light` preset, and optionally override individual colors:

```toml
[theme]
preset = "light"
primary = "#6D28D9"     # accents, cursor, selections, top-level headings
accent = "#059669"      # unsaved-changes marker, directory names
text = "#111827"        # body text
background = "#FFFFFF"  # editor background
heading = "#6D28D9"     # Markdown headings
```

## License

MIT
