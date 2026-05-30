// Package store is the SQLite-backed historical data repository for
// trader-evolver. It holds the multi-year OHLCV + macro + fear/greed history
// that collectors fill and the backtest engine reads from.
//
// Pure-Go driver (modernc.org/sqlite) — no cgo, so the project stays
// self-contained and cross-compilable.
//
// Schema:
//
//	candles(instrument_key, interval, open_time_ms, open, high, low, close, volume)
//	    PK(instrument_key, interval, open_time_ms) — idempotent upserts
//	daily_macro(series, date_ms, close)
//	    PK(series, date_ms)
//	feargreed(date_ms, value, classification)
//	    PK(date_ms)
package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"

	"trader-evolver/internal/types"
)

// Store wraps a SQLite database handle.
type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS candles (
    instrument_key TEXT    NOT NULL,
    interval       TEXT    NOT NULL,
    open_time_ms   INTEGER NOT NULL,
    open           REAL    NOT NULL,
    high           REAL    NOT NULL,
    low            REAL    NOT NULL,
    close          REAL    NOT NULL,
    volume         REAL    NOT NULL,
    PRIMARY KEY (instrument_key, interval, open_time_ms)
) WITHOUT ROWID;

CREATE INDEX IF NOT EXISTS idx_candles_time
    ON candles (instrument_key, interval, open_time_ms);

CREATE TABLE IF NOT EXISTS daily_macro (
    series   TEXT    NOT NULL,
    date_ms  INTEGER NOT NULL,
    close    REAL    NOT NULL,
    PRIMARY KEY (series, date_ms)
) WITHOUT ROWID;

CREATE TABLE IF NOT EXISTS feargreed (
    date_ms        INTEGER NOT NULL PRIMARY KEY,
    value          INTEGER NOT NULL,
    classification TEXT    NOT NULL
) WITHOUT ROWID;
`

// Open opens (creating if needed) a SQLite store at path and applies the schema.
// The parent directory is created automatically.
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("store: mkdir %s: %w", dir, err)
		}
	}
	// _pragma busy_timeout avoids "database is locked" under collector retries.
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open: %w", err)
	}
	// modernc/sqlite is not safe for unbounded concurrent writers on one conn.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: apply schema: %w", err)
	}
	return &Store{db: db}, nil
}

// Close releases the database handle.
func (s *Store) Close() error { return s.db.Close() }

// DB exposes the underlying handle (for advanced queries / future tables).
func (s *Store) DB() *sql.DB { return s.db }

// ---- Candles ----

// UpsertCandles inserts or replaces candles in a single transaction.
// Idempotent on (instrument_key, interval, open_time_ms).
func (s *Store) UpsertCandles(candles []types.Candle) error {
	if len(candles) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	stmt, err := tx.Prepare(`
        INSERT INTO candles (instrument_key, interval, open_time_ms, open, high, low, close, volume)
        VALUES (?, ?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(instrument_key, interval, open_time_ms) DO UPDATE SET
            open=excluded.open, high=excluded.high, low=excluded.low,
            close=excluded.close, volume=excluded.volume`)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("store: prepare candle upsert: %w", err)
	}
	defer stmt.Close()
	for _, c := range candles {
		if _, err := stmt.Exec(c.InstrumentKey, c.Interval, c.OpenTimeMs, c.Open, c.High, c.Low, c.Close, c.Volume); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("store: upsert candle %s@%d: %w", c.InstrumentKey, c.OpenTimeMs, err)
		}
	}
	return tx.Commit()
}

// GetCandles returns candles for a series within [fromMs, toMs] (inclusive),
// ordered by open time. Pass fromMs<=0 / toMs<=0 to leave a bound open.
func (s *Store) GetCandles(instrumentKey, interval string, fromMs, toMs int64) ([]types.Candle, error) {
	q := `SELECT instrument_key, interval, open_time_ms, open, high, low, close, volume
          FROM candles WHERE instrument_key=? AND interval=?`
	args := []any{instrumentKey, interval}
	if fromMs > 0 {
		q += " AND open_time_ms >= ?"
		args = append(args, fromMs)
	}
	if toMs > 0 {
		q += " AND open_time_ms <= ?"
		args = append(args, toMs)
	}
	q += " ORDER BY open_time_ms ASC"

	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: query candles: %w", err)
	}
	defer rows.Close()
	var out []types.Candle
	for rows.Next() {
		var c types.Candle
		if err := rows.Scan(&c.InstrumentKey, &c.Interval, &c.OpenTimeMs, &c.Open, &c.High, &c.Low, &c.Close, &c.Volume); err != nil {
			return nil, fmt.Errorf("store: scan candle: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// CandleCoverage reports the stored range for a series, or (zero, nil) if empty.
func (s *Store) CandleCoverage(instrumentKey, interval string) (types.Coverage, error) {
	cov := types.Coverage{InstrumentKey: instrumentKey, Interval: interval}
	var first, last sql.NullInt64
	var count int64
	err := s.db.QueryRow(`
        SELECT COUNT(*), MIN(open_time_ms), MAX(open_time_ms)
        FROM candles WHERE instrument_key=? AND interval=?`,
		instrumentKey, interval).Scan(&count, &first, &last)
	if err != nil {
		return cov, fmt.Errorf("store: candle coverage: %w", err)
	}
	cov.Count = count
	if first.Valid {
		cov.FirstOpenMs = first.Int64
	}
	if last.Valid {
		cov.LastOpenMs = last.Int64
	}
	return cov, nil
}

// LatestCandleTime returns the newest open_time_ms for a series, or 0 if empty.
// Useful for incremental collection (resume from last bar).
func (s *Store) LatestCandleTime(instrumentKey, interval string) (int64, error) {
	var last sql.NullInt64
	err := s.db.QueryRow(`SELECT MAX(open_time_ms) FROM candles WHERE instrument_key=? AND interval=?`,
		instrumentKey, interval).Scan(&last)
	if err != nil {
		return 0, fmt.Errorf("store: latest candle time: %w", err)
	}
	if last.Valid {
		return last.Int64, nil
	}
	return 0, nil
}

// ---- Daily macro ----

// UpsertDailyMacro inserts/replaces daily macro rows in one transaction.
func (s *Store) UpsertDailyMacro(rows []types.DailyMacro) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	stmt, err := tx.Prepare(`
        INSERT INTO daily_macro (series, date_ms, close) VALUES (?, ?, ?)
        ON CONFLICT(series, date_ms) DO UPDATE SET close=excluded.close`)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("store: prepare macro upsert: %w", err)
	}
	defer stmt.Close()
	for _, r := range rows {
		if _, err := stmt.Exec(r.Series, r.DateMs, r.Close); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("store: upsert macro %s@%d: %w", r.Series, r.DateMs, err)
		}
	}
	return tx.Commit()
}

// GetDailyMacro returns macro rows for a series within [fromMs, toMs], ordered.
func (s *Store) GetDailyMacro(series string, fromMs, toMs int64) ([]types.DailyMacro, error) {
	q := `SELECT series, date_ms, close FROM daily_macro WHERE series=?`
	args := []any{series}
	if fromMs > 0 {
		q += " AND date_ms >= ?"
		args = append(args, fromMs)
	}
	if toMs > 0 {
		q += " AND date_ms <= ?"
		args = append(args, toMs)
	}
	q += " ORDER BY date_ms ASC"
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, fmt.Errorf("store: query macro: %w", err)
	}
	defer rows.Close()
	var out []types.DailyMacro
	for rows.Next() {
		var m types.DailyMacro
		if err := rows.Scan(&m.Series, &m.DateMs, &m.Close); err != nil {
			return nil, fmt.Errorf("store: scan macro: %w", err)
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// MacroAsOf returns the most recent macro close at or before atMs for a series.
// Returns (value, true) when found. Used to reconstruct regime inputs in
// backtests without requiring an exact daily timestamp match.
func (s *Store) MacroAsOf(series string, atMs int64) (float64, bool, error) {
	var close sql.NullFloat64
	err := s.db.QueryRow(`
        SELECT close FROM daily_macro
        WHERE series=? AND date_ms <= ?
        ORDER BY date_ms DESC LIMIT 1`, series, atMs).Scan(&close)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("store: macro as-of: %w", err)
	}
	if !close.Valid {
		return 0, false, nil
	}
	return close.Float64, true, nil
}

// ---- Fear & Greed ----

// UpsertFearGreed inserts/replaces fear & greed rows in one transaction.
func (s *Store) UpsertFearGreed(rows []types.FearGreed) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	stmt, err := tx.Prepare(`
        INSERT INTO feargreed (date_ms, value, classification) VALUES (?, ?, ?)
        ON CONFLICT(date_ms) DO UPDATE SET value=excluded.value, classification=excluded.classification`)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("store: prepare feargreed upsert: %w", err)
	}
	defer stmt.Close()
	for _, r := range rows {
		if _, err := stmt.Exec(r.DateMs, r.Value, r.Classification); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("store: upsert feargreed @%d: %w", r.DateMs, err)
		}
	}
	return tx.Commit()
}

// FearGreedAsOf returns the most recent fear&greed value at or before atMs.
func (s *Store) FearGreedAsOf(atMs int64) (int, bool, error) {
	var value sql.NullInt64
	err := s.db.QueryRow(`
        SELECT value FROM feargreed
        WHERE date_ms <= ?
        ORDER BY date_ms DESC LIMIT 1`, atMs).Scan(&value)
	if err == sql.ErrNoRows {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("store: feargreed as-of: %w", err)
	}
	if !value.Valid {
		return 0, false, nil
	}
	return int(value.Int64), true, nil
}

// FearGreedCount returns the number of stored fear&greed rows.
func (s *Store) FearGreedCount() (int64, error) {
	var n int64
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM feargreed`).Scan(&n); err != nil {
		return 0, fmt.Errorf("store: feargreed count: %w", err)
	}
	return n, nil
}
