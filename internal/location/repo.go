// Package location stores and rearranges the warehouse address tree.
//
// A location is addressed two ways at once, and the difference is the whole
// design. Its id is immutable and is what stock points at, so re-labelling a
// shelf never orphans an item. Its path is the materialised human address —
// "KAUNAS-GARAGE/R1/S3/A000005" — which is what an operator types, pastes and
// reads, and which therefore has to change the moment any node above it is
// renamed or moved. Every node under a renamed node is re-addressed in the same
// transaction as the node itself; a tree half-way through a rename is a tree
// whose paths lie.
//
// [Repo] is the storage adapter, [Service] the write side that keeps the two
// addressings consistent.
package location

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
)

// Repo reads and writes the storage tree in SQLite.
//
// It holds the two handles the application opens over one SQLite file: reads
// are served by the many-reader handle, every mutation goes to the
// single-writer handle. The
// split is not a convention this type is trusted to honour — the read handle is
// opened query_only, so a write attempted on it is refused by the driver.
type Repo struct {
	read  *sql.DB
	write *sql.DB
}

// NewRepo returns a Repo serving reads from read and writes through write.
func NewRepo(read, write *sql.DB) *Repo {
	return &Repo{read: read, write: write}
}

// Repo is the only implementation of both the domain port and the transactional
// subtree move the service needs; the assertions keep that a compile error
// rather than a runtime surprise.
var (
	_ core.LocationStore = (*Repo)(nil)
	_ Store              = (*Repo)(nil)
)

// locationColumns is the column list every read shares, in the order
// [scanLocation] expects.
const locationColumns = `id, parent_id, kind, code, path, label, custodian, ` +
	`custodian_contact, city, country, notes, created_at`

// timeLayout is how a timestamp is written to the TEXT created_at column.
// RFC3339 with nanoseconds sorts lexicographically in the same order it sorts
// chronologically, which a report can rely on.
const timeLayout = time.RFC3339Nano

// rowScanner is satisfied by both *sql.Row and *sql.Rows, so one scan function
// serves the single-row and the multi-row reads.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanLocation reads one row in locationColumns order.
func scanLocation(s rowScanner) (core.Location, error) {
	var (
		l         core.Location
		parent    sql.NullString
		kind      string
		createdAt string
	)
	if err := s.Scan(&l.ID, &parent, &kind, &l.Code, &l.Path, &l.Label, &l.Custodian,
		&l.CustodianContact, &l.City, &l.Country, &l.Notes, &createdAt); err != nil {
		return core.Location{}, err
	}
	l.ParentID = parent.String
	l.Kind = core.Kind(kind)
	t, err := time.Parse(timeLayout, createdAt)
	if err != nil {
		return core.Location{}, fmt.Errorf("location: created_at of %s: %w", l.ID, err)
	}
	l.CreatedAt = t.UTC()
	return l, nil
}

// scanLocations drains rows into a slice.
func scanLocations(rows *sql.Rows) ([]core.Location, error) {
	defer rows.Close()
	out := []core.Location{}
	for rows.Next() {
		l, err := scanLocation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// parentValue maps an empty parent id to SQL NULL, which is how a site — the
// root of a tree — is stored.
func parentValue(parentID string) any {
	if parentID == "" {
		return nil
	}
	return parentID
}

// Location returns one node by id, or an error wrapping [core.ErrNotFound].
func (r *Repo) Location(ctx context.Context, id string) (core.Location, error) {
	row := r.read.QueryRowContext(ctx,
		`SELECT `+locationColumns+` FROM locations WHERE id = ?`, id)
	l, err := scanLocation(row)
	if errors.Is(err, sql.ErrNoRows) {
		return core.Location{}, fmt.Errorf("location %s: %w", id, core.ErrNotFound)
	}
	if err != nil {
		return core.Location{}, fmt.Errorf("location: read %s: %w", id, err)
	}
	return l, nil
}

// LocationByPath resolves a materialised path such as "KAUNAS/R1/A000005", or
// returns an error wrapping [core.ErrNotFound]. The path is unique across the
// whole tree, so a pasted address resolves to exactly one place.
func (r *Repo) LocationByPath(ctx context.Context, path string) (core.Location, error) {
	row := r.read.QueryRowContext(ctx,
		`SELECT `+locationColumns+` FROM locations WHERE path = ?`, path)
	l, err := scanLocation(row)
	if errors.Is(err, sql.ErrNoRows) {
		return core.Location{}, fmt.Errorf("location %q: %w", path, core.ErrNotFound)
	}
	if err != nil {
		return core.Location{}, fmt.Errorf("location: read %q: %w", path, err)
	}
	return l, nil
}

// Children returns the direct children of parentID ordered by path.
//
// An EMPTY parentID returns the sites, because a site's parent_id is NULL and
// "= NULL" matches nothing in SQL — the two cases need different statements,
// not different arguments.
func (r *Repo) Children(ctx context.Context, parentID string) ([]core.Location, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if parentID == "" {
		rows, err = r.read.QueryContext(ctx,
			`SELECT `+locationColumns+` FROM locations WHERE parent_id IS NULL ORDER BY path`)
	} else {
		rows, err = r.read.QueryContext(ctx,
			`SELECT `+locationColumns+` FROM locations WHERE parent_id = ? ORDER BY path`, parentID)
	}
	if err != nil {
		return nil, fmt.Errorf("location: children of %q: %w", parentID, err)
	}
	out, err := scanLocations(rows)
	if err != nil {
		return nil, fmt.Errorf("location: children of %q: %w", parentID, err)
	}
	return out, nil
}

// ancestorsQuery walks parent_id upward from the node and hands back the chain
// without it.
//
// depth counts hops from the node, so ORDER BY depth DESC yields the site first
// and the node's own parent last — the outward-to-inward order
// [core.Location.Where] resolves inherited custodian and city by. One recursive
// statement rather than a query per level keeps the cost of a deep tree one
// round trip.
const ancestorsQuery = `
WITH RECURSIVE chain(depth, ` + locationColumns + `) AS (
    SELECT 0, ` + locationColumns + ` FROM locations WHERE id = ?
    UNION ALL
    SELECT c.depth + 1, l.id, l.parent_id, l.kind, l.code, l.path, l.label,
           l.custodian, l.custodian_contact, l.city, l.country, l.notes, l.created_at
    FROM locations l JOIN chain c ON l.id = c.parent_id
)
SELECT ` + locationColumns + ` FROM chain WHERE depth > 0 ORDER BY depth DESC`

// Ancestors returns the chain from the site down to, but excluding, id.
//
// A site has no ancestors and an unknown id has none either; neither is an
// error, because the caller has already loaded the node it is asking about.
func (r *Repo) Ancestors(ctx context.Context, id string) ([]core.Location, error) {
	rows, err := r.read.QueryContext(ctx, ancestorsQuery, id)
	if err != nil {
		return nil, fmt.Errorf("location: ancestors of %s: %w", id, err)
	}
	out, err := scanLocations(rows)
	if err != nil {
		return nil, fmt.Errorf("location: ancestors of %s: %w", id, err)
	}
	return out, nil
}

// AllLocations returns every node ordered by path.
//
// Path order is tree order: a node sorts immediately before its own subtree and
// after its earlier siblings, so a picker can render the result as an indented
// tree without sorting or grouping it again.
func (r *Repo) AllLocations(ctx context.Context) ([]core.Location, error) {
	rows, err := r.read.QueryContext(ctx,
		`SELECT `+locationColumns+` FROM locations ORDER BY path`)
	if err != nil {
		return nil, fmt.Errorf("location: list: %w", err)
	}
	out, err := scanLocations(rows)
	if err != nil {
		return nil, fmt.Errorf("location: list: %w", err)
	}
	return out, nil
}

// CreateLocation inserts a node exactly as given. Assigning the id, composing
// the path and stamping the time are [Service.Create]'s job, so that a caller
// cannot insert a node whose path disagrees with its parent's.
//
// A duplicate code or path is returned as [ErrCodeTaken] or [ErrPathTaken].
func (r *Repo) CreateLocation(ctx context.Context, l core.Location) error {
	_, err := r.write.ExecContext(ctx,
		`INSERT INTO locations (`+locationColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?)`,
		l.ID, parentValue(l.ParentID), string(l.Kind), l.Code, l.Path, l.Label, l.Custodian,
		l.CustodianContact, l.City, l.Country, l.Notes, l.CreatedAt.UTC().Format(timeLayout))
	if err != nil {
		return fmt.Errorf("location: insert %q: %w", l.Path, uniqueViolation(err))
	}
	return nil
}

// UpdateLocation writes every mutable column of l.
//
// It re-addresses ONLY the row it is given. A node's path is a prefix of every
// path beneath it, so changing path or parent here would leave the descendants
// pointing at an address that no longer exists — use [Service.Rename] or
// [Service.Move], which rewrite the subtree in the same transaction.
func (r *Repo) UpdateLocation(ctx context.Context, l core.Location) error {
	res, err := r.write.ExecContext(ctx,
		`UPDATE locations SET parent_id = ?, kind = ?, code = ?, path = ?, label = ?,
		    custodian = ?, custodian_contact = ?, city = ?, country = ?, notes = ?
		 WHERE id = ?`,
		parentValue(l.ParentID), string(l.Kind), l.Code, l.Path, l.Label, l.Custodian,
		l.CustodianContact, l.City, l.Country, l.Notes, l.ID)
	if err != nil {
		return fmt.Errorf("location: update %s: %w", l.ID, uniqueViolation(err))
	}
	return affectedOne(res, l.ID)
}

// DeleteLocation removes one node.
//
// The database refuses the delete while anything still references the row, but
// its FOREIGN KEY error does not say WHICH reference held it — a child location
// and a stored offer both trip the same ON DELETE RESTRICT. Classifying it is
// therefore [Service.Delete]'s job: it rules out children first, so the refusal
// that survives can only be stock.
func (r *Repo) DeleteLocation(ctx context.Context, id string) error {
	res, err := r.write.ExecContext(ctx, `DELETE FROM locations WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("location: delete %s: %w", id, err)
	}
	return affectedOne(res, id)
}

// MoveSubtree re-addresses l and everything beneath it in ONE transaction.
//
// l must already carry its new ParentID, Code and Path; oldPath is the address
// it is leaving. Splitting the two updates across transactions would leave a
// window in which a descendant's path names a node that no longer exists there,
// and a reader in that window gets a wrong answer rather than an error.
func (r *Repo) MoveSubtree(ctx context.Context, l core.Location, oldPath string) error {
	tx, err := r.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("location: begin move of %q: %w", oldPath, err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op once Commit has succeeded

	res, err := tx.ExecContext(ctx,
		`UPDATE locations SET parent_id = ?, code = ?, path = ? WHERE id = ?`,
		parentValue(l.ParentID), l.Code, l.Path, l.ID)
	if err != nil {
		return fmt.Errorf("location: re-address %q as %q: %w", oldPath, l.Path, uniqueViolation(err))
	}
	if err := affectedOne(res, l.ID); err != nil {
		return err
	}

	// The descendants are exactly the rows whose path begins with the old path
	// plus a separator — the separator matters, because without it renaming "A"
	// would also catch the unrelated site "AB".
	//
	// The prefix is matched with substr and NOT with LIKE. "_" and "%" are LIKE
	// wildcards, this schema forbids only "/" in a code, and so a real code like
	// "A_B" turned into a LIKE pattern would silently match "AXB" and rewrite a
	// subtree nobody touched. substr has no metacharacters, so there is nothing
	// to escape and nothing to get wrong.
	//
	// SQLite's substr and length count CHARACTERS, not bytes, so the offsets are
	// rune counts: a site called "ŠIAULIAI" would otherwise slice one rune short
	// of its separator and match none of its own children.
	oldPrefix := oldPath + "/"
	prefixLen := utf8.RuneCountInString(oldPrefix)
	if _, err := tx.ExecContext(ctx,
		`UPDATE locations SET path = ? || substr(path, ?) WHERE substr(path, 1, ?) = ?`,
		l.Path+"/", prefixLen+1, prefixLen, oldPrefix); err != nil {
		return fmt.Errorf("location: re-address subtree of %q: %w", oldPath, uniqueViolation(err))
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("location: commit move of %q: %w", oldPath, err)
	}
	return nil
}

// affectedOne turns "no such row" into [core.ErrNotFound]. A write that matched
// nothing is a failure the caller has to hear about: silently succeeding is how
// a double-submitted delete reads as a delete that worked.
func affectedOne(res sql.Result, id string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("location: rows affected for %s: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("location %s: %w", id, core.ErrNotFound)
	}
	return nil
}

// uniqueViolation maps SQLite's UNIQUE refusals onto the package's sentinels,
// leaving anything else untouched.
//
// The index named in the message is what distinguishes them, and which index
// fires is not symmetric between a site and a deeper node: UNIQUE(parent_id,
// code) is inert at the root, because SQL treats every NULL parent_id as
// distinct from every other. A site's code is therefore guarded only by
// UNIQUE(path) and a duplicate site code arrives as [ErrPathTaken] — which for
// a site is the same statement, since a site's path IS its code.
func uniqueViolation(err error) error {
	switch msg := err.Error(); {
	case strings.Contains(msg, "UNIQUE constraint failed: locations.parent_id, locations.code"):
		return fmt.Errorf("%w: %w", ErrCodeTaken, err)
	case strings.Contains(msg, "UNIQUE constraint failed: locations.path"):
		return fmt.Errorf("%w: %w", ErrPathTaken, err)
	default:
		return err
	}
}

// restrictedByReference reports whether err is the database refusing to remove
// a row something still points at.
func restrictedByReference(err error) bool {
	return err != nil && strings.Contains(err.Error(), "FOREIGN KEY constraint failed")
}
