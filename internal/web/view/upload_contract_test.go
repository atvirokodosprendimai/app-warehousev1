package view

import (
	"context"
	"regexp"
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

// uploadSurfaces renders the two file-upload cards, which are the subject of
// ADR-015.
func uploadSurfaces(t *testing.T) map[string]string {
	t.Helper()
	photo := core.Photo{ID: "11111111-1111-1111-1111-111111111111", ContentType: "image/jpeg"}
	return map[string]string{
		"offer photos card": renderString(t, OfferPhotosCard(OfferDetail{
			Row: OfferRow{Offer: core.Offer{ID: "o1", Title: "Lamp", Photos: []core.Photo{photo}}},
		})),
		"submission photos card": renderString(t, SubmissionBody(SubmissionDetail{
			Submission: core.Submission{ID: "sub-1", Title: "Oak chair", Status: core.SubmissionNew},
			Currencies: core.KnownCurrencies(),
		})),
	}
}

// uploadIndicatorSignal is the signal ADR-015 puts on both uploads.
//
// It is deliberately NOT `_busy`: datastar signals are global and flattened, so
// reusing that name would spin every other spinner on the page during an upload.
const uploadIndicatorSignal = "_uploading"

// TestPhotoUploadShowsABusyState pins the feedback ADR-015 exists to provide.
//
// A photograph off a phone camera is several megabytes over a mobile uplink, so
// this is the longest request the application makes — and until ADR-015 it was
// the ONLY interactive control with no busy state at all. The reported symptom
// was exactly what that produces: "with slow mobile i have no idea its doing
// something or not", and a user who cannot tell a slow upload from a dead
// control taps again.
//
// Three things have to be true together, and each fails differently:
//   - the input carries data-indicator, or no signal is ever created;
//   - it carries data-attr:disabled on that signal, or a second tap starts a
//     duplicate upload;
//   - something READS the signal, or the indicator is wired to nothing and the
//     markup looks correct while showing the user precisely nothing.
func TestPhotoUploadShowsABusyState(t *testing.T) {
	sig := uploadIndicatorSignal

	for name, html := range uploadSurfaces(t) {
		fi := strings.Index(html, `type="file"`)
		if fi < 0 {
			t.Errorf("%s: no <input type=\"file\"> at all — this test is asserting nothing", name)
			continue
		}
		start := strings.LastIndex(html[:fi], "<input")
		end := strings.Index(html[fi:], ">")
		if start < 0 || end < 0 {
			t.Errorf("%s: malformed file input", name)
			continue
		}
		input := html[start : fi+end]

		if !strings.Contains(input, "data-indicator:"+sig) {
			t.Errorf("%s: the file input carries no data-indicator:%s, so nothing creates the "+
				"in-flight signal and the upload gives no feedback on a slow connection. "+
				"Input: %s", name, sig, input)
		}
		if !strings.Contains(input, `data-attr:disabled="$`+sig+`"`) {
			t.Errorf("%s: the file input is not disabled while $%s is true, so a second tap "+
				"during a slow upload starts a duplicate one. Input: %s", name, sig, input)
		}
		if !strings.Contains(html, `data-show="$`+sig+`"`) {
			t.Errorf("%s: nothing reads $%s, so the indicator is wired to NOTHING — the "+
				"attribute is present and the user still sees no change", name, sig)
		}
		if !strings.Contains(html, "Uploading") {
			t.Errorf("%s: no busy text. A spinner alone says something is happening; the word "+
				"says what", name)
		}
	}
}

// indicatorKeyRe matches the colon-key form, where the signal name is part of
// the ATTRIBUTE NAME — which is the form this codebase uses everywhere.
var indicatorKeyRe = regexp.MustCompile(`data-indicator:([A-Za-z0-9_-]+)`)

// indicatorValRe matches the value form, where the name is the attribute VALUE.
var indicatorValRe = regexp.MustCompile(`data-indicator="([^"]*)"`)

// everyIndicatorSurface renders the screens that carry indicators, so the two
// invariants below hold for the whole package rather than only for the uploads.
func everyIndicatorSurface(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{
		"auth":       renderString(t, AuthPage(Auth{})),
		"offers":     renderString(t, OffersScreen(OfferList{Currencies: core.KnownCurrencies()})),
		"carts":      renderString(t, CartsScreen(Carts{})),
		"users":      renderString(t, UsersScreen(Users{})),
		"newPlace":   renderString(t, NewLocationScreen(nil, "")),
		"submit":     renderString(t, SubmitScreen(Submit{Currencies: core.KnownCurrencies()})),
		"offerPhoto": renderString(t, OfferPhotosCard(OfferDetail{Row: OfferRow{Offer: core.Offer{ID: "o1"}}})),
	}
	for k, v := range uploadSurfaces(t) {
		out[k] = v
	}
	return out
}

// TestEveryIndicatorNameSurvivesHTMLLowercasing refuses the defect that took a
// sibling project's admin page down in production on 2026-08-31.
//
// `data-indicator:<name>` puts the signal name in the ATTRIBUTE NAME, and the
// HTML parser lowercases attribute names. A camelCase name therefore creates a
// DIFFERENT signal from the one every consumer reads: datastar wrote
// `_plansaving` while the markup read `$_planSaving`, so nothing ever cleared
// the busy flag — and because `data-attr:disabled` on an undefined signal sets
// the attribute, and `disabled` is a boolean attribute where presence alone
// disables, every control on the page rendered dead and stayed dead.
//
// This codebase is currently safe only because every indicator it happens to
// use is already lowercase. That is luck, and this test is what replaces it.
func TestEveryIndicatorNameSurvivesHTMLLowercasing(t *testing.T) {
	found := 0
	for name, html := range everyIndicatorSurface(t) {
		for _, m := range indicatorKeyRe.FindAllStringSubmatch(html, -1) {
			found++
			if m[1] != strings.ToLower(m[1]) {
				t.Errorf("%s: data-indicator:%s puts a name with an uppercase letter in an "+
					"ATTRIBUTE NAME. HTML lowercases it, so datastar creates $%s while every "+
					"consumer reads $%s — different signals, and nothing reports it. Use an "+
					"all-lowercase name, or the value form data-indicator=%q",
					name, m[1], strings.ToLower(m[1]), m[1], m[1])
			}
		}
	}
	if found == 0 {
		t.Fatal("no data-indicator attribute was found on any surface, so this test is " +
			"asserting nothing — either the indicators moved or the rendering changed")
	}
}

// TestEveryIndicatorSignalHasAConsumer refuses an indicator wired to nothing.
//
// data-indicator maintains a signal; it does not itself display anything. An
// indicator with no data-show or data-attr reading it is markup that looks like
// feedback and produces none, and no server-side check can see the difference.
// In the same sibling project the most-used screen in the product carried THREE
// such attributes with zero consumers.
func TestEveryIndicatorSignalHasAConsumer(t *testing.T) {
	for name, html := range everyIndicatorSurface(t) {
		signals := map[string]bool{}
		for _, m := range indicatorKeyRe.FindAllStringSubmatch(html, -1) {
			// What datastar actually creates is the lowercased name.
			signals[strings.ToLower(m[1])] = true
		}
		for _, m := range indicatorValRe.FindAllStringSubmatch(html, -1) {
			signals[m[1]] = true
		}
		for sig := range signals {
			if !strings.Contains(html, "$"+sig) {
				t.Errorf("%s: data-indicator creates $%s and nothing on this surface reads it. "+
					"An indicator with no consumer renders no feedback at all, and the markup "+
					"looks correct either way", name, sig)
			}
		}
	}
}
