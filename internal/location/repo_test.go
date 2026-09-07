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
	"github.com/atvirokodosprendimai/app-warehousev1/internal/store"
	"github.com/atvirokodosprendimai/app-warehousev1/migrations"
)

// newRepo opens a private database, runs the REAL migrations over it, and
// returns a Repo wired the way the application wires one: a query_only reader
// and an immediate-locking writer over the same file. The reader is query_only
// on purpose — a read method that reached for the write handle would still pass,
// but a write that reached for the read handle is refused by the driver, which
// is the direction that can corrupt the tree.
//
// The write handle is returned as well, for the fixtures that have to insert
// rows this package does not own.
//
// ⚠ IT USED TO CARRY A COPY OF `locations` AND A CUT-DOWN `offers`, "copied so
// the tests do not depend on a migration runner". The `offers` copy had eight
// columns; the real table has more than twenty. That is drift of exactly the
// kind ADR-013 was written about, and it had already happened here: the copy and
// the tests using it agreed with each other perfectly while describing a table
// production does not have. A copied schema cannot detect its own drift, because
// it defines the thing the test then measures.
func newRepo(t *testing.T) (*Repo, *sql.DB) {
	t.Helper()

	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := store.Migrate(db.Write, migrations.FS); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	return NewRepo(db.Read, db.Write), db.Write
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
