package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// UpsertQuoteLatest inserts or updates the latest quote and key stats for a symbol.
func (db *DB) UpsertQuoteLatest(ctx context.Context, quote *QuoteLatest) error {
	query := `
		INSERT INTO quotes_latest (
			symbol, price, change, change_pct, open, high, low, volume,
			market_cap, pe_ratio, forward_pe, dividend_yield,
			fifty_two_week_high, fifty_two_week_low, fifty_two_week_change, fifty_two_week_change_pct,
			two_hundred_day_avg, two_hundred_day_avg_change, two_hundred_day_avg_change_pct,
			fifty_day_avg, fifty_day_avg_change, fifty_day_avg_change_pct,
			beta, trailing_eps, forward_eps, peg_ratio,
			eps_surprise, eps_surprise_pct, target_mean_price,
			quote_source, quote_updated_at, pe_changed_at, forward_pe_changed_at,
			stats_changed_at, synced_at
		) VALUES (
			?, ?, ?, ?, ?, ?, ?, ?,
			?, ?, ?, ?,
			?, ?, ?, ?,
			?, ?, ?,
			?, ?, ?,
			?, ?, ?, ?,
			?, ?, ?,
			?, ?, ?, ?,
			?, ?
		)
		ON CONFLICT(symbol) DO UPDATE SET
			price = excluded.price,
			change = excluded.change,
			change_pct = excluded.change_pct,
			open = excluded.open,
			high = excluded.high,
			low = excluded.low,
			volume = excluded.volume,
			market_cap = excluded.market_cap,
			pe_ratio = excluded.pe_ratio,
			forward_pe = excluded.forward_pe,
			dividend_yield = excluded.dividend_yield,
			fifty_two_week_high = excluded.fifty_two_week_high,
			fifty_two_week_low = excluded.fifty_two_week_low,
			fifty_two_week_change = excluded.fifty_two_week_change,
			fifty_two_week_change_pct = excluded.fifty_two_week_change_pct,
			two_hundred_day_avg = excluded.two_hundred_day_avg,
			two_hundred_day_avg_change = excluded.two_hundred_day_avg_change,
			two_hundred_day_avg_change_pct = excluded.two_hundred_day_avg_change_pct,
			fifty_day_avg = excluded.fifty_day_avg,
			fifty_day_avg_change = excluded.fifty_day_avg_change,
			fifty_day_avg_change_pct = excluded.fifty_day_avg_change_pct,
			beta = excluded.beta,
			trailing_eps = excluded.trailing_eps,
			forward_eps = excluded.forward_eps,
			peg_ratio = excluded.peg_ratio,
			eps_surprise = excluded.eps_surprise,
			eps_surprise_pct = excluded.eps_surprise_pct,
			target_mean_price = excluded.target_mean_price,
			quote_source = excluded.quote_source,
			quote_updated_at = excluded.quote_updated_at,
			pe_changed_at = excluded.pe_changed_at,
			forward_pe_changed_at = excluded.forward_pe_changed_at,
			stats_changed_at = excluded.stats_changed_at,
			synced_at = CURRENT_TIMESTAMP
	`

	_, err := db.Exec(ctx, query,
		quote.Symbol, quote.Price, quote.Change, quote.ChangePct,
		quote.Open, quote.High, quote.Low, quote.Volume,
		quote.MarketCap, quote.PERatio, quote.ForwardPE, quote.DividendYield,
		quote.FiftyTwoWeekHigh, quote.FiftyTwoWeekLow, quote.FiftyTwoWeekChange, quote.FiftyTwoWeekChangePct,
		quote.TwoHundredDayAvg, quote.TwoHundredDayAvgChange, quote.TwoHundredDayAvgChangePct,
		quote.FiftyDayAvg, quote.FiftyDayAvgChange, quote.FiftyDayAvgChangePct,
		quote.Beta, quote.TrailingEPS, quote.ForwardEPS, quote.PEGRatio,
		quote.EPSSurprise, quote.EPSSurprisePct, quote.TargetMeanPrice,
		quote.QuoteSource, quote.QuoteUpdatedAt, quote.PEChangedAt, quote.ForwardPEChangedAt,
		quote.StatsChangedAt, quote.SyncedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to upsert quote: %w", err)
	}
	return nil
}

// GetQuoteLatest retrieves the latest quote for a symbol.
func (db *DB) GetQuoteLatest(ctx context.Context, symbol string) (*QuoteLatest, error) {
	query := `
		SELECT
			symbol, price, change, change_pct, open, high, low, volume,
			market_cap, pe_ratio, forward_pe, dividend_yield,
			fifty_two_week_high, fifty_two_week_low, fifty_two_week_change, fifty_two_week_change_pct,
			two_hundred_day_avg, two_hundred_day_avg_change, two_hundred_day_avg_change_pct,
			fifty_day_avg, fifty_day_avg_change, fifty_day_avg_change_pct,
			beta, trailing_eps, forward_eps, peg_ratio,
			eps_surprise, eps_surprise_pct, target_mean_price,
			quote_source, quote_updated_at, pe_changed_at, forward_pe_changed_at,
			stats_changed_at, synced_at
		FROM quotes_latest
		WHERE symbol = ?
	`

	row := db.QueryRow(ctx, query, symbol)
	quote := &QuoteLatest{}
	err := row.Scan(
		&quote.Symbol, &quote.Price, &quote.Change, &quote.ChangePct,
		&quote.Open, &quote.High, &quote.Low, &quote.Volume,
		&quote.MarketCap, &quote.PERatio, &quote.ForwardPE, &quote.DividendYield,
		&quote.FiftyTwoWeekHigh, &quote.FiftyTwoWeekLow, &quote.FiftyTwoWeekChange, &quote.FiftyTwoWeekChangePct,
		&quote.TwoHundredDayAvg, &quote.TwoHundredDayAvgChange, &quote.TwoHundredDayAvgChangePct,
		&quote.FiftyDayAvg, &quote.FiftyDayAvgChange, &quote.FiftyDayAvgChangePct,
		&quote.Beta, &quote.TrailingEPS, &quote.ForwardEPS, &quote.PEGRatio,
		&quote.EPSSurprise, &quote.EPSSurprisePct, &quote.TargetMeanPrice,
		&quote.QuoteSource, &quote.QuoteUpdatedAt, &quote.PEChangedAt, &quote.ForwardPEChangedAt,
		&quote.StatsChangedAt, &quote.SyncedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get quote: %w", err)
	}
	return quote, nil
}

// IsQuoteStale checks if a quote needs refresh based on age.
func (db *DB) IsQuoteStale(ctx context.Context, symbol string, maxAge time.Duration) (bool, error) {
	quote, err := db.GetQuoteLatest(ctx, symbol)
	if err != nil {
		return false, err
	}
	if quote == nil {
		return true, nil // Missing quote is stale
	}
	return time.Since(quote.SyncedAt) > maxAge, nil
}

// AddQuoteHistory adds a historical snapshot of a quote.
func (db *DB) AddQuoteHistory(ctx context.Context, history *QuoteHistory) error {
	query := `
		INSERT INTO quotes_history (
			symbol, price, change, change_pct, pe_ratio, forward_pe, dividend_yield,
			fifty_two_week_high, fifty_two_week_low, trailing_eps, forward_eps,
			target_mean_price, recorded_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	_, err := db.Exec(ctx, query,
		history.Symbol, history.Price, history.Change, history.ChangePct,
		history.PERatio, history.ForwardPE, history.DividendYield,
		history.FiftyTwoWeekHigh, history.FiftyTwoWeekLow,
		history.TrailingEPS, history.ForwardEPS, history.TargetMeanPrice,
		history.RecordedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to add quote history: %w", err)
	}
	return nil
}

// GetQuoteHistory retrieves historical snapshots for a symbol within a time range.
func (db *DB) GetQuoteHistory(ctx context.Context, symbol string, limit int) ([]*QuoteHistory, error) {
	query := `
		SELECT
			id, symbol, price, change, change_pct, pe_ratio, forward_pe, dividend_yield,
			fifty_two_week_high, fifty_two_week_low, trailing_eps, forward_eps,
			target_mean_price, recorded_at
		FROM quotes_history
		WHERE symbol = ?
		ORDER BY recorded_at DESC
		LIMIT ?
	`

	rows, err := db.Query(ctx, query, symbol, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query quote history: %w", err)
	}
	defer rows.Close()

	var history []*QuoteHistory
	for rows.Next() {
		h := &QuoteHistory{}
		err := rows.Scan(
			&h.ID, &h.Symbol, &h.Price, &h.Change, &h.ChangePct,
			&h.PERatio, &h.ForwardPE, &h.DividendYield,
			&h.FiftyTwoWeekHigh, &h.FiftyTwoWeekLow,
			&h.TrailingEPS, &h.ForwardEPS, &h.TargetMeanPrice,
			&h.RecordedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan quote history: %w", err)
		}
		history = append(history, h)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating quote history: %w", err)
	}

	return history, nil
}
