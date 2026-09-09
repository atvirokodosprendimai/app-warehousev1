package view

import (
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
)

// fieldsDetail builds an offer editor read model whose category asks the given
// questions, resolved the way internal/taxonomy resolves them: root first.
func fieldsDetail(fields ...core.OfferField) OfferDetail {
	return OfferDetail{
		Page: Page{Title: "A turbocharger"},
		Row: OfferRow{Offer: core.Offer{
			ID:         "o1",
			SKU:        "WH0000001",
			Title:      "A turbocharger",
			Status:     core.StatusDraft,
			Quantity:   1,
			CategoryID: "c-turbo",
		}},
		Categories: []core.Category{
			{ID: "c-car", Code: "CAR", Path: "CAR", Name: "Car parts"},
			{ID: "c-engine", Code: "ENGINE", Path: "CAR/ENGINE", Name: "Engine"},
			{ID: "c-turbo", Code: "TURBO", Path: "CAR/ENGINE/TURBO", Name: "Turbocharger"},
		},
		Fields: fields,
		// ⚠ THE FULL SET, because that is what the handler always builds and what
		// the screen's signal seeding assumes (ADR-022 T4). A fixture that left
		// this empty would render a Marketplace card with no controls in it, which
		// is how this fixture first met the new card.
		Marketplace: marketplaceChoiceFixtures(),
	}
}

// marketplaceChoiceFixtures is one entry per must-have, deliberately mixed: the
// category has a list to pick from and the other two have none, so one fixture
// exercises BOTH arms of the card — the dropdown and the free-text escape a
// warehouse falls back to before an administrator has filled the lists.
func marketplaceChoiceFixtures() []MarketplaceChoice {
	return []MarketplaceChoice{
		{
			Field: core.MarketplaceCategory, Title: "eBay category",
			Signal: "offerEbayCategory", Attr: "offer-ebay-category",
			Options: []core.MarketplaceOption{
				{ID: "opt-1", Profile: "ebay", Field: core.MarketplaceCategory,
					Value: "20081", Label: "Antiques"},
				{ID: "opt-2", Profile: "ebay", Field: core.MarketplaceCategory,
					Value: "11450", Label: "Clothing"},
			},
			Default: "11450",
		},
		{
			Field: core.MarketplaceCondition, Title: "Condition",
			Signal: "offerEbayCondition", Attr: "offer-ebay-condition",
			Default: "3000",
		},
		{
			Field: core.MarketplaceLocation, Title: "Dispatches from",
			Signal: "offerEbayLocation", Attr: "offer-ebay-location",
			Default: "Kaunas",
		},
	}
}

// field is a shorthand for one resolved question.
func field(id, path, label string, kind core.FieldKind) core.OfferField {
	return core.OfferField{
		CategoryField: core.CategoryField{
			ID: id, CategoryPath: path, Code: strings.ToLower(label), Label: label, Kind: kind,
		},
	}
}

// TestAnOfferIsAskedItsAncestorsQuestions is the visible half of ADR-021: an
// offer filed three levels down is asked all three levels' questions, root
// first, each labelled with the level it came from.
func TestAnOfferIsAskedItsAncestorsQuestions(t *testing.T) {
	d := fieldsDetail(
		field("f-vin", "CAR", "VIN", core.FieldText),
		field("f-code", "CAR/ENGINE", "Engine code", core.FieldText),
		field("f-num", "CAR/ENGINE/TURBO", "Turbo number", core.FieldText),
	)
	html := renderString(t, OfferScreen(d))

	for _, label := range []string{"VIN", "Engine code", "Turbo number"} {
		if !strings.Contains(html, label) {
			t.Errorf("the editor does not ask %q; an inherited question that is not "+
				"rendered is a question nobody answers", label)
		}
	}

	// DOM order is reading order, and root-first is the order the record chose.
	vin := strings.Index(html, "VIN")
	code := strings.Index(html, "Engine code")
	num := strings.Index(html, "Turbo number")
	if !(vin < code && code < num) {
		t.Errorf("questions render in the wrong order (VIN %d, Engine code %d, Turbo "+
			"number %d); the general question comes before the specific one, which "+
			"is the order a person thinks in", vin, code, num)
	}

	// Each group says which level it came from, so an operator can see why they
	// are being asked and where to change it.
	for _, level := range []string{"CAR", "ENGINE", "TURBO"} {
		if !strings.Contains(html, ">"+level+"</h3>") {
			t.Errorf("no group heading for level %q; an inherited question with no "+
				"provenance looks like one this category defined", level)
		}
	}
}

// TestEachFieldKindRendersItsOwnControl checks that the five kinds are five
// controls rather than five text boxes.
func TestEachFieldKindRendersItsOwnControl(t *testing.T) {
	num := field("f-km", "CAR", "Mileage", core.FieldNumber)
	num.Unit = "km"
	choice := field("f-grade", "CAR", "Grade", core.FieldChoice)
	choice.Options = "A\nB\nC"

	d := fieldsDetail(
		field("f-note", "CAR", "Note", core.FieldText),
		field("f-desc", "CAR", "Damage", core.FieldLongText),
		num,
		choice,
		field("f-oem", "CAR", "Original part", core.FieldBool),
	)
	html := renderString(t, OfferScreen(d))

	if !strings.Contains(html, `<textarea id="cf-f-desc"`) {
		t.Error("a long-text field did not render a textarea, so a paragraph has to " +
			"be typed into a single line")
	}
	if !strings.Contains(html, `id="cf-f-km"`) || !strings.Contains(html, `inputmode="numeric"`) {
		t.Error("a number field did not render a numeric input, so a phone offers " +
			"letters for a mileage")
	}
	if !strings.Contains(html, ">km</span>") {
		t.Error("the unit is not shown beside the number; it is stored separately " +
			"precisely so it can be displayed without being typed into the value")
	}
	if !strings.Contains(html, `<select id="cf-f-grade"`) {
		t.Error("a choice field did not render a select")
	}
	for _, opt := range []string{">A<", ">B<", ">C<"} {
		if !strings.Contains(html, opt) {
			t.Errorf("the choice is missing option %q", opt)
		}
	}
	if !strings.Contains(html, `<input id="cf-f-oem" type="checkbox"`) {
		t.Error("a yes/no field did not render a checkbox")
	}

	// Every control binds a signal named for its field id, lowercase, because
	// HTML lowercases attribute names and a camelCase suffix would bind a
	// different signal than the seed writes.
	for _, id := range []string{"f-note", "f-desc", "f-km", "f-grade", "f-oem"} {
		bind := "data-bind:" + FieldSignal(id)
		if !strings.Contains(html, bind) {
			t.Errorf("no %q in the markup; an unbound control looks editable and "+
				"saves nothing", bind)
		}
	}
}

// TestAnUnknownKindRendersAsText is the degradation ADR-021 records as a Risk.
//
// A row carrying a kind this binary does not know is possible the moment a newer
// version writes one, and the page must still render: a field that reads as text
// is recoverable, a screen that will not load is not.
func TestAnUnknownKindRendersAsText(t *testing.T) {
	d := fieldsDetail(field("f-weird", "CAR", "From the future", core.FieldKind("colour")))
	html := renderString(t, OfferScreen(d))

	if !strings.Contains(html, "From the future") {
		t.Fatal("a field of an unrecognised kind vanished from the page entirely")
	}
	if !strings.Contains(html, `<input id="cf-f-weird" class="input" type="text"`) {
		t.Error("an unrecognised kind did not fall back to a text input; the default " +
			"branch of the kind switch is load-bearing, not tidiness")
	}
}

// TestTheFieldCardSitsAfterPhotosAndBeforeStatus keeps ADR-020's ordering intact
// across the insertion of a new card.
func TestTheFieldCardSitsAfterPhotosAndBeforeStatus(t *testing.T) {
	d := fieldsDetail(field("f-vin", "CAR", "VIN", core.FieldText))
	html := renderString(t, OfferScreen(d))

	photos := strings.Index(html, "<h2>Photos</h2>")
	fields := strings.Index(html, "Details for this category")
	status := strings.LastIndex(html, "Status")

	if photos < 0 || fields < 0 || status < 0 {
		t.Fatalf("a card is missing entirely (photos %d, fields %d, status %d)",
			photos, fields, status)
	}
	if !(photos < fields) {
		t.Error("the questions card comes before the photographs; ADR-020 puts the " +
			"camera first because the operator is still holding the object")
	}
	if !(fields < status) {
		t.Error("the questions card comes after Status; ADR-020 puts the publish " +
			"decision last, after everything that records what the thing IS")
	}
}

// TestTheCategoryPickerIsNotTheMarketplaceCategory pins the distinction the
// parent ADR names as its likeliest source of a wrong-field bug.
func TestTheCategoryPickerIsNotTheMarketplaceCategory(t *testing.T) {
	d := fieldsDetail()
	html := renderString(t, OfferScreen(d))

	if !strings.Contains(html, "data-bind:offer-category") {
		t.Fatal("no taxonomy category picker on the editor")
	}
	if !strings.Contains(html, "data-bind:offer-ebay-category") {
		t.Fatal("the eBay category control disappeared; the two are separate fields " +
			"and this task must not have merged them")
	}
	// Both pickers exist, and the taxonomy one offers the tree.
	if !strings.Contains(html, "CAR/ENGINE/TURBO") {
		t.Error("the picker does not offer the tree by path; path order is tree " +
			"order, which is what makes an indented list readable")
	}
}

// TestAnUncategorisedOfferIsAskedNothing keeps a category optional, which
// ADR-004 and ADR-019 both require of the intake flow.
func TestAnUncategorisedOfferIsAskedNothing(t *testing.T) {
	d := fieldsDetail()
	d.Row.Offer.CategoryID = ""
	html := renderString(t, OfferScreen(d))

	if strings.Contains(html, "Details for this category") {
		t.Error("an offer with no category renders an empty questions card; a card " +
			"with nothing in it reads as something still loading")
	}
	if !strings.Contains(html, "— not filed —") {
		t.Error("the picker offers no way back to no category; un-filing something " +
			"is a legitimate edit and there is no other control for it")
	}

	// ⚠ THE WRAPPER IS STILL THERE, AND THAT IS THE WHOLE OF THE DEFECT M
	// REPORTED. An SSE patch replaces an element that is already in the document,
	// so a page rendering NOTHING for an uncategorised offer can never be sent the
	// questions when one is chosen — the operator files the offer and is asked
	// nothing until they reload the page by hand.
	if !strings.Contains(html, `id="offer-fields"`) {
		t.Error("an uncategorised offer renders no patch target for the questions " +
			"card, so choosing a category cannot make the questions appear")
	}
}

// TestChoosingACategoryAsksItsQuestionsWithoutASecondSave pins the live half of
// the same defect.
//
// ⚠ ADR-021-T3's STEP S6 ALREADY CLAIMED THIS — "re-render the card when the
// category changes so the questions follow the filing" — and nothing asserted
// it. The picker wrote its value into a signal and stopped; the questions
// arrived on the next full page load. Same shape as ADR-020-T1's Reachability
// rung 3, and found the same way: by M doing the job.
func TestChoosingACategoryAsksItsQuestionsWithoutASecondSave(t *testing.T) {
	d := fieldsDetail(field("f-vin", "CAR", "VIN", core.FieldText))
	html := renderString(t, OfferScreen(d))

	// Dynamic attribute, so templ escapes the apostrophes.
	want := `data-on:change="@post(&#39;/offers/` + d.Row.Offer.ID + `&#39;)"`
	if !strings.Contains(html, want) {
		t.Error("the category picker does not apply when it changes, so choosing a " +
			"category shows the operator nothing until they save the card and " +
			"reload the page by hand")
	}
}

// TestTheTwoSignalSeedsAgree pins the rule the page and the stream have to share.
//
// The page seeds its signals as a JS object literal; the stream sends them as
// JSON when the card appears mid-session. A yes/no has to be a real boolean by
// BOTH routes — a checkbox bound to the string "" renders TICKED — or the same
// unanswered question answers itself depending on how it reached the page.
func TestTheTwoSignalSeedsAgree(t *testing.T) {
	yes := field("f-oem", "CAR", "Original part", core.FieldBool)
	text := field("f-vin", "CAR", "VIN", core.FieldText)
	text.Value = "WVW123"
	d := fieldsDetail(yes, text)

	if seed := offerSignals(d); !strings.Contains(seed, FieldSignal("f-oem")+": false") {
		t.Errorf("the page seed does not carry an unanswered yes/no as a JS boolean: %s", seed)
	}

	sent := FieldSignalValues(d)
	if got, ok := sent[FieldSignal("f-oem")].(bool); !ok || got {
		t.Errorf("the stream sends an unanswered yes/no as %#v, not the boolean false "+
			"the page seeds; a checkbox bound to a string renders ticked",
			sent[FieldSignal("f-oem")])
	}
	if got := sent[FieldSignal("f-vin")]; got != "WVW123" {
		t.Errorf("the stream lost a stored text answer: %#v", got)
	}
}

// TestFieldSignalIsLowercaseAndIdentifierSafe pins the naming rule that has
// already cost this project debugging rounds once, on the price fields.
func TestFieldSignalIsLowercaseAndIdentifierSafe(t *testing.T) {
	got := FieldSignal("A1B2C3D4-5E6F-7080-9012-3456789ABCDE")
	if got != "fa1b2c3d45e6f708090123456789abcde" {
		t.Fatalf("FieldSignal = %q; it must strip the hyphens (a signal name is an "+
			"identifier) and lowercase the result (HTML lowercases attribute names, "+
			"so data-bind:fAB binds \"fab\" and misses the seeded signal)", got)
	}
	if strings.ContainsAny(got, "-ABCDEF") {
		t.Errorf("FieldSignal(%q) still carries a hyphen or an uppercase letter", got)
	}
}
