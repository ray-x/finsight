package llm

import (
	"fmt"
	"math"
	"sort"
	"strings"
)

// PortfolioSnapshot is a compact per-position view used to build the
// portfolio context block in prompts.
type PortfolioSnapshot struct {
	Symbol          string
	Name            string
	Position        float64
	OpenPrice       float64
	Price           float64
	PreviousClose   float64
	DayOpen         float64
	DailyChange     float64
	DailyChangePct  float64
	MarketValue     float64
	DailyPL         float64
	UnrealizedPL    float64
	UnrealizedPLPct float64
	WeightPct       float64
	Currency        string
	Note            string
}

const portfolioSystemPrompt = `You are a pragmatic portfolio risk coach. Given a user's full portfolio and (optionally) a focus position, provide specific, actionable guidance.

Rules:
1. Ground every recommendation in the data provided. Cite concrete numbers (prices, %, weights).
2. Consider portfolio-level effects: concentration, sector/theme overlap, and how a change in the focus position would impact overall risk/return.
3. Be concrete: suggest price levels for take-profit, add, trim, or stop when relevant.
4. Flag key risks (earnings, macro, valuation, concentration) briefly.
5. Acknowledge uncertainty; avoid overconfident "buy/sell" calls when data is insufficient.
6. This is informational analysis, not financial advice. Keep tone professional and concise.`

// PortfolioSystemPrompt returns the system prompt for the portfolio advisor.
func PortfolioSystemPrompt() string { return portfolioSystemPrompt }

// BuildPortfolioContext returns a compact markdown table of the entire
// portfolio suitable for embedding in a prompt.
func BuildPortfolioContext(snaps []PortfolioSnapshot) string {
	if len(snaps) == 0 {
		return "(portfolio is empty)"
	}
	// Sort by weight desc for stable, informative ordering.
	sorted := make([]PortfolioSnapshot, len(snaps))
	copy(sorted, snaps)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].WeightPct > sorted[j].WeightPct })

	var sb strings.Builder
	var totalMV, totalDayPL, totalUnreal float64
	for _, s := range sorted {
		totalMV += s.MarketValue
		totalDayPL += s.DailyPL
		totalUnreal += s.UnrealizedPL
	}

	sb.WriteString("| Symbol | Shares | Open | Last | Day % | Unreal % | Weight % | Mkt Value |\n")
	sb.WriteString("|---|---:|---:|---:|---:|---:|---:|---:|\n")
	for _, s := range sorted {
		open := "—"
		unreal := "—"
		if s.OpenPrice > 0 {
			open = fmt.Sprintf("%.2f", s.OpenPrice)
			unreal = fmt.Sprintf("%+.2f%%", s.UnrealizedPLPct)
		}
		name := s.Name
		if name == "" || name == s.Symbol {
			name = ""
		} else {
			name = fmt.Sprintf(" (%s)", truncateStr(name, 24))
		}
		sb.WriteString(fmt.Sprintf("| %s%s | %s | %s | %.2f | %+.2f%% | %s | %.1f%% | %s |\n",
			s.Symbol, name,
			formatShares(s.Position),
			open,
			s.Price,
			s.DailyChangePct,
			unreal,
			s.WeightPct,
			formatMoney(s.MarketValue),
		))
	}
	sb.WriteString(fmt.Sprintf("\n**Totals**: Market Value %s · Day P/L %+s · Unrealized P/L %+s\n",
		formatMoney(totalMV), formatMoney(totalDayPL), formatMoney(totalUnreal)))
	return sb.String()
}

// BuildPortfolioAdvicePrompt focuses on a single symbol while providing
// the whole portfolio as context so the model can weigh allocation effects.
func BuildPortfolioAdvicePrompt(focus string, snaps []PortfolioSnapshot) string {
	ctx := BuildPortfolioContext(snaps)
	var focusSnap *PortfolioSnapshot
	for i := range snaps {
		if snaps[i].Symbol == focus {
			focusSnap = &snaps[i]
			break
		}
	}
	var sb strings.Builder
	sb.WriteString("## Portfolio\n")
	sb.WriteString(ctx)
	sb.WriteString("\n\n## Focus position\n")
	if focusSnap != nil {
		sb.WriteString(fmt.Sprintf("Analyse **%s** (%s) at last price %.2f.\n", focusSnap.Symbol, focusSnap.Name, focusSnap.Price))
		if focusSnap.OpenPrice > 0 {
			sb.WriteString(fmt.Sprintf("Entry: %.2f · Unrealized: %+.2f%% (%s)\n", focusSnap.OpenPrice, focusSnap.UnrealizedPLPct, formatMoney(focusSnap.UnrealizedPL)))
		} else {
			sb.WriteString("No entry price set.\n")
		}
		sb.WriteString(fmt.Sprintf("Shares: %s · Portfolio weight: %.1f%%\n", formatShares(focusSnap.Position), focusSnap.WeightPct))
	} else {
		sb.WriteString(fmt.Sprintf("Focus symbol: %s (not currently held)\n", focus))
	}
	sb.WriteString("\n## Questions\n")
	sb.WriteString("1. Should the user take profit, add, trim, hold, or stop? Give concrete price levels.\n")
	sb.WriteString("2. How does the focus position affect overall concentration and risk?\n")
	sb.WriteString("3. What are the 2–3 key risks to this position right now?\n")
	sb.WriteString("4. One-line verdict: Take profit / Add / Hold / Trim / Exit.\n")
	sb.WriteString(formatInstruction)
	return sb.String()
}

// BuildPortfolioReviewPrompt asks for a holistic review of the portfolio.
func BuildPortfolioReviewPrompt(snaps []PortfolioSnapshot) string {
	ctx := BuildPortfolioContext(snaps)
	var sb strings.Builder
	sb.WriteString("## Portfolio\n")
	sb.WriteString(ctx)
	sb.WriteString("\n## Task\n")
	sb.WriteString("Review this portfolio as a whole. Cover:\n")
	sb.WriteString("1. **Allocation**: concentration, largest exposures, overlap/diversification.\n")
	sb.WriteString("2. **Performance**: which positions are pulling weight vs dragging.\n")
	sb.WriteString("3. **Risks**: correlation, sector/theme clustering, macro or earnings risks.\n")
	sb.WriteString("4. **Actions**: 3–5 prioritised, concrete moves (trim, add, rebalance, hedge).\n")
	sb.WriteString("5. **Verdict**: overall stance (Defensive / Balanced / Aggressive) with a one-line rationale.\n")
	sb.WriteString(formatInstruction)
	return sb.String()
}

func formatShares(v float64) string {
	if v == math.Trunc(v) {
		return fmt.Sprintf("%d", int64(v))
	}
	return fmt.Sprintf("%.4f", v)
}

func formatMoney(v float64) string {
	neg := v < 0
	if neg {
		v = -v
	}
	out := fmt.Sprintf("$%.2f", v)
	if v >= 1e9 {
		out = fmt.Sprintf("$%.2fB", v/1e9)
	} else if v >= 1e6 {
		out = fmt.Sprintf("$%.2fM", v/1e6)
	} else if v >= 1e3 {
		out = fmt.Sprintf("$%.2fK", v/1e3)
	}
	if neg {
		out = "-" + out
	}
	return out
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}
