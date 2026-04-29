package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// InsertEarningsEventIfNew inserts an event keyed by (symbol, fingerprint).
// Returns true when a new row is created, false when the event already exists.
func (db *DB) InsertEarningsEventIfNew(ctx context.Context, ev *EarningsEvent) (bool, error) {
	query := `
		INSERT INTO earnings_events (
			symbol, source_type, source_name, title, url,
			released_at, detected_at, ingested_at, fingerprint,
			confidence, status, metadata_json, content
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(symbol, fingerprint) DO NOTHING
	`

	var releasedAt any
	if !ev.ReleasedAt.IsZero() {
		releasedAt = ev.ReleasedAt
	}

	res, err := db.Exec(ctx, query,
		ev.Symbol, ev.SourceType, ev.SourceName, ev.Title, ev.URL,
		releasedAt, ev.DetectedAt, ev.IngestedAt, ev.Fingerprint,
		ev.Confidence, ev.Status, ev.MetadataJSON, ev.Content,
	)
	if err != nil {
		return false, fmt.Errorf("failed to insert earnings event: %w", err)
	}
	rows, err := res.RowsAffected()
	if err != nil {
		return false, nil
	}
	return rows > 0, nil
}

// GetRecentEarningsEvents retrieves recent events for a symbol.
func (db *DB) GetRecentEarningsEvents(ctx context.Context, symbol string, limit int) ([]*EarningsEvent, error) {
	if limit <= 0 {
		limit = 20
	}
	query := `
		SELECT
			id, symbol, source_type, source_name, title, url,
			released_at, detected_at, ingested_at, fingerprint,
			confidence, status, metadata_json, content, updated_at
		FROM earnings_events
		WHERE symbol = ?
		ORDER BY detected_at DESC
		LIMIT ?
	`

	rows, err := db.Query(ctx, query, symbol, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query earnings events: %w", err)
	}
	defer rows.Close()

	var out []*EarningsEvent
	for rows.Next() {
		ev, err := scanEarningsEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, ev)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating earnings events: %w", err)
	}
	return out, nil
}

// GetIRFeedState returns conditional-fetch state for a feed URL.
func (db *DB) GetIRFeedState(ctx context.Context, feedURL string) (*IRFeedState, error) {
	query := `
		SELECT feed_url, etag, last_modified, last_checked_at, last_status, last_error, updated_at
		FROM ir_feed_state
		WHERE feed_url = ?
	`
	row := db.QueryRow(ctx, query, feedURL)

	state, err := scanIRFeedState(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get ir feed state: %w", err)
	}
	return state, nil
}

// UpsertIRFeedState writes the latest conditional-fetch state for a feed URL.
func (db *DB) UpsertIRFeedState(ctx context.Context, state *IRFeedState) error {
	query := `
		INSERT INTO ir_feed_state (
			feed_url, etag, last_modified, last_checked_at, last_status, last_error
		) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(feed_url) DO UPDATE SET
			etag = excluded.etag,
			last_modified = excluded.last_modified,
			last_checked_at = excluded.last_checked_at,
			last_status = excluded.last_status,
			last_error = excluded.last_error,
			updated_at = CURRENT_TIMESTAMP
	`

	_, err := db.Exec(ctx, query,
		state.FeedURL, state.ETag, state.LastModified, state.LastCheckedAt, state.LastStatus, state.LastError,
	)
	if err != nil {
		return fmt.Errorf("failed to upsert ir feed state: %w", err)
	}
	return nil
}

func scanEarningsEvent(scanner interface{ Scan(dest ...any) error }) (*EarningsEvent, error) {
	ev := &EarningsEvent{}
	var releasedAt sql.NullTime
	if err := scanner.Scan(
		&ev.ID, &ev.Symbol, &ev.SourceType, &ev.SourceName, &ev.Title, &ev.URL,
		&releasedAt, &ev.DetectedAt, &ev.IngestedAt, &ev.Fingerprint,
		&ev.Confidence, &ev.Status, &ev.MetadataJSON, &ev.Content, &ev.UpdatedAt,
	); err != nil {
		return nil, err
	}
	if releasedAt.Valid {
		ev.ReleasedAt = releasedAt.Time
	}
	return ev, nil
}

func scanIRFeedState(scanner interface{ Scan(dest ...any) error }) (*IRFeedState, error) {
	state := &IRFeedState{}
	var checkedAt sql.NullTime
	var etag sql.NullString
	var lm sql.NullString
	var status sql.NullInt64
	var lastErr sql.NullString
	var updatedAt sql.NullTime

	if err := scanner.Scan(&state.FeedURL, &etag, &lm, &checkedAt, &status, &lastErr, &updatedAt); err != nil {
		return nil, err
	}
	if etag.Valid {
		state.ETag = etag.String
	}
	if lm.Valid {
		state.LastModified = lm.String
	}
	if checkedAt.Valid {
		state.LastCheckedAt = checkedAt.Time
	}
	if status.Valid {
		state.LastStatus = int(status.Int64)
	}
	if lastErr.Valid {
		state.LastError = lastErr.String
	}
	if updatedAt.Valid {
		state.UpdatedAt = updatedAt.Time
	}
	return state, nil
}

// PruneEarningsEvents deletes events older than retention.
func (db *DB) PruneEarningsEvents(ctx context.Context, retention time.Duration) (int64, error) {
	if retention <= 0 {
		return 0, nil
	}
	cutoff := time.Now().Add(-retention)
	res, err := db.Exec(ctx, `DELETE FROM earnings_events WHERE detected_at < ?`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("failed to prune earnings events: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, nil
	}
	return n, nil
}
