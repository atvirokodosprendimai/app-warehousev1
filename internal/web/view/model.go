// Package view holds the read models and templ components that render them.
//
// A read model here is a plain struct: the handler builds it from a repository
// and hands it to a templ function. That pairing is deliberate and is the whole
// rendering contract — think of it as json.Marshal for HTML. The same struct is
// used by the initial page load and by any SSE patch, so a fragment can never
// drift from the page it lives in.
package view

import (
	"net/url"
	"strings"

	"github.com/a-h/templ"

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
	// NeedsDescribing is how many photograph groups are still waiting on the
	// cataloguing step — one person photographed them, nobody has named them yet
	// (ADR-019). It is the hand-off signal between the two, so it is carried on
	// every page rather than only on the queue.
	NeedsDescribing int
	// InboxOpen is how many staff submissions are still waiting on a decision.
	// It drives the live banner, so it is carried on every page rather than only
	// on the inbox — the whole point of the banner is to reach an administrator
	// who is looking somewhere else.
	InboxOpen int
	// StreamPath is the ONE SSE endpoint this page opens.
	//
	// One connection per page, not one per widget: the server already knows which
	// fragments changed, so a second stream buys nothing and costs a socket. The
	// path carries what this page needs — the offer editor asks for its own id —
	// and the server decides everything that comes back down it.
	StreamPath string
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

// PhotoCountText renders how many photographs a row carries.
//
// It exists for the describing queue, where the row has no title yet: the count
// and the reference are then the ONLY things distinguishing one waiting item
// from another, and the count is also what says whether the group is complete
// enough to describe.
func (r OfferRow) PhotoCountText() string {
	n := len(r.Offer.Photos)
	if n == 1 {
		return "1 photograph"
	}
	return itoa(n) + " photographs"
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
	// Currencies is the list the price-range filter may choose from.
	Currencies []string
	// Carts are the saved carts, for the Add buttons to put things into.
	Carts []core.Cart
	// ActiveCart is the one Add currently targets. Empty when none exists yet,
	// in which case the first Add creates one.
	ActiveCart string
}

// RowsPath is the endpoint the live search asks for a new table.
//
// It carries the STATUS part of the current filter, because that part lives in
// the page URL and a bare "/offers/rows" would not know about it — so typing in
// the search box while looking at "Listed" would quietly widen the result to
// every status. The text and price parts are not here: they travel as signals,
// which is the whole reason they are not in the URL.
func (l OfferList) RowsPath() string {
	q := make([]string, 0, 3)
	for _, s := range l.Filter.Status {
		q = append(q, "status="+string(s))
	}
	if l.Filter.NeedsPricing {
		q = append(q, "needs_pricing=1")
	}
	if l.Filter.NeedsDescribing {
		q = append(q, "needs_describing=1")
	}
	if l.Filter.LocationPathPrefix != "" {
		q = append(q, "at="+url.QueryEscape(l.Filter.LocationPathPrefix))
	}
	if len(q) == 0 {
		return "/offers/rows"
	}
	return "/offers/rows?" + strings.Join(q, "&")
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
	// Carts is every saved batch, so this offer can be added to one.
	Carts []core.Cart
	// InCarts are the batches this offer is already in. Showing them is what
	// stops the same item being put into two batches destined for two different
	// marketplaces without anyone noticing.
	InCarts []core.Cart
	// Categories is the flat, path-ordered taxonomy tree for the "what is this"
	// picker (ADR-021). ⚠ Not the marketplace category, which is per profile and
	// lives on the Marketplace card.
	Categories []core.Category
	// Fields are the questions this offer's category asks — its own and every
	// ancestor's, root first — with the answers already given. Empty when the
	// offer has no category, which is the ordinary case.
	Fields []core.OfferField
}

// FieldsByLevel groups Fields by the category that defined them, preserving the
// root-first order the read returned.
//
// The grouping is done here rather than in the template because templ has no
// good way to express "start a new group when this row's path differs from the
// last", and because the ORDER is the contract: a group that sorted itself would
// quietly discard the inheritance order the whole record is about.
func (d OfferDetail) FieldsByLevel() []FieldGroup {
	out := []FieldGroup{}
	for _, f := range d.Fields {
		if n := len(out); n > 0 && out[n-1].Path == f.CategoryPath {
			out[n-1].Fields = append(out[n-1].Fields, f)
			continue
		}
		out = append(out, FieldGroup{Path: f.CategoryPath, Fields: []core.OfferField{f}})
	}
	return out
}

// FieldGroup is one category's worth of questions, labelled with the level they
// came from so an operator can see WHY they are being asked.
type FieldGroup struct {
	// Path is the materialised path of the category that defined these fields.
	Path string
	// Fields are its questions, in the operator's order.
	Fields []core.OfferField
}

// Leaf returns the last segment of the group's path, which is the level's own
// code — "ENGINE" out of "CAR/ENGINE".
func (g FieldGroup) Leaf() string {
	if i := strings.LastIndex(g.Path, "/"); i >= 0 {
		return g.Path[i+1:]
	}
	return g.Path
}

// Taxonomy is the read model for the screen an operator shapes their own tree
// on (ADR-021).
//
// ⚠ It is NOT the warehouse tree. `/warehouse` addresses where a thing IS;
// this addresses what a thing is. They are the same shape on purpose — an
// operator who has moved a shelf should not have to learn a second idiom to move
// a category — and they are different data.
type Taxonomy struct {
	Page Page
	// Tree is every node ordered by path, which is tree order.
	Tree []core.Category
	// Selected is the node being edited. Zero when nothing is selected, which is
	// the screen's first state.
	Selected core.Category
	// HasSelection distinguishes "nothing selected" from "a node whose id is
	// empty", which cannot happen but would render identically if it could.
	HasSelection bool
	// Own are the questions defined ON the selected node — the editable ones.
	Own []core.CategoryField
	// Inherited are the questions it gets from its ancestors. Shown so the
	// operator can see the whole set an offer will be asked, and NOT editable
	// here: editing one from a descendant would change it for every other
	// subtree under that ancestor without saying so.
	Inherited []core.CategoryField
	// ValueCounts is how many stored answers each of Own's fields has, so a
	// delete can say what it will take with it before it does.
	ValueCounts map[string]int
	// Children is how many nodes sit directly under Selected. A node with any
	// cannot be deleted.
	Children int
	// Editing is the field currently loaded into the field form, if any. An
	// empty ID means the form is adding rather than editing.
	Editing core.CategoryField
	// Kinds is the list a field's kind may be chosen from.
	Kinds []core.FieldKind
	// Templates are the starter trees on offer. They are read-only descriptions
	// here: what a press of one produces is ordinary categories and questions,
	// indistinguishable afterwards from ones somebody typed.
	Templates []core.CategoryTemplate
}

// Deletable reports whether the selected node can be removed.
func (t Taxonomy) Deletable() bool { return t.HasSelection && t.Children == 0 }

// Indent returns how deep a node sits, for rendering the tree as a list.
func (t Taxonomy) Indent(c core.Category) int { return c.Depth() }

// FieldSignal is the datastar signal name for one custom field's control.
//
// A field's id is a UUID and a signal name has to be a plain identifier, so the
// hyphens come out and an "f" goes on the front. ⚠ The result must be entirely
// LOWERCASE: HTML lowercases attribute names, so data-bind:fAB would bind the
// signal "fab" and silently miss the one this seeds — the same trap the price
// fields carry a comment about.
func FieldSignal(fieldID string) string {
	return "f" + strings.ToLower(strings.ReplaceAll(fieldID, "-", ""))
}

// FieldBind returns the datastar binding for one custom field's control.
//
// It is a spread rather than a literal attribute because the name is computed
// per field and templ attribute names are static. The value is bool true so
// templ renders the bare attribute, which is the form data-bind takes.
func FieldBind(f core.OfferField) templ.Attributes {
	return templ.Attributes{"data-bind:" + FieldSignal(f.ID): true}
}

// InCart reports whether this offer is already in a given batch.
func (d OfferDetail) InCart(cartID string) bool {
	for _, c := range d.InCarts {
		if c.ID == cartID {
			return true
		}
	}
	return false
}

// Carts is the saved-batches screen.
type Carts struct {
	Page  Page
	Carts []core.Cart
}

// CartScreen is one saved batch, with its export readiness resolved.
type CartScreen struct {
	Page Page
	Cart core.Cart
	// Send are the items an export would carry.
	Send []core.Offer
	// Held are the items it would not, each with a reason. They are shown rather
	// than dropped: every member of a batch was chosen by hand, so an export that
	// quietly omits one loses an item the operator believes they are sending.
	Held []core.Offer
	// Profiles are the marketplaces this batch can be exported to, each with
	// whether this deployment is actually configured for it.
	Profiles []ExportProfile
}

// ExportProfile is one marketplace's readiness to receive an export.
//
// Ready is false when the deployment has not been configured for it — eBay needs
// a category and an item location, and neither has a defensible default. The
// page shows the reason instead of offering a download that would fail, which is
// what it did before this type existed.
type ExportProfile struct {
	// Name is the profile's registry name, e.g. "shopify".
	Name string
	// Ready reports whether an export to this marketplace could run at all.
	Ready bool
	// Problem is why not, in the exporter's own words. Empty when Ready.
	Problem string
}

// HoldReason explains why an item is not being sent.
func (s CartScreen) HoldReason(o core.Offer) string { return core.HoldReason(o) }

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

// Submit is the staff submission screen: the form, and that person's own history.
type Submit struct {
	Page Page
	// Mine is what this person has offered, whatever came of it.
	Mine []core.Submission
	// Currencies is what an asking price may be stated in.
	Currencies []string
}

// Inbox is the administrator's triage queue.
type Inbox struct {
	Page        Page
	Submissions []core.Submission
	// ShowAll is true when the listing includes decided items as well as open
	// ones, so the header and the toggle can say which view this is.
	ShowAll bool
}

// SubmissionDetail is one proposal.
type SubmissionDetail struct {
	Page       Page
	Submission core.Submission
	// Locations is the tree, for choosing where an accepted item goes.
	Locations []core.Location
	// Currencies is what a price may be stated in.
	Currencies []string
	// CanDecide is true for an administrator. A submitter sees their own
	// proposal and its outcome, but not the panel that decides it.
	CanDecide bool
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
