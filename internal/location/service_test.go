package location

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"testing"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
)

// newService returns a Service over a private database, with the repo and the
// write handle for the assertions and fixtures that need them.
func newService(t *testing.T) (*Service, *Repo, *sql.DB) {
	t.Helper()
	r, write := newRepo(t)
	return NewService(r), r, write
}

// create is Create with the error turned into a fatal, for arranging a tree.
func create(t *testing.T, s *Service, parentID string, l core.Location) core.Location {
	t.Helper()
	got, err := s.Create(context.Background(), parentID, l)
	if err != nil {
		t.Fatalf("create %q under %q: %v", l.Code, parentID, err)
	}
	return got
}

// site is the fixture site, held by someone else in another city — the case the
// custodian and city fields exist for.
func site(t *testing.T, s *Service, code string) core.Location {
	t.Helper()
	return create(t, s, "", core.Location{
		Kind:             core.KindSite,
		Code:             code,
		Custodian:        "Jonas",
		CustodianContact: "+37060000000",
		City:             "Kaunas",
		Country:          "LT",
	})
}

func TestCreateGivesASiteItsBareCodeAsAPath(t *testing.T) {
	s, _, _ := newService(t)

	got := site(t, s, "KAUNAS-GARAGE")

	if got.Path != "KAUNAS-GARAGE" {
		t.Errorf("site path = %q, want %q", got.Path, "KAUNAS-GARAGE")
	}
	if got.ID == "" {
		t.Error("Create left the id empty")
	}
	if got.CreatedAt.IsZero() || got.CreatedAt.Location() != nil && got.CreatedAt.Location().String() != "UTC" {
		t.Errorf("CreatedAt = %v in %v, want a UTC stamp", got.CreatedAt, got.CreatedAt.Location())
	}
}

func TestCreateComposesTheChildPathFromTheParent(t *testing.T) {
	s, _, _ := newService(t)
	root := site(t, s, "KAUNAS-GARAGE")
	room := create(t, s, root.ID, core.Location{Kind: core.KindRoom, Code: "R1"})

	// A tiny warehouse skips levels: a bin may hang straight off a room.
	bin := create(t, s, room.ID, core.Location{Kind: core.KindBin, Code: "A000005"})

	if want := "KAUNAS-GARAGE/R1"; room.Path != want {
		t.Errorf("room path = %q, want %q", room.Path, want)
	}
	if want := "KAUNAS-GARAGE/R1/A000005"; bin.Path != want {
		t.Errorf("bin path = %q, want %q", bin.Path, want)
	}
	if bin.ParentID != room.ID {
		t.Errorf("bin parent = %q, want the room %q", bin.ParentID, room.ID)
	}
}

func TestCreateRefusesADuplicateCodeUnderTheSameParent(t *testing.T) {
	s, _, _ := newService(t)
	root := site(t, s, "KAUNAS-GARAGE")
	create(t, s, root.ID, core.Location{Kind: core.KindRoom, Code: "R1"})

	_, err := s.Create(context.Background(), root.ID, core.Location{Kind: core.KindRoom, Code: "R1"})

	if !errors.Is(err, ErrCodeTaken) {
		t.Fatalf("second R1 under the same site = %v, want ErrCodeTaken", err)
	}
}

func TestCreateAllowsTheSameCodeUnderDifferentParents(t *testing.T) {
	s, _, _ := newService(t)
	a := site(t, s, "KAUNAS-GARAGE")
	b := site(t, s, "VILNIUS-UNIT")

	create(t, s, a.ID, core.Location{Kind: core.KindShelf, Code: "S3"})
	got := create(t, s, b.ID, core.Location{Kind: core.KindShelf, Code: "S3"})

	if want := "VILNIUS-UNIT/S3"; got.Path != want {
		t.Errorf("second S3 path = %q, want %q — a code is unique among siblings only", got.Path, want)
	}
}

func TestCreateRefusesASiteWhoseCodeIsAlreadyASitePath(t *testing.T) {
	s, _, _ := newService(t)
	site(t, s, "KAUNAS-GARAGE")

	// UNIQUE(parent_id, code) cannot catch this: a site's parent_id is NULL and
	// SQL counts every NULL as distinct, so UNIQUE(path) is the only guard at the
	// root and the refusal arrives as ErrPathTaken.
	_, err := s.Create(context.Background(), "", core.Location{
		Kind: core.KindSite, Code: "KAUNAS-GARAGE", City: "Kaunas",
	})

	if !errors.Is(err, ErrPathTaken) {
		t.Fatalf("second site called KAUNAS-GARAGE = %v, want ErrPathTaken", err)
	}
}

func TestCreateRefusesWhatTheDomainRefuses(t *testing.T) {
	s, _, _ := newService(t)
	root := site(t, s, "KAUNAS-GARAGE")
	shelf := create(t, s, root.ID, core.Location{Kind: core.KindShelf, Code: "S3"})

	cases := map[string]struct {
		parentID string
		l        core.Location
	}{
		"a building with no site above it": {"", core.Location{Kind: core.KindBuilding, Code: "B1", City: "Kaunas"}},
		"a site with no city":              {"", core.Location{Kind: core.KindSite, Code: "NOWHERE"}},
		"a site inside something":          {root.ID, core.Location{Kind: core.KindSite, Code: "INNER", City: "Kaunas"}},
		"a room inside a shelf":            {shelf.ID, core.Location{Kind: core.KindRoom, Code: "R1"}},
		"a code carrying a separator":      {root.ID, core.Location{Kind: core.KindBin, Code: "A/5"}},
		"a code that is blank":             {root.ID, core.Location{Kind: core.KindBin, Code: "  "}},
		"a kind nobody defined":            {root.ID, core.Location{Kind: core.Kind("drawer"), Code: "D1"}},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := s.Create(context.Background(), tc.parentID, tc.l); !errors.Is(err, core.ErrInvalid) {
				t.Errorf("Create(%s) = %v, want core.ErrInvalid", name, err)
			}
		})
	}
}

func TestCreateReportsAMissingParent(t *testing.T) {
	s, _, _ := newService(t)

	_, err := s.Create(context.Background(), "ghost", core.Location{Kind: core.KindBin, Code: "A000005"})

	if !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("Create under an unknown parent = %v, want core.ErrNotFound", err)
	}
}

func TestRenameReAddressesTheWholeSubtree(t *testing.T) {
	s, r, _ := newService(t)
	root := site(t, s, "KAUNAS-GARAGE")
	room := create(t, s, root.ID, core.Location{Kind: core.KindRoom, Code: "R1"})
	shelf := create(t, s, room.ID, core.Location{Kind: core.KindShelf, Code: "S3"})
	bin := create(t, s, shelf.ID, core.Location{Kind: core.KindBin, Code: "A000005"})

	if err := s.Rename(context.Background(), room.ID, "R2"); err != nil {
		t.Fatalf("Rename: %v", err)
	}

	want := []string{
		"KAUNAS-GARAGE",
		"KAUNAS-GARAGE/R2",
		"KAUNAS-GARAGE/R2/S3",
		"KAUNAS-GARAGE/R2/S3/A000005",
	}
	if got := pathsOf(t, r); !slices.Equal(got, want) {
		t.Errorf("after renaming R1 to R2\n got %q\nwant %q", got, want)
	}

	// The ids are untouched, which is the point: stock points at the bin's id, so
	// a re-labelled room must not lose track of what is on its shelves.
	got, err := r.Location(context.Background(), bin.ID)
	if err != nil {
		t.Fatalf("the bin lost its id: %v", err)
	}
	if got.Path != "KAUNAS-GARAGE/R2/S3/A000005" {
		t.Errorf("bin path = %q, want the re-addressed one", got.Path)
	}
}

// TestRenameTreatsAnUnderscoreAsALetterNotAWildcard is the test the whole
// prefix-matching decision exists for. "A_B" and "AXB" are two unrelated sites;
// "_" is a single-character wildcard in SQL LIKE, so a prefix match built by
// concatenating the old path into a LIKE pattern rewrites AXB's subtree too —
// silently, since both statements succeed.
func TestRenameTreatsAnUnderscoreAsALetterNotAWildcard(t *testing.T) {
	s, r, _ := newService(t)

	underscore := site(t, s, "A_B")
	uShelf := create(t, s, underscore.ID, core.Location{Kind: core.KindShelf, Code: "S1"})
	create(t, s, uShelf.ID, core.Location{Kind: core.KindBin, Code: "A000001"})

	// AXB's codes differ from A_B's on purpose: were they the same, a wildcard
	// match would collide on UNIQUE(path) and fail loudly. Distinct codes let the
	// wrong rewrite SUCCEED, which is the failure this test has to catch.
	wildcard := site(t, s, "AXB")
	wShelf := create(t, s, wildcard.ID, core.Location{Kind: core.KindShelf, Code: "S2"})
	create(t, s, wShelf.ID, core.Location{Kind: core.KindBin, Code: "A000002"})

	if err := s.Rename(context.Background(), underscore.ID, "A-B"); err != nil {
		t.Fatalf("Rename: %v", err)
	}

	want := []string{
		"A-B",
		"A-B/S1",
		"A-B/S1/A000001",
		"AXB",
		"AXB/S2",
		"AXB/S2/A000002",
	}
	if got := pathsOf(t, r); !slices.Equal(got, want) {
		t.Errorf("renaming A_B must not touch AXB\n got %q\nwant %q", got, want)
	}
}

// TestRenameReAddressesANonASCIISubtree pins the rune arithmetic: SQLite's
// substr and length count characters, so a byte-length offset would slice a
// multi-byte site code short of its separator and match none of its children.
func TestRenameReAddressesANonASCIISubtree(t *testing.T) {
	s, r, _ := newService(t)
	root := site(t, s, "ŠIAULIAI")
	shelf := create(t, s, root.ID, core.Location{Kind: core.KindShelf, Code: "S1"})
	create(t, s, shelf.ID, core.Location{Kind: core.KindBin, Code: "A000005"})

	if err := s.Rename(context.Background(), root.ID, "ŠIAULIAI-2"); err != nil {
		t.Fatalf("Rename: %v", err)
	}

	want := []string{"ŠIAULIAI-2", "ŠIAULIAI-2/S1", "ŠIAULIAI-2/S1/A000005"}
	if got := pathsOf(t, r); !slices.Equal(got, want) {
		t.Errorf("after renaming a non-ASCII site\n got %q\nwant %q", got, want)
	}
}

func TestRenameRefusesACodeAlreadyUsedBySibling(t *testing.T) {
	s, r, _ := newService(t)
	root := site(t, s, "KAUNAS-GARAGE")
	create(t, s, root.ID, core.Location{Kind: core.KindRoom, Code: "R1"})
	second := create(t, s, root.ID, core.Location{Kind: core.KindRoom, Code: "R2"})

	err := s.Rename(context.Background(), second.ID, "R1")

	if !errors.Is(err, ErrCodeTaken) {
		t.Fatalf("renaming R2 onto R1 = %v, want ErrCodeTaken", err)
	}
	want := []string{"KAUNAS-GARAGE", "KAUNAS-GARAGE/R1", "KAUNAS-GARAGE/R2"}
	if got := pathsOf(t, r); !slices.Equal(got, want) {
		t.Errorf("a refused rename must change nothing\n got %q\nwant %q", got, want)
	}
}

func TestRenameRefusesACodeCarryingASeparator(t *testing.T) {
	s, _, _ := newService(t)
	root := site(t, s, "KAUNAS-GARAGE")
	room := create(t, s, root.ID, core.Location{Kind: core.KindRoom, Code: "R1"})

	err := s.Rename(context.Background(), room.ID, "R1/R2")

	if !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("renaming to a code with a slash = %v, want core.ErrInvalid", err)
	}
}

func TestMoveReAddressesTheSubtreeUnderItsNewParent(t *testing.T) {
	s, r, _ := newService(t)
	root := site(t, s, "KAUNAS-GARAGE")
	from := create(t, s, root.ID, core.Location{Kind: core.KindRoom, Code: "R1"})
	to := create(t, s, root.ID, core.Location{Kind: core.KindRoom, Code: "R2"})
	shelf := create(t, s, from.ID, core.Location{Kind: core.KindShelf, Code: "S3"})
	create(t, s, shelf.ID, core.Location{Kind: core.KindBin, Code: "A000005"})

	if err := s.Move(context.Background(), shelf.ID, to.ID); err != nil {
		t.Fatalf("Move: %v", err)
	}

	want := []string{
		"KAUNAS-GARAGE",
		"KAUNAS-GARAGE/R1",
		"KAUNAS-GARAGE/R2",
		"KAUNAS-GARAGE/R2/S3",
		"KAUNAS-GARAGE/R2/S3/A000005",
	}
	if got := pathsOf(t, r); !slices.Equal(got, want) {
		t.Errorf("after moving S3 from R1 to R2\n got %q\nwant %q", got, want)
	}

	moved, err := r.Location(context.Background(), shelf.ID)
	if err != nil {
		t.Fatalf("Location: %v", err)
	}
	if moved.ParentID != to.ID {
		t.Errorf("moved shelf parent = %q, want %q", moved.ParentID, to.ID)
	}
}

func TestMoveRefusesToPutANodeInsideItself(t *testing.T) {
	s, r, _ := newService(t)
	root := site(t, s, "KAUNAS-GARAGE")
	room := create(t, s, root.ID, core.Location{Kind: core.KindRoom, Code: "R1"})
	shelf := create(t, s, room.ID, core.Location{Kind: core.KindShelf, Code: "S3"})

	cases := map[string]struct{ id, newParent string }{
		"into its own child":      {room.ID, shelf.ID},
		"into its own grandchild": {root.ID, shelf.ID},
		"into itself":             {room.ID, room.ID},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if err := s.Move(context.Background(), tc.id, tc.newParent); !errors.Is(err, ErrCycle) {
				t.Errorf("Move %s = %v, want ErrCycle", name, err)
			}
		})
	}

	want := []string{"KAUNAS-GARAGE", "KAUNAS-GARAGE/R1", "KAUNAS-GARAGE/R1/S3"}
	if got := pathsOf(t, r); !slices.Equal(got, want) {
		t.Errorf("a refused move must change nothing\n got %q\nwant %q", got, want)
	}
}

// TestMoveAllowsATargetWhosePathMerelyStartsWithTheNodes pins the separator in
// the cycle check. A bin "A" and a shelf "AB" under one site give paths "K/A"
// and "K/AB": the second starts with the first as TEXT, but the bin is not
// above the shelf. Comparing the bare path instead of the path plus a separator
// refuses this legitimate move as a cycle.
func TestMoveAllowsATargetWhosePathMerelyStartsWithTheNodes(t *testing.T) {
	s, r, _ := newService(t)
	root := site(t, s, "K")
	bin := create(t, s, root.ID, core.Location{Kind: core.KindBin, Code: "A"})
	shelf := create(t, s, root.ID, core.Location{Kind: core.KindShelf, Code: "AB"})

	if err := s.Move(context.Background(), bin.ID, shelf.ID); err != nil {
		t.Fatalf("moving bin K/A onto shelf K/AB: %v", err)
	}

	want := []string{"K", "K/AB", "K/AB/A"}
	if got := pathsOf(t, r); !slices.Equal(got, want) {
		t.Errorf("after the move\n got %q\nwant %q", got, want)
	}
}

func TestMoveRefusesAKindThatCannotSitThere(t *testing.T) {
	s, _, _ := newService(t)
	root := site(t, s, "KAUNAS-GARAGE")
	room := create(t, s, root.ID, core.Location{Kind: core.KindRoom, Code: "R1"})
	other := site(t, s, "VILNIUS-UNIT")
	bin := create(t, s, other.ID, core.Location{Kind: core.KindBin, Code: "A000005"})

	// A room inside a bin inverts the hierarchy, and the bin is in another tree
	// so this is refused on kind order, not as a cycle.
	if err := s.Move(context.Background(), room.ID, bin.ID); !errors.Is(err, core.ErrInvalid) {
		t.Errorf("moving a room into a bin = %v, want core.ErrInvalid", err)
	}
	// A site is a root: it cannot be given a parent.
	if err := s.Move(context.Background(), other.ID, root.ID); !errors.Is(err, core.ErrInvalid) {
		t.Errorf("moving a site under a site = %v, want core.ErrInvalid", err)
	}
}

func TestMoveRefusesAPromotionThatWouldMakeANonSiteARoot(t *testing.T) {
	s, _, _ := newService(t)
	root := site(t, s, "KAUNAS-GARAGE")
	room := create(t, s, root.ID, core.Location{Kind: core.KindRoom, Code: "R1"})

	if err := s.Move(context.Background(), room.ID, ""); !errors.Is(err, core.ErrInvalid) {
		t.Errorf("promoting a room to a root = %v, want core.ErrInvalid", err)
	}
}

func TestDeleteRefusesANodeThatStillEnclosesLocations(t *testing.T) {
	s, r, _ := newService(t)
	root := site(t, s, "KAUNAS-GARAGE")
	create(t, s, root.ID, core.Location{Kind: core.KindRoom, Code: "R1"})

	err := s.Delete(context.Background(), root.ID)

	if !errors.Is(err, ErrHasChildren) {
		t.Fatalf("deleting a site with a room in it = %v, want ErrHasChildren", err)
	}
	if _, err := r.Location(context.Background(), root.ID); err != nil {
		t.Errorf("the refused site is gone: %v", err)
	}
}

func TestDeleteRefusesALocationThatStillHoldsStock(t *testing.T) {
	s, r, write := newService(t)
	root := site(t, s, "KAUNAS-GARAGE")
	bin := create(t, s, root.ID, core.Location{Kind: core.KindBin, Code: "A000005"})
	if _, err := write.Exec(
		`INSERT INTO offers (id, sku, title, location_id, created_at, updated_at)
		 VALUES ('o1', 'SKU-1', 'A chair', ?, '2026-09-06T12:00:00Z', '2026-09-06T12:00:00Z')`,
		bin.ID); err != nil {
		t.Fatalf("shelve an offer: %v", err)
	}

	err := s.Delete(context.Background(), bin.ID)

	if !errors.Is(err, ErrLocationOccupied) {
		t.Fatalf("deleting an occupied bin = %v, want ErrLocationOccupied", err)
	}
	if _, err := r.Location(context.Background(), bin.ID); err != nil {
		t.Errorf("the refused bin is gone, and the offer with it: %v", err)
	}
}

func TestDeleteRemovesAnEmptyLocation(t *testing.T) {
	s, r, _ := newService(t)
	root := site(t, s, "KAUNAS-GARAGE")
	bin := create(t, s, root.ID, core.Location{Kind: core.KindBin, Code: "A000005"})

	if err := s.Delete(context.Background(), bin.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := r.Location(context.Background(), bin.ID); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("after Delete, Location = %v, want core.ErrNotFound", err)
	}
}

func TestDeleteReportsAMissingLocation(t *testing.T) {
	s, _, _ := newService(t)

	if err := s.Delete(context.Background(), "ghost"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("Delete of an unknown id = %v, want core.ErrNotFound", err)
	}
}

func TestDeleteRefusesAnEmptyID(t *testing.T) {
	s, r, _ := newService(t)
	site(t, s, "KAUNAS-GARAGE")

	// Children("") lists the sites, so an empty id must be refused before it can
	// be read as "this node has every site as a child".
	if err := s.Delete(context.Background(), ""); !errors.Is(err, core.ErrInvalid) {
		t.Errorf("Delete(\"\") = %v, want core.ErrInvalid", err)
	}
	if got := pathsOf(t, r); len(got) != 1 {
		t.Errorf("tree after a refused delete = %q, want the one site", got)
	}
}

func TestPlaceInheritsTheCustodianAndCityFromTheSite(t *testing.T) {
	s, _, _ := newService(t)
	root := site(t, s, "KAUNAS-GARAGE")
	shelf := create(t, s, root.ID, core.Location{Kind: core.KindShelf, Code: "S3"})
	bin := create(t, s, shelf.ID, core.Location{Kind: core.KindBin, Code: "A000005"})

	got, err := s.Place(context.Background(), bin.ID)
	if err != nil {
		t.Fatalf("Place: %v", err)
	}

	want := core.Placement{
		Custodian:        "Jonas",
		CustodianContact: "+37060000000",
		City:             "Kaunas",
		Country:          "LT",
	}
	if got != want {
		t.Errorf("Place\n got %+v\nwant %+v", got, want)
	}
	if !got.Offsite() {
		t.Error("a bin in someone else's garage should report as offsite")
	}
}

func TestPlacePrefersTheNearestCustodian(t *testing.T) {
	s, _, _ := newService(t)
	root := site(t, s, "KAUNAS-GARAGE")
	// One room inside the site is lent by someone else, with their own number.
	room := create(t, s, root.ID, core.Location{
		Kind: core.KindRoom, Code: "R9",
		Custodian: "Rasa", CustodianContact: "rasa@example.lt",
	})
	bin := create(t, s, room.ID, core.Location{Kind: core.KindBin, Code: "A000005"})

	got, err := s.Place(context.Background(), bin.ID)
	if err != nil {
		t.Fatalf("Place: %v", err)
	}

	want := core.Placement{
		Custodian:        "Rasa",
		CustodianContact: "rasa@example.lt",
		City:             "Kaunas",
		Country:          "LT",
	}
	if got != want {
		t.Errorf("Place\n got %+v\nwant %+v — the nearer custodian wins, and the contact travels with them", got, want)
	}
}

func TestPlaceReportsAMissingLocation(t *testing.T) {
	s, _, _ := newService(t)

	if _, err := s.Place(context.Background(), "ghost"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("Place of an unknown id = %v, want core.ErrNotFound", err)
	}
}
