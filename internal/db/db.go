package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const (
	// Schema version for migrations
	schemaVersion = 1

	// TTL constants (in seconds)
	IntradayBarTTL = 15 * 60 // 15 minutes
	DailyBarTTL    = 0       // Never expire
	WeeklyBarTTL   = 0       // Never expire
	MonthlyBarTTL  = 0       // Never expire
	StatsTTL       = 0       // Never expire
	EarningsTTL    = 0       // Never expire
	DocumentsTTL   = 0       // Never expire
	LLMReportsTTL  = 0       // Never expire
)

// DB wraps SQLite connection with schema management and query helpers.
type DB struct {
	conn *sql.DB
	mu   sync.RWMutex
}

// Open opens or creates a SQLite database at the given path.
func Open(dbPath string) (*DB, error) {
	// Ensure directory exists
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create db directory: %w", err)
	}

	// Open connection with pragmas for performance and safety
	connStr := fmt.Sprintf("file:%s?cache=shared&mode=rwc&journal=WAL&timeout=5s", dbPath)
	conn, err := sql.Open("sqlite", connStr)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Test connection
	if err := conn.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	db := &DB{conn: conn}

	// Initialize schema
	if err := db.initSchema(); err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to initialize schema: %w", err)
	}

	return db, nil
}

// Close closes the database connection.
func (db *DB) Close() error {
	db.mu.Lock()
	defer db.mu.Unlock()
	if db.conn != nil {
		return db.conn.Close()
	}
	return nil
}

// initSchema creates all tables and indices if they don't exist, applying migrations as needed.
func (db *DB) initSchema() error {
	// Check current schema version
	var version int
	err := db.conn.QueryRow("PRAGMA user_version").Scan(&version)
	if err != nil {
		return fmt.Errorf("failed to read schema version: %w", err)
	}

	// Apply migrations only if needed
	if version < schemaVersion {
		if err := db.migrate(version); err != nil {
			return err
		}

		// Update schema version
		if _, err := db.conn.Exec(fmt.Sprintf("PRAGMA user_version = %d", schemaVersion)); err != nil {
			return fmt.Errorf("failed to update schema version: %w", err)
		}
	}

	return nil
}

// migrate applies schema migrations from the current version to schemaVersion.
// Pre-release: a single consolidated initial schema. Once released,
// additive migrations should be appended under new schema versions.
func (db *DB) migrate(from int) error {
	if from >= schemaVersion {
		return nil
	}
	return db.migrateInitial()
}

// migrateInitial creates the full initial schema.
func (db *DB) migrateInitial() error {
	migrations := []string{
		// quotes_latest: latest quote and key stats per symbol
		`CREATE TABLE IF NOT EXISTS quotes_latest (
			symbol TEXT PRIMARY KEY,
			price REAL,
			change REAL,
			change_pct REAL,
			open REAL,
			high REAL,
			low REAL,
			volume INTEGER,
			market_cap INTEGER,
			pe_ratio REAL,
			forward_pe REAL,
			dividend_yield REAL,
			fifty_two_week_high REAL,
			fifty_two_week_low REAL,
			fifty_two_week_change REAL,
			fifty_two_week_change_pct REAL,
			two_hundred_day_avg REAL,
			two_hundred_day_avg_change REAL,
			two_hundred_day_avg_change_pct REAL,
			fifty_day_avg REAL,
			fifty_day_avg_change REAL,
			fifty_day_avg_change_pct REAL,
			beta REAL,
			trailing_eps REAL,
			forward_eps REAL,
			peg_ratio REAL,
			eps_surprise REAL,
			eps_surprise_pct REAL,
			target_mean_price REAL,
			quote_source TEXT,
			quote_updated_at DATETIME,
			pe_changed_at DATETIME,
			forward_pe_changed_at DATETIME,
			stats_changed_at DATETIME,
			synced_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,

		// quotes_history: time-series snapshots of quotes for audit and change tracking
		`CREATE TABLE IF NOT EXISTS quotes_history (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			symbol TEXT NOT NULL,
			price REAL,
			change REAL,
			change_pct REAL,
			pe_ratio REAL,
			forward_pe REAL,
			dividend_yield REAL,
			fifty_two_week_high REAL,
			fifty_two_week_low REAL,
			trailing_eps REAL,
			forward_eps REAL,
			target_mean_price REAL,
			recorded_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (symbol) REFERENCES quotes_latest(symbol)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_quotes_history_symbol_recorded 
			ON quotes_history(symbol, recorded_at DESC)`,

		// price_bars: OHLCV bars with interval and expiry tracking
		`CREATE TABLE IF NOT EXISTS price_bars (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			symbol TEXT NOT NULL,
			interval TEXT NOT NULL,
			open_time DATETIME NOT NULL,
			open REAL NOT NULL,
			high REAL NOT NULL,
			low REAL NOT NULL,
			close REAL NOT NULL,
			volume INTEGER,
			expiry_seconds INTEGER,
			expires_at DATETIME,
			synced_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(symbol, interval, open_time)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_price_bars_symbol_interval_time 
			ON price_bars(symbol, interval, open_time DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_price_bars_expires_at 
			ON price_bars(expires_at) WHERE expires_at IS NOT NULL`,

		// earnings_records: structured earnings history (quarterly, annual, guidance)
		`CREATE TABLE IF NOT EXISTS earnings_records (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			symbol TEXT NOT NULL,
			fiscal_period TEXT NOT NULL,
			period_type TEXT,
			announcement_date DATETIME,
			eps_actual REAL,
			eps_estimate REAL,
			revenue_actual REAL,
			revenue_estimate REAL,
			guidance_high REAL,
			guidance_low REAL,
			surprise_pct REAL,
			raw_json TEXT,
			synced_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(symbol, fiscal_period)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_earnings_records_symbol_period 
			ON earnings_records(symbol, fiscal_period DESC)`,

		// documents: full-text filings, transcripts, and reports
		`CREATE TABLE IF NOT EXISTS documents (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			symbol TEXT NOT NULL,
			document_type TEXT NOT NULL,
			fiscal_period TEXT,
			title TEXT,
			source TEXT,
			url TEXT,
			content TEXT NOT NULL,
			content_hash TEXT,
			metadata_json TEXT,
			synced_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(symbol, document_type, fiscal_period, content_hash)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_documents_symbol_type_period 
			ON documents(symbol, document_type, fiscal_period)`,

		// FTS5 virtual table for documents (content-linked)
		`CREATE VIRTUAL TABLE IF NOT EXISTS documents_fts USING fts5(
			symbol,
			document_type,
			fiscal_period,
			title,
			content,
			content='documents',
			content_rowid='id'
		)`,

		// Triggers to keep FTS index in sync with documents table
		`CREATE TRIGGER IF NOT EXISTS documents_ai AFTER INSERT ON documents BEGIN
			INSERT INTO documents_fts(rowid, symbol, document_type, fiscal_period, title, content)
			VALUES (new.id, new.symbol, new.document_type, new.fiscal_period, new.title, new.content);
		END`,
		`CREATE TRIGGER IF NOT EXISTS documents_ad AFTER DELETE ON documents BEGIN
			INSERT INTO documents_fts(documents_fts, rowid, symbol, document_type, fiscal_period, title, content)
			VALUES('delete', old.id, old.symbol, old.document_type, old.fiscal_period, old.title, old.content);
		END`,

		// llm_reports: markdown and structured JSON reports per symbol/month
		`CREATE TABLE IF NOT EXISTS llm_reports (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			symbol TEXT NOT NULL,
			fiscal_period TEXT,
			title TEXT NOT NULL,
			question_hash TEXT,
			markdown_content TEXT NOT NULL,
			json_content TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			synced_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(symbol, fiscal_period, title, question_hash)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_llm_reports_symbol_period 
			ON llm_reports(symbol, fiscal_period)`,

		// llm_report_sections: extracted sections from markdown by heading level
		`CREATE TABLE IF NOT EXISTS llm_report_sections (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			report_id INTEGER NOT NULL,
			heading_level INTEGER,
			heading_title TEXT NOT NULL,
			section_content TEXT NOT NULL,
			section_hash TEXT,
			synced_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (report_id) REFERENCES llm_reports(id) ON DELETE CASCADE,
			UNIQUE(report_id, heading_level, heading_title)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_llm_report_sections_report_id 
			ON llm_report_sections(report_id)`,

		// FTS5 virtual table for LLM reports (content-linked)
		`CREATE VIRTUAL TABLE IF NOT EXISTS llm_reports_fts USING fts5(
			symbol,
			title,
			heading_title,
			section_content,
			content='llm_report_sections',
			content_rowid='id'
		)`,

		// Triggers to keep FTS index in sync with llm_report_sections table
		`CREATE TRIGGER IF NOT EXISTS llm_report_sections_ai AFTER INSERT ON llm_report_sections BEGIN
			INSERT INTO llm_reports_fts(rowid, symbol, title, heading_title, section_content)
			SELECT new.id, r.symbol, r.title, new.heading_title, new.section_content
			FROM llm_reports r WHERE r.id = new.report_id;
		END`,
		`CREATE TRIGGER IF NOT EXISTS llm_report_sections_ad AFTER DELETE ON llm_report_sections BEGIN
			INSERT INTO llm_reports_fts(llm_reports_fts, rowid, symbol, title, heading_title, section_content)
			SELECT 'delete', old.id,
			       (SELECT symbol FROM llm_reports WHERE id = old.report_id),
			       (SELECT title FROM llm_reports WHERE id = old.report_id),
			       old.heading_title,
			       old.section_content;
		END`,

		// earnings_events: durable discovery/ingestion event log
		`CREATE TABLE IF NOT EXISTS earnings_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			symbol TEXT NOT NULL,
			source_type TEXT NOT NULL,
			source_name TEXT,
			title TEXT,
			url TEXT NOT NULL,
			released_at DATETIME,
			detected_at DATETIME NOT NULL,
			ingested_at DATETIME NOT NULL,
			fingerprint TEXT NOT NULL,
			confidence TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'new',
			metadata_json TEXT,
			content TEXT,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			UNIQUE(symbol, fingerprint)
		)`,
		`CREATE INDEX IF NOT EXISTS idx_earnings_events_symbol_detected
			ON earnings_events(symbol, detected_at DESC)`,
		`CREATE INDEX IF NOT EXISTS idx_earnings_events_source_detected
			ON earnings_events(source_type, detected_at DESC)`,

		// ir_feed_state: conditional-GET metadata for IR RSS/Atom polling
		`CREATE TABLE IF NOT EXISTS ir_feed_state (
			feed_url TEXT PRIMARY KEY,
			etag TEXT,
			last_modified TEXT,
			last_checked_at DATETIME,
			last_status INTEGER,
			last_error TEXT,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
	}

	for _, migration := range migrations {
		if _, err := db.conn.Exec(migration); err != nil {
			return fmt.Errorf("migration failed: %w", err)
		}
	}

	return nil
}

// Exec executes a statement.
func (db *DB) Exec(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return db.conn.ExecContext(ctx, query, args...)
}

// QueryRow queries a single row.
func (db *DB) QueryRow(ctx context.Context, query string, args ...interface{}) *sql.Row {
	db.mu.RLock()
	defer db.mu.RUnlock()
	return db.conn.QueryRowContext(ctx, query, args...)
}

// Query queries multiple rows.
func (db *DB) Query(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()
	return db.conn.QueryContext(ctx, query, args...)
}

// BeginTx starts a transaction.
func (db *DB) BeginTx(ctx context.Context) (*sql.Tx, error) {
	db.mu.RLock()
	defer db.mu.RUnlock()
	return db.conn.BeginTx(ctx, nil)
}

// Health checks the database health.
func (db *DB) Health(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	return db.conn.PingContext(ctx)
}
