package settings

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
	"github.com/atvirokodosprendimai/app-warehousev1/internal/store"
	"github.com/atvirokodosprendimai/app-warehousev1/migrations"
)

// newRepo opens a database with the REAL migrations (ADR-013), so the settings
// table is exactly what production has.
//
// This package had no tests at all before ADR-016. It deliberately does not
// carry a copied CREATE TABLE: four other packages still do, and each one is a
// schema that can drift from the migration without anything going red.
func newRepo(t *testing.T) *Repo {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := store.Migrate(db.Write, migrations.FS); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewRepo(db.Read, db.Write)
}

// TestSettingsRoundTripTheMarketplaceDefaults pins ADR-016's storage half.
//
// The reported failure was that eBay's category could be supplied only through
// an environment variable, read once at start-up, documented nowhere. These
// values have to survive a write and a read for the Settings page to be worth
// anything, and a fresh installation with no rows at all has to come back empty
// rather than erroring — that is the ordinary first-run state, and treating it
// as an exception is what would push the value back into the environment.
func TestSettingsRoundTripTheMarketplaceDefaults(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()

	// A fresh installation: no rows, no error, nothing set.
	got, err := r.Settings(ctx)
	if err != nil {
		t.Fatalf("Settings on a fresh database: %v", err)
	}
	if got.Ebay != (core.Marketplace{}) {
		t.Errorf("fresh Ebay = %+v, want the zero value so the caller falls back to its "+
			"configured default", got.Ebay)
	}

	want := core.Settings{
		PublicBaseURL: "https://warehouse.example.com",
		Ebay: core.Marketplace{
			Category:    "11450",
			ConditionID: "3000",
			Location:    "Kaunas",
		},
	}
	if err := r.SaveSettings(ctx, want); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	got, err = r.Settings(ctx)
	if err != nil {
		t.Fatalf("Settings after save: %v", err)
	}
	if got.Ebay != want.Ebay {
		t.Errorf("Ebay = %+v, want %+v", got.Ebay, want.Ebay)
	}
	if got.PublicBaseURL != want.PublicBaseURL {
		t.Errorf("PublicBaseURL = %q, want %q — the marketplace keys must not disturb the "+
			"setting that was already there", got.PublicBaseURL, want.PublicBaseURL)
	}
	if got.UpdatedAt.IsZero() {
		t.Error("UpdatedAt is zero after a save")
	}
}

// TestSavingOneMarketplaceFieldLeavesTheOthers is the half that makes the
// Settings page usable rather than a trap.
//
// Every key moves in ONE transaction, and the whole struct is written each time.
// So a caller that loads, edits one field and saves must not blank the rest —
// and a caller that constructs a partial Settings by hand WILL blank them. That
// is a real edge with a real consequence, so it is pinned rather than assumed.
func TestSavingOneMarketplaceFieldLeavesTheOthers(t *testing.T) {
	r := newRepo(t)
	ctx := context.Background()

	full := core.Settings{
		PublicBaseURL: "https://warehouse.example.com",
		Ebay:          core.Marketplace{Category: "11450", ConditionID: "3000", Location: "Kaunas"},
	}
	if err := r.SaveSettings(ctx, full); err != nil {
		t.Fatalf("SaveSettings: %v", err)
	}

	// The read-modify-write an editing screen actually performs.
	cur, err := r.Settings(ctx)
	if err != nil {
		t.Fatalf("Settings: %v", err)
	}
	cur.Ebay.Category = "20081"
	if err := r.SaveSettings(ctx, cur); err != nil {
		t.Fatalf("SaveSettings (edit): %v", err)
	}

	got, err := r.Settings(ctx)
	if err != nil {
		t.Fatalf("Settings after edit: %v", err)
	}
	if got.Ebay.Category != "20081" {
		t.Errorf("Category = %q, want the edited %q", got.Ebay.Category, "20081")
	}
	if got.Ebay.ConditionID != "3000" || got.Ebay.Location != "Kaunas" {
		t.Errorf("editing the category disturbed its siblings: %+v", got.Ebay)
	}
}

// TestTheReadHandleRefusesToWrite pins ADR-001's guarantee for this package too:
// the read-only port is enforced by the driver, not by review.
func TestTheReadHandleRefusesToWrite(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "settings.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := store.Migrate(db.Write, migrations.FS); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	_, err = db.Read.Exec(
		`INSERT INTO settings (key, value, updated_at) VALUES ('x', 'y', 'z')`)
	if err == nil {
		t.Fatal("the read handle accepted a write; query_only(1) is not in effect")
	}
}
