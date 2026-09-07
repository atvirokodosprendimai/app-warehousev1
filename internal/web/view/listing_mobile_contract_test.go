package view

import (
	"os"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
)

// This file pins the one thing about the offers listing that a stylesheet and a
// template have to agree on, and that neither can check alone.
//
// Below 860px the listing stops being a table: eight columns are 802px wide, and
// measured in Chromium at 390x844 the page scrolled sideways with the LOCATION
// column off the right edge — with mobile emulation switched off too, so it was
// the layout rather than the harness. The stylesheet lays each row out as a
// stacked card instead.
//
// That rewrite costs two things unless the markup pays for them:
//
//  1. ARIA. Setting `display` to anything other than `table*` STRIPS a table's
//     implicit semantics, so without explicit roles a screen reader is handed
//     eight anonymous blocks per offer.
//  2. The column headings. `thead` is hidden, so a value with no `data-label`
//     is a number on a phone with nothing saying what it is.
//
// Both are invisible on a desktop, in the server-side suite, and in a diff. They
// are asserted here because nothing else looks at them.

// fullRow exercises every conditional cell at once: an administrator looking at
// somebody else's stock with a cart open sees the widest row the table can make.
func fullRow() OfferRow {
	return OfferRow{Offer: core.Offer{
		ID:     "o-9",
		SKU:    "WH0000009",
		Title:  "Enamel advertising sign",
		Status: core.StatusDraft,
	}}
}

func fullTableHTML(t *testing.T) string {
	t.Helper()
	return renderString(t, OfferTable("all", []OfferRow{fullRow()}, true, true))
}

// TestEveryListingCellCarriesAnExplicitRole refuses a silent loss of table
// semantics.
//
// The roles look redundant while the element is displayed as a table, which is
// exactly why somebody would delete them as noise. They are load-bearing only on
// a phone, where the display is changed out from under them.
func TestEveryListingCellCarriesAnExplicitRole(t *testing.T) {
	html := fullTableHTML(t)

	for _, want := range []string{
		`<table role="table"`,
		`<thead role="rowgroup"`,
		`<tbody role="rowgroup"`,
		`<tr role="row"`,
		`role="columnheader"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("the listing is missing %s, so below 860px — where the stylesheet sets "+
				"display:block — the browser stops reporting a table at all", want)
		}
	}

	// Every cell, not merely one: a role added to the first <td> and forgotten on
	// the rest reads as done and is not.
	cells := strings.Count(html, "<td")
	roled := strings.Count(html, `<td role="cell"`)
	if cells == 0 {
		t.Fatal("the row rendered no cells at all, so this test proves nothing")
	}
	if cells != roled {
		t.Errorf("%d of %d cells carry no explicit role, so those become anonymous blocks "+
			"on a phone while the rest stay cells", cells-roled, cells)
	}
}

// TestEveryValueCellCarriesItsLabel refuses an unlabelled number on a phone.
//
// These four cells are the ones whose meaning lives entirely in a column heading
// that the phone layout hides. A price with no label beside it is just a number.
func TestEveryValueCellCarriesItsLabel(t *testing.T) {
	html := fullTableHTML(t)

	for _, want := range []string{
		`data-label="Location"`,
		`data-label="Shop price"`,
		`data-label="Owner wants"`,
		`data-label="Margin"`,
	} {
		if !strings.Contains(html, want) {
			t.Errorf("no cell carries %s, so with the header row hidden that value appears "+
				"on a phone with nothing saying what it is", want)
		}
	}

	// The wording must match the heading it replaces, or the phone and the desktop
	// are describing the same column with two different names.
	for _, heading := range []string{"Location", "Shop price", "Owner wants", "Margin"} {
		if !strings.Contains(html, ">"+heading+"</th>") {
			t.Errorf("the header cell for %q is gone, so the data-label of the same name is "+
				"no longer standing in for anything", heading)
		}
	}
}

// TestTheStylesheetAndTheListingAgreeOnEveryCellName binds the two files.
//
// The phone layout is positional: the stylesheet places each cell by class. A
// class renamed on one side and not the other does not fail a build, does not
// fail the suite, and does not look wrong in either file on its own — the row
// simply collapses into a heap the next time somebody opens it on a phone.
func TestTheStylesheetAndTheListingAgreeOnEveryCellName(t *testing.T) {
	css, err := os.ReadFile("../assets/app.css")
	if err != nil {
		t.Fatalf("cannot read the stylesheet this test exists to check: %v", err)
	}
	sheet := string(css)
	html := fullTableHTML(t)

	for _, class := range []string{
		"cell-photo", "cell-item", "cell-status", "cell-where",
		"cell-price", "cell-owner", "cell-margin", "cell-add",
	} {
		inHTML := strings.Contains(html, class)
		inCSS := strings.Contains(sheet, "."+class)
		switch {
		case inHTML && !inCSS:
			t.Errorf("the listing renders %q and the stylesheet never positions it, so that "+
				"cell falls wherever the grid puts it on a phone", class)
		case inCSS && !inHTML:
			t.Errorf("the stylesheet positions %q and no cell carries it, so the rule is dead "+
				"and the cell it was written for is unstyled", class)
		}
	}

	// The three rules that DO the stacking. Without them the roles and labels
	// above are paid for and never used.
	for _, rule := range []string{
		"thead { display: none; }",
		".table-wrap { overflow-x: visible; }",
		"td[data-label]::before",
	} {
		if !strings.Contains(sheet, rule) {
			t.Errorf("the stylesheet no longer contains %q, so the listing is a wide table on "+
				"a phone again and the page scrolls sideways", rule)
		}
	}
}
