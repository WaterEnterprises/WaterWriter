# AGENTS.md — Water Writer

An AI-agent CLI/TUI application that autonomously writes books. Built in Go with
Cobra (commands) and Bubble Tea (TUI), backed by a pure-Go SQLite database
(`modernc.org/sqlite`, no cgo). The LLM layer talks to any OpenAI-compatible
Chat Completions API.

## TL;DR for an agent working in this repo

- Module path: `github.com/WaterEnterprises/WaterWriter`
- Language: Go 1.26, no CGO (`SET CGO_ENABLED=0` is fine).
- Build: `go build ./...` then `go build -o waterwriter.exe .`
- Verify: `go vet ./...` and `go build ./...` must both pass.
- Dependencies are managed with `go mod tidy` (already resolved). Do not
  vendor; rely on the module cache.
- The app requires an LLM API key at runtime (see "Configuration" below).
  Commands that need the LLM call `initApp()`, which fails fast if the key is
  missing — this is expected, not a build error.

## What the software does

A "book project" is driven through strictly ordered phases. State is persisted
in SQLite after every step so work can be resumed. The phase is derived from the
database (see `db.DB.GetPhase`), never from a local file.

Phases (as stored in `projects.status` / derived via `GetPhase`):

1. `qa` — The LLM generates interview questions (persisted in `qa_questions`);
   the user answers them in the TUI one at a time. Each answer is saved
   immediately to `qa_pairs`. `GetPhase` stays in `qa` until **every** question
   has an answer, so resuming mid-interview continues at the first unanswered
   question (it "checks what was answered before" building the TOC).
2. `brief` — The Q&A is compiled by the LLM into a markdown "book brief"
   (the compiled context) and saved (`briefs`).
3. `titletoc` — The LLM generates a title, subtitle, and an ordered list of
   chapter titles; saved to `books` + `chapters`. A `todos` row is created per
   chapter ("huge todo for each chapter").
4. `subchapters` — For each chapter the LLM generates subchapter titles; saved
   to `subchapters` and a matching `todos` row per subchapter is created.
5. `writing` — Each subchapter's prose is streamed from the LLM, displayed live
   in the TUI, and saved to `subchapters.content` (status `done`) as it
   completes. The matching subchapter `todos` row is marked done; when a whole
   chapter's subchapters are all done, the chapter `todos` row and chapter
   status are marked done. Continuity is maintained by feeding previously
   written subchapters back into the prompt (`Agent.BuildWriteContext`), which
   assembles the compiled Q&A brief, the title + TOC, and all prior prose.
6. `done` — All subchapters written; the project is complete.

The "context" that persists across the whole run is: the compiled Q&A brief,
the title + table of contents, prior subchapter prose, and the current
assignment (chapter + subchapter). It is assembled fresh for each write call
from the database.

## CLI commands (`cmd/`)

- `waterwriter` (no subcommand, interactive terminal) — launch the TUI home
  screen: a project picker that lists existing projects and lets you create a
  new one or open/resume an existing one. Double-clicking the executable opens
  this (a console is attached, so it is treated as interactive). Piped or
  non-interactive use prints help instead.
- `waterwriter create <name>` — create a project and launch the TUI.
- `waterwriter open <name>` — open an existing project; resumes at its current
  phase automatically.
- `waterwriter list` — list projects with phase and title.
- `waterwriter status <name>` — detailed per-chapter/subchapter progress.
- `waterwriter export <name> [dir]` — write the finished book to `book.md`
  under a sanitized title folder.
- `waterwriter delete <name>` — remove project and all related rows.
- `waterwriter providers` — list every supported provider with its default
  base URL, model, and API style.
- `waterwriter models` — query the configured endpoint for its available
  models (live; requires a key for cloud providers).
- `waterwriter config [--provider X] [--model Y] [--base-url Z] [--style W]
  [--select]` — view or change the LLM provider/model saved in the database.
  With no flags it prints the current selection (and where each value comes
  from). When you change the provider (or pass `--select`) without giving
  `--model`, the tool queries the endpoint for its live model list and lets you
  pick one interactively, so you always choose a real, current model instead of
  a hardcoded default.

Global flag: `--db` (default `~/.waterwriter/waterwriter.db`).

## Package layout

- `main.go` — entrypoint, calls `cmd.Execute()`.
- `cmd/` — Cobra commands. `root.go` owns `initApp()` (DB + LLM + Agent wiring
  and env loading) and the `--db` flag.
- `internal/db/` — `db.go` (connection + migrations, pure-Go sqlite driver
  name `"sqlite"`), `models.go` (structs), `store.go` (all CRUD + `GetPhase`,
   `DeleteProject*`). Tables: `qa_questions` (the generated question list,
   persisted so a resumed interview continues at the first unanswered question),
   `qa_pairs`, `briefs`, `books`, `chapters`, `subchapters`, `todos`,
   `settings` (key/value store for the saved `provider`/`model`/`base_url`/
   `style`; see `SetSetting`/`GetSettings`). The API key is never written here.
- `internal/llm/` — `client.go`. `Client` supports many providers via a
  preset table (`Providers`): `openai`, `anthropic`, `gemini`, `deepseek`,
  `groq`, `openrouter`, `together`, `ollama`, `xai`, `mistral`, `perplexity`,
  `fireworks`, `huggingface`, `deepinfra`, `novita`, `nvidia`, `siliconflow`,
  `qwen`, `moonshot`, `zhipu`, `replicate`, `anyscale`, `scaleway`, plus local
  servers `vllm`/`lmstudio`/`textgenwebui`, and a `custom` endpoint. Three wire
  formats are implemented: OpenAI-compatible Chat Completions (the majority of
  providers), native Anthropic `/v1/messages` (SSE event parsing), and native
  Google Gemini Generative Language API (`x-goog-api-key`, `generateContent` /
  `streamGenerateContent`). Arbitrary extra request headers are supported via
  `WATERWRITER_LLM_EXTRA_HEADERS`, so any endpoint is reachable. Exposes
  `Complete` (buffered) and `CompleteStream` (SSE, `onChunk` callback).
  `NewClient()` reads provider presets + env overrides; `Ready()` reports
  whether a request can be made (key requirement is per-provider). Honors
  `WATERWRITER_LLM_PROVIDER`, `_BASE_URL`, `_MODEL`, `_API_KEY`, `_API_STYLE`,
  `_EXTRA_HEADERS`.
- `internal/agent/` — `agent.go`. Orchestration logic and prompt construction.
  Public methods used by the TUI: `GenerateQuestions`, `CompileBrief`,
  `GenerateTitleTOC`, `GenerateSubchapters`, `BuildWriteContext`,
  `WriteSubchapterStream`.
- `internal/tui/` — `app.go`. Bubble Tea model + view. States: `stateInit`,
  `stateQA`, `stateThink`, `stateWrite`, `stateDone`, `stateError`. The writing
  loop streams chunks over a channel (`beginWrite`/`listenStream`) and saves
  each subchapter to the DB on completion.

## Configuration (required for LLM-backed commands)

Create a `.env` (see `.env.example`) or export these environment variables:

- `WATERWRITER_LLM_PROVIDER` — one of `openai` (default), `anthropic`,
  `gemini`, `deepseek`, `groq`, `openrouter`, `together`, `ollama`, `custom`.
- `WATERWRITER_LLM_API_KEY` — required for cloud providers (not `ollama`).
- `WATERWRITER_LLM_BASE_URL` — default per provider; override for `custom`.
- `WATERWRITER_LLM_MODEL` — default per provider.
- `WATERWRITER_LLM_API_STYLE` — `openai` | `anthropic` | `gemini` (for `custom` endpoints).
- `WATERWRITER_LLM_EXTRA_HEADERS` — `"Name: Value, Other: Value2"` for any
  extra HTTP headers an endpoint requires (e.g. an alternate auth scheme).

`.env` is loaded via `godotenv` in `initApp()`. Run `waterwriter providers`
to list every supported provider and its defaults. Any OpenAI-compatible
endpoint (or a native Anthropic-compatible or Gemini-compatible one) works via
`custom`.

The provider/model/base_url/style are also persisted in the database
(`settings` table) and can be changed at runtime with `waterwriter config`
without touching `.env`. Precedence in `initApp()`: environment variables win,
then saved database settings, then per-provider presets. The API key is only
ever read from the environment. Rather than hardcoding a model, `config`
(when changing provider or given `--select` without `--model`) queries the
endpoint via `Client.ListModels` and lets the user pick a currently-available
model interactively.

## Conventions for contributors

- No CGO. Keep the SQLite dependency pure-Go (`modernc.org/sqlite`). Do not
  switch to `mattn/go-sqlite3` or `sqlite3` (cgo).
- Persist to the DB at every phase boundary and after each subchapter write so
  `open` can resume.
- The TUI is the primary UI. Running `waterwriter` with no subcommand launches
  the TUI home screen (`internal/tui` `stateHome`) when attached to an
  interactive terminal; piped/non-interactive use prints help. The other
  commands (`list`, `status`, `export`, `delete`, `providers`, `models`,
  `config`) print to stdout and never launch the TUI. `cmd.Execute()` sets
  `cobra.MousetrapHelpText = ""` so double-clicking the exe opens the TUI
  instead of Cobra's "open cmd.exe" splash (Windows). On Windows, when launched
  by double-clicking (detected via `mousetrap.StartedByExplorer()`), the process
  re-execs itself through `cmd /c start` into a fresh interactive console
  (`WATERWRITER_REEXEC` env guard) because the explorer-inherited console is not
  interactive and would make the TUI appear frozen.
- LLM calls go through `internal/llm` only; prompt text lives in
  `internal/agent`. Do not hardcode prompts in `cmd/` or `tui/`.
- Streaming writes should continue to use the channel/listener pattern in
  `app.go` so live text renders and state stays consistent.
- Run `go vet ./...` and `go build ./...` before considering any change done.

## Testing / running locally without burning API calls

- `go build -o waterwriter.exe .` then `.\waterwriter.exe list` works without
  an API key (no LLM call).
- Creating/opening a project needs a valid `WATERWRITER_LLM_API_KEY`.
- Verifying the DB layer without the LLM: use `status`/`export`/`delete` on a
  project created in a prior run, or unit-test `internal/db` directly.
