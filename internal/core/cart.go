package core

import (
	"fmt"
	"strings"
	"time"
)

// Cart is a named, saved selection of offers.
//
// It exists so that a batch destined for one marketplace can be assembled over
// days and exported as a unit, rather than reconstructed from a filter every
// time. That distinction is the whole point: a filter answers "everything that
// currently matches", which changes underneath you as stock moves, while a cart
// answers "the things I chose", which does not.
//
// A cart holds REFERENCES, never copies. It stores offer ids and nothing about
// the offers themselves, so an item repriced after being added exports at its
// new price — the cart is a selection, not a snapshot, and a snapshot would
// quietly publish a price nobody meant to publish.
type Cart struct {
	// ID is the immutable internal identifier (a UUID).
	ID string
	// Name is what the operator calls it, e.g. "eBay batch — September".
	Name string
	// Note is free text: what this batch is for, who it is going to.
	Note string
	// Items are the offer references in display order.
	Items []CartItem
	// CreatedAt and UpdatedAt are UTC timestamps. UpdatedAt moves when an item is
	// added or removed, so "which batch was I working on" is answerable.
	CreatedAt time.Time
	UpdatedAt time.Time
}

// CartItem is one offer's membership of a cart.
type CartItem struct {
	// CartID and OfferID identify the membership. Together they are unique: an
	// offer is either in a cart or not, and adding it twice is not meaningful.
	CartID  string
	OfferID string
	// Position orders the items for display and for the exported file, so a batch
	// arrives at the marketplace in the order it was assembled.
	Position int
	// AddedAt is when it was put in, in UTC.
	AddedAt time.Time
}

// Validate checks a cart's own rules.
func (c *Cart) Validate() error {
	if strings.TrimSpace(c.Name) == "" {
		return fmt.Errorf("%w: a cart needs a name, or it cannot be told from the "+
			"other saved batches", ErrInvalid)
	}
	return nil
}

// Size returns how many offers the cart holds.
func (c *Cart) Size() int { return len(c.Items) }

// Has reports whether an offer is already in the cart.
func (c *Cart) Has(offerID string) bool {
	for _, it := range c.Items {
		if it.OfferID == offerID {
			return true
		}
	}
	return false
}

// CartContents is a cart with its offers resolved — the read model an export or
// a cart page works from.
type CartContents struct {
	// Cart is the cart itself.
	Cart Cart
	// Offers are its offers, in cart order.
	Offers []Offer
}

// Exportable splits the cart into what a marketplace export will publish and
// what it will not.
//
// Returning BOTH halves is deliberate: a cart's members were each chosen by
// hand, so dropping one without saying so loses an item the operator believes
// they are sending. The caller is expected to show the second slice, not discard
// it.
func (c CartContents) Exportable() (send []Offer, held []Offer) {
	return PartitionExportable(c.Offers)
}

// PartitionExportable splits offers into those a marketplace export will carry
// and those it will not.
//
// It is shared by the cart export and the whole-catalogue export rather than
// each deciding for itself, because "can this be published" is one question and
// two implementations of it would drift — leaving a page that promises to send
// something the exporter then refuses.
//
// The three conditions are exactly what an exporter needs and cannot invent: a
// status that means it is for sale, a price to sell it at, and a picture, since
// a listing with no photograph is one nobody clicks.
func PartitionExportable(offers []Offer) (send []Offer, held []Offer) {
	for _, o := range offers {
		if HoldReason(o) == "" {
			send = append(send, o)
			continue
		}
		held = append(held, o)
	}
	return send, held
}

// HoldReason explains, for one offer, why an export would not carry it.
//
// It returns "" when the offer is exportable, so a caller can use it directly as
// the cell contents in a "not being sent" table.
func HoldReason(o Offer) string {
	switch {
	case !o.Status.Exportable():
		return "status is " + o.Status.Label()
	case o.Shop.IsZero():
		return "no shop price yet"
	case len(o.Photos) == 0:
		return "no photographs"
	}
	return ""
}
