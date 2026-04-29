package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// barIntervalClosed reports whether the bar's interval window has
// already ended — i.e. the bar is historical and immutable. Such bars
// are persisted with a NULL expires_at so they never look stale on
// subsequent reads.
func barIntervalClosed(bar *PriceBar) bool {
	d := IntervalDuration(bar.Interval)
	if d <= 0 {
		return false
	}
	return time.Now().After(bar.OpenTime.Add(d))
}

// alignBarOpenTime normalises intraday open-times to the interval grid
// boundary. Yahoo's chart API returns the currently-forming bar with
// its timestamp set to the last trade time (e.g. 15:58:10 for a 5m
// bar whose boundary is 15:55:00). Without alignment the unique key
// (symbol, interval, open_time) fails to dedup across refreshes and
// stray bars accumulate, polluting sparkline renders with duplicate
// points.
func alignBarOpenTime(bar *PriceBar) {
	d := IntervalDuration(bar.Interval)
	// Only align intraday grids; daily/weekly/monthly bars are
	// calendar-aligned and rounding them would shift days around
	// across timezones.
	if d <= 0 || d >= 24*time.Hour {
		return
	}
	bar.OpenTime = bar.OpenTime.Truncate(d)
}

// UpsertPriceBar inserts or updates a price bar.
func (db *DB) UpsertPriceBar(ctx context.Context, bar *PriceBar) error {
	alignBarOpenTime(bar)
	query := `
		INSERT INTO price_bars (
			symbol, interval, open_time, open, high, low, close, volume,
			expiry_seconds, expires_at, synced_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(symbol, interval, open_time) DO UPDATE SET
			open = excluded.open,
			high = excluded.high,
			low = excluded.low,
			close = excluded.close,
			volume = excluded.volume,
			expiry_seconds = excluded.expiry_seconds,
			expires_at = excluded.expires_at,
			synced_at = CURRENT_TIMESTAMP
	`

	var expiresAt interface{}
	if bar.ExpirySeconds > 0 && !barIntervalClosed(bar) {
		expiresAt = bar.OpenTime.Add(time.Duration(bar.ExpirySeconds) * time.Second)
	}

	_, err := db.Exec(ctx, query,
		bar.Symbol, bar.Interval, bar.OpenTime,
		bar.Open, bar.High, bar.Low, bar.Close, bar.Volume,
		bar.ExpirySeconds, expiresAt, bar.SyncedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to upsert price bar: %w", err)
	}
	return nil
}

// UpsertPriceBars inserts or updates many price bars in a single
// transaction. Orders of magnitude faster than calling UpsertPriceBar
// in a loop for large charts, and gives atomic all-or-nothing semantics
// so a partial write cannot leave the cache half-fresh.
func (db *DB) UpsertPriceBars(ctx context.Context, bars []*PriceBar) error {
	if len(bars) == 0 {
		return nil
	}
	tx, err := db.BeginTx(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() {
		_ = tx.Rollback()
	}()

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO price_bars (
			symbol, interval, open_time, open, high, low, close, volume,
			expiry_seconds, expires_at, synced_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(symbol, interval, open_time) DO UPDATE SET
			open = excluded.open,
			high = excluded.high,
			low = excluded.low,
			close = excluded.close,
			volume = excluded.volume,
			expiry_seconds = excluded.expiry_seconds,
			expires_at = excluded.expires_at,
			synced_at = CURRENT_TIMESTAMP
	`)
	if err != nil {
		return fmt.Errorf("prepare upsert: %w", err)
	}
	defer stmt.Close()

	for _, bar := range bars {
		alignBarOpenTime(bar)
		var expiresAt interface{}
		if bar.ExpirySeconds > 0 && !barIntervalClosed(bar) {
			expiresAt = bar.OpenTime.Add(time.Duration(bar.ExpirySeconds) * time.Second)
		}
		if _, err := stmt.ExecContext(ctx,
			bar.Symbol, bar.Interval, bar.OpenTime,
			bar.Open, bar.High, bar.Low, bar.Close, bar.Volume,
			bar.ExpirySeconds, expiresAt, bar.SyncedAt,
		); err != nil {
			return fmt.Errorf("upsert %s %s %v: %w", bar.Symbol, bar.Interval, bar.OpenTime, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit upsert: %w", err)
	}
	return nil
}

// GetPriceBars retrieves price bars for a symbol and interval.
func (db *DB) GetPriceBars(ctx context.Context, symbol, interval string, limit int) ([]*PriceBar, error) {
	query := `
		SELECT
			id, symbol, interval, open_time, open, high, low, close, volume,
			expiry_seconds, expires_at, synced_at
		FROM price_bars
		WHERE symbol = ? AND interval = ?
		ORDER BY open_time DESC
		LIMIT ?
	`

	rows, err := db.Query(ctx, query, symbol, interval, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query price bars: %w", err)
	}
	defer rows.Close()

	var bars []*PriceBar
	for rows.Next() {
		bar := &PriceBar{}
		var expiresAt sql.NullTime
		err := rows.Scan(
			&bar.ID, &bar.Symbol, &bar.Interval, &bar.OpenTime,
			&bar.Open, &bar.High, &bar.Low, &bar.Close, &bar.Volume,
			&bar.ExpirySeconds, &expiresAt, &bar.SyncedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan price bar: %w", err)
		}
		if expiresAt.Valid {
			bar.ExpiresAt = expiresAt.Time
		}
		bars = append(bars, bar)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating price bars: %w", err)
	}

	return bars, nil
}

// GetPriceBarsInRange retrieves bars within a time range.
func (db *DB) GetPriceBarsInRange(ctx context.Context, symbol, interval string, startTime, endTime time.Time) ([]*PriceBar, error) {
	query := `
		SELECT
			id, symbol, interval, open_time, open, high, low, close, volume,
			expiry_seconds, expires_at, synced_at
		FROM price_bars
		WHERE symbol = ? AND interval = ? AND open_time >= ? AND open_time <= ?
		ORDER BY open_time DESC
	`

	rows, err := db.Query(ctx, query, symbol, interval, startTime, endTime)
	if err != nil {
		return nil, fmt.Errorf("failed to query price bars in range: %w", err)
	}
	defer rows.Close()

	var bars []*PriceBar
	for rows.Next() {
		bar := &PriceBar{}
		var expiresAt sql.NullTime
		err := rows.Scan(
			&bar.ID, &bar.Symbol, &bar.Interval, &bar.OpenTime,
			&bar.Open, &bar.High, &bar.Low, &bar.Close, &bar.Volume,
			&bar.ExpirySeconds, &expiresAt, &bar.SyncedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan price bar: %w", err)
		}
		if expiresAt.Valid {
			bar.ExpiresAt = expiresAt.Time
		}
		bars = append(bars, bar)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating price bars: %w", err)
	}

	return bars, nil
}

// CleanupExpiredBars deletes bars that have expired.
func (db *DB) CleanupExpiredBars(ctx context.Context) (int64, error) {
	query := `DELETE FROM price_bars WHERE expires_at IS NOT NULL AND expires_at < CURRENT_TIMESTAMP`

	result, err := db.Exec(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("failed to cleanup expired bars: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to get rows affected: %w", err)
	}

	return rowsAffected, nil
}

// HasFreshBars checks if fresh (non-expired) bars exist for a symbol and interval.
func (db *DB) HasFreshBars(ctx context.Context, symbol, interval string) (bool, error) {
	query := `
		SELECT COUNT(*) FROM price_bars
		WHERE symbol = ? AND interval = ?
		AND (expires_at IS NULL OR expires_at >= CURRENT_TIMESTAMP)
		LIMIT 1
	`

	var count int
	err := db.QueryRow(ctx, query, symbol, interval).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("failed to check fresh bars: %w", err)
	}

	return count > 0, nil
}
