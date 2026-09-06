// Package view holds the read models and templ components that render them.
//
// A read model here is a plain struct: the handler builds it from a repository
// and hands it to a templ function. That pairing is deliberate and is the whole
// rendering contract — think of it as json.Marshal for HTML. The same struct is
// used by the initial page load and by any SSE patch, so a fragment can never
// drift from the page it lives in.
package view

import (
	"strings"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
)

// DatastarBundle is the client bundle the pages load.
//
// It is pinned rather than floating: the team's recorded datastar findings —
// including the positional array-binding behaviour of bound checkboxes — were
// measured against this exact build, so a floating tag would silently move the
// code out from under those notes. The Go SDK versions independently of the
// bundle and carries no constant naming a client version, so this pin is a
// decision, not a lookup.
const DatastarBundle = "https://cdn.jsdelivr.net/gh/starfederation/datastar@v1.0.2/bundles/datastar.js"

// Page is the chrome every signed-in screen shares.
type Page struct {
	// Title is the browser title and the heading in the top bar.
	Title string
	// Nav is which sidebar entry is current.
	Nav string
	// User is who is signed in.
	User core.User
	// Counts drives the per-status badges in the sidebar.
	Counts map[core.Status]int
	// NeedsPricing is how many drafts are still waiting on the research step.
	NeedsPricing int
}

// Initial returns the user's initials for the sidebar avatar.
func (p Page) Initial() string {
	n := strings.TrimSpace(p.User.Name())
	if n == "" {
		return "?"
	}
	return strings.ToUpper(n[:1])
}

// Role renders the user's role for the sidebar.
func (p Page) Role() string {
	if p.User.IsAdmin {
		return "Administrator"
	}
	return "Staff"
}

// Count returns how many offers sit in a status, safely on a nil map.
func (p Page) Count(s core.Status) int {
	if p.Counts == nil {
		return 0
	}
	return p.Counts[s]
}

// Dashboard is the landing screen's read model.
type Dashboard struct {
	Page Page
	// Recent is the newest handful of offers, whatever their status.
	Recent []OfferRow
	// Pricing is the queue of drafts with no price yet — the work the intake
	// flow deliberately defers.
	Pricing []OfferRow
	// StockValue is the shop-price total of everything currently listed.
	StockValue core.Money
	// OwedToHolders is what is owed to owners for that same stock.
	OwedToHolders core.Money
	// SoldThisMonth is realised revenue in the base currency.
	SoldThisMonth core.Money
	// Rate is the newest EUR/USD reference rate, or the zero value when none has
	// been fetched yet.
	Rate core.Rate
}

// OfferRow is one line in a listing. It is flattened deliberately: a table cell
// should not have to walk an object graph, and resolving the placement once in
// the handler is cheaper than once per render.
type OfferRow struct {
	Offer core.Offer
	// Where is the resolved custodian and city for the offer's location.
	Where core.Placement
	// LocationPath is the materialised address, e.g. "KAUNAS/S3/A000005".
	LocationPath string
}

// Thumb returns the primary photo's public path, or "" when there is none.
func (r OfferRow) Thumb() string {
	p := r.Offer.PrimaryPhoto()
	if p.ID == "" {
		return ""
	}
	return p.PublicPath()
}

// PriceText renders the shop price, or a dash when pricing is still pending.
func (r OfferRow) PriceText() string {
	if r.Offer.Shop.IsZero() {
		return "—"
	}
	return r.Offer.Shop.String()
}

// OwnerText renders what the holder wants, or a dash.
func (r OfferRow) OwnerText() string {
	if r.Offer.Owner.IsZero() {
		return "—"
	}
	return r.Offer.Owner.String()
}

// MarginText renders the margin, or a dash when it cannot be computed.
func (r OfferRow) MarginText() string {
	m, err := r.Offer.Margin()
	if err != nil {
		return "—"
	}
	return m.String()
}

// BadgeClass returns the CSS class for the offer's status badge.
func (r OfferRow) BadgeClass() string { return "badge badge-" + string(r.Offer.Status) }

// OfferList is the offers screen.
type OfferList struct {
	Page Page
	Rows []OfferRow
	// Filter is the filter currently applied, so the toolbar can render its own
	// state from the server rather than from a client-held copy.
	Filter core.OfferFilter
	// Total is how many offers match before paging.
	Total int
}

// StatusActive reports whether a status chip should render pressed.
func (l OfferList) StatusActive(s core.Status) bool {
	for _, x := range l.Filter.Status {
		if x == s {
			return true
		}
	}
	return false
}

// OfferDetail is the single-offer screen.
type OfferDetail struct {
	Page Page
	Row  OfferRow
	// Locations is the flat, path-ordered tree for the location picker.
	Locations []core.Location
	// Currencies is the list a price input may choose from.
	Currencies []string
	// PublicBase is the origin a marketplace will fetch photos from. Shown to
	// the operator because an export is worthless if it is wrong.
	PublicBase string
}

// LocationTree is the warehouse screen.
type LocationTree struct {
	Page  Page
	Nodes []LocationNode
}

// LocationNode is one row of the flattened tree.
type LocationNode struct {
	Location core.Location
	// Depth is how far to indent, derived from the path rather than stored.
	Depth int
	// Where is the resolved custodian and city.
	Where core.Placement
	// Stock is how many offers sit at or beneath this node.
	Stock int
}

// Indent returns an inline style that indents the row by its depth.
func (n LocationNode) Indent() string {
	if n.Depth == 0 {
		return ""
	}
	return "padding-left:" + itoa(n.Depth*18) + "px"
}

// itoa is a tiny non-negative integer formatter, avoiding a strconv import in a
// file that otherwise needs none.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [8]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// Users is the account-management screen.
type Users struct {
	Page  Page
	Users []core.User
}

// Auth is the sign-in and bootstrap screen.
type Auth struct {
	// Bootstrap is true when the database holds no users at all, which is the
	// only circumstance in which anyone may create an account for themselves.
	Bootstrap bool
	// Error is a message to show above the fields, empty when there is none.
	Error string
}
