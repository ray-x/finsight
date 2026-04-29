package config

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/ray-x/finsight/internal/portfolio"
	"gopkg.in/yaml.v3"
)

type WatchItem struct {
	Symbol string `yaml:"symbol"`
	Name   string `yaml:"name"`
}

// WatchlistGroup is a named group of watch items.
type WatchlistGroup struct {
	Name    string      `yaml:"name"`
	Symbols []WatchItem `yaml:"symbols"`
}

type LLMConfig struct {
	// Provider selects the backend protocol. Supported values:
	//   "openai"    — OpenAI-compatible /chat/completions (default; works with
	//                 OpenAI, Ollama, llama.cpp, vLLM, LM Studio, OpenRouter…)
	//   "copilot"   — GitHub Copilot chat (api.githubcopilot.com). The OAuth
	//                 token is read from env or ~/.config/github-copilot/apps.json.
	//   "vertex"    — Google Cloud Vertex AI Model Garden (Gemini family).
	//                 Requires project; access token from env.
	//   "gemini"    — Google AI Studio (generativelanguage.googleapis.com).
	//                 Uses GEMINI_API_KEY; simpler than vertex.
	//   "anthropic" — Anthropic Claude (api.anthropic.com).
	//                 Uses ANTHROPIC_API_KEY.
	//
	// Secrets are NEVER read from this file. The api_key is loaded from
	// environment variables at runtime; see Load() for the lookup order.
	Provider      string `yaml:"provider"`
	Endpoint      string `yaml:"endpoint"`        // OpenAI-compat / Copilot base URL (ignored for vertex)
	Model         string `yaml:"model"`           // Model id (e.g. gpt-4o, gemini-2.0-flash, gemma3:e4b)
	APIKeyEnv     string `yaml:"api_key_env"`     // Env var name to read the credential from (preferred over the default lookup)
	APIKey        string `yaml:"-"`               // populated from env at load time; never serialized
	Project       string `yaml:"project"`         // Vertex: GCP project id
	Location      string `yaml:"location"`        // Vertex: region (default "global")
	ContextTokens int    `yaml:"context_tokens"`  // Model context window size in tokens (default 32768)
	UseMultiAgent bool   `yaml:"use_multi_agent"` // Run 7 concurrent role agents (true) or 1 single-agent (false, default)
}

type Config struct {
	RefreshInterval int               `yaml:"refresh_interval"`
	ChartRange      string            `yaml:"chart_range"`
	ChartInterval   string            `yaml:"chart_interval"`
	ChartStyle      string            `yaml:"chart_style"`
	ColorScheme     string            `yaml:"colorscheme"`
	EdgarEmail      string            `yaml:"edgar_email"`
	LogFile         string            `yaml:"log_file"`
	Earnings        EarningsConfig    `yaml:"earnings,omitempty"`
	LLM             LLMConfig         `yaml:"llm"`
	MCP             []MCPServerConfig `yaml:"mcp,omitempty"`
	ACP             []ACPAgentConfig  `yaml:"acp,omitempty"`
	CopilotSDK      *CopilotSDKConfig `yaml:"copilot_sdk,omitempty"`
	A2A             []A2AAgentConfig  `yaml:"a2a,omitempty"` // Deprecated: use ACP instead
	Investor        InvestorProfile   `yaml:"investor,omitempty"`
	Watchlists      []WatchlistGroup  `yaml:"watchlists"`
	// Deprecated: use Watchlists instead. Kept for backward compatibility.
	Watchlist []WatchItem `yaml:"watchlist,omitempty"`
	// Portfolio holds private position data. Prefer a separate
	// ~/.config/finsight/portfolio.yaml file; this field is a fallback
	// for users who want a single config file.
	Portfolio []portfolio.Position `yaml:"portfolio,omitempty"`
}

// MCPServerConfig describes an MCP server to connect to for tool integration.
type MCPServerConfig struct {
	Name    string   `yaml:"name"`
	Command string   `yaml:"command"`
	Args    []string `yaml:"args,omitempty"`
	Env     []string `yaml:"env,omitempty"`
	URL     string   `yaml:"url,omitempty"` // SSE transport (overrides command)
}

// ACPAgentConfig describes an ACP (Agent Client Protocol) agent subprocess.
// Supports gemini-cli (--acp), copilot-cli, claude-code, etc.
type ACPAgentConfig struct {
	Name    string   `yaml:"name"`
	Command string   `yaml:"command"`
	Args    []string `yaml:"args,omitempty"`
	Env     []string `yaml:"env,omitempty"`
}

// CopilotSDKConfig configures the native GitHub Copilot CLI SDK integration.
type CopilotSDKConfig struct {
	CLIPath       string   `yaml:"cli_path,omitempty"`       // path to copilot CLI executable (default: auto-detect)
	Model         string   `yaml:"model,omitempty"`          // e.g. "gpt-5", "claude-sonnet-4.5"
	SystemMessage string   `yaml:"system_message,omitempty"` // appended to the system prompt
	Env           []string `yaml:"env,omitempty"`            // extra env vars for CLI process
}

// A2AAgentConfig describes a remote A2A agent for agent-to-agent communication.
// Deprecated: use ACPAgentConfig instead.
type A2AAgentConfig struct {
	Name string `yaml:"name"`
	URL  string `yaml:"url"`
}

// EarningsConfig controls freshness and source-priority for earnings ingestion.
// These settings are source-agnostic tuning knobs; individual integrations can
// choose how many of them to implement.
type EarningsConfig struct {
	SourcePriority            []string          `yaml:"source_priority,omitempty"`              // e.g. ["edgar", "yahoo", "company_ir"]
	IRFeedRegistry            map[string]string `yaml:"ir_feed_registry,omitempty"`             // symbol -> RSS/Atom feed URL
	IRPageRegistry            map[string]string `yaml:"ir_page_registry,omitempty"`             // symbol -> investor relations/news page URL
	SearchHints               map[string]string `yaml:"search_hints,omitempty"`                 // symbol -> human search hint phrase for manual/discovery flows
	WebCrawlerCommand         string            `yaml:"web_crawler_command,omitempty"`          // optional external crawler executable (e.g. crawl4ai)
	WebCrawlerArgs            []string          `yaml:"web_crawler_args,omitempty"`             // optional args; supports {url} placeholder
	HighFreqPollingSeconds    int               `yaml:"high_freq_polling_seconds,omitempty"`    // polling cadence during release windows
	NormalPollingSeconds      int               `yaml:"normal_polling_seconds,omitempty"`       // polling cadence during market hours
	OffHoursPollingSeconds    int               `yaml:"off_hours_polling_seconds,omitempty"`    // polling cadence outside market hours
	WindowBeforeMinutes       int               `yaml:"window_before_minutes,omitempty"`        // enter high-frequency mode N minutes before expected release
	WindowAfterMinutes        int               `yaml:"window_after_minutes,omitempty"`         // keep high-frequency mode N minutes after expected release
	EDGARConfirmSeconds       int               `yaml:"edgar_confirm_seconds,omitempty"`        // cadence for post-trigger EDGAR confirmation pass
	EDGARConfirmDurationMins  int               `yaml:"edgar_confirm_duration_mins,omitempty"`  // how long to run EDGAR confirmation after trigger
	YahooBackfillSeconds      int               `yaml:"yahoo_backfill_seconds,omitempty"`       // cadence for post-trigger Yahoo normalization pass
	YahooBackfillDurationMins int               `yaml:"yahoo_backfill_duration_mins,omitempty"` // how long to run Yahoo backfill after trigger
}

// InvestorProfile captures the user's investment preferences so the AI
// can tailor its analysis. All fields are optional; empty values mean
// "no preference" and the AI will give generic advice.
type InvestorProfile struct {
	// Strategies are free-form tags describing the user's approach,
	// e.g. "passive", "growth", "value", "income", "buy-and-hold", "dca",
	// "active", "momentum", "dividend". Multiple values are combined.
	Strategies []string `yaml:"strategies,omitempty"`
	// Risk is one of: "conservative", "balanced", "aggressive".
	Risk string `yaml:"risk,omitempty"`
	// Horizon is an optional free-form note, e.g. "10+ years",
	// "retirement 2045", "short-term trading".
	Horizon string `yaml:"horizon,omitempty"`
	// Goals are optional free-form objectives, e.g.
	// "capital preservation", "income in retirement", "maximum growth".
	Goals []string `yaml:"goals,omitempty"`
	// Notes is an optional free-form paragraph for anything else the
	// user wants the AI to consider (e.g. tax situation, ESG preferences).
	Notes string `yaml:"notes,omitempty"`
}

// SystemPromptBlock returns a ready-to-inject markdown paragraph
// describing the investor profile. Returns "" when the profile is
// effectively empty so callers can skip injection.
func (p InvestorProfile) SystemPromptBlock() string {
	if len(p.Strategies) == 0 && p.Risk == "" && p.Horizon == "" && len(p.Goals) == 0 && p.Notes == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n\n## Investor Profile\n")
	b.WriteString("Tailor your analysis to this investor's preferences:\n")
	if len(p.Strategies) > 0 {
		b.WriteString("- Strategies: ")
		b.WriteString(strings.Join(p.Strategies, ", "))
		b.WriteString("\n")
	}
	if p.Risk != "" {
		b.WriteString("- Risk tolerance: ")
		b.WriteString(p.Risk)
		b.WriteString("\n")
	}
	if p.Horizon != "" {
		b.WriteString("- Investment horizon: ")
		b.WriteString(p.Horizon)
		b.WriteString("\n")
	}
	if len(p.Goals) > 0 {
		b.WriteString("- Goals: ")
		b.WriteString(strings.Join(p.Goals, ", "))
		b.WriteString("\n")
	}
	if p.Notes != "" {
		b.WriteString("- Notes: ")
		b.WriteString(p.Notes)
		b.WriteString("\n")
	}
	b.WriteString("Weigh suggestions accordingly (e.g. avoid high-volatility picks for conservative investors, de-emphasize short-term trades for buy-and-hold).")
	return b.String()
}

func DefaultConfigPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "finsight", "config.yaml")
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		cfg := defaultConfig()
		cfg.LLM.APIKey = ResolveLLMAPIKey(cfg.LLM.Provider, cfg.LLM.APIKeyEnv)
		return cfg, nil
	}

	cfg := &Config{}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}

	// Secrets are read from the environment, never from config.yaml.
	cfg.LLM.APIKey = ResolveLLMAPIKey(cfg.LLM.Provider, cfg.LLM.APIKeyEnv)
	cfg.LogFile = expandPath(cfg.LogFile)

	if cfg.RefreshInterval == 0 {
		cfg.RefreshInterval = 30
	}
	if cfg.ChartRange == "" {
		cfg.ChartRange = "1d"
	}
	if cfg.ChartInterval == "" {
		cfg.ChartInterval = "1h"
	}
	if cfg.ChartStyle == "" {
		cfg.ChartStyle = "candlestick_dotted"
	}
	if cfg.ColorScheme == "" {
		cfg.ColorScheme = "default"
	}
	if len(cfg.Earnings.SourcePriority) == 0 {
		cfg.Earnings.SourcePriority = []string{"edgar", "yahoo", "company_ir"}
	}
	if cfg.Earnings.HighFreqPollingSeconds <= 0 {
		cfg.Earnings.HighFreqPollingSeconds = 60
	}
	if cfg.Earnings.NormalPollingSeconds <= 0 {
		cfg.Earnings.NormalPollingSeconds = 300
	}
	if cfg.Earnings.OffHoursPollingSeconds <= 0 {
		cfg.Earnings.OffHoursPollingSeconds = 900
	}
	if cfg.Earnings.WindowBeforeMinutes <= 0 {
		cfg.Earnings.WindowBeforeMinutes = 120
	}
	if cfg.Earnings.WindowAfterMinutes <= 0 {
		cfg.Earnings.WindowAfterMinutes = 120
	}
	if cfg.Earnings.EDGARConfirmSeconds <= 0 {
		cfg.Earnings.EDGARConfirmSeconds = 120
	}
	if cfg.Earnings.EDGARConfirmDurationMins <= 0 {
		cfg.Earnings.EDGARConfirmDurationMins = 30
	}
	if cfg.Earnings.YahooBackfillSeconds <= 0 {
		cfg.Earnings.YahooBackfillSeconds = 900
	}
	if cfg.Earnings.YahooBackfillDurationMins <= 0 {
		cfg.Earnings.YahooBackfillDurationMins = 240
	}

	// Migrate old flat watchlist → grouped watchlists
	if len(cfg.Watchlists) == 0 && len(cfg.Watchlist) > 0 {
		cfg.Watchlists = []WatchlistGroup{
			{Name: "Default", Symbols: cfg.Watchlist},
		}
		cfg.Watchlist = nil
	}
	if len(cfg.Watchlists) == 0 {
		cfg.Watchlists = defaultConfig().Watchlists
	}

	return cfg, nil
}

func expandPath(path string) string {
	if path == "" {
		return ""
	}
	path = os.ExpandEnv(path)
	if path == "~" || strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil && home != "" {
			if path == "~" {
				return home
			}
			return filepath.Join(home, path[2:])
		}
	}
	return path
}

func Save(path string, cfg *Config) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}

// AddSymbol adds a symbol to the specified group index.
func (c *Config) AddSymbol(group int, symbol, name string) {
	if group < 0 || group >= len(c.Watchlists) {
		return
	}
	for _, item := range c.Watchlists[group].Symbols {
		if item.Symbol == symbol {
			return
		}
	}
	c.Watchlists[group].Symbols = append(c.Watchlists[group].Symbols, WatchItem{Symbol: symbol, Name: name})
}

// RemoveSymbol removes a symbol from the specified group index.
func (c *Config) RemoveSymbol(group int, symbol string) {
	if group < 0 || group >= len(c.Watchlists) {
		return
	}
	items := c.Watchlists[group].Symbols
	for i, item := range items {
		if item.Symbol == symbol {
			c.Watchlists[group].Symbols = append(items[:i], items[i+1:]...)
			return
		}
	}
}

func defaultConfig() *Config {
	return &Config{
		RefreshInterval: 900,
		ChartRange:      "1d",
		ChartInterval:   "1h",
		ChartStyle:      "candlestick_dotted",
		ColorScheme:     "default",
		Earnings: EarningsConfig{
			SourcePriority:            []string{"edgar", "yahoo", "company_ir"},
			IRFeedRegistry:            map[string]string{},
			IRPageRegistry:            map[string]string{},
			SearchHints:               map[string]string{},
			HighFreqPollingSeconds:    60,
			NormalPollingSeconds:      300,
			OffHoursPollingSeconds:    900,
			WindowBeforeMinutes:       120,
			WindowAfterMinutes:        120,
			EDGARConfirmSeconds:       120,
			EDGARConfirmDurationMins:  30,
			YahooBackfillSeconds:      900,
			YahooBackfillDurationMins: 240,
		},
		Watchlists: []WatchlistGroup{
			{
				Name: "Default",
				Symbols: []WatchItem{
					{Symbol: "^GSPC", Name: "S&P 500"},
					{Symbol: "^IXIC", Name: "NASDAQ Composite"},
				},
			},
		},
	}
}

// ResolveLLMAPIKey returns the LLM credential for the given provider,
// loaded from environment variables. Secrets must NEVER be stored in
// config.yaml. Lookup order (first non-empty wins):
//
//  1. The env var named by llm.api_key_env in config.yaml (if set)
//  2. FINSIGHT_LLM_API_KEY  — generic override
//  3. Per-provider defaults:
//     openai  → OPENAI_API_KEY
//     copilot → GITHUB_COPILOT_TOKEN, GH_TOKEN, GITHUB_TOKEN
//     (if all empty the client falls back to
//     ~/.config/github-copilot/apps.json populated by the
//     official Copilot extensions)
//     vertex  → GOOGLE_ACCESS_TOKEN, GCLOUD_ACCESS_TOKEN
//     (produced by `gcloud auth print-access-token`)
func ResolveLLMAPIKey(provider, envName string) string {
	if envName != "" {
		if v := os.Getenv(envName); v != "" {
			return v
		}
	}
	if v := os.Getenv("FINSIGHT_LLM_API_KEY"); v != "" {
		return v
	}
	switch strings.ToLower(provider) {
	case "copilot":
		for _, k := range []string{"GITHUB_COPILOT_TOKEN", "GH_TOKEN", "GITHUB_TOKEN"} {
			if v := os.Getenv(k); v != "" {
				return v
			}
		}
	case "vertex":
		for _, k := range []string{"GOOGLE_ACCESS_TOKEN", "GCLOUD_ACCESS_TOKEN"} {
			if v := os.Getenv(k); v != "" {
				return v
			}
		}
	case "gemini":
		for _, k := range []string{"GEMINI_API_KEY", "GOOGLE_API_KEY"} {
			if v := os.Getenv(k); v != "" {
				return v
			}
		}
	case "anthropic":
		if v := os.Getenv("ANTHROPIC_API_KEY"); v != "" {
			return v
		}
	default: // openai and anything else
		if v := os.Getenv("OPENAI_API_KEY"); v != "" {
			return v
		}
	}
	return ""
}
