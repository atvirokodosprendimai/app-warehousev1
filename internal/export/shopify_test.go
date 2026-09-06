package export

import (
	"bytes"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
)

// wantShopifyHeader is the header verbatim, kept as one string rather than
// rebuilt from shopifyHeader so that a typo in the source cannot agree with a
// matching typo in the test.
const wantShopifyHeader = "Handle,Title,Body (HTML),Vendor,Type,Tags,Published,Option1 Name," +
	"Option1 Value,Variant SKU,Variant Inventory Qty,Variant Inventory Policy," +
	"Variant Fulfillment Service,Variant Price,Variant Requires Shipping,Variant Taxable," +
	"Image Src,Image Position,Image Alt Text,Status"

func TestShopifyHeaderMatchesTheImporterSpec(t *testing.T) {
	out := render(t, Shopify{}, []core.Offer{testOffer("LAMP-01", 1)}, testOptions())
	if got := headerLine(out); got != wantShopifyHeader {
		t.Errorf("header =\n%q\nwant\n%q", got, wantShopifyHeader)
	}
}

func TestShopifyImageColumnIndexesPointAtTheirColumns(t *testing.T) {
	// The extra-image rows address three cells by index; if the header moves
	// under them they would fill the wrong columns without any error.
	tests := []struct {
		index int
		want  string
	}{
		{shHandle, "Handle"},
		{shImageSrc, "Image Src"},
		{shImagePosition, "Image Position"},
	}
	for _, tt := range tests {
		if got := shopifyHeader[tt.index]; got != tt.want {
			t.Errorf("shopifyHeader[%d] = %q, want %q", tt.index, got, tt.want)
		}
	}
}

func TestShopifyRendersEveryProductColumn(t *testing.T) {
	out := render(t, Shopify{}, []core.Offer{testOffer("LAMP-01", 1)}, testOptions())
	recs := records(t, out)
	if len(recs) != 2 {
		t.Fatalf("got %d records, want header + 1 row:\n%s", len(recs), out)
	}
	want := []string{
		"lamp-01",
		"Vintage Brass Lamp",
		"A brass lamp, rewired and tested.",
		"",
		"",
		"used - good",
		"true",
		"Title",
		"Default Title",
		"LAMP-01",
		"2",
		"deny",
		"manual",
		"125.00",
		"true",
		"true",
		"https://warehouse.example.com/p/photo-lamp-01-0.jpg",
		"1",
		"Vintage Brass Lamp",
		"active",
	}
	if !reflect.DeepEqual(recs[1], want) {
		t.Errorf("product row =\n%q\nwant\n%q", recs[1], want)
	}
}

func TestShopifyThreePhotosProduceThreeRows(t *testing.T) {
	out := render(t, Shopify{}, []core.Offer{testOffer("LAMP-01", 3)}, testOptions())
	recs := records(t, out)
	if len(recs) != 4 {
		t.Fatalf("got %d records, want header + 3 rows:\n%s", len(recs), out)
	}

	base := "https://warehouse.example.com/p/photo-lamp-01-"
	if got := recs[1][shImageSrc]; got != base+"0.jpg" {
		t.Errorf("product row Image Src = %q, want %q", got, base+"0.jpg")
	}
	if got := recs[1][shImagePosition]; got != "1" {
		t.Errorf("product row Image Position = %q, want \"1\"", got)
	}

	// The extra rows carry the handle and the image and nothing else: a repeated
	// Title or Variant SKU there creates a second product instead of a second
	// picture.
	extras := []struct {
		row      int
		src      string
		position string
	}{
		{2, base + "1.jpg", "2"},
		{3, base + "2.jpg", "3"},
	}
	for _, e := range extras {
		row := recs[e.row]
		if got := row[shHandle]; got != "lamp-01" {
			t.Errorf("row %d Handle = %q, want \"lamp-01\"", e.row, got)
		}
		if got := row[shImageSrc]; got != e.src {
			t.Errorf("row %d Image Src = %q, want %q", e.row, got, e.src)
		}
		if got := row[shImagePosition]; got != e.position {
			t.Errorf("row %d Image Position = %q, want %q", e.row, got, e.position)
		}
		for i, cell := range row {
			if i == shHandle || i == shImageSrc || i == shImagePosition {
				continue
			}
			if cell != "" {
				t.Errorf("row %d column %q = %q, want empty",
					e.row, shopifyHeader[i], cell)
			}
		}
	}
}

func TestShopifyHandleIsASlugOfTheSKU(t *testing.T) {
	tests := []struct {
		name string
		sku  string
		want string
	}{
		{"already a slug", "LAMP-01", "lamp-01"},
		{"slash and space", "A/B 12", "a-b-12"},
		{"padded", "  spaced  sku  ", "spaced-sku"},
		{"runs collapse", "SKU--__--X", "sku-x"},
		{"punctuation only between", "sku.01.b", "sku-01-b"},
		{"non-ascii is dropped", "ĄŽ-9", "9"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := render(t, Shopify{}, []core.Offer{testOffer(tt.sku, 1)}, testOptions())
			recs := records(t, out)
			if len(recs) != 2 {
				t.Fatalf("got %d records, want header + 1 row", len(recs))
			}
			if got := recs[1][shHandle]; got != tt.want {
				t.Errorf("handle for SKU %q = %q, want %q", tt.sku, got, tt.want)
			}
			// The SKU itself is still published verbatim as the variant SKU.
			if got := recs[1][9]; got != tt.sku {
				t.Errorf("Variant SKU = %q, want %q", got, tt.sku)
			}
		})
	}
}

func TestShopifyRefusesASKUThatCannotBecomeAHandle(t *testing.T) {
	var buf bytes.Buffer
	err := Shopify{}.Write(&buf, []core.Offer{testOffer("###", 1)}, testOptions())
	if !errors.Is(err, ErrIncomplete) {
		t.Fatalf("Write error = %v, want ErrIncomplete", err)
	}
	if !strings.Contains(err.Error(), "###") {
		t.Errorf("error does not name the offending SKU:\n%v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("refused export wrote %d bytes: %q", buf.Len(), buf.String())
	}
}

func TestShopifyNeverExportsTheOwnerPrice(t *testing.T) {
	// ★ The rule the package exists to keep: what the owner wants to receive is
	// the business's private cost and must not reach a marketplace in any
	// column. The owner amount uses digits that appear nowhere else, so these
	// substring checks mean something.
	offer := testOffer("LAMP-01", 3)
	offer.Sold = core.Money{Minor: 11900, Currency: "EUR"}
	out := render(t, Shopify{}, []core.Offer{offer}, testOptions())

	for _, leak := range []string{ownerDecimal, "3077"} {
		if strings.Contains(out, leak) {
			t.Errorf("owner price %q appears in the export:\n%s", leak, out)
		}
	}
	// Prove the check can fail: the shop price, which IS published, is there.
	if !strings.Contains(out, "125.00") {
		t.Fatalf("shop price is missing, so the leak check proved nothing:\n%s", out)
	}
	// No column may hold it either, in case a future column renders it in a
	// form the substring check above would miss.
	for _, row := range records(t, out) {
		for i, cell := range row {
			if cell == ownerDecimal || cell == "3077" {
				t.Errorf("owner price in column %q", shopifyHeader[i])
			}
		}
	}
}
