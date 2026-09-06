package cart

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	// modernc.org/sqlite is the CGO-free driver; it registers itself as "sqlite".
	_ "modernc.org/sqlite"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
)

// schemaStatements is the part of migrations/00001_init.sql and
// migrations/00002_carts.sql this package needs, copied verbatim so the suite
// depends on no sibling package. Keep it in step with those migrations: a drift
// here makes the tests pass against a schema production does not have.
//
// locations is included even though no test shelves anything, because offers
// carries a foreign key onto it and SQLite reports a missing parent table as an
// error on the child's very first insert.
var schemaStatements = []string{
	`CREATE TABLE locations (
	    id                TEXT PRIMARY KEY,
	    parent_id         TEXT REFERENCES locations (id) ON DELETE RESTRICT,
	    kind              TEXT NOT NULL CHECK (kind IN
	                          ('site','building','room','aisle','shelf','segment','bin')),
	    code              TEXT NOT NULL,
	    path              TEXT NOT NULL UNIQUE,
	    label             TEXT NOT NULL DEFAULT '',
	    custodian         TEXT NOT NULL DEFAULT '',
	    custodian_contact TEXT NOT NULL DEFAULT '',
	    city              TEXT NOT NULL DEFAULT '',
	    country           TEXT NOT NULL DEFAULT '',
	    notes             TEXT NOT NULL DEFAULT '',
	    created_at        TEXT NOT NULL,
	    UNIQUE (parent_id, code)
	) STRICT`,
	`CREATE TABLE offers (
	    id             TEXT PRIMARY KEY,
	    sku            TEXT NOT NULL UNIQUE,
	    title          TEXT NOT NULL,
	    description    TEXT NOT NULL DEFAULT '',
	    condition      TEXT NOT NULL DEFAULT '',
	    status         TEXT NOT NULL DEFAULT 'draft' CHECK (status IN
	                       ('draft','listed','pending','sold','archived')),
	    quantity       INTEGER NOT NULL DEFAULT 1 CHECK (quantity >= 0),
	    shop_minor     INTEGER NOT NULL DEFAULT 0,
	    shop_currency  TEXT NOT NULL DEFAULT 'EUR',
	    owner_minor    INTEGER NOT NULL DEFAULT 0,
	    owner_currency TEXT NOT NULL DEFAULT 'EUR',
	    sold_minor     INTEGER NOT NULL DEFAULT 0,
	    sold_currency  TEXT NOT NULL DEFAULT '',
	    sold_at        TEXT,
	    location_id    TEXT REFERENCES locations (id) ON DELETE RESTRICT,
	    created_at     TEXT NOT NULL,
	    updated_at     TEXT NOT NULL,
	    CHECK (status <> 'sold' OR sold_at IS NOT NULL),
	    CHECK (status NOT IN ('listed','pending') OR shop_minor > 0)
	) STRICT`,
	`CREATE INDEX offers_status_idx ON offers (status)`,
	`CREATE TABLE offer_photos (
	    id           TEXT PRIMARY KEY,
	    offer_id     TEXT NOT NULL REFERENCES offers (id) ON DELETE CASCADE,
	    position     INTEGER NOT NULL DEFAULT 0,
	    filename     TEXT NOT NULL DEFAULT '',
	    content_type TEXT NOT NULL,
	    byte_size    INTEGER NOT NULL DEFAULT 0,
	    sha256       TEXT NOT NULL DEFAULT '',
	    created_at   TEXT NOT NULL
	) STRICT`,
	`CREATE INDEX offer_photos_offer_idx ON offer_photos (offer_id, position)`,
	`CREATE TABLE carts (
	    id         TEXT PRIMARY KEY,
	    name       TEXT NOT NULL,
	    note       TEXT NOT NULL DEFAULT '',
	    created_at TEXT NOT NULL,
	    updated_at TEXT NOT NULL
	) STRICT`,
	`CREATE TABLE cart_items (
	    cart_id  TEXT NOT NULL REFERENCES carts (id) ON DELETE CASCADE,
	    offer_id TEXT NOT NULL REFERENCES offers (id) ON DELETE CASCADE,
	    position INTEGER NOT NULL DEFAULT 0,
	    added_at TEXT NOT NULL,
	    PRIMARY KEY (cart_id, offer_id)
	) STRICT`,
	`CREATE INDEX cart_items_order_idx ON cart_items (cart_id, position)`,
	`CREATE INDEX cart_items_offer_idx ON cart_items (offer_id)`,
}

// newTestRepo opens a fresh SQLite database in the test's temp directory,
// creates the schema, and returns a Repo over it.
//
// It mirrors the application's own handle split: a single writer with
// _txlock=immediate, and a reader carrying query_only(1). Neither pragma is
// decoration. foreign_keys(1) is what makes the cascade assertions mean
// something, and query_only(1) makes the suite prove that no read path writes,
// because the driver refuses rather than merely being asked not to.
func newTestRepo(t *testing.T) *Repo {
	t.Helper()

	path := filepath.Join(t.TempDir(), "cart_test.db")
	write, err := sql.Open("sqlite", "file:"+path+
		"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"+
		"&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)&_txlock=immediate")
	if err != nil {
		t.Fatalf("open writer: %v", err)
	}
	// SQLite admits one writer; a larger pool would only queue somewhere with
	// worse diagnostics.
	write.SetMaxOpenConns(1)
	if err := write.Ping(); err != nil {
		t.Fatalf("ping writer: %v", err)
	}
	for _, stmt := range schemaStatements {
		if _, err := write.Exec(stmt); err != nil {
			t.Fatalf("create schema: %v\n%s", err, stmt)
		}
	}

	read, err := sql.Open("sqlite", "file:"+path+
		"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"+
		"&_pragma=foreign_keys(1)&_pragma=query_only(1)")
	if err != nil {
		t.Fatalf("open reader: %v", err)
	}
	if err := read.Ping(); err != nil {
		t.Fatalf("ping reader: %v", err)
	}

	t.Cleanup(func() {
		_ = read.Close()
		_ = write.Close()
	})
	return NewRepo(read, write)
}

// baseTime is a fixed instant the tests build timestamps from, so that ordering
// assertions do not depend on how fast the machine is.
var baseTime = time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC)

// newDraft returns a minimal unpriced draft, the normal state of a fresh intake.
func newDraft(id, sku, title string, createdAt time.Time) core.Offer {
	return core.Offer{
		ID:        id,
		SKU:       sku,
		Title:     title,
		Status:    core.StatusDraft,
		Quantity:  1,
		CreatedAt: createdAt,
		UpdatedAt: createdAt,
	}
}

// newListed returns a listed offer priced at minor EUR cents. The schema refuses
// a listed offer with no price, so the two always travel together.
func newListed(id, sku, title string, minor int64, createdAt time.Time) core.Offer {
	o := newDraft(id, sku, title, createdAt)
	o.Status = core.StatusListed
	o.Shop = core.Money{Minor: minor, Currency: "EUR"}
	return o
}

// insertOffer writes an offer row directly, because this package owns carts and
// not offers: the sibling write side is deliberately not imported.
func insertOffer(t *testing.T, r *Repo, o core.Offer) {
	t.Helper()
	const q = `INSERT INTO offers (id, sku, title, description, condition, status, quantity,
		shop_minor, shop_currency, owner_minor, owner_currency, sold_minor, sold_currency,
		sold_at, location_id, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, NULL, ?, ?)`
	_, err := r.write.Exec(q, o.ID, o.SKU, o.Title, o.Description, o.Condition,
		string(o.Status), o.Quantity, o.Shop.Minor, o.Shop.Currency,
		o.Owner.Minor, o.Owner.Currency, o.Sold.Minor, o.Sold.Currency,
		formatTime(o.CreatedAt), formatTime(o.UpdatedAt))
	if err != nil {
		t.Fatalf("insert offer %s: %v", o.SKU, err)
	}
}

// insertPhoto writes a photo row directly, for the same reason as insertOffer.
func insertPhoto(t *testing.T, r *Repo, id, offerID string, position int) {
	t.Helper()
	const q = `INSERT INTO offer_photos (` + photoColumns + `) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := r.write.Exec(q, id, offerID, position, id+".jpg", "image/jpeg",
		int64(1024), "", formatTime(baseTime))
	if err != nil {
		t.Fatalf("insert photo %s: %v", id, err)
	}
}

// makeCart creates a cart whose timestamps are baseTime plus offset, so that
// "most recently updated first" can be asserted without sleeping.
func makeCart(t *testing.T, r *Repo, id, name string, offset time.Duration) core.Cart {
	t.Helper()
	at := baseTime.Add(offset)
	c := core.Cart{ID: id, Name: name, CreatedAt: at, UpdatedAt: at}
	if err := r.CreateCart(context.Background(), c); err != nil {
		t.Fatalf("create cart %s: %v", id, err)
	}
	return c
}

// setUpdatedAt rewinds a cart's updated_at directly.
//
// The repository's clock has one-second resolution, so two writes in the same
// second are indistinguishable by timestamp. Rewinding to a known instant first
// is what lets a test assert that a bump did — or did not — happen.
func setUpdatedAt(t *testing.T, r *Repo, cartID string, at time.Time) {
	t.Helper()
	if _, err := r.write.Exec(`UPDATE carts SET updated_at = ? WHERE id = ?`,
		formatTime(at), cartID); err != nil {
		t.Fatalf("set updated_at on %s: %v", cartID, err)
	}
}

// updatedAt reads a cart's stored updated_at back.
func updatedAt(t *testing.T, r *Repo, cartID string) time.Time {
	t.Helper()
	var s string
	if err := r.write.QueryRow(`SELECT updated_at FROM carts WHERE id = ?`, cartID).
		Scan(&s); err != nil {
		t.Fatalf("read updated_at of %s: %v", cartID, err)
	}
	got, err := parseTime(s)
	if err != nil {
		t.Fatalf("parse updated_at of %s: %v", cartID, err)
	}
	return got
}

// positions reads a cart's membership as offer id to position, straight from the
// table, so an assertion about order does not depend on the code under test to
// report it.
func positions(t *testing.T, r *Repo, cartID string) map[string]int {
	t.Helper()
	rows, err := r.write.Query(
		`SELECT offer_id, position FROM cart_items WHERE cart_id = ?`, cartID)
	if err != nil {
		t.Fatalf("read positions of %s: %v", cartID, err)
	}
	defer rows.Close()

	out := map[string]int{}
	for rows.Next() {
		var (
			id  string
			pos int
		)
		if err := rows.Scan(&id, &pos); err != nil {
			t.Fatalf("scan position: %v", err)
		}
		out[id] = pos
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read positions of %s: %v", cartID, err)
	}
	return out
}

// countRows is a direct row count through the write handle, used to assert that
// a no-op left nothing behind and that a cascade took its rows with it.
func countRows(t *testing.T, r *Repo, table string) int {
	t.Helper()
	var n int
	// table is a literal from the test, never operator input.
	if err := r.write.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// offerIDs maps offers to their ids, for readable ordering assertions.
func offerIDs(offers []core.Offer) []string {
	out := make([]string, len(offers))
	for i, o := range offers {
		out[i] = o.ID
	}
	return out
}

// photoIDs maps photos to their ids, for readable ordering assertions.
func photoIDs(photos []core.Photo) []string {
	out := make([]string, len(photos))
	for i, p := range photos {
		out[i] = p.ID
	}
	return out
}

// cartIDs maps carts to their ids, for readable ordering assertions.
func cartIDs(carts []core.Cart) []string {
	out := make([]string, len(carts))
	for i, c := range carts {
		out[i] = c.ID
	}
	return out
}

// equalStrings reports whether two id slices match element for element.
func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// stubStore is a core.CartStore whose Contents is canned.
//
// It exists for one case the real schema cannot produce: core.HoldReason's "no
// shop price yet" needs an offer that is exportable AND unpriced, and the offers
// table's CHECK (status NOT IN ('listed','pending') OR shop_minor > 0) refuses
// exactly that row. The embedded interface is nil on purpose — any method a test
// has not arranged for panics loudly rather than returning a quiet zero value.
type stubStore struct {
	core.CartStore
	contents core.CartContents
}

// Contents returns the canned read model.
func (s stubStore) Contents(context.Context, string) (core.CartContents, error) {
	return s.contents, nil
}
