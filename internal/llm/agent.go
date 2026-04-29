package llm

import (
	"context"
	"fmt"
	"strings"

	"github.com/ray-x/finsight/internal/edgar"
	"github.com/ray-x/finsight/internal/logger"
)

const earningsSystemPrompt = `You are an expert financial analyst reviewing a company's quarterly SEC filing (10-Q or 10-K).
Provide a comprehensive earnings analysis covering:

1. **Revenue & Earnings**: Key numbers, YoY and QoQ trends
2. **Margins**: Gross, operating, and net margins — improving or declining?
3. **Guidance & Outlook**: Any forward guidance, management commentary on future quarters
4. **Risk Factors**: Notable risks or concerns mentioned in the filing
5. **Key Highlights**: Significant events (acquisitions, new products, regulatory changes)
6. **Financial Health**: Cash position, debt levels, cash flow trends
7. **Verdict**: Buy / Hold / Sell recommendation with key reasoning

Compare current results to prior periods where data is available.
Keep the response under 500 words.`

// EarningsReport holds the result of an earnings analysis.
type EarningsReport struct {
	Symbol     string
	FilingForm string // "10-Q" or "10-K"
	FilingDate string
	Analysis   string
}

// AnalyzeEarnings fetches the latest quarterly filing from EDGAR,
// combines it with Yahoo Finance data, and sends it to the LLM for analysis.
func (c *Client) AnalyzeEarnings(ctx context.Context, edgarClient *edgar.Client, symbol string, yahooData string) (*EarningsReport, error) {
	logger.Log("AnalyzeEarnings: symbol=%s, yahooData_len=%d, edgarClient=%v, contextTokens=%d", symbol, len(yahooData), edgarClient != nil, c.cfg.ContextTokens)

	if edgarClient == nil {
		return c.analyzeYahooOnly(ctx, symbol, yahooData, fmt.Errorf("EDGAR disabled (no edgar_email configured)"))
	}

	// Fetch latest quarterly filing
	filing, err := edgarClient.GetLatestQuarterlyFiling(symbol)
	if err != nil {
		logger.Log("EDGAR filing lookup failed for %s: %v", symbol, err)
		return c.analyzeYahooOnly(ctx, symbol, yahooData, err)
	}

	logger.Log("EDGAR filing found: %s %s (%s)", symbol, filing.Form, filing.FilingDate)

	// Compute filing text limit from context window.
	// Reserve ~2K tokens for system prompt + response; use ~75% of remaining for filing.
	// Rough estimate: 1 token ≈ 4 chars.
	reservedTokens := 2048 + 8192 // system prompt + max response tokens
	availableTokens := c.cfg.ContextTokens - reservedTokens
	if availableTokens < 8192 {
		availableTokens = 8192
	}
	// Subtract Yahoo data (in tokens)
	yahooTokens := len(yahooData) / 4
	filingTokens := availableTokens - yahooTokens
	if filingTokens < 8192 {
		filingTokens = 8192
	}
	maxFilingChars := filingTokens * 4
	logger.Log("Filing text limit: context=%d tokens, available=%d tokens, filing_limit=%d chars",
		c.cfg.ContextTokens, availableTokens, maxFilingChars)

	filingText, err := edgarClient.FetchFilingText(symbol, *filing, maxFilingChars)
	if err != nil {
		logger.Log("EDGAR filing text fetch failed: %v", err)
		return c.analyzeYahooOnly(ctx, symbol, yahooData, err)
	}

	logger.Log("EDGAR filing text: %d chars, preview: %.500s", len(filingText.Text), filingText.Text)

	// Build the user prompt combining EDGAR + Yahoo data
	userPrompt := buildEarningsPrompt(symbol, filingText, yahooData) + formatInstruction
	logger.Log("Earnings prompt: total_len=%d, filing_len=%d, yahoo_len=%d", len(userPrompt), len(filingText.Text), len(yahooData))

	analysis, err := c.Chat(ctx, earningsSystemPrompt, userPrompt)
	if err != nil {
		return nil, fmt.Errorf("LLM analysis failed: %w", err)
	}

	logger.Log("AnalyzeEarnings: LLM response for %s (%d chars): %.500s", symbol, len(analysis), analysis)

	return &EarningsReport{
		Symbol:     symbol,
		FilingForm: filing.Form,
		FilingDate: filing.FilingDate,
		Analysis:   analysis,
	}, nil
}

func (c *Client) analyzeYahooOnly(ctx context.Context, symbol, yahooData string, edgarErr error) (*EarningsReport, error) {
	if yahooData == "" {
		return nil, fmt.Errorf("no data available: EDGAR: %v, no Yahoo data", edgarErr)
	}

	logger.Log("analyzeYahooOnly: symbol=%s, edgarErr=%v, yahooData_len=%d", symbol, edgarErr, len(yahooData))
	logger.Log("Yahoo data preview: %.500s", yahooData)

	prompt := fmt.Sprintf("Analyze the latest earnings for %s.\n\n"+
		"Note: SEC EDGAR filing was not available (%v). Analysis is based on Yahoo Finance data only.\n\n"+
		"Yahoo Finance Data:\n%s", symbol, edgarErr, yahooData) + formatInstruction

	analysis, err := c.Chat(ctx, earningsSystemPrompt, prompt)
	if err != nil {
		return nil, fmt.Errorf("LLM analysis failed: %w", err)
	}

	logger.Log("analyzeYahooOnly: LLM response for %s (%d chars): %.500s", symbol, len(analysis), analysis)

	return &EarningsReport{
		Symbol:   symbol,
		Analysis: analysis,
	}, nil
}

func buildEarningsPrompt(symbol string, filingText *edgar.FilingText, yahooData string) string {
	var sb strings.Builder

	sb.WriteString(fmt.Sprintf("Analyze the latest earnings for %s.\n\n", symbol))

	sb.WriteString(fmt.Sprintf("=== SEC Filing: %s (filed %s) ===\n",
		filingText.Filing.Form, filingText.Filing.FilingDate))
	sb.WriteString(filingText.Text)
	sb.WriteString("\n\n")

	if yahooData != "" {
		sb.WriteString("=== Yahoo Finance Data (current metrics) ===\n")
		sb.WriteString(yahooData)
		sb.WriteString("\n")
	}

	sb.WriteString(formatInstruction)

	return sb.String()
}
