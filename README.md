# Finsight

[![CI](https://github.com/ray-x/finsight/actions/workflows/ci.yml/badge.svg)](https://github.com/ray-x/finsight/actions/workflows/ci.yml)
[![Release](https://github.com/ray-x/finsight/actions/workflows/release.yml/badge.svg)](https://github.com/ray-x/finsight/actions/workflows/release.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/ray-x/finsight)](https://goreportcard.com/report/github.com/ray-x/finsight)

Finsight is a terminal-native market workstation for tracking symbols, reviewing charts and fundamentals, monitoring earnings/news, managing a private portfolio, and asking AI-driven questions without leaving the shell.

<img width="1200"  alt="image" src="https://gist.github.com/user-attachments/assets/aec9dbbe-198b-4bb6-9829-117fdbbcd1bb.png" />

<img width="1200"  alt="image" src="https://gist.github.com/user-attachments/assets/24be820a-d7d5-436d-8ad4-055c6ae7b834.png" />

Built with Go on top of [Bubble Tea](https://github.com/charmbracelet/bubbletea) and [Lip Gloss](https://github.com/charmbracelet/lipgloss), it combines a fast TUI with local caching, SQLite persistence, multi-provider LLM support, and an agentic prompt flow that can pull quotes, technicals, earnings, news, and portfolio context on demand.

## Application highlights

- **One terminal workflow** — watchlist, detail view, portfolio, heatmap, earnings, and AI prompt window are all part of the same TUI
- **Fast visual analysis** — multi-timeframe charts, sparklines, technical overlays, related-symbol comparison, and heatmaps for watchlists and portfolios
- **Private portfolio tracking** — separate `portfolio.yaml`, quantity + bought-date aware entries, auto-filled cost basis, live P/L, and AI portfolio review
- **AI that understands context** — use slash macros or ask in plain English; Finsight can inject portfolio/watchlist/earnings/news context automatically
- **Event-aware earnings pipeline** — company IR monitoring, EDGAR confirmation, and Yahoo backfill work together for faster earnings coverage
- **Extensible agent stack** — OpenAI-compatible APIs, GitHub Copilot, Vertex, Gemini, Anthropic, MCP servers, ACP agents, and Copilot SDK integration

## Features

### Watchlist and overview

- **Grouped watchlists** — organize symbols into named tabs and switch with `1`-`9`
- **Live market rows** — price, change, change %, volume, market cap, 52-week range, and inline sparklines
- **Multiple sort modes** — symbol, change %, and market cap ascending/descending
- **Extended-hours awareness** — pre/post-market quotes and market-state details when available
- **Watchlist summary** — ask the configured LLM for an at-a-glance summary of the active group with `S`
- **Aggregate heatmap** — open a watchlist heatmap across **all symbols in all groups**, not just the current tab
- **Symbol search** — search Yahoo Finance, preview results, and add them directly from the app

### Detail analysis

- **8 timeframes** — `1D`, `1W`, `1M`, `6M`, `1Y`, `3Y`, `5Y`, `10Y`
- **Multiple chart styles** — `candlestick_dotted`, `candlestick`, `dotted_line`
- **Technical overlays and panels** — moving-average presets, MACD, and related indicator context
- **Related symbols** — auto-discovered peers with compare mode from the detail screen
- **Deep market popup** — market, financials, and holders tabs from a single `m` popup
- **Key stats bar** — valuation, EPS, beta, dividend yield, ranges, and other high-signal snapshot data

### Portfolio and heatmap

- **Private portfolio storage** — positions live separately from shared watchlist config
- **In-app add/edit form** — new positions default to quantity `10` and today's `bought_at` date, both editable before saving
- **Auto cost basis fill** — leave `open_price` empty and Finsight can seed it from today’s open on first fetch
- **Live position metrics** — market value, daily P/L, unrealized P/L, and position weighting
- **Profit display toggle** — switch between `%` and `$` with `%`
- **Portfolio heatmap** — view all holdings as a treemap and toggle tile sizing between **current market value** and **market cap**
- **Portfolio AI review** — ask about a selected position with `a` or trigger a whole-portfolio review with `A`

### AI command window

<img width="1200" alt="image" src="https://gist.github.com/user-attachments/assets/fcf4892e-9493-4134-98c4-e992b1f15124.png" />

- **Natural-language and macro-driven prompts** — use plain English, slash commands, or mix both
- **Context macros** — `/symbol`, `/range`, `/earning`, `/news`, `/portfolio`, `/watchlist`, `/help`
- **Prompt helpers** — `/ask`, `/summarise`, `/analyze`, and `/compare`
- **Autocomplete** — slash-triggered suggestions for macros, symbols, and timeframes
- **Detail-view autofill** — bare `/symbol`, `/earning`, and `/news` expand to the current symbol
- **Agentic execution path** — plain questions can route through a tool-using agent loop instead of macro expansion
- **Optional seven-role analysis** — enable `llm.use_multi_agent: true` for Market / Fundamental / Technical / Risk / Sentiment / Strategy roles plus Portfolio Manager synthesis
- **Markdown output** — scrollable rendered tables, headings, lists, and code blocks inside the TUI

### Data, earnings, and integrations

- **Three-phase earnings ingestion** — company IR detection, EDGAR confirmation, and Yahoo normalization/backfill
- **Local cache + SQLite persistence** — quotes, chart bars, filings, reports, and text cache stored locally
- **Multiple AI providers** — OpenAI-compatible APIs, GitHub Copilot, Vertex AI, Google Gemini, and Anthropic
- **External agent/tool integration** — MCP servers, ACP subprocess agents, native Copilot SDK support, and legacy A2A support
- **Cross-platform** — macOS, Linux, and Windows

## Install

### Pre-built binaries

Download from the [Releases](https://github.com/ray-x/finsight/releases) page:

| Platform       | File                             |
| -------------- | -------------------------------- |
| macOS ARM64    | `finsight-*-darwin-arm64.tar.gz` |
| Linux x86_64   | `finsight-*-linux-amd64.tar.gz`  |
| Windows x86_64 | `finsight-*-windows-amd64.zip`   |

### Go install

```sh
go install github.com/ray-x/finsight@latest
```

### Build from source

```sh
git clone https://github.com/ray-x/finsight.git
cd finsight
go build -o finsight .
```

## Usage

```sh
finsight                    # use default config (~/.config/finsight/config.yaml)
finsight --config path.yaml # use custom config
finsight --version          # print version
```

Finsight uses `./config.yaml` if present; otherwise it falls back to `~/.config/finsight/config.yaml`. Portfolio data is stored separately in `./portfolio.yaml` or `~/.config/finsight/portfolio.yaml`.

### Common workflows

1. **Open the watchlist** and switch groups with `1`-`9`
2. **Press `Enter`** on a symbol for the detail screen
3. **Press `m`** for market / financials / holders tabs
4. **Press `a`** to ask the AI about the current symbol, watchlist, or portfolio
5. **Press `H`** from the watchlist or `h` / `H` from the portfolio for heatmap view
6. **Press `p`** to move between watchlist and portfolio

### AI prompt examples

```text
/analyze /symbol:NVDA in /range:1M and /earning:NVDA
Review my /portfolio for concentration risk
/ask What changed for AMD this week?
/compare /symbol:AAPL vs /symbol:MSFT in /range:1Y
```

## Keybindings

### Watchlist

| Key         | Action                                         |
| ----------- | ---------------------------------------------- |
| `↑` / `k`   | Move up                                        |
| `↓` / `j`   | Move down                                      |
| `←` / `h`   | Previous timeframe                             |
| `→` / `l`   | Next timeframe                                 |
| `Enter`     | Open detail view                               |
| `/`         | Search symbols                                 |
| `s`         | Cycle sort mode                                |
| `d` / `x`   | Remove symbol                                  |
| `1`-`9`     | Switch watchlist group                         |
| `p` / `P`   | Open Portfolio view                            |
| `H`         | Open watchlist heatmap (all groups)            |
| `S`         | Open AI summary for the active watchlist group |
| `a`         | Open AI Command window                         |
| `r` / `R`   | Refresh symbol / refresh all                   |
| `c`         | Cycle chart style                              |
| `M`         | Cycle moving-average preset                    |
| `?`         | Help screen                                    |
| `Esc` / `q` | Quit                                           |

### Detail View

| Key       | Action                                                                 |
| --------- | ---------------------------------------------------------------------- |
| `←` / `→` | Change timeframe                                                       |
| `[` / `]` | Previous / next symbol                                                 |
| `m`       | Open market data popup                                                 |
| `t`       | Toggle technicals panel                                                |
| `c`       | Cycle chart style                                                      |
| `M`       | Cycle moving-average preset                                            |
| `a`       | Open AI Command window (pre-seeded with current symbol + timeframe)    |
| `e`       | Earnings report (requires LLM config)                                  |
| `w` / `W` | Return to watchlist; add transient symbol to watchlist when applicable |
| `r` / `R` | Refresh symbol / refresh all                                           |
| `Esc`     | Back to watchlist                                                      |

### Market Data Popup

| Key                 | Action                                |
| ------------------- | ------------------------------------- |
| `Tab` / `Shift+Tab` | Cycle tabs                            |
| `1` / `2` / `3`     | Jump to Market / Financials / Holders |
| `m` / `Esc`         | Close popup                           |

### AI Command Window

| Key                 | Action                                          |
| ------------------- | ----------------------------------------------- |
| Typing              | Edits prompt; `/` triggers macro autocomplete   |
| `Tab` / `→`         | Accept selected suggestion                      |
| `Enter`             | Send prompt to LLM                              |
| `↑` / `↓`           | Prompt history (input) · scroll output (result) |
| `PgUp` / `PgDn`     | Scroll output 10 lines                          |
| `Ctrl+L`            | Clear buffer                                    |
| `Ctrl+R`            | Regenerate (bypass cache)                       |
| `Ctrl+W`            | Delete previous word · `Ctrl+U` delete to start |
| `Ctrl+A` / `Ctrl+E` | Cursor to start / end                           |
| `Esc`               | Close window                                    |

### Earnings Popup

| Key           | Action                                 |
| ------------- | -------------------------------------- |
| `R` (Shift+R) | Force refresh (clear cache + re-fetch) |
| `↑` / `↓`     | Scroll                                 |
| `Esc`         | Close popup                            |

### Portfolio

| Key       | Action                                                                  |
| --------- | ----------------------------------------------------------------------- |
| `p` / `P` | Toggle between Portfolio and Watchlist views                            |
| `w` / `W` | Switch to Watchlist view                                                |
| `↑` / `↓` | Navigate positions                                                      |
| `Enter`   | Open detail view for selected symbol                                    |
| `/`       | Add position (search then fill position size)                           |
| `e`       | Edit selected position (size / open price)                              |
| `d` / `x` | Remove selected position                                                |
| `%`       | Toggle profit column between `%` and `$`                                |
| `h` / `H` | Open portfolio heatmap                                                  |
| `a`       | Open AI Command window (pre-seeded with `/symbol:<SYM>` + `/portfolio`) |
| `A`       | AI review of the entire portfolio                                       |
| `r`       | Force refresh                                                           |
| `Esc`     | Back to watchlist                                                       |

### Heatmap

| Key         | Action                                                              |
| ----------- | ------------------------------------------------------------------- |
| `↑` / `↓`   | Move selection                                                      |
| `Enter`     | Open detail view for selected symbol                                |
| `t` / `T`   | Cycle heatmap type                                                  |
| `s` / `S`   | Cycle sort mode                                                     |
| `v` / `V`   | Toggle portfolio heatmap sizing between market value and market cap |
| `r` / `R`   | Reload current heatmap                                              |
| `w` / `W`   | Jump to Watchlist                                                   |
| `p` / `P`   | Jump to Portfolio                                                   |
| `Esc` / `q` | Leave heatmap                                                       |

## Configuration

Config file: `~/.config/finsight/config.yaml` (or `config.yaml` in the current directory).

```yaml
refresh_interval: 900       # auto-refresh interval in seconds
chart_range: 1d             # default chart range
chart_interval: 1h          # default chart interval
chart_style: candlestick_dotted  # candlestick_dotted | candlestick | dotted_line
colorscheme: default        # default | tokyonight | catppuccin | dracula | nord | gruvbox | solarized | ansi

llm:
  provider: copilot         # openai (default) | copilot | vertex | gemini | anthropic
  model: gpt-4o             # provider-specific model id
  # endpoint is only required for provider: openai; copilot auto-discovers it
  # endpoint: https://api.openai.com/v1
  # api_key_env: MY_TOKEN   # optional: name a custom env var to read the secret from
  context_tokens: 65536
  use_multi_agent: false    # true = 7-role analysis pipeline for plain-English AI prompts
  # vertex-only:
  # project: my-gcp-project
  # location: global        # or us-central1, etc.

# Optional: earnings source strategy and polling cadence.
earnings:
  source_priority: [company_ir, edgar, yahoo]  # fetch/reconcile in this order
  ir_feed_registry:                             # optional symbol -> IR RSS/Atom feed URL
    AAPL: https://investor.apple.com/rss/news.rss
    NVDA: https://investor.nvidia.com/rss/news.xml
  high_freq_polling_seconds: 60                # release windows
  normal_polling_seconds: 300                  # normal market hours
  off_hours_polling_seconds: 900               # outside market hours
  window_before_minutes: 120                   # high-frequency mode before release
  window_after_minutes: 120                    # high-frequency mode after release
  edgar_confirm_seconds: 120                   # SEC confirmation cadence
  edgar_confirm_duration_mins: 30              # SEC confirmation duration
  yahoo_backfill_seconds: 900                  # Yahoo normalization cadence
  yahoo_backfill_duration_mins: 240            # Yahoo normalization duration

#### Earnings Ingestion Strategy

Finsight detects earnings announcements using a **three-phase strategy**:

**Phase 1: IR Feed Polling** (fastest)
- Monitors company investor relations RSS/Atom feeds registered in `ir_feed_registry`
- Polls at high frequency during configured release windows (configurable `window_before_minutes` / `window_after_minutes` around expected announcements)
- Stores detected events as `company_ir_release` events in the database with fingerprint-based deduplication
- Conditional HTTP requests (ETag, Last-Modified) minimize bandwidth
- Events are available for LLM analysis immediately upon detection

**Phase 2: EDGAR Confirmation** (official)
- Background worker spawned when an IR event is detected
- Polls the SEC EDGAR API at `edgar_confirm_seconds` interval for `edgar_confirm_duration_mins` minutes
- Searches for matching 8-K, 10-Q, or 10-K filings to confirm the earnings announcement
- Stores confirmed events as `edgar_confirmation` records, marking them as officially filed
- Provides legal/official source of truth alongside IR feed data

**Phase 3: Yahoo Backfill** (normalization)
- Background worker spawned concurrently with EDGAR confirmation
- Polls Yahoo Finance at `yahoo_backfill_seconds` interval for `yahoo_backfill_duration_mins` minutes
- Extracts EPS actual, estimate, revenue, guidance if available
- Normalizes timestamps and data quality, stores as `yahoo_backfill` records
- Provides fallback data when IR or EDGAR data is incomplete

**Cache Freshness**: Earnings report cache TTL is dynamic:
- High-frequency window (before/after expected release): 60 seconds
- Normal market hours: 300 seconds
- Off-hours: 900 seconds

This strategy ensures you see earnings the **moment** they're announced on company IR sites, with official EDGAR filings and Yahoo normalization running in the background.

# Optional: investor profile — injected into every AI analysis so
# suggestions match your style. All fields are optional.
investor:
  strategies: [buy-and-hold, dca, growth]   # passive, active, value, income, dividend, momentum, …
  risk: balanced                             # conservative | balanced | aggressive
  horizon: 10+ years
  goals: [long-term capital appreciation, diversified across sectors]
  # notes: prefer ETFs over individual stocks

watchlists:
  - name: Index & ETF
    symbols:
      - symbol: "^IXIC"
        name: NASDAQ Composite
  - name: Tech
    symbols:
      - symbol: NVDA
        name: NVIDIA Corporation
      - symbol: AAPL
        name: Apple Inc.
```

### LLM / AI

Finsight supports five LLM backends. Select one with `llm.provider`:

| Provider             | Description                                                                                                  | Required config                | Credential (env)                                                                                                                                                                                                                 |
| -------------------- | ------------------------------------------------------------------------------------------------------------ | ------------------------------ | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `openai` _(default)_ | Any OpenAI-compatible `/chat/completions` API — OpenAI, Ollama, llama.cpp, vLLM, LM Studio, OpenRouter, etc. | `endpoint`, `model`            | `OPENAI_API_KEY` (or none for local servers)                                                                                                                                                                                     |
| `copilot`            | GitHub Copilot chat. Works with personal, Business, and Enterprise subscriptions.                            | `model`                        | `GITHUB_COPILOT_TOKEN`, `GH_TOKEN`, or `GITHUB_TOKEN`. If none are set, Finsight reads the OAuth token from `~/.config/github-copilot/apps.json` (written by the official VS Code / Neovim Copilot extensions when you sign in). |
| `vertex`             | Google Cloud Vertex AI Model Garden (Gemini family).                                                         | `model`, `project`, `location` | `GOOGLE_ACCESS_TOKEN` — usually `export GOOGLE_ACCESS_TOKEN=(gcloud auth print-access-token)`                                                                                                                                    |
| `gemini`             | Google AI Studio / Gemini API.                                                                               | `model`                        | `GEMINI_API_KEY` or `GOOGLE_API_KEY`                                                                                                                                                                                             |
| `anthropic`          | Anthropic Claude via Messages API.                                                                           | `model`                        | `ANTHROPIC_API_KEY`                                                                                                                                                                                                              |

**Secrets are never read from `config.yaml`.** Credentials are loaded from environment variables at startup. Lookup order (first non-empty wins):

1. `$<api_key_env>` — if you named a custom env var in `llm.api_key_env`
2. `FINSIGHT_LLM_API_KEY` — generic override for any provider
3. Per-provider defaults (listed in the table above)

**Copilot auto-discovery:** for `provider: copilot`, Finsight exchanges your OAuth token for a short-lived chat token and reads the chat host from the response's `endpoints.api` field — so personal (`api.githubcopilot.com`), Business (`api.business.githubcopilot.com`), and Enterprise tenants all work without setting `endpoint:` manually. Override with `endpoint:` only if you route through a corporate proxy.

**Copilot without signing in separately:** if you've already signed in to Copilot in VS Code or Neovim (`copilot.lua` / `copilot.vim`), Finsight picks up the OAuth token automatically from the shared `~/.config/github-copilot/apps.json` file. No extra setup.

**Multi-agent mode:** set `llm.use_multi_agent: true` to route plain-English AI questions through the concurrent seven-role analysis path documented in [`docs/multi_agent_orchestration.md`](docs/multi_agent_orchestration.md).

### AI Command Window

Press `a` from the watchlist, detail, or portfolio view to open an interactive prompt. Type plain English and embed **slash macros** to inject structured context into the LLM request. Output is rendered as markdown (headings, bullets, **tables**, code blocks) and is scrollable.

| Macro                                    | Effect                                                                                    |
| ---------------------------------------- | ----------------------------------------------------------------------------------------- |
| `/symbol:SYM`                            | Quote snapshot: price, day range, 52W range, valuation, volume, market cap                |
| `/range:1D\|1W\|1M\|6M\|1Y\|3Y\|5Y\|10Y` | Timeframe hint; applies to the chart summary in a following `/symbol`                     |
| `/earning:SYM`                           | Latest financials, margins, analyst targets & recommendation (auto-fetch if missing)      |
| `/news:SYM`                              | Recent headlines (~2 week window, merged from Yahoo Finance + Google News RSS, cached 1h) |
| `/portfolio`                             | Your whole portfolio table (weights, P/L, concentration)                                  |
| `/watchlist`                             | Current watchlist group as a markdown table                                               |
| `/help`                                  | Built-in cheatsheet (no LLM call, no token usage)                                         |

**Autocomplete:** typing `/` opens a dropdown of macro names; `/symbol:`, `/earning:`, and `/news:` suggest tickers from your watchlist + portfolio; `/range:` suggests timeframes. Press `Tab` to accept. On a detail page, picking `/symbol`, `/earning`, or `/news` from the dropdown attaches the current ticker automatically.

**Plain-English mode:** prompts without macros can use the tool-calling agent path automatically. Use `/ask` if you want to make that intent explicit.

**Example prompts:**

```text
# 1. Quick one-liner on the currently-focused symbol (just press `a`)
/analyze /symbol:NVDA in /range:1M and /earning:NVDA.
Include recent /news:NVDA to explain short-term moves.

# 2. Explain a price move with news + chart context
/explain why /symbol:TSLA dropped this week.
Use /range:1W and /news:TSLA.

# 3. Compare two names head-to-head
/compare /symbol:AAPL vs /symbol:MSFT in /range:1Y.
Use /earning:AAPL and /earning:MSFT for valuation + margins.

# 4. Portfolio risk check
Review my /portfolio for concentration risk and suggest rebalancing.

# 5. Event-driven question
Did anything material happen to /symbol:NVDA today?
Pull /news:NVDA and flag filings / guidance changes.

# 6. Cheap iteration — on a detail page, type just:
/symbol and /earning and /news
# (all three bare macros expand to the current ticker)

# 7. Valuation sanity check
/analyze /symbol:GOOGL /earning:GOOGL.
Is it fairly valued vs the analyst target range?
```

**Follow-ups:** after a result renders, just start typing — the window flips back to input mode automatically so `↑` / `↓` navigate your prompt history instead of scrolling the last answer.

The old one-shot AI popup is replaced by this unified command window. If `llm` is unconfigured, the `a` key still opens the window but submitting shows an error.

### Watchlist Groups

Organize your watchlist into named groups. Press `1`–`9` on the watchlist screen to switch between groups. A tab bar above the list shows all groups with the active one highlighted.

```yaml
watchlists:
  - name: Index & ETF
    symbols:
      - symbol: "^IXIC"
        name: NASDAQ Composite
  - name: Tech
    symbols:
      - symbol: NVDA
        name: NVIDIA Corporation
      - symbol: AAPL
        name: Apple Inc.
  - name: Asia
    symbols:
      - symbol: 005930.KS
        name: Samsung
```

If you use the old flat `watchlist` format, it is automatically migrated to a single "Default" group on first load.

### Portfolio

Portfolio positions are private and stored separately from your watchlist to keep them out of any shared `config.yaml`.

**Storage precedence (first match wins):**

1. `./portfolio.yaml` in the working directory (handy for per-repo portfolios)
2. `~/.config/finsight/portfolio.yaml` (default, written with `0600` perms)
3. `portfolio:` block inside `config.yaml` (fallback only; the first in-app add auto-promotes to a dedicated file)

**Schema:**

```yaml
portfolio:
  - symbol: NVDA
    position: 25 # shares or contracts (fractional OK)
    bought_at: 2026-04-30 # optional; defaults to the current date in-app
    open_price: 712.40 # optional; blank = auto-fill from today's open
    note: long-term
  - symbol: AAPL
    position: 10 # in-app add defaults to 10
    bought_at: 2026-04-30
    # open_price omitted — filled on first fetch
```

**AI advisor:** requires an `llm` section (see the [LLM / AI](#llm--ai) section). `a` opens the AI Command window pre-seeded with `/analyze /symbol:<SYM> using my /portfolio`; edit freely before sending, or accept to send. `A` triggers a one-shot holistic review of the whole portfolio.

Tip: add `portfolio.yaml` and `~/.config/finsight/portfolio.yaml` to your `.gitignore` if you keep a repo-local copy.

### Color Schemes

| Name         | Description                                   |
| ------------ | --------------------------------------------- |
| `default`    | Original palette (TokyoNight-inspired)        |
| `tokyonight` | Faithful TokyoNight colors                    |
| `catppuccin` | Catppuccin Mocha                              |
| `dracula`    | Dracula                                       |
| `nord`       | Nord                                          |
| `gruvbox`    | Gruvbox Dark                                  |
| `solarized`  | Solarized Dark                                |
| `ansi`       | 16-color ANSI palette (works in any terminal) |

### Chart styles

| Style                | Description                                   |
| -------------------- | --------------------------------------------- |
| `candlestick_dotted` | Braille-dot OHLC candles with wicks (default) |
| `candlestick`        | Block-character candlesticks                  |
| `dotted_line`        | Braille sparkline (line only)                 |

## Data Source

Market data is fetched from [Yahoo Finance](https://finance.yahoo.com). Data includes real-time quotes, historical charts, financial statements, analyst recommendations, institutional holdings, and related symbol suggestions.

## License

Apache NON-AI License 2.0 — see [LICENSE](LICENSE) for details. This software may not be used as training data for AI/ML models without explicit written permission.

## Disclaimer

This is a personal project for informational and educational purposes only. It is **not** financial advice. The author is not a licensed financial advisor and assumes no responsibility for any investment decisions made based on information provided by this tool.

Stock market investments carry inherent risks, including the potential loss of principal. Past performance does not guarantee future results. Always do your own research and consult a qualified financial professional before making any investment decisions.

The AI-generated analysis features rely on third-party language models and SEC filings. These outputs may contain inaccuracies, hallucinations, or outdated information and should never be used as the sole basis for financial decisions.

**Use at your own risk.**
