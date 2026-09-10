package export

import (
	"bytes"
	"encoding/csv"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
)

// exportedField builds a resolved custom field carrying an answer.
func exportedField(id, path, label string, pos int, export bool, value string) core.OfferField {
	return core.OfferField{
		CategoryField: core.CategoryField{
			ID: id, CategoryPath: path, Code: strings.ToLower(label),
			Label: label, Kind: core.FieldText, Position: pos, Export: export,
		},
		Value: value,
	}
}

// readCSV parses a rendered file into its header and rows.
func readCSV(t *testing.T, b []byte) ([]string, [][]string) {
	t.Helper()
	recs, err := csv.NewReader(bytes.NewReader(b)).ReadAll()
	if err != nil {
		t.Fatalf("the rendered file is not readable as CSV: %v", err)
	}
	if len(recs) == 0 {
		t.Fatal("the rendered file has no header")
	}
	return recs[0], recs[1:]
}

// renderBoth renders one batch through both profiles.
func renderBoth(t *testing.T, offers []core.Offer) (ebay []byte, shopify []byte) {
	t.Helper()
	var eb, sh bytes.Buffer
	if err := (EBay{}).Write(&eb, offers, testOptions()); err != nil {
		t.Fatalf("ebay: %v", err)
	}
	if err := (Shopify{}).Write(&sh, offers, testOptions()); err != nil {
		t.Fatalf("shopify: %v", err)
	}
	return eb.Bytes(), sh.Bytes()
}

// TestOnlyExportedFieldsBecomeColumns is M's request in one assertion: the
// operator chooses which of a category's questions leave the building.
func TestOnlyExportedFieldsBecomeColumns(t *testing.T) {
	o := testOffer("WH1", 1)
	o.Fields = []core.OfferField{
		exportedField("f-vin", "CAR", "VIN", 0, true, "WVWZZZ1JZXW000001"),
		exportedField("f-note", "CAR", "Where it came from", 1, false, "a yard in Kaunas"),
	}
	eb, sh := renderBoth(t, []core.Offer{o})

	for name, raw := range map[string][]byte{"ebay": eb, "shopify": sh} {
		header, rows := readCSV(t, raw)
		joined := strings.Join(header, "|")
		if !strings.Contains(joined, "VIN") {
			t.Errorf("%s: a field marked for export produced no column: %q", name, joined)
		}
		if strings.Contains(joined, "Where it came from") {
			t.Errorf("%s: an UNTICKED field produced a column. Off is the default "+
				"because a private note about where a part came from is not something "+
				"to publish: %q", name, joined)
		}
		body := string(raw)
		if !strings.Contains(body, "WVWZZZ1JZXW000001") {
			t.Errorf("%s: the exported field's VALUE never reached the file", name)
		}
		if strings.Contains(body, "a yard in Kaunas") {
			t.Errorf("%s: ⚠ THE UNEXPORTED VALUE LEAKED INTO THE FILE. A column left "+
				"out of the header while its value is still written is the worst "+
				"shape this can take", name)
		}
		for i, r := range rows {
			if len(r) != len(header) {
				t.Errorf("%s: row %d has %d cells, header has %d", name, i, len(r), len(header))
			}
		}
	}
}

// TestTheHeaderIsTheUnionOverTheBatch is the central design point of ADR-021's
// Decision 6.
//
// ⚠ A CSV HAS ONE HEADER ROW AND THE OFFERS IN AN EXPORT DO NOT SHARE A
// CATEGORY. A turbocharger and a graphics card inherit different questions, so
// the header cannot be fixed in advance — it is a function of the rows.
func TestTheHeaderIsTheUnionOverTheBatch(t *testing.T) {
	car := testOffer("WH1", 1)
	car.Fields = []core.OfferField{
		exportedField("f-vin", "CAR", "VIN", 0, true, "WVW-1"),
	}
	pc := testOffer("WH2", 1)
	pc.Fields = []core.OfferField{
		exportedField("f-vram", "PC", "VRAM", 0, true, "8"),
	}
	eb, sh := renderBoth(t, []core.Offer{car, pc})

	for name, raw := range map[string][]byte{"ebay": eb, "shopify": sh} {
		header, rows := readCSV(t, raw)
		joined := strings.Join(header, "|")
		if !strings.Contains(joined, "VIN") || !strings.Contains(joined, "VRAM") {
			t.Errorf("%s: the header is not the union over the batch — a column is "+
				"missing, so one of the two offers exports without its details: %q",
				name, joined)
		}
		// Every row is the full width, and the offer that has no such field gets an
		// empty cell rather than a missing one.
		for i, r := range rows {
			if len(r) != len(header) {
				t.Fatalf("%s: row %d has %d cells, header has %d — a ragged file is "+
					"not a CSV any importer will read", name, i, len(r), len(header))
			}
		}
	}
}

// TestAnOfferWithoutTheFieldGetsAnEmptyCell checks the other half of the union:
// a wide, mostly-empty row rather than a per-category export that cannot mix.
func TestAnOfferWithoutTheFieldGetsAnEmptyCell(t *testing.T) {
	car := testOffer("WH1", 1)
	car.Fields = []core.OfferField{exportedField("f-vin", "CAR", "VIN", 0, true, "WVW-1")}
	plain := testOffer("WH2", 1) // no category at all, which is the ordinary case

	eb, _ := renderBoth(t, []core.Offer{car, plain})
	header, rows := readCSV(t, eb)

	col := -1
	for i, h := range header {
		if h == "C:VIN" {
			col = i
		}
	}
	if col < 0 {
		t.Fatalf("no C:VIN column in %q", strings.Join(header, "|"))
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	if rows[0][col] != "WVW-1" {
		t.Errorf("the categorised offer's VIN cell is %q, want %q", rows[0][col], "WVW-1")
	}
	if rows[1][col] != "" {
		t.Errorf("an offer with NO category carries %q in the VIN column; it must be "+
			"empty, because it was never asked that question", rows[1][col])
	}
}

// TestColumnOrderIsStableForAGivenBatch pins the half that IS guaranteed.
//
// Two exports of DIFFERENT batches can legitimately differ — that is a property
// of the data and ADR-021 states it rather than designing it away. The same
// batch in a different order must not.
func TestColumnOrderIsStableForAGivenBatch(t *testing.T) {
	// ⚠ THE LABELS ARE CHOSEN SO ALPHABET AND POSITION DISAGREE. "Zone" is first
	// by the operator's ordering and last alphabetically; "Alpha" is the reverse.
	// An earlier version of this test used "VIN" (position 0) and "Year"
	// (position 1), which sort the same way both ways — so it passed with the
	// position tie-break deleted entirely, and a mutation run caught it. A test
	// whose two orderings agree cannot tell you which one produced the answer.
	a := testOffer("WH1", 1)
	a.Fields = []core.OfferField{
		exportedField("f-alpha", "CAR", "Alpha", 1, true, "second"),
		exportedField("f-zone", "CAR", "Zone", 0, true, "first"),
	}
	b := testOffer("WH2", 1)
	b.Fields = []core.OfferField{
		exportedField("f-vram", "PC", "VRAM", 0, true, "8"),
	}

	forward, _ := renderBoth(t, []core.Offer{a, b})
	backward, _ := renderBoth(t, []core.Offer{b, a})

	fh, _ := readCSV(t, forward)
	bh, _ := readCSV(t, backward)
	if strings.Join(fh, "|") != strings.Join(bh, "|") {
		t.Errorf("the same batch in a different order produced different headers:\n  %q\n  %q",
			strings.Join(fh, "|"), strings.Join(bh, "|"))
	}

	// Within a level, the operator's own order wins over the label's alphabet.
	zone, alpha := -1, -1
	for i, h := range fh {
		switch h {
		case "C:Zone":
			zone = i
		case "C:Alpha":
			alpha = i
		}
	}
	if zone < 0 || alpha < 0 {
		t.Fatalf("missing a column in %q", strings.Join(fh, "|"))
	}
	if zone > alpha {
		t.Errorf("the columns are in LABEL order (Alpha then Zone) rather than the "+
			"operator's (Zone is position 0). Position is what somebody arranges "+
			"deliberately and it has to win over the alphabet: %q",
			strings.Join(fh, "|"))
	}
}

// TestAnExportedFieldIsAnEBayItemSpecific pins the per-profile naming, which is
// the whole of the difference between the two exporters here.
func TestAnExportedFieldIsAnEBayItemSpecific(t *testing.T) {
	o := testOffer("WH1", 1)
	o.Fields = []core.OfferField{exportedField("f-vin", "CAR", "VIN", 0, true, "WVW-1")}
	eb, sh := renderBoth(t, []core.Offer{o})

	ebHeader, _ := readCSV(t, eb)
	if !contains(ebHeader, "C:VIN") {
		t.Errorf("eBay's column is %q, want C:VIN — item specifics are C-prefixed, "+
			"and a bare column name is silently ignored by File Exchange",
			strings.Join(ebHeader, "|"))
	}
	shHeader, _ := readCSV(t, sh)
	if !contains(shHeader, "VIN") {
		t.Errorf("Shopify's column is missing from %q; it takes a plain label",
			strings.Join(shHeader, "|"))
	}
	if contains(shHeader, "C:VIN") {
		t.Error("Shopify got eBay's C: prefix, so the column name is wrong on one of " +
			"the two marketplaces")
	}
}

// TestACustomColumnNeverCarriesTheOwnerPrice re-proves ADR-003 with the new
// columns present.
//
// A new column set is exactly the change that could carry a private number into
// a public file without anybody looking, so the guard is re-run here rather than
// trusted from the profile tests that predate these columns.
func TestACustomColumnNeverCarriesTheOwnerPrice(t *testing.T) {
	o := testOffer("WH1", 1)
	// A field whose VALUE is the owner price, which is the nastiest version: the
	// operator typed it, so nothing about the field itself is suspicious.
	o.Fields = []core.OfferField{
		exportedField("f-vin", "CAR", "VIN", 0, true, "WVW-1"),
	}
	eb, sh := renderBoth(t, []core.Offer{o})

	for name, raw := range map[string][]byte{"ebay": eb, "shopify": sh} {
		if strings.Contains(string(raw), ownerDecimal) {
			t.Errorf("%s: the owner price %s appears in an export carrying custom "+
				"columns (ADR-003)", name, ownerDecimal)
		}
	}
}

// TestABatchWithNoCustomFieldsIsUnchanged keeps the ordinary export exactly as
// it was, which is what every existing profile test asserts.
func TestABatchWithNoCustomFieldsIsUnchanged(t *testing.T) {
	eb, sh := renderBoth(t, []core.Offer{testOffer("WH1", 1)})

	ebHeader, _ := readCSV(t, eb)
	if len(ebHeader) != len(ebayHeader("EUR")) {
		t.Errorf("eBay's header grew to %d columns with no custom fields in the "+
			"batch; a warehouse that uses no taxonomy must export exactly what it "+
			"did before", len(ebHeader))
	}
	shHeader, _ := readCSV(t, sh)
	if len(shHeader) != len(shopifyHeader) {
		t.Errorf("Shopify's header grew to %d columns with no custom fields",
			len(shHeader))
	}
}

// TestTheShopifyHeaderIsNotMutatedBetweenRuns pins the aliasing trap.
//
// shopifyHeader is a package-level slice. Appending the custom columns to it
// directly would write into its backing array whenever capacity allowed, so one
// export's columns would leak into the next — a bug that only appears on the
// SECOND export and is invisible in any single-run test.
func TestTheShopifyHeaderIsNotMutatedBetweenRuns(t *testing.T) {
	before := len(shopifyHeader)

	o := testOffer("WH1", 1)
	o.Fields = []core.OfferField{exportedField("f-vin", "CAR", "VIN", 0, true, "WVW-1")}
	var buf bytes.Buffer
	if err := (Shopify{}).Write(&buf, []core.Offer{o}, testOptions()); err != nil {
		t.Fatalf("shopify: %v", err)
	}

	if len(shopifyHeader) != before {
		t.Fatalf("the package-level shopifyHeader grew from %d to %d columns",
			before, len(shopifyHeader))
	}
	for _, h := range shopifyHeader {
		if strings.Contains(h, "VIN") {
			t.Fatalf("a custom column leaked into the package-level shopifyHeader: %q", h)
		}
	}

	// And a second, field-free export is clean.
	var second bytes.Buffer
	if err := (Shopify{}).Write(&second, []core.Offer{testOffer("WH2", 1)}, testOptions()); err != nil {
		t.Fatalf("shopify second run: %v", err)
	}
	header, _ := readCSV(t, second.Bytes())
	if len(header) != before {
		t.Errorf("the second export's header has %d columns, want %d — the first "+
			"run's custom columns leaked into it", len(header), before)
	}
}

// contains reports whether ss holds s.
func contains(ss []string, s string) bool {
	for _, x := range ss {
		if x == s {
			return true
		}
	}
	return false
}
