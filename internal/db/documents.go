package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"io"
)

// UpsertEarningsRecord inserts or updates an earnings record.
func (db *DB) UpsertEarningsRecord(ctx context.Context, record *EarningsRecord) error {
	query := `
		INSERT INTO earnings_records (
			symbol, fiscal_period, period_type, announcement_date,
			eps_actual, eps_estimate, revenue_actual, revenue_estimate,
			guidance_high, guidance_low, surprise_pct, raw_json, synced_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(symbol, fiscal_period) DO UPDATE SET
			period_type = excluded.period_type,
			announcement_date = excluded.announcement_date,
			eps_actual = excluded.eps_actual,
			eps_estimate = excluded.eps_estimate,
			revenue_actual = excluded.revenue_actual,
			revenue_estimate = excluded.revenue_estimate,
			guidance_high = excluded.guidance_high,
			guidance_low = excluded.guidance_low,
			surprise_pct = excluded.surprise_pct,
			raw_json = excluded.raw_json,
			synced_at = CURRENT_TIMESTAMP
	`

	_, err := db.Exec(ctx, query,
		record.Symbol, record.FiscalPeriod, record.PeriodType, record.AnnouncementDate,
		record.EPSActual, record.EPSEstimate, record.RevenueActual, record.RevenueEstimate,
		record.GuidanceHigh, record.GuidanceLow, record.SurprisePct, record.RawJSON, record.SyncedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to upsert earnings record: %w", err)
	}
	return nil
}

// GetEarningsRecord retrieves a specific earnings record.
func (db *DB) GetEarningsRecord(ctx context.Context, symbol, fiscalPeriod string) (*EarningsRecord, error) {
	query := `
		SELECT
			id, symbol, fiscal_period, period_type, announcement_date,
			eps_actual, eps_estimate, revenue_actual, revenue_estimate,
			guidance_high, guidance_low, surprise_pct, raw_json, synced_at
		FROM earnings_records
		WHERE symbol = ? AND fiscal_period = ?
	`

	row := db.QueryRow(ctx, query, symbol, fiscalPeriod)
	record := &EarningsRecord{}
	err := row.Scan(
		&record.ID, &record.Symbol, &record.FiscalPeriod, &record.PeriodType, &record.AnnouncementDate,
		&record.EPSActual, &record.EPSEstimate, &record.RevenueActual, &record.RevenueEstimate,
		&record.GuidanceHigh, &record.GuidanceLow, &record.SurprisePct, &record.RawJSON, &record.SyncedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get earnings record: %w", err)
	}
	return record, nil
}

// GetEarningsRecords retrieves recent earnings records for a symbol.
func (db *DB) GetEarningsRecords(ctx context.Context, symbol string, limit int) ([]*EarningsRecord, error) {
	query := `
		SELECT
			id, symbol, fiscal_period, period_type, announcement_date,
			eps_actual, eps_estimate, revenue_actual, revenue_estimate,
			guidance_high, guidance_low, surprise_pct, raw_json, synced_at
		FROM earnings_records
		WHERE symbol = ?
		ORDER BY fiscal_period DESC
		LIMIT ?
	`

	rows, err := db.Query(ctx, query, symbol, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query earnings records: %w", err)
	}
	defer rows.Close()

	var records []*EarningsRecord
	for rows.Next() {
		record := &EarningsRecord{}
		err := rows.Scan(
			&record.ID, &record.Symbol, &record.FiscalPeriod, &record.PeriodType, &record.AnnouncementDate,
			&record.EPSActual, &record.EPSEstimate, &record.RevenueActual, &record.RevenueEstimate,
			&record.GuidanceHigh, &record.GuidanceLow, &record.SurprisePct, &record.RawJSON, &record.SyncedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan earnings record: %w", err)
		}
		records = append(records, record)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating earnings records: %w", err)
	}

	return records, nil
}

// UpsertDocument inserts or updates a document (filing, transcript, etc.).
func (db *DB) UpsertDocument(ctx context.Context, doc *Document) error {
	// Compute content hash to detect duplicates
	hash := sha256.New()
	io.WriteString(hash, doc.Content)
	contentHash := fmt.Sprintf("%x", hash.Sum(nil))[:16]

	query := `
		INSERT INTO documents (
			symbol, document_type, fiscal_period, title, source, url,
			content, content_hash, metadata_json, synced_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(symbol, document_type, fiscal_period, content_hash) DO UPDATE SET
			title = excluded.title,
			url = excluded.url,
			metadata_json = excluded.metadata_json,
			synced_at = CURRENT_TIMESTAMP
	`

	_, err := db.Exec(ctx, query,
		doc.Symbol, doc.DocumentType, doc.FiscalPeriod, doc.Title, doc.Source, doc.URL,
		doc.Content, contentHash, doc.MetadataJSON, doc.SyncedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to upsert document: %w", err)
	}
	return nil
}

// GetDocument retrieves a document by ID.
func (db *DB) GetDocument(ctx context.Context, id int64) (*Document, error) {
	query := `
		SELECT
			id, symbol, document_type, fiscal_period, title, source, url,
			content, content_hash, metadata_json, synced_at
		FROM documents
		WHERE id = ?
	`

	row := db.QueryRow(ctx, query, id)
	doc := &Document{}
	err := row.Scan(
		&doc.ID, &doc.Symbol, &doc.DocumentType, &doc.FiscalPeriod, &doc.Title,
		&doc.Source, &doc.URL, &doc.Content, &doc.ContentHash, &doc.MetadataJSON, &doc.SyncedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get document: %w", err)
	}
	return doc, nil
}

// GetDocuments retrieves documents for a symbol and type.
func (db *DB) GetDocuments(ctx context.Context, symbol, documentType string, limit int) ([]*Document, error) {
	query := `
		SELECT
			id, symbol, document_type, fiscal_period, title, source, url,
			content, content_hash, metadata_json, synced_at
		FROM documents
		WHERE symbol = ? AND document_type = ?
		ORDER BY fiscal_period DESC
		LIMIT ?
	`

	rows, err := db.Query(ctx, query, symbol, documentType, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query documents: %w", err)
	}
	defer rows.Close()

	var docs []*Document
	for rows.Next() {
		doc := &Document{}
		err := rows.Scan(
			&doc.ID, &doc.Symbol, &doc.DocumentType, &doc.FiscalPeriod, &doc.Title,
			&doc.Source, &doc.URL, &doc.Content, &doc.ContentHash, &doc.MetadataJSON, &doc.SyncedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan document: %w", err)
		}
		docs = append(docs, doc)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating documents: %w", err)
	}

	return docs, nil
}

// SearchDocuments performs a full-text search across documents.
func (db *DB) SearchDocuments(ctx context.Context, query string, limit int) ([]*FTSSearchResult, error) {
	ftsQuery := `
		SELECT d.id, df.rank, d.symbol, d.document_type, d.fiscal_period, d.title
		FROM documents_fts df
		JOIN documents d ON df.rowid = d.id
		WHERE documents_fts MATCH ?
		ORDER BY df.rank DESC
		LIMIT ?
	`

	rows, err := db.Query(ctx, ftsQuery, query, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to search documents: %w", err)
	}
	defer rows.Close()

	var results []*FTSSearchResult
	for rows.Next() {
		result := &FTSSearchResult{
			Context: make(map[string]string),
		}
		var symbol, docType, period, title string
		err := rows.Scan(&result.ID, &result.Rank, &symbol, &docType, &period, &title)
		if err != nil {
			return nil, fmt.Errorf("failed to scan search result: %w", err)
		}
		result.Context["symbol"] = symbol
		result.Context["document_type"] = docType
		result.Context["fiscal_period"] = period
		result.Context["title"] = title
		results = append(results, result)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating search results: %w", err)
	}

	return results, nil
}

// SearchDocumentsBySymbol performs a full-text search for a specific symbol.
func (db *DB) SearchDocumentsBySymbol(ctx context.Context, symbol, searchQuery string, limit int) ([]*FTSSearchResult, error) {
	// Build FTS query with symbol filter
	ftsQuery := fmt.Sprintf("symbol:%s %s", symbol, searchQuery)

	return db.SearchDocuments(ctx, ftsQuery, limit)
}
