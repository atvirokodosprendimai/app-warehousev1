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
	// Ask is the asking price. This is the price that goes into an export.
	Ask Money
	// Sold is what it actually fetched. Zero until the sale is recorded, and
	// deliberately separate from Ask so a discount is visible in a report.
	Sold Money
	// SoldAt is when the sale happened, in UTC. Nil until sold.
	SoldAt *time.Time
	// LocationID is where the item physically is. Empty if not yet put away.
	LocationID string
	// Photos are the offer's images in display order. The first is the primary.
	Photos []Photo
	// CreatedAt and UpdatedAt are UTC timestamps.
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Validate checks the domain rules that hold regardless of who is writing.
//
// It deliberately does NOT require a price on a draft: an operator photographs
// and shelves an item before deciding what to ask for it, and forcing a price at
// creation time would push them to type a placeholder that later ships to a
// marketplace.
func (o *Offer) Validate() error {
	if strings.TrimSpace(o.Title) == "" {
		return fmt.Errorf("%w: title is required", ErrInvalid)
	}
	if strings.TrimSpace(o.SKU) == "" {
		return fmt.Errorf("%w: SKU is required", ErrInvalid)
	}
	if !o.Status.Valid() {
		return fmt.Errorf("%w: unknown status %q", ErrInvalid, o.Status)
	}
	if o.Quantity < 0 {
		return fmt.Errorf("%w: quantity cannot be negative", ErrInvalid)
	}
	if o.Status.Exportable() && o.Ask.IsZero() {
		return fmt.Errorf("%w: an offer in status %s needs an asking price, because that "+
			"status is exported to a marketplace", ErrInvalid, o.Status)
	}
	if o.Status == StatusSold && o.SoldAt == nil {
		return fmt.Errorf("%w: a sold offer needs a sale date, or the revenue cannot be "+
			"attributed to a period", ErrInvalid)
	}
	if o.Ask.Currency != "" {
		if _, err := o.Ask.Exponent(); err != nil {
			return err
		}
	}
	if o.Sold.Currency != "" {
		if _, err := o.Sold.Exponent(); err != nil {
			return err
		}
	}
	return nil
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
