package core

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// draft returns the minimum an intake produces: a photo and a title, no price.
func draft() Offer {
	return Offer{
		ID: "o1", SKU: "SKU-1", Title: "Vintage lamp",
		Status: StatusDraft, Quantity: 1,
		Photos: []Photo{{ID: "11111111-1111-1111-1111-111111111111", ContentType: "image/jpeg"}},
	}
}

// TestADraftNeedsNoPrice is the intake flow: photograph, title, shelve — price
// later, after finding out what the thing can fetch. Requiring a price here
// would make the operator invent one.
func TestADraftNeedsNoPrice(t *testing.T) {
	o := draft()
	if err := o.Validate(); err != nil {
		t.Fatalf("a photographed, titled, unpriced draft was refused: %v", err)
	}
	if !o.NeedsPricing() {
		t.Error("an unpriced draft should appear in the pricing queue")
	}
}

// TestListingWithoutAPriceIsRefused is the other half of the same rule: the
// price may be deferred, but not past the point where it reaches a marketplace.
func TestListingWithoutAPriceIsRefused(t *testing.T) {
	for _, st := range []Status{StatusListed, StatusPending} {
		o := draft()
		o.Status = st
		err := o.Validate()
		if err == nil {
			t.Errorf("status %s was accepted with no shop price; a 0.00 listing would be "+
				"published", st)
			continue
		}
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("status %s: got %v, want ErrInvalid", st, err)
		}
	}
}

func TestPricedOfferLeavesThePricingQueue(t *testing.T) {
	o := draft()
	o.Shop = Money{Minor: 4500, Currency: "EUR"}
	if o.NeedsPricing() {
		t.Error("a priced draft is still showing as awaiting pricing")
	}
}

// TestMarginIsShopLessOwner pins the two-price model: what we publish, and what
// the holder wants, are separate figures and the difference is what the business
// keeps.
func TestMarginIsShopLessOwner(t *testing.T) {
	o := draft()
	o.Shop = Money{Minor: 4500, Currency: "EUR"}
	o.Owner = Money{Minor: 3000, Currency: "EUR"}

	got, err := o.Margin()
	if err != nil {
		t.Fatalf("Margin: %v", err)
	}
	if got.Minor != 1500 || got.Currency != "EUR" {
		t.Errorf("Margin = %+v, want 1500 EUR", got)
	}
}

func TestMarginWithNoOwnerShareIsTheWholePrice(t *testing.T) {
	o := draft()
	o.Shop = Money{Minor: 4500, Currency: "EUR"}

	got, err := o.Margin()
	if err != nil {
		t.Fatalf("Margin: %v", err)
	}
	if got.Minor != 4500 {
		t.Errorf("Margin = %+v, want the whole 4500 EUR when nothing is owed", got)
	}
}

// TestMarginRefusesToSubtractAcrossCurrencies keeps the exchange-rate assumption
// out of a figure nobody could reproduce.
func TestMarginRefusesToSubtractAcrossCurrencies(t *testing.T) {
	o := draft()
	o.Shop = Money{Minor: 4500, Currency: "EUR"}
	o.Owner = Money{Minor: 3000, Currency: "USD"}

	if _, err := o.Margin(); err == nil {
		t.Fatal("EUR and USD were subtracted from each other without a rate")
	}
}

func TestMarginNeedsAShopPrice(t *testing.T) {
	o := draft()
	o.Owner = Money{Minor: 3000, Currency: "EUR"}
	if _, err := o.Margin(); err == nil {
		t.Fatal("a margin was reported for an offer with no shop price")
	}
}

// TestRealisedUsesTheSoldPriceNotTheShopPrice is why Sold is stored separately:
// an item discounted to move still owes the holder the same amount.
func TestRealisedUsesTheSoldPriceNotTheShopPrice(t *testing.T) {
	when := time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)
	o := draft()
	o.Status = StatusSold
	o.Shop = Money{Minor: 4500, Currency: "EUR"}
	o.Owner = Money{Minor: 3000, Currency: "EUR"}
	o.Sold = Money{Minor: 3800, Currency: "EUR"} // haggled down
	o.SoldAt = &when

	if err := o.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	got, err := o.Realised()
	if err != nil {
		t.Fatalf("Realised: %v", err)
	}
	if got.Minor != 800 {
		t.Errorf("Realised = %+v, want 800 EUR (3800 sold - 3000 owed), not the "+
			"1500 the shop price would have implied", got)
	}
}

// TestSoldNeedsADate is what makes a period report possible at all.
func TestSoldNeedsADate(t *testing.T) {
	o := draft()
	o.Status = StatusSold
	o.Shop = Money{Minor: 4500, Currency: "EUR"}
	o.Sold = Money{Minor: 4500, Currency: "EUR"}

	if err := o.Validate(); err == nil {
		t.Fatal("a sold offer with no sale date was accepted; its revenue belongs to no period")
	}
}

// TestOnlyLiveStatusesAreExported keeps drafts and withdrawn stock out of a
// marketplace feed.
func TestOnlyLiveStatusesAreExported(t *testing.T) {
	want := map[Status]bool{
		StatusDraft: false, StatusListed: true, StatusPending: true,
		StatusSold: false, StatusArchived: false,
	}
	for st, w := range want {
		if got := st.Exportable(); got != w {
			t.Errorf("%s.Exportable() = %v, want %v", st, got, w)
		}
	}
}

// TestPhotoPublicURLIsAbsolute is the property the whole export depends on:
// Shopify and eBay fetch the image from their own servers, so a relative path
// produces a listing with no pictures and no error message.
func TestPhotoPublicURLIsAbsolute(t *testing.T) {
	p := Photo{ID: "abc", ContentType: "image/jpeg"}
	got := p.PublicURL("https://warehouse.example.com/")
	want := "https://warehouse.example.com/p/abc.jpg"
	if got != want {
		t.Errorf("PublicURL = %q, want %q", got, want)
	}
	if p.PublicPath() != "/p/abc.jpg" {
		t.Errorf("PublicPath = %q", p.PublicPath())
	}
}

func TestPhotoExtFollowsContentType(t *testing.T) {
	cases := map[string]string{
		"image/jpeg": ".jpg", "image/png": ".png",
		"image/webp": ".webp", "image/gif": ".gif",
	}
	for ct, want := range cases {
		if got := (Photo{ContentType: ct}).Ext(); got != want {
			t.Errorf("Ext(%q) = %q, want %q", ct, got, want)
		}
	}
}

// TestADraftNeedsNoTitle is ADR-019, and it is ADR-004's rule one field over.
//
// One person walks the warehouse photographing things; a second person names and
// describes them afterwards. The photographer is holding an object they often
// cannot identify, so requiring a title before they may take a picture is exactly
// what makes somebody type "lamp 2" — and unlike a placeholder price, which both
// the domain and a CHECK constraint refuse, a placeholder TITLE would sail
// through to a marketplace as though it were real.
func TestADraftNeedsNoTitle(t *testing.T) {
	o := draft()
	o.Title = ""

	if err := o.Validate(); err != nil {
		t.Fatalf("a photographed, unnamed draft was refused: %v", err)
	}
	if !o.NeedsDescribing() {
		t.Error("an untitled draft should appear in the describing queue")
	}

	// A title of nothing but spaces is not a title. If the predicate and the
	// repository clause disagreed about that, the queue's count and its contents
	// would differ by exactly the rows somebody had pressed space in.
	o.Title = "   "
	if !o.NeedsDescribing() {
		t.Error("a whitespace-only title should count as undescribed, as it does in SQL")
	}

	// And the queue must empty as the work is done, or it is a list nobody can
	// finish.
	o.Title = "Vintage brass desk lamp"
	if o.NeedsDescribing() {
		t.Error("a named draft should have left the describing queue")
	}
}

// TestAPublishedOfferStillNeedsATitle is the other half of ADR-019: the rule did
// not disappear, it moved to the boundary where somebody outside the building
// reads the value.
func TestAPublishedOfferStillNeedsATitle(t *testing.T) {
	for _, st := range []Status{StatusListed, StatusPending} {
		o := draft()
		o.Title = ""
		o.Status = st
		o.Shop = Money{Minor: 4500, Currency: "EUR"}

		err := o.Validate()
		if err == nil {
			t.Errorf("%s: an untitled offer was accepted into a status that is exported", st)
			continue
		}
		if !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: refused with %v, want ErrInvalid so a handler can turn it into a "+
				"sentence rather than a 500", st, err)
		}
		if !strings.Contains(err.Error(), "title") {
			t.Errorf("%s: the refusal does not mention the title, so the operator is told "+
				"something is wrong without being told what: %v", st, err)
		}
	}

	// The draft path must stay open, or this test would pass with the old
	// unconditional rule restored.
	o := draft()
	o.Title = ""
	if err := o.Validate(); err != nil {
		t.Fatalf("the draft case regressed, so the check above proves nothing: %v", err)
	}
}

// TestHoldReasonNamesAMissingTitle keeps an untitled offer out of an export WITH
// A REASON rather than silently.
//
// The row is constructed directly because no supported write path can produce it
// — Validate and a database trigger both refuse to publish an untitled offer, so
// this is the third gate, for a row that arrived some other way. The cart package
// already builds such rows for the same purpose.
func TestHoldReasonNamesAMissingTitle(t *testing.T) {
	o := draft()
	o.Title = ""
	o.Status = StatusListed
	o.Shop = Money{Minor: 4500, Currency: "EUR"}

	if got := HoldReason(o); got != "no title yet" {
		t.Errorf("HoldReason = %q, want %q — an export that drops the row instead of "+
			"explaining it is the failure the \"not being sent\" table exists to prevent", got, "no title yet")
	}

	o.Title = "Vintage brass desk lamp"
	if got := HoldReason(o); got != "" {
		t.Errorf("HoldReason = %q on a complete offer, want \"\"; the case above would then "+
			"be holding everything back", got)
	}
}

// TestADR004NoLongerClaimsValidateRequiresATitle reads a decision record from a
// Go test, which is unusual and deliberate.
//
// ADR-004 said "Offer.Validate() requires a title and a SKU". ADR-019 makes the
// title half false. This corpus has already shipped a record asserting something
// untrue for a day — ADR-013, corrected 2026-09-07 — and the cost was a session
// planning against it. Correcting a document is otherwise a step no gate can see,
// which is exactly the class of step that silently does not happen.
func TestADR004NoLongerClaimsValidateRequiresATitle(t *testing.T) {
	const path = "../../docs/adr/ADR-004-an-unpriced-draft-is-a-normal-state.md"

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read %s: %v — if the record was renamed this test must be "+
			"repointed, not deleted", path, err)
	}
	body := string(b)

	// The precondition: without it, a renamed heading would make the assertion
	// below pass against a file that says nothing at all.
	if !strings.Contains(body, "Offer.Validate()") {
		t.Fatalf("%s no longer mentions Offer.Validate() at all, so this test is asserting "+
			"nothing about what it claims", path)
	}
	// ⚠ A CORRECTION IS ALLOWED TO QUOTE THE SENTENCE IT RETIRES — that is how a
	// reader learns what changed, and this test failed on its own correction the
	// first time it ran. What must not survive is an UNMARKED occurrence, because
	// that is the one somebody reads as though it still held.
	const retired = "requires a title and a SKU"
	for i := 0; ; {
		j := strings.Index(body[i:], retired)
		if j < 0 {
			break
		}
		at := i + j
		i = at + len(retired)

		from := at - 500
		if from < 0 {
			from = 0
		}
		if !strings.Contains(body[from:at], "CORRECTED") {
			t.Errorf("%s repeats %q with no correction marker above it, so a reader meets "+
				"the retired rule as though it were current. ADR-019 moved the title to the "+
				"exportable boundary", path, retired)
		}
	}
	if !strings.Contains(body, "ADR-019") {
		t.Errorf("%s does not point at ADR-019, so a reader has no way to find the record "+
			"that superseded this part of it", path)
	}
}
