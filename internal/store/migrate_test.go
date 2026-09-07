package store

import (
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

	if err := goose.Down(db.Write, "."); err != nil {
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
