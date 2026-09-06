// Package offer holds the offer aggregate's persistence and its write side.
//
// It follows the project's CQRS split. [Repo] is the SQLite adapter that
// implements core.OfferStore; [Service] is the only thing that mutates an offer.
// A read model is handed a core.OfferReader and therefore cannot write, and the
// reader database handle carries query_only(1) so the driver refuses a write
// even if the compiler were somehow persuaded to allow one.
package offer

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
)

// ErrSKUTaken reports that an offer already holds the SKU being written.
//
// It is a sentinel rather than a driver error because the SKU is operator-typed:
// a collision is a form validation failure the operator can fix, not a fault.
var ErrSKUTaken = errors.New("offer: SKU already taken")

// defaultLimit bounds a listing whose filter asked for no limit. An unbounded
// dashboard query is fine on the day it is written and loads the whole warehouse
// a year later, so the repository refuses to have no ceiling at all.
const defaultLimit = 100

// sqliteConstraintUnique is SQLITE_CONSTRAINT_UNIQUE, the extended result code
// SQLite returns for a violated UNIQUE index.
const sqliteConstraintUnique = 2067

// timeLayout is the on-disk timestamp format. Every stored time is RFC3339 in
// UTC, so the text sorts in the same order as the instants it names — which is
// what lets "newest first" be an ORDER BY on a TEXT column.
const timeLayout = time.RFC3339

// offerColumns is the select list for an offer row, aliased to o so it can be
// reused by a query that joins locations.
const offerColumns = `o.id, o.sku, o.title, o.description, o.condition, o.status,
	o.quantity, o.shop_minor, o.shop_currency, o.owner_minor, o.owner_currency,
	o.sold_minor, o.sold_currency, o.sold_at, o.location_id, o.created_at, o.updated_at`

// photoColumns is the select list for a photo row.
const photoColumns = `id, offer_id, position, filename, content_type, byte_size, sha256, created_at`

// Repo is the SQLite-backed offer store.
//
// It holds two handles onto the same database file: reads go through read, which
// is a concurrent pool, and every mutation goes through write, which is the
// single writer. Nothing in this type writes through read.
type Repo struct {
	read  *sql.DB
	write *sql.DB
}

// Repo must satisfy the whole port; this fails the build rather than a request
// if a method signature drifts from core.
var _ core.OfferStore = (*Repo)(nil)

// NewRepo returns a Repo reading through read and writing through write.
//
// The two handles are expected to address the same database file with different
// pragmas, as internal/store.Open produces them.
func NewRepo(read, write *sql.DB) *Repo {
	return &Repo{read: read, write: write}
}

// Offer returns the offer with this id, photos included, or core.ErrNotFound.
func (r *Repo) Offer(ctx context.Context, id string) (core.Offer, error) {
	return r.oneOffer(ctx, "o.id = ?", id)
}

// OfferBySKU resolves the operator-facing handle, photos included, or
// core.ErrNotFound.
func (r *Repo) OfferBySKU(ctx context.Context, sku string) (core.Offer, error) {
	return r.oneOffer(ctx, "o.sku = ?", sku)
}

// oneOffer loads a single offer by an equality predicate and populates its
// photos.
//
// The photos are loaded here rather than left to the caller because the read
// model is a function of the id alone. A caller that had to remember a second
// call would remember it on one of its two code paths and forget it on the
// other, and the one that forgets renders an offer with no pictures.
func (r *Repo) oneOffer(ctx context.Context, where string, arg any) (core.Offer, error) {
	q := "SELECT " + offerColumns + " FROM offers o WHERE " + where
	o, err := scanOffer(r.read.QueryRowContext(ctx, q, arg))
	if errors.Is(err, sql.ErrNoRows) {
		return core.Offer{}, fmt.Errorf("offer %v: %w", arg, core.ErrNotFound)
	}
	if err != nil {
		return core.Offer{}, fmt.Errorf("offer %v: %w", arg, err)
	}
	photos, err := r.photosByOffer(ctx, []string{o.ID})
	if err != nil {
		return core.Offer{}, err
	}
	o.Photos = photos[o.ID]
	return o, nil
}

// Offers lists the offers matching f, newest first, each with its photos in
// display order.
//
// Every field of f narrows the result and a zero f means everything, bounded by
// [defaultLimit].
func (r *Repo) Offers(ctx context.Context, f core.OfferFilter) ([]core.Offer, error) {
	q, args := listQuery(f)

	rows, err := r.read.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("offer: list: %w", err)
	}
	defer rows.Close()

	var (
		out []core.Offer
		ids []string
	)
	for rows.Next() {
		o, err := scanOffer(rows)
		if err != nil {
			return nil, fmt.Errorf("offer: list: %w", err)
		}
		out = append(out, o)
		ids = append(ids, o.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("offer: list: %w", err)
	}

	// One photo query for the whole page, never one per offer. An N+1 here is
	// invisible on the ten rows a developer tests with and becomes several
	// hundred round trips per render once the warehouse is real.
	byOffer, err := r.photosByOffer(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Photos = byOffer[out[i].ID]
	}
	return out, nil
}

// listQuery builds the SELECT and its arguments for a filter.
//
// It is separate from [Repo.Offers] so the SQL can be reasoned about — and
// tested — without a database round trip in the way.
func listQuery(f core.OfferFilter) (string, []any) {
	var (
		join   string
		where  []string
		args   []any
		ranked bool
	)

	if len(f.Status) > 0 {
		ph := make([]string, len(f.Status))
		for i, s := range f.Status {
			ph[i] = "?"
			args = append(args, string(s))
		}
		where = append(where, "o.status IN ("+strings.Join(ph, ", ")+")")
	}

	if p := strings.TrimSpace(f.LocationPathPrefix); p != "" {
		// The prefix is a value, so it is escaped rather than concatenated into
		// the pattern. "_" and "%" are LIKE syntax and a location code may
		// legitimately contain an underscore: an unescaped "A_B" would also
		// match the unrelated subtree "AXB", silently widening a filter the
		// operator wrote to narrow. ESCAPE '\' declares the escape character
		// that [likeEscape] applied.
		join += " JOIN locations l ON l.id = o.location_id"
		where = append(where, `l.path LIKE ? ESCAPE '\'`)
		args = append(args, likeEscape(p)+"%")
	}

	if q := strings.TrimSpace(f.Query); q != "" {
		// ★ A REFERENCE TYPED OFF A LABEL IS MATCHED EXACTLY, and this is half of
		// what makes a short reference worth having. Somebody reading WH0000042
		// off a box types "42", or "wh42", and will not reproduce the zero
		// padding — so the number is normalised back to the canonical reference
		// and matched against the column directly. The text search still runs
		// beside it, because "42" might also appear in a description.
		//
		// The full-text half is not LIKE: a substring match cannot answer "brass
		// lamp" for a row titled "Lamp, brass" — both words present, wrong order,
		// split across fields, which is the ordinary shape of a search box query
		// rather than an edge case. The index is external-content, so it reads
		// its columns back from `offers` and keeps no second copy of every
		// description.
		if sku := core.NormalizeSKUQuery(q); sku != "" {
			// A subquery rather than a join, because this arm ORs two conditions
			// and a joined row would drop anything matching only the reference.
			where = append(where,
				`(o.sku = ? OR o.rowid IN (SELECT rowid FROM offers_fts WHERE offers_fts MATCH ?))`)
			args = append(args, sku, ftsQuery(q))
			// Recency, not relevance: an exact reference match has no rank to
			// order by, and there is rarely more than one of them anyway.
		} else {
			join += " JOIN offers_fts fts ON fts.rowid = o.rowid"
			where = append(where, "offers_fts MATCH ?")
			args = append(args, ftsQuery(q))
			ranked = true
		}
	}

	// A price bound also pins the currency. Minor units are only comparable
	// within one — 5000 is 50 EUR and also 5000 JPY — so a bound that ignored it
	// would compare against the wrong scale and quietly return the wrong stock.
	if f.PriceMin != nil {
		where = append(where, "o.shop_currency = ? AND o.shop_minor >= ?")
		args = append(args, f.PriceMin.Currency, f.PriceMin.Minor)
	}
	if f.PriceMax != nil {
		where = append(where, "o.shop_currency = ? AND o.shop_minor <= ?")
		args = append(args, f.PriceMax.Currency, f.PriceMax.Minor)
	}

	if f.NeedsPricing {
		// The pricing queue, matching core.Offer.NeedsPricing: photographed and
		// titled, not yet researched.
		where = append(where, "o.status = 'draft' AND o.shop_minor = 0")
	}

	q := "SELECT " + offerColumns + " FROM offers o" + join
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}

	limit := f.Limit
	if limit <= 0 {
		limit = defaultLimit
	}
	offset := f.Offset
	if offset < 0 {
		offset = 0
	}

	// A search orders by relevance; a plain listing orders by recency. Ordering
	// search results by date instead would bury an exact title match under
	// whatever happened to be taken in this morning. id breaks either tie, so
	// that two offers sharing a rank or a second come back in a stable order —
	// without it a page boundary can show one row twice and skip another.
	order := " ORDER BY o.created_at DESC, o.id DESC"
	if ranked {
		order = " ORDER BY fts.rank, o.id DESC"
	}
	q += order + " LIMIT ? OFFSET ?"
	args = append(args, limit, offset)

	return q, args
}

// ftsQuery turns what somebody typed into an FTS5 query.
//
// ⚠ The raw string CANNOT be passed through. FTS5 MATCH takes a query language,
// not a phrase: a bare `"` is a syntax error that fails the whole search, and
// bare words like AND, OR, NOT and NEAR are OPERATORS — so searching for the
// word "not" would either error or mean something entirely different from what
// was typed.
//
// Every token is therefore quoted, which makes it a literal, and given a `*` so
// that a half-typed word still matches. Tokens are joined with AND because
// adding a word to a search should narrow it; OR would make every extra word
// return more, which is the opposite of what anyone expects.
func ftsQuery(s string) string {
	fields := strings.Fields(s)
	terms := make([]string, 0, len(fields))
	for _, w := range fields {
		// A double quote inside a quoted FTS5 string is escaped by doubling it,
		// the same as in SQL.
		w = strings.ReplaceAll(w, `"`, `""`)
		terms = append(terms, `"`+w+`"*`)
	}
	return strings.Join(terms, " AND ")
}

// CountByStatus returns how many offers sit in each status.
//
// A status holding nothing is absent from the map rather than present as zero;
// reading a missing key from a Go map yields 0, which is exactly what a
// dashboard tile wants to render.
func (r *Repo) CountByStatus(ctx context.Context) (map[core.Status]int, error) {
	rows, err := r.read.QueryContext(ctx, `SELECT status, COUNT(*) FROM offers GROUP BY status`)
	if err != nil {
		return nil, fmt.Errorf("offer: count by status: %w", err)
	}
	defer rows.Close()

	out := make(map[core.Status]int, len(core.Statuses()))
	for rows.Next() {
		var (
			status string
			n      int
		)
		if err := rows.Scan(&status, &n); err != nil {
			return nil, fmt.Errorf("offer: count by status: %w", err)
		}
		out[core.Status(status)] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("offer: count by status: %w", err)
	}
	return out, nil
}

// CreateOffer inserts a new offer. A SKU already in use returns [ErrSKUTaken].
func (r *Repo) CreateOffer(ctx context.Context, o core.Offer) error {
	const q = `INSERT INTO offers (id, sku, title, description, condition, status, quantity,
		shop_minor, shop_currency, owner_minor, owner_currency, sold_minor, sold_currency,
		sold_at, location_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := r.write.ExecContext(ctx, q,
		o.ID, o.SKU, o.Title, o.Description, o.Condition, string(o.Status), o.Quantity,
		o.Shop.Minor, o.Shop.Currency, o.Owner.Minor, o.Owner.Currency,
		o.Sold.Minor, o.Sold.Currency, nullTime(o.SoldAt), nullString(o.LocationID),
		formatTime(o.CreatedAt), formatTime(o.UpdatedAt))
	if err != nil {
		return writeErr("create offer", o.SKU, err)
	}
	return nil
}

// UpdateOffer rewrites every mutable column of an existing offer and returns
// core.ErrNotFound when there is no such row.
//
// created_at is deliberately not in the SET list: when the item was taken in is
// not something an edit may change, and leaving it out also means a caller that
// passes a partially-filled struct cannot erase it. A rename onto another
// offer's SKU returns [ErrSKUTaken].
func (r *Repo) UpdateOffer(ctx context.Context, o core.Offer) error {
	const q = `UPDATE offers SET sku = ?, title = ?, description = ?, condition = ?,
		status = ?, quantity = ?, shop_minor = ?, shop_currency = ?, owner_minor = ?,
		owner_currency = ?, sold_minor = ?, sold_currency = ?, sold_at = ?,
		location_id = ?, updated_at = ? WHERE id = ?`
	res, err := r.write.ExecContext(ctx, q,
		o.SKU, o.Title, o.Description, o.Condition, string(o.Status), o.Quantity,
		o.Shop.Minor, o.Shop.Currency, o.Owner.Minor, o.Owner.Currency,
		o.Sold.Minor, o.Sold.Currency, nullTime(o.SoldAt), nullString(o.LocationID),
		formatTime(o.UpdatedAt), o.ID)
	if err != nil {
		return writeErr("update offer", o.SKU, err)
	}
	return checkAffected(res, "update offer", o.ID)
}

// DeleteOffer removes an offer, or returns core.ErrNotFound.
//
// The photo ROWS go with it through the schema's ON DELETE CASCADE. The photo
// BYTES do not: a blob store cannot join a database transaction, so removing
// them is [Service.Delete]'s job.
func (r *Repo) DeleteOffer(ctx context.Context, id string) error {
	res, err := r.write.ExecContext(ctx, `DELETE FROM offers WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("offer: delete offer: %w", err)
	}
	return checkAffected(res, "delete offer", id)
}

// AddPhoto inserts a photo row. The bytes are written separately and first; see
// [Service.AddPhoto] for why that order is not negotiable.
func (r *Repo) AddPhoto(ctx context.Context, p core.Photo) error {
	const q = `INSERT INTO offer_photos (` + photoColumns + `) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := r.write.ExecContext(ctx, q,
		p.ID, p.OfferID, p.Position, p.Filename, p.ContentType, p.ByteSize, p.SHA256,
		formatTime(p.CreatedAt))
	if err != nil {
		return fmt.Errorf("offer: add photo: %w", err)
	}
	return nil
}

// DeletePhoto removes a photo row, or returns core.ErrNotFound.
func (r *Repo) DeletePhoto(ctx context.Context, photoID string) error {
	res, err := r.write.ExecContext(ctx, `DELETE FROM offer_photos WHERE id = ?`, photoID)
	if err != nil {
		return fmt.Errorf("offer: delete photo: %w", err)
	}
	return checkAffected(res, "delete photo", photoID)
}

// ReorderPhotos rewrites the display positions of an offer's photos, in order,
// in one transaction.
//
// It refuses any list that is not exactly the offer's current photo set —
// missing ids, foreign ids or a repeat. A partial reorder would leave the
// omitted photos on their old positions, so two photos would claim the same
// slot and which one becomes position 0 — the primary image every marketplace
// shows first — would be decided by a tiebreak nobody chose.
func (r *Repo) ReorderPhotos(ctx context.Context, offerID string, photoIDsInOrder []string) error {
	tx, err := r.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("offer: reorder photos: %w", err)
	}
	// Rollback after a successful Commit returns sql.ErrTxDone, which is the
	// normal path and not a failure worth reporting.
	defer func() { _ = tx.Rollback() }()

	current, err := currentPhotoIDs(ctx, tx, offerID)
	if err != nil {
		return err
	}
	if err := checkFullSet(offerID, current, photoIDsInOrder); err != nil {
		return err
	}

	for pos, id := range photoIDsInOrder {
		if _, err := tx.ExecContext(ctx,
			`UPDATE offer_photos SET position = ? WHERE id = ?`, pos, id); err != nil {
			return fmt.Errorf("offer: reorder photos: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("offer: reorder photos: commit: %w", err)
	}
	return nil
}

// currentPhotoIDs reads the offer's photo ids inside tx. The rows are drained
// and closed before the caller issues its updates, because a transaction holds
// one connection and an open cursor would block them.
func currentPhotoIDs(ctx context.Context, tx *sql.Tx, offerID string) (map[string]bool, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM offer_photos WHERE offer_id = ?`, offerID)
	if err != nil {
		return nil, fmt.Errorf("offer: reorder photos: %w", err)
	}
	defer rows.Close()

	current := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("offer: reorder photos: %w", err)
		}
		current[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("offer: reorder photos: %w", err)
	}
	return current, rows.Close()
}

// checkFullSet reports whether want is a permutation of have, naming precisely
// what is wrong when it is not.
func checkFullSet(offerID string, have map[string]bool, want []string) error {
	if len(want) != len(have) {
		return fmt.Errorf("%w: offer %s has %d photo(s) but %d were given to reorder",
			core.ErrInvalid, offerID, len(have), len(want))
	}
	seen := make(map[string]bool, len(want))
	for _, id := range want {
		if !have[id] {
			return fmt.Errorf("%w: photo %s does not belong to offer %s",
				core.ErrInvalid, id, offerID)
		}
		if seen[id] {
			return fmt.Errorf("%w: photo %s was listed twice", core.ErrInvalid, id)
		}
		seen[id] = true
	}
	return nil
}

// photosByOffer loads the photos of every listed offer in ONE query and groups
// them in Go, keyed by offer id and ordered by position.
//
// The IN list carries one placeholder per offer on the page, so the caller's
// page size is what keeps it inside SQLite's variable limit.
func (r *Repo) photosByOffer(ctx context.Context, offerIDs []string) (map[string][]core.Photo, error) {
	out := make(map[string][]core.Photo, len(offerIDs))
	if len(offerIDs) == 0 {
		return out, nil
	}

	ph := make([]string, len(offerIDs))
	args := make([]any, len(offerIDs))
	for i, id := range offerIDs {
		ph[i] = "?"
		args[i] = id
	}
	q := `SELECT ` + photoColumns + ` FROM offer_photos WHERE offer_id IN (` +
		strings.Join(ph, ", ") + `) ORDER BY offer_id, position, id`

	rows, err := r.read.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("offer: load photos: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			p       core.Photo
			created string
		)
		if err := rows.Scan(&p.ID, &p.OfferID, &p.Position, &p.Filename, &p.ContentType,
			&p.ByteSize, &p.SHA256, &created); err != nil {
			return nil, fmt.Errorf("offer: load photos: %w", err)
		}
		if p.CreatedAt, err = parseTime(created); err != nil {
			return nil, err
		}
		out[p.OfferID] = append(out[p.OfferID], p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("offer: load photos: %w", err)
	}
	return out, nil
}

// rowScanner is the one method *sql.Row and *sql.Rows have in common, so that a
// single-row read and a page read share one scan.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanOffer reads one offer row. Photos are not its business; the caller
// populates them.
func scanOffer(sc rowScanner) (core.Offer, error) {
	var (
		o          core.Offer
		status     string
		soldAt     sql.NullString
		locationID sql.NullString
		created    string
		updated    string
	)
	err := sc.Scan(&o.ID, &o.SKU, &o.Title, &o.Description, &o.Condition, &status,
		&o.Quantity, &o.Shop.Minor, &o.Shop.Currency, &o.Owner.Minor, &o.Owner.Currency,
		&o.Sold.Minor, &o.Sold.Currency, &soldAt, &locationID, &created, &updated)
	if err != nil {
		return core.Offer{}, err
	}
	o.Status = core.Status(status)
	o.LocationID = locationID.String
	if o.CreatedAt, err = parseTime(created); err != nil {
		return core.Offer{}, err
	}
	if o.UpdatedAt, err = parseTime(updated); err != nil {
		return core.Offer{}, err
	}
	if soldAt.Valid {
		t, err := parseTime(soldAt.String)
		if err != nil {
			return core.Offer{}, err
		}
		o.SoldAt = &t
	}
	return o, nil
}

// likeEscape makes s literal inside a LIKE pattern that declares ESCAPE '\'.
//
// "%", "_" and "\" are the characters LIKE reads as syntax. Leaving them alone
// turns a value into a pattern: a location prefix of "A_B" would match "AXB"
// too, and nothing would report the wrong rows as wrong.
func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// formatTime renders t for storage: RFC3339, in UTC, to the second.
func formatTime(t time.Time) string { return t.UTC().Format(timeLayout) }

// parseTime reads a stored timestamp back as UTC.
func parseTime(s string) (time.Time, error) {
	t, err := time.Parse(timeLayout, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("offer: parse timestamp %q: %w", s, err)
	}
	return t.UTC(), nil
}

// nullString maps Go's empty string to SQL NULL.
//
// location_id references locations, so an empty string would be a foreign key
// that resolves to nothing rather than the "not shelved yet" the domain means.
func nullString(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// nullTime maps a nil sale date to SQL NULL, which is how "not sold" is stored.
func nullTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return formatTime(*t)
}

// checkAffected turns a statement that matched no row into core.ErrNotFound, so
// that a caller cannot mistake "there was nothing to change" for success.
func checkAffected(res sql.Result, op, id string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("offer: %s %s: %w", op, id, err)
	}
	if n == 0 {
		return fmt.Errorf("offer: %s %s: %w", op, id, core.ErrNotFound)
	}
	return nil
}

// writeErr turns a driver constraint failure into a sentinel the caller can
// branch on.
//
// It matches the SQLite result code through a small interface rather than
// importing the concrete driver, so this package still speaks only
// database/sql. offers.sku is the table's only UNIQUE index apart from the
// primary key, so a unique violation on an offer write is always the SKU.
func writeErr(op, sku string, err error) error {
	var coded interface{ Code() int }
	if errors.As(err, &coded) && coded.Code() == sqliteConstraintUnique {
		return fmt.Errorf("offer: %s %q: %w", op, sku, ErrSKUTaken)
	}
	return fmt.Errorf("offer: %s: %w", op, err)
}
