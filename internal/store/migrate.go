package store

import (
	"database/sql"
	"fmt"
	"io/fs"

	"github.com/pressly/goose/v3"
)

// Migrate brings the schema up to date using the embedded SQL migrations.
//
// It runs on the WRITER handle only. The reader carries query_only(1) and would
// be refused by the driver — which is the guarantee working, not a problem to
// route around.
func Migrate(db *sql.DB, migrations fs.FS) error {
	goose.SetBaseFS(migrations)
	// Restored afterwards so a later caller (a test, a CLI subcommand) is not
	// silently bound to this FS.
	defer goose.SetBaseFS(nil)

	if err := goose.SetDialect("sqlite3"); err != nil {
		return fmt.Errorf("store: goose dialect: %w", err)
	}
	// goose logs each applied migration to stdout by default, which turns a
	// no-op start-up into noise; silence it and let the caller report.
	goose.SetLogger(goose.NopLogger())

	if err := goose.Up(db, "."); err != nil {
		return fmt.Errorf("store: migrate: %w", err)
	}
	return nil
}

// Version reports the current schema version, for a health endpoint or a
// start-up log line.
func Version(db *sql.DB) (int64, error) {
	if err := goose.SetDialect("sqlite3"); err != nil {
		return 0, fmt.Errorf("store: goose dialect: %w", err)
	}
	v, err := goose.GetDBVersion(db)
	if err != nil {
		return 0, fmt.Errorf("store: schema version: %w", err)
	}
	return v, nil
}
