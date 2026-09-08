package export

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
)

// recarAddImageColumns is how many "Add image" columns follow "Main image" in
// recar.lt's own export template, counted from the file they sent.
//
// ⚠ THE WIDTH IS FIXED RATHER THAN DERIVED FROM THE BATCH, which is the opposite
// of the custom-field tail's rule (see [customColumns]). The reason is that this
// header is not ours: it is recar's template, and their importer is known to
// accept exactly this shape because it is the shape they exported. A narrower
// header — 18 columns for a batch whose widest offer has 18 photographs — has
// never been put in front of their importer, and guessing that it would be
// accepted is the kind of assumption this package exists to refuse.
const recarAddImageColumns = 26

// recarMaxPhotos is how many photographs one row can carry: the main image plus
// the additional ones. An offer with more is refused by name rather than
// silently truncated — a listing quietly missing its last four pictures is
// invisible until somebody opens it on the marketplace.
const recarMaxPhotos = 1 + recarAddImageColumns

// recarCurrency is the only currency this profile publishes.
//
// ⚠ NOT A DEFAULT — A CONSTRAINT. The price column's header is the literal
// "Price €", so the currency is baked into the header text exactly as eBay's is
// baked into its *Action token (see [ebayHeader]). A dollar-priced feed under
// this header does not fail: recar reads the header, believes it, and sells at
// euro numbers.
const recarCurrency = "EUR"

// The two values recar's "Stan" (condition) column takes, in their spelling.
const (
	recarConditionNew  = "Nowy"
	recarConditionUsed = "Używany"
)

// The CAR starter template's field codes (ADR-021 T5), which is where these
// answers come from. That template was written from recar.lt's own intake form,
// so the mapping below is a correspondence rather than a coincidence.
//
// ⚠ CODE, NEVER LABEL. [core.CategoryField.Label] is what an operator sees and
// may rename at will; Code is the stable key that never moves. A profile keyed
// on labels would stop publishing the brand the day somebody typed "Marke".
const (
	recarCodeMake     = "make"
	recarCodeModel    = "model"
	recarCodeOEMMain  = "oem_main"
	recarCodeOEMOther = "oem_other"
	recarCodeYear     = "year"
)

// recarHeader returns recar.lt's column set, transcribed from their export.
//
// The names are Polish and are reproduced exactly, diacritics included: they are
// the importer's keys, not prose, and "Numer katalogowy czesci" is a different
// string from "Numer katalogowy części".
func recarHeader() []string {
	h := []string{
		"Brand",
		"Model",
		"Numer katalogowy części",
		"Numer katalogowy oryginału",
		"Numery katalogowe zamienników",
		"Producent części",
		"Stan",
		"Price €",
		"Title",
		"Model Year",
		"Main image",
	}
	for range recarAddImageColumns {
		h = append(h, "Add image")
	}
	return h
}

// Recar renders offers as a recar.lt parts CSV.
//
// The zero value is ready to use and carries no state, so one instance is safe
// to share across goroutines.
type Recar struct{}

// Name returns "recar", the profile's registry name.
func (Recar) Name() string { return "recar" }

// Write renders offers as the CSV recar.lt's importer ingests.
//
// Every item is exactly one row, and its photographs are spread across a FIXED
// run of image columns — a third shape again from eBay's one pipe-joined cell
// and Shopify's extra row per image. Absorbing that asymmetry is what these
// profiles are for.
//
// # This profile has no custom-field tail, deliberately
//
// eBay and Shopify append a column per exported custom field ([customColumns]),
// because both tolerate columns they do not recognise. This one does not: recar's
// template is a fixed form, and a field that matters to it already HAS a named
// column below. An exported field with no column here therefore does not reach
// recar at all — which is a property of their template rather than a gap in
// ADR-021, and the reason the mapping is written out cell by cell where it can be
// read.
//
// Two of their columns are left empty in every row because nothing in this
// system holds the answer; see the row construction below.
func (Recar) Write(w io.Writer, offers []core.Offer, opt Options) error {
	if err := opt.validateCommon(); err != nil {
		return fmt.Errorf("recar: %w", err)
	}
	if cur := opt.currency(); cur != recarCurrency {
		return fmt.Errorf("recar: %w: this profile publishes %s only, but this export is "+
			"configured for %s. The price column's header is the literal \"Price €\", so a "+
			"%s feed under it is read as euros and every row is mis-priced with no error "+
			"anywhere", ErrOptions, recarCurrency, cur, cur)
	}
	items, err := exportable(offers, opt)
	if err != nil {
		return fmt.Errorf("recar: %w", err)
	}

	rows := make([][]string, 0, len(items))
	var problems []error
	for _, o := range items {
		if len(o.Photos) > recarMaxPhotos {
			problems = append(problems, fmt.Errorf(
				"%w: %s has %d photographs and recar's template holds %d; remove the extras "+
					"rather than letting the last ones drop off a listing nobody thinks to "+
					"count", ErrIncomplete, o.SKU, len(o.Photos), recarMaxPhotos))
			continue
		}
		price, err := o.Shop.Decimal()
		if err != nil {
			problems = append(problems, fmt.Errorf("%w: %s: %w", ErrIncomplete, o.SKU, err))
			continue
		}
		row := []string{
			recarField(o, recarCodeMake),  // Brand — the donor vehicle's make
			recarField(o, recarCodeModel), // Model — the donor vehicle's model
			recarField(o, recarCodeOEMMain),
			// ⚠ "Numer katalogowy oryginału" — the ORIGINAL part's catalogue
			// number, which an aftermarket part quotes to say what it replaces.
			// Nothing here holds it: the CAR template has one OEM code and a free
			// list of alternates, not this third distinct thing. Empty is the
			// honest answer, and it is what recar's own sample carries.
			"",
			recarField(o, recarCodeOEMOther),
			// ⚠ "Producent części" — the PART's manufacturer, which is not the
			// vehicle's make: recar's own sample pairs brand "Mercedes-Benz" with
			// producer "Mercedes-Benz OE". No field holds it either.
			"",
			recarCondition(o),
			price,   // Price € — Shop, never Owner
			o.Title, // Title
			recarField(o, recarCodeYear),
		}
		rows = append(rows, append(row, recarImages(o, opt.BaseURL)...))
	}
	if len(problems) > 0 {
		return fmt.Errorf("recar: %w", errors.Join(problems...))
	}
	return writeCSV(w, "recar", recarHeader(), rows)
}

// recarField returns o's answer to the category question with this code, or an
// empty string when the offer has no such field.
//
// ⚠ AN UNEXPORTED FIELD RETURNS EMPTY, and that is the whole point rather than an
// oversight. [core.CategoryField.Export] is off by default and means "this does
// not leave the building"; a profile that read the value anyway would be a hole
// in that flag, and the operator who unticked it would never learn the answer was
// still being published.
//
// The first field carrying the code wins. Codes are unique within a category's
// resolved set, so a second match would mean an ancestor and a descendant asking
// the same question — in which case the nearer one, which [core.Offer.Fields]
// orders last, is not what a caller of this function means.
func recarField(o core.Offer, code string) string {
	for _, f := range o.Fields {
		if f.Code != code {
			continue
		}
		if !f.Export {
			return ""
		}
		return f.Display()
	}
	return ""
}

// recarCondition maps the offer's free-text condition onto the two values recar's
// "Stan" column accepts.
//
// Used is the default because almost everything in a second-hand warehouse is —
// the same reasoning that gives [DefaultConditionID] its value. A wrong condition
// is printed on the listing where somebody notices it, which is what makes a
// default defensible here and not for a category.
//
// ⚠ WHOLE WORDS, NOT SUBSTRINGS, AND A HYPHEN DOES NOT BREAK ONE. "renewed" and
// "new" share four letters, and "as-new" is the ordinary way of writing that
// something is NOT new; a substring test publishes both as factory-new, and a
// splitter that treated "-" as a separator would still get "as-new" wrong. Both
// are claims about goods, so the narrow reading is the safe one.
//
// The words are English, Polish and Lithuanian because those are the three
// languages this operator, this marketplace and this codebase actually use.
func recarCondition(o core.Offer) string {
	words := strings.FieldsFunc(strings.ToLower(o.Condition), func(r rune) bool {
		return !unicode.IsLetter(r) && r != '-'
	})
	for _, w := range words {
		switch w {
		case "new", "nowy", "nowa", "naujas", "nauja":
			return recarConditionNew
		}
	}
	return recarConditionUsed
}

// recarImages returns the photograph cells for one row: the main image followed
// by [recarAddImageColumns] additional slots, blank where the offer has fewer.
//
// The length is constant so every row is exactly as wide as the header, which is
// the difference between a CSV an importer reads and one it rejects. Callers
// refuse an offer with more photographs than fit before reaching here.
func recarImages(o core.Offer, base string) []string {
	cells := make([]string, recarMaxPhotos)
	copy(cells, photoURLs(o, base))
	return cells
}
