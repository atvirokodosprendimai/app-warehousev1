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
	// Query is a case-insensitive substring match over SKU, title and description.
	Query string
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
