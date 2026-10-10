// Package store keeps every rate in a single SQLite file and answers lookups by date,
// falling back to the last published quote when a day has none (weekends, holidays, today before publication).
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jmendozaf/cotizapy/internal/rates"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS rates (
    source    TEXT NOT NULL,
    currency  TEXT NOT NULL,
    date      TEXT NOT NULL,
    buy       TEXT,
    sell      TEXT,
    reference TEXT,
    PRIMARY KEY (source, currency, date)
) WITHOUT ROWID;
CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
`

// MaxLookback is how far back a lookup goes for the last published quote before giving up.
const MaxLookback = 10

// ErrNotFound is returned when there is no quote for the date (or the days before it).
var ErrNotFound = errors.New("store: not found")

// Store is a handle on a rates database.
type Store struct {
	db *sql.DB
}

// Open opens (and creates, if needed) a rates database.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=synchronous(NORMAL)")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	return &Store{db: db}, nil
}

// Close releases the database.
func (s *Store) Close() error { return s.db.Close() }

// Upsert stores rates and returns how many were new or changed.
func (s *Store) Upsert(ctx context.Context, list []rates.Rate) (int, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO rates (source, currency, date, buy, sell, reference) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (source, currency, date) DO UPDATE SET
		    buy = excluded.buy, sell = excluded.sell, reference = excluded.reference
		WHERE buy IS NOT excluded.buy OR sell IS NOT excluded.sell OR reference IS NOT excluded.reference`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()
	changed := 0
	for _, r := range list {
		res, err := stmt.ExecContext(ctx, r.Source, r.Currency, r.Date, null(r.Buy), null(r.Sell), null(r.Reference))
		if err != nil {
			return 0, err
		}
		n, _ := res.RowsAffected()
		changed += int(n)
	}
	return changed, tx.Commit()
}

func null(d rates.Decimal) any {
	if d == "" {
		return nil
	}
	return string(d)
}

const columns = `source, currency, date, COALESCE(buy, ''), COALESCE(sell, ''), COALESCE(reference, '')`

func scan(row interface{ Scan(...any) error }) (rates.Rate, error) {
	var r rates.Rate
	var buy, sell, ref string
	if err := row.Scan(&r.Source, &r.Currency, &r.Date, &buy, &sell, &ref); err != nil {
		return rates.Rate{}, err
	}
	r.Buy, r.Sell, r.Reference = rates.Decimal(buy), rates.Decimal(sell), rates.Decimal(ref)
	return r, nil
}

// Quote is the rate that applies on RequestedDate: the one published that day or, failing that,
// the last one published before it (Rate.Date says which).
type Quote struct {
	rates.Rate
	RequestedDate string `json:"requested_date"`
	Exact         bool   `json:"exact"`
}

// Find returns the quote that applies on date.
func (s *Store) Find(ctx context.Context, source, currency, date string) (Quote, error) {
	day, err := time.Parse(time.DateOnly, date)
	if err != nil {
		return Quote{}, fmt.Errorf("store: invalid date %q", date)
	}
	floor := day.AddDate(0, 0, -MaxLookback).Format(time.DateOnly)
	row := s.db.QueryRowContext(ctx, `SELECT `+columns+` FROM rates
		WHERE source = ? AND currency = ? AND date <= ? AND date >= ?
		ORDER BY date DESC LIMIT 1`, source, currency, date, floor)
	r, err := scan(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Quote{}, ErrNotFound
	}
	if err != nil {
		return Quote{}, err
	}
	return Quote{Rate: r, RequestedDate: date, Exact: r.Date == date}, nil
}

// Range returns the published rates between from and to (inclusive), oldest first.
func (s *Store) Range(ctx context.Context, source, currency, from, to string) ([]rates.Rate, error) {
	return s.query(ctx, `SELECT `+columns+` FROM rates
		WHERE source = ? AND currency = ? AND date BETWEEN ? AND ? ORDER BY date`, source, currency, from, to)
}

// Latest returns the most recent rate of every source and currency.
func (s *Store) Latest(ctx context.Context) ([]rates.Rate, error) {
	return s.query(ctx, `SELECT `+columns+` FROM rates r
		WHERE date = (SELECT MAX(date) FROM rates WHERE source = r.source AND currency = r.currency)
		ORDER BY source, currency`)
}

func (s *Store) query(ctx context.Context, q string, args ...any) ([]rates.Rate, error) {
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []rates.Rate{}
	for rows.Next() {
		r, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Series describes the stored history of one currency in one source.
type Series struct {
	Source   string `json:"source"`
	Currency string `json:"currency"`
	Name     string `json:"name"`
	First    string `json:"first"`
	Last     string `json:"last"`
	Count    int    `json:"count"`
}

// Series lists every source and currency with its date range.
func (s *Store) Series(ctx context.Context) ([]Series, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT source, currency, MIN(date), MAX(date), COUNT(*)
		FROM rates GROUP BY source, currency ORDER BY source, currency`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Series{}
	for rows.Next() {
		var x Series
		if err := rows.Scan(&x.Source, &x.Currency, &x.First, &x.Last, &x.Count); err != nil {
			return nil, err
		}
		x.Name = rates.Names[x.Currency]
		out = append(out, x)
	}
	return out, rows.Err()
}

// Count returns how many rates a source has.
func (s *Store) Count(ctx context.Context, source string) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM rates WHERE source = ?`, source).Scan(&n)
	return n, err
}

// SetMeta stores a key/value pair (e.g. the time of the last sync).
func (s *Store) SetMeta(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO meta (key, value) VALUES (?, ?)
		ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// Meta reads a key, or "" if it is not set.
func (s *Store) Meta(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}
