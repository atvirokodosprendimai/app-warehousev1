package core

import (
	"errors"
	"strings"
	"testing"
)

// TestMarketplaceFieldVocabularyIsClosed pins the three fields and the refusal of
// anything else.
//
// ⚠ CLOSED IS THE DECISION (ADR-022). Each member has a home in
// `export.Options`, so a fourth is a code change rather than something an
// administrator can type into a form. An open vocabulary would let the settings
// screen mint a field that no exporter reads, and the operator would fill it in
// and watch it reach no marketplace.
func TestMarketplaceFieldVocabularyIsClosed(t *testing.T) {
	want := []MarketplaceField{MarketplaceCategory, MarketplaceCondition, MarketplaceLocation}
	got := MarketplaceFields()
	if len(got) != len(want) {
		t.Fatalf("MarketplaceFields() = %v, want exactly %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("field %d = %q, want %q", i, got[i], want[i])
		}
	}

	for _, name := range []string{"category", "condition", "location"} {
		if !ValidMarketplaceField(name) {
			t.Errorf("ValidMarketplaceField(%q) = false, want true", name)
		}
	}
	// Case and surrounding space are the operator's, not a different field.
	if !ValidMarketplaceField("  Category ") {
		t.Error("ValidMarketplaceField does not normalise, so a form's stray space mints a miss")
	}
	for _, name := range []string{"", "colour", "price", "owner"} {
		if ValidMarketplaceField(name) {
			t.Errorf("ValidMarketplaceField(%q) = true, want false — an unknown field would be "+
				"configurable and unreadable at the same time", name)
		}
	}
}

// TestAMarketplaceOptionRefusesWhatCannotReachACSV checks each refusal separately.
//
// Value and label are refused independently because they fail differently: a blank
// value writes an empty cell into a column the marketplace requires, and a blank
// label renders a dropdown entry the operator cannot tell from the next one.
func TestAMarketplaceOptionRefusesWhatCannotReachACSV(t *testing.T) {
	ok := MarketplaceOption{
		ID: "opt-1", Profile: "ebay", Field: MarketplaceCategory,
		Value: "20081", Label: "Antiques",
	}
	if err := ok.Validate(); err != nil {
		t.Fatalf("a well-formed option was refused: %v — every case below would then pass "+
			"for the wrong reason", err)
	}

	for _, tc := range []struct {
		name   string
		mutate func(*MarketplaceOption)
		want   string
	}{
		{"no value", func(o *MarketplaceOption) { o.Value = "  " }, "value"},
		{"no label", func(o *MarketplaceOption) { o.Label = "" }, "label"},
		{"unknown field", func(o *MarketplaceOption) { o.Field = "colour" }, "field"},
		{"unknown profile", func(o *MarketplaceOption) { o.Profile = "gumtree" }, "profile"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			o := ok
			tc.mutate(&o)
			err := o.Validate()
			if err == nil {
				t.Fatalf("%s was accepted", tc.name)
			}
			if !errors.Is(err, ErrInvalid) {
				t.Errorf("error %v does not wrap ErrInvalid, so callers cannot tell a bad "+
					"option from a broken database", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not say %q, so the operator is not told which box "+
					"to fix", err, tc.want)
			}
		})
	}
}

// TestOfferMarketplaceValueIsTheOnlyWayIn pins the accessor, including the part
// that looks like a missing feature.
//
// ⚠ IT DOES NOT SUBSTITUTE A DEFAULT, deliberately. "This offer says nothing" and
// "this offer says the same as the default" have to stay distinguishable, because
// the dropdown renders an explicit empty option for the first and a selected entry
// for the second. An accessor that folded them together would make the fallback
// invisible and every offer would look configured.
func TestOfferMarketplaceValueIsTheOnlyWayIn(t *testing.T) {
	o := Offer{Marketplace: map[MarketplaceKey]string{
		{Profile: "ebay", Field: MarketplaceCategory}:  "20081",
		{Profile: "ebay", Field: MarketplaceCondition}: "1000",
		{Profile: "recar", Field: MarketplaceCategory}: "silniki",
	}}

	if got := o.MarketplaceValue("ebay", MarketplaceCategory); got != "20081" {
		t.Errorf("ebay category = %q, want %q", got, "20081")
	}
	if got := o.MarketplaceValue("ebay", MarketplaceCondition); got != "1000" {
		t.Errorf("ebay condition = %q, want %q", got, "1000")
	}
	// The same field on another profile is a different value, which is the whole
	// reason the key carries both.
	if got := o.MarketplaceValue("recar", MarketplaceCategory); got != "silniki" {
		t.Errorf("recar category = %q, want %q", got, "silniki")
	}
	if got := o.MarketplaceValue("ebay", MarketplaceLocation); got != "" {
		t.Errorf("an unset field returned %q; empty is how the caller learns to fall back "+
			"to the configured default", got)
	}
	if got := (Offer{}).MarketplaceValue("ebay", MarketplaceCategory); got != "" {
		t.Errorf("a nil map returned %q, want empty — an offer that has never been given a "+
			"marketplace value is the ordinary case at intake (ADR-004)", got)
	}
	// Profile casing is the caller's; the key is not.
	if got := o.MarketplaceValue("eBay", MarketplaceCategory); got != "20081" {
		t.Errorf("eBay category = %q, want %q — export.For matches case-insensitively and "+
			"this must agree with it", got, "20081")
	}
}
