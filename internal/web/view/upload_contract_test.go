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
