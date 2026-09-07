package fx

import (
	"database/sql"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/store"
	"github.com/atvirokodosprendimai/app-warehousev1/migrations"
)

// newTestDB opens a fresh database, runs the REAL migrations over it, and
// returns the reader and writer handles a Repo expects.
//
// ⚠ IT USED TO CARRY A COPY OF `CREATE TABLE fx_rates`, with a comment saying
// the copy was acceptable because a drift would show up as a failing test here.
// That is the same comment `internal/offer` and `internal/submission` carried
// before their copies drifted and left the suite green against a schema
// production does not have — which is what ADR-013 was written about. The copy
// cannot detect its own drift: it defines the table the test then uses, so both
// sides move together and agree with each other while disagreeing with
// production.
//
// The old comment also said these tests "must not depend on the sibling store
// package". Nothing needed that: it is a TEST-ONLY import of a package that sits
// below this one and imports nothing from it, and four other packages here
// already do exactly this.
//
// store.Open also gives these tests the real handle split rather than a
// hand-copied DSN — one writer with _txlock=immediate, and a reader carrying
// query_only(1) so the driver REFUSES a write on the read path.
func newTestDB(t *testing.T) (read, write *sql.DB) {
	t.Helper()

	db, err := store.Open(filepath.Join(t.TempDir(), "fx_test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := store.Migrate(db.Write, migrations.FS); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db.Read, db.Write
}

// newTestRepo returns a Repo over a fresh database, plus the reader handle so a
// test can check the stored rows independently of the code under test.
func newTestRepo(t *testing.T) (*Repo, *sql.DB) {
	t.Helper()
	read, write := newTestDB(t)
	return NewRepo(read, write), read
}

// countRates reports how many rows fx_rates holds.
func countRates(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM fx_rates`).Scan(&n); err != nil {
		t.Fatalf("count fx_rates: %v", err)
	}
	return n
}

// ecbFixture is a real-shaped daily feed: the gesmes prefix on the envelope, the
// default namespace on the Cube tree, and the three levels of <Cube> the ECB
// actually publishes. GBP's trailing zero is deliberate — it is what a float
// round-trip would quietly discard.
const ecbFixture = `<?xml version="1.0" encoding="UTF-8"?>
<gesmes:Envelope xmlns:gesmes="http://www.gesmes.org/xml/2002-08-01" xmlns="http://www.ecb.int/vocabulary/2002-08-01/eurofxref">
	<gesmes:subject>Reference rates</gesmes:subject>
	<gesmes:Sender>
		<gesmes:name>European Central Bank</gesmes:name>
	</gesmes:Sender>
	<Cube>
		<Cube time='2026-09-04'>
			<Cube currency='USD' rate='1.1712'/>
			<Cube currency='JPY' rate='172.53'/>
			<Cube currency='GBP' rate='0.86720'/>
			<Cube currency='PLN' rate='4.2528'/>
		</Cube>
	</Cube>
</gesmes:Envelope>`

// fixtureDay is the publication date inside [ecbFixture]. It is a Friday, so a
// lookup for the weekend after it exercises RateOn's fallback.
const fixtureDay = "2026-09-04"

// fixtureCurrencies is how many rates [ecbFixture] quotes.
const fixtureCurrencies = 4

// quietLogs sends slog output to io.Discard for the duration of one test, so a
// deliberately failing refresh does not print an error that looks like a broken
// test.
func quietLogs(t *testing.T) {
	t.Helper()
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })
}
