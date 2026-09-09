package store

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/app-warehousev1/migrations"
	"github.com/pressly/goose/v3"
)

// TestMigrateIsIdempotent runs the migrations twice: a start-up that re-runs
// them must be a no-op, because the binary migrates on every boot.
func TestMigrateIsIdempotent(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if err := Migrate(db.Write, migrations.FS); err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	v1, err := Version(db.Write)
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if v1 == 0 {
		t.Fatal("schema version is 0 after migrating; nothing ran")
	}

	if err := Migrate(db.Write, migrations.FS); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	v2, err := Version(db.Write)
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if v1 != v2 {
		t.Errorf("re-running migrations moved the version %d -> %d", v1, v2)
	}
}

// migrateDownToZero rolls the whole set backward.
//
// It drives goose directly rather than through a `store` function, because there
// is no rollback path in the application: the binary migrates UP on every boot
// and nothing has ever needed to go the other way. Adding a production API whose
// only caller is this test would be inventing a surface to justify a check.
func migrateDownToZero(t *testing.T, db *sql.DB) {
	t.Helper()
	goose.SetBaseFS(migrations.FS)
	defer goose.SetBaseFS(nil)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("goose dialect: %v", err)
	}
	goose.SetLogger(goose.NopLogger())
	if err := goose.DownTo(db, ".", 0); err != nil {
		t.Fatalf("rolling the migrations back: %v", err)
	}
}

// TestEveryMigrationCanBeRolledBack exercises the direction none of them has
// ever been run in.
//
// ⚠ EVERY MIGRATION IN THIS REPOSITORY HAS A `-- +goose Down` SECTION AND, UNTIL
// THIS TEST, NOT ONE OF THEM HAD EVER EXECUTED. `TestMigrateIsIdempotent` runs
// the set forward twice; nothing ran it backward. A down migration that does not
// work is discovered during the incident it was written for — at the moment
// somebody needs it and has least time to debug it.
//
// ★ THE SECOND `Migrate` IS THE REAL ASSERTION, not the rollback itself. goose
// reports success for a Down section that drops nothing, so "it rolled back
// without erroring" proves very little. Re-applying the whole set afterwards is
// what catches it: a table a Down forgot to drop makes the next CREATE TABLE
// fail, and a foreign key dropped in the wrong ORDER fails on the way down.
// Migration 00009 is the live example — it drops `offers.category_id` BEFORE the
// tables it references, and that ordering is load-bearing rather than tidy.
func TestEveryMigrationCanBeRolledBack(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if err := Migrate(db.Write, migrations.FS); err != nil {
		t.Fatalf("migrating up: %v", err)
	}
	up, err := Version(db.Write)
	if err != nil {
		t.Fatalf("Version: %v", err)
	}
	if up == 0 {
		t.Fatal("schema version is 0 after migrating; nothing ran, so rolling " +
			"back would prove nothing either")
	}

	migrateDownToZero(t, db.Write)

	if v, err := Version(db.Write); err != nil {
		t.Fatalf("Version after rollback: %v", err)
	} else if v != 0 {
		t.Errorf("rolling back left the schema at version %d, not 0", v)
	}

	// Nothing of ours may survive. goose keeps its own bookkeeping table, and
	// SQLite keeps internal ones; anything else is a table some Down forgot.
	left := applicationTables(t, db.Write)
	if len(left) > 0 {
		t.Errorf("these tables survived a full rollback: %s. A Down section that "+
			"drops nothing still reports success, so the only way this is caught "+
			"is by looking at what is actually left", strings.Join(left, ", "))
	}

	// ★ And the set must go back up over the wreckage. This is what a real
	// rollback is FOR: undo the release, fix it, deploy again.
	if err := Migrate(db.Write, migrations.FS); err != nil {
		t.Fatalf("re-applying the migrations after a rollback: %v. The rollback "+
			"left the database in a state its own migrations cannot start from, "+
			"which is worse than not being able to roll back at all", err)
	}
	again, err := Version(db.Write)
	if err != nil {
		t.Fatalf("Version after re-applying: %v", err)
	}
	if again != up {
		t.Errorf("the schema came back to version %d rather than %d", again, up)
	}
}

// applicationTables lists the tables this repository's migrations created, which
// is every table except goose's bookkeeping and SQLite's own internals.
func applicationTables(t *testing.T, db *sql.DB) []string {
	t.Helper()
	rows, err := db.Query(`
		SELECT name FROM sqlite_master
		WHERE type = 'table'
		  AND name NOT LIKE 'sqlite_%'
		  AND name <> 'goose_db_version'
		ORDER BY name`)
	if err != nil {
		t.Fatalf("listing tables: %v", err)
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scanning table name: %v", err)
		}
		out = append(out, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("listing tables: %v", err)
	}
	return out
}

// TestSchemaRefusesASoldOfferWithNoDate pins the CHECK constraint at the
// database level. The domain enforces the same rule, and both matter: a report
// is only as trustworthy as the weakest write path that can reach the table.
func TestSchemaRefusesASoldOfferWithNoDate(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := Migrate(db.Write, migrations.FS); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	_, err = db.Write.Exec(`
		INSERT INTO offers (id, sku, title, status, created_at, updated_at)
		VALUES ('x', 'SKU-1', 'A thing', 'sold', '2026-09-06', '2026-09-06')`)
	if err == nil {
		t.Fatal("the database accepted a sold offer with no sale date; revenue could not " +
			"be attributed to a period")
	}
	if !strings.Contains(strings.ToUpper(err.Error()), "CHECK") {
		t.Errorf("insert failed, but not on the CHECK constraint: %v", err)
	}
}

// TestSchemaKeepsSiblingCodesUniqueButAllowsReuseAcrossParents pins the rule
// that makes a growing warehouse workable: two different sites may each have a
// shelf called S3.
func TestSchemaKeepsSiblingCodesUniqueButAllowsReuseAcrossParents(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := Migrate(db.Write, migrations.FS); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	ins := `INSERT INTO locations (id, parent_id, kind, code, path, city, created_at)
	        VALUES (?, ?, ?, ?, ?, ?, '2026-09-06')`
	mustExec := func(args ...any) {
		t.Helper()
		if _, err := db.Write.Exec(ins, args...); err != nil {
			t.Fatalf("insert %v: %v", args, err)
		}
	}
	mustExec("s1", nil, "site", "KAUNAS", "KAUNAS", "Kaunas")
	mustExec("s2", nil, "site", "VILNIUS", "VILNIUS", "Vilnius")
	mustExec("a", "s1", "shelf", "S3", "KAUNAS/S3", "")
	// Same code, different parent: must be allowed.
	mustExec("b", "s2", "shelf", "S3", "VILNIUS/S3", "")

	// Same code, same parent: must be refused.
	if _, err := db.Write.Exec(ins, "c", "s1", "shelf", "S3", "KAUNAS/S3-dup", ""); err == nil {
		t.Error("two shelves called S3 were allowed under the same parent")
	}
	// And a duplicate path must be refused however it arises, because a pasted
	// path has to resolve to exactly one place.
	if _, err := db.Write.Exec(ins, "d", "s2", "shelf", "S4", "KAUNAS/S3", ""); err == nil {
		t.Error("two locations were allowed to share a materialised path")
	}
}

// TestSchemaRefusesToDeleteAnOccupiedLocation pins ON DELETE RESTRICT: emptying
// a shelf must be a deliberate act of moving stock, not a side effect of tidying
// the tree.
func TestSchemaRefusesToDeleteAnOccupiedLocation(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := Migrate(db.Write, migrations.FS); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	if _, err := db.Write.Exec(`
		INSERT INTO locations (id, parent_id, kind, code, path, city, created_at)
		VALUES ('loc', NULL, 'site', 'KAUNAS', 'KAUNAS', 'Kaunas', '2026-09-06')`); err != nil {
		t.Fatalf("insert location: %v", err)
	}
	if _, err := db.Write.Exec(`
		INSERT INTO offers (id, sku, title, status, location_id, created_at, updated_at)
		VALUES ('o', 'SKU-1', 'A thing', 'draft', 'loc', '2026-09-06', '2026-09-06')`); err != nil {
		t.Fatalf("insert offer: %v", err)
	}

	if _, err := db.Write.Exec(`DELETE FROM locations WHERE id = 'loc'`); err == nil {
		t.Fatal("a location still holding stock was deleted; the answer to \"where is this " +
			"thing\" was silently orphaned")
	}
}

// TestSchemaCascadesPhotosWithTheirOffer confirms the one place a cascade IS
// wanted: a photo has no meaning without its offer.
func TestSchemaCascadesPhotosWithTheirOffer(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := Migrate(db.Write, migrations.FS); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	if _, err := db.Write.Exec(`
		INSERT INTO offers (id, sku, title, status, created_at, updated_at)
		VALUES ('o', 'SKU-1', 'A thing', 'draft', '2026-09-06', '2026-09-06')`); err != nil {
		t.Fatalf("insert offer: %v", err)
	}
	if _, err := db.Write.Exec(`
		INSERT INTO offer_photos (id, offer_id, content_type, created_at)
		VALUES ('p', 'o', 'image/jpeg', '2026-09-06')`); err != nil {
		t.Fatalf("insert photo: %v", err)
	}
	if _, err := db.Write.Exec(`DELETE FROM offers WHERE id = 'o'`); err != nil {
		t.Fatalf("delete offer: %v", err)
	}

	var n int
	if err := db.Read.QueryRow(`SELECT count(*) FROM offer_photos`).Scan(&n); err != nil {
		t.Fatalf("count photos: %v", err)
	}
	if n != 0 {
		t.Errorf("%d photo row(s) outlived their offer", n)
	}
}

// TestTheCategoriesMigrationGoesDownAndUpAgain exercises ONE down migration:
// 00009, the one this change adds.
//
// ⚠ THIS REPOSITORY HAD NEVER RUN A DOWN MIGRATION when this test was written —
// `BACKLOG.md` names it as one of the three gaps that matter, and an untested
// down is not a rollback, it is a paragraph of SQL nobody has compiled. The
// scope is deliberately this migration and not the corpus: proving 00009
// reverses is in scope for the change that adds it, and proving 00001..00008 do
// is its own piece of work, still on the backlog.
//
// It matters here beyond diligence. 00009 both CREATES tables and ALTERs an
// existing one, and with foreign keys enabled the order of the down steps is
// load-bearing: `offers.category_id` references `categories`, so the column has
// to go first or the DROP is refused.
func TestTheCategoriesMigrationGoesDownAndUpAgain(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	if err := Migrate(db.Write, migrations.FS); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if _, err := db.Write.Exec(`SELECT 1 FROM categories LIMIT 1`); err != nil {
		t.Fatalf("categories is absent after migrating up: %v", err)
	}

	goose.SetBaseFS(migrations.FS)
	defer goose.SetBaseFS(nil)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("dialect: %v", err)
	}
	goose.SetLogger(goose.NopLogger())

	// ⚠ DownTo(8), NOT Down(). A bare `goose.Down` undoes THE NEWEST migration,
	// whichever that happens to be — so this test silently stopped being about
	// 00009 the moment 00010 was added, and its assertions failed against a
	// migration it was never written for. Naming the version is what keeps a test
	// about the migration it says it is about.
	if err := goose.DownTo(db.Write, ".", 8); err != nil {
		t.Fatalf("down: %v — the rollback this migration documents does not run", err)
	}
	for _, table := range []string{"categories", "category_fields", "offer_field_values"} {
		if _, err := db.Write.Exec(`SELECT 1 FROM ` + table + ` LIMIT 1`); err == nil {
			t.Errorf("%s survived the down migration", table)
		}
	}
	// The column has to be gone too, or a second up would fail on a duplicate.
	if _, err := db.Write.Exec(`SELECT category_id FROM offers LIMIT 1`); err == nil {
		t.Error("offers.category_id survived the down migration; the next up would " +
			"fail on a duplicate column name")
	}

	if err := Migrate(db.Write, migrations.FS); err != nil {
		t.Fatalf("re-migrate after down: %v — a down that cannot be followed by an up "+
			"is not a rollback, it is a one-way trip", err)
	}
	if _, err := db.Write.Exec(`SELECT 1 FROM categories LIMIT 1`); err != nil {
		t.Fatalf("categories is absent after migrating up again: %v", err)
	}
}

// TestAnExistingOfferCategorySurvivesTheWidening proves migration 00010 CARRIES
// its rows rather than dropping them (ADR-022 T2).
//
// ⚠ IT HAS TO START FROM THE OLD SCHEMA, and that is the whole design of the
// test. A test that migrates straight up and then looks at the new table sees an
// empty database agreeing with itself: the copy could be missing entirely and
// nothing would fail. So this goes UP, steps back DOWN to 9 to restore
// `offer_categories`, writes a row a real deployment would already hold, and then
// migrates up again — which is the only arrangement where the INSERT…SELECT is on
// the path being tested.
func TestAnExistingOfferCategorySurvivesTheWidening(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "app.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := Migrate(db.Write, migrations.FS); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	goose.SetBaseFS(migrations.FS)
	defer goose.SetBaseFS(nil)
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("dialect: %v", err)
	}
	goose.SetLogger(goose.NopLogger())

	// Back to the schema a deployment upgrading from ADR-016 actually has.
	if err := goose.DownTo(db.Write, ".", 9); err != nil {
		t.Fatalf("down to 9: %v", err)
	}
	if _, err := db.Write.Exec(`SELECT 1 FROM offer_categories LIMIT 1`); err != nil {
		t.Fatalf("offer_categories is absent at version 9, so this test is not starting "+
			"from the schema it claims to: %v", err)
	}

	if _, err := db.Write.Exec(`
		INSERT INTO offers (id, sku, title, status, created_at, updated_at)
		VALUES ('o1', 'WH0000001', 'A thing', 'draft', '2026-09-09', '2026-09-09')`); err != nil {
		t.Fatalf("insert offer: %v", err)
	}
	if _, err := db.Write.Exec(`
		INSERT INTO offer_categories (offer_id, profile, category)
		VALUES ('o1', 'ebay', '11450')`); err != nil {
		t.Fatalf("insert category: %v", err)
	}

	if err := Migrate(db.Write, migrations.FS); err != nil {
		t.Fatalf("re-migrate: %v", err)
	}

	var profile, field, value string
	err = db.Read.QueryRow(`
		SELECT profile, field, value FROM offer_marketplace_values WHERE offer_id = 'o1'`).
		Scan(&profile, &field, &value)
	if err != nil {
		t.Fatalf("the per-offer category did not survive the widening: %v — every offer "+
			"an operator had already categorised would silently fall back to the "+
			"configured default", err)
	}
	if profile != "ebay" || field != "category" || value != "11450" {
		t.Errorf("carried row = (%q, %q, %q), want (ebay, category, 11450)",
			profile, field, value)
	}

	// And the old table is gone, or two places would answer the same question.
	if _, err := db.Write.Exec(`SELECT 1 FROM offer_categories LIMIT 1`); err == nil {
		t.Error("offer_categories survived the widening; two tables now hold a per-offer " +
			"category and nothing says which one an export reads")
	}
}
