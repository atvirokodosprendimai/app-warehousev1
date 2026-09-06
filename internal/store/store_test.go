package store

import (
	"database/sql"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// contendRW runs a read-then-write transaction concurrently and returns how many
// of them failed.
//
// The read-then-write ordering is the whole point: it is the shape that forces a
// lock upgrade, and it is the shape ordinary service code has (check something,
// then write based on the check).
func contendRW(t *testing.T, db *sql.DB, goroutines, perGoroutine int) int {
	t.Helper()

	var (
		mu     sync.Mutex
		failed int
		wg     sync.WaitGroup
	)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < perGoroutine; i++ {
				err := func() error {
					tx, err := db.Begin()
					if err != nil {
						return err
					}
					defer tx.Rollback()

					var n int
					// The READ that forces the upgrade.
					if err := tx.QueryRow(`SELECT count(*) FROM contend`).Scan(&n); err != nil {
						return err
					}
					if _, err := tx.Exec(`INSERT INTO contend (n) VALUES (?)`, n); err != nil {
						return err
					}
					return tx.Commit()
				}()
				if err != nil {
					mu.Lock()
					failed++
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()
	return failed
}

// openContended opens path with a MULTI-connection pool, so that the only
// variable between the two arms of the test is _txlock. Capping the pool at one
// connection would also remove the failure and would hide what is being measured.
func openContended(t *testing.T, path, txlock string) *sql.DB {
	t.Helper()

	db, err := sql.Open("sqlite", dsn(path, txlock,
		"journal_mode(WAL)",
		"busy_timeout(5000)",
		"foreign_keys(1)",
	))
	if err != nil {
		t.Fatalf("open (_txlock=%q): %v", txlock, err)
	}
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(8)
	t.Cleanup(func() { db.Close() })
	return db
}

// TestTxlockImmediateIsHonouredByTheDriver proves, by behaviour change, that
// modernc.org/sqlite acts on _txlock=immediate.
//
// _txlock is a documented mattn/go-sqlite3 parameter; that another driver
// accepts the DSN establishes nothing, because a silently ignored knob is
// byte-identical to an honoured one from the caller's side. The only evidence
// that distinguishes them is a differing failure count, so this test asserts
// BOTH arms: the deferred arm must actually fail, or the immediate arm's zero
// proves nothing and the test would pass without testing anything.
func TestTxlockImmediateIsHonouredByTheDriver(t *testing.T) {
	const (
		goroutines   = 8
		perGoroutine = 40
	)

	deferredPath := filepath.Join(t.TempDir(), "deferred.db")
	deferred := openContended(t, deferredPath, "")
	if _, err := deferred.Exec(`CREATE TABLE contend (n INTEGER)`); err != nil {
		t.Fatalf("create table (deferred): %v", err)
	}
	deferredFailures := contendRW(t, deferred, goroutines, perGoroutine)

	immediatePath := filepath.Join(t.TempDir(), "immediate.db")
	immediate := openContended(t, immediatePath, "immediate")
	if _, err := immediate.Exec(`CREATE TABLE contend (n INTEGER)`); err != nil {
		t.Fatalf("create table (immediate): %v", err)
	}
	immediateFailures := contendRW(t, immediate, goroutines, perGoroutine)

	total := goroutines * perGoroutine
	t.Logf("read-then-write transactions: %d; deferred failures=%d immediate failures=%d",
		total, deferredFailures, immediateFailures)

	if deferredFailures == 0 {
		t.Fatalf("the deferred arm did not fail (%d/%d), so this test cannot distinguish an "+
			"honoured _txlock from an ignored one; do not read the immediate arm as evidence",
			deferredFailures, total)
	}
	if immediateFailures != 0 {
		t.Errorf("_txlock=immediate still lost %d/%d transactions; the writer DSN in store.go "+
			"does not deliver the property its doc comment claims", immediateFailures, total)
	}
}

// TestReaderHandleCannotWrite pins the driver-level guarantee that the read path
// is read-only, so that CQRS's read-only port is enforced by the database rather
// than by review.
func TestReaderHandleCannotWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "app.db")

	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if _, err := db.Write.Exec(`CREATE TABLE t (n INTEGER)`); err != nil {
		t.Fatalf("writer could not create table: %v", err)
	}

	// The reader must see the table...
	var n int
	if err := db.Read.QueryRow(`SELECT count(*) FROM t`).Scan(&n); err != nil {
		t.Fatalf("reader could not read: %v", err)
	}

	// ...and must be refused when it tries to write to it.
	_, err = db.Read.Exec(`INSERT INTO t (n) VALUES (1)`)
	if err == nil {
		t.Fatal("reader handle accepted a write; query_only(1) is not in effect and the " +
			"read-only port is a convention rather than a guarantee")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "readonly") {
		t.Errorf("reader write failed, but not as a readonly refusal: %v", err)
	}
}
