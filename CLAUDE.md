# CLAUDE.md

Guidance for agents and contributors working in this repository. User-facing behavior
(keys, commands, configuration) is documented in `README.md`; this file covers internals,
invariants, and conventions.

## What this is

marginctl is a terminal novel editor written in Go on
[Bubbletea](https://github.com/charmbracelet/bubbletea) (Elm-style Model/Update/View) with
Lipgloss for styling. It provides Vim-style modal editing, chapter projects, story-state
tracking, outline navigation, a `:` command prompt, and optional LLM assistance.

## Commands

```bash
go build -o marginctl ./cmd/marginctl   # build
go run ./cmd/marginctl [path]           # run (no path: empty buffer; directory: project; file: single file)
go test ./...                           # tests
go test -race ./...                     # race detector
go vet ./...                            # vet
golangci-lint run ./...                 # lint (v2, also run in CI)
```

A directory is a project only if it contains `chapters/`. Story state lives in `state/`:
`characters/`, `locations/`, custom category directories, `style/voice.md`, `timeline.md`,
and `open_threads.md`.

## Architecture

`cmd/marginctl/main.go` builds the root model and runs it in the alternate screen. All logic
lives in `internal/`, one concept per package, with no import cycles. Leaf packages never
import `app`.

**Root model: `internal/app`.** Owns the sub-models, routes `tea.Msg` values, and coordinates
layout, persistence, prompts, pending actions, and generation-checked asynchronous work. The
package is split one concept per file (for example `update.go`, `keys.go`, `commands.go`,
`prompts.go`, `pending.go`, `proposals.go`, `chapters.go`, `view.go`). `coordinator.go`
(`persistenceCoordinator`, `writeExternalDocument`, `saveBufferTo`) is the app-level path for
editor-buffer loads and saves and for fingerprint-checked state-document writes (voice guide,
proposal updates and appends). Other durable writes have their own writers: `state.Manager`
(`:InitState`, `:AddCharacter`/`:AddLocation`, `:AddTimeline`, new proposal entries),
`project.Project` (`:NewChapter`, chapter lifecycle, `:replace`, `:Compile`), swap files, notes
sidecars, sessions, config, auth, and project scaffolding.

- **`Focus`** (`FocusEditor`, `FocusLeftPane`, `FocusResults`) selects which pane receives keys.
- **`LeftPaneMode`** (`PaneModeOutline`, `PaneModeProject`, `PaneModeState`) selects what the
  left pane shows.
- `promptKind`, `PendingAction`, recovery state, connection state, and LLM operation envelopes
  are tagged states. All bottom-line prompts share `internal/input`.
- Registered commands receive an immutable `command.Context` snapshot and return typed intents.
  Only the root model executes side effects. LLM calls and slow file work run as `tea.Cmd`s and
  carry immutable project and file generations back to `Update`.

**Sub-models.** Each pane package exposes a Bubbletea-style `Model` with
`Update(tea.Msg) (Model, tea.Cmd)`, `View() string`, `Focus()`/`Blur()`, and `SetSize(w, h)`.
Panes communicate upward by returning typed messages (`ChapterSelectedMsg`,
`HeadingSelectedMsg`, `DiffAcceptedMsg`, and so on) that `app.Update` handles.

| Package | Role |
|---|---|
| `editor` | Modal buffer (Normal/Insert/Visual), soft wrap, Markdown-tinted rendering, cursor and scroll, clipboard. |
| `command` | Command `Registry` and `Context`; `RegisterDefaults` registers built-in commands. |
| `results` | Review pane with a fixed header and footer, streaming reports, confirmations, and word-aware diffs. |
| `llm` | Hand-rolled HTTP client for `anthropic`, `openai`, `vercel`, and `chatgpt`; prompts; streaming. No vendor SDK. Never imports `command`. |
| `llmcmd` | Adapter that registers LLM commands into `command.Registry`: `command.Result` builders, `ResponseMsg`/`StreamStartMsg`/`Operation`, state-proposal parsing, async runners. Imports `llm` and `command`. |
| `auth` | ChatGPT OAuth (browser and device-code flows), token store, and provider activation. |
| `config` | Loads `config.toml` plus environment overrides; surgical `[llm]` saves (`tomledit.go`). |
| `session` | Per-project last file, cursor, and layout in `sessions.json` next to the config file. Restored on `OpenPath`, saved on an intentional quit. |
| `project` | Project and file discovery, chapter listing and lifecycle, reads and saves, search and replace, compile. |
| `state` | Contained `state/` discovery and creation; entry slugs (`slug.go`). |
| `mdtitle` | Leaf package: title-heading rule shared by `state` and `project`. |
| `fsutil` | Containment, symlink rejection, name validation (`pathshape.go`), atomic writes. |
| `outline`, `projectnav`, `statenav` | Left-pane navigators. |
| `input`, `notification` | Cursor-aware prompt editing; persistent typed notifications. |
| `headerbar`, `statusbar`, `theme` | Chrome and styles. |

## Conventions

- Standard Go idioms. Import groups: standard library, third party, internal. Alias Bubbletea
  as `tea`.
- Pointer receivers for mutation, value receivers for reads. Sub-models are stored and returned
  by value through `Update`.
- Panes never reach into `app`; they emit messages.
- Keep `README.md` accurate when you change user-visible keys, commands, or configuration.
  `:help` (`internal/app/help.go`) has a hand-maintained static section followed by lists
  generated from `colonAliases` and the registry; update the static section when you add a
  builtin `:` verb or key.

## Integrity invariants

- Editor positions are grapheme indices, and the raw and displayed text share one index space.
  Wrapped segments own authoritative source ranges; never reconstruct coordinates from rendered
  strings.
- Project-controlled chapter and state paths reject symlinks and must pass
  `fsutil.ContainedPath`. Chapter and category directory names pass through
  `fsutil.SanitizeName`. State entry filenames are `state.Slug` output, checked with
  `fsutil.PlainName`; the real name is kept in the entry's `# ` heading.
- Saves preserve existing file modes, use an atomic rename plus a directory sync, and compare
  strong fingerprints. Private sidecars and credential stores use `0600`.
- Recovery, pending confirmations, and asynchronous results are bound to exact buffer, project,
  revision, target, and action identities. Never weaken those checks.
- Navigator motion only moves the selection. Files load on `Enter` and must preserve a dirty
  active buffer.

### Titles and state entries

State entry files (characters, locations, custom categories) are named with `state.Slug`;
collisions get `-2`, `-3`, and so on. The display name is the `# ` heading when it is the first
non-blank line after any front matter, else the front-matter `name:`, else the filename stem
(for hand-made files). Proposals resolve existing entries by normalized display name (NFC,
invisible characters stripped, case-folded; the stem only for untitled files) and never build a
path from a model-supplied name.

Chapter display titles use the same rule through `internal/mdtitle`. The navigator shows
`N  Title` from the title heading and falls back to the filename. Filenames keep the
`%03d_` ordering prefix.

## Testing

- Tests that can reach user directories must isolate them. `cmd/marginctl`, `internal/app`,
  `internal/auth`, `internal/config`, `internal/session`, `internal/llm`, and `internal/llmcmd`
  have a `TestMain` that points `HOME`, `USERPROFILE`, `XDG_*`, `APPDATA`, and `LOCALAPPDATA` at a
  temporary directory. Some tests also use `t.Setenv` or the `authStorePath` seam.
  Packages whose tests drive the editor clipboard call `editor.UseMemoryClipboard()` from
  `TestMain` (`internal/app/testmain_test.go` does both). Add the applicable isolation to any
  new package.
  Tests must never read or write the real `~/.config/marginctl` or the system clipboard.
- Assert on rendered `View()` output when a test checks what the user sees, not only on model
  fields; the status bar is one row and clips long text.

## `:` command prompt

`handleCommandInput` (`prompts.go`) calls `executeCommand`, which dispatches through the
`builtinCommands` table in `internal/app/commands.go`. Related verbs share a handler that
receives the verb. Registry commands are exposed as PascalCase verbs through `colonAliases` in
`internal/app/colon.go` (verb to registry ID, run by `executeRegisteredCommandCmd`).
`hiddenColonAliases` holds back-compat verbs (`:words`, `:compile`) that dispatch but never
complete.

`colon.go` is the single completion source (`allColonVerbs`, `matchColonPrefix`).
`builtinColonVerbs` is derived from the table, so adding a row both dispatches and completes
the verb. `completeCommand` cycles matches on `Tab`/`Shift+Tab`, resets whenever the buffer
text changes (including deletes and history recall), and never completes inside an argument.
`commandBarView` renders a dim bracketed candidate hint.

## Configuration internals

The config file is `$XDG_CONFIG_HOME/marginctl/config.toml`, defaulting to
`~/.config/marginctl/config.toml`. `config.Load` applies env > file > defaults:

- An explicit `provider` (file or `MARGINCTL_LLM_PROVIDER`) always wins. Provider inference
  from a stray `*_API_KEY` runs only when no provider is chosen, and a chosen provider never
  borrows another provider's key.
- Switching provider through the environment drops the file-sourced `api_key`, `model`, and
  `endpoint`, unless those are also set in the environment.
- Unknown TOML keys and load errors surface through notifications at startup.

`:setup` writes through `config.SaveSetup` using file-sourced values only, never the env-merged
`Config`, so an environment key cannot leak to disk. A provider switch clears the file `model`
and `endpoint`. Saves edit only the `[llm]` keys in place (`tomledit.go`), preserving comments
and other tables. An empty key passed to `SaveSetup` keeps the file's key when the provider is
unchanged (the `:setup` prompt itself requires a key). When the layout cannot be edited safely,
the save rewrites the file, keeps `config.toml.bak-<timestamp>`, and returns a notice in
`SaveResult` that the app shows through `reportConfigRewrite`. OAuth activation uses
`SaveProviderMetadataResult` and carries the same notice.

With no config file and no environment key, startup hints at `:setup`. `--version` prints the
`-X main.version=` stamp (CI), else the module version embedded by `go install`, else `dev`.

## LLM internals

Providers: `anthropic` (`/v1/messages`), `openai` and `vercel` (OpenAI-compatible
`/v1/chat/completions`), and `chatgpt` (OAuth, Responses API). `endpoint` is the full POST URL,
used verbatim. Default models are in `internal/llm/client.go` (`claude-sonnet-5`,
`gpt-5.6-sol`, `openai/gpt-5.6-sol`); re-check that they are current when you touch that file,
and keep the README table in sync.

- **Output cap.** `modelRequiresMaxCompletionTokens` is a prefix heuristic (o-series and `gpt-N`
  with N ≥ 5) that sends `max_completion_tokens`; other models get `max_tokens`. Chat
  Completions returns 400 for `max_tokens` on GPT-5+ and o-series, so a new model family the
  heuristic misses needs a one-line update.
- **Retries.** `Client.send` retries 429, 500, 502, 503, 529, and connection errors up to
  `maxRetries` times with exponential backoff, honoring `Retry-After` (seconds or HTTP date),
  all within the single per-call timeout. A cancel or timeout during backoff is reported as
  such through `ctxError`, never as the last HTTP status. Streaming requests make a single
  attempt and do not retry rate-limit, server, or connection failures; ChatGPT requests refresh
  credentials and retry once after a 401. ChatGPT one-shot calls retry opening their
  stream (`openResponsesStreamRetrying`) but never restart a partially consumed one.
- **API.** `Complete` returns `(text, Usage, error)`.
- **Streaming.** Text-result commands (continuity, summarize) use `Client.CompleteStream`
  (`internal/llm/stream.go`), which returns a `<-chan StreamEvent`. The app subscribes through
  `waitForStream` and renders through `results.StreamText`. The stream path is request-ID-gated
  like the one-shot `ResponseMsg` path, and the producer goroutine selects on `ctx.Done` for
  every send so a cancelled or superseded stream cannot leak. Diff and voice-guide commands
  use one-shot `Complete`, because a diff needs the whole text.

### ChatGPT OAuth

`chatgpt` is separate from the API-key `openai` provider. Activation (`internal/auth`) stores
tokens in marginctl's own auth store (paths in `README.md`) and writes only non-secret
`provider = "chatgpt"` metadata through `SaveProviderMetadataResult`. Environment provider
precedence still applies, so `MARGINCTL_LLM_PROVIDER` can block activation until it is unset.

This path uses the private Codex-style contract at
`https://chatgpt.com/backend-api/codex/responses`. Never document it as stable public OpenAI
API support. The backend speaks the Responses API, not Chat Completions. It rejects `messages`,
`max_tokens`, and even `max_output_tokens` (it accepts only the exact parameters the Codex CLI
sends, so there is no token cap), and it defaults `store` to true. Each violation returns
`400 {"detail":"…"}` from ChatGPT's gateway.

So `internal/llm/chatgpt.go` has its own request and response code, separate from the
`openai`/`vercel` Chat Completions path. `newResponsesRequest` builds a `responsesRequest` with
system `instructions`, a structured `input` message array, `store: false`, and `stream: true`;
the backend requires streaming, so both entry points consume Responses SSE.
`completeChatGPT` accumulates the deltas before returning, and `streamChatGPT` forwards them.
`readResponsesStream` handles `response.output_text.delta`, refusals, `response.completed`,
`response.incomplete` (reason `content_filter` is a refusal; any other reason, or
`max_output_tokens` on `response.completed`, is truncated), and
`response.failed`, including usage (`usage.{input,output}_tokens`). Both use
`openOpenAICompatibleStream` for OAuth headers and the 401 refresh retry. `Complete` and
`CompleteStream` route `chatgpt` to these functions; `openai` and `vercel` keep the Chat
Completions body unchanged.
