package export

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
)

// wantEBayHeader is the File Exchange header verbatim for a EUR run, kept as one
// string rather than rebuilt from ebayHeader so a typo in the source cannot
// agree with a matching typo in the test.
const wantEBayHeader = "*Action(SiteID=US|Country=US|Currency=EUR|Version=1193),CustomLabel," +
	"*Category,*Title,Description,PicURL,*Quantity,*StartPrice,*ConditionID,*Format,*Duration," +
	"*Location,*ReturnsAcceptedOption"

// Columns the tests below address by name instead of by counting commas.
const (
	ebPicURL      = 5
	ebConditionID = 8
)

func TestEBayHeaderMatchesTheFileExchangeSpec(t *testing.T) {
	out := render(t, EBay{}, []core.Offer{testOffer("LAMP-01", 1)}, testOptions())
	if got := headerLine(out); got != wantEBayHeader {
		t.Errorf("header =\n%q\nwant\n%q", got, wantEBayHeader)
	}
}

func TestEBayHeaderCurrencyFollowsOptions(t *testing.T) {
	// The currency is a token inside the *Action header rather than a column,
	// so a hardcoded one sells a EUR catalogue in dollars at the same numbers.
	tests := []struct {
		name     string
		currency string
		want     string
	}{
		{"euro", "EUR", "Currency=EUR|"},
		{"dollar", "USD", "Currency=USD|"},
		{"pound", "GBP", "Currency=GBP|"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opt := testOptions()
			opt.Currency = tt.currency
			offer := testOffer("LAMP-01", 1)
			offer.Shop = core.Money{Minor: 12500, Currency: tt.currency}

			out := render(t, EBay{}, []core.Offer{offer}, opt)
			header := headerLine(out)
			if !strings.Contains(header, tt.want) {
				t.Errorf("header %q does not carry %q", header, tt.want)
			}
			for _, other := range tests {
				if other.currency == tt.currency {
					continue
				}
				if strings.Contains(header, other.want) {
					t.Errorf("header %q also carries %q", header, other.want)
				}
			}
		})
	}
}

func TestEBayRendersEveryItemColumn(t *testing.T) {
	out := render(t, EBay{}, []core.Offer{testOffer("LAMP-01", 1)}, testOptions())
	recs := records(t, out)
	if len(recs) != 2 {
		t.Fatalf("got %d records, want header + 1 row:\n%s", len(recs), out)
	}
	want := []string{
		"Add",
		"LAMP-01",
		"20081",
		"Vintage Brass Lamp",
		"A brass lamp, rewired and tested.",
		"https://warehouse.example.com/p/photo-lamp-01-0.jpg",
		"2",
		"125.00",
		"3000",
		"FixedPrice",
		"GTC",
		"Kaunas",
		"ReturnsAccepted",
	}
	if !reflect.DeepEqual(recs[1], want) {
		t.Errorf("item row =\n%q\nwant\n%q", recs[1], want)
	}
}

func TestEBayThreePhotosProduceOneRowWithTwoPipes(t *testing.T) {
	// ★ The asymmetry with Shopify: eBay takes every image in the one PicURL
	// cell, pipe-separated, where Shopify wants an extra row per extra image.
	out := render(t, EBay{}, []core.Offer{testOffer("LAMP-01", 3)}, testOptions())
	recs := records(t, out)
	if len(recs) != 2 {
		t.Fatalf("got %d records, want header + exactly 1 row:\n%s", len(recs), out)
	}

	pics := recs[1][ebPicURL]
	if n := strings.Count(pics, "|"); n != 2 {
		t.Errorf("PicURL %q has %d pipes, want 2", pics, n)
	}
	want := []string{
		"https://warehouse.example.com/p/photo-lamp-01-0.jpg",
		"https://warehouse.example.com/p/photo-lamp-01-1.jpg",
		"https://warehouse.example.com/p/photo-lamp-01-2.jpg",
	}
	if got := strings.Split(pics, "|"); !reflect.DeepEqual(got, want) {
		t.Errorf("PicURL = %q, want %q", got, want)
	}
}

// TestEBayRequiresTheOptionsOnlyTheOperatorKnows covers the settings that are
// still per RUN and have no defensible default.
//
// ⚠ The category used to be one of them and deliberately is not any more.
// ADR-016 moved it from options-time to row-time, because eBay's categories are
// per item and an offer may name its own — so "no category" is now a property of
// an OFFER (ErrIncomplete, naming the SKU) rather than of the run (ErrOptions).
// TestEBayRefusalNamesWhereToSetTheCategory covers that case. The location is
// unchanged: it is a property of the seller, not of the item.
func TestEBayRequiresTheOptionsOnlyTheOperatorKnows(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func(*Options)
		wantText string
	}{
		{"no location", func(o *Options) { o.Location = "" }, "location"},
		{"blank location", func(o *Options) { o.Location = "\t" }, "location"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opt := testOptions()
			tt.mutate(&opt)
			var buf bytes.Buffer
			err := EBay{}.Write(&buf, []core.Offer{testOffer("LAMP-01", 1)}, opt)
			if !errors.Is(err, ErrOptions) {
				t.Fatalf("Write error = %v, want ErrOptions", err)
			}
			if !strings.Contains(err.Error(), tt.wantText) {
				t.Errorf("error does not mention %q:\n%v", tt.wantText, err)
			}
			if buf.Len() != 0 {
				t.Errorf("refused export wrote %d bytes: %q", buf.Len(), buf.String())
			}
		})
	}
}

func TestEBayConditionIDDefaultsToUsed(t *testing.T) {
	tests := []struct {
		name string
		give string
		want string
	}{
		{"unset falls back", "", DefaultConditionID},
		{"blank falls back", "  ", DefaultConditionID},
		{"supplied wins", "1000", "1000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opt := testOptions()
			opt.ConditionID = tt.give
			out := render(t, EBay{}, []core.Offer{testOffer("LAMP-01", 1)}, opt)
			recs := records(t, out)
			if len(recs) != 2 {
				t.Fatalf("got %d records, want header + 1 row", len(recs))
			}
			if got := recs[1][ebConditionID]; got != tt.want {
				t.Errorf("*ConditionID = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestEBayNeverExportsTheOwnerPrice(t *testing.T) {
	// ★ The rule the package exists to keep: what the owner wants to receive is
	// the business's private cost and must not reach a marketplace in any
	// column. The owner amount uses digits that appear nowhere else, so these
	// substring checks mean something.
	offer := testOffer("LAMP-01", 3)
	offer.Sold = core.Money{Minor: 11900, Currency: "EUR"}
	out := render(t, EBay{}, []core.Offer{offer}, testOptions())

	for _, leak := range []string{ownerDecimal, "3077"} {
		if strings.Contains(out, leak) {
			t.Errorf("owner price %q appears in the export:\n%s", leak, out)
		}
	}
	// Prove the check can fail: the shop price, which IS published, is there.
	if !strings.Contains(out, "125.00") {
		t.Fatalf("shop price is missing, so the leak check proved nothing:\n%s", out)
	}
	header := records(t, out)[0]
	for _, row := range records(t, out) {
		for i, cell := range row {
			if cell == ownerDecimal || cell == "3077" {
				t.Errorf("owner price in column %q", header[i])
			}
		}
	}
}

// TestEBayPrefersTheOffersOwnCategory is ADR-016's Enforced-by.
//
// eBay's categories are per ITEM. One category for a whole export file means one
// export per category, which is what the reported error argued when it said a
// warehouse that sells anything has no defensible default. An offer that names
// its own must therefore beat the configured one.
func TestEBayPrefersTheOffersOwnCategory(t *testing.T) {
	o := testOffer("LAMP-01", 1)
	o.Marketplace = map[core.MarketplaceKey]string{
		{Profile: "ebay", Field: core.MarketplaceCategory}: "11450",
	}

	opt := testOptions() // its Category is "20081"
	recs := records(t, render(t, EBay{}, []core.Offer{o}, opt))

	if len(recs) != 2 {
		t.Fatalf("got %d records, want header + 1 row", len(recs))
	}
	// Column 2 is *Category.
	if recs[1][2] != "11450" {
		t.Errorf("*Category = %q, want the offer's own %q rather than the default %q",
			recs[1][2], "11450", opt.Category)
	}
}

// TestEBayFallsBackToTheConfiguredDefault keeps the ordinary case working: most
// offers name no category and must still export.
func TestEBayFallsBackToTheConfiguredDefault(t *testing.T) {
	o := testOffer("LAMP-01", 1) // no marketplace values at all
	opt := testOptions()

	recs := records(t, render(t, EBay{}, []core.Offer{o}, opt))
	if len(recs) != 2 {
		t.Fatalf("got %d records, want header + 1 row", len(recs))
	}
	if recs[1][2] != opt.Category {
		t.Errorf("*Category = %q, want the configured default %q", recs[1][2], opt.Category)
	}

	// A category for a DIFFERENT profile must not be picked up: the values are
	// not interchangeable — Shopify's is a product type, eBay's is a number.
	o.Marketplace = map[core.MarketplaceKey]string{
		{Profile: "shopify", Field: core.MarketplaceCategory}: "Lighting",
	}
	recs = records(t, render(t, EBay{}, []core.Offer{o}, opt))
	if recs[1][2] != opt.Category {
		t.Errorf("*Category = %q after setting only a shopify category, want eBay's default %q",
			recs[1][2], opt.Category)
	}
}

// TestEBayPrefersTheOffersOwnCondition is ADR-022's reason for existing, applied
// to the must-have an operator asked for by name.
//
// Condition was ONE value for a whole export file until ADR-022. A warehouse
// holding a new part and a used one therefore had to run two exports or describe
// one of them wrongly — and describing a used part as new is the kind of wrong
// that ends an eBay account rather than merely annoying a buyer.
func TestEBayPrefersTheOffersOwnCondition(t *testing.T) {
	o := testOffer("LAMP-01", 1)
	o.Marketplace = map[core.MarketplaceKey]string{
		{Profile: "ebay", Field: core.MarketplaceCondition}: "1000",
	}

	opt := testOptions()
	opt.ConditionID = "3000" // the file's default says USED
	recs := records(t, render(t, EBay{}, []core.Offer{o}, opt))

	if len(recs) != 2 {
		t.Fatalf("got %d records, want header + 1 row", len(recs))
	}
	// Column 8 is *ConditionID.
	if recs[1][8] != "1000" {
		t.Errorf("*ConditionID = %q, want the offer's own %q rather than the file default %q",
			recs[1][8], "1000", opt.ConditionID)
	}

	// And an offer that names none still exports, under the configured default.
	// Empty has to keep meaning "use the default" or the dropdown's first option
	// — which is deliberately empty — would refuse every offer nobody has touched.
	plain := testOffer("LAMP-02", 1)
	recs = records(t, render(t, EBay{}, []core.Offer{plain}, opt))
	if recs[1][8] != "3000" {
		t.Errorf("*ConditionID = %q for an offer naming none, want the default %q", recs[1][8], "3000")
	}
}

// TestEBayPrefersTheOffersOwnLocation covers the third must-have.
//
// Unlike the category, a default location stays REQUIRED — every deployment has
// one, and eBay quotes postage from it. The offer's own value is an override for
// the item that ships from somewhere else, which is what a warehouse with two
// sites needs and could not express before.
func TestEBayPrefersTheOffersOwnLocation(t *testing.T) {
	o := testOffer("LAMP-01", 1)
	o.Marketplace = map[core.MarketplaceKey]string{
		{Profile: "ebay", Field: core.MarketplaceLocation}: "Vilnius",
	}

	opt := testOptions()
	opt.Location = "Kaunas"
	recs := records(t, render(t, EBay{}, []core.Offer{o}, opt))

	if len(recs) != 2 {
		t.Fatalf("got %d records, want header + 1 row", len(recs))
	}
	// Column 11 is *Location.
	if recs[1][11] != "Vilnius" {
		t.Errorf("*Location = %q, want the offer's own %q rather than the default %q",
			recs[1][11], "Vilnius", opt.Location)
	}

	plain := testOffer("LAMP-02", 1)
	recs = records(t, render(t, EBay{}, []core.Offer{plain}, opt))
	if recs[1][11] != "Kaunas" {
		t.Errorf("*Location = %q for an offer naming none, want the default %q", recs[1][11], "Kaunas")
	}
}

// TestEBayRefusalNamesWhereToSetTheCategory pins the half of ADR-016 that is
// about the MESSAGE rather than the mechanism.
//
// The reported failure was not that a value was missing. It was that the error
// said a value was required and nothing anywhere said where to put one — the
// variable was undocumented and there was no screen for it. So the refusal has
// to name the offer it is about and both places a person can act.
func TestEBayRefusalNamesWhereToSetTheCategory(t *testing.T) {
	o := testOffer("LAMP-01", 1)
	opt := testOptions()
	opt.Category = "" // nothing configured, and the offer names none either

	var buf bytes.Buffer
	err := EBay{}.Write(&buf, []core.Offer{o}, opt)
	if err == nil {
		t.Fatal("Write with no category anywhere returned nil; want a refusal")
	}
	if !errors.Is(err, ErrIncomplete) && !errors.Is(err, ErrOptions) {
		t.Errorf("error = %v, want it to wrap ErrIncomplete or ErrOptions", err)
	}

	msg := err.Error()
	for _, want := range []string{"LAMP-01", "Settings", "offer"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal does not mention %q, so it does not tell the operator where to "+
				"act. Message: %s", want, msg)
		}
	}
	if buf.Len() != 0 {
		t.Errorf("a refused export wrote %d bytes; it must leave w untouched", buf.Len())
	}
}
