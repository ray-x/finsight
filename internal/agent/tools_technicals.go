package agent

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"math"
	"strings"

	"github.com/ray-x/finsight/internal/chart"
	"github.com/ray-x/finsight/internal/llm"
	"github.com/ray-x/finsight/internal/yahoo"
)

// TechnicalsTool fetches chart data for a ticker, computes the standard
// algorithmic-trading indicator stack (EMA/SMA + cross, RSI, Bollinger,
// MACD, Stochastic, classic Pivot levels), and returns a compact JSON
// payload focused on the latest readings plus derived signal labels.
// Intended for LLM consumption — verbose raw series are deliberately
// omitted.
func TechnicalsTool(y *yahoo.Client) Tool {
	return Tool{
		Spec: llm.ToolSpec{
			Name: "get_technicals",
			Description: "Compute algorithmic-trading indicators for a ticker at a given timeframe: moving averages (SMA/EMA) with 9/26 cross state, RSI(14), Bollinger Bands (20,2) + %B, MACD (12/26/9) with signal cross, Stochastic (14,1,3) with %K/%D cross, and classic pivot-point support/resistance. Use this whenever the user asks about technicals, indicators, entry/exit levels, overbought/oversold conditions, momentum, or chart signals. `range` examples: 1d 5d 1mo 3mo 6mo 1y 2y 5y. `interval` examples: 1m 5m 15m 1h 1d 1wk. Default range=6mo, interval=1d.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"symbol":   map[string]any{"type": "string", "description": "Ticker symbol, e.g. \"NVDA\"."},
					"range":    map[string]any{"type": "string", "description": "History window. Default 6mo."},
					"interval": map[string]any{"type": "string", "description": "Bar size. Default 1d."},
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
			rng, err := optionalStringArg(args, "range")
			if err != nil {
				return "", err
			}
			if rng == "" {
				rng = "6mo"
			}
			interval, err := optionalStringArg(args, "interval")
			if err != nil {
				return "", err
			}
			if interval == "" {
				interval = "1d"
			}
			cd, err := y.GetChart(sym, rng, interval)
			if err != nil {
				return "", fmt.Errorf("chart fetch failed: %w", err)
			}
			if cd == nil || len(cd.Closes) == 0 {
				return "", fmt.Errorf("no chart data returned")
			}
			// For intraday intervals, fetch 1y-daily context to warm up
			// the moving-average / MACD / Bollinger lines across prior
			// sessions. The merged series treats each session as a
			// continuation of the last so indicators don't start blank
			// at market open.
			if isIntraday(interval) {
				if ctx, cerr := y.GetChart(sym, "1y", "1d"); cerr == nil && ctx != nil && len(ctx.Closes) > 0 {
					cd = mergeChartContext(ctx, cd)
				}
			}
			if len(cd.Closes) < 30 {
				return "", fmt.Errorf("insufficient chart data (need >=30 bars)")
			}
			payload := buildTechnicalsPayload(sym, rng, interval, cd)
			b, err := json.Marshal(payload)
			if err != nil {
				return "", fmt.Errorf("serialize technicals: %w", err)
			}
			return string(b), nil
		},
	}
}

// isIntraday reports whether interval is a sub-daily bar size.
func isIntraday(interval string) bool {
	switch interval {
	case "1d", "1wk", "1mo", "3mo":
		return false
	}
	return true
}

// mergeChartContext returns a new ChartData with context bars strictly
// older than primary prepended. Used to give indicators warmup data
// when primary is a short intraday window. Returns primary unchanged
// when no overlap-free prefix is found.
func mergeChartContext(ctx, primary *yahoo.ChartData) *yahoo.ChartData {
	if ctx == nil || primary == nil ||
		len(ctx.Timestamps) != len(ctx.Closes) ||
		len(primary.Timestamps) == 0 {
		return primary
	}
	start := primary.Timestamps[0]
	cut := 0
	for i, ts := range ctx.Timestamps {
		if ts < start {
			cut = i + 1
		} else {
			break
		}
	}
	if cut == 0 {
		return primary
	}
	out := &yahoo.ChartData{
		Timestamps: append(append([]int64{}, ctx.Timestamps[:cut]...), primary.Timestamps...),
		Closes:     append(append([]float64{}, ctx.Closes[:cut]...), primary.Closes...),
	}
	if len(ctx.Opens) == len(ctx.Closes) && len(primary.Opens) == len(primary.Closes) {
		out.Opens = append(append([]float64{}, ctx.Opens[:cut]...), primary.Opens...)
	}
	if len(ctx.Highs) == len(ctx.Closes) && len(primary.Highs) == len(primary.Closes) {
		out.Highs = append(append([]float64{}, ctx.Highs[:cut]...), primary.Highs...)
	}
	if len(ctx.Lows) == len(ctx.Closes) && len(primary.Lows) == len(primary.Closes) {
		out.Lows = append(append([]float64{}, ctx.Lows[:cut]...), primary.Lows...)
	}
	if len(ctx.Volumes) == len(ctx.Closes) && len(primary.Volumes) == len(primary.Closes) {
		out.Volumes = append(append([]int64{}, ctx.Volumes[:cut]...), primary.Volumes...)
	}
	return out
}

// buildTechnicalsPayload produces the JSON-ready structure the LLM sees.
func buildTechnicalsPayload(sym, rng, interval string, cd *yahoo.ChartData) map[string]any {
	closes := cd.Closes
	highs := cd.Highs
	lows := cd.Lows
	if len(highs) != len(closes) {
		highs = closes
	}
	if len(lows) != len(closes) {
		lows = closes
	}

	sma20 := chart.SMA(closes, 20)
	sma50 := chart.SMA(closes, 50)
	sma200 := chart.SMA(closes, 200)
	ema9 := chart.EMA(closes, 9)
	ema26 := chart.EMA(closes, 26)
	ema59 := chart.EMA(closes, 59)
	ema120 := chart.EMA(closes, 120)
	rsi := chart.RSI(closes, 14)
	bbU, bbM, bbL := chart.BollingerBands(closes, 20, 2)
	macd, sig, hist := chart.MACD(closes, 12, 26, 9)
	stK, stD := chart.Stochastic(highs, lows, closes, 14, 1, 3)

	last := closes[len(closes)-1]
	pivot := chart.Pivot{}
	if len(closes) >= 2 {
		pivot = chart.PivotPoints(highs[len(closes)-2], lows[len(closes)-2], closes[len(closes)-2])
	}

	emaCrossDir, emaCrossAgo := chart.CrossState(ema9, ema26)
	macdCrossDir, macdCrossAgo := chart.CrossState(macd, sig)
	smaCrossDir, smaCrossAgo := chart.CrossState(sma50, sma200)

	rsiLast := lastf(rsi)
	stKLast := lastf(stK)
	stDLast := lastf(stD)
	bbUL := lastf(bbU)
	bbML := lastf(bbM)
	bbLL := lastf(bbL)
	pctB := math.NaN()
	if !math.IsNaN(bbUL) && !math.IsNaN(bbLL) && bbUL != bbLL {
		pctB = (last - bbLL) / (bbUL - bbLL)
	}

	signals := []string{}
	// Trend
	switch {
	case emaCrossDir > 0:
		signals = append(signals, fmt.Sprintf("EMA9>EMA26 bullish cross %d bars ago", emaCrossAgo))
	case emaCrossDir < 0:
		signals = append(signals, fmt.Sprintf("EMA9<EMA26 bearish cross %d bars ago", emaCrossAgo))
	}
	switch {
	case smaCrossDir > 0:
		signals = append(signals, fmt.Sprintf("SMA50>SMA200 golden cross %d bars ago", smaCrossAgo))
	case smaCrossDir < 0:
		signals = append(signals, fmt.Sprintf("SMA50<SMA200 death cross %d bars ago", smaCrossAgo))
	}
	// RSI
	switch {
	case !math.IsNaN(rsiLast) && rsiLast >= 70:
		signals = append(signals, fmt.Sprintf("RSI14 %.1f overbought", rsiLast))
	case !math.IsNaN(rsiLast) && rsiLast <= 30:
		signals = append(signals, fmt.Sprintf("RSI14 %.1f oversold", rsiLast))
	}
	// Bollinger
	switch {
	case !math.IsNaN(pctB) && pctB >= 1:
		signals = append(signals, fmt.Sprintf("price above upper Bollinger band (%%B %.2f)", pctB))
	case !math.IsNaN(pctB) && pctB <= 0:
		signals = append(signals, fmt.Sprintf("price below lower Bollinger band (%%B %.2f)", pctB))
	}
	// MACD
	switch {
	case macdCrossDir > 0:
		signals = append(signals, fmt.Sprintf("MACD bullish signal cross %d bars ago", macdCrossAgo))
	case macdCrossDir < 0:
		signals = append(signals, fmt.Sprintf("MACD bearish signal cross %d bars ago", macdCrossAgo))
	}
	// Stoch
	switch {
	case !math.IsNaN(stKLast) && stKLast >= 80:
		signals = append(signals, fmt.Sprintf("Stoch %%K %.1f overbought", stKLast))
	case !math.IsNaN(stKLast) && stKLast <= 20:
		signals = append(signals, fmt.Sprintf("Stoch %%K %.1f oversold", stKLast))
	}
	if !math.IsNaN(stKLast) && !math.IsNaN(stDLast) {
		if stKLast > stDLast {
			signals = append(signals, "Stoch K above D")
		} else if stKLast < stDLast {
			signals = append(signals, "Stoch K below D")
		}
	}

	return map[string]any{
		"symbol":   sym,
		"range":    rng,
		"interval": interval,
		"bars":     len(closes),
		"last_close": last,
		"ma": map[string]any{
			"sma20":  roundOrNil(lastf(sma20)),
			"sma50":  roundOrNil(lastf(sma50)),
			"sma200": roundOrNil(lastf(sma200)),
			"ema9":   roundOrNil(lastf(ema9)),
			"ema26":  roundOrNil(lastf(ema26)),
			"ema59":  roundOrNil(lastf(ema59)),
			"ema120": roundOrNil(lastf(ema120)),
			"ema9_26_cross":  crossLabel(emaCrossDir, emaCrossAgo),
			"sma50_200_cross": crossLabel(smaCrossDir, smaCrossAgo),
		},
		"rsi14": roundOrNil(rsiLast),
		"bollinger": map[string]any{
			"upper": roundOrNil(bbUL),
			"mid":   roundOrNil(bbML),
			"lower": roundOrNil(bbLL),
			"pct_b": roundOrNil(pctB),
		},
		"macd": map[string]any{
			"line":   roundOrNil(lastf(macd)),
			"signal": roundOrNil(lastf(sig)),
			"hist":   roundOrNil(lastf(hist)),
			"cross":  crossLabel(macdCrossDir, macdCrossAgo),
		},
		"stochastic": map[string]any{
			"k": roundOrNil(stKLast),
			"d": roundOrNil(stDLast),
		},
		"pivot_classic": map[string]any{
			"p":  roundOrNil(pivot.P),
			"r1": roundOrNil(pivot.R1),
			"r2": roundOrNil(pivot.R2),
			"r3": roundOrNil(pivot.R3),
			"s1": roundOrNil(pivot.S1),
			"s2": roundOrNil(pivot.S2),
			"s3": roundOrNil(pivot.S3),
		},
		"signals": signals,
	}
}

func lastf(s []float64) float64 { v, _ := chart.LastValid(s); return v }

func roundOrNil(v float64) any {
	if math.IsNaN(v) {
		return nil
	}
	// Round to 4 decimals to keep JSON compact.
	return math.Round(v*10000) / 10000
}

func crossLabel(dir, ago int) string {
	switch {
	case dir > 0:
		return fmt.Sprintf("bullish %db", ago)
	case dir < 0:
		return fmt.Sprintf("bearish %db", ago)
	}
	return "none"
}
