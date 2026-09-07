package view

import (
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
)

// This file pins ADR-020: intake is one click, and the editor is read
// camera-first and status-last.
//
// M asked for it in three sentences — "1step click offer, 2nd upload photos 3rd
// - all other info" and "status: listing, draf, pending etc. should be last
// block" — and every one of them is an ORDERING or a REACHABILITY claim, which
// is the class of thing that compiles, renders, passes a suite and is still
// wrong. Nothing else in this repository looks at the order of the cards.

// cardOrder returns the card headings in the order they appear in the document.
//
// DOM order is the whole subject here: `.cols` is a single column below 861px,
// so on a phone the order these appear in the markup IS the order a person
// meets them, and the two-column desktop layout fills column one before column
// two — so "last in the document" is "last on the page" on both.
func cardOrder(html string) []string {
	var out []string
	for _, seg := range strings.Split(html, `<div class="card-head">`)[1:] {
		open := strings.Index(seg, "<h2>")
		if open < 0 {
			continue
		}
		rest := seg[open+len("<h2>"):]
		end := strings.Index(rest, "</h2>")
		if end < 0 {
			continue
		}
		out = append(out, rest[:end])
	}
	return out
}

func editorHTML(t *testing.T) string {
	t.Helper()
	return renderString(t, OfferScreen(OfferDetail{
		Row: OfferRow{Offer: core.Offer{
			ID: "o-1", SKU: "WH0000001", Title: "Enamel sign",
			Status: core.StatusDraft, Quantity: 1,
		}},
	}))
}

// TestCreatingAnOfferAsksNothingFirst refuses a form between a person and the
// camera.
//
// The control must CREATE, not navigate. A link to a screen is exactly the
// "aditional step" M named, and the screen it pointed at asked three questions
// that were all already optional — a title (ADR-019), a reference the database
// allocates (ADR-010), and a location with its own card on the editor.
func TestCreatingAnOfferAsksNothingFirst(t *testing.T) {
	chrome := renderString(t, NewOfferAction())

	if strings.Contains(chrome, `href="/offers/new"`) {
		t.Error("the New offer control still links to the intake screen, so creating an " +
			"offer still costs a form before the camera — which is the step ADR-020 removes")
	}
	if !strings.Contains(chrome, "@post('/offers')") {
		t.Error("the New offer control does not post to /offers, so pressing it does not " +
			"create anything and the flow has no first step at all")
	}
	// ADR-008: the mechanism must stay a datastar action, not a smuggled form.
	if strings.Contains(chrome, "<form") {
		t.Error("a <form> appeared in the top bar, which ADR-008 permits only for the photo upload")
	}

	// The empty state is the OTHER way somebody starts, and it is the one a new
	// installation meets first. It must not survive as a link to a dead address.
	empty := renderString(t, OfferTable("t", nil, false, false))
	if strings.Contains(empty, `href="/offers/new"`) {
		t.Error("the empty state still links to /offers/new, an address ADR-020 deletes, so " +
			"the first thing a new installation offers is a 404")
	}
}

// TestTheCameraIsTheFirstCard pins M's step 2.
//
// Photographs come before every other card. This is not styling: on a phone the
// column collapses, so whichever card is first in the document is the only one
// visible without scrolling.
func TestTheCameraIsTheFirstCard(t *testing.T) {
	order := cardOrder(editorHTML(t))
	if len(order) == 0 {
		t.Fatal("the editor rendered no cards, so this test proves nothing")
	}
	if order[0] != "Photos" {
		t.Errorf("the first card is %q, not \"Photos\" — an operator who has just pressed "+
			"New offer is holding the object, and the camera is what they need first "+
			"(full order: %v)", order[0], order)
	}
}

// TestStatusIsTheLastCard pins M's "status ... should be last block".
//
// Status is the only card that is not data entry: every other one records what
// the thing IS, and this one decides what the business does with it. It used to
// head the second column, which on a phone put "Listed / Draft / Pending"
// between the photographs and the price.
func TestStatusIsTheLastCard(t *testing.T) {
	order := cardOrder(editorHTML(t))
	if len(order) == 0 {
		t.Fatal("the editor rendered no cards, so this test proves nothing")
	}
	if last := order[len(order)-1]; last != "Status" {
		t.Errorf("the last card is %q, not \"Status\" — %q therefore sits after the decision "+
			"M asked to come last (full order: %v)", last, last, order)
	}
}

// TestTheReferenceIsEditableAfterCreation refuses a silent capability loss.
//
// The deleted intake screen was the ONLY place a reference could be typed. M's
// flow moves that to "3rd - all other info" rather than removing it, so the
// Details card has to carry it — otherwise deleting the screen quietly takes a
// capability away under cover of a reordering.
func TestTheReferenceIsEditableAfterCreation(t *testing.T) {
	html := editorHTML(t)

	if !strings.Contains(html, "data-bind:offer-sku") {
		t.Error("no bound reference field on the editor, so deleting the intake screen removed " +
			"the only way to set a reference instead of moving it to step 3")
	}
	// HTML lowercases attribute names, so the kebab-case binding is what reaches
	// the camelCase signal `offerSku`. Writing data-bind:offerSku binds
	// `offersku`, a different signal, and nothing reports it.
	if strings.Contains(html, "data-bind:offerSku") {
		t.Error("the reference binds camelCase, which the HTML parser lowercases to a " +
			"DIFFERENT signal than the handler reads, and nothing anywhere reports it")
	}
	if !strings.Contains(html, "WH0000001") {
		t.Error("the editor no longer shows the reference at all, so nobody can read the " +
			"number that is written on the box")
	}
}
