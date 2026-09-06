package fx

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
)

// dayLayout is the YYYY-MM-DD form of core.Rate.AsOf and of the fx_rates.as_of
// column. It sorts lexicographically, which is what lets RateOn's date
// comparison run in SQL against a TEXT column.
const dayLayout = "2006-01-02"

// timestampLayout is how fetched_at is written. RFC 3339 in UTC, so the column
// is readable, unambiguous about zone, and parses back to the same instant.
const timestampLayout = time.RFC3339Nano

// Repo is the SQLite-backed [core.RateStore].
type Repo struct {
	read  *sql.DB
	write *sql.DB
}

// Repo must satisfy the port exactly; a drift in either is a compile error here
// rather than a wiring failure at startup.
var _ core.RateStore = (*Repo)(nil)

// NewRepo returns a Repo reading through read and writing through write.
//
// The two handles address the same database file. Reads go through the reader
// pool so that a rate lookup during a report never queues behind the daily
// refresh's write lock.
func NewRepo(read, write *sql.DB) *Repo {
	return &Repo{read: read, write: write}
}

// upsertRate writes one day's rate for one currency, replacing what is there.
//
// Re-fetching a day the table already holds is normal — the daily refresh runs
// on a schedule and may run twice — so a conflict updates rather than fails.
const upsertRate = `
INSERT INTO fx_rates (as_of, quote, rate, fetched_at)
VALUES (?, ?, ?, ?)
ON CONFLICT (as_of, quote) DO UPDATE SET
    rate       = excluded.rate,
    fetched_at = excluded.fetched_at`

// SaveRates upserts a day's rates in one transaction.
//
// All of them or none: a half-written day would give a report a rate for USD and
// none for GBP from the same publication, and the gap would look like a currency
// the ECB simply did not quote that day.
func (r *Repo) SaveRates(ctx context.Context, rates []core.Rate) error {
	if len(rates) == 0 {
		return nil
	}

	tx, err := r.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("fx: begin rate transaction: %w", err)
	}
	// Rollback after a successful Commit is a no-op, so this needs no flag.
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx, upsertRate)
	if err != nil {
		return fmt.Errorf("fx: prepare rate upsert: %w", err)
	}
	defer stmt.Close()

	for _, rate := range rates {
		_, err := stmt.ExecContext(ctx, rate.AsOf, rate.Quote, rate.Rate,
			rate.FetchedAt.UTC().Format(timestampLayout))
		if err != nil {
			return fmt.Errorf("fx: upsert rate %s %s: %w", rate.AsOf, rate.Quote, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("fx: commit rates: %w", err)
	}
	return nil
}

// rateOnQuery selects the newest rate for a currency that is not in the future.
const rateOnQuery = `
SELECT as_of, quote, rate, fetched_at
FROM fx_rates
WHERE quote = ? AND as_of <= ?
ORDER BY as_of DESC
LIMIT 1`

// RateOn returns the rate for quote on day, in YYYY-MM-DD.
//
// It falls back to the most recent EARLIER day rather than failing. The ECB
// publishes on business days only, so an exact-date lookup misses every
// Saturday, Sunday and public holiday — and second-hand goods sell hardest at
// the weekend, so the missing days are the ones a report needs most. Friday's
// rate is the rate that was in force on Sunday; it is the correct answer, not an
// approximation.
//
// The bound is one-sided on purpose: it never reaches FORWARD to a later
// publication, because a sale cannot have been settled at a rate that had not
// been published yet.
//
// It returns an error wrapping [core.ErrNotFound] only when no rate for quote
// exists on or before day at all.
func (r *Repo) RateOn(ctx context.Context, day, quote string) (core.Rate, error) {
	// rateOnQuery bounds as_of with a TEXT comparison, which is only correct for
	// a canonically formatted date: "2026-9-6" sorts after "2026-09-06" and would
	// quietly select the wrong row rather than fail. Normalising here, rather
	// than trusting every caller to have formatted it, is what makes that
	// impossible instead of unlikely.
	day, err := core.NormalizeDay(day)
	if err != nil {
		return core.Rate{}, fmt.Errorf("fx: read %s rate: %w", quote, err)
	}
	rate, err := scanRate(r.read.QueryRowContext(ctx, rateOnQuery, quote, day))
	if errors.Is(err, sql.ErrNoRows) {
		return core.Rate{}, fmt.Errorf("fx: no %s rate on or before %s: %w",
			quote, day, core.ErrNotFound)
	}
	if err != nil {
		return core.Rate{}, fmt.Errorf("fx: read %s rate for %s: %w", quote, day, err)
	}
	return rate, nil
}

// latestRatesQuery selects the newest held row for every quote currency.
const latestRatesQuery = `
SELECT f.as_of, f.quote, f.rate, f.fetched_at
FROM fx_rates AS f
JOIN (
    SELECT quote, MAX(as_of) AS as_of
    FROM fx_rates
    GROUP BY quote
) AS newest ON newest.quote = f.quote AND newest.as_of = f.as_of
ORDER BY f.quote`

// LatestRates returns the newest rate held for every quote currency, ordered by
// currency code so a rendered table does not reshuffle between requests.
func (r *Repo) LatestRates(ctx context.Context) ([]core.Rate, error) {
	rows, err := r.read.QueryContext(ctx, latestRatesQuery)
	if err != nil {
		return nil, fmt.Errorf("fx: list latest rates: %w", err)
	}
	defer rows.Close()

	var out []core.Rate
	for rows.Next() {
		rate, err := scanRate(rows)
		if err != nil {
			return nil, fmt.Errorf("fx: scan latest rate: %w", err)
		}
		out = append(out, rate)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("fx: read latest rates: %w", err)
	}
	return out, nil
}

// scanner is the one method [database/sql.Row] and [database/sql.Rows] share, so
// the single-row and multi-row paths can decode a row the same way.
type scanner interface {
	Scan(dest ...any) error
}

// scanRate decodes one fx_rates row in the column order every query above uses.
func scanRate(s scanner) (core.Rate, error) {
	var (
		rate      core.Rate
		fetchedAt string
	)
	if err := s.Scan(&rate.AsOf, &rate.Quote, &rate.Rate, &fetchedAt); err != nil {
		return core.Rate{}, err
	}
	t, err := time.Parse(time.RFC3339, fetchedAt)
	if err != nil {
		return core.Rate{}, fmt.Errorf("fetched_at %q: %w", fetchedAt, err)
	}
	rate.FetchedAt = t.UTC()
	return rate, nil
}
