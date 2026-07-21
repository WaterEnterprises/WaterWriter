# AGENTS.md — Water Writer

An AI-agent CLI/TUI application that autonomously writes books. Built in Go with
Cobra (commands) and Bubble Tea (TUI), backed by a pure-Go SQLite database
(`modernc.org/sqlite`, no cgo). The LLM layer talks to any OpenAI-compatible,
native Anthropic, or native Google Gemini Chat Completions API.

## TL;DR for an agent working in this repo

- **Module path:** `github.com/WaterEnterprises/WaterWriter`
- **Language:** Go 1.26+, no CGO (`SET CGO_ENABLED=0` is fine).
- **Build:** `go build -o waterwriter.exe .`
- **Verify:** `go vet ./...` and `go build ./...` must both pass.
- **Config:** The first time you create/open a project, a built-in TUI **config
  wizard** walks you through provider selection, API key entry, model selection
  (live from the endpoint), and thinking effort. Everything is saved to the
  database — no `.env` file needed. The API key is stored in the `settings`
  table (`db.SettingAPIKey`).
- **Dependencies** are managed with `go mod tidy` (already resolved). Do not
  vendor; rely on the module cache.

## What the software does

A "book project" is driven through strictly ordered phases. State is persisted
in SQLite after every step so work can be resumed. The phase is derived from the
database (see `db.DB.GetPhase`), never from a local file.

### Phases (stored in `projects.status` / derived via `GetPhase`)

1. **`qa`** — The LLM generates interview questions (persisted in
   `qa_questions`); the user answers them in the TUI one at a time. Each answer
   is saved immediately to `qa_pairs`. `GetPhase` stays in `qa` until **every**
   question has an answer, so resuming mid-interview continues at the first
   unanswered question (it "checks what was answered before" building the TOC).

2. **`brief`** — All Q&A pairs are compiled by the LLM into a markdown "book
   brief" (the compiled context) and saved (`briefs`).

3. **`titletoc`** — The LLM generates a title, subtitle, and an ordered list of
   chapter titles; saved to `books` + `chapters`. A `todos` row is created per
   chapter ("huge todo for each chapter").

4. **`subchapters`** — For each chapter the LLM generates subchapter titles;
   saved to `subchapters` and a matching `todos` row per subchapter is created.

5. **`writing`** — Each subchapter's prose is streamed from the LLM, displayed
   live in the TUI, and saved to `subchapters.content` (status `done`) as it
   completes. The matching subchapter `todos` row is marked done; when a whole
   chapter's subchapters are all done, the chapter `todos` row and chapter
   status are marked done. Continuity is maintained by feeding previously
   written subchapters back into the prompt (`Agent.BuildWriteContext`), which
   assembles the compiled Q&A brief, the title + TOC, and all prior prose.

6. **`done`** — All subchapters written; the project is complete.

### What persists as "context" across the whole run

The compiled Q&A brief, the title + table of contents, prior subchapter prose,
and the current assignment (chapter + subchapter). It is assembled fresh for
each write call from the database.

### TUI screens and states (`internal/tui/app.go`)

| State | Purpose |
|-------|---------|
| `stateInit` | Resolving project phase on open |
| `stateHome` | Project picker; shows LLM warning banner if unconfigured |
| `stateConfig` | **Config wizard** — 5-step interactive setup |
| `stateQA` | Interview mode — answering one question at a time |
| `stateQAReview` | **Review all Q&A answers** before compiling brief |
| `stateThink` | Spinner while LLM generates (brief / TOC / subchapters) |
| `stateWrite` | Live streaming of subchapter prose |
| `stateDone` | Completion screen |
| `stateError` | Error display |

### Config wizard (5 steps, stateConfig)

| Step | What | Interaction |
|------|------|-------------|
| **0** | Provider selection | Navigate list with ↑/↓, select with Enter |
| **1** | API key entry | Text input; paste with Ctrl+V works (paste detection ignores stray Enter) |
| **2** | **Model picker** | Queries the provider's `/models` endpoint live; shows sorted list. Picks with ↑/↓ + Enter. Falls back to manual text entry on error. |
| **3** | **Thinking effort** | Choose Default / Low / Medium / **High** (default) |
| **4** | Save result | Shows what was saved; Enter to continue |

All settings (provider, model, API key, thinking effort) are persisted to the
`settings` table in the database.

### Q&A review screen (stateQAReview)

After answering all questions (or when resuming a project with all answers
complete), the user sees a **review screen** listing every Q&A pair:

- **↑/↓** — navigate through questions + "Confirm and compile" button
- **Enter on a question** — edit that answer (pre-filled input)
- **Esc while editing** — cancel, return to review unchanged
- **Enter on compile button**, or **`c`** — compile the brief and advance

Paste detection is built in: pasting a large multi-line answer via Ctrl+V
buffers all text and ignores embedded newline-as-Enter events (both bracketed
and unbracketed paste).

### Writing view (stateWrite)

Streams subchapter prose from the LLM in real time. Shows:
- Progress bar (% complete across all subchapters)
- Current chapter / subchapter heading
- Live-scrolling viewport of the text being written
- [Ctrl+C] to quit

## CLI commands (`cmd/`)

- **`waterwriter`** (no subcommand, interactive terminal) — launch the TUI home
  screen. Double-clicking the executable opens this (a console is attached, so
  it is treated as interactive). Piped or non-interactive use prints help.
- **`waterwriter create <name>`** — create a project and launch the TUI.
- **`waterwriter open <name>`** — open an existing project; resumes at its
  current phase automatically.
- **`waterwriter list`** — list projects with phase and title.
- **`waterwriter status <name>`** — detailed per-chapter/subchapter progress.
- **`waterwriter export <name> [dir]`** — write the finished book to `book.md`
  under a sanitized title folder.
- **`waterwriter delete <name>`** — remove project and all related rows.
- **`waterwriter providers`** — list every supported provider with its default
  base URL, model, and API style.
- **`waterwriter models`** — query the configured endpoint for its available
  models (live; requires a key for cloud providers).
- **`waterwriter config [--provider X] [--model Y] [--base-url Z] [--style W]
  [--select]`** — view or change the LLM provider/model saved in the database.
  With no flags it prints the current selection (and where each value comes
  from). When you change the provider (or pass `--select`) without giving
  `--model`, the tool queries the endpoint for its live model list and lets you
  pick one interactively.

**Global flag:** `--db` (default `~/.waterwriter/waterwriter.db`).

## Package layout

```
main.go                  — entrypoint, calls cmd.Execute()

cmd/
  root.go                — root command, initApp() wiring (DB + LLM + Agent)
  create.go              — waterwriter create
  open.go                — waterwriter open
  list.go                — waterwriter list
  status.go              — waterwriter status
  export.go              — waterwriter export
  delete.go              — waterwriter delete
  providers.go           — waterwriter providers
  models.go              — waterwriter models
  config.go              — waterwriter config (view/change provider/model)

internal/
  db/
    db.go                — connection + migrations (pure-Go sqlite, driver "sqlite")
    models.go            — Go structs (Project, QA, Brief, Book, Chapter, ...)
    store.go             — all CRUD + GetPhase + DeleteProject* + settings helpers
  llm/
    client.go            — Client struct for all OpenAI/Anthropic/Gemini API calls
                           Complete / CompleteStream / ListModels / Ready
  agent/
    agent.go             — orchestration: GenerateQuestions, CompileBrief,
                           GenerateTitleTOC, GenerateSubchapters,
                           BuildWriteContext, WriteSubchapterStream
  tui/
    app.go               — Bubble Tea Model + Update + View (~1300 lines)
                           States: stateInit, stateHome, stateConfig, stateQA,
                           stateQAReview, stateThink, stateWrite, stateDone,
                           stateError
```

### Key internals

- **`initApp()`** in `cmd/root.go` — loads DB settings (provider, model, base
  URL, style, API key, thinking effort) with env-var overrides, constructs the
  LLM client and Agent. Called by every command that needs the LLM.
- **`saveConfig()`** in `internal/tui/app.go` — persists wizard choices to the
  DB via `SetSettings`, then sends a `configSavedMsg` to the main loop which
  updates `m.agent.LLM` in a thread-safe way.
- **`configLoadModelsCmd()`** — creates a temporary LLM client with the
  partially-entered config, calls `ListModels`, returns a `modelsLoadedMsg`.
- **Paste detection** — In Q&A mode, `qaLastChar` records every keystroke
  timestamp. If an `Enter` arrives within 500ms of the last character, it is
  treated as part of an unbracketed paste and ignored. Text accumulates in the
  input until the user presses Enter deliberately.
- **`reasoning_effort`** — The LLM client's `chatRequest` struct includes an
  optional `ReasoningEffort` field (`"low" | "medium" | "high"`) passed through
  to OpenAI-compatible endpoints. The TUI wizard lets users choose it.

## Database tables (`internal/db/`)

| Table | Purpose |
|-------|---------|
| `projects` | Project metadata (name, status, timestamps) |
| `qa_questions` | LLM-generated interview questions (position ordered) |
| `qa_pairs` | User answers; position links to question |
| `briefs` | Compiled Q&A brief (markdown) |
| `books` | Title + subtitle |
| `chapters` | Chapter titles, position, status |
| `subchapters` | Subchapter titles, content, status |
| `todos` | Per-chapter and per-subchapter tracking |
| `settings` | Key/value store (provider, model, api_key, thinking_effort, etc.) |

**LLM setting keys** (`db.SettingProvider`, `SettingModel`, `SettingBaseURL`,
`SettingStyle`, `SettingAPIKey`, `SettingThinkingEffort`) — persisted via
`SetSettings` / read via `GetSettings`. The API key is stored here (not in a
`.env` file).

## Configuration

All configuration is done through the **TUI config wizard** or the
`waterwriter config` CLI command. Settings are stored in the database `settings`
table. Environment variables (from `.env` or the OS) are still honored as
fallbacks for backwards compatibility.

| Setting | DB key | Env fallback |
|---------|--------|-------------|
| Provider | `provider` | `WATERWRITER_LLM_PROVIDER` |
| Model | `model` | `WATERWRITER_LLM_MODEL` |
| Base URL | `base_url` | `WATERWRITER_LLM_BASE_URL` |
| API style | `style` | `WATERWRITER_LLM_API_STYLE` |
| API key | `api_key` | `WATERWRITER_LLM_API_KEY` |
| Thinking effort | `thinking_effort` | (none) |

The home screen shows a warning banner when the LLM is not configured, with
instructions to press `[c]` or `[Enter]` to launch the wizard. There is no
`.env` file to create — just run the wizard once.

## Conventions for contributors

- **No CGO.** Keep the SQLite dependency pure-Go (`modernc.org/sqlite`). Do not
  switch to `mattn/go-sqlite3` or `sqlite3` (cgo).
- **Persist to the DB** at every phase boundary and after each subchapter write
  so `open` can resume.
- **The TUI is the primary UI.** Running `waterwriter` with no subcommand
  launches the TUI home screen (`stateHome`) when attached to an interactive
  terminal; piped/non-interactive use prints help. Other commands (`list`,
  `status`, `export`, `delete`, `providers`, `models`, `config`) print to
  stdout and never launch the TUI.
- **Windows double-click:** `cmd.Execute()` sets `cobra.MousetrapHelpText = ""`
  so double-clicking the exe opens the TUI instead of Cobra's "open cmd.exe"
  splash. When launched by double-clicking (detected via
  `mousetrap.StartedByExplorer()`), the process re-execs itself through
  `cmd /c start` into a fresh interactive console (`WATERWRITER_REEXEC` env
  guard) because the explorer-inherited console is not interactive and would
  make the TUI appear frozen.
- **LLM calls** go through `internal/llm` only; prompt text lives in
  `internal/agent`. Do not hardcode prompts in `cmd/` or `tui/`.
- **Streaming writes** should continue to use the channel/listener pattern in
  `app.go` so live text renders and state stays consistent.
- **New states** for the Bubble Tea model must be added to the `state` iota
  enum, handled in `View()` and `Update()`, and the `View()` switch-statement
  must include a case for them.
- **Config wizard steps** are numbered 0-4 in `handleConfigKey()` /
  `configView()`. Adding a step requires updating both the key handler and the
  view switch.
- **Run `go vet ./...` and `go build ./...`** before considering any change
  done.

## Testing / running locally without burning API calls

- `go build -o waterwriter.exe .` then `.\waterwriter.exe list` works without
  an API key (no LLM call).
- Creating/opening a project needs a valid API key. The TUI wizard handles this.
- Verifying the DB layer without the LLM: use `status`/`export`/`delete` on a
  project created in a prior run, or unit-test `internal/db` directly.
