// Package auth owns accounts: the SQLite repository behind [core.UserStore] and
// the write-side [Service] that mints, authenticates and disables them.
//
// The product has no public registration. Exactly one account is ever created
// without an existing administrator — the zero-user bootstrap in
// [Service.Register] — and every account after it is created by an admin
// through [Service.CreateUser].
package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
	"modernc.org/sqlite"
)

// ErrEmailTaken reports that an account already uses that sign-in address.
//
// It is a distinct sentinel rather than a wrapped driver error so that a handler
// can turn a duplicate registration into a field-level form message without
// pattern-matching on SQLite's wording.
var ErrEmailTaken = errors.New("auth: email already taken")

// ErrBootstrapClosed reports that the one-time admin bootstrap has already been
// used: somebody else inserted the first account first.
var ErrBootstrapClosed = errors.New("auth: bootstrap already closed")

// sqliteConstraintUnique is SQLITE_CONSTRAINT_UNIQUE, the extended result code
// SQLite returns when a UNIQUE index rejects a row.
//
// The users table has exactly one unique index besides its primary key — email —
// so this code on a users write means the login was taken. A duplicate id
// reports SQLITE_CONSTRAINT_PRIMARYKEY (1555) instead and is deliberately left
// as a raw error: a colliding UUID is a bug, not a taken address, and reporting
// it as one would send the operator looking in the wrong place.
const sqliteConstraintUnique = 2067

// timeLayout is how every timestamp in this package is stored: RFC3339, always
// in UTC.
//
// One layout with a fixed zone keeps lexical order and chronological order the
// same, which is what lets "ORDER BY created_at" mean "oldest first" without
// parsing every row. A mixed-offset column would silently break that.
const timeLayout = time.RFC3339

// userColumns is the column list, in the order [scanUser] reads them. Every
// query in this file shares it so that adding a column cannot leave one SELECT
// scanning into the wrong field.
const userColumns = "id, email, password_hash, display_name, is_admin, created_at, disabled_at"

// Repo is a [core.UserStore] backed by SQLite.
//
// It holds two handles to the same database on purpose: reads go to read, writes
// go to write. That is the CQRS split made physical — the reader DSN carries
// query_only(1), so a write that took the wrong path is refused by the driver
// rather than found in review.
type Repo struct {
	read  *sql.DB
	write *sql.DB
}

// Repo must satisfy the port it exists to implement; catch a signature drift at
// compile time rather than at the call site.
var _ core.UserStore = (*Repo)(nil)

// NewRepo returns a Repo reading through read and writing through write.
//
// The two handles are expected to address the same database file, as
// internal/store opens it.
func NewRepo(read, write *sql.DB) *Repo {
	return &Repo{read: read, write: write}
}

// User returns one account by id, or an error wrapping [core.ErrNotFound].
func (r *Repo) User(ctx context.Context, id string) (core.User, error) {
	row := r.read.QueryRowContext(ctx,
		"SELECT "+userColumns+" FROM users WHERE id = ?", id)
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return core.User{}, fmt.Errorf("auth: user %q: %w", id, core.ErrNotFound)
	}
	if err != nil {
		return core.User{}, fmt.Errorf("auth: user %q: %w", id, err)
	}
	return u, nil
}

// UserByEmail resolves a sign-in identifier, or returns an error wrapping
// [core.ErrNotFound].
//
// The address is folded before the lookup because the column stores it folded:
// leaving that to callers turns a capital letter typed into a login box into a
// silent "no such account", and a doc comment does not prevent that.
func (r *Repo) UserByEmail(ctx context.Context, email string) (core.User, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	row := r.read.QueryRowContext(ctx,
		"SELECT "+userColumns+" FROM users WHERE email = ?", email)
	u, err := scanUser(row)
	if errors.Is(err, sql.ErrNoRows) {
		return core.User{}, fmt.Errorf("auth: user %q: %w", email, core.ErrNotFound)
	}
	if err != nil {
		return core.User{}, fmt.Errorf("auth: user %q: %w", email, err)
	}
	return u, nil
}

// Users lists every account, oldest first.
//
// The id is a secondary sort key because created_at has one-second resolution:
// two accounts made in the same second would otherwise come back in whatever
// order the query planner chose that day.
func (r *Repo) Users(ctx context.Context) ([]core.User, error) {
	rows, err := r.read.QueryContext(ctx,
		"SELECT "+userColumns+" FROM users ORDER BY created_at, id")
	if err != nil {
		return nil, fmt.Errorf("auth: list users: %w", err)
	}
	defer rows.Close()

	var users []core.User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, fmt.Errorf("auth: list users: %w", err)
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("auth: list users: %w", err)
	}
	return users, nil
}

// CountUsers reports how many accounts exist. Zero is what opens the one-time
// admin bootstrap, so this read is security-relevant.
func (r *Repo) CountUsers(ctx context.Context) (int, error) {
	var n int
	if err := r.read.QueryRowContext(ctx,
		"SELECT count(*) FROM users").Scan(&n); err != nil {
		return 0, fmt.Errorf("auth: count users: %w", err)
	}
	return n, nil
}

// CreateUser inserts an account.
//
// It returns [ErrEmailTaken] rather than overwriting when the address is already
// in use, so that a caller cannot silently reassign somebody's login. The
// timestamps are the caller's: a repository that stamped its own clock would
// make a test's fixed time unreproducible.
func (r *Repo) CreateUser(ctx context.Context, u core.User) error {
	_, err := r.write.ExecContext(ctx,
		"INSERT INTO users ("+userColumns+") VALUES (?, ?, ?, ?, ?, ?, ?)",
		u.ID, u.Email, u.PasswordHash, u.DisplayName, boolToInt(u.IsAdmin),
		formatTime(u.CreatedAt), nullableTime(u.DisabledAt))
	if err != nil {
		return fmt.Errorf("auth: create user %q: %w", u.Email, mapEmailConflict(err))
	}
	return nil
}

// UpdateUser overwrites the mutable columns of an existing account and returns
// an error wrapping [core.ErrNotFound] when there is no such id.
//
// created_at is not in the SET list: when the account was made is a fact about
// the past, and a caller that round-trips a user through a form must not be able
// to rewrite it.
func (r *Repo) UpdateUser(ctx context.Context, u core.User) error {
	res, err := r.write.ExecContext(ctx,
		`UPDATE users
		    SET email = ?, password_hash = ?, display_name = ?,
		        is_admin = ?, disabled_at = ?
		  WHERE id = ?`,
		u.Email, u.PasswordHash, u.DisplayName, boolToInt(u.IsAdmin),
		nullableTime(u.DisabledAt), u.ID)
	if err != nil {
		return fmt.Errorf("auth: update user %q: %w", u.ID, mapEmailConflict(err))
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("auth: update user %q: %w", u.ID, err)
	}
	if n == 0 {
		return fmt.Errorf("auth: update user %q: %w", u.ID, core.ErrNotFound)
	}
	return nil
}

// CreateFirstAdmin inserts u as an administrator only if the users table is
// still empty, and returns [ErrBootstrapClosed] when it is not.
//
// The emptiness test and the insert are ONE statement on purpose. A
// "SELECT count(*)" followed by a separate INSERT promises nothing: two requests
// arriving together both read zero and both become admin, and the second admin
// is a stranger with full access. INSERT ... SELECT ... WHERE NOT EXISTS runs
// inside SQLite's implicit transaction for the statement, holding the write lock
// across both halves, so the losing request inserts nothing and says so through
// RowsAffected.
func (r *Repo) CreateFirstAdmin(ctx context.Context, u core.User) error {
	res, err := r.write.ExecContext(ctx,
		`INSERT INTO users (`+userColumns+`)
		 SELECT ?, ?, ?, ?, 1, ?, ?
		  WHERE NOT EXISTS (SELECT 1 FROM users)`,
		u.ID, u.Email, u.PasswordHash, u.DisplayName,
		formatTime(u.CreatedAt), nullableTime(u.DisabledAt))
	if err != nil {
		return fmt.Errorf("auth: create first admin %q: %w", u.Email, mapEmailConflict(err))
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("auth: create first admin %q: %w", u.Email, err)
	}
	if n == 0 {
		return fmt.Errorf("auth: create first admin %q: %w", u.Email, ErrBootstrapClosed)
	}
	return nil
}

// scanner is the part of *sql.Row and *sql.Rows that scanUser needs, so the one
// row mapping serves both the single-row lookups and the listing.
type scanner interface {
	Scan(dest ...any) error
}

// scanUser maps one users row onto a [core.User].
func scanUser(s scanner) (core.User, error) {
	var (
		u          core.User
		createdAt  string
		disabledAt sql.NullString
	)
	if err := s.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.DisplayName,
		&u.IsAdmin, &createdAt, &disabledAt); err != nil {
		return core.User{}, err
	}

	var err error
	if u.CreatedAt, err = parseTime(createdAt); err != nil {
		return core.User{}, err
	}
	// disabled_at is the nullable column: NULL is an active account, and the
	// domain models that absence as a nil pointer rather than a zero time.
	if disabledAt.Valid {
		t, err := parseTime(disabledAt.String)
		if err != nil {
			return core.User{}, err
		}
		u.DisabledAt = &t
	}
	return u, nil
}

// formatTime renders t for storage.
func formatTime(t time.Time) string { return t.UTC().Format(timeLayout) }

// parseTime reads a stored timestamp back as UTC.
func parseTime(s string) (time.Time, error) {
	t, err := time.Parse(timeLayout, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse timestamp %q: %w", s, err)
	}
	return t.UTC(), nil
}

// nullableTime renders an optional timestamp as a bind value, mapping a nil
// pointer to SQL NULL.
func nullableTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return formatTime(*t)
}

// boolToInt encodes a flag for the INTEGER column that holds it. The users table
// is STRICT with a CHECK (is_admin IN (0, 1)), so the encoding is stated here
// rather than left to whatever the driver does with a Go bool.
func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// mapEmailConflict translates SQLite's unique-violation into [ErrEmailTaken] and
// passes every other error through untouched.
func mapEmailConflict(err error) error {
	var serr *sqlite.Error
	if errors.As(err, &serr) && serr.Code() == sqliteConstraintUnique {
		return ErrEmailTaken
	}
	return err
}
