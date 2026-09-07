// Package export renders offers a caller has already loaded as the CSV files
// marketplaces ingest.
//
// Every function here is pure: no database, no network, no clock. An exporter is
// handed offers and an [io.Writer] and produces bytes, so a test asserts on the
// exact file Shopify or eBay would receive rather than on an intention.
//
// # Owner prices are never exported
//
// [core.Offer.Owner] — what the item's holder in a distributed warehouse wants
// to receive — must never appear in an exported file, in any column, in any
// profile. It is the business's private cost, and a buyer or a competitor
// reading it off a public feed is a disclosure nobody chose to make.
// [core.Offer.Shop] is the only price that is ever published. Each profile
// carries a test that renders an offer with a distinctive owner amount and
// asserts those digits appear nowhere in the output.
//
// # Refusing beats guessing
//
// An offer that would render a broken row — no shop price, no photos, or a price
// in a currency this run does not publish — fails the whole export with an error
// naming the SKUs, rather than being dropped. Stock missing from a feed is
// invisible until somebody counts the listings, whereas an error is read the
// moment it happens. Offers whose status is not exportable are the one case that
// is skipped silently: keeping a draft or an archived item off a marketplace is
// exactly what [core.Status.Exportable] exists to decide.
package export

import (
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
)

// ErrUnknownProfile reports a profile name that no exporter is registered under.
var ErrUnknownProfile = errors.New("export: unknown profile")

// ErrOptions reports an export configuration that would produce a file no
// marketplace can use — a relative photo base, a missing currency, a missing
// eBay category. It is the operator's settings that are wrong, not the stock.
var ErrOptions = errors.New("export: invalid options")

// ErrIncomplete reports offers that cannot be rendered into a usable row. The
// error names every offending SKU, because the fix is to edit those offers.
var ErrIncomplete = errors.New("export: offer cannot be exported")

// DefaultConditionID is eBay's numeric condition code for "Used", which is what
// almost everything in a second-hand warehouse is.
//
// Condition gets a default and [Options.Category] does not, because a wrong
// condition is printed on the listing where somebody notices it, while a wrong
// category is a listing nobody ever finds.
const DefaultConditionID = "3000"

// Options configures one export run.
//
// The zero value is not usable. BaseURL and Currency have no defensible default:
// guessing at either produces a file that imports cleanly and lists the wrong
// thing, which is the failure mode this package is built to avoid.
type Options struct {
	// BaseURL is the public origin photos are served from, e.g.
	// "https://warehouse.example.com". It must be absolute — see
	// [Exporter.Write] for why a relative one is refused.
	BaseURL string

	// Currency is the ISO 4217 code this run publishes prices in. Offers priced
	// in anything else are refused rather than converted; a conversion needs a
	// dated rate, and a rate picked inside an exporter is an assumption buried
	// in a number nobody can reproduce.
	Currency string

	// Category is the marketplace's own category identifier. eBay only, and
	// required there: eBay's category numbers are its own taxonomy and no
	// default could be right for a warehouse that sells anything.
	Category string

	// ConditionID is eBay's numeric condition code. Empty means
	// [DefaultConditionID]. eBay only.
	ConditionID string

	// Location is the city an eBay item ships from. eBay shows it on every
	// listing and uses it to quote postage, so it is required there. eBay only.
	Location string
}

// currency returns the ISO code to publish, normalised to the uppercase form
// [core.Money] stores, so that an operator typing "eur" is not a mismatch.
func (o Options) currency() string {
	return strings.ToUpper(strings.TrimSpace(o.Currency))
}

// validateCommon checks the options every profile depends on.
func (o Options) validateCommon() error {
	if err := validateBaseURL(o.BaseURL); err != nil {
		return err
	}
	cur := o.currency()
	if cur == "" {
		return fmt.Errorf("%w: a currency is required; an export that does not say what its "+
			"prices are in is read as the marketplace's default and mis-prices the whole feed",
			ErrOptions)
	}
	// Fail here rather than per row: an unrenderable currency is one wrong
	// setting, and reporting it once is clearer than reporting it per offer.
	if _, err := (core.Money{Currency: cur}).Exponent(); err != nil {
		return fmt.Errorf("%w: %w", ErrOptions, err)
	}
	return nil
}

// validateBaseURL refuses a photo base that cannot be fetched from outside this
// machine.
//
// Shopify and eBay download images from their own servers, so a relative path
// produces a listing with no pictures and no error message from the marketplace
// — a silent failure, which is the reason this check exists at all.
//
// It cannot catch every unfetchable base: "http://localhost:8080" is absolute
// and still unreachable from a marketplace. Absoluteness is the part that can be
// decided from the string alone.
func validateBaseURL(base string) error {
	b := strings.TrimSpace(base)
	for _, scheme := range []string{"https://", "http://"} {
		if rest, ok := strings.CutPrefix(b, scheme); ok && rest != "" {
			return nil
		}
	}
	return fmt.Errorf("%w: base URL %q must be absolute and start with https:// or http://, "+
		"because a marketplace fetches photos from its own servers and a relative URL there "+
		"fails silently", ErrOptions, base)
}

// Exporter renders offers as one marketplace's CSV dialect.
//
// Implementations are stateless values, so one instance is safe to share across
// goroutines; [For] returns a shared one.
type Exporter interface {
	// Name returns the profile's registry name, e.g. "shopify".
	Name() string
	// Write renders offers to w. Every offer is validated before anything is
	// written, so a refused export leaves w untouched rather than half a
	// catalogue that reads like a whole one.
	Write(w io.Writer, offers []core.Offer, opt Options) error
}

// registry holds one instance per profile, in no particular order; [Names]
// sorts. A slice rather than a map because there are two of them and each
// already knows its own name.
var registry = []Exporter{Shopify{}, EBay{}}

// For returns the exporter registered under name, matching case-insensitively
// so that a profile arriving from a form or a CLI flag does not fail on "Ebay".
// It returns an error wrapping [ErrUnknownProfile] when nothing is registered.
func For(name string) (Exporter, error) {
	key := strings.ToLower(strings.TrimSpace(name))
	for _, e := range registry {
		if e.Name() == key {
			return e, nil
		}
	}
	return nil, fmt.Errorf("%w: %q; known profiles are %s",
		ErrUnknownProfile, name, strings.Join(Names(), ", "))
}

// Names returns every registered profile name, sorted, for populating a select
// box or a --profile flag's help text.
func Names() []string {
	out := make([]string, 0, len(registry))
	for _, e := range registry {
		out = append(out, e.Name())
	}
	sort.Strings(out)
	return out
}

// exportable returns the offers a profile should render, in the order given, and
// refuses the whole run when any of them would produce a broken row.
//
// The two behaviours are deliberately different. A non-exportable status is
// dropped without comment, because deciding that is the whole job of
// [core.Status.Exportable]. Anything else is an error naming the SKU: a row
// missing from a feed is invisible until somebody counts the listings.
func exportable(offers []core.Offer, opt Options) ([]core.Offer, error) {
	want := opt.currency()
	out := make([]core.Offer, 0, len(offers))
	var problems []error
	for _, o := range offers {
		if !o.Status.Exportable() {
			continue
		}
		switch {
		case o.Shop.IsZero():
			problems = append(problems, fmt.Errorf(
				"%w: %s has no shop price, and a marketplace row without one is not a listing",
				ErrIncomplete, o.SKU))
		case o.Shop.Currency != want:
			problems = append(problems, fmt.Errorf(
				"%w: %s is priced in %s but this export publishes %s; convert it at a dated "+
					"rate before exporting", ErrIncomplete, o.SKU, o.Shop.Currency, want))
		}
		if len(o.Photos) == 0 {
			problems = append(problems, fmt.Errorf(
				"%w: %s has no photos, and a listing with no picture is one nobody clicks",
				ErrIncomplete, o.SKU))
		}
		out = append(out, o)
	}
	if len(problems) > 0 {
		return nil, errors.Join(problems...)
	}
	return out, nil
}

// photoURLs renders every photo's absolute URL in display order.
//
// [core.Offer] documents Photos as already ordered with the primary first, so
// this does not re-sort: a store returning them shuffled is a bug there, and
// papering over it here would hide it.
func photoURLs(o core.Offer, base string) []string {
	urls := make([]string, 0, len(o.Photos))
	for _, p := range o.Photos {
		urls = append(urls, p.PublicURL(base))
	}
	return urls
}

// writeCSV writes header then rows, so both profiles fail a broken io.Writer the
// same way instead of each inventing a message for it.
// customColumns returns the exported custom fields present across offers, in a
// stable order (ADR-021).
//
// ⚠ A CSV HAS ONE HEADER ROW AND THE OFFERS IN AN EXPORT DO NOT SHARE A
// CATEGORY. A turbocharger and a graphics card inherit different questions, so
// there is no fixed column set to declare in advance and no profile can carry
// one. The header is therefore a function of the ROWS: the union of every
// exported field over the offers actually being written.
//
// Two exports of different batches can legitimately have different headers. That
// is a property of the data rather than a defect, and both marketplaces tolerate
// columns they do not recognise. The alternative — a fixed set — means either
// every field in the whole tree as columns on every row, or a per-category export
// that cannot mix; both are worse for the operator than a wide, mostly-empty row.
//
// The order is category path, then the operator's position within a level, then
// label and id as tie-breaks. Path first so a batch's columns group by what the
// things ARE; the last two so the order is total and the same batch in a
// different sequence produces a byte-identical header.
//
// ⚠ A field NOT marked for export is absent entirely — no column, no cell. Off is
// the default, because a private note about where a part came from is not
// something to publish.
func customColumns(offers []core.Offer) []core.CategoryField {
	seen := map[string]bool{}
	out := []core.CategoryField{}
	for _, o := range offers {
		for _, f := range o.Fields {
			if !f.Export || seen[f.ID] {
				continue
			}
			seen[f.ID] = true
			out = append(out, f.CategoryField)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.CategoryPath != b.CategoryPath {
			return a.CategoryPath < b.CategoryPath
		}
		if a.Position != b.Position {
			return a.Position < b.Position
		}
		if a.Label != b.Label {
			return a.Label < b.Label
		}
		return a.ID < b.ID
	})
	return out
}

// customCells returns o's answers for cols, in the same order.
//
// An offer that has no such field gets an EMPTY cell rather than a missing one:
// every row must carry exactly as many cells as the header, or the file is not a
// CSV any importer will read.
func customCells(o core.Offer, cols []core.CategoryField) []string {
	byID := make(map[string]core.OfferField, len(o.Fields))
	for _, f := range o.Fields {
		byID[f.ID] = f
	}
	cells := make([]string, len(cols))
	for i, c := range cols {
		// ⚠ No second Export check here, and its absence is deliberate. cols holds
		// only exported fields, and a field's export flag is a property of the
		// DEFINITION rather than of one offer's answer — so `f.Export` at this point
		// is always true. A mutation run proved it: deleting the check changed
		// nothing, because the state it guarded against cannot occur. Defensive
		// handling for an impossible state reads as a real rule to the next person.
		if f, ok := byID[c.ID]; ok {
			// Display, not the raw value: it renders a yes/no as words and appends a
			// unit, so "180000" reaches the marketplace as "180000 km". The unit is
			// stored beside the number precisely so it can be shown without ever
			// having been typed into the value.
			cells[i] = f.Display()
		}
	}
	return cells
}

// customLabels maps the columns to their header text through name.
//
// ⚠ THE LABEL IS WHAT REACHES THE MARKETPLACE, so renaming a field renames a
// published column. Nothing internal breaks — the field's `code` is the stable
// key everything here uses — but a listing tool on the far side might.
func customLabels(cols []core.CategoryField, name func(core.CategoryField) string) []string {
	out := make([]string, len(cols))
	for i, c := range cols {
		out[i] = name(c)
	}
	return out
}

func writeCSV(w io.Writer, profile string, header []string, rows [][]string) error {
	cw := csv.NewWriter(w)
	if err := cw.Write(header); err != nil {
		return fmt.Errorf("export: writing %s header: %w", profile, err)
	}
	// WriteAll flushes, so its error is the last chance to see a failed write.
	if err := cw.WriteAll(rows); err != nil {
		return fmt.Errorf("export: writing %s rows: %w", profile, err)
	}
	return nil
}
