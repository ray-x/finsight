# Finsight — Copilot Instructions

## Build, Test & Lint

```sh
# Build
go build -o finsight .
go build -v ./...

# Vet
go vet ./...

# Unit tests (skips integration tests, enables race detector)
go test -short -race ./...

# Single package / single test
go test -run TestCacheQuotesRoundTrip ./internal/cache/
go test -run TestWatchlistNavigation ./internal/ui/

# Integration tests (hit real Yahoo Finance; requires network)
go test -v -run TestIntegration -count=1 ./internal/yahoo/
```

Integration tests are tagged with `-run TestIntegration` and are excluded by `-short`. Do not run them in CI unless you intend to make live network calls.

## Architecture

Finsight is a terminal-based stock market dashboard and AI analysis tool. The entry point is `main.go` → `internal/ui.NewModel`, which owns a single **Bubble Tea** `Model` for the entire TUI.

### Package map

| Package | Role |
|---------|------|
| `internal/ui` | All TUI views: watchlist, detail, portfolio, heatmap, search, AI command window. Single `Model` struct; Bubble Tea elm loop (`Init`/`Update`/`View`). |
| `internal/llm` | Multi-provider LLM client: OpenAI-compatible, GitHub Copilot, Vertex AI, Gemini, Anthropic. Copilot auto-discovers its chat endpoint via token exchange. |
| `internal/agent` | Tool-calling loop on top of `llm`. Dispatches tool calls in parallel, feeds results back until the model produces a final answer or hits a step budget. |
| `internal/yahoo` | Yahoo Finance HTTP client — quotes, chart bars, financials, holders, related symbols, news. |
| `internal/cache` | Two-layer cache: in-memory TTL store + SQLite-backed (`cache_sqlite.go`). Quotes/charts: 15 min; financials/holders: 24 h; news: 1 h. |
| `internal/db` | SQLite database in WAL mode (`~/.config/finsight/finsight.db`). Stores OHLCV bars, earnings events, LLM reports, SEC documents. Schema version tracked for migrations. |
| `internal/earnings` | Three-phase earnings ingestion: IR feed polling → EDGAR SEC confirmation → Yahoo backfill, each managed by background workers via `Orchestrator`. |
| `internal/edgar` | SEC EDGAR 8-K/10-Q/10-K filing client. |
| `internal/mcp` | MCP (Model Context Protocol) client. Supports stdio subprocess and SSE HTTP transports. Converts MCP tool definitions to `llm.ToolSpec`. |
| `internal/a2a` | Google Agent-to-Agent (A2A) protocol client. Remote agent capabilities exposed as `llm.ToolSpec` for transparent delegation. |
| `internal/acp` | ACP (Agent Communication Protocol) client. |
| `internal/chart` | Braille-dot OHLC candlestick and sparkline renderer. |
| `internal/config` | YAML config loading (`~/.config/finsight/config.yaml`). |
| `internal/portfolio` | Portfolio YAML load/save (`~/.config/finsight/portfolio.yaml`, `0600` perms). |
| `internal/news` | News aggregation (Yahoo Finance + Google News RSS). |
| `internal/logger` | Internal structured logger. Use this, not `log` from the standard library. |

### Data flow

```
main.go
  └─ ui.NewModel(cfg, cfgPath)          ← creates the Bubble Tea model
       ├─ yahoo.Client                  ← market data
       ├─ cache.Cacher                  ← in-memory + SQLite
       ├─ db.DB                         ← persistent store
       ├─ llm.Client                    ← LLM backend
       ├─ agent.Run(tools...)           ← agentic tool-call loop
       ├─ earnings.Orchestrator         ← background IR/EDGAR/Yahoo workers
       └─ mcp.Client / a2a.Client       ← external agent/tool integrations
```

## Key Conventions

### Bubble Tea patterns
- The `Model` is the single source of truth. `Init()` fires startup `tea.Cmd`s; `Update(tea.Msg)` handles every message and returns a new model + commands; `View()` renders to a string.
- **All async work (HTTP fetches, LLM calls) is dispatched as `tea.Cmd` functions**, not raw goroutines, so they deliver results back as typed `tea.Msg` values (e.g. `quotesMsg`, `chartMsg`, `aiResultMsg`).
- Handler functions (`handleKey`, `handleWatchlistKey`, `handleDetailKey`, etc.) return `(tea.Model, tea.Cmd)` and are called from `Update`.

### Cache access pattern
Always check the cache before fetching. If fresh data is unavailable, fall back to stale (use `GetQuotesStale` / `GetChartStale` etc.) while dispatching a background refresh. This keeps the UI responsive.

### JSON encoding
Use `encoding/json/v2` (not `encoding/json`). This is already a project-wide convention.

### Credentials / secrets
Secrets are never in `config.yaml`. All credentials are read from environment variables at startup. Lookup order: `$<api_key_env>` → `FINSIGHT_LLM_API_KEY` → provider defaults (`OPENAI_API_KEY`, `GITHUB_COPILOT_TOKEN`/`GH_TOKEN`, `GOOGLE_ACCESS_TOKEN`).

### Integration tests
- Files named `*_integration_test.go` or test functions prefixed `TestIntegration` target live external APIs.
- Gate new integration tests with `testing.Short()` skip or a `-run TestIntegration` pattern so they never run in the normal `go test -short` suite.

### LLM tool registration
When adding a new agent tool (in `internal/agent/tools_*.go`), define a `Tool` with a `llm.ToolSpec` (name, description, JSON schema for `parameters`) and a `Handler func(ctx, args) (string, error)`. The handler should summarise or truncate output because the string goes directly back into the model context.

### MCP / A2A tools
Both `mcp.Client` and `a2a.Client` convert external capabilities to `llm.ToolSpec` so the agent loop treats them identically to first-party tools. Register them alongside internal tools when building the tool set for `agent.Run`.

### Config file precedence
1. `--config` CLI flag
2. `./config.yaml` in the working directory
3. `~/.config/finsight/config.yaml`

Portfolio file precedence: `./portfolio.yaml` → `~/.config/finsight/portfolio.yaml` → `portfolio:` block in `config.yaml`.
