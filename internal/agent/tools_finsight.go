package agent

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/ray-x/finsight/internal/db"
	"github.com/ray-x/finsight/internal/edgar"
	"github.com/ray-x/finsight/internal/llm"
	"github.com/ray-x/finsight/internal/news"
	"github.com/ray-x/finsight/internal/yahoo"
)

// Deps bundles the finsight services an agent can reach through tools.
// All fields are optional; tools whose dependencies are missing are
// silently omitted from DefaultTools.
type Deps struct {
	Yahoo       *yahoo.Client
	News        news.Provider
	Edgar       *edgar.Client
	KnowledgeDB *db.DB // SQLite database for persistent storage and retrieval
}

// DefaultTools returns a core set of tools for financial analysis and insight.
// get_earnings (+ get_guidance when EDGAR is configured, + retrieval tools when KnowledgeDB is configured).
// Extend this function (or build tools inline) as new capabilities come online.
func DefaultTools(d Deps) []Tool {
	var out []Tool
	if d.Yahoo != nil {
		out = append(out, QuoteTool(d.Yahoo))
		out = append(out, EarningsTool(d.Yahoo))
		out = append(out, TechnicalsTool(d.Yahoo))
	}
	if d.News != nil {
		out = append(out, NewsTool(d.News))
	}
	if d.Edgar != nil {
		out = append(out, GuidanceTool(d.Edgar))
	}
	// Add retrieval and storage tools when KnowledgeDB is configured
	if d.KnowledgeDB != nil {
		out = AddRetrievalTools(d, out)
	}
	return out
}

// QuoteTool exposes yahoo.GetQuotes as a tool. Returns a compact JSON
// payload with price + change% to keep response tokens low.
func QuoteTool(y *yahoo.Client) Tool {
	return Tool{
		Spec: llm.ToolSpec{
			Name:        "get_quote",
			Description: "Fetch the latest price and intraday change for one or more tickers. Use this first when the user asks about recent price action.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"symbols": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "string"},
						"description": "Ticker symbols, e.g. [\"NVDA\", \"AAPL\"]. Keep to at most 5 per call.",
					},
				},
				"required": []string{"symbols"},
			},
		},
		Handler: func(ctx context.Context, args map[string]any) (string, error) {
			syms, err := requiredStringSliceArg(args, "symbols")
			if err != nil {
				return "", err
			}
			for i := range syms {
				syms[i] = strings.ToUpper(strings.TrimSpace(syms[i]))
			}
			if len(syms) == 0 {
				return "", fmt.Errorf("symbols array is required and non-empty")
			}
			if len(syms) > 5 {
				syms = syms[:5]
			}
			quotes, err := y.GetQuotes(syms)
			if err != nil {
				return "", err
			}
			type row struct {
				Symbol        string  `json:"symbol"`
				Name          string  `json:"name,omitempty"`
				Price         float64 `json:"price"`
				Change        float64 `json:"change"`
				ChangePercent float64 `json:"change_percent"`
				Currency      string  `json:"currency,omitempty"`
			}
			rows := make([]row, 0, len(quotes))
			for _, q := range quotes {
				rows = append(rows, row{
					Symbol:        q.Symbol,
					Name:          q.Name,
					Price:         q.Price,
					Change:        q.Change,
					ChangePercent: q.ChangePercent,
					Currency:      q.Currency,
				})
			}
			b, err := json.Marshal(rows)
			if err != nil {
				return "", fmt.Errorf("serialize quotes: %w", err)
			}
			return string(b), nil
		},
	}
}

// NewsTool exposes the filtered news feed as a tool. Always runs
// results through news.Filter so off-topic / stale / blocklisted items
// never reach the model.
func NewsTool(p news.Provider) Tool {
	return Tool{
		Spec: llm.ToolSpec{
			Name:        "get_news",
			Description: "Fetch recent headlines for a single ticker, filtered for relevance. Call this when explaining price moves, sentiment, or company-specific events.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"symbol": map[string]any{
						"type":        "string",
						"description": "Ticker symbol, e.g. \"NVDA\".",
					},
					"company_name": map[string]any{
						"type":        "string",
						"description": "Optional full company name (e.g. \"NVIDIA Corporation\") to improve headline relevance filtering.",
					},
					"days": map[string]any{
						"type":        "integer",
						"description": "Look-back window in days. Defaults to 7. Max 30.",
					},
					"limit": map[string]any{
						"type":        "integer",
						"description": "Max headlines to return after filtering. Defaults to 8. Max 15.",
					},
				},
				"required": []string{"symbol"},
			},
		},
		Handler: func(ctx context.Context, args map[string]any) (string, error) {
			sym, err := requiredStringArg(args, "symbol")
			if err != nil {
				return "", err
			}
			sym = strings.ToUpper(strings.TrimSpace(sym))
			companyName, err := optionalStringArg(args, "company_name")
			if err != nil {
				return "", err
			}
			days := intArg(args, "days", 7, 30)
			limit := intArg(args, "limit", 8, 15)
			since := time.Now().AddDate(0, 0, -days)
			items, err := p.GetCompanyNews(sym, since, limit*3) // over-fetch; filter trims
			if err != nil {
				return "", err
			}
			items = news.Filter(items, news.FilterOpts{
				Symbol:      sym,
				CompanyName: companyName,
				Limit:       limit,
				MaxAge:      time.Duration(days) * 24 * time.Hour,
			})
			type row struct {
				Title     string `json:"title"`
				Publisher string `json:"publisher,omitempty"`
				Published string `json:"published,omitempty"`
				Link      string `json:"link,omitempty"`
			}
			rows := make([]row, 0, len(items))
			for _, it := range items {
				rows = append(rows, row{
					Title:     it.Title,
					Publisher: it.Publisher,
					Published: it.PublishedAt.Format(time.RFC3339),
					Link:      it.Link,
				})
			}
			b, err := json.Marshal(rows)
			if err != nil {
				return "", fmt.Errorf("serialize news: %w", err)
			}
			return string(b), nil
		},
	}
}

// EarningsTool exposes yahoo.GetFinancials as a tool. Returns a
// compact JSON summary (TTM revenue, margins, growth, EPS surprises,
// analyst consensus) rather than the full FinancialData blob to keep
// response tokens manageable.
func EarningsTool(y *yahoo.Client) Tool {
	return Tool{
		Spec: llm.ToolSpec{
			Name:        "get_earnings",
			Description: "Fetch recent quarterly earnings, EPS beats/misses, forward guidance (analyst consensus for the current/next quarter and current/next year covering EPS + revenue + growth + estimate dispersion), the next earnings date, growth rates, margins, and analyst price targets for a single ticker. Use this for any question about recent earnings, upcoming earnings, guidance, fundamentals, or estimates. The `eps_history` and `quarterly` arrays are returned NEWEST-FIRST. The `guidance` array is FORWARD-LOOKING (current quarter, next quarter, current year, next year). Each entry's date is in M/D/YYYY or YYYY-MM-DD format. Prefer `latest_reported_quarter` to identify the most recent result and `next_earnings_date` for the upcoming report.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"symbol": map[string]any{
						"type":        "string",
						"description": "Ticker symbol, e.g. \"NVDA\".",
					},
					"quarters": map[string]any{
						"type":        "integer",
						"description": "Number of recent quarters to include in the EPS history. Defaults to 4. Max 8.",
					},
				},
				"required": []string{"symbol"},
			},
		},
		Handler: func(ctx context.Context, args map[string]any) (string, error) {
			sym, err := requiredStringArg(args, "symbol")
			if err != nil {
				return "", err
			}
			sym = strings.ToUpper(strings.TrimSpace(sym))
			quarters := intArg(args, "quarters", 4, 8)

			fin, err := y.GetFinancials(sym)
			if err != nil {
				return "", err
			}
			if fin == nil {
				return "", fmt.Errorf("no financial data for %s", sym)
			}

			type epsRow struct {
				Quarter         string  `json:"quarter"`
				EPSActual       float64 `json:"eps_actual"`
				EPSEstimate     float64 `json:"eps_estimate"`
				SurprisePercent float64 `json:"surprise_percent"`
				Verdict         string  `json:"verdict,omitempty"` // beat | meet | miss
			}
			type periodRow struct {
				Date     string `json:"date"`
				Revenue  int64  `json:"revenue"`
				Earnings int64  `json:"earnings"`
			}
			// Yahoo returns earningsHistory / financialsChart in ASCENDING
			// order (oldest first). The model expects newest-first so it
			// doesn't drop the most recent quarter when we slice.
			eps := reverseEPS(fin.EPSHistory)
			if len(eps) > quarters {
				eps = eps[:quarters]
			}
			epsRows := make([]epsRow, 0, len(eps))
			for _, q := range eps {
				epsRows = append(epsRows, epsRow{
					Quarter:         q.Quarter,
					EPSActual:       q.EPSActual,
					EPSEstimate:     q.EPSEstimate,
					SurprisePercent: q.SurprisePercent,
					Verdict:         epsVerdict(q.EPSActual, q.EPSEstimate, q.SurprisePercent),
				})
			}
			qtr := reversePeriods(fin.Quarterly)
			if len(qtr) > quarters {
				qtr = qtr[:quarters]
			}
			qRows := make([]periodRow, 0, len(qtr))
			for _, p := range qtr {
				qRows = append(qRows, periodRow{Date: p.Date, Revenue: p.Revenue, Earnings: p.Earnings})
			}
			yr := reversePeriods(fin.Yearly)
			if len(yr) > 3 {
				yr = yr[:3]
			}
			yRows := make([]periodRow, 0, len(yr))
			for _, p := range yr {
				yRows = append(yRows, periodRow{Date: p.Date, Revenue: p.Revenue, Earnings: p.Earnings})
			}

			// Surface staleness / empty-payload cases explicitly so the
			// model can retry or ask the user, instead of guessing.
			if len(epsRows) == 0 && len(qRows) == 0 && len(yRows) == 0 && fin.RevenueTTM == 0 {
				return "", fmt.Errorf("no financial data returned for %s (upstream may be rate-limited or the symbol may be unsupported)", sym)
			}

			latestQuarter := ""
			latestEPSVerdict := ""
			latestSurprisePct := 0.0
			if len(epsRows) > 0 {
				latestQuarter = epsRows[0].Quarter
				latestEPSVerdict = epsRows[0].Verdict
				latestSurprisePct = epsRows[0].SurprisePercent
			} else if len(qRows) > 0 {
				latestQuarter = qRows[0].Date
			}

			type guidanceRow struct {
				Period              string  `json:"period"`
				Label               string  `json:"label"`
				EndDate             string  `json:"end_date,omitempty"`
				EPSAvg              float64 `json:"eps_avg"`
				EPSLow              float64 `json:"eps_low,omitempty"`
				EPSHigh             float64 `json:"eps_high,omitempty"`
				EPSYearAgo          float64 `json:"eps_year_ago,omitempty"`
				EPSAnalysts         int64   `json:"eps_analysts,omitempty"`
				EPSGrowthYoY        float64 `json:"eps_growth_yoy,omitempty"`
				RevenueAvg          int64   `json:"revenue_avg"`
				RevenueLow          int64   `json:"revenue_low,omitempty"`
				RevenueHigh         int64   `json:"revenue_high,omitempty"`
				RevenueYearAgo      int64   `json:"revenue_year_ago,omitempty"`
				RevenueAnalysts     int64   `json:"revenue_analysts,omitempty"`
				RevenueGrowthYoY    float64 `json:"revenue_growth_yoy,omitempty"`
				EPSRevisionsUp7d    int64   `json:"eps_revisions_up_7d,omitempty"`
				EPSRevisionsDown7d  int64   `json:"eps_revisions_down_7d,omitempty"`
				EPSRevisionsUp30d   int64   `json:"eps_revisions_up_30d,omitempty"`
				EPSRevisionsDown30d int64   `json:"eps_revisions_down_30d,omitempty"`
			}
			guidanceRows := make([]guidanceRow, 0, len(fin.Guidance))
			for _, g := range fin.Guidance {
				guidanceRows = append(guidanceRows, guidanceRow{
					Period:              g.Period,
					Label:               g.Label,
					EndDate:             g.EndDate,
					EPSAvg:              g.EPSAvg,
					EPSLow:              g.EPSLow,
					EPSHigh:             g.EPSHigh,
					EPSYearAgo:          g.EPSYearAgo,
					EPSAnalysts:         g.EPSAnalysts,
					EPSGrowthYoY:        g.Growth,
					RevenueAvg:          g.RevenueAvg,
					RevenueLow:          g.RevenueLow,
					RevenueHigh:         g.RevenueHigh,
					RevenueYearAgo:      g.RevenueYearAgo,
					RevenueAnalysts:     g.RevenueAnalysts,
					RevenueGrowthYoY:    g.RevenueGrowth,
					EPSRevisionsUp7d:    g.EPSRevisionsUp7d,
					EPSRevisionsDown7d:  g.EPSRevisionsDown7d,
					EPSRevisionsUp30d:   g.EPSRevisionsUp30d,
					EPSRevisionsDown30d: g.EPSRevisionsDown30d,
				})
			}

			payload := map[string]any{
				"symbol":                  sym,
				"data_as_of":              time.Now().UTC().Format("2006-01-02"),
				"latest_reported_quarter": latestQuarter,
				"latest_eps_verdict":      latestEPSVerdict,  // beat | meet | miss
				"latest_eps_surprise_pct": latestSurprisePct, // signed, e.g. 4.68
				"next_earnings_date":      fin.NextEarningsDate,
				"ttm": map[string]any{
					"revenue":            fin.RevenueTTM,
					"ebitda":             fin.EBITDA,
					"free_cashflow":      fin.FreeCashflow,
					"operating_cashflow": fin.OperatingCashflow,
				},
				"margins": map[string]any{
					"profit":    fin.ProfitMargin,
					"operating": fin.OperatingMargins,
					"gross":     fin.GrossMargins,
				},
				"growth_yoy": map[string]any{
					"revenue":  fin.RevenueGrowth,
					"earnings": fin.EarningsGrowth,
				},
				"balance": map[string]any{
					"debt_to_equity": fin.DebtToEquity,
					"current_ratio":  fin.CurrentRatio,
					"total_cash":     fin.TotalCash,
					"total_debt":     fin.TotalDebt,
				},
				"valuation": map[string]any{
					"price_to_sales":        fin.PriceToSales,
					"enterprise_value":      fin.EnterpriseValue,
					"enterprise_to_revenue": fin.EnterpriseToRevenue,
					"enterprise_to_ebitda":  fin.EnterpriseToEbitda,
				},
				"analysts": map[string]any{
					"count":            fin.NumberOfAnalysts,
					"recommendation":   fin.RecommendationKey,
					"target_mean":      fin.TargetMeanPrice,
					"target_high":      fin.TargetHighPrice,
					"target_low":       fin.TargetLowPrice,
					"return_on_equity": fin.ReturnOnEquity,
				},
				"guidance":    guidanceRows,
				"eps_history": epsRows,
				"quarterly":   qRows,
				"yearly":      yRows,
			}
			b, err := json.Marshal(payload)
			if err != nil {
				return "", fmt.Errorf("serialize earnings: %w", err)
			}
			return string(b), nil
		},
	}
}

// GuidanceTool exposes EDGAR 8-K (US filers) and 6-K (foreign private
// issuers) press releases as a tool. These are the official documents
// where companies issue forward guidance — e.g. "We expect Q2 revenue
// of $30.5–$31.5B". The tool returns the cleaned filing text and
// lets the LLM extract the numeric guidance.
//
// Returns the most recent press release; the agent should call this
// AFTER get_earnings so it can correlate guidance with the actuals.
func GuidanceTool(e *edgar.Client) Tool {
	return Tool{
		Spec: llm.ToolSpec{
			Name:        "get_guidance",
			Description: "Fetch the most recent SEC 8-K (US issuers) or 6-K (foreign issuers like TSM, ASML, NVO) press release for a single ticker. These filings contain company-issued FORWARD GUIDANCE (e.g. \"We expect Q2 revenue of $X-$Y billion\") that is NOT available in get_earnings (which only returns analyst consensus). Use this to compare actual results against the company's own targets, or to surface management's outlook for the upcoming quarter/year. Returns cleaned filing text; you parse the guidance numbers from it.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"symbol": map[string]any{
						"type":        "string",
						"description": "Ticker symbol, e.g. \"NVDA\" or \"TSM\".",
					},
					"max_chars": map[string]any{
						"type":        "integer",
						"description": "Max characters of filing text to return. Defaults to 25000. Max 60000.",
					},
				},
				"required": []string{"symbol"},
			},
		},
		Handler: func(ctx context.Context, args map[string]any) (string, error) {
			sym, err := requiredStringArg(args, "symbol")
			if err != nil {
				return "", err
			}
			sym = strings.ToUpper(strings.TrimSpace(sym))
			maxChars := intArg(args, "max_chars", 25000, 60000)

			filing, err := e.GetLatestPressRelease(sym)
			if err != nil {
				return "", err
			}
			ft, err := e.FetchFilingText(sym, *filing, maxChars)
			if err != nil {
				return "", err
			}
			payload := map[string]any{
				"symbol":       sym,
				"data_as_of":   time.Now().UTC().Format("2006-01-02"),
				"form":         filing.Form,
				"filing_date":  filing.FilingDate,
				"description":  filing.Description,
				"accession_no": filing.AccessionNo,
				"text":         ft.Text,
				"note":         "Extract company-issued guidance ranges from the text (e.g. revenue, gross margin, opex). If the text contains routine non-earnings disclosures (board changes, M&A) and no guidance, say so explicitly.",
			}
			b, err := json.Marshal(payload)
			if err != nil {
				return "", fmt.Errorf("serialize guidance: %w", err)
			}
			return string(b), nil
		},
	}
}

func requiredStringArg(args map[string]any, key string) (string, error) {
	v, ok := args[key]
	if !ok {
		return "", fmt.Errorf("%s is required", key)
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", key)
	}
	s = strings.TrimSpace(s)
	if s == "" {
		return "", fmt.Errorf("%s is required", key)
	}
	return s, nil
}

func optionalStringArg(args map[string]any, key string) (string, error) {
	v, ok := args[key]
	if !ok || v == nil {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", key)
	}
	return strings.TrimSpace(s), nil
}

func requiredStringSliceArg(args map[string]any, key string) ([]string, error) {
	v, ok := args[key]
	if !ok {
		return nil, fmt.Errorf("%s is required", key)
	}
	raw, ok := v.([]any)
	if !ok {
		return nil, fmt.Errorf("%s must be an array of strings", key)
	}
	out := make([]string, 0, len(raw))
	for _, item := range raw {
		s, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("%s must contain only strings", key)
		}
		s = strings.TrimSpace(s)
		if s != "" {
			out = append(out, s)
		}
	}
	return out, nil
}

// intArg extracts an integer argument with a default and a cap.
// Gracefully handles both JSON numbers (float64) and numeric strings.
func intArg(args map[string]any, key string, def, max int) int {
	if v, ok := args[key]; ok {
		switch n := v.(type) {
		case float64:
			def = int(n)
		case int:
			def = n
		case string:
			parsed, err := strconv.Atoi(strings.TrimSpace(n))
			if err == nil {
				def = parsed
			}
		}
	}
	if def <= 0 {
		def = 1
	}
	if max > 0 && def > max {
		def = max
	}
	return def
}

// reverseEPS returns a copy of the EPS history in newest-first order.
// Yahoo returns earningsHistory oldest-first; the agent needs newest-first
// so slicing to N keeps the most recent quarter.
func reverseEPS(in []yahoo.EPSQuarter) []yahoo.EPSQuarter {
	out := make([]yahoo.EPSQuarter, len(in))
	for i, v := range in {
		out[len(in)-1-i] = v
	}
	return out
}

// reversePeriods mirrors reverseEPS for revenue/earnings period slices.
func reversePeriods(in []yahoo.FinancialPeriod) []yahoo.FinancialPeriod {
	out := make([]yahoo.FinancialPeriod, len(in))
	for i, v := range in {
		out[len(in)-1-i] = v
	}
	return out
}

// epsVerdict classifies an EPS report against analyst consensus.
// Returns "beat" / "meet" / "miss" using ±2% surprise as the threshold,
// or "" when no data is available. Falls back to absolute comparison
// when surprisePct is missing (Yahoo sometimes omits it).
func epsVerdict(actual, estimate, surprisePct float64) string {
	if actual == 0 && estimate == 0 {
		return ""
	}
	pct := surprisePct
	if pct == 0 && estimate != 0 {
		pct = (actual - estimate) / estimate * 100
	}
	switch {
	case pct >= 2:
		return "beat"
	case pct <= -2:
		return "miss"
	default:
		return "meet"
	}
}
