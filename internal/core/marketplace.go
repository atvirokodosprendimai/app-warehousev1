package core

import (
	"fmt"
	"strings"
)

// MarketplaceField names one of the values a marketplace refuses a listing
// without — the things an operator must say about WHERE an item is listed, as
// opposed to what it IS.
//
// ⚠ THE VOCABULARY IS CLOSED, AND THAT IS THE DECISION (ADR-022). Each member has
// a home in the exporter's options, so adding a fourth is a code change rather
// than something an administrator types into a form. An open vocabulary would let
// the settings screen mint a field no exporter reads: the operator would fill it
// in, and it would reach no marketplace with nothing to say so.
//
// ⚠ NOT TO BE CONFUSED WITH [FieldKind], whose constants are named `Field…`.
// That vocabulary describes a question a CATEGORY asks about an item (ADR-021);
// this one describes a marketplace's own requirement. The constants here are
// named `Marketplace…` for exactly that reason.
type MarketplaceField string

const (
	// MarketplaceCategory is the marketplace's own taxonomy identifier — eBay's
	// number, Allegro's category name. Not [Offer.CategoryID], which says what
	// the thing is.
	MarketplaceCategory MarketplaceField = "category"
	// MarketplaceCondition is the marketplace's condition code or word, e.g.
	// eBay's numeric `1000` for New.
	MarketplaceCondition MarketplaceField = "condition"
	// MarketplaceLocation is where the item dispatches from. A distributed
	// warehouse (ADR-005) has more than one, which is why it is per item rather
	// than per deployment.
	MarketplaceLocation MarketplaceField = "location"
)

// MarketplaceFields lists every field this application defines, in the order a
// settings screen should present them.
func MarketplaceFields() []MarketplaceField {
	return []MarketplaceField{MarketplaceCategory, MarketplaceCondition, MarketplaceLocation}
}

// ValidMarketplaceField reports whether name is a field this application knows,
// matching case-insensitively and ignoring surrounding space so that a value
// arriving from a form is not a miss for a reason nobody can see.
func ValidMarketplaceField(name string) bool {
	f := MarketplaceField(strings.ToLower(strings.TrimSpace(name)))
	for _, known := range MarketplaceFields() {
		if known == f {
			return true
		}
	}
	return false
}

// MarketplaceKey addresses one marketplace value on one offer.
//
// Both halves are needed: the same field carries different values per profile —
// eBay's category is a number and Allegro's is a name — so a map keyed by field
// alone would force at least one of them to be wrong.
type MarketplaceKey struct {
	// Profile is the export profile's registered name, lower case.
	Profile string
	// Field is which of the must-haves this is.
	Field MarketplaceField
}

// MarketplaceOption is one entry in the list an operator picks from for a given
// profile and field.
//
// Value and Label are separate because both are needed and neither can be derived
// from the other: `20081` is what reaches the CSV and `Antiques` is what a person
// can choose between.
type MarketplaceOption struct {
	// ID is the immutable internal identifier (a UUID).
	ID string
	// Profile is the export profile this option belongs to.
	Profile string
	// Field is which must-have this option answers.
	Field MarketplaceField
	// Value is what reaches the marketplace.
	Value string
	// Label is what the operator reads in the dropdown.
	Label string
	// Position orders this option among the others for the same profile and field.
	Position int
}

// Validate reports whether this option can be offered and exported.
//
// Value and Label are refused separately because they fail differently and the
// operator fixes them in different boxes: a blank value writes an empty cell into
// a column the marketplace requires, and a blank label renders a dropdown entry
// that cannot be told apart from the next one.
func (o MarketplaceOption) Validate() error {
	if !ValidExportProfile(o.Profile) {
		return fmt.Errorf("%w: unknown export profile %q; an option filed against a profile "+
			"nothing exports is one the operator will pick and never see published",
			ErrInvalid, o.Profile)
	}
	if !ValidMarketplaceField(string(o.Field)) {
		return fmt.Errorf("%w: unknown marketplace field %q; the vocabulary is closed because "+
			"each field has to have somewhere to go in the export", ErrInvalid, o.Field)
	}
	if strings.TrimSpace(o.Value) == "" {
		return fmt.Errorf("%w: an option needs a value, because that is what reaches the "+
			"marketplace; an empty one writes a blank cell into a required column",
			ErrInvalid)
	}
	if strings.TrimSpace(o.Label) == "" {
		return fmt.Errorf("%w: an option needs a label, because that is what the operator "+
			"chooses between; two unlabelled entries are indistinguishable in a dropdown",
			ErrInvalid)
	}
	return nil
}

// MarketplaceValue returns what this offer says for one profile's field, or an
// empty string when it says nothing.
//
// ⚠ IT DOES NOT SUBSTITUTE THE CONFIGURED DEFAULT, and that is deliberate rather
// than unfinished. "This offer chose nothing" and "this offer chose the value that
// happens to equal the default" must stay distinguishable: the editor renders an
// explicit empty option for the first and a selected entry for the second, and an
// accessor that folded them together would make the fallback invisible and every
// offer look configured. Falling back is the caller's job, where the default is
// actually in hand.
func (o Offer) MarketplaceValue(profile string, f MarketplaceField) string {
	return o.Marketplace[MarketplaceKey{
		Profile: strings.ToLower(strings.TrimSpace(profile)),
		Field:   f,
	}]
}
