package export

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
)

// recarOffer returns an offer answering the CAR template's questions, which is
// what a recar export is actually made of.
//
// The codes and the Export flags mirror the shipped template (ADR-021 T5): make,
// model, oem_main and year are ticked for export there, oem_other is not.
func recarOffer(sku string, photos int) core.Offer {
	o := testOffer(sku, photos)
	o.Title = "1762000-00-E SILNIK TYL TESLA S PLAID"
	o.Fields = []core.OfferField{
		recarAnswer("make", "Make", core.FieldText, true, "Tesla"),
		recarAnswer("model", "Model", core.FieldText, true, "TESLA MODEL S PLAID"),
		recarAnswer("oem_main", "Main OEM code", core.FieldText, true, "1762000-00-E"),
		recarAnswer("year", "Year", core.FieldNumber, true, "2021"),
	}
	return o
}

func recarAnswer(code, label string, kind core.FieldKind, export bool, value string) core.OfferField {
	return core.OfferField{
		CategoryField: core.CategoryField{
			ID:     "field-" + code,
			Code:   code,
			Label:  label,
			Kind:   kind,
			Export: export,
		},
		Value: value,
	}
}

// TestRecarHeaderIsTheTemplateTheySent compares our header against recar's own,
// column for column.
//
// ★ THE EXPECTATION IS THEIR FILE, NOT A LIST RETYPED BESIDE IT. testdata holds
// the header line lifted from the export recar.lt sent, so this asserts we agree
// with the artifact rather than with my transcription of it — which is the one
// thing a hand-written want list cannot do, because it would be wrong in exactly
// the same way the code was.
//
// ⚠ THE HEADER IS NOT OURS TO TIDY. These are an importer's keys, in Polish,
// with diacritics: "Numer katalogowy czesci" is a different string from "Numer
// katalogowy części", and a well-meaning de-accenting pass produces a file that
// imports with empty columns and no error anywhere. The count matters too — a
// dropped "Add image" shifts nothing visibly and narrows every row.
//
// The stored line is LF where theirs is CRLF. That is deliberate: the contract is
// the field names, which encoding/csv parses identically either way.
func TestRecarHeaderIsTheTemplateTheySent(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "recar-header.csv"))
	if err != nil {
		t.Fatalf("reading recar's own header: %v", err)
	}
	theirs := records(t, string(raw))[0]
	if len(theirs) == 0 {
		t.Fatal("the stored header is empty, so this test compares against nothing")
	}

	out := render(t, Recar{}, []core.Offer{recarOffer("TESLA-1", 2)}, testOptions())
	ours := records(t, out)[0]

	if len(ours) != len(theirs) {
		t.Fatalf("we write %d columns and recar's template has %d", len(ours), len(theirs))
	}
	for i := range theirs {
		if ours[i] != theirs[i] {
			t.Errorf("column %d = %q, want %q — these are recar's own keys, not prose",
				i, ours[i], theirs[i])
		}
	}
}

// TestRecarMapsTheCarTemplateOntoTheirColumns is the correspondence the whole
// profile rests on: the CAR starter template was written from recar's intake
// form, so its answers belong in these named columns.
func TestRecarMapsTheCarTemplateOntoTheirColumns(t *testing.T) {
	out := render(t, Recar{}, []core.Offer{recarOffer("TESLA-1", 2)}, testOptions())
	recs := records(t, out)
	if len(recs) != 2 {
		t.Fatalf("want a header and one row, got %d records:\n%s", len(recs), out)
	}
	row := recs[1]

	for _, c := range []struct {
		col  int
		name string
		want string
	}{
		{0, "Brand", "Tesla"},
		{1, "Model", "TESLA MODEL S PLAID"},
		{2, "Numer katalogowy części", "1762000-00-E"},
		{3, "Numer katalogowy oryginału", ""},
		{4, "Numery katalogowe zamienników", ""},
		{5, "Producent części", ""},
		{6, "Stan", recarConditionUsed},
		{7, "Price €", "125.00"},
		{8, "Title", "1762000-00-E SILNIK TYL TESLA S PLAID"},
		{9, "Model Year", "2021"},
	} {
		if row[c.col] != c.want {
			t.Errorf("%s = %q, want %q", c.name, row[c.col], c.want)
		}
	}
}

// TestRecarDoesNotPublishAFieldNotMarkedForExport closes the hole a profile that
// reads answers by code could open in ADR-021's export flag.
//
// ⚠ The flag is off by default and means "this does not leave the building". A
// named column is not an exemption from it: an operator who unticks the OEM code
// has said not to publish it, and a profile that published it anyway would give
// them no way to find out.
func TestRecarDoesNotPublishAFieldNotMarkedForExport(t *testing.T) {
	o := recarOffer("TESLA-1", 2)
	for i := range o.Fields {
		if o.Fields[i].Code == "oem_main" {
			o.Fields[i].Export = false
		}
	}
	out := render(t, Recar{}, []core.Offer{o}, testOptions())

	if strings.Contains(out, "1762000-00-E SILNIK") == false {
		t.Fatalf("the title is missing, so nothing was exported and the check below "+
			"would pass over an empty file:\n%s", out)
	}
	if got := records(t, out)[1][2]; got != "" {
		t.Errorf("Numer katalogowy części = %q, want empty: the field is not marked for "+
			"export, and a named column is not an exemption from that flag", got)
	}
}

// TestRecarSpreadsPhotographsAcrossTheFixedImageColumns checks the third photo
// shape in this package — neither eBay's one pipe-joined cell nor Shopify's extra
// row per image.
func TestRecarSpreadsPhotographsAcrossTheFixedImageColumns(t *testing.T) {
	const photos = 3
	o := recarOffer("TESLA-1", photos)
	out := render(t, Recar{}, []core.Offer{o}, testOptions())
	recs := records(t, out)
	row, header := recs[1], recs[0]

	if len(row) != len(header) {
		t.Fatalf("row has %d cells and the header %d; a ragged CSV is one no importer reads",
			len(row), len(header))
	}
	want := photoURLs(o, testOptions().BaseURL)
	for i, w := range want {
		if got := row[10+i]; got != w {
			t.Errorf("image column %d = %q, want %q", i, got, w)
		}
	}
	// Every remaining slot must be present and empty, not absent.
	for i := 10 + photos; i < len(row); i++ {
		if row[i] != "" {
			t.Errorf("image column %d = %q, want it empty", i-10, row[i])
		}
	}
}

// TestRecarRefusesMorePhotographsThanTheTemplateHolds pins the choice to refuse
// rather than truncate.
//
// Dropping the extras would import cleanly and list the item with most of its
// pictures, which is exactly the kind of failure nobody counts. The whole export
// is refused and the writer left untouched, the same way a missing price is
// handled.
func TestRecarRefusesMorePhotographsThanTheTemplateHolds(t *testing.T) {
	o := recarOffer("TESLA-1", recarMaxPhotos+1)

	var buf bytes.Buffer
	err := Recar{}.Write(&buf, []core.Offer{o}, testOptions())
	if err == nil {
		t.Fatal("an offer with more photographs than the template holds was exported anyway; " +
			"the last ones would have dropped off the listing silently")
	}
	if !errors.Is(err, ErrIncomplete) {
		t.Errorf("error is %v, want it to wrap ErrIncomplete so callers can tell stock "+
			"apart from settings", err)
	}
	if !strings.Contains(err.Error(), "TESLA-1") {
		t.Errorf("error %q does not name the SKU, and naming it is how the operator "+
			"knows which offer to fix", err)
	}
	if buf.Len() != 0 {
		t.Errorf("a refused export wrote %d bytes; it must leave the writer untouched "+
			"rather than half a catalogue that reads like a whole one", buf.Len())
	}

	// The boundary itself must still export, or the refusal is off by one.
	if err := (Recar{}).Write(&bytes.Buffer{}, []core.Offer{recarOffer("TESLA-2", recarMaxPhotos)},
		testOptions()); err != nil {
		t.Errorf("exactly %d photographs was refused: %v", recarMaxPhotos, err)
	}
}

// TestRecarRefusesACurrencyItsHeaderCannotSay is the eBay *Action lesson applied
// to a header that hardcodes the euro sign.
//
// ⚠ This failure is otherwise SILENT. exportable() only checks each offer against
// the run's own currency, so a dollar-configured run renders dollar-priced offers
// happily — under a column headed "Price €". recar reads the header, believes it,
// and sells at euro numbers.
func TestRecarRefusesACurrencyItsHeaderCannotSay(t *testing.T) {
	opt := testOptions()
	opt.Currency = "USD"
	o := recarOffer("TESLA-1", 2)
	o.Shop = core.Money{Minor: 12500, Currency: "USD"}

	var buf bytes.Buffer
	err := Recar{}.Write(&buf, []core.Offer{o}, opt)
	if err == nil {
		t.Fatal("a USD export was written under a \"Price €\" header; every row is " +
			"mis-priced and nothing anywhere reports it")
	}
	if !errors.Is(err, ErrOptions) {
		t.Errorf("error is %v, want it to wrap ErrOptions — the settings are wrong, "+
			"not the stock", err)
	}
	if buf.Len() != 0 {
		t.Errorf("a refused export wrote %d bytes", buf.Len())
	}

	// Lower case must still be accepted, or an operator typing "eur" is refused
	// for no reason: Options.currency() normalises precisely so it is not.
	opt2 := testOptions()
	opt2.Currency = "eur"
	if err := (Recar{}).Write(&bytes.Buffer{}, []core.Offer{recarOffer("TESLA-2", 2)}, opt2); err != nil {
		t.Errorf("a lower-case %q was refused: %v", "eur", err)
	}
}

// TestRecarConditionReadsWholeWordsAndDefaultsToUsed pins both halves of the Stan
// mapping.
func TestRecarConditionReadsWholeWordsAndDefaultsToUsed(t *testing.T) {
	for _, tc := range []struct {
		condition string
		want      string
	}{
		{"", recarConditionUsed},
		{"used - good", recarConditionUsed},
		{"New", recarConditionNew},
		{"new, in box", recarConditionNew},
		{"nowy", recarConditionNew},
		{"naujas", recarConditionNew},
		// ⚠ The two a looser reading gets wrong, and both are claims about goods:
		// a renewed part is not a new one, and "as-new" is how somebody says a
		// thing is NOT new. A substring test fails the first; a splitter that
		// broke on hyphens would still fail the second.
		{"renewed", recarConditionUsed},
		{"as-new, light wear", recarConditionUsed},
	} {
		if got := recarCondition(core.Offer{Condition: tc.condition}); got != tc.want {
			t.Errorf("condition %q => %q, want %q", tc.condition, got, tc.want)
		}
	}
}
