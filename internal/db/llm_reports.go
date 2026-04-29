package db

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// UpsertLLMReport stores a new LLM-generated report and extracts sections.
func (db *DB) UpsertLLMReport(ctx context.Context, report *LLMReport, markdownContent string) error {
	tx, err := db.BeginTx(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			tx.Rollback()
		}
	}()

	// Insert report
	query := `
		INSERT INTO llm_reports (
			symbol, fiscal_period, title, question_hash, markdown_content, json_content, created_at, synced_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(symbol, fiscal_period, title, question_hash) DO UPDATE SET
			markdown_content = excluded.markdown_content,
			json_content = excluded.json_content,
			synced_at = CURRENT_TIMESTAMP
		RETURNING id
	`

	var reportID int64
	err = tx.QueryRowContext(ctx, query,
		report.Symbol, report.FiscalPeriod, report.Title, report.QuestionHash,
		markdownContent, report.JSON, report.CreatedAt, report.SyncedAt,
	).Scan(&reportID)
	if err != nil {
		return fmt.Errorf("failed to upsert report: %w", err)
	}

	// Delete existing sections for this report (if updating)
	delQuery := `DELETE FROM llm_report_sections WHERE report_id = ?`
	if _, err := tx.ExecContext(ctx, delQuery, reportID); err != nil {
		return fmt.Errorf("failed to delete old sections: %w", err)
	}

	// Extract and insert sections
	sections := extractMarkdownSections(markdownContent)
	for _, section := range sections {
		insectQuery := `
			INSERT INTO llm_report_sections (
				report_id, heading_level, heading_title, section_content, synced_at
			) VALUES (?, ?, ?, ?, ?)
		`
		if _, err := tx.ExecContext(ctx, insectQuery,
			reportID, section.HeadingLevel, section.HeadingTitle, section.SectionContent, time.Now(),
		); err != nil {
			return fmt.Errorf("failed to insert section: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// GetLLMReport retrieves a report by ID.
func (db *DB) GetLLMReport(ctx context.Context, id int64) (*LLMReport, error) {
	query := `
		SELECT
			id, symbol, fiscal_period, title, question_hash, markdown_content, json_content, created_at, synced_at
		FROM llm_reports
		WHERE id = ?
	`

	row := db.QueryRow(ctx, query, id)
	report := &LLMReport{}
	err := row.Scan(
		&report.ID, &report.Symbol, &report.FiscalPeriod, &report.Title, &report.QuestionHash,
		&report.Markdown, &report.JSON, &report.CreatedAt, &report.SyncedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get report: %w", err)
	}
	return report, nil
}

// GetLLMReports retrieves reports for a symbol.
func (db *DB) GetLLMReports(ctx context.Context, symbol string, limit int) ([]*LLMReport, error) {
	query := `
		SELECT
			id, symbol, fiscal_period, title, question_hash, markdown_content, json_content, created_at, synced_at
		FROM llm_reports
		WHERE symbol = ?
		ORDER BY created_at DESC
		LIMIT ?
	`

	rows, err := db.Query(ctx, query, symbol, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query reports: %w", err)
	}
	defer rows.Close()

	var reports []*LLMReport
	for rows.Next() {
		report := &LLMReport{}
		err := rows.Scan(
			&report.ID, &report.Symbol, &report.FiscalPeriod, &report.Title, &report.QuestionHash,
			&report.Markdown, &report.JSON, &report.CreatedAt, &report.SyncedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan report: %w", err)
		}
		reports = append(reports, report)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating reports: %w", err)
	}

	return reports, nil
}

// GetLLMReportSections retrieves all sections for a report.
func (db *DB) GetLLMReportSections(ctx context.Context, reportID int64) ([]*LLMReportSection, error) {
	query := `
		SELECT
			id, report_id, heading_level, heading_title, section_content, section_hash, synced_at
		FROM llm_report_sections
		WHERE report_id = ?
		ORDER BY id ASC
	`

	rows, err := db.Query(ctx, query, reportID)
	if err != nil {
		return nil, fmt.Errorf("failed to query sections: %w", err)
	}
	defer rows.Close()

	var sections []*LLMReportSection
	for rows.Next() {
		section := &LLMReportSection{}
		var sectionHash sql.NullString
		err := rows.Scan(
			&section.ID, &section.ReportID, &section.HeadingLevel, &section.HeadingTitle,
			&section.SectionContent, &sectionHash, &section.SyncedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan section: %w", err)
		}
		if sectionHash.Valid {
			section.SectionHash = sectionHash.String
		}
		sections = append(sections, section)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating sections: %w", err)
	}

	return sections, nil
}

// SearchLLMReports performs a full-text search across LLM reports and sections.
func (db *DB) SearchLLMReports(ctx context.Context, query string, limit int) ([]*FTSSearchResult, error) {
	ftsQuery := `
		SELECT s.id, rf.rank, r.symbol, r.title, s.heading_title
		FROM llm_reports_fts rf
		JOIN llm_report_sections s ON rf.rowid = s.id
		JOIN llm_reports r ON s.report_id = r.id
		WHERE llm_reports_fts MATCH ?
		ORDER BY rf.rank DESC
		LIMIT ?
	`

	rows, err := db.Query(ctx, ftsQuery, query, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to search reports: %w", err)
	}
	defer rows.Close()

	var results []*FTSSearchResult
	for rows.Next() {
		result := &FTSSearchResult{
			Context: make(map[string]string),
		}
		var symbol, title, headingTitle string
		err := rows.Scan(&result.ID, &result.Rank, &symbol, &title, &headingTitle)
		if err != nil {
			return nil, fmt.Errorf("failed to scan search result: %w", err)
		}
		result.Context["symbol"] = symbol
		result.Context["title"] = title
		result.Context["heading"] = headingTitle
		results = append(results, result)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating search results: %w", err)
	}

	return results, nil
}

// SearchLLMReportsBySymbol performs a full-text search for a specific symbol.
func (db *DB) SearchLLMReportsBySymbol(ctx context.Context, symbol, searchQuery string, limit int) ([]*FTSSearchResult, error) {
	// Build FTS query with symbol filter
	ftsQuery := fmt.Sprintf("symbol:%s %s", symbol, searchQuery)
	return db.SearchLLMReports(ctx, ftsQuery, limit)
}

// MarkdownSection represents a section extracted from markdown.
type markdownSection struct {
	HeadingLevel   int
	HeadingTitle   string
	SectionContent string
}

// extractMarkdownSections extracts sections from markdown by heading level (## and ###).
func extractMarkdownSections(content string) []markdownSection {
	var sections []markdownSection

	// Split by lines
	lines := strings.Split(content, "\n")

	var currentSection *markdownSection
	currentContent := []string{}

	for _, line := range lines {
		// Check for heading
		level, title := parseHeading(line)
		if level > 0 && (level == 2 || level == 3) {
			// Save previous section if any
			if currentSection != nil {
				currentSection.SectionContent = strings.TrimSpace(strings.Join(currentContent, "\n"))
				sections = append(sections, *currentSection)
				currentContent = []string{}
			}

			// Start new section
			currentSection = &markdownSection{
				HeadingLevel: level,
				HeadingTitle: title,
			}
		} else if currentSection != nil {
			currentContent = append(currentContent, line)
		}
	}

	// Save last section
	if currentSection != nil {
		currentSection.SectionContent = strings.TrimSpace(strings.Join(currentContent, "\n"))
		sections = append(sections, *currentSection)
	}

	return sections
}

// parseHeading extracts heading level and title from a markdown line.
func parseHeading(line string) (int, string) {
	line = strings.TrimSpace(line)

	// Match ## or ### at the start
	re := regexp.MustCompile(`^(#{2,3})\s+(.+)$`)
	matches := re.FindStringSubmatch(line)
	if len(matches) == 3 {
		level := len(matches[1])
		title := strings.TrimSpace(matches[2])
		return level, title
	}

	return 0, ""
}
