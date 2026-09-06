package view

import (
	"context"
	"strings"
	"testing"

	"github.com/a-h/templ"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
)

// This file pins the ONE datastar contract that no server-side test can reach.
//
// A file upload is the single place this application uses form encoding, and
// datastar v1.0.2 imposes three requirements on the markup around it. Read from
// the pinned bundle, verbatim:
//
//	else if (u === "form") {
//	    let T = l ? document.querySelector(l) : r.closest("form");
//	    if (!T) throw i("FetchFormNotFound", {action: D, selector: l});
//	    ...
//	    let A = T.getAttribute("enctype") === "multipart/form-data";
//	    A || (q["Content-Type"] = "application/x-www-form-urlencoded");
//	    let X = new URLSearchParams(F);
//	    if (ot(t)) A ? Y.body = F : Y.body = X;
//	}
//
// So an upload needs an enclosing <form>, that form needs
// enctype="multipart/form-data", and — because new FormData(form) collects only
// NAMED controls — the input needs a name. Each one alone is fatal, and each
// fails differently: no form THROWS and never sends a request at all, so the
// control looks dead; the wrong enctype sends urlencoded, which cannot carry
// bytes; an unnamed input is simply omitted from the body.
//
// ⚠ ALL THREE SHIPPED, AND EVERY SERVER-SIDE CHECK STAYED GREEN. The handler was
// exercised by a smoke test that built its own multipart body with curl, which
// bypasses the browser entirely — it proved the server accepts an upload and said
// nothing about whether the page can produce one. That gap is what this file
// closes: it asserts the MARKUP the client needs, which is the half curl cannot
// see.

// renderString renders a component for inspection.
func renderString(t *testing.T, c templ.Component) string {
	t.Helper()
	var sb strings.Builder
	if err := c.Render(context.Background(), &sb); err != nil {
		t.Fatalf("render: %v", err)
	}
	return sb.String()
}

// assertFormUploadContract checks every form-encoded datastar action in html.
func assertFormUploadContract(t *testing.T, name, html string) {
	t.Helper()

	// The action expression is HTML-escaped in the attribute, so match on the
	// unescaped key rather than on the whole expression.
	const marker = "contentType:"
	if !strings.Contains(html, marker) {
		t.Fatalf("%s: no form-encoded action found at all — this test is asserting "+
			"nothing; either the upload moved or the marker changed", name)
	}

	for idx := 0; ; {
		i := strings.Index(html[idx:], marker)
		if i < 0 {
			break
		}
		at := idx + i
		idx = at + len(marker)

		// Nearest enclosing form: the last <form before the action, and its </form>
		// must come after it.
		open := strings.LastIndex(html[:at], "<form")
		if open < 0 {
			t.Fatalf("%s: a form-encoded action has NO enclosing <form>. datastar does "+
				"el.closest(\"form\") and throws FetchFormNotFound, so nothing is sent "+
				"and the control appears dead rather than failing", name)
		}
		closeAt := strings.Index(html[open:], "</form>")
		if closeAt < 0 || open+closeAt < at {
			t.Fatalf("%s: the form-encoded action is not inside the form that precedes "+
				"it, so closest(\"form\") would not find it", name)
		}
		form := html[open : open+closeAt]

		// The open tag ends at the first '>'.
		tagEnd := strings.Index(form, ">")
		if tagEnd < 0 {
			t.Fatalf("%s: malformed <form> tag", name)
		}
		openTag := form[:tagEnd]

		if !strings.Contains(openTag, `enctype="multipart/form-data"`) {
			t.Errorf("%s: <form> lacks enctype=\"multipart/form-data\", so datastar "+
				"sends URLSearchParams and the file bytes are dropped silently. Tag: %s",
				name, openTag)
		}

		// A file input inside that form, carrying a name — FormData ignores
		// unnamed controls, so without one the body arrives empty.
		fi := strings.Index(form, `type="file"`)
		if fi < 0 {
			t.Errorf("%s: no <input type=\"file\"> inside the upload form", name)
			continue
		}
		inputStart := strings.LastIndex(form[:fi], "<input")
		inputEnd := strings.Index(form[fi:], ">")
		if inputStart < 0 || inputEnd < 0 {
			t.Errorf("%s: malformed file input", name)
			continue
		}
		input := form[inputStart : fi+inputEnd]
		if !strings.Contains(input, `name="`) {
			t.Errorf("%s: the file input has no name attribute, so new FormData(form) "+
				"omits it entirely and the request carries no file. Input: %s", name, input)
		}
	}
}

func TestOfferPhotoUploadSatisfiesDatastarsFormContract(t *testing.T) {
	d := OfferDetail{
		Row:        OfferRow{Offer: core.Offer{ID: "offer-1", Title: "Vintage brass desk lamp"}},
		PublicBase: "https://warehouse.example.com",
	}
	assertFormUploadContract(t, "offer photos card", renderString(t, OfferPhotosCard(d)))
}

func TestSubmissionPhotoUploadSatisfiesDatastarsFormContract(t *testing.T) {
	d := SubmissionDetail{
		Submission: core.Submission{
			ID:     "sub-1",
			Title:  "Oak dining chair",
			Status: core.SubmissionNew,
		},
		Currencies: core.KnownCurrencies(),
	}
	assertFormUploadContract(t, "submission photos card", renderString(t, SubmissionBody(d)))
}

// TestPhotoThumbnailsOpenTheOriginal pins the only way to magnify a photograph.
//
// The thumbnail is rendered at 104px with object-fit:cover, so it is a CROP —
// the strip cannot show the whole image at any zoom level, and on a phone a bare
// <img> leaves no way to see the rest of it. Linking to the stored original
// hands magnification to the browser's own image viewer, which does pinch-zoom,
// double-tap and save without a line of our code.
//
// This is asserted rather than left to review because it is invisible to every
// server-side check: an <img> and a linked <img> serve identical bytes, return
// identical status codes, and differ only in what a person can do with them.
func TestPhotoThumbnailsOpenTheOriginal(t *testing.T) {
	photo := core.Photo{ID: "11111111-1111-1111-1111-111111111111", ContentType: "image/jpeg"}

	cases := map[string]string{
		"offer photos": renderString(t, OfferPhotosCard(OfferDetail{
			Row: OfferRow{Offer: core.Offer{ID: "o1", Title: "Lamp", Photos: []core.Photo{photo}}},
		})),
		"submission photos": renderString(t, SubmissionBody(SubmissionDetail{
			Submission: core.Submission{ID: "s1", Title: "Chair", Photos: []core.Photo{photo}},
		})),
	}

	for name, html := range cases {
		i := strings.Index(html, `<img src="`+photo.PublicPath())
		if i < 0 {
			t.Errorf("%s: the thumbnail is missing entirely", name)
			continue
		}
		// The nearest preceding tag must be the opening anchor.
		open := strings.LastIndex(html[:i], "<a ")
		if open < 0 {
			t.Errorf("%s: the thumbnail is not wrapped in a link, so there is no way to "+
				"see the photograph at full size — the tile is a crop", name)
			continue
		}
		anchor := html[open:i]
		if !strings.Contains(anchor, `href="`+photo.PublicPath()+`"`) {
			t.Errorf("%s: the link does not point at the stored original: %s", name, anchor)
		}
		// Same-tab navigation would discard unsaved signal state on the offer
		// editor — the title, description and prices the operator has typed.
		if !strings.Contains(anchor, `target="_blank"`) {
			t.Errorf("%s: the photo link is not target=_blank, so opening it would "+
				"navigate away and discard unsaved edits", name)
		}
		if !strings.Contains(anchor, `rel="noopener"`) {
			t.Errorf("%s: target=_blank without rel=noopener", name)
		}
	}
}

// TestRemoveIsNotNestedInsideThePhotoLink guards both a mobile hazard and valid
// HTML: a <button> inside an <a> is invalid, and an overlaid destructive control
// sits exactly where a thumb taps to view.
func TestRemoveIsNotNestedInsideThePhotoLink(t *testing.T) {
	html := renderString(t, OfferPhotosCard(OfferDetail{
		Row: OfferRow{Offer: core.Offer{
			ID: "o1", Title: "Lamp",
			Photos: []core.Photo{{ID: "11111111-1111-1111-1111-111111111111", ContentType: "image/jpeg"}},
		}},
	}))

	del := strings.Index(html, `class="photo-del"`)
	if del < 0 {
		t.Fatal("no remove control found, so this test asserts nothing")
	}
	openA := strings.LastIndex(html[:del], "<a ")
	closeA := strings.LastIndex(html[:del], "</a>")
	if openA > closeA {
		t.Error("the remove button is nested inside the photo link: invalid HTML, and " +
			"a destructive control sitting on the tap target that opens the photo")
	}
}

// TestNoFormsOutsideFileUpload guards the other half of the team rule: forms are
// permitted for file upload and nowhere else.
//
// It matters because the exception is easy to over-apply once one form exists —
// and a form around ordinary inputs would take the browser away from the page on
// a stray Enter, rather than sending a datastar action.
func TestNoFormsOutsideFileUpload(t *testing.T) {
	cases := map[string]templ.Component{
		"auth":     AuthPage(Auth{}),
		"intake":   IntakeScreen(nil),
		"offers":   OffersScreen(OfferList{Currencies: core.KnownCurrencies()}),
		"carts":    CartsScreen(Carts{}),
		"users":    UsersScreen(Users{}),
		"newPlace": NewLocationScreen(nil, ""),
		"submit":   SubmitScreen(Submit{Currencies: core.KnownCurrencies()}),
	}
	for name, c := range cases {
		html := renderString(t, c)
		if strings.Contains(html, "<form") {
			t.Errorf("%s renders a <form>; this application binds inputs to signals "+
				"instead, and file upload is the only exception", name)
		}
	}
}
