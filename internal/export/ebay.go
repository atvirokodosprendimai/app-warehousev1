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
// ebayValue resolves one of eBay's must-haves for one offer.
//
// The offer's own value wins and the configured setting is the fallback, which
// is the same precedence [core.Marketplace.Resolve] applies at the settings
// screen — written once here so the two can never disagree about which value an
// operator is actually looking at.
func ebayValue(o core.Offer, f core.MarketplaceField, fallback string) string {
	if v := strings.TrimSpace(o.MarketplaceValue(EBay{}.Name(), f)); v != "" {
		return v
	}
	return strings.TrimSpace(fallback)
}

func (EBay) Write(w io.Writer, offers []core.Offer, opt Options) error {
	if err := opt.validateCommon(); err != nil {
		return fmt.Errorf("ebay: %w", err)
	}
	// ⚠ The category is NOT checked here, deliberately. It is resolved PER OFFER
	// below, because eBay's categories are per item: a lamp and a chair are not
	// the same number, so one value for a whole file would mean one export per
	// category. [Options.Category] is the DEFAULT for offers that name none.
	//
	// The trade is that a wholly unconfigured export now fails on its first
	// offer rather than before any work — which is what lets the message name
	// the offer, and naming it is the point (ADR-016).
	if strings.TrimSpace(opt.Location) == "" {
		return fmt.Errorf("ebay: %w: an item location is required; eBay prints it on every "+
			"listing and quotes postage from it", ErrOptions)
	}
	items, err := exportable(offers, opt)
	if err != nil {
		return fmt.Errorf("ebay: %w", err)
	}

	// The custom columns are computed from the offers actually being written, not
	// from the whole tree — see [customColumns] for why a CSV cannot declare a
	// fixed set here. eBay's item specifics are "C:<Label>" columns, so an
	// exported field becomes one.
	cols := customColumns(items)

	rows := make([][]string, 0, len(items))
	var problems []error
	for _, o := range items {
		// ⚠ ALL THREE MUST-HAVES RESOLVE PER OFFER, not just the category. ADR-022
		// gives an operator a list to pick each one from, and a value they can pick
		// and store but that no export ever reads would be worse than no control at
		// all — it would look set, on the offer, for ever. Condition was a single
		// value for the whole file until then, so a warehouse holding a new part
		// and a used one had to run two exports or lie about one of them.
		category := ebayValue(o, core.MarketplaceCategory, opt.Category)
		condition := ebayValue(o, core.MarketplaceCondition, opt.ConditionID)
		if condition == "" {
			condition = DefaultConditionID
		}
		// Location keeps its up-front requirement (checked above) rather than
		// following the category's refuse-per-offer shape: a default location is
		// something every deployment has, and the offer's own value is an override
		// for the item that ships from somewhere else.
		location := ebayValue(o, core.MarketplaceLocation, opt.Location)
		if category == "" {
			// Naming BOTH places is the whole point of this message. The reported
			// bug was not a missing value — it was that the error said one was
			// required and nothing anywhere said where to put it.
			problems = append(problems, fmt.Errorf(
				"%w: %s: no eBay category. Set one on the offer, or set a default at "+
					"Settings → eBay. The numbers are eBay's own taxonomy and a warehouse "+
					"that sells anything has no defensible default",
				ErrIncomplete, o.SKU))
			continue
		}

		price, err := o.Shop.Decimal()
		if err != nil {
			problems = append(problems, fmt.Errorf("%w: %s: %w", ErrIncomplete, o.SKU, err))
			continue
		}
		row := []string{
			"Add",         // *Action
			o.SKU,         // CustomLabel
			category,      // *Category — this offer's own, or the configured default
			o.Title,       // *Title
			o.Description, // Description
			strings.Join(photoURLs(o, opt.BaseURL), "|"), // PicURL — all of them, one cell
			strconv.Itoa(o.Quantity),                     // *Quantity
			price,                                        // *StartPrice — Shop, never Owner
			condition,                                    // *ConditionID — this offer's own, or the configured default
			"FixedPrice",                                 // *Format
			"GTC",                                        // *Duration
			location,                                     // *Location — this offer's own, or the configured default
			"ReturnsAccepted",                            // *ReturnsAcceptedOption
		}
		// The variable tail. Empty where this offer's category does not ask that
		// question, so every row is the same width as the header.
		rows = append(rows, append(row, customCells(o, cols)...))
	}
	if len(problems) > 0 {
		return fmt.Errorf("ebay: %w", errors.Join(problems...))
	}
	header := append(ebayHeader(opt.currency()),
		customLabels(cols, func(c core.CategoryField) string { return "C:" + c.Label })...)
	return writeCSV(w, "ebay", header, rows)
}
