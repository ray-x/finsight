package llm

import (
	"fmt"
	"strings"
)

const systemPrompt = `You are a concise financial analyst. Given stock data, provide a brief analysis covering:
1. **Price Action**: Current trend and key levels
2. **Valuation**: Whether the stock appears over/under-valued based on metrics
3. **Financial Health**: Key strengths or concerns from the financials
4. **Verdict**: A one-line summary (Bullish / Bearish / Neutral) with key reasoning

Keep the response under 300 words.`

// formatInstruction is appended to every user prompt so even weak models format correctly.
const formatInstruction = `

Format your response using markdown:
- Use ## headings for sections
- Use **bold** for important terms
- Use ==double equals== around critical numbers (e.g. ==revenue $8.7B==, ==-12% YoY==)
- Use bullet points for lists`

// QuoteData holds the fields we send to the LLM.
type QuoteData struct {
	Symbol           string
	Name             string
	Price            float64
	Change           float64
	ChangePercent    float64
	MarketCap        int64
	PE               float64
	ForwardPE        float64
	EPS              float64
	PEG              float64
	Beta             float64
	FiftyTwoWeekHigh float64
	FiftyTwoWeekLow  float64
	DividendYield    float64
	Volume           int64
	AvgVolume        int64
	DayHigh          float64
	DayLow           float64
	Open             float64
	PreviousClose    float64
}

// FinancialSummary holds key financial metrics for the prompt.
type FinancialSummary struct {
	ProfitMargin      float64
	RevenueGrowth     float64
	EarningsGrowth    float64
	DebtToEquity      float64
	CurrentRatio      float64
	FreeCashflow      int64
	GrossMargins      float64
	OperatingMargins  float64
	ReturnOnEquity    float64
	RecommendationKey string
	TargetMeanPrice   float64
	TargetHighPrice   float64
	TargetLowPrice    float64
	NumberOfAnalysts  int64
}

// BuildUserPrompt constructs the data prompt sent to the LLM.
func BuildUserPrompt(q QuoteData, fin *FinancialSummary) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("Analyze %s (%s):\n\n", q.Symbol, q.Name))
	sb.WriteString(fmt.Sprintf("Price: $%.2f (%.2f%% today)\n", q.Price, q.ChangePercent))
	sb.WriteString(fmt.Sprintf("Open: $%.2f | Day Range: $%.2f - $%.2f\n", q.Open, q.DayLow, q.DayHigh))
	sb.WriteString(fmt.Sprintf("52-Week Range: $%.2f - $%.2f\n", q.FiftyTwoWeekLow, q.FiftyTwoWeekHigh))
	sb.WriteString(fmt.Sprintf("Volume: %d (Avg: %d)\n", q.Volume, q.AvgVolume))

	if q.MarketCap > 0 {
		sb.WriteString(fmt.Sprintf("Market Cap: %s\n", formatLargeNumber(q.MarketCap)))
	}
	if q.PE > 0 {
		sb.WriteString(fmt.Sprintf("P/E (TTM): %.2f", q.PE))
		if q.ForwardPE > 0 {
			sb.WriteString(fmt.Sprintf(" | Forward P/E: %.2f", q.ForwardPE))
		}
		sb.WriteString("\n")
	}
	if q.EPS != 0 {
		sb.WriteString(fmt.Sprintf("EPS: $%.2f\n", q.EPS))
	}
	if q.PEG > 0 {
		sb.WriteString(fmt.Sprintf("PEG Ratio: %.2f\n", q.PEG))
	}
	if q.Beta > 0 {
		sb.WriteString(fmt.Sprintf("Beta: %.2f\n", q.Beta))
	}
	if q.DividendYield > 0 {
		sb.WriteString(fmt.Sprintf("Dividend Yield: %.2f%%\n", q.DividendYield*100))
	}

	if fin != nil {
		sb.WriteString("\nFinancials:\n")
		if fin.ProfitMargin != 0 {
			sb.WriteString(fmt.Sprintf("Profit Margin: %.1f%%\n", fin.ProfitMargin*100))
		}
		if fin.GrossMargins != 0 {
			sb.WriteString(fmt.Sprintf("Gross Margin: %.1f%%\n", fin.GrossMargins*100))
		}
		if fin.OperatingMargins != 0 {
			sb.WriteString(fmt.Sprintf("Operating Margin: %.1f%%\n", fin.OperatingMargins*100))
		}
		if fin.RevenueGrowth != 0 {
			sb.WriteString(fmt.Sprintf("Revenue Growth (QoQ): %.1f%%\n", fin.RevenueGrowth*100))
		}
		if fin.EarningsGrowth != 0 {
			sb.WriteString(fmt.Sprintf("Earnings Growth (QoQ): %.1f%%\n", fin.EarningsGrowth*100))
		}
		if fin.DebtToEquity > 0 {
			sb.WriteString(fmt.Sprintf("Debt/Equity: %.1f%%\n", fin.DebtToEquity))
		}
		if fin.CurrentRatio > 0 {
			sb.WriteString(fmt.Sprintf("Current Ratio: %.2f\n", fin.CurrentRatio))
		}
		if fin.ReturnOnEquity != 0 {
			sb.WriteString(fmt.Sprintf("Return on Equity: %.1f%%\n", fin.ReturnOnEquity*100))
		}
		if fin.FreeCashflow != 0 {
			sb.WriteString(fmt.Sprintf("Free Cash Flow: %s\n", formatLargeNumber(fin.FreeCashflow)))
		}
		if fin.NumberOfAnalysts > 0 {
			sb.WriteString(fmt.Sprintf("\nAnalyst Consensus: %s (n=%d)\n", fin.RecommendationKey, fin.NumberOfAnalysts))
			sb.WriteString(fmt.Sprintf("Price Targets: $%.0f (low) — $%.0f (mean) — $%.0f (high)\n",
				fin.TargetLowPrice, fin.TargetMeanPrice, fin.TargetHighPrice))
		}
	}

	return sb.String() + formatInstruction
}

const summarySystemPrompt = `You are a concise market analyst. Given a watchlist of stocks with their current data, provide a brief dashboard summary covering:
1. **Market Overview**: Overall market sentiment based on the group's performance
2. **Top Movers**: Biggest gainers and losers with key reasons
3. **Sector Themes**: Common patterns or trends across the watchlist
4. **Watch Items**: Stocks that need attention (unusual volume, extreme moves, near 52-week levels)
5. **Outlook**: Brief forward-looking summary

Keep the response under 400 words.`

// sevenRoleInstruction standardizes /ask analysis into seven specialist passes
// plus a final portfolio-manager synthesis. Keeping this in llm allows reuse
// by both UI and any future API surfaces.
const sevenRoleInstruction = `

Use this seven-role analysis workflow for single-stock and compare-style questions:

1. Market Analyst
- Summarize broad context (index trend, macro/rates sensitivity, sector tape).
- Output: market_regime (risk-on/risk-off/neutral), market_score [-2..2], confidence [0..1].

2. Fundamental Analyst
- Evaluate growth, profitability, balance-sheet quality, valuation, and estimate quality.
- Output: fundamental_score [-2..2], key_drivers[], valuation_view, confidence [0..1].

3. Technical Analyst
- Evaluate trend, momentum, volatility regime, and key levels.
- Use technical tools when available.
- Output: technical_score [-2..2], trend, momentum_state, key_levels[], invalidation_level, confidence [0..1].

4. Risk Analyst
- Identify concentration, event, liquidity, and downside risks.
- Output: risk_score [-2..2] (more negative = higher risk), top_risks[], risk_triggers[], confidence [0..1].

5. Sentiment/News Analyst
- Combine headlines + tone + novelty; separate facts from interpretation.
- Output: sentiment_score [-2..2], news_drivers[], novelty_assessment, durability (short/medium/long), confidence [0..1].

6. Strategy Analyst
- Evaluate alignment with user's investment strategies (value, growth, dividend, momentum, etc.).
- Consider position sizing constraints, sector concentration, correlation with existing holdings, beta fit.
- Output: strategy_score [-2..2], strategy_fit_summary, positioning_notes, confidence [0..1].

7. Portfolio Manager (Synthesis)
- Merge the six role outputs into one decision.
- Default weighting: Fundamentals 35%, Technicals 25%, Sentiment/News 15%, Strategy fit 15%, Market 5%, Risk overlay 5%.
- Adjust weighting down when a role has stale or low-confidence evidence.
- Output final stance and what would change your view.

Response contract (markdown):
- Use sections in this exact order:
	1) ## Market
	2) ## Fundamentals
	3) ## Technicals
	4) ## Risk
	5) ## Sentiment & News
	6) ## Thesis
	7) ## Catalysts
	8) ## Risks
	9) ## Valuation
	10) ## What Changed Since Last Report
	11) ## Factor Vote
	12) ## Final Recommendation
- In each non-synthesis role section, include one score line: Score: X/2, Confidence: Y.
- In ## What Changed Since Last Report, include explicit deltas (new risks, estimate revisions, sentiment shifts, and level changes) or "No material change".
- In ## Factor Vote, include one JSON block in a fenced code block with exactly this shape:
	{
		"market": {"score": 0.0, "confidence": 0.0},
		"fundamental": {"score": 0.0, "confidence": 0.0},
		"technical": {"score": 0.0, "confidence": 0.0},
		"risk": {"score": 0.0, "confidence": 0.0},
		"sentiment_news": {"score": 0.0, "confidence": 0.0},
		"strategy": {"score": 0.0, "confidence": 0.0},
		"weighted_total": 0.0,
		"verdict": "Bullish|Neutral|Bearish"
	}
- In ## Final Recommendation include:
	- Final verdict: Bullish / Neutral / Bearish
	- Time horizon: Trading / Swing / Long-term
	- Key levels or triggers
	- 2-3 concrete actions
- Keep it concise, data-driven, and explicit about uncertainty.`

const knowledgeMemoryInstruction = `

When knowledge-retrieval tools are available, use this memory workflow:
1) Before final synthesis, call search_llm_reports for the symbol to retrieve prior analyses.
2) Use prior findings to populate "What Changed Since Last Report" with concrete deltas.
3) After producing the final markdown report, call store_llm_report with:
	 - symbol
	 - title (include date and topic)
	 - markdown_content (the full final report)
	 - json_content (the Factor Vote JSON only)
This ensures auditable history and better follow-up analysis.`

// SummarySystemPrompt returns the system prompt for watchlist summary.
func SummarySystemPrompt() string {
	return summarySystemPrompt
}

// SevenRoleInstruction returns the role-based analysis contract for
// agentic /ask workflows.
func SevenRoleInstruction() string {
	return sevenRoleInstruction
}

// KnowledgeMemoryInstruction returns the workflow block for persistent
// report retrieval + storage when DB-backed tools are available.
func KnowledgeMemoryInstruction() string {
	return knowledgeMemoryInstruction
}

// DaySummary holds open/close for a single trading day.
type DaySummary struct {
	Date   string
	Open   float64
	Close  float64
	High   float64
	Low    float64
	Volume int64
}

// WeekSummary holds computed 1-week price statistics for one symbol.
type WeekSummary struct {
	WeekOpen      float64
	WeekHigh      float64
	WeekLow       float64
	WeekClose     float64
	WeekChangePct float64
	WeekVolume    int64
	Days          []DaySummary
}

// BuildSummaryPrompt constructs a prompt with all watchlist items' data,
// including 1-day and 1-week price information.
func BuildSummaryPrompt(groupName string, items []QuoteData, weekData map[string]*WeekSummary) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("Analyze this watchlist group: **%s** (%d symbols)\n\n", groupName, len(items)))

	for _, q := range items {
		sb.WriteString(fmt.Sprintf("### %s (%s)\n", q.Symbol, q.Name))
		sb.WriteString(fmt.Sprintf("Price: $%.2f (%.2f%% today)\n", q.Price, q.ChangePercent))

		// 1-Day data
		if q.Open > 0 {
			sb.WriteString(fmt.Sprintf("1D: Open $%.2f | High $%.2f | Low $%.2f | Close $%.2f\n",
				q.Open, q.DayHigh, q.DayLow, q.Price))
		}

		// 1-Week data
		if w, ok := weekData[q.Symbol]; ok && w != nil && w.WeekOpen > 0 {
			sb.WriteString(fmt.Sprintf("1W: Open $%.2f | High $%.2f | Low $%.2f | Close $%.2f (%.2f%%) | Vol %s\n",
				w.WeekOpen, w.WeekHigh, w.WeekLow, w.WeekClose, w.WeekChangePct, formatLargeNumber(w.WeekVolume)))
			for _, d := range w.Days {
				chg := 0.0
				if d.Open > 0 {
					chg = (d.Close - d.Open) / d.Open * 100
				}
				sb.WriteString(fmt.Sprintf("  %s: Open $%.2f | Close $%.2f (%.2f%%) | Vol %s\n", d.Date, d.Open, d.Close, chg, formatLargeNumber(d.Volume)))
			}
		}

		if q.MarketCap > 0 {
			sb.WriteString(fmt.Sprintf("Market Cap: %s\n", formatLargeNumber(q.MarketCap)))
		}
		if q.PE > 0 {
			sb.WriteString(fmt.Sprintf("P/E: %.1f", q.PE))
			if q.ForwardPE > 0 {
				sb.WriteString(fmt.Sprintf(" | Fwd P/E: %.1f", q.ForwardPE))
			}
			sb.WriteString("\n")
		}
		if q.Volume > 0 && q.AvgVolume > 0 {
			volRatio := float64(q.Volume) / float64(q.AvgVolume)
			if volRatio > 1.5 || volRatio < 0.5 {
				sb.WriteString(fmt.Sprintf("Volume: %d (%.1fx avg)\n", q.Volume, volRatio))
			}
		}
		if q.FiftyTwoWeekHigh > 0 && q.Price > 0 {
			pctFromHigh := (q.Price - q.FiftyTwoWeekHigh) / q.FiftyTwoWeekHigh * 100
			sb.WriteString(fmt.Sprintf("52W Range: $%.2f - $%.2f (%.1f%% from high)\n", q.FiftyTwoWeekLow, q.FiftyTwoWeekHigh, pctFromHigh))
		}
		sb.WriteString("\n")
	}

	return sb.String() + formatInstruction
}

// SystemPrompt returns the system prompt for financial analysis.
func SystemPrompt() string {
	return systemPrompt
}

// HistoricalPeriod holds revenue and earnings for one period.
type HistoricalPeriod struct {
	Date     string
	Revenue  int64
	Earnings int64
}

// EPSQuarter holds EPS data for one quarter.
type EPSQuarter struct {
	Quarter         string
	EPSActual       float64
	EPSEstimate     float64
	SurprisePercent float64
}

// BuildEarningsData appends historical revenue/earnings and EPS history
// to the base prompt for earnings analysis.
func BuildEarningsData(base string, quarterly, yearly []HistoricalPeriod, eps []EPSQuarter) string {
	var sb strings.Builder
	sb.WriteString(base)

	if len(quarterly) > 0 {
		sb.WriteString("\nQuarterly Revenue & Earnings:\n")
		for _, p := range quarterly {
			sb.WriteString(fmt.Sprintf("  %s: Revenue %s, Earnings %s\n",
				p.Date, formatLargeNumber(p.Revenue), formatLargeNumber(p.Earnings)))
		}
	}

	if len(yearly) > 0 {
		sb.WriteString("\nAnnual Revenue & Earnings:\n")
		for _, p := range yearly {
			sb.WriteString(fmt.Sprintf("  %s: Revenue %s, Earnings %s\n",
				p.Date, formatLargeNumber(p.Revenue), formatLargeNumber(p.Earnings)))
		}
	}

	if len(eps) > 0 {
		sb.WriteString("\nEPS History (recent quarters):\n")
		for _, e := range eps {
			sb.WriteString(fmt.Sprintf("  %s: Actual $%.2f vs Est $%.2f (surprise %.1f%%)\n",
				e.Quarter, e.EPSActual, e.EPSEstimate, e.SurprisePercent))
		}
	}

	return sb.String()
}

func formatLargeNumber(n int64) string {
	abs := n
	sign := ""
	if n < 0 {
		abs = -n
		sign = "-"
	}
	switch {
	case abs >= 1_000_000_000_000:
		return fmt.Sprintf("%s$%.1fT", sign, float64(abs)/1e12)
	case abs >= 1_000_000_000:
		return fmt.Sprintf("%s$%.1fB", sign, float64(abs)/1e9)
	case abs >= 1_000_000:
		return fmt.Sprintf("%s$%.1fM", sign, float64(abs)/1e6)
	default:
		return fmt.Sprintf("%s$%d", sign, abs)
	}
}
