package core

import "context"

// The ports below exist so that CQRS's read/write asymmetry is carried by the
// type system rather than by a paragraph nobody re-reads. A read model is handed
// a Reader and therefore CANNOT write — not because it was told not to, but
// because it was never given anything that can. The database enforces the same
// rule underneath through the reader handle's query_only pragma, so a mistake
// has to defeat both the compiler and the driver.

// OfferReader loads offers. Read models accept this and nothing wider.
type OfferReader interface {
	// Offer returns one offer with its photos, or ErrNotFound.
	Offer(ctx context.Context, id string) (Offer, error)
	// OfferBySKU resolves the operator-facing handle.
	OfferBySKU(ctx context.Context, sku string) (Offer, error)
	// Offers lists offers matching a filter, newest first.
	Offers(ctx context.Context, f OfferFilter) ([]Offer, error)
	// CountByStatus returns how many offers sit in each status, for the
	// dashboard tiles.
	CountByStatus(ctx context.Context) (map[Status]int, error)
}

// OfferWriter mutates offers. Only a service holds one.
type OfferWriter interface {
	CreateOffer(ctx context.Context, o Offer) error
	UpdateOffer(ctx context.Context, o Offer) error
	DeleteOffer(ctx context.Context, id string) error
	AddPhoto(ctx context.Context, p Photo) error
	DeletePhoto(ctx context.Context, photoID string) error
	ReorderPhotos(ctx context.Context, offerID string, photoIDsInOrder []string) error
	// SetMarketplaceValue records what this offer says for one export profile's
	// must-have field — its category, its condition, where it dispatches from.
	// An empty value clears the override, so "never set" and "cleared" are the
	// same state: both mean "use the configured default" (ADR-022).
	SetMarketplaceValue(ctx context.Context, offerID, profile string, field MarketplaceField, value string) error
}

// OfferStore is both halves, held by the write side only.
type OfferStore interface {
	OfferReader
	OfferWriter
}

// OfferFilter narrows a listing. A zero filter means "everything".
type OfferFilter struct {
	// Status, when non-empty, restricts to these statuses.
	Status []Status
	// LocationPathPrefix restricts to items stored at or beneath a path, so
	// "everything in the Kaunas garage" is one query rather than a tree walk.
	LocationPathPrefix string
	// Query is a full-text search over SKU, title, description and condition.
	//
	// It is matched against an FTS index rather than with LIKE, so a two-word
	// query finds rows containing both words in either order and in either field
	// — which is what someone typing into a search box means, and what a
	// substring match cannot do.
	Query string
	// PriceMin and PriceMax bound the SHOP price. A nil bound is open on that
	// side, so "under 50" and "over 20" are both expressible without a sentinel.
	//
	// They carry their currency with them because minor units are only comparable
	// within one: 5000 is 50 EUR and also 500 JPY, and a bound that lost track of
	// which would silently filter against the wrong scale. A bound therefore
	// restricts the result to offers priced in that same currency.
	PriceMin *Money
	PriceMax *Money
	// NeedsPricing restricts to drafts with no shop price — the work queue for
	// the research step in the photograph, title, price-later flow.
	NeedsPricing bool
	// NeedsDescribing restricts to drafts with no title — the work queue for the
	// cataloguing step, where one person photographs a thing and another names
	// and describes it afterwards (ADR-019).
	NeedsDescribing bool
	// Limit and Offset page the result. Limit 0 means the repository's default.
	Limit  int
	Offset int
}

// LocationReader loads storage locations.
type LocationReader interface {
	// Location returns one node, or ErrNotFound.
	Location(ctx context.Context, id string) (Location, error)
	// LocationByPath resolves a materialised path such as "KAUNAS/R1/A000005".
	LocationByPath(ctx context.Context, path string) (Location, error)
	// Children returns the direct children of a node; an empty parentID returns
	// the sites.
	Children(ctx context.Context, parentID string) ([]Location, error)
	// Ancestors returns the chain from the site down to, but excluding, id.
	// It is what [Location.Where] needs to resolve custodian and city.
	Ancestors(ctx context.Context, id string) ([]Location, error)
	// AllLocations returns every node ordered by path, for a picker.
	AllLocations(ctx context.Context) ([]Location, error)
}

// LocationWriter mutates the storage tree.
type LocationWriter interface {
	CreateLocation(ctx context.Context, l Location) error
	UpdateLocation(ctx context.Context, l Location) error
	// DeleteLocation removes a node. It must refuse a node that still has
	// children or still holds stock, because either would silently orphan the
	// answer to "where is this thing".
	DeleteLocation(ctx context.Context, id string) error
}

// LocationStore is both halves.
type LocationStore interface {
	LocationReader
	LocationWriter
}

// TaxonomyReader loads the operator's tree of what things ARE, and the questions
// each node asks (ADR-021).
//
// ⚠ Nothing here touches [Offer.Categories], which is the per-marketplace
// category and belongs to OfferWriter.SetCategory. The two are different things
// wearing one word.
type TaxonomyReader interface {
	// Category returns one node, or ErrNotFound.
	Category(ctx context.Context, id string) (Category, error)
	// CategoryChildren returns the direct children of a node; an empty parentID
	// returns the roots.
	CategoryChildren(ctx context.Context, parentID string) ([]Category, error)
	// CategoryAncestors returns the chain from the root down to, but excluding,
	// id — the walk field inheritance is resolved along.
	CategoryAncestors(ctx context.Context, id string) ([]Category, error)
	// AllCategories returns every node ordered by path, for a picker. Path order
	// is tree order, so the result renders as an indented tree unsorted.
	AllCategories(ctx context.Context) ([]Category, error)
	// OwnFields returns the fields defined ON a node, without its ancestors'.
	// It is what an editing screen shows as "this level's questions".
	OwnFields(ctx context.Context, categoryID string) ([]CategoryField, error)
	// ResolvedFields returns the fields a node asks INCLUDING every ancestor's,
	// ordered root first and by position within each level — the order a person
	// thinks in, general before specific.
	ResolvedFields(ctx context.Context, categoryID string) ([]CategoryField, error)
	// OfferFields returns ResolvedFields for the offer's category with this
	// offer's answers filled in. An offer with no category gets none.
	OfferFields(ctx context.Context, offerID, categoryID string) ([]OfferField, error)
	// CountFieldValues reports how many stored answers a field has, so deleting
	// it can say what it will take with it before it does.
	CountFieldValues(ctx context.Context, fieldID string) (int, error)
}

// TaxonomyWriter mutates the tree, its fields, and the answers given to them.
type TaxonomyWriter interface {
	CreateCategory(ctx context.Context, c Category) error
	UpdateCategory(ctx context.Context, c Category) error
	// DeleteCategory removes a node. It must refuse a node that still has
	// children, because the database's RESTRICT does not say which reference
	// held it.
	DeleteCategory(ctx context.Context, id string) error
	CreateField(ctx context.Context, f CategoryField) error
	UpdateField(ctx context.Context, f CategoryField) error
	// DeleteField removes a question AND, by cascade, every answer to it.
	DeleteField(ctx context.Context, id string) error
	// SetFieldValue stores one answer, keyed by FIELD ID so renaming the field
	// cannot orphan it. An empty value deletes the row rather than storing a
	// blank, so "never answered" and "answered with nothing" stay one state.
	SetFieldValue(ctx context.Context, offerID, fieldID, value string) error
}

// TaxonomyStore is both halves.
type TaxonomyStore interface {
	TaxonomyReader
	TaxonomyWriter
}

// UserReader loads accounts.
type UserReader interface {
	// User returns one account by id, or ErrNotFound.
	User(ctx context.Context, id string) (User, error)
	// UserByEmail resolves a sign-in identifier, or ErrNotFound.
	UserByEmail(ctx context.Context, email string) (User, error)
	// Users lists every account, oldest first.
	Users(ctx context.Context) ([]User, error)
	// CountUsers reports how many accounts exist. Zero is what opens the
	// one-time admin bootstrap, so this is a security-relevant read.
	CountUsers(ctx context.Context) (int, error)
}

// UserWriter mutates accounts.
type UserWriter interface {
	// CreateUser inserts an account. It must fail rather than overwrite when the
	// email is taken, so that the caller cannot silently reassign a login.
	CreateUser(ctx context.Context, u User) error
	UpdateUser(ctx context.Context, u User) error
	// CreateFirstAdmin inserts u as an administrator only if the users table is
	// still empty, in ONE transaction. Two requests arriving together must not
	// both become admin, and a count followed by an insert cannot promise that.
	CreateFirstAdmin(ctx context.Context, u User) error
}

// UserStore is both halves.
type UserStore interface {
	UserReader
	UserWriter
}

// RateReader loads stored exchange rates.
type RateReader interface {
	// RateOn returns the rate for quote on the given day, falling back to the
	// most recent earlier day. Markets close at weekends, so an exact-day lookup
	// would fail for every Saturday sale.
	RateOn(ctx context.Context, day, quote string) (Rate, error)
	// LatestRates returns the newest rate held for every quote currency.
	LatestRates(ctx context.Context) ([]Rate, error)
}

// RateWriter stores exchange rates.
type RateWriter interface {
	// SaveRates upserts a day's rates.
	SaveRates(ctx context.Context, rates []Rate) error
}

// RateStore is both halves.
type RateStore interface {
	RateReader
	RateWriter
}

// CartReader loads saved batches.
type CartReader interface {
	// Cart returns one cart with its items in order, or ErrNotFound.
	Cart(ctx context.Context, id string) (Cart, error)
	// Carts lists every cart, most recently touched first.
	Carts(ctx context.Context) ([]Cart, error)
	// Contents returns a cart with its offers resolved, in cart order, each with
	// its photos — everything an export or a cart page needs, in one call.
	Contents(ctx context.Context, cartID string) (CartContents, error)
	// CartsHolding returns the carts an offer is already in, so the offer page
	// can say so rather than letting the operator add it to a second batch by
	// accident.
	CartsHolding(ctx context.Context, offerID string) ([]Cart, error)
}

// CartWriter mutates saved batches.
type CartWriter interface {
	CreateCart(ctx context.Context, c Cart) error
	UpdateCart(ctx context.Context, c Cart) error
	DeleteCart(ctx context.Context, id string) error
	// AddToCart appends an offer. Adding one that is already present must be a
	// no-op rather than an error: the button that calls it is visible on a page
	// that may be a few seconds stale, and failing there would be noise about
	// nothing.
	AddToCart(ctx context.Context, cartID, offerID string) error
	// RemoveFromCart removes an offer, and is likewise a no-op when it is not
	// there.
	RemoveFromCart(ctx context.Context, cartID, offerID string) error
	// ReorderCart rewrites item positions to the given order.
	ReorderCart(ctx context.Context, cartID string, offerIDsInOrder []string) error
}

// CartStore is both halves.
type CartStore interface {
	CartReader
	CartWriter
}

// SubmissionReader loads staff proposals.
type SubmissionReader interface {
	// Submission returns one proposal with its photos, or ErrNotFound.
	Submission(ctx context.Context, id string) (Submission, error)
	// Submissions lists proposals matching a filter, newest first.
	Submissions(ctx context.Context, f SubmissionFilter) ([]Submission, error)
	// CountOpen returns how many proposals still need a decision. It is what the
	// live inbox banner shows, so it is a single cheap count rather than a list
	// the caller has to measure.
	CountOpen(ctx context.Context) (int, error)
}

// SubmissionWriter mutates staff proposals.
type SubmissionWriter interface {
	CreateSubmission(ctx context.Context, s Submission) error
	UpdateSubmission(ctx context.Context, s Submission) error
	AddSubmissionPhoto(ctx context.Context, p Photo) error
	DeleteSubmissionPhoto(ctx context.Context, photoID string) error
	// MovePhotosToOffer re-parents a submission's photographs onto an offer,
	// KEEPING each photo's id. The id is the public URL, so minting new ones
	// would break any link already shared and orphan the blobs on disk.
	MovePhotosToOffer(ctx context.Context, submissionID, offerID string) error
}

// SubmissionStore is both halves.
type SubmissionStore interface {
	SubmissionReader
	SubmissionWriter
}

// SubmissionFilter narrows an inbox listing. A zero filter means "everything".
type SubmissionFilter struct {
	// Status, when non-empty, restricts to these triage states.
	Status []SubmissionStatus
	// SubmittedBy restricts to one submitter, which is how a staff user sees
	// their own proposals and only their own.
	SubmittedBy string
	// OpenOnly restricts to proposals still awaiting a decision.
	OpenOnly bool
	// Limit and Offset page the result.
	Limit  int
	Offset int
}

// Sequencer hands out monotonic numbers.
//
// It is a port rather than a helper because allocation must be ATOMIC: two
// concurrent intakes asking for a reference at the same moment must not receive
// the same one, and "read the highest, add one" cannot promise that. The
// implementation does it in a single statement.
type Sequencer interface {
	// NextSequence allocates and returns the next value of a named counter.
	//
	// A number is never handed out twice, INCLUDING after the row that used it is
	// deleted: a reference that has been written on a physical label is spent
	// whether or not the record survives.
	NextSequence(ctx context.Context, name string) (int64, error)
}

// SettingsReader loads the deployment values an administrator can change.
type SettingsReader interface {
	// Settings returns the stored values. A fresh installation has none, and
	// that is a zero value rather than an error — the caller falls back to its
	// configured default.
	Settings(ctx context.Context) (Settings, error)
}

// SettingsWriter stores them.
type SettingsWriter interface {
	SaveSettings(ctx context.Context, s Settings) error
}

// SettingsStore is both halves.
type SettingsStore interface {
	SettingsReader
	SettingsWriter
}

// BlobStore holds photo bytes. It is separate from the database because image
// blobs and row data have different backup and serving needs.
type BlobStore interface {
	// Put stores the bytes for a photo id.
	Put(ctx context.Context, id string, data []byte) error
	// Open returns the stored bytes for a photo id.
	Open(ctx context.Context, id string) ([]byte, error)
	// Delete removes the bytes. It must be a no-op when they are already gone,
	// so that retrying a partly-failed offer deletion converges.
	Delete(ctx context.Context, id string) error
}
