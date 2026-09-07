package view

import (
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
)

// This file pins ADR-019's half of the interface: the hand-off point between the
// person who photographs stock and the person who names it.
//
// The rule and the query live in internal/core and internal/offer and are tested
// there. What CANNOT be tested there is the only thing that makes the feature
// real — that somebody can FIND the work. A queue nothing links to is an address
// you have to already know, which is not a queue.

// untitledRow is what a photographer leaves behind: photographs, a reference,
// and no name.
func untitledRow() OfferRow {
	return OfferRow{Offer: core.Offer{
		ID:     "o-1",
		SKU:    "WH0000042",
		Title:  "",
		Status: core.StatusDraft,
		Photos: []core.Photo{
			{ID: "11111111-1111-1111-1111-111111111111", ContentType: "image/jpeg"},
			{ID: "22222222-2222-2222-2222-222222222222", ContentType: "image/jpeg"},
			{ID: "33333333-3333-3333-3333-333333333333", ContentType: "image/jpeg"},
		},
	}}
}

// TestUntitledOffersRenderAsUntitled refuses a blank cell.
//
// An empty title is now a legitimate state (ADR-019), so it reaches every screen
// that lists offers. Rendered as nothing it reads as a RENDERING FAULT — the
// operator sees a row with a hole in it and reports a bug — where the word plus
// the reference and the photograph count reads as work waiting, which is what it
// is. The reference is what is written on the box, so it is also the only way to
// tell two unnamed rows apart.
func TestUntitledOffersRenderAsUntitled(t *testing.T) {
	html := renderString(t, OfferTable("q", []OfferRow{untitledRow()}, false, false))

	if !strings.Contains(html, "Untitled") {
		t.Error("an untitled offer renders no placeholder at all, so the row has a hole in " +
			"it where its name should be and reads as a rendering fault")
	}
	if !strings.Contains(html, "WH0000042") {
		t.Error("the reference is missing, so two unnamed rows cannot be told apart and " +
			"nobody can match a row to the box in their hands")
	}
	// The count is what says how much of a thing has been photographed, and it is
	// the cataloguer's cue that the group is complete enough to describe.
	if !strings.Contains(html, "3") {
		t.Error("the photograph count is missing, so the queue does not say how much was shot")
	}

	// The precondition: a NAMED row must still render its own title, or the
	// assertion above would pass on a table that prints "Untitled" for everything.
	named := untitledRow()
	named.Offer.Title = "Vintage brass desk lamp"
	namedHTML := renderString(t, OfferTable("q", []OfferRow{named}, false, false))
	if !strings.Contains(namedHTML, "Vintage brass desk lamp") {
		t.Fatal("a named offer lost its title, so this test is asserting nothing")
	}
	if strings.Contains(namedHTML, "Untitled") {
		t.Error("a named offer is rendered as Untitled")
	}
}

// TestTheDescribingQueueIsReachableFromTheMenu is the feature M actually asked
// for: "need new menu item for photos which needs to be categoriezed".
//
// The filter existed before this test and was reachable only by typing the query
// string, which is indistinguishable from not existing. A division of labour
// needs a hand-off point somebody can see, and the count is what tells them
// there is anything to pick up.
func TestTheDescribingQueueIsReachableFromTheMenu(t *testing.T) {
	html := renderString(t, sidebar(Page{
		User:            core.User{Email: "m@example.com", DisplayName: "M"},
		NeedsDescribing: 3,
	}))

	if !strings.Contains(html, "/offers?needs_describing=1") {
		t.Fatal("the sidebar carries no link to the describing queue, so the work is " +
			"reachable only by typing a query string nobody has been told about")
	}
	if !strings.Contains(html, "Needs describing") {
		t.Error("the menu entry has no label a person would recognise")
	}
	// The count is the whole signal. A menu entry that always looks the same does
	// not tell anybody there is work.
	i := strings.Index(html, "/offers?needs_describing=1")
	end := strings.Index(html[i:], "</a>")
	if end < 0 {
		t.Fatal("malformed nav entry")
	}
	if !strings.Contains(html[i:i+end], "3") {
		t.Errorf("the entry does not carry its count, so it looks identical whether three "+
			"items are waiting or none are: %s", html[i:i+end])
	}
}

// TestIntakeDoesNotDemandATitle was RETIRED on 2026-09-07 by ADR-020.
//
// It rendered `IntakeScreen` and asserted the title field carried no `required`
// attribute, and that the screen said where an unnamed item goes. Both were true
// and both were worth pinning while that screen existed.
//
// ADR-020 DELETED the screen: pressing "New offer" now creates the draft and
// lands the operator on its photographs, so there is no field left to demand
// anything. The property this test protected is not weakened but subsumed — a
// screen that does not exist cannot ask for a title.
//
// ⚠ Retired in place rather than deleted, because the ADR-019 property it
// carried is still live and somebody re-reading that record needs to find where
// its proof went. Its successor is
// `intake_contract_test.go::TestCreatingAnOfferAsksNothingFirst`, which asserts
// the control CREATES instead of navigating to a form, and
// `smoke.sh`'s check that `/offers/new` now returns 404.

// TestRowsPathKeepsTheDescribingFilter stops the live search widening the queue.
//
// The status part of a filter lives in the URL and the text part travels as a
// signal, so the rows endpoint has to be told which filter it is refreshing. If
// it is not, typing one character into the search box while looking at the
// describing queue silently replaces it with every offer in the warehouse — and
// the cataloguer has no way to tell that the list they are working from changed
// meaning underneath them.
func TestRowsPathKeepsTheDescribingFilter(t *testing.T) {
	l := OfferList{Filter: core.OfferFilter{NeedsDescribing: true}}
	if got := l.RowsPath(); !strings.Contains(got, "needs_describing=1") {
		t.Errorf("RowsPath() = %q, want it to carry needs_describing=1 — otherwise a search "+
			"inside the queue quietly widens to every offer", got)
	}

	// And it must not appear when it was not asked for, or every plain listing
	// would collapse to the queue.
	plain := OfferList{}
	if got := plain.RowsPath(); strings.Contains(got, "needs_describing") {
		t.Errorf("RowsPath() = %q on an unfiltered listing, want no describing filter", got)
	}
}
