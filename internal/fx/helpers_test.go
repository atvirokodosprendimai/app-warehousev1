package fx

import (
	"database/sql"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	// modernc.org/sqlite is the CGO-free driver, registered as "sqlite".
	_ "modernc.org/sqlite"
)

// createFxRates is fx_rates as migrations/00001_init.sql defines it, copied
// rather than imported: these tests must not depend on the sibling store
// package, and a copy that drifts from the migration shows up as a failing test
// here rather than as a surprise in production.
const createFxRates = `
CREATE TABLE fx_rates (
    as_of      TEXT NOT NULL,
    quote      TEXT NOT NULL,
    rate       TEXT NOT NULL,
    fetched_at TEXT NOT NULL,
    PRIMARY KEY (as_of, quote)
) STRICT;`

// newTestDB opens a fresh SQLite database in a temp directory and returns the
// reader and writer handles a Repo expects.
//
// The writer carries _txlock=immediate for the same reason the application's
// does: a transaction that reads before it writes would otherwise have to
// upgrade its lock, and SQLite fails an upgrade conflict without waiting.
func newTestDB(t *testing.T) (read, write *sql.DB) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "fx_test.db")
	const pragmas = "_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"

	write, err := sql.Open("sqlite", "file:"+path+"?"+pragmas+"&_txlock=immediate")
	if err != nil {
		t.Fatalf("open writer: %v", err)
	}
	// SQLite admits one writer; a larger pool would only queue less visibly.
	write.SetMaxOpenConns(1)
	if _, err := write.Exec(createFxRates); err != nil {
		write.Close()
		t.Fatalf("create fx_rates: %v", err)
	}

	// Opened after the writer has created the file: query_only(1) cannot.
	read, err = sql.Open("sqlite", "file:"+path+"?"+pragmas+"&_pragma=query_only(1)")
	if err != nil {
		write.Close()
		t.Fatalf("open reader: %v", err)
	}

	t.Cleanup(func() {
		read.Close()
		write.Close()
	})
	return read, write
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
