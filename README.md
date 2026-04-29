# Financial market insight with AI

[![CI](https://github.com/ray-x/finsight/actions/workflows/ci.yml/badge.svg)](https://github.com/ray-x/finsight/actions/workflows/ci.yml)
[![Release](https://github.com/ray-x/finsight/actions/workflows/release.yml/badge.svg)](https://github.com/ray-x/finsight/actions/workflows/release.yml)
[![Go Report Card](https://goreportcard.com/badge/github.com/ray-x/finsight)](https://goreportcard.com/report/github.com/ray-x/finsight)

**Elevator pitch:** Track, analyze, and act faster: Finsight combines real-time watchlists, multi-timeframe charting, financial and holder data, and AI-powered insights without leaving your terminal.
It is a terminal-based stock market watchlist and charting tool inspired by [btop+](https://github.com/aristocratos/btop) and Google Finance. Built with Go using [Bubble Tea](https://github.com/charmbracelet/bubbletea) and [Lip Gloss](https://github.com/charmbracelet/lipgloss).

## Features

### Watchlist
- **Real-time quotes** — Price, change, change%, volume, market cap, 52-week range
- **Inline sparklines** — Color-coded trend charts in every row
- **Watchlist groups** — Organize symbols into named groups (e.g., "Tech", "Index & ETF", "Asia"); switch with `1`-`9`
- **Sort modes** — By symbol, change%, or market cap (asc/desc)
- **Pre/post market data** — Extended hours pricing when markets are closed
- **Tab bar** — `Watchlist | Portfolio` switch on line 1 (with `w`/`p` shortcuts), group tabs on line 2

### Detail View
- **Candlestick charts** — Braille-dot OHLC candlesticks with wicks, volume bars, X/Y axis labels
- **Multiple chart styles** — `candlestick_dotted`, `candlestick`, `dotted_line`
- **8 timeframes** — 1D, 1W, 1M, 6M, 1Y, 3Y, 5Y, 10Y
- **Related symbols** — Auto-discovered similar stocks with synced sparkline charts
- **Key stats bar** — PE, Forward PE, PEG, EPS, Beta, 52-week range, dividend yield

### Market Data Popup (press `m`)
- **Market tab** — Bid/ask, volume, fundamentals, market state
- **Financials tab** — Revenue vs Earnings chart, key metrics, balance sheet, valuation (EV, P/S, EV/EBITDA), EPS trend, analyst price targets, recommendation trend chart
- **Holders tab** — Institutional ownership breakdown, top holders, insider holdings

### Portfolio
- **Private positions** — Track shares, entry price, live market value, daily and unrealized P/L per position; press `p` from the watchlist to open.
- **Auto cost basis** — Leave the open price blank and Finsight fills it from today's open on the first fetch and persists it.
- **Profit toggle** — Swap the profit column between `%` and absolute `$` with the `%` key.
- **AI advisor** — `a` opens the AI Command window pre-seeded with the selected position + `/portfolio` context; `A` starts a holistic portfolio review.
- **Private by default** — Positions live in `~/.config/finsight/portfolio.yaml` (chmod `0600`) separate from your shared `config.yaml`.

### AI Command Window (press `a` from any view)
- **Interactive prompt** with macro expansion — type plain English plus slash macros
- **Macros**: `/symbol:SYM`, `/range:1D…5Y`, `/earning:SYM`, `/news:SYM`, `/portfolio`, `/watchlist`, `/help`
- **Autocomplete dropdown** — `/` for macros, `/symbol:` / `/earning:` / `/news:` for tickers from your watchlist+portfolio, `/range:` for timeframes
- **Detail-view auto-fill** — on a symbol page, bare `/symbol`, `/earning`, and `/news` expand to the current ticker automatically
- **Auto-fetch** — missing earnings or news data is fetched on demand before the LLM is called; results are cached (24h / 1h)
- **Prompt history** (↑/↓), scrollable markdown output with **table rendering**
- **Multi-provider LLM** — OpenAI-compatible endpoints, **GitHub Copilot** (personal / business / enterprise, with auto-discovered endpoint), and **Google Vertex AI** (Gemini via Model Garden)

### Other
- **Symbol search** — Search and add symbols from Yahoo Finance with preview sparklines
- **Local cache** — 15-min TTL for quotes/charts, 24-hour TTL for financials/holders
- **YAML config** — Persistent watchlist and preferences
- **Cross-platform** — macOS (ARM64), Linux (x86_64), Windows (x86_64)

## Install

### Pre-built binaries

Download from the [Releases](https://github.com/ray-x/finsight/releases) page:

| Platform | File |
|----------|------|
| macOS ARM64 | `finsight-*-darwin-arm64.tar.gz` |
| Linux x86_64 | `finsight-*-linux-amd64.tar.gz` |
| Windows x86_64 | `finsight-*-windows-amd64.zip` |

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

## Keybindings

### Watchlist

| Key | Action |
|-----|--------|
| `↑` / `k` | Move up |
| `↓` / `j` | Move down |
| `←` / `h` | Previous timeframe |
| `→` / `l` | Next timeframe |
| `Enter` | Open detail view |
| `/` | Search symbols |
| `s` | Cycle sort mode |
| `d` / `x` | Remove symbol |
| `1`-`9` | Switch watchlist group |
| `p` / `P` | Open Portfolio view |
| `a` | Open AI Command window |
| `r` | Force refresh |
| `?` | Help screen |
| `Esc` / `q` | Quit |

### Detail View

| Key | Action |
|-----|--------|
| `←` / `→` | Change timeframe |
| `[` / `]` | Previous / next symbol |
| `m` | Open market data popup |
| `a` | Open AI Command window (pre-seeded with current symbol + timeframe) |
| `e` | Earnings report (requires LLM config) |
| `Esc` | Back to watchlist |

### Market Data Popup

| Key | Action |
|-----|--------|
| `Tab` / `Shift+Tab` | Cycle tabs |
| `1` / `2` / `3` | Jump to Market / Financials / Holders |
| `m` / `Esc` | Close popup |

### AI Command Window

| Key | Action |
|-----|--------|
| Typing | Edits prompt; `/` triggers macro autocomplete |
| `Tab` / `→` | Accept selected suggestion |
| `Enter` | Send prompt to LLM |
| `↑` / `↓` | Prompt history (input) · scroll output (result) |
| `PgUp` / `PgDn` | Scroll output 10 lines |
| `Ctrl+L` | Clear buffer |
| `Ctrl+R` | Regenerate (bypass cache) |
| `Ctrl+W` | Delete previous word · `Ctrl+U` delete to start |
| `Ctrl+A` / `Ctrl+E` | Cursor to start / end |
| `Esc` | Close window |

### Earnings Popup

| Key | Action |
|-----|--------|
| `R` (Shift+R) | Force refresh (clear cache + re-fetch) |
| `↑` / `↓` | Scroll |
| `Esc` | Close popup |

### Portfolio

| Key | Action |
|-----|--------|
| `p` / `P` | Toggle between Portfolio and Watchlist views |
| `w` / `W` | Switch to Watchlist view |
| `↑` / `↓` | Navigate positions |
| `Enter` | Open detail view for selected symbol |
| `/` | Add position (search then fill position size) |
| `e` | Edit selected position (size / open price) |
| `d` / `x` | Remove selected position |
| `%` | Toggle profit column between `%` and `$` |
| `a` | Open AI Command window (pre-seeded with `/symbol:<SYM>` + `/portfolio`) |
| `A` | AI review of the entire portfolio |
| `r` | Force refresh |
| `Esc` | Back to watchlist |

## Configuration

Config file: `~/.config/finsight/config.yaml` (or `config.yaml` in the current directory).

```yaml
refresh_interval: 900       # auto-refresh interval in seconds
chart_range: 1d             # default chart range
chart_interval: 1h          # default chart interval
chart_style: candlestick_dotted  # candlestick_dotted | candlestick | dotted_line
colorscheme: default        # default | tokyonight | catppuccin | dracula | nord | gruvbox | solarized | ansi

llm:
  provider: copilot         # openai (default) | copilot | vertex
  model: gpt-4o             # provider-specific model id
  # endpoint is only required for provider: openai; copilot auto-discovers it
  # endpoint: https://api.openai.com/v1
  # api_key_env: MY_TOKEN   # optional: name a custom env var to read the secret from
  context_tokens: 65536
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

Finsight supports three backends. Select one with `llm.provider`:

| Provider | Description | Required config | Credential (env) |
|---|---|---|---|
| `openai` *(default)* | Any OpenAI-compatible `/chat/completions` API — OpenAI, Ollama, llama.cpp, vLLM, LM Studio, OpenRouter, etc. | `endpoint`, `model` | `OPENAI_API_KEY` (or none for local servers) |
| `copilot` | GitHub Copilot chat. Works with personal, Business, and Enterprise subscriptions. | `model` | `GITHUB_COPILOT_TOKEN`, `GH_TOKEN`, or `GITHUB_TOKEN`. If none are set, Finsight reads the OAuth token from `~/.config/github-copilot/apps.json` (written by the official VS Code / Neovim Copilot extensions when you sign in). |
| `vertex` | Google Cloud Vertex AI Model Garden (Gemini family). | `model`, `project`, `location` | `GOOGLE_ACCESS_TOKEN` — usually `export GOOGLE_ACCESS_TOKEN=(gcloud auth print-access-token)` |

**Secrets are never read from `config.yaml`.** Credentials are loaded from environment variables at startup. Lookup order (first non-empty wins):

1. `$<api_key_env>` — if you named a custom env var in `llm.api_key_env`
2. `FINSIGHT_LLM_API_KEY` — generic override for any provider
3. Per-provider defaults (listed in the table above)

**Copilot auto-discovery:** for `provider: copilot`, Finsight exchanges your OAuth token for a short-lived chat token and reads the chat host from the response's `endpoints.api` field — so personal (`api.githubcopilot.com`), Business (`api.business.githubcopilot.com`), and Enterprise tenants all work without setting `endpoint:` manually. Override with `endpoint:` only if you route through a corporate proxy.

**Copilot without signing in separately:** if you've already signed in to Copilot in VS Code or Neovim (`copilot.lua` / `copilot.vim`), Finsight picks up the OAuth token automatically from the shared `~/.config/github-copilot/apps.json` file. No extra setup.

### AI Command Window

Press `a` from the watchlist, detail, or portfolio view to open an interactive prompt. Type plain English and embed **slash macros** to inject structured context into the LLM request. Output is rendered as markdown (headings, bullets, **tables**, code blocks) and is scrollable.

| Macro | Effect |
|---|---|
| `/symbol:SYM` | Quote snapshot: price, day range, 52W range, valuation, volume, market cap |
| `/range:1D\|1W\|1M\|6M\|1Y\|3Y\|5Y\|10Y` | Timeframe hint; applies to the chart summary in a following `/symbol` |
| `/earning:SYM` | Latest financials, margins, analyst targets & recommendation (auto-fetch if missing) |
| `/news:SYM` | Recent headlines (~2 week window, merged from Yahoo Finance + Google News RSS, cached 1h) |
| `/portfolio` | Your whole portfolio table (weights, P/L, concentration) |
| `/watchlist` | Current watchlist group as a markdown table |
| `/help` | Built-in cheatsheet (no LLM call, no token usage) |

**Autocomplete:** typing `/` opens a dropdown of macro names; `/symbol:`, `/earning:`, and `/news:` suggest tickers from your watchlist + portfolio; `/range:` suggests timeframes. Press `Tab` to accept. On a detail page, picking `/symbol`, `/earning`, or `/news` from the dropdown attaches the current ticker automatically.

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
    position: 25        # shares or contracts (fractional OK)
    open_price: 712.40  # optional; blank = auto-fill from today's open
    note: long-term
  - symbol: AAPL
    position: 40
    # open_price omitted — filled on first fetch
```

**AI advisor:** requires an `llm` section (see the [LLM / AI](#llm--ai) section). `a` opens the AI Command window pre-seeded with `/analyze /symbol:<SYM> using my /portfolio`; edit freely before sending, or accept to send. `A` triggers a one-shot holistic review of the whole portfolio.

Tip: add `portfolio.yaml` and `~/.config/finsight/portfolio.yaml` to your `.gitignore` if you keep a repo-local copy.

### Color Schemes

| Name | Description |
|------|-------------|
| `default` | Original palette (TokyoNight-inspired) |
| `tokyonight` | Faithful TokyoNight colors |
| `catppuccin` | Catppuccin Mocha |
| `dracula` | Dracula |
| `nord` | Nord |
| `gruvbox` | Gruvbox Dark |
| `solarized` | Solarized Dark |
| `ansi` | 16-color ANSI palette (works in any terminal) |

### Chart styles

| Style | Description |
|-------|-------------|
| `candlestick_dotted` | Braille-dot OHLC candles with wicks (default) |
| `candlestick` | Block-character candlesticks |
| `dotted_line` | Braille sparkline (line only) |

## Data Source

Market data is fetched from [Yahoo Finance](https://finance.yahoo.com). Data includes real-time quotes, historical charts, financial statements, analyst recommendations, institutional holdings, and related symbol suggestions.

## License

Apache NON-AI License 2.0 — see [LICENSE](LICENSE) for details. This software may not be used as training data for AI/ML models without explicit written permission.

## Disclaimer

This is a personal project for informational and educational purposes only. It is **not** financial advice. The author is not a licensed financial advisor and assumes no responsibility for any investment decisions made based on information provided by this tool.

Stock market investments carry inherent risks, including the potential loss of principal. Past performance does not guarantee future results. Always do your own research and consult a qualified financial professional before making any investment decisions.

The AI-generated analysis features rely on third-party language models and SEC filings. These outputs may contain inaccuracies, hallucinations, or outdated information and should never be used as the sole basis for financial decisions.

**Use at your own risk.**
