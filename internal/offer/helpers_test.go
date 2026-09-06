package offer

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	// modernc.org/sqlite is the CGO-free driver; it registers itself as "sqlite".
	_ "modernc.org/sqlite"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
)

// schemaStatements is the part of migrations/00001_init.sql this package needs,
// copied verbatim so the suite depends on no sibling package. Keep it in step
// with that migration: a drift here makes the tests pass against a schema
// production does not have.
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
	`CREATE INDEX locations_parent_idx ON locations (parent_id)`,
	`CREATE INDEX locations_path_idx ON locations (path)`,
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
	`CREATE INDEX offers_location_idx ON offers (location_id)`,
	`CREATE INDEX offers_sold_at_idx ON offers (sold_at)`,
	`CREATE INDEX offers_needs_pricing_idx ON offers (status, shop_minor)`,
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
}

// newTestRepo opens a fresh SQLite database in the test's temp directory,
// creates the schema, and returns a Repo over it.
//
// It mirrors the application's own handle split: a single writer with
// _txlock=immediate, and a reader carrying query_only(1). The reader pragma is
// not decoration — it makes the suite prove that no read path writes, because
// the driver refuses rather than merely being asked not to.
func newTestRepo(t *testing.T) *Repo {
	t.Helper()

	path := filepath.Join(t.TempDir(), "offer_test.db")
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

// newOffer returns a minimal valid draft, ready for a test to adjust.
func newOffer(id, sku, title string, createdAt time.Time) core.Offer {
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

// insertLocation writes a location row directly, because this package owns
// offers and not the location tree.
func insertLocation(t *testing.T, r *Repo, id, code, path string) {
	t.Helper()
	_, err := r.write.Exec(
		`INSERT INTO locations (id, parent_id, kind, code, path, created_at)
		 VALUES (?, NULL, 'site', ?, ?, ?)`,
		id, code, path, formatTime(baseTime))
	if err != nil {
		t.Fatalf("insert location %s: %v", path, err)
	}
}

// fakeBlobs is an in-memory core.BlobStore: a map behind a mutex, so the suite
// runs under -race without touching a filesystem or the sibling blob package.
type fakeBlobs struct {
	mu     sync.Mutex
	data   map[string][]byte
	putErr error
	delErr error
}

// newFakeBlobs returns an empty blob store.
func newFakeBlobs() *fakeBlobs {
	return &fakeBlobs{data: map[string][]byte{}}
}

// Put stores the bytes, or fails with the configured putErr.
func (f *fakeBlobs) Put(_ context.Context, id string, data []byte) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.putErr != nil {
		return f.putErr
	}
	cp := make([]byte, len(data))
	copy(cp, data)
	f.data[id] = cp
	return nil
}

// Open returns the stored bytes.
func (f *fakeBlobs) Open(_ context.Context, id string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	b, ok := f.data[id]
	if !ok {
		return nil, errors.New("fakeBlobs: not found")
	}
	return b, nil
}

// Delete removes the bytes; a missing id is a no-op, as the port requires.
func (f *fakeBlobs) Delete(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.delErr != nil {
		return f.delErr
	}
	delete(f.data, id)
	return nil
}

// has reports whether the store holds bytes for id.
func (f *fakeBlobs) has(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.data[id]
	return ok
}

// count returns how many blobs are stored.
func (f *fakeBlobs) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.data)
}

// fakeBlobs must satisfy the port it stands in for.
var _ core.BlobStore = (*fakeBlobs)(nil)

// countRows is a direct row count through the write handle, used to assert that
// a failed write left nothing behind.
func countRows(t *testing.T, r *Repo, table string) int {
	t.Helper()
	var n int
	// table is a literal from the test, never operator input.
	if err := r.write.QueryRow(`SELECT COUNT(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// ids maps offers to their ids, for readable ordering assertions.
func ids(offers []core.Offer) []string {
	out := make([]string, len(offers))
	for i, o := range offers {
		out[i] = o.ID
	}
	return out
}
