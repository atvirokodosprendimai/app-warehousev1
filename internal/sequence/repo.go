// Package sequence hands out monotonic numbers for human-facing references.
//
// It exists because the alternative — MAX(column)+1 over the table that uses the
// reference — is wrong twice over: the column is a string, so "highest" would
// order lexically (WH0000009 beats WH0000010), and a deleted row would hand its
// number back to the next arrival, which is a genuine hazard when the previous
// number is written on a box somewhere.
package sequence

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
)

// ErrUnknownSequence reports a counter nobody has seeded.
//
// It is an error rather than a lazily-created row: a typo'd sequence name would
// otherwise silently start its own counter at 1 and mint duplicate references
// alongside the real one.
var ErrUnknownSequence = errors.New("sequence: unknown counter")

// Repo allocates from the counters table.
type Repo struct{ write *sql.DB }

// NewRepo returns a Repo.
//
// It takes only the WRITE handle: every operation here is a mutation, and the
// reader carries query_only(1) so it could not serve one anyway.
func NewRepo(write *sql.DB) *Repo { return &Repo{write: write} }

// NextSequence allocates the next value of a named counter.
//
// ⚠ ONE STATEMENT, DELIBERATELY. `UPDATE ... RETURNING` increments and reads in a
// single atomic operation, so two callers arriving together are serialised by
// the database and receive different numbers. The obvious alternative — SELECT
// the value, add one, UPDATE — is a read-then-write, which is exactly the shape
// this application's writer DSN exists to make safe and which would still be a
// lost update if anything ever widened the writer pool.
func (r *Repo) NextSequence(ctx context.Context, name string) (int64, error) {
	var n int64
	err := r.write.QueryRowContext(ctx,
		`UPDATE counters SET value = value + 1 WHERE name = ? RETURNING value`,
		name).Scan(&n)

	if errors.Is(err, sql.ErrNoRows) {
		return 0, fmt.Errorf("%w: %q", ErrUnknownSequence, name)
	}
	if err != nil {
		return 0, fmt.Errorf("sequence: allocate %s: %w", name, err)
	}
	return n, nil
}

// Compile-time proof that this satisfies the port the domain declares.
var _ core.Sequencer = (*Repo)(nil)
