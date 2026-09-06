package offer

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
	"github.com/atvirokodosprendimai/app-warehousev1/internal/store"
	"github.com/atvirokodosprendimai/app-warehousev1/migrations"
)

// newTestRepo opens a fresh database, runs the REAL migrations over it, and
// returns a Repo.
//
// ⚠ It deliberately uses the embedded migrations rather than a copy of the
// CREATE TABLE statements. This file used to carry such a copy, with a comment
// asking whoever changed the schema to keep it in step — and it drifted at the
// first opportunity: adding the full-text index left the suite green against a
// schema production does not have, because the copy knew nothing about it. A
// fixture that restates the schema is a second source of truth, and the test
// that reads it cannot tell when it has stopped being true.
//
// store.Open also gives the tests the application's real handle split: a single
// writer with _txlock=immediate and a reader carrying query_only(1). The reader
// pragma is not decoration — it makes the suite prove that no read path writes,
// because the driver refuses rather than merely being asked not to.
func newTestRepo(t *testing.T) *Repo {
	t.Helper()

	db, err := store.Open(filepath.Join(t.TempDir(), "offer_test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := store.Migrate(db.Write, migrations.FS); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewRepo(db.Read, db.Write)
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
