// Package cart holds the cart aggregate's persistence and its write side.
//
// A cart is a NAMED, SAVED selection of offers — "eBay batch, September" —
// assembled over days and then exported to a marketplace as one file. It is
// deliberately not a saved filter, and that difference is the reason the
// aggregate exists: a filter answers "everything that currently matches" and its
// membership changes underneath you as stock moves, while a cart answers "the
// things I chose", which does not.
//
// A cart stores REFERENCES and never copies — no title, no price, no photo list.
// An item repriced after being added therefore exports at its new price, which
// is what the operator means. Copying the price in would let a saved batch
// publish a figure nobody chose to publish, and the divergence would be silent.
//
// The package follows the project's CQRS split. [Repo] is the SQLite adapter
// that implements core.CartStore; [Service] is the only thing that mutates a
// cart. Reads go through the reader handle, which carries query_only(1), so a
// write on a read path is refused by the driver and not merely by convention.
package cart

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
)

// ErrNotCartMembership reports a reorder whose offer list is not exactly the
// cart's current membership: an id missing, an id belonging to some other cart,
// or one listed twice.
//
// It wraps core.ErrInvalid because the usual cause is a stale page — the
// operator dragged rows on a view rendered before somebody else removed an item
// — which is a form-level failure the caller can re-render, not a server fault.
var ErrNotCartMembership = fmt.Errorf(
	"%w: the offers given are not exactly the cart's membership", core.ErrInvalid)

// timeLayout is the on-disk timestamp format. Every stored time is RFC3339 in
// UTC, so the text sorts in the same order as the instants it names — which is
// what lets "most recently updated first" be an ORDER BY on a TEXT column.
const timeLayout = time.RFC3339

// cartColumns is the select list for a cart row, aliased to c so it can be
// reused by the listing that joins cart_items.
const cartColumns = `c.id, c.name, c.note, c.created_at, c.updated_at`

// offerColumns is the select list for an offer row, aliased to o.
//
// It restates internal/offer's list rather than importing it: resolving a cart's
// offers is this package's own read, and reaching into the sibling aggregate's
// write side to borrow a constant would couple the two in the one direction the
// CQRS split exists to prevent. Keep it in step with the offers table.
const offerColumns = `o.id, o.sku, o.title, o.description, o.condition, o.status,
	o.quantity, o.shop_minor, o.shop_currency, o.owner_minor, o.owner_currency,
	o.sold_minor, o.sold_currency, o.sold_at, o.location_id, o.created_at, o.updated_at`

// photoColumns is the select list for a photo row.
const photoColumns = `id, offer_id, position, filename, content_type, byte_size, sha256, created_at`

// Repo is the SQLite-backed cart store.
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
var _ core.CartStore = (*Repo)(nil)

// NewRepo returns a Repo reading through read and writing through write.
//
// The two handles are expected to address the same database file with different
// pragmas, as internal/store.Open produces them.
func NewRepo(read, write *sql.DB) *Repo {
	return &Repo{read: read, write: write}
}

// Cart returns the cart with this id and its items in order, or
// core.ErrNotFound.
func (r *Repo) Cart(ctx context.Context, id string) (core.Cart, error) {
	const q = `SELECT ` + cartColumns + ` FROM carts c WHERE c.id = ?`

	c, err := scanCart(r.read.QueryRowContext(ctx, q, id))
	if errors.Is(err, sql.ErrNoRows) {
		return core.Cart{}, fmt.Errorf("cart %s: %w", id, core.ErrNotFound)
	}
	if err != nil {
		return core.Cart{}, fmt.Errorf("cart %s: %w", id, err)
	}

	items, err := r.itemsByCart(ctx, []string{c.ID})
	if err != nil {
		return core.Cart{}, err
	}
	c.Items = items[c.ID]
	return c, nil
}

// Carts lists every cart, most recently updated first, each with its items.
func (r *Repo) Carts(ctx context.Context) ([]core.Cart, error) {
	const q = `SELECT ` + cartColumns + ` FROM carts c
		ORDER BY c.updated_at DESC, c.id DESC`
	return r.cartsBy(ctx, "list", q)
}

// CartsHolding returns the carts an offer is already in, most recently updated
// first, so the offer page can say so rather than letting the operator add the
// same item to a second batch by accident.
func (r *Repo) CartsHolding(ctx context.Context, offerID string) ([]core.Cart, error) {
	const q = `SELECT ` + cartColumns + ` FROM carts c
		JOIN cart_items ci ON ci.cart_id = c.id
		WHERE ci.offer_id = ?
		ORDER BY c.updated_at DESC, c.id DESC`
	return r.cartsBy(ctx, "carts holding", q, offerID)
}

// cartsBy runs a cart listing and populates every returned cart's items in ONE
// further query, grouped in Go.
//
// The items are loaded rather than left empty because core.Cart.Size reads
// len(Items): a listing that skipped them would report every saved batch as
// holding nothing, which is a wrong answer rather than a missing one.
func (r *Repo) cartsBy(ctx context.Context, op, q string, args ...any) ([]core.Cart, error) {
	rows, err := r.read.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("cart: %s: %w", op, err)
	}
	defer rows.Close()

	var (
		out []core.Cart
		ids []string
	)
	for rows.Next() {
		c, err := scanCart(rows)
		if err != nil {
			return nil, fmt.Errorf("cart: %s: %w", op, err)
		}
		out = append(out, c)
		ids = append(ids, c.ID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("cart: %s: %w", op, err)
	}

	byCart, err := r.itemsByCart(ctx, ids)
	if err != nil {
		return nil, err
	}
	for i := range out {
		out[i].Items = byCart[out[i].ID]
	}
	return out, nil
}

// Contents returns the cart with its offers resolved in CART ORDER, each offer
// fully populated including its photos in position order — everything an export
// or a cart page needs, in one call.
//
// ⚠ The photos are loaded for the whole cart in ONE query and grouped in Go,
// never one query per offer, and the second reason matters more than the first.
// An N+1 is invisible on the three rows a developer tests with and becomes
// hundreds of round trips on a real batch; and the exporter reads offer.Photos,
// so an offer arriving with nil photos would silently export as an item with no
// images — which a marketplace accepts, and nobody notices until the listing is
// live.
func (r *Repo) Contents(ctx context.Context, cartID string) (core.CartContents, error) {
	c, err := r.Cart(ctx, cartID)
	if err != nil {
		return core.CartContents{}, err
	}

	// Ordering is the cart's, taken from cart_items.position. The offers' own
	// created_at is a different order entirely and would silently rearrange a
	// batch the operator arranged by hand; o.id only breaks a tie between two
	// rows that somehow share a position.
	const q = `SELECT ` + offerColumns + ` FROM cart_items ci
		JOIN offers o ON o.id = ci.offer_id
		WHERE ci.cart_id = ?
		ORDER BY ci.position, o.id`

	rows, err := r.read.QueryContext(ctx, q, cartID)
	if err != nil {
		return core.CartContents{}, fmt.Errorf("cart: contents %s: %w", cartID, err)
	}
	defer rows.Close()

	var (
		offers []core.Offer
		ids    []string
	)
	for rows.Next() {
		o, err := scanOffer(rows)
		if err != nil {
			return core.CartContents{}, fmt.Errorf("cart: contents %s: %w", cartID, err)
		}
		offers = append(offers, o)
		ids = append(ids, o.ID)
	}
	if err := rows.Err(); err != nil {
		return core.CartContents{}, fmt.Errorf("cart: contents %s: %w", cartID, err)
	}

	byOffer, err := r.photosByOffer(ctx, ids)
	if err != nil {
		return core.CartContents{}, err
	}
	for i := range offers {
		offers[i].Photos = byOffer[offers[i].ID]
	}
	return core.CartContents{Cart: c, Offers: offers}, nil
}

// CreateCart inserts a new saved batch.
func (r *Repo) CreateCart(ctx context.Context, c core.Cart) error {
	const q = `INSERT INTO carts (id, name, note, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?)`
	if _, err := r.write.ExecContext(ctx, q, c.ID, c.Name, c.Note,
		formatTime(c.CreatedAt), formatTime(c.UpdatedAt)); err != nil {
		return fmt.Errorf("cart: create cart %s: %w", c.ID, err)
	}
	return nil
}

// UpdateCart rewrites a cart's name, note and updated_at, or returns
// core.ErrNotFound.
//
// created_at is deliberately not in the SET list — when a batch was started is
// not something a rename may change — and neither are the items. Membership
// moves only through [Repo.AddToCart], [Repo.RemoveFromCart] and
// [Repo.ReorderCart], so a caller saving a form-shaped struct cannot empty a
// batch by accident.
func (r *Repo) UpdateCart(ctx context.Context, c core.Cart) error {
	const q = `UPDATE carts SET name = ?, note = ?, updated_at = ? WHERE id = ?`
	res, err := r.write.ExecContext(ctx, q, c.Name, c.Note, formatTime(c.UpdatedAt), c.ID)
	if err != nil {
		return fmt.Errorf("cart: update cart %s: %w", c.ID, err)
	}
	return checkAffected(res, "update cart", c.ID)
}

// DeleteCart removes a saved batch, or returns core.ErrNotFound.
//
// Its membership rows go with it through the schema's ON DELETE CASCADE. The
// offers do not: a cart is a selection, and discarding the selection is not a
// decision about stock.
func (r *Repo) DeleteCart(ctx context.Context, id string) error {
	res, err := r.write.ExecContext(ctx, `DELETE FROM carts WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("cart: delete cart %s: %w", id, err)
	}
	return checkAffected(res, "delete cart", id)
}

// AddToCart appends an offer to the end of a cart and bumps the cart's
// updated_at, in one transaction.
//
// Adding an offer the cart already holds is a NO-OP rather than an error: the
// button that calls this sits on a page that may be seconds stale, so a second
// click is noise about nothing. ON CONFLICT DO NOTHING makes that the database's
// decision, which a read-then-write pair could not promise against two requests
// arriving together.
//
// The no-op leaves updated_at alone too. That column answers "which batch was I
// working on", and a duplicate click changed nothing — promoting the cart to the
// top of the list would be a claim nobody made.
//
// The new position is the highest in use plus one rather than the row count,
// because a removal leaves a gap and renumbering to close it would move items
// the operator did not touch.
func (r *Repo) AddToCart(ctx context.Context, cartID, offerID string) error {
	const q = `INSERT INTO cart_items (cart_id, offer_id, position, added_at)
		VALUES (?, ?,
			(SELECT COALESCE(MAX(position), -1) + 1 FROM cart_items WHERE cart_id = ?),
			?)
		ON CONFLICT (cart_id, offer_id) DO NOTHING`

	tx, err := r.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("cart: add offer %s to cart %s: %w", offerID, cartID, err)
	}
	// Rollback after a successful Commit returns sql.ErrTxDone, which is the
	// normal path and not a failure worth reporting.
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx, q, cartID, offerID, cartID, formatTime(now()))
	if err != nil {
		return fmt.Errorf("cart: add offer %s to cart %s: %w", offerID, cartID, err)
	}
	added, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("cart: add offer %s to cart %s: %w", offerID, cartID, err)
	}
	if added > 0 {
		if err := touch(ctx, tx, cartID); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("cart: add offer %s to cart %s: commit: %w", offerID, cartID, err)
	}
	return nil
}

// RemoveFromCart takes an offer out of a cart and bumps the cart's updated_at,
// in one transaction.
//
// Removing an offer that is not there is a no-op for the same reason adding a
// duplicate is, and as with the add a no-op leaves updated_at where it was.
//
// The remaining items keep their positions, gap included. Closing the gap would
// renumber rows nobody touched, and a reorder submitted from a page rendered
// before the removal would then be applied against positions that had already
// moved.
func (r *Repo) RemoveFromCart(ctx context.Context, cartID, offerID string) error {
	tx, err := r.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("cart: remove offer %s from cart %s: %w", offerID, cartID, err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.ExecContext(ctx,
		`DELETE FROM cart_items WHERE cart_id = ? AND offer_id = ?`, cartID, offerID)
	if err != nil {
		return fmt.Errorf("cart: remove offer %s from cart %s: %w", offerID, cartID, err)
	}
	removed, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("cart: remove offer %s from cart %s: %w", offerID, cartID, err)
	}
	if removed > 0 {
		if err := touch(ctx, tx, cartID); err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("cart: remove offer %s from cart %s: commit: %w", offerID, cartID, err)
	}
	return nil
}

// ReorderCart rewrites the cart's item positions to the given order, in ONE
// transaction.
//
// It refuses any list that is not exactly the cart's current membership —
// missing ids, foreign ids, or a repeat — with [ErrNotCartMembership]. A partial
// reorder would leave the omitted items on their old positions, so two items
// would claim the same slot and their order in the exported file would be
// settled by a tiebreak nobody chose. Every member of a cart was picked by hand;
// losing one's place loses information the operator supplied.
func (r *Repo) ReorderCart(ctx context.Context, cartID string, offerIDsInOrder []string) error {
	tx, err := r.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("cart: reorder cart %s: %w", cartID, err)
	}
	defer func() { _ = tx.Rollback() }()

	current, err := currentOfferIDs(ctx, tx, cartID)
	if err != nil {
		return err
	}
	if err := checkMembership(cartID, current, offerIDsInOrder); err != nil {
		return err
	}

	for pos, id := range offerIDsInOrder {
		if _, err := tx.ExecContext(ctx,
			`UPDATE cart_items SET position = ? WHERE cart_id = ? AND offer_id = ?`,
			pos, cartID, id); err != nil {
			return fmt.Errorf("cart: reorder cart %s: %w", cartID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("cart: reorder cart %s: commit: %w", cartID, err)
	}
	return nil
}

// touch moves a cart's updated_at to now inside the caller's transaction, so
// that a membership change and the timestamp reporting it either both land or
// neither does. A cart that is not there is core.ErrNotFound.
func touch(ctx context.Context, tx *sql.Tx, cartID string) error {
	res, err := tx.ExecContext(ctx,
		`UPDATE carts SET updated_at = ? WHERE id = ?`, formatTime(now()), cartID)
	if err != nil {
		return fmt.Errorf("cart: touch cart %s: %w", cartID, err)
	}
	return checkAffected(res, "touch cart", cartID)
}

// currentOfferIDs reads a cart's membership inside tx. The rows are drained and
// closed before the caller issues its updates, because a transaction holds one
// connection and an open cursor would block them.
func currentOfferIDs(ctx context.Context, tx *sql.Tx, cartID string) (map[string]bool, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT offer_id FROM cart_items WHERE cart_id = ?`, cartID)
	if err != nil {
		return nil, fmt.Errorf("cart: reorder cart %s: %w", cartID, err)
	}
	defer rows.Close()

	current := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("cart: reorder cart %s: %w", cartID, err)
		}
		current[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("cart: reorder cart %s: %w", cartID, err)
	}
	return current, rows.Close()
}

// checkMembership reports whether want is a permutation of have, naming exactly
// what is wrong when it is not.
func checkMembership(cartID string, have map[string]bool, want []string) error {
	if len(want) != len(have) {
		return fmt.Errorf("%w: cart %s holds %d offer(s) but %d were given to reorder",
			ErrNotCartMembership, cartID, len(have), len(want))
	}
	seen := make(map[string]bool, len(want))
	for _, id := range want {
		if !have[id] {
			return fmt.Errorf("%w: offer %s is not in cart %s",
				ErrNotCartMembership, id, cartID)
		}
		if seen[id] {
			return fmt.Errorf("%w: offer %s was listed twice", ErrNotCartMembership, id)
		}
		seen[id] = true
	}
	return nil
}

// itemsByCart loads the membership of every listed cart in ONE query and groups
// it in Go, keyed by cart id and ordered by position.
func (r *Repo) itemsByCart(ctx context.Context, cartIDs []string) (map[string][]core.CartItem, error) {
	out := make(map[string][]core.CartItem, len(cartIDs))
	if len(cartIDs) == 0 {
		return out, nil
	}

	ph, args := placeholders(cartIDs)
	q := `SELECT cart_id, offer_id, position, added_at FROM cart_items
		WHERE cart_id IN (` + ph + `) ORDER BY cart_id, position, offer_id`

	rows, err := r.read.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("cart: load items: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			it    core.CartItem
			added string
		)
		if err := rows.Scan(&it.CartID, &it.OfferID, &it.Position, &added); err != nil {
			return nil, fmt.Errorf("cart: load items: %w", err)
		}
		if it.AddedAt, err = parseTime(added); err != nil {
			return nil, err
		}
		out[it.CartID] = append(out[it.CartID], it)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("cart: load items: %w", err)
	}
	return out, nil
}

// photosByOffer loads the photos of every listed offer in ONE query and groups
// them in Go, keyed by offer id and ordered by position.
//
// The IN list carries one placeholder per offer in the cart, so the size of a
// batch is what keeps it inside SQLite's variable limit.
func (r *Repo) photosByOffer(ctx context.Context, offerIDs []string) (map[string][]core.Photo, error) {
	out := make(map[string][]core.Photo, len(offerIDs))
	if len(offerIDs) == 0 {
		return out, nil
	}

	ph, args := placeholders(offerIDs)
	q := `SELECT ` + photoColumns + ` FROM offer_photos WHERE offer_id IN (` + ph +
		`) ORDER BY offer_id, position, id`

	rows, err := r.read.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("cart: load photos: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			p       core.Photo
			created string
		)
		if err := rows.Scan(&p.ID, &p.OfferID, &p.Position, &p.Filename, &p.ContentType,
			&p.ByteSize, &p.SHA256, &created); err != nil {
			return nil, fmt.Errorf("cart: load photos: %w", err)
		}
		if p.CreatedAt, err = parseTime(created); err != nil {
			return nil, err
		}
		out[p.OfferID] = append(out[p.OfferID], p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("cart: load photos: %w", err)
	}
	return out, nil
}

// placeholders builds an IN list of "?" and the arguments filling it, so a batch
// load is one query with one placeholder per id rather than one query per id.
func placeholders(ids []string) (string, []any) {
	ph := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		ph[i] = "?"
		args[i] = id
	}
	return strings.Join(ph, ", "), args
}

// rowScanner is the one method *sql.Row and *sql.Rows have in common, so that a
// single-row read and a page read share one scan.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanCart reads one cart row. Items are not its business; the caller populates
// them.
func scanCart(sc rowScanner) (core.Cart, error) {
	var (
		c       core.Cart
		created string
		updated string
	)
	if err := sc.Scan(&c.ID, &c.Name, &c.Note, &created, &updated); err != nil {
		return core.Cart{}, err
	}
	var err error
	if c.CreatedAt, err = parseTime(created); err != nil {
		return core.Cart{}, err
	}
	if c.UpdatedAt, err = parseTime(updated); err != nil {
		return core.Cart{}, err
	}
	return c, nil
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

// checkAffected turns a statement that matched no row into core.ErrNotFound, so
// that a caller cannot mistake "there was nothing to change" for success.
func checkAffected(res sql.Result, op, id string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("cart: %s %s: %w", op, id, err)
	}
	if n == 0 {
		return fmt.Errorf("cart: %s %s: %w", op, id, core.ErrNotFound)
	}
	return nil
}

// formatTime renders t for storage: RFC3339, in UTC, to the second.
func formatTime(t time.Time) string { return t.UTC().Format(timeLayout) }

// parseTime reads a stored timestamp back as UTC.
func parseTime(s string) (time.Time, error) {
	t, err := time.Parse(timeLayout, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("cart: parse timestamp %q: %w", s, err)
	}
	return t.UTC(), nil
}

// now is this package's clock, truncated to the second because that is the
// resolution the RFC3339 columns store. Without the truncation a struct handed
// back to a caller would not equal the one the next read returns.
func now() time.Time { return time.Now().UTC().Truncate(time.Second) }
