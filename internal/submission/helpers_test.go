package submission

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

// schemaStatements is the part of migrations/00001_init.sql and
// migrations/00003_submissions.sql this package needs, copied verbatim so the
// suite depends on no sibling package. Keep it in step with those migrations: a
// drift here makes the tests pass against a schema production does not have.
//
// locations is present because offers.location_id references it and a conversion
// must name a location, so an offer row written during an acceptance test would
// otherwise fail the foreign key. CHECK constraints are copied too — the decline
// reason and the accepted offer_id are enforced in three places on purpose, and a
// test schema without them would only prove two of them.
var schemaStatements = []string{
	`CREATE TABLE users (
	    id            TEXT PRIMARY KEY,
	    email         TEXT NOT NULL UNIQUE,
	    password_hash TEXT NOT NULL,
	    display_name  TEXT NOT NULL DEFAULT '',
	    is_admin      INTEGER NOT NULL DEFAULT 0 CHECK (is_admin IN (0, 1)),
	    created_at    TEXT NOT NULL,
	    disabled_at   TEXT
	) STRICT`,
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
	`CREATE TABLE submissions (
	    id              TEXT PRIMARY KEY,
	    submitted_by    TEXT NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
	    title           TEXT NOT NULL,
	    note            TEXT NOT NULL DEFAULT '',
	    asking_minor    INTEGER NOT NULL DEFAULT 0,
	    asking_currency TEXT NOT NULL DEFAULT 'EUR',
	    status          TEXT NOT NULL DEFAULT 'new' CHECK (status IN
	                        ('new','reviewing','accepted','declined')),
	    decline_reason  TEXT NOT NULL DEFAULT '',
	    offer_id        TEXT REFERENCES offers (id) ON DELETE SET NULL,
	    created_at      TEXT NOT NULL,
	    reviewed_at     TEXT,
	    reviewed_by     TEXT REFERENCES users (id) ON DELETE SET NULL,
	    CHECK (status <> 'declined' OR decline_reason <> ''),
	    CHECK (status <> 'accepted' OR offer_id IS NOT NULL)
	) STRICT`,
	`CREATE INDEX submissions_status_idx ON submissions (status, created_at)`,
	`CREATE INDEX submissions_by_idx ON submissions (submitted_by, created_at)`,
	`CREATE TABLE submission_photos (
	    id            TEXT PRIMARY KEY,
	    submission_id TEXT NOT NULL REFERENCES submissions (id) ON DELETE CASCADE,
	    position      INTEGER NOT NULL DEFAULT 0,
	    filename      TEXT NOT NULL DEFAULT '',
	    content_type  TEXT NOT NULL,
	    byte_size     INTEGER NOT NULL DEFAULT 0,
	    sha256        TEXT NOT NULL DEFAULT '',
	    created_at    TEXT NOT NULL
	) STRICT`,
	`CREATE INDEX submission_photos_idx ON submission_photos (submission_id, position)`,
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
	return newTestRepoReadingThrough(t, "sqlite")
}

// newTestRepoReadingThrough is [newTestRepo] with the reader's driver named, so
// that one test can read through a counting wrapper while every other reads
// through the plain driver.
func newTestRepoReadingThrough(t *testing.T, readDriver string) *Repo {
	t.Helper()

	path := filepath.Join(t.TempDir(), "submission_test.db")
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

	read, err := sql.Open(readDriver, "file:"+path+
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

// insertUser writes a user row directly, because this package owns submissions
// and not accounts. It returns the core.User a service call would be handed.
func insertUser(t *testing.T, r *Repo, id, email, displayName string, admin bool) core.User {
	t.Helper()
	isAdmin := 0
	if admin {
		isAdmin = 1
	}
	_, err := r.write.Exec(
		`INSERT INTO users (id, email, password_hash, display_name, is_admin, created_at)
		 VALUES (?, ?, 'x', ?, ?, ?)`,
		id, email, displayName, isAdmin, formatTime(baseTime))
	if err != nil {
		t.Fatalf("insert user %s: %v", id, err)
	}
	return core.User{ID: id, Email: email, DisplayName: displayName, IsAdmin: admin}
}

// insertLocation writes a location row directly, because this package owns
// submissions and not the location tree.
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

// insertOffer writes a bare draft offer row, for tests that need a valid target
// for a photo move without going through a conversion.
func insertOffer(t *testing.T, r *Repo, id, sku string) {
	t.Helper()
	_, err := r.write.Exec(
		`INSERT INTO offers (id, sku, title, status, quantity, created_at, updated_at)
		 VALUES (?, ?, 'target', 'draft', 1, ?, ?)`,
		id, sku, formatTime(baseTime), formatTime(baseTime))
	if err != nil {
		t.Fatalf("insert offer %s: %v", id, err)
	}
}

// newSubmission returns a minimal valid new submission, ready for a test to
// adjust.
func newSubmission(id, submittedBy, title string, createdAt time.Time) core.Submission {
	return core.Submission{
		ID:          id,
		SubmittedBy: submittedBy,
		Title:       title,
		Status:      core.SubmissionNew,
		CreatedAt:   createdAt,
	}
}

// insertPhoto writes a submission photo row directly, at a chosen id and
// position, so an ordering or id-preservation assertion names exact values.
func insertPhoto(t *testing.T, r *Repo, id, submissionID string, position int) {
	t.Helper()
	err := r.AddSubmissionPhoto(context.Background(), core.Photo{
		ID:          id,
		OfferID:     submissionID,
		Position:    position,
		Filename:    id + ".jpg",
		ContentType: "image/jpeg",
		ByteSize:    int64(100 + position),
		SHA256:      "hash-" + id,
		CreatedAt:   baseTime,
	})
	if err != nil {
		t.Fatalf("insert photo %s: %v", id, err)
	}
}

// fakeBlobs is an in-memory core.BlobStore: a map behind a mutex, so the suite
// runs under -race without touching a filesystem or the sibling blob package.
type fakeBlobs struct {
	mu     sync.Mutex
	data   map[string][]byte
	putErr error
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
	delete(f.data, id)
	return nil
}

// count returns how many blobs are stored.
func (f *fakeBlobs) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.data)
}

// fakeBlobs must satisfy the port it stands in for.
var _ core.BlobStore = (*fakeBlobs)(nil)

// fakeOffers stands in for the offer aggregate's write side.
//
// When repo is set it ALSO writes the offers row into that repo's database,
// because offer_photos.offer_id is a real foreign key: a photo move onto an
// offer that exists only in a map would be refused by the driver. Its read-back
// then returns the photographs as the database actually holds them, which is
// what the real offer.Repo does and what makes the id-preservation assertion on
// a returned offer mean something.
type fakeOffers struct {
	mu        sync.Mutex
	repo      *Repo
	created   map[string]core.Offer
	createErr error
	getErr    error
}

// newFakeOffers returns an offer writer backed only by a map.
func newFakeOffers() *fakeOffers {
	return &fakeOffers{created: map[string]core.Offer{}}
}

// newDBOffers returns an offer writer that also inserts rows into r's database.
func newDBOffers(r *Repo) *fakeOffers {
	return &fakeOffers{repo: r, created: map[string]core.Offer{}}
}

// CreateOffer records the offer, or fails with the configured createErr.
func (f *fakeOffers) CreateOffer(_ context.Context, o core.Offer) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.createErr != nil {
		return f.createErr
	}
	f.created[o.ID] = o
	if f.repo != nil {
		_, err := f.repo.write.Exec(
			`INSERT INTO offers (id, sku, title, description, condition, status, quantity,
			 shop_minor, shop_currency, owner_minor, owner_currency, location_id,
			 created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			o.ID, o.SKU, o.Title, o.Description, o.Condition, string(o.Status), o.Quantity,
			o.Shop.Minor, o.Shop.Currency, o.Owner.Minor, o.Owner.Currency,
			nullString(o.LocationID), formatTime(o.CreatedAt), formatTime(o.UpdatedAt))
		if err != nil {
			return err
		}
	}
	return nil
}

// Offer returns a recorded offer, with its photos when a database backs this
// fake, or fails with the configured getErr.
func (f *fakeOffers) Offer(ctx context.Context, id string) (core.Offer, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.getErr != nil {
		return core.Offer{}, f.getErr
	}
	o, ok := f.created[id]
	if !ok {
		return core.Offer{}, core.ErrNotFound
	}
	if f.repo == nil {
		return o, nil
	}
	rows, err := f.repo.read.QueryContext(ctx,
		`SELECT id, offer_id, position, filename, content_type, byte_size, sha256, created_at
		 FROM offer_photos WHERE offer_id = ? ORDER BY position, id`, id)
	if err != nil {
		return core.Offer{}, err
	}
	defer rows.Close()
	o.Photos = nil
	for rows.Next() {
		var (
			p       core.Photo
			created string
		)
		if err := rows.Scan(&p.ID, &p.OfferID, &p.Position, &p.Filename, &p.ContentType,
			&p.ByteSize, &p.SHA256, &created); err != nil {
			return core.Offer{}, err
		}
		if p.CreatedAt, err = parseTime(created); err != nil {
			return core.Offer{}, err
		}
		o.Photos = append(o.Photos, p)
	}
	return o, rows.Err()
}

// count returns how many offers were created through this fake.
func (f *fakeOffers) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.created)
}

// only returns the single offer this fake holds, failing the test otherwise.
func (f *fakeOffers) only(t *testing.T) core.Offer {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.created) != 1 {
		t.Fatalf("want exactly 1 created offer, got %d", len(f.created))
	}
	for _, o := range f.created {
		return o
	}
	return core.Offer{}
}

// fakeOffers must satisfy the narrow port this package declares.
var _ OfferWriter = (*fakeOffers)(nil)

// failingMove is a real store with one method broken, so a partial-failure test
// exercises every other write for real and fails only where it means to.
type failingMove struct {
	core.SubmissionStore
	err error
}

// MovePhotosToOffer always fails.
func (f failingMove) MovePhotosToOffer(_ context.Context, _, _ string) error {
	return f.err
}

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

// subIDs maps submissions to their ids, for readable ordering assertions.
func subIDs(subs []core.Submission) []string {
	out := make([]string, len(subs))
	for i, s := range subs {
		out[i] = s.ID
	}
	return out
}

// photoIDs maps photos to their ids, for readable id-preservation assertions.
func photoIDs(photos []core.Photo) []string {
	out := make([]string, len(photos))
	for i, p := range photos {
		out[i] = p.ID
	}
	return out
}

// equalStrings reports whether two string slices match element for element.
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
