package view

import (
	"os"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
)

// This file pins the CHOICE between taking a photograph and picking one.
//
// M, 2026-09-07: the upload "needs to choose if on mobile, want to make few
// photos or choose from gallery".
//
// ⚠ The choice cannot be expressed on a single input. `capture` opens the camera
// and removes the gallery as an option; omitting it hands the decision to the
// operating system, and an `accept` naming specific MIME types — which this
// upload has always used — is exactly what makes iOS drop "Take Photo" from that
// sheet. So the difference between the two controls is a handful of attributes
// that look like noise, are invisible on a desktop, and decide the whole
// behaviour on the device M actually uses.

func photosCardHTML(t *testing.T) string {
	t.Helper()
	return renderString(t, OfferPhotosCard(OfferDetail{
		Row:        OfferRow{Offer: core.Offer{ID: "offer-1", Title: "Enamel sign"}},
		PublicBase: "https://warehouse.example.com",
	}))
}

// TestBothAPhotographAndAGalleryPickAreOffered refuses a single control that
// silently picks one of the two for the operator.
func TestBothAPhotographAndAGalleryPickAreOffered(t *testing.T) {
	html := photosCardHTML(t)

	inputs := fileInputs(html)
	if len(inputs) != 2 {
		t.Fatalf("the photos card offers %d file input(s), want 2 — one to take a "+
			"photograph and one to choose from the gallery. Without both, the operating "+
			"system decides which the operator gets and there is no way to ask for the "+
			"other", len(inputs))
	}

	var camera, gallery string
	for _, in := range inputs {
		if strings.Contains(in, "capture=") {
			camera = in
		} else {
			gallery = in
		}
	}
	if camera == "" {
		t.Fatal("no input carries `capture`, so nothing opens the camera directly and " +
			"'take a photo' depends entirely on the OS offering it in a sheet")
	}
	if gallery == "" {
		t.Fatal("every input carries `capture`, so the gallery is unreachable — `capture` " +
			"REMOVES the picker rather than adding the camera beside it")
	}

	// environment = the rear camera. `user` would open the selfie camera, which
	// is never what somebody photographing stock on a shelf wants.
	if !strings.Contains(camera, `capture="environment"`) {
		t.Errorf("the camera input does not request the rear camera, so it opens the "+
			"front-facing one to photograph a shelf. Input: %s", camera)
	}
	// `multiple` alongside `capture` is ignored by every platform; carrying it
	// would advertise a capability the control does not have.
	if strings.Contains(camera, "multiple") {
		t.Errorf("the camera input claims `multiple`, which no platform honours beside "+
			"`capture` — taking several is tapping it several times. Input: %s", camera)
	}
	// The gallery is where "a few" actually happens.
	if !strings.Contains(gallery, "multiple") {
		t.Errorf("the gallery input is not `multiple`, so a shelf of photographs has to be "+
			"chosen one at a time. Input: %s", gallery)
	}

	// Both must post to the same place, or one of them is wired to nothing.
	for _, in := range inputs {
		if !strings.Contains(in, "/photos&#39;, {contentType: &#39;form&#39;}") &&
			!strings.Contains(in, "/photos', {contentType: 'form'}") {
			t.Errorf("a file input does not post the upload as form encoding, so its bytes "+
				"are dropped. Input: %s", in)
		}
	}

	// scripts/smoke.sh greps for `type="file" name="photos"` as ADJACENT
	// attributes on the served page. templ emits them in source order, so a
	// reordering here turns that assertion green against nothing.
	if n := strings.Count(html, `type="file" name="photos"`); n != 2 {
		t.Errorf("found %d inputs with type and name adjacent, want 2 — scripts/smoke.sh "+
			"matches that exact pair, and it stops proving anything if they separate", n)
	}
}

// TestTheCameraControlIsOfferedOnlyToATouchPointer binds the stylesheet to the
// markup, the same way the responsive listing does.
//
// The camera control is rendered on every viewport and HIDDEN by CSS, so if the
// rule is renamed or dropped the markup still looks right and a desktop grows a
// second control that does the same thing as the first.
func TestTheCameraControlIsOfferedOnlyToATouchPointer(t *testing.T) {
	html := photosCardHTML(t)
	css, err := os.ReadFile("../assets/app.css")
	if err != nil {
		t.Fatalf("cannot read the stylesheet this test exists to check: %v", err)
	}
	sheet := string(css)

	if !strings.Contains(html, "drop-camera") {
		t.Fatal("no element carries drop-camera, so the stylesheet rule below governs nothing")
	}
	if !strings.Contains(sheet, ".drop-camera { display: none; }") {
		t.Error("the stylesheet does not hide the camera control by default, so a desktop " +
			"shows a capture button whose behaviour browsers honour inconsistently")
	}
	if !strings.Contains(sheet, "@media (pointer: coarse)") {
		t.Error("nothing reveals the camera control on a touch pointer, so it is hidden " +
			"everywhere — including on the phone it exists for")
	}
	// The reveal must come AFTER the hide: same specificity, so order decides.
	hide := strings.Index(sheet, ".drop-camera { display: none; }")
	show := strings.LastIndex(sheet, ".drop-camera { display: flex; }")
	if show < 0 {
		t.Fatal("no rule ever shows the camera control")
	}
	if show < hide {
		t.Error("the rule showing the camera control comes BEFORE the rule hiding it, and " +
			"they have equal specificity — so the hide wins and the phone never sees it")
	}
}
