package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ray-x/finsight/internal/db"
	"github.com/ray-x/finsight/internal/llm"
	"github.com/ray-x/finsight/internal/yahoo"
)

// MarketSnapshotTool returns latest quote + key stats + technicals for a symbol.
// DB-first with optional refresh override.
func MarketSnapshotTool(kdb *db.DB, yc *yahoo.Client) Tool {
	return Tool{
		Spec: llm.ToolSpec{
			Name:        "get_market_snapshot",
			Description: "Get the latest market snapshot for a ticker including quote, key stats, 52-week range, and technical indicators. DB-first retrieval with optional refresh.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"symbol": map[string]any{
						"type":        "string",
						"description": "Stock ticker symbol (e.g., AAPL)",
					},
					"refresh": map[string]any{
						"type":        "boolean",
						"description": "Force live refresh from Yahoo API (default false)",
					},
				},
				"required": []string{"symbol"},
			},
		},
		Handler: func(ctx context.Context, args map[string]any) (string, error) {
			symbol, err := requiredStringArg(args, "symbol")
			if err != nil {
				return "", err
			}
			symbol = strings.ToUpper(symbol)

			refresh, _ := args["refresh"].(bool)

			// Try DB first unless forced refresh
			if !refresh {
				quote, err := kdb.GetQuoteLatest(ctx, symbol)
				if err == nil && quote != nil && time.Since(quote.SyncedAt) < 15*time.Minute {
					// Return summary
					return fmt.Sprintf("Symbol: %s, Price: $%.2f, Change: %.2f%%, PE: %.2f, 52W High: $%.2f, 52W Low: $%.2f",
						quote.Symbol, quote.Price, quote.ChangePct, quote.PERatio, quote.FiftyTwoWeekHigh, quote.FiftyTwoWeekLow), nil
				}
			}

			// Fetch fresh from Yahoo
			if yc == nil {
				return "", fmt.Errorf("yahoo client not configured")
			}

			quotes, err := yc.GetQuotes([]string{symbol})
			if err != nil {
				return "", fmt.Errorf("failed to fetch quote: %w", err)
			}

			if len(quotes) == 0 {
				return "", fmt.Errorf("symbol not found")
			}

			q := quotes[0]

			// Store in DB
			dbQuote := &db.QuoteLatest{
				Symbol:           q.Symbol,
				Price:            q.Price,
				Change:           q.Change,
				ChangePct:        q.ChangePercent,
				Open:             q.Open,
				High:             q.DayHigh,
				Low:              q.DayLow,
				Volume:           q.Volume,
				MarketCap:        q.MarketCap,
				PERatio:          q.PE,
				ForwardPE:        q.ForwardPE,
				DividendYield:    q.DividendYield,
				FiftyTwoWeekHigh: q.FiftyTwoWeekHigh,
				FiftyTwoWeekLow:  q.FiftyTwoWeekLow,
				Beta:             q.Beta,
				TrailingEPS:      q.EPS,
				PEGRatio:         q.PEG,
				QuoteSource:      "yahoo",
				QuoteUpdatedAt:   time.Now(),
				SyncedAt:         time.Now(),
			}
			_ = kdb.UpsertQuoteLatest(ctx, dbQuote)

			return fmt.Sprintf("Symbol: %s, Price: $%.2f, Change: %.2f%%, PE: %.2f, 52W High: $%.2f, 52W Low: $%.2f (refreshed)",
				q.Symbol, q.Price, q.ChangePercent, q.PE, q.FiftyTwoWeekHigh, q.FiftyTwoWeekLow), nil
		},
	}
}

// PriceHistoryTool retrieves historical quotes from the database.
func PriceHistoryTool(kdb *db.DB) Tool {
	return Tool{
		Spec: llm.ToolSpec{
			Name:        "get_price_history",
			Description: "Retrieve historical price and stats snapshots for a symbol from the database",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"symbol": map[string]any{
						"type":        "string",
						"description": "Stock ticker symbol",
					},
					"limit": map[string]any{
						"type":        "number",
						"description": "Number of historical records to retrieve (default 10)",
					},
				},
				"required": []string{"symbol"},
			},
		},
		Handler: func(ctx context.Context, args map[string]any) (string, error) {
			symbol, ok := args["symbol"].(string)
			if !ok {
				return "", fmt.Errorf("missing or invalid symbol")
			}

			limit := 10
			if l, ok := args["limit"].(float64); ok && l > 0 {
				limit = int(l)
			}

			history, err := kdb.GetQuoteHistory(ctx, symbol, limit)
			if err != nil {
				return "", fmt.Errorf("failed to retrieve history: %w", err)
			}

			if len(history) == 0 {
				return fmt.Sprintf("No historical data found for %s", symbol), nil
			}

			result := fmt.Sprintf("Price history for %s (%d records):\n", symbol, len(history))
			for _, h := range history {
				result += fmt.Sprintf("  %s: $%.2f (PE: %.2f)\n", h.RecordedAt.Format("2006-01-02"), h.Price, h.PERatio)
			}
			return result, nil
		},
	}
}

// EarningsHistoryTool retrieves earnings records for a symbol.
func EarningsHistoryTool(kdb *db.DB) Tool {
	return Tool{
		Spec: llm.ToolSpec{
			Name:        "get_earnings_history",
			Description: "Retrieve historical earnings records (EPS, revenue, surprises) for a symbol",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"symbol": map[string]any{
						"type":        "string",
						"description": "Stock ticker symbol",
					},
					"limit": map[string]any{
						"type":        "number",
						"description": "Number of earnings records to retrieve (default 8)",
					},
				},
				"required": []string{"symbol"},
			},
		},
		Handler: func(ctx context.Context, args map[string]any) (string, error) {
			symbol, ok := args["symbol"].(string)
			if !ok {
				return "", fmt.Errorf("missing or invalid symbol")
			}

			limit := 8
			if l, ok := args["limit"].(float64); ok && l > 0 {
				limit = int(l)
			}

			records, err := kdb.GetEarningsRecords(ctx, symbol, limit)
			if err != nil {
				return "", fmt.Errorf("failed to retrieve earnings: %w", err)
			}

			if len(records) == 0 {
				return fmt.Sprintf("No earnings records found for %s", symbol), nil
			}

			result := fmt.Sprintf("Earnings history for %s (%d records):\n", symbol, len(records))
			for _, r := range records {
				eps := fmt.Sprintf("EPS: %.2f (est: %.2f)", r.EPSActual, r.EPSEstimate)
				if r.SurprisePct != 0 {
					eps += fmt.Sprintf(" [%.1f%% surprise]", r.SurprisePct)
				}
				result += fmt.Sprintf("  %s (%s): %s\n", r.FiscalPeriod, r.PeriodType, eps)
			}
			return result, nil
		},
	}
}

// SearchDocumentsTool performs full-text search over earnings/transcripts.
func SearchDocumentsTool(kdb *db.DB) Tool {
	return Tool{
		Spec: llm.ToolSpec{
			Name:        "search_documents",
			Description: "Full-text search over earnings filings and transcripts",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "Search query (keywords or FTS5 syntax)",
					},
					"symbol": map[string]any{
						"type":        "string",
						"description": "Optional: limit search to a specific symbol",
					},
					"limit": map[string]any{
						"type":        "number",
						"description": "Number of results (default 5)",
					},
				},
				"required": []string{"query"},
			},
		},
		Handler: func(ctx context.Context, args map[string]any) (string, error) {
			query, ok := args["query"].(string)
			if !ok {
				return "", fmt.Errorf("missing or invalid query")
			}

			limit := 5
			if l, ok := args["limit"].(float64); ok && l > 0 {
				limit = int(l)
			}

			var results []*db.FTSSearchResult
			var err error

			if symbol, ok := args["symbol"].(string); ok && symbol != "" {
				results, err = kdb.SearchDocumentsBySymbol(ctx, symbol, query, limit)
			} else {
				results, err = kdb.SearchDocuments(ctx, query, limit)
			}

			if err != nil {
				return "", fmt.Errorf("search failed: %w", err)
			}

			if len(results) == 0 {
				return fmt.Sprintf("No documents found matching query: %s", query), nil
			}

			response := fmt.Sprintf("Found %d document(s) matching '%s':\n", len(results), query)
			for _, r := range results {
				sym := r.Context["symbol"]
				docType := r.Context["document_type"]
				title := r.Context["title"]
				response += fmt.Sprintf("  - [%s] %s (%s)\n", sym, title, docType)
			}
			return response, nil
		},
	}
}

// StoreLLMReportTool stores an AI-generated analysis report.
func StoreLLMReportTool(kdb *db.DB) Tool {
	return Tool{
		Spec: llm.ToolSpec{
			Name:        "store_llm_report",
			Description: "Store an AI-generated analysis report with markdown content and optional JSON structure",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"symbol": map[string]any{
						"type":        "string",
						"description": "Stock ticker symbol",
					},
					"title": map[string]any{
						"type":        "string",
						"description": "Report title (e.g., 'Q4 Earnings Analysis')",
					},
					"markdown_content": map[string]any{
						"type":        "string",
						"description": "Report in markdown format (can include ## and ### section headings)",
					},
					"fiscal_period": map[string]any{
						"type":        "string",
						"description": "Optional fiscal period (e.g., '2025Q1')",
					},
					"json_content": map[string]any{
						"type":        "string",
						"description": "Optional structured JSON data",
					},
				},
				"required": []string{"symbol", "title", "markdown_content"},
			},
		},
		Handler: func(ctx context.Context, args map[string]any) (string, error) {
			symbol, ok := args["symbol"].(string)
			if !ok {
				return "", fmt.Errorf("missing or invalid symbol")
			}

			title, ok := args["title"].(string)
			if !ok {
				return "", fmt.Errorf("missing or invalid title")
			}

			markdown, ok := args["markdown_content"].(string)
			if !ok {
				return "", fmt.Errorf("missing or invalid markdown_content")
			}

			fiscalPeriod, _ := args["fiscal_period"].(string)
			jsonContent, _ := args["json_content"].(string)

			report := &db.LLMReport{
				Symbol:       symbol,
				FiscalPeriod: fiscalPeriod,
				Title:        title,
				Markdown:     markdown,
				JSON:         jsonContent,
				CreatedAt:    time.Now(),
				SyncedAt:     time.Now(),
			}

			err := kdb.UpsertLLMReport(ctx, report, markdown)
			if err != nil {
				return "", fmt.Errorf("failed to store report: %w", err)
			}

			return fmt.Sprintf("Report '%s' stored successfully for %s (sections extracted and indexed)", title, symbol), nil
		},
	}
}

// SearchLLMReportsTool performs full-text search over AI-generated reports.
func SearchLLMReportsTool(kdb *db.DB) Tool {
	return Tool{
		Spec: llm.ToolSpec{
			Name:        "search_llm_reports",
			Description: "Full-text search over AI-generated analysis reports and their sections",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{
						"type":        "string",
						"description": "Search query",
					},
					"symbol": map[string]any{
						"type":        "string",
						"description": "Optional: limit search to a specific symbol",
					},
					"limit": map[string]any{
						"type":        "number",
						"description": "Number of results (default 5)",
					},
				},
				"required": []string{"query"},
			},
		},
		Handler: func(ctx context.Context, args map[string]any) (string, error) {
			query, ok := args["query"].(string)
			if !ok {
				return "", fmt.Errorf("missing or invalid query")
			}

			limit := 5
			if l, ok := args["limit"].(float64); ok && l > 0 {
				limit = int(l)
			}

			var results []*db.FTSSearchResult
			var err error

			if symbol, ok := args["symbol"].(string); ok && symbol != "" {
				results, err = kdb.SearchLLMReportsBySymbol(ctx, symbol, query, limit)
			} else {
				results, err = kdb.SearchLLMReports(ctx, query, limit)
			}

			if err != nil {
				return "", fmt.Errorf("search failed: %w", err)
			}

			if len(results) == 0 {
				return fmt.Sprintf("No reports found matching query: %s", query), nil
			}

			response := fmt.Sprintf("Found %d report section(s) matching '%s':\n", len(results), query)
			for _, r := range results {
				sym := r.Context["symbol"]
				title := r.Context["title"]
				heading := r.Context["heading"]
				response += fmt.Sprintf("  - [%s] %s > %s\n", sym, title, heading)
			}
			return response, nil
		},
	}
}

// AddRetrievalTools adds all retrieval and storage tools to the tool set.
func AddRetrievalTools(d Deps, tools []Tool) []Tool {
	if d.KnowledgeDB == nil {
		return tools
	}

	tools = append(tools, MarketSnapshotTool(d.KnowledgeDB, d.Yahoo))
	tools = append(tools, PriceHistoryTool(d.KnowledgeDB))
	tools = append(tools, EarningsHistoryTool(d.KnowledgeDB))
	tools = append(tools, SearchDocumentsTool(d.KnowledgeDB))
	tools = append(tools, StoreLLMReportTool(d.KnowledgeDB))
	tools = append(tools, SearchLLMReportsTool(d.KnowledgeDB))

	return tools
}
