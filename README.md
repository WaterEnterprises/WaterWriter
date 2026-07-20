# Water Writer 🌊📖

**An AI-powered CLI/TUI application that autonomously writes books.** Water Writer interviews you about your book idea, generates a title and table of contents, then writes each chapter through a fully autonomous workflow — all from your terminal.

Built with Go, [Bubble Tea](https://github.com/charmbracelet/bubbletea) (TUI), [Cobra](https://github.com/spf13/cobra) (CLI), and a pure-Go SQLite database.

## Features

- **📝 Interactive Interview** — The LLM asks you 8 thoughtful questions about your book (genre, audience, themes, tone, etc.)
- **📋 Book Brief** — Your answers are compiled into a structured book brief that guides the entire writing process
- **🎯 Title & TOC Generation** — An AI-generated title, subtitle, and chapter outline
- **📑 Subchapter Outlining** — Each chapter is broken into 3-6 subchapters
- **✍️ Autonomous Writing** — Each subchapter is written with full context awareness (previous chapters, brief, tone), streamed live to your terminal
- **⏯️ Resumable Workflow** — Every step is persisted to SQLite. Close and resume anytime
- **📤 Export** — Export your finished book to a clean markdown file
- **🔌 Multi-Provider LLM** — Supports **28+ providers**: OpenAI, Anthropic, Gemini, DeepSeek, Groq, Ollama, and many more

## Installation

### Prerequisites

- [Go 1.26+](https://go.dev/dl/)
- An LLM API key (see [Configuration](#configuration))

### Build from source

```bash
git clone https://github.com/WaterEnterprises/WaterWriter.git
cd WaterWriter
go build -o waterwriter.exe .
```

Or build for your platform:

```bash
go build -o waterwriter .
```

## Quick Start

### 1. Configure an LLM provider

Create a `.env` file in the project directory:

```bash
echo "WATERWRITER_LLM_PROVIDER=openai" > .env
echo "WATERWRITER_LLM_API_KEY=sk-your-key-here" >> .env
```

Or use the interactive config tool:

```bash
waterwriter config --select
```

For local models (no API key required):

```bash
export WATERWRITER_LLM_PROVIDER=ollama
waterwriter
```

### 2. Launch the TUI

```bash
waterwriter
```

This opens the home screen where you can create a new book project or resume an existing one.

### 3. Write a book

1. Press **c** to create a new project
2. Answer the interview questions
3. Wait as the AI generates the title, TOC, and subchapters
4. Watch as each subchapter is written in real-time

## CLI Commands

| Command | Description |
|---------|-------------|
| `waterwriter` | Launch the interactive TUI home screen |
| `waterwriter create <name>` | Create a new project and launch the TUI |
| `waterwriter open <name>` | Open and resume an existing project |
| `waterwriter list` | List all projects with phase and title |
| `waterwriter status <name>` | Detailed per-chapter/subchapter progress |
| `waterwriter export <name> [dir]` | Export the finished book to markdown |
| `waterwriter delete <name>` | Remove a project and all its data |
| `waterwriter config` | View or change the LLM provider/model |
| `waterwriter providers` | List every supported LLM provider |
| `waterwriter models` | Query the endpoint for available models |

### Global Flags

- `--db` — Path to the SQLite database (default: `~/.waterwriter/waterwriter.db`)

## Configuration

Water Writer supports **28 LLM providers** out of the box:

| Provider Key | Provider | Requires Key |
|-------------|----------|:------------:|
| `openai` | OpenAI | ✅ |
| `anthropic` | Anthropic Claude | ✅ |
| `gemini` | Google Gemini | ✅ |
| `deepseek` | DeepSeek | ✅ |
| `groq` | Groq | ✅ |
| `openrouter` | OpenRouter | ✅ |
| `together` | Together AI | ✅ |
| `ollama` | Ollama (local) | ❌ |
| `mistral` | Mistral AI | ✅ |
| `perplexity` | Perplexity | ✅ |
| `xai` | xAI Grok | ✅ |
| `custom` | Any OpenAI-compatible endpoint | varies |

...and many more (see `waterwriter providers`).

### Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `WATERWRITER_LLM_PROVIDER` | Provider key | `openai` |
| `WATERWRITER_LLM_API_KEY` | API key | _(required for cloud providers)_ |
| `WATERWRITER_LLM_BASE_URL` | Override base URL | _(provider default)_ |
| `WATERWRITER_LLM_MODEL` | Model name | _(provider default)_ |
| `WATERWRITER_LLM_API_STYLE` | API style: `openai`, `anthropic`, or `gemini` | _(provider default)_ |
| `WATERWRITER_LLM_EXTRA_HEADERS` | Extra HTTP headers for custom endpoints | — |

Settings can also be saved in the database via `waterwriter config` — no need to touch `.env` after the initial setup.

## How It Works

Water Writer guides your book through **6 phases**, with every step persisted to SQLite:

```
QA → Brief → Title/TOC → Subchapters → Writing → Done
```

1. **QA** — The LLM generates interview questions. You answer them in the TUI one at a time.
2. **Brief** — Your Q&A is compiled into a detailed markdown book brief.
3. **Title/TOC** — The LLM generates a title, subtitle, and chapter list.
4. **Subchapters** — Each chapter is split into subchapters (3-6 each).
5. **Writing** — Each subchapter is written with full context from all prior content, streamed live to your terminal.
6. **Done** — Your book is complete! Export it to markdown.

### Continuity

When writing, the LLM receives the full context: the book brief, the title & TOC, **and all previously written subchapters**. This ensures consistent voice, style, and continuity throughout the book. Each subchapter targets 800–1500 words.

## Project Structure

```
WaterWriter/
├── main.go              # Entry point
├── cmd/                 # Cobra CLI commands
│   ├── root.go          # Root command, initApp(), TUI wiring
│   ├── create.go        # Create project
│   ├── open.go          # Resume project
│   ├── list.go          # List projects
│   ├── status.go        # Project status
│   ├── export.go        # Export to markdown
│   ├── delete.go        # Delete project
│   ├── config.go        # LLM configuration
│   ├── providers.go     # List providers
│   └── models.go        # List models
└── internal/
    ├── agent/agent.go   # LLM orchestration & prompt construction
    ├── db/
    │   ├── db.go        # SQLite connection & migrations
    │   ├── models.go    # Data models
    │   └── store.go     # CRUD operations & phase logic
    ├── llm/client.go    # Multi-provider LLM client
    └── tui/app.go       # Bubble Tea TUI
```

## Development

### Prerequisites

- Go 1.26+
- No CGO required (pure-Go SQLite)

### Build & Verify

```bash
go build ./...           # Build all packages
go build -o waterwriter.exe .  # Build binary
go vet ./...             # Static analysis
go test ./internal/...   # Run tests
```

### Architecture Notes

- **No CGO** — The project uses `modernc.org/sqlite` for pure-Go SQLite. Never switch to `mattn/go-sqlite3`.
- **Phase-driven** — The current phase is derived from database state, never from a local file. Resuming a project automatically picks up where you left off.
- **Provider-agnostic LLM client** — The `internal/llm` package supports OpenAI-compatible, Anthropic-native, and Gemini-native wire formats.
- **Streaming writes** — Subchapter content is streamed character-by-character from the LLM and rendered live in the TUI via channels.
- **Windows-friendly** — Handles double-click execution on Windows by re-launching in an interactive console.

## License

MIT
