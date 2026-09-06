package export

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
)

// shopifyHeader is the product-import column set in the exact order Shopify's
// admin importer expects. Shopify matches columns by name, but an unexpected
// order is how a hand-edited file starts importing prices into quantities, so
// the order is pinned by a test rather than by care.
var shopifyHeader = []string{
	"Handle",
	"Title",
	"Body (HTML)",
	"Vendor",
	"Type",
	"Tags",
	"Published",
	"Option1 Name",
	"Option1 Value",
	"Variant SKU",
	"Variant Inventory Qty",
	"Variant Inventory Policy",
	"Variant Fulfillment Service",
	"Variant Price",
	"Variant Requires Shipping",
	"Variant Taxable",
	"Image Src",
	"Image Position",
	"Image Alt Text",
	"Status",
}

// The three columns an extra-image row fills. Named so that row can say which
// cells it sets instead of counting commas; a test asserts they still point at
// the headers they are named for.
const (
	shHandle        = 0
	shImageSrc      = 16
	shImagePosition = 17
)

// Shopify renders offers as the product CSV Shopify's admin importer accepts.
//
// The zero value is ready to use and carries no state, so one instance is safe
// to share across goroutines.
type Shopify struct{}

// Name returns "shopify", the profile's registry name.
func (Shopify) Name() string { return "shopify" }

// Write renders offers as a Shopify product CSV.
//
// Every product gets one full row plus one extra row per additional image. The
// extra rows repeat only Handle, Image Src and Image Position and leave every
// other column empty — that is how Shopify attaches images beyond the first, and
// repeating the product columns on them creates duplicate products instead.
// Image Position is 1-based and taken from the photo's place in the slice, so
// the positions in a file are always contiguous from 1.
//
// The whole export is refused, with w untouched, when the options or any
// exportable offer would produce a file that imports into the wrong thing. See
// the package documentation for why that beats dropping the offending rows.
func (Shopify) Write(w io.Writer, offers []core.Offer, opt Options) error {
	if err := opt.validateCommon(); err != nil {
		return fmt.Errorf("shopify: %w", err)
	}
	items, err := exportable(offers, opt)
	if err != nil {
		return fmt.Errorf("shopify: %w", err)
	}

	rows := make([][]string, 0, len(items))
	var problems []error
	for _, o := range items {
		handle := shopifyHandle(o.SKU)
		if handle == "" {
			problems = append(problems, fmt.Errorf(
				"%w: SKU %q has no letters or digits, so it cannot become a Shopify handle "+
					"and the product would be unreachable by URL", ErrIncomplete, o.SKU))
			continue
		}
		price, err := o.Shop.Decimal()
		if err != nil {
			problems = append(problems, fmt.Errorf("%w: %s: %w", ErrIncomplete, o.SKU, err))
			continue
		}
		urls := photoURLs(o, opt.BaseURL)

		rows = append(rows, []string{
			handle,                   // Handle
			o.Title,                  // Title
			o.Description,            // Body (HTML)
			"",                       // Vendor — Shopify substitutes the shop name
			"",                       // Type — core has no product taxonomy to map
			o.Condition,              // Tags — the one facet an offer carries
			"true",                   // Published
			"Title",                  // Option1 Name
			"Default Title",          // Option1 Value
			o.SKU,                    // Variant SKU
			strconv.Itoa(o.Quantity), // Variant Inventory Qty
			"deny",                   // Variant Inventory Policy
			"manual",                 // Variant Fulfillment Service
			price,                    // Variant Price — Shop, never Owner
			"true",                   // Variant Requires Shipping
			"true",                   // Variant Taxable
			urls[0],                  // Image Src
			"1",                      // Image Position
			o.Title,                  // Image Alt Text
			"active",                 // Status
		})

		// One row per image beyond the first, carrying nothing but the handle
		// that ties it to the product above and the image itself.
		for i, u := range urls[1:] {
			row := make([]string, len(shopifyHeader))
			row[shHandle] = handle
			row[shImageSrc] = u
			row[shImagePosition] = strconv.Itoa(i + 2)
			rows = append(rows, row)
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("shopify: %w", errors.Join(problems...))
	}
	return writeCSV(w, "shopify", shopifyHeader, rows)
}

// shopifyHandle turns a SKU into a Shopify handle: lowercased, with every run of
// non-alphanumerics collapsed to a single "-" and no leading or trailing dash.
//
// The handle is the product's URL slug, so only ASCII letters and digits survive
// — a non-ASCII character would arrive percent-encoded in links and stop reading
// as the SKU it came from. A SKU with nothing left is refused rather than given
// a made-up handle, because a generated one would silently detach the product
// from the SKU an operator searches by.
func shopifyHandle(sku string) string {
	var b strings.Builder
	b.Grow(len(sku))
	dash := false
	for _, r := range strings.ToLower(sku) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(r)
		default:
			dash = true
		}
	}
	return b.String()
}
