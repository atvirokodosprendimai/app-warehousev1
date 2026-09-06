// Package store opens the application's SQLite database and runs its migrations.
//
// It deliberately opens the same file twice. See [DB] for why.
package store

import (
	"database/sql"
	"fmt"
	"net/url"
	"strings"

	// modernc.org/sqlite is the CGO-free driver. It registers itself as "sqlite".
	_ "modernc.org/sqlite"
)

// busyTimeoutMS is how long SQLite waits for an ordinary lock before giving up.
// It is deliberately generous: the only waits that should ever reach it are
// short writer queues, because the lock-upgrade case that would otherwise
// dominate is designed out by Write's _txlock=immediate.
const busyTimeoutMS = 5000

// maxReaders bounds the reader pool. WAL lets readers run concurrently with the
// writer and with each other, so this is sized for request concurrency rather
// than for lock contention.
const maxReaders = 8

// DB holds the two handles the application uses to reach one SQLite file.
//
// Write and Read address the SAME file through different DSNs, which is a
// correctness decision rather than redundancy.
//
// SQLite grants a deferred transaction its write lock on the first write
// statement. A transaction that reads first must therefore UPGRADE its lock,
// and on an upgrade conflict SQLite returns SQLITE_BUSY immediately without
// consulting the busy handler — two transactions both waiting to upgrade would
// deadlock. So busy_timeout does nothing at all for the read-then-write shape
// that almost every service method has, which is the trap this split exists to
// avoid. Write takes the write lock at BEGIN instead (_txlock=immediate), so
// there is no upgrade to conflict over and contention degrades into an ordinary
// lock wait that busy_timeout does honour.
//
// Write is capped at one connection because SQLite admits one writer regardless;
// Read is a normal pool and stays concurrent under WAL. Capping the whole
// application at one connection also removes the failure, but it serialises
// reads through that connection and throws away the reader/writer concurrency
// WAL was enabled for — the wrong trade for a read-heavy dashboard.
//
// Read additionally carries query_only(1), so the driver itself refuses a write
// on the read path. That turns "read models must not write" from a code-review
// rule into something the database enforces.
type DB struct {
	// Write is the single-writer handle. Every mutation goes through it.
	Write *sql.DB
	// Read is the many-reader handle. It cannot write: the driver refuses.
	Read *sql.DB
}

// Close closes both handles, returning the first error encountered.
func (db *DB) Close() error {
	werr := db.Write.Close()
	rerr := db.Read.Close()
	if werr != nil {
		return werr
	}
	return rerr
}

// dsn builds a modernc.org/sqlite DSN for path with the given pragmas and an
// optional transaction lock mode.
//
// path is placed in the URI's opaque position rather than being escaped as a
// query value, because SQLite's own URI filename handling expects it there.
func dsn(path string, txlock string, pragmas ...string) string {
	q := url.Values{}
	for _, p := range pragmas {
		q.Add("_pragma", p)
	}
	if txlock != "" {
		q.Set("_txlock", txlock)
	}
	return "file:" + path + "?" + q.Encode()
}

// writerDSN returns the DSN for the single-writer handle.
func writerDSN(path string) string {
	return dsn(path, "immediate",
		"journal_mode(WAL)",
		fmt.Sprintf("busy_timeout(%d)", busyTimeoutMS),
		"foreign_keys(1)",
		"synchronous(NORMAL)",
	)
}

// readerDSN returns the DSN for the many-reader handle. query_only(1) makes the
// driver refuse writes on this handle.
func readerDSN(path string) string {
	return dsn(path, "",
		"journal_mode(WAL)",
		fmt.Sprintf("busy_timeout(%d)", busyTimeoutMS),
		"foreign_keys(1)",
		"query_only(1)",
	)
}

// Open opens path as a WAL-mode SQLite database and returns both handles.
//
// The writer is opened and pinged first so that WAL mode and the database file
// exist before any reader connects; a reader opened with query_only(1) cannot
// create the file itself.
func Open(path string) (*DB, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("store: empty database path")
	}

	w, err := sql.Open("sqlite", writerDSN(path))
	if err != nil {
		return nil, fmt.Errorf("store: open writer: %w", err)
	}
	// One connection: SQLite admits a single writer, so a larger pool would only
	// queue in a place with worse diagnostics.
	w.SetMaxOpenConns(1)
	w.SetMaxIdleConns(1)
	if err := w.Ping(); err != nil {
		w.Close()
		return nil, fmt.Errorf("store: ping writer: %w", err)
	}

	r, err := sql.Open("sqlite", readerDSN(path))
	if err != nil {
		w.Close()
		return nil, fmt.Errorf("store: open reader: %w", err)
	}
	r.SetMaxOpenConns(maxReaders)
	r.SetMaxIdleConns(maxReaders)
	if err := r.Ping(); err != nil {
		w.Close()
		r.Close()
		return nil, fmt.Errorf("store: ping reader: %w", err)
	}

	return &DB{Write: w, Read: r}, nil
}
