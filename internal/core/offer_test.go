package core

import (
	"errors"
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
