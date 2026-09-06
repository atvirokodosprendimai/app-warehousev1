package core

import (
	"fmt"
	"strings"
	"time"
)

// Kind is a level in the storage hierarchy.
//
// The levels are ordered but NOT mandatory: a tiny warehouse is one Site holding
// Bins directly, and the same tree later grows Buildings, Rooms and Shelves
// between them without moving a single item. That is the whole reason storage is
// a tree rather than the flat "A000005" code the operator actually types — the
// flat code stays as the bin's own name, and the tree is what makes it mean
// something once there is more than one place.
type Kind string

const (
	// KindSite is a whole physical place, possibly in another city and possibly
	// held by someone who is not the operator. It is the root of a tree.
	KindSite Kind = "site"
	// KindBuilding is one building on a site.
	KindBuilding Kind = "building"
	// KindRoom is a room within a building.
	KindRoom Kind = "room"
	// KindAisle is a run of shelving within a room.
	KindAisle Kind = "aisle"
	// KindShelf is one shelf in an aisle.
	KindShelf Kind = "shelf"
	// KindSegment is a division of a shelf.
	KindSegment Kind = "segment"
	// KindBin is the smallest addressable place — where a thing actually is.
	KindBin Kind = "bin"
)

// Kinds lists the levels from largest to smallest.
func Kinds() []Kind {
	return []Kind{KindSite, KindBuilding, KindRoom, KindAisle, KindShelf, KindSegment, KindBin}
}

// Depth returns k's rank, 0 for a site. It is used to refuse a tree that nests
// a building inside a shelf.
func (k Kind) Depth() int {
	for i, x := range Kinds() {
		if x == k {
			return i
		}
	}
	return -1
}

// Valid reports whether k is a level this application defines.
func (k Kind) Valid() bool { return k.Depth() >= 0 }

// Label returns a human-readable name for the level.
func (k Kind) Label() string {
	if !k.Valid() {
		return string(k)
	}
	return strings.ToUpper(string(k)[:1]) + string(k)[1:]
}

// ParseKind validates a kind coming off a form.
func ParseKind(s string) (Kind, error) {
	k := Kind(strings.ToLower(strings.TrimSpace(s)))
	if !k.Valid() {
		return "", fmt.Errorf("%w: unknown location kind %q", ErrInvalid, s)
	}
	return k, nil
}

// Location is one node in the storage tree.
//
// Custodian, City and Country are set on whichever node they first become true
// of — normally a Site — and are INHERITED by everything beneath. Resolving them
// is [Location.Where]'s job, not the caller's, so a bin deep in a friend's
// garage in another city answers "who has this, and where" without every
// descendant carrying a copy that can drift.
type Location struct {
	// ID is the immutable internal identifier (a UUID).
	ID string
	// ParentID is the enclosing node, empty for a site.
	ParentID string
	// Kind is this node's level.
	Kind Kind
	// Code is the operator's own name for this node, unique among its siblings.
	// For a bin this is the label physically written on it, e.g. "A000005".
	Code string
	// Path is the materialised full address, slash-separated from the site down,
	// e.g. "KAUNAS-GARAGE/R1/S3/A000005". Unique across the whole tree, so it can
	// be pasted into a search box and resolve to exactly one place.
	Path string
	// Label is an optional human description, e.g. "under the window".
	Label string
	// Custodian is who physically holds this place, when that is not the
	// operator — a person or a company. Empty means "inherit from the parent".
	Custodian string
	// CustodianContact is a phone number or email for the custodian. Storage in
	// someone else's building is worthless if nobody can reach them to collect.
	CustodianContact string
	// City and Country locate the place. Empty means "inherit from the parent".
	City    string
	Country string
	// Notes is free text, e.g. access instructions.
	Notes string
	// CreatedAt is a UTC timestamp.
	CreatedAt time.Time
}

// Validate checks the rules that hold for any node in the tree.
func (l *Location) Validate(parent *Location) error {
	if !l.Kind.Valid() {
		return fmt.Errorf("%w: unknown location kind %q", ErrInvalid, l.Kind)
	}
	if strings.TrimSpace(l.Code) == "" {
		return fmt.Errorf("%w: a location needs a code", ErrInvalid)
	}
	if strings.ContainsAny(l.Code, "/") {
		return fmt.Errorf("%w: a location code cannot contain %q, because the path is "+
			"slash-separated and the code would split it", ErrInvalid, "/")
	}
	if parent == nil {
		if l.Kind != KindSite {
			return fmt.Errorf("%w: a %s must sit inside something; only a site has no parent",
				ErrInvalid, l.Kind)
		}
		if strings.TrimSpace(l.City) == "" {
			return fmt.Errorf("%w: a site needs a city — a site in another town is the case "+
				"this field exists for", ErrInvalid)
		}
		return nil
	}
	if l.Kind == KindSite {
		return fmt.Errorf("%w: a site is a root and cannot sit inside %s", ErrInvalid, parent.Path)
	}
	// Strictly deeper, but not necessarily by one: skipping levels is normal in a
	// small warehouse and forbidding it would force empty placeholder rows.
	if l.Kind.Depth() <= parent.Kind.Depth() {
		return fmt.Errorf("%w: a %s cannot sit inside a %s", ErrInvalid, l.Kind, parent.Kind)
	}
	return nil
}

// ChildPath returns the materialised path a child with the given code would have.
func (l *Location) ChildPath(code string) string {
	if l == nil || l.Path == "" {
		return code
	}
	return l.Path + "/" + code
}

// Where resolves the inherited custodian and place for this node, given its
// ancestors ordered from the site down to (but excluding) the node itself.
//
// The nearest setting wins, so a single room lent by a different person inside a
// site you otherwise control can override just that room.
func (l *Location) Where(ancestors []Location) Placement {
	p := Placement{
		Custodian:        l.Custodian,
		CustodianContact: l.CustodianContact,
		City:             l.City,
		Country:          l.Country,
	}
	// Walk outward from the nearest ancestor, filling only what is still blank.
	for i := len(ancestors) - 1; i >= 0; i-- {
		a := ancestors[i]
		if p.Custodian == "" {
			p.Custodian = a.Custodian
			// Contact travels with the custodian it belongs to; taking a nearer
			// custodian's name with a further one's phone number would produce a
			// contact that reaches the wrong person.
			if p.Custodian != "" {
				p.CustodianContact = a.CustodianContact
			}
		}
		if p.City == "" {
			p.City = a.City
		}
		if p.Country == "" {
			p.Country = a.Country
		}
	}
	return p
}

// Placement is the resolved answer to "where does this live, and who has it".
type Placement struct {
	// Custodian is who holds the item, empty when it is the operator themselves.
	Custodian string
	// CustodianContact reaches the custodian.
	CustodianContact string
	// City and Country say where.
	City    string
	Country string
}

// Offsite reports whether the item is held by somebody else.
func (p Placement) Offsite() bool { return strings.TrimSpace(p.Custodian) != "" }

// Summary renders the placement for a dashboard cell, e.g. "Kaunas · held by Jonas".
func (p Placement) Summary() string {
	parts := make([]string, 0, 2)
	if p.City != "" {
		parts = append(parts, p.City)
	}
	if p.Offsite() {
		parts = append(parts, "held by "+p.Custodian)
	}
	return strings.Join(parts, " · ")
}
