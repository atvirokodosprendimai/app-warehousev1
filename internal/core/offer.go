package core

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrInvalid reports a value that fails a domain rule. Handlers match on it to
// decide between a 200-with-error-fragment and a genuine server fault.
var ErrInvalid = errors.New("core: invalid")

// ErrNotFound reports that a requested aggregate does not exist.
var ErrNotFound = errors.New("core: not found")

// Status is where an offer sits in its selling lifecycle.
//
// It is a closed set rather than free text because the dashboard filters on it,
// the CSV export decides publish-or-not from it, and reports count by it — three
// consumers that would each have to guess at a typo.
type Status string

const (
	// StatusDraft is being prepared and is not exported to any marketplace.
	StatusDraft Status = "draft"
	// StatusListed is published and available to buy.
	StatusListed Status = "listed"
	// StatusPending has a buyer but the money or the handover is not complete.
	StatusPending Status = "pending"
	// StatusSold is finished; SoldPrice and SoldAt are expected to be set.
	StatusSold Status = "sold"
	// StatusArchived is withdrawn and kept only for the record.
	StatusArchived Status = "archived"
)

// Statuses lists every status in lifecycle order, for rendering filters and
// select boxes without each caller hardcoding the set.
func Statuses() []Status {
	return []Status{StatusDraft, StatusListed, StatusPending, StatusSold, StatusArchived}
}

// Label returns a human-readable name for the status.
func (s Status) Label() string {
	switch s {
	case StatusDraft:
		return "Draft"
	case StatusListed:
		return "Listed"
	case StatusPending:
		return "Pending"
	case StatusSold:
		return "Sold"
	case StatusArchived:
		return "Archived"
	}
	return string(s)
}

// Valid reports whether s is a status this application defines.
func (s Status) Valid() bool {
	for _, k := range Statuses() {
		if k == s {
			return true
		}
	}
	return false
}

// Exportable reports whether an offer in this status should appear in a
// marketplace CSV. Draft is unfinished and archived is withdrawn; publishing
// either would list stock that is not for sale.
func (s Status) Exportable() bool {
	return s == StatusListed || s == StatusPending
}

// ParseStatus validates a status coming off a form or a query string.
func ParseStatus(s string) (Status, error) {
	st := Status(strings.ToLower(strings.TrimSpace(s)))
	if !st.Valid() {
		return "", fmt.Errorf("%w: unknown status %q", ErrInvalid, s)
	}
	return st, nil
}

// Offer is the sellable unit: one thing in the warehouse, described once and
// exported to every marketplace from that one description.
type Offer struct {
	// ID is the immutable internal identifier (a UUID).
	ID string
	// SKU is the operator-facing handle. It is what appears in Shopify's Handle
	// column and eBay's CustomLabel, so it must be stable once published.
	SKU string
	// Title is the listing headline.
	Title string
	// Description is the listing body. Stored as the operator typed it; the
	// exporters decide how to present it per marketplace.
	Description string
	// Condition is free text such as "used - good".
	Condition string
	// Status is the lifecycle position.
	Status Status
	// Quantity is how many of this item are held.
	Quantity int
	// Shop is the price we publish. This is the only price that ever reaches a
	// marketplace export.
	Shop Money
	// Owner is what the item's owner wants to receive for it — the person whose
	// distributed warehouse it sits in, resolved through the location's
	// custodian. It is deliberately a separate figure from Shop rather than a
	// margin percentage: the two are negotiated with different people at
	// different times, and storing only the difference would lose whichever one
	// was agreed first.
	//
	// It must never be exported. A buyer seeing what the holder was paid is a
	// disclosure the business did not choose to make.
	Owner Money
	// Sold is what it actually fetched. Zero until the sale is recorded, and
	// deliberately separate from Shop so a discount is visible in a report.
	Sold Money
	// SoldAt is when the sale happened, in UTC. Nil until sold.
	SoldAt *time.Time
	// LocationID is where the item physically is. Empty if not yet put away.
	LocationID string
	// Photos are the offer's images in display order. The first is the primary.
	Photos []Photo
	// Categories is the marketplace category this item should be listed under,
	// keyed by export profile name ("ebay", "shopify"). A missing entry means
	// "use the configured default for that profile", which is the ordinary case:
	// requiring a taxonomy number at intake would block the photograph-title-
	// shelve flow this type deliberately permits.
	//
	// The values are not interchangeable between profiles. eBay's is a number
	// from its own taxonomy; Allegro's is a category NAME. That is why this is a
	// map keyed by profile rather than one field.
	Categories map[string]string
	// CreatedAt and UpdatedAt are UTC timestamps.
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Validate checks the domain rules that hold regardless of who is writing.
//
// It deliberately does NOT require a price on a draft, and since ADR-019 it does
// not require a TITLE on one either. The intake flow is photograph, title,
// shelve — and both of the later steps can happen later still, increasingly by a
// DIFFERENT PERSON: one walks the warehouse photographing things, another names
// and describes what they photographed. Demanding either value at creation is
// what pushes an operator to invent one, and a placeholder that reaches
// StatusListed ships to a marketplace as though it were real.
//
// Both become mandatory at the same boundary — the moment the offer enters a
// status that is exported — because that is the moment somebody outside the
// building reads them. The SKU is different and stays required from the start:
// it is the reference written on the box (ADR-010), so it has to exist before
// the item is put on a shelf.
func (o *Offer) Validate() error {
	if strings.TrimSpace(o.SKU) == "" {
		return fmt.Errorf("%w: SKU is required", ErrInvalid)
	}
	if !o.Status.Valid() {
		return fmt.Errorf("%w: unknown status %q", ErrInvalid, o.Status)
	}
	if o.Quantity < 0 {
		return fmt.Errorf("%w: quantity cannot be negative", ErrInvalid)
	}
	if o.Status.Exportable() && strings.TrimSpace(o.Title) == "" {
		return fmt.Errorf("%w: an offer in status %s needs a title, because that "+
			"status is exported to a marketplace", ErrInvalid, o.Status)
	}
	if o.Status.Exportable() && o.Shop.IsZero() {
		return fmt.Errorf("%w: an offer in status %s needs a shop price, because that "+
			"status is exported to a marketplace", ErrInvalid, o.Status)
	}
	if o.Status == StatusSold && o.SoldAt == nil {
		return fmt.Errorf("%w: a sold offer needs a sale date, or the revenue cannot be "+
			"attributed to a period", ErrInvalid)
	}
	for _, m := range []Money{o.Shop, o.Owner, o.Sold} {
		if m.Currency == "" {
			continue
		}
		if _, err := m.Exponent(); err != nil {
			return err
		}
	}
	return nil
}

// NeedsPricing reports whether the offer is waiting on the research step — it
// has been photographed and titled but has no shop price yet.
//
// This is derived rather than stored as its own status, because it is a property
// of the price fields and a stored duplicate would go stale the moment somebody
// typed a price without also moving the status.
func (o *Offer) NeedsPricing() bool {
	return o.Status == StatusDraft && o.Shop.IsZero()
}

// NeedsDescribing reports whether the offer is waiting on the cataloguing step —
// it has been photographed but nobody has named it yet.
//
// This is the hand-off point between two people (ADR-019): a photographer creates
// the group of pictures, and whoever describes it finds it through this. It is
// derived rather than stored for the same reason NeedsPricing is — a stored
// duplicate goes stale the moment somebody types a title without also moving a
// status, and the queue then shows work that is already done.
func (o *Offer) NeedsDescribing() bool {
	return o.Status == StatusDraft && strings.TrimSpace(o.Title) == ""
}

// Margin returns what the business keeps: the shop price less what the owner
// wants. It is the number that decides whether listing the item is worth doing.
//
// It refuses to subtract across currencies rather than converting silently,
// because the rate that applied would be an assumption buried inside a figure
// nobody could reproduce. Convert deliberately, with a dated [Rate], first.
func (o *Offer) Margin() (Money, error) {
	if o.Shop.IsZero() {
		return Money{}, fmt.Errorf("%w: no shop price yet, so there is no margin to report",
			ErrInvalid)
	}
	if o.Owner.IsZero() {
		// Nothing owed to a holder: the whole shop price is margin.
		return o.Shop, nil
	}
	if o.Shop.Currency != o.Owner.Currency {
		return Money{}, fmt.Errorf("%w: shop price is %s and the owner wants %s; convert one "+
			"of them at a dated rate before comparing", ErrInvalid,
			o.Shop.Currency, o.Owner.Currency)
	}
	return Money{Minor: o.Shop.Minor - o.Owner.Minor, Currency: o.Shop.Currency}, nil
}

// Realised returns what the business actually kept on a completed sale: the sold
// price less what the owner wants. Zero-value Sold means the sale is not
// recorded yet, which is a different thing from a sale that made nothing.
func (o *Offer) Realised() (Money, error) {
	if o.Sold.IsZero() {
		return Money{}, fmt.Errorf("%w: no sold price recorded", ErrInvalid)
	}
	if o.Owner.IsZero() {
		return o.Sold, nil
	}
	if o.Sold.Currency != o.Owner.Currency {
		return Money{}, fmt.Errorf("%w: sold in %s but the owner wants %s; convert at the "+
			"rate for the sale date before comparing", ErrInvalid,
			o.Sold.Currency, o.Owner.Currency)
	}
	return Money{Minor: o.Sold.Minor - o.Owner.Minor, Currency: o.Sold.Currency}, nil
}

// PrimaryPhoto returns the first photo, or the zero Photo when there are none.
func (o *Offer) PrimaryPhoto() Photo {
	if len(o.Photos) == 0 {
		return Photo{}
	}
	return o.Photos[0]
}

// Photo is one image belonging to an offer.
//
// The ID is a UUID and it is also the public URL's path segment, so that the URL
// handed to Shopify or eBay leaks neither the original filename nor a guessable
// sequence — a marketplace fetcher needs the URL to be stable and public, which
// is exactly the pair that makes an enumerable one a mistake.
type Photo struct {
	// ID is the UUID that names this photo publicly.
	ID string
	// OfferID is the owning offer.
	OfferID string
	// Position orders the photos; 0 is primary.
	Position int
	// Filename is the operator's original name, kept for their benefit only.
	Filename string
	// ContentType is the stored image's MIME type.
	ContentType string
	// ByteSize is the stored size.
	ByteSize int64
	// SHA256 is the content hash, hex-encoded, so a re-upload of the same bytes
	// is detectable.
	SHA256 string
	// CreatedAt is a UTC timestamp.
	CreatedAt time.Time
}

// Ext returns the file extension implied by the photo's content type, including
// the leading dot. The public URL carries it so that a marketplace fetcher which
// sniffs the URL rather than the Content-Type header still gets it right.
func (p Photo) Ext() string {
	switch p.ContentType {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	}
	return ".bin"
}

// PublicPath returns the photo's absolute path on this server, without a host.
func (p Photo) PublicPath() string {
	return "/p/" + p.ID + p.Ext()
}

// PublicURL returns the absolute URL a marketplace will fetch, given the
// deployment's public base (e.g. "https://warehouse.example.com").
//
// An export is worthless without this: Shopify and eBay fetch the image from
// their own servers, so a relative path or a localhost URL produces a listing
// with no pictures and no error message.
func (p Photo) PublicURL(base string) string {
	return strings.TrimRight(base, "/") + p.PublicPath()
}
