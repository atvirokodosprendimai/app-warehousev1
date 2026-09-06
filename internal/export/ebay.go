package export

import (
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
)

// ebayHeader returns the File Exchange column set for the given ISO currency.
//
// The currency is not a column: it is a token inside the *Action header's own
// text, which is why this is a function and not a var. Hardcoding it there is
// how a EUR-priced feed gets listed in dollars at the same numbers, with no
// error anywhere — eBay reads the header, believes it, and sells.
func ebayHeader(currency string) []string {
	return []string{
		"*Action(SiteID=US|Country=US|Currency=" + currency + "|Version=1193)",
		"CustomLabel",
		"*Category",
		"*Title",
		"Description",
		"PicURL",
		"*Quantity",
		"*StartPrice",
		"*ConditionID",
		"*Format",
		"*Duration",
		"*Location",
		"*ReturnsAcceptedOption",
	}
}

// EBay renders offers as an eBay File Exchange CSV.
//
// The zero value is ready to use and carries no state, so one instance is safe
// to share across goroutines.
type EBay struct{}

// Name returns "ebay", the profile's registry name.
func (EBay) Name() string { return "ebay" }

// Write renders offers as an eBay File Exchange CSV.
//
// Every item is exactly one row: eBay takes all of an item's images in the
// single PicURL cell, pipe-separated. That is the opposite of Shopify's shape,
// where extra images are extra rows — the same photos, two formats, and the
// asymmetry these two profiles exist to absorb so that neither the offer nor the
// caller has to know about it.
//
// [Options.Category] and [Options.Location] are required here and have no
// default; [Options.ConditionID] falls back to [DefaultConditionID]. The whole
// export is refused, with w untouched, when the options or any exportable offer
// would produce a file that lists the wrong thing.
func (EBay) Write(w io.Writer, offers []core.Offer, opt Options) error {
	if err := opt.validateCommon(); err != nil {
		return fmt.Errorf("ebay: %w", err)
	}
	if strings.TrimSpace(opt.Category) == "" {
		return fmt.Errorf("ebay: %w: a category id is required; the numbers are eBay's own "+
			"taxonomy and a warehouse that sells anything has no defensible default",
			ErrOptions)
	}
	if strings.TrimSpace(opt.Location) == "" {
		return fmt.Errorf("ebay: %w: an item location is required; eBay prints it on every "+
			"listing and quotes postage from it", ErrOptions)
	}
	items, err := exportable(offers, opt)
	if err != nil {
		return fmt.Errorf("ebay: %w", err)
	}

	condition := strings.TrimSpace(opt.ConditionID)
	if condition == "" {
		condition = DefaultConditionID
	}

	rows := make([][]string, 0, len(items))
	var problems []error
	for _, o := range items {
		price, err := o.Shop.Decimal()
		if err != nil {
			problems = append(problems, fmt.Errorf("%w: %s: %w", ErrIncomplete, o.SKU, err))
			continue
		}
		rows = append(rows, []string{
			"Add",         // *Action
			o.SKU,         // CustomLabel
			opt.Category,  // *Category
			o.Title,       // *Title
			o.Description, // Description
			strings.Join(photoURLs(o, opt.BaseURL), "|"), // PicURL — all of them, one cell
			strconv.Itoa(o.Quantity),                     // *Quantity
			price,                                        // *StartPrice — Shop, never Owner
			condition,                                    // *ConditionID
			"FixedPrice",                                 // *Format
			"GTC",                                        // *Duration
			opt.Location,                                 // *Location
			"ReturnsAccepted",                            // *ReturnsAcceptedOption
		})
	}
	if len(problems) > 0 {
		return fmt.Errorf("ebay: %w", errors.Join(problems...))
	}
	return writeCSV(w, "ebay", ebayHeader(opt.currency()), rows)
}
