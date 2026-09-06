package core

import "testing"

// TestWhereInheritsFromTheNearestAncestor is the rule that makes an off-site
// warehouse workable: the custodian and city are stated once on the site and
// every shelf beneath answers with them.
func TestWhereInheritsFromTheNearestAncestor(t *testing.T) {
	site := Location{
		Kind: KindSite, Code: "KAUNAS-GARAGE", Path: "KAUNAS-GARAGE",
		Custodian: "Jonas", CustodianContact: "+37060000000",
		City: "Kaunas", Country: "LT",
	}
	shelf := Location{Kind: KindShelf, Code: "S3", Path: "KAUNAS-GARAGE/S3"}
	bin := Location{Kind: KindBin, Code: "A000005", Path: "KAUNAS-GARAGE/S3/A000005"}

	got := bin.Where([]Location{site, shelf})
	if got.City != "Kaunas" || got.Country != "LT" {
		t.Errorf("city/country did not inherit: %+v", got)
	}
	if got.Custodian != "Jonas" || got.CustodianContact != "+37060000000" {
		t.Errorf("custodian did not inherit: %+v", got)
	}
	if !got.Offsite() {
		t.Error("a bin in someone else's garage should report as off-site")
	}
	if got.Summary() != "Kaunas · held by Jonas" {
		t.Errorf("Summary() = %q", got.Summary())
	}
}

// TestWhereNearerCustodianWinsWithItsOwnContact pins the half that is easy to
// get wrong: taking a nearer custodian's NAME while still inheriting the further
// one's phone number would produce a contact that reaches the wrong person.
func TestWhereNearerCustodianWinsWithItsOwnContact(t *testing.T) {
	site := Location{
		Kind: KindSite, Code: "VILNIUS", Path: "VILNIUS",
		Custodian: "Ana", CustodianContact: "ana@example.com",
		City: "Vilnius", Country: "LT",
	}
	room := Location{
		Kind: KindRoom, Code: "R2", Path: "VILNIUS/R2",
		Custodian: "Petras", CustodianContact: "petras@example.com",
	}
	bin := Location{Kind: KindBin, Code: "B1", Path: "VILNIUS/R2/B1"}

	got := bin.Where([]Location{site, room})
	if got.Custodian != "Petras" {
		t.Errorf("nearer custodian did not win: %q", got.Custodian)
	}
	if got.CustodianContact != "petras@example.com" {
		t.Errorf("contact came from the wrong custodian: %q — this reaches the wrong person",
			got.CustodianContact)
	}
	// City still inherits: only the custodian was overridden.
	if got.City != "Vilnius" {
		t.Errorf("city = %q, want Vilnius", got.City)
	}
}

// TestWhereOnOwnPremisesIsNotOffsite guards the default case, where nobody set a
// custodian because the operator holds the stock themselves.
func TestWhereOnOwnPremisesIsNotOffsite(t *testing.T) {
	site := Location{Kind: KindSite, Code: "HOME", Path: "HOME", City: "Kaunas", Country: "LT"}
	bin := Location{Kind: KindBin, Code: "A000005", Path: "HOME/A000005"}

	got := bin.Where([]Location{site})
	if got.Offsite() {
		t.Error("a bin on your own premises must not report as off-site")
	}
	if got.Summary() != "Kaunas" {
		t.Errorf("Summary() = %q, want just the city", got.Summary())
	}
}

func TestValidateRejectsAnImpossibleTree(t *testing.T) {
	shelf := &Location{Kind: KindShelf, Code: "S1", Path: "X/S1"}

	// A building inside a shelf is upside down.
	building := &Location{Kind: KindBuilding, Code: "B1"}
	if err := building.Validate(shelf); err == nil {
		t.Error("a building was allowed inside a shelf")
	}
	// A site is a root and cannot be nested.
	site := &Location{Kind: KindSite, Code: "S", City: "Kaunas"}
	if err := site.Validate(shelf); err == nil {
		t.Error("a site was allowed inside a shelf")
	}
	// Skipping levels is legitimate: a small warehouse is a site holding bins.
	bin := &Location{Kind: KindBin, Code: "A000005"}
	root := &Location{Kind: KindSite, Code: "HOME", Path: "HOME", City: "Kaunas"}
	if err := bin.Validate(root); err != nil {
		t.Errorf("a bin directly inside a site was refused: %v", err)
	}
}

func TestValidateRequiresACityOnASite(t *testing.T) {
	site := &Location{Kind: KindSite, Code: "SOMEWHERE"}
	if err := site.Validate(nil); err == nil {
		t.Error("a site with no city was accepted; a site in another town is the whole " +
			"reason the field exists")
	}
}

func TestValidateRejectsASlashInACode(t *testing.T) {
	// The path is slash-separated, so a slash in a code would split it and two
	// different nodes could materialise the same path.
	bin := &Location{Kind: KindBin, Code: "A/5"}
	root := &Location{Kind: KindSite, Code: "HOME", Path: "HOME", City: "Kaunas"}
	if err := bin.Validate(root); err == nil {
		t.Error("a code containing a slash was accepted")
	}
}

func TestChildPath(t *testing.T) {
	root := &Location{Path: "KAUNAS"}
	if got := root.ChildPath("R1"); got != "KAUNAS/R1" {
		t.Errorf("ChildPath = %q", got)
	}
	var none *Location
	if got := none.ChildPath("KAUNAS"); got != "KAUNAS" {
		t.Errorf("a root's path should be its bare code, got %q", got)
	}
}
