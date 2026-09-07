package view

import (
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
)

// This file pins the quantity field to the interface.
//
// ⚠ `core.Offer.Quantity` is NOT new. It has existed since the first migration,
// is constrained by the schema, refused when negative by `Offer.Validate`,
// rewritten by `Repo.UpdateOffer`, read back by the cart, and written by BOTH
// exporters — eBay's `*Quantity` and Shopify's `Variant Inventory Qty`.
//
// What was missing is the only link in that chain a person can reach: nothing in
// `internal/web` ever set it, so every offer carried the database default of 1
// and a shelf of five went to a marketplace as one. That is a wrong number
// leaving the building rather than a cosmetic gap, and it is why these assertions
// exist at the EDGE — the rest of the chain was always covered and always green.

// signalSeed returns what the initial data-signals object assigns to name.
//
// It reads the RENDERED page rather than the template source, which is the whole
// point: the seed sits in an HTML attribute, so templ escapes its quotes, and a
// test matching the unescaped spelling passes or fails on the Go source instead
// of on what a browser receives.
func signalSeed(html, name string) string {
	i := strings.Index(html, name+":")
	if i < 0 {
		return ""
	}
	rest := html[i+len(name)+1:]
	if end := strings.IndexAny(rest, ",}"); end >= 0 {
		return strings.TrimSpace(rest[:end])
	}
	return strings.TrimSpace(rest)
}

// TestQuantityIsEditable refuses a field that can be read and never written.
func TestQuantityIsEditable(t *testing.T) {
	html := renderString(t, OfferScreen(OfferDetail{
		Row: OfferRow{Offer: core.Offer{
			ID: "o-1", SKU: "WH0000001", Title: "Enamel sign",
			Status: core.StatusDraft, Quantity: 7,
		}},
	}))

	if !strings.Contains(html, "data-bind:offer-quantity") {
		t.Error("the editor has no bound quantity field, so the number both exporters " +
			"send to a marketplace is still one nobody can set")
	}
	// HTML lowercases attribute names, so the kebab-case binding is what reaches
	// the camelCase signal the handler reads. The camelCase spelling would bind
	// `offerquantity` — a different signal — and nothing would report it.
	if strings.Contains(html, "data-bind:offerQuantity") {
		t.Error("the quantity binds a camelCase attribute, which the parser lowercases to a " +
			"DIFFERENT signal than the handler reads, and nothing anywhere reports it")
	}
	// The seeded signal is what puts the CURRENT value in the box. Without it the
	// field renders empty, and an empty payload means "unchanged" — so a save
	// would silently leave the old number in place while showing a blank.
	//
	// The seed lives inside a data-signals ATTRIBUTE, so templ escapes its quotes
	// — matching on `offerQuantity: "7"` would be matching the Go source rather
	// than the rendered page. Read the value out of the attribute instead.
	if seed := signalSeed(html, "offerQuantity"); !strings.Contains(seed, "7") {
		t.Errorf("the editor seeds offerQuantity as %q, not the offer's 7, so the field "+
			"opens blank or wrong and the operator cannot tell 7 from unset", seed)
	}

	// A number input would send a JSON number into a string field and fail to
	// unmarshal. Every other numeric input on this screen is text + inputmode,
	// and this one has to match or the save breaks in a way no view test sees.
	i := strings.Index(html, `id="o-qty"`)
	if i < 0 {
		t.Fatal("no quantity input at all, so this test proves nothing")
	}
	start := strings.LastIndex(html[:i], "<input")
	end := strings.Index(html[i:], ">")
	if start < 0 || end < 0 {
		t.Fatal("malformed quantity input")
	}
	if input := html[start : i+end]; strings.Contains(input, `type="number"`) {
		t.Errorf("the quantity is a number input, so datastar sends a JSON number into a "+
			"string signal and the save fails at unmarshal time. Input: %s", input)
	}
}

// TestQuantityIsVisibleOnTheListing answers M's question directly: "how much we
// have them" is asked while looking at a list, not while inside one offer.
func TestQuantityIsVisibleOnTheListing(t *testing.T) {
	html := renderString(t, OfferTable("all", []OfferRow{{Offer: core.Offer{
		ID: "o-2", SKU: "WH0000002", Title: "Enamel sign",
		Status: core.StatusDraft, Quantity: 5,
	}}}, true, true))

	if !strings.Contains(html, ">Qty</th>") {
		t.Error("the listing has no quantity column, so stock is invisible anywhere except " +
			"inside one offer at a time")
	}
	if !strings.Contains(html, `data-label="Qty"`) {
		t.Error("the quantity cell carries no data-label, so on a phone — where the header " +
			"row is hidden — the number appears with nothing saying what it is")
	}
	if !strings.Contains(html, ">5</td>") {
		t.Error("the listing does not render the actual quantity, so the column is there " +
			"and says nothing")
	}
}
