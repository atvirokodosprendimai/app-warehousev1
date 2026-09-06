package location

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"

	// modernc.org/sqlite is the CGO-free driver, registered as "sqlite". The
	// tests open their own database rather than importing internal/store, so a
	// failure here is this package's and not the store's.
	_ "modernc.org/sqlite"
)

// testSchema is the part of migrations/00001_init.sql this package touches,
// copied so the tests do not depend on a migration runner. The offers table is
// here for one reason: it is what makes a location occupied, and its ON DELETE
// RESTRICT is the refusal [Service.Delete] has to surface.
const testSchema = `
CREATE TABLE locations (
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
) STRICT;
CREATE INDEX locations_parent_idx ON locations (parent_id);
CREATE INDEX locations_path_idx ON locations (path);

CREATE TABLE offers (
    id             TEXT PRIMARY KEY,
    sku            TEXT NOT NULL UNIQUE,
    title          TEXT NOT NULL,
    status         TEXT NOT NULL DEFAULT 'draft',
    shop_minor     INTEGER NOT NULL DEFAULT 0,
    location_id    TEXT REFERENCES locations (id) ON DELETE RESTRICT,
    created_at     TEXT NOT NULL,
    updated_at     TEXT NOT NULL
) STRICT;
`

// newRepo opens a private database and returns a Repo wired the way the
// application wires one: a query_only reader and an immediate-locking writer
// over the same file. The reader is query_only on purpose — a read method that
// reached for the write handle would still pass, but a write that reached for
// the read handle is refused by the driver, which is the direction that can
// corrupt the tree.
//
// The write handle is returned as well, for the fixtures that have to insert
// rows this package does not own.
func newRepo(t *testing.T) (*Repo, *sql.DB) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "t.db")

	write, err := sql.Open("sqlite", "file:"+path+
		"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"+
		"&_pragma=foreign_keys(1)&_txlock=immediate")
	if err != nil {
		t.Fatalf("open writer: %v", err)
	}
	write.SetMaxOpenConns(1)
	write.SetMaxIdleConns(1)
	if _, err := write.Exec(testSchema); err != nil {
		t.Fatalf("create schema: %v", err)
	}

	read, err := sql.Open("sqlite", "file:"+path+
		"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)"+
		"&_pragma=foreign_keys(1)&_pragma=query_only(1)")
	if err != nil {
		write.Close()
		t.Fatalf("open reader: %v", err)
	}
	t.Cleanup(func() {
		read.Close()
		write.Close()
	})
	return NewRepo(read, write), write
}

// insert puts one row in directly, so that the read methods are tested against
// rows this package's own write path did not compose.
func insert(t *testing.T, r *Repo, id, parentID string, kind core.Kind, code, path string) core.Location {
	t.Helper()
	l := core.Location{
		ID:        id,
		ParentID:  parentID,
		Kind:      kind,
		Code:      code,
		Path:      path,
		CreatedAt: time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC),
	}
	if err := r.CreateLocation(context.Background(), l); err != nil {
		t.Fatalf("insert %q: %v", path, err)
	}
	return l
}

// fourDeep seeds KAUNAS-GARAGE/B1/R1/S3/A000005 and returns the bin at the
// bottom. Four levels is the shallowest tree that can get ancestor ORDER wrong
// in a way a two-level tree would hide.
func fourDeep(t *testing.T, r *Repo) core.Location {
	t.Helper()
	insert(t, r, "site", "", core.KindSite, "KAUNAS-GARAGE", "KAUNAS-GARAGE")
	insert(t, r, "bld", "site", core.KindBuilding, "B1", "KAUNAS-GARAGE/B1")
	insert(t, r, "room", "bld", core.KindRoom, "R1", "KAUNAS-GARAGE/B1/R1")
	insert(t, r, "shelf", "room", core.KindShelf, "S3", "KAUNAS-GARAGE/B1/R1/S3")
	return insert(t, r, "bin", "shelf", core.KindBin, "A000005", "KAUNAS-GARAGE/B1/R1/S3/A000005")
}

// pathsOf reads every path in tree order.
func pathsOf(t *testing.T, r *Repo) []string {
	t.Helper()
	all, err := r.AllLocations(context.Background())
	if err != nil {
		t.Fatalf("AllLocations: %v", err)
	}
	out := make([]string, 0, len(all))
	for _, l := range all {
		out = append(out, l.Path)
	}
	return out
}

func TestLocationRoundTripsEveryField(t *testing.T) {
	r, _ := newRepo(t)
	ctx := context.Background()
	want := core.Location{
		ID:               "site",
		Kind:             core.KindSite,
		Code:             "KAUNAS-GARAGE",
		Path:             "KAUNAS-GARAGE",
		Label:            "the back garage",
		Custodian:        "Jonas",
		CustodianContact: "+37060000000",
		City:             "Kaunas",
		Country:          "LT",
		Notes:            "key under the mat",
		CreatedAt:        time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC),
	}
	if err := r.CreateLocation(ctx, want); err != nil {
		t.Fatalf("CreateLocation: %v", err)
	}

	got, err := r.Location(ctx, "site")
	if err != nil {
		t.Fatalf("Location: %v", err)
	}
	if got != want {
		t.Errorf("Location round trip\n got %+v\nwant %+v", got, want)
	}
	if loc := got.CreatedAt.Location(); loc != time.UTC {
		t.Errorf("CreatedAt came back in %v, want UTC", loc)
	}
}

func TestLocationReportsNotFound(t *testing.T) {
	r, _ := newRepo(t)
	ctx := context.Background()

	if _, err := r.Location(ctx, "nope"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("Location of an unknown id = %v, want core.ErrNotFound", err)
	}
	if _, err := r.LocationByPath(ctx, "NOWHERE/X"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("LocationByPath of an unknown path = %v, want core.ErrNotFound", err)
	}
}

func TestLocationByPathResolvesTheMaterialisedAddress(t *testing.T) {
	r, _ := newRepo(t)
	bin := fourDeep(t, r)

	got, err := r.LocationByPath(context.Background(), "KAUNAS-GARAGE/B1/R1/S3/A000005")
	if err != nil {
		t.Fatalf("LocationByPath: %v", err)
	}
	if got.ID != bin.ID {
		t.Errorf("LocationByPath returned %s, want the bin %s", got.ID, bin.ID)
	}
}

func TestAncestorsRunFromTheSiteDownToButExcludingTheNode(t *testing.T) {
	r, _ := newRepo(t)
	bin := fourDeep(t, r)

	got, err := r.Ancestors(context.Background(), bin.ID)
	if err != nil {
		t.Fatalf("Ancestors: %v", err)
	}
	var paths []string
	for _, a := range got {
		paths = append(paths, a.Path)
	}
	want := []string{
		"KAUNAS-GARAGE",
		"KAUNAS-GARAGE/B1",
		"KAUNAS-GARAGE/B1/R1",
		"KAUNAS-GARAGE/B1/R1/S3",
	}
	if !slices.Equal(paths, want) {
		t.Errorf("Ancestors\n got %q\nwant %q", paths, want)
	}
}

func TestAncestorsOfASiteAreEmpty(t *testing.T) {
	r, _ := newRepo(t)
	fourDeep(t, r)

	got, err := r.Ancestors(context.Background(), "site")
	if err != nil {
		t.Fatalf("Ancestors: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("Ancestors of a site = %d rows, want 0", len(got))
	}
}

func TestChildrenOfAnEmptyParentAreTheSites(t *testing.T) {
	r, _ := newRepo(t)
	fourDeep(t, r)
	insert(t, r, "site2", "", core.KindSite, "VILNIUS", "VILNIUS")

	got, err := r.Children(context.Background(), "")
	if err != nil {
		t.Fatalf("Children: %v", err)
	}
	var paths []string
	for _, c := range got {
		paths = append(paths, c.Path)
	}
	want := []string{"KAUNAS-GARAGE", "VILNIUS"}
	if !slices.Equal(paths, want) {
		t.Errorf("Children(\"\")\n got %q\nwant %q (the sites, in path order)", paths, want)
	}
}

func TestChildrenOfANodeAreOnlyItsDirectChildren(t *testing.T) {
	r, _ := newRepo(t)
	fourDeep(t, r)
	insert(t, r, "bld2", "site", core.KindBuilding, "B2", "KAUNAS-GARAGE/B2")

	got, err := r.Children(context.Background(), "site")
	if err != nil {
		t.Fatalf("Children: %v", err)
	}
	var paths []string
	for _, c := range got {
		paths = append(paths, c.Path)
	}
	want := []string{"KAUNAS-GARAGE/B1", "KAUNAS-GARAGE/B2"}
	if !slices.Equal(paths, want) {
		t.Errorf("Children(site)\n got %q\nwant %q — grandchildren must not appear", paths, want)
	}
}

func TestAllLocationsAreOrderedByPath(t *testing.T) {
	r, _ := newRepo(t)
	// Inserted out of order on purpose: path order is what a picker renders.
	insert(t, r, "site2", "", core.KindSite, "VILNIUS", "VILNIUS")
	fourDeep(t, r)
	insert(t, r, "bin2", "shelf", core.KindBin, "A000001", "KAUNAS-GARAGE/B1/R1/S3/A000001")

	want := []string{
		"KAUNAS-GARAGE",
		"KAUNAS-GARAGE/B1",
		"KAUNAS-GARAGE/B1/R1",
		"KAUNAS-GARAGE/B1/R1/S3",
		"KAUNAS-GARAGE/B1/R1/S3/A000001",
		"KAUNAS-GARAGE/B1/R1/S3/A000005",
		"VILNIUS",
	}
	if got := pathsOf(t, r); !slices.Equal(got, want) {
		t.Errorf("AllLocations\n got %q\nwant %q", got, want)
	}
}

func TestUpdateLocationWritesTheMutableFields(t *testing.T) {
	r, _ := newRepo(t)
	ctx := context.Background()
	fourDeep(t, r)

	shelf, err := r.Location(ctx, "shelf")
	if err != nil {
		t.Fatalf("Location: %v", err)
	}
	shelf.Label = "by the window"
	shelf.Custodian = "Rasa"
	if err := r.UpdateLocation(ctx, shelf); err != nil {
		t.Fatalf("UpdateLocation: %v", err)
	}

	got, err := r.Location(ctx, "shelf")
	if err != nil {
		t.Fatalf("Location: %v", err)
	}
	if got.Label != "by the window" || got.Custodian != "Rasa" {
		t.Errorf("after UpdateLocation label=%q custodian=%q, want %q and %q",
			got.Label, got.Custodian, "by the window", "Rasa")
	}
}

func TestUpdateAndDeleteReportAMissingRow(t *testing.T) {
	r, _ := newRepo(t)
	ctx := context.Background()
	ghost := core.Location{ID: "ghost", Kind: core.KindSite, Code: "X", Path: "X"}

	if err := r.UpdateLocation(ctx, ghost); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("UpdateLocation of an unknown id = %v, want core.ErrNotFound", err)
	}
	if err := r.DeleteLocation(ctx, "ghost"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("DeleteLocation of an unknown id = %v, want core.ErrNotFound", err)
	}
}
