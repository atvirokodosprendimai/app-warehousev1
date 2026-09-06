package store

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestFTS5IsCompiledIntoTheDriver proves the CGO-free driver actually ships
// FTS5, before anything depends on it.
//
// modernc.org/sqlite is a translation of SQLite rather than a binding to it, so
// which optional modules it carries is a property of that build and not of
// SQLite's documentation. A missing module does not degrade quietly here — the
// CREATE VIRTUAL TABLE fails outright — but it would fail at migration time on
// somebody else's machine after a driver bump, which is a worse place to find
// out than a test.
//
// The test does a real round trip rather than only creating the table, because
// "the module loaded" and "matching works" are different claims and only the
// second one is what the search box needs.
func TestFTS5IsCompiledIntoTheDriver(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "fts.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if _, err := db.Write.Exec(`CREATE VIRTUAL TABLE probe USING fts5(title, body)`); err != nil {
		t.Fatalf("FTS5 is not available in this driver build, so full-text search "+
			"cannot be used: %v", err)
	}
	if _, err := db.Write.Exec(
		`INSERT INTO probe (title, body) VALUES (?, ?)`,
		"Vintage brass desk lamp", "art deco, rewired, working",
	); err != nil {
		t.Fatalf("insert: %v", err)
	}
	if _, err := db.Write.Exec(
		`INSERT INTO probe (title, body) VALUES (?, ?)`,
		"Oak dining chair", "one of six, sound joints",
	); err != nil {
		t.Fatalf("insert: %v", err)
	}

	// A prefix query is what a search-as-you-type box sends, so that is what is
	// asserted rather than an exact-token match that would pass on a weaker
	// tokenizer.
	var title string
	if err := db.Read.QueryRow(
		`SELECT title FROM probe WHERE probe MATCH ? ORDER BY rank LIMIT 1`, `bras*`,
	).Scan(&title); err != nil {
		t.Fatalf("prefix MATCH failed: %v", err)
	}
	if !strings.Contains(title, "brass") {
		t.Errorf("prefix search returned %q", title)
	}

	// And a term that appears only in the body must find the row too, or the
	// description would be indexed without being searchable.
	if err := db.Read.QueryRow(
		`SELECT title FROM probe WHERE probe MATCH ? LIMIT 1`, `rewired`,
	).Scan(&title); err != nil {
		t.Fatalf("body MATCH failed: %v", err)
	}
	if title != "Vintage brass desk lamp" {
		t.Errorf("body search returned %q", title)
	}

	// A query that matches nothing must return no rows rather than everything —
	// the failure that makes a search box look like it is working while it is
	// ignoring the query entirely.
	var n int
	if err := db.Read.QueryRow(
		`SELECT count(*) FROM probe WHERE probe MATCH ?`, `zzzznotpresent`,
	).Scan(&n); err != nil {
		t.Fatalf("negative MATCH failed: %v", err)
	}
	if n != 0 {
		t.Errorf("a query matching nothing returned %d rows", n)
	}
}
