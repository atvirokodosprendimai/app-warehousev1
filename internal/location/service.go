package location

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
	"github.com/google/uuid"
)

// Errors a caller is expected to match on with [errors.Is]. Each names a
// refusal an operator can act on, which is why they are distinguishable rather
// than one "conflict".
var (
	// ErrCodeTaken reports that a sibling already uses this code. Codes only have
	// to be unique among siblings, so the fix is a different code here, not
	// anywhere else in the tree.
	//
	// At the ROOT this refusal arrives as ErrPathTaken instead: SQL treats every
	// NULL parent as distinct, so a site's code is guarded by the path index. A
	// handler highlighting the code field should match both.
	ErrCodeTaken = errors.New("location: code already used under this parent")

	// ErrPathTaken reports that the resulting materialised address is already in
	// use. A path resolves to exactly one place, so two nodes cannot share one.
	ErrPathTaken = errors.New("location: path already in use")

	// ErrHasChildren reports a delete refused because the node still encloses
	// other locations. Removing it would leave them with no way up to a site.
	ErrHasChildren = errors.New("location: still has child locations")

	// ErrLocationOccupied reports a delete refused because stock still sits here.
	// Emptying a shelf has to be a deliberate act of moving what is on it, never
	// a side effect of tidying the tree.
	ErrLocationOccupied = errors.New("location: still holds stock")

	// ErrCycle reports a move that would put a node inside its own subtree,
	// leaving a ring of locations with no site above them.
	ErrCycle = errors.New("location: a location cannot be moved into itself")
)

// Store is what a [Service] writes through.
//
// It is the domain port plus the one operation a materialised-path tree cannot
// express through it: re-addressing a node and its whole subtree atomically.
// [core.LocationWriter] can only update one row at a time, and a rename is not
// one row.
type Store interface {
	core.LocationStore

	// MoveSubtree re-addresses l — which already carries its new ParentID, Code
	// and Path — together with every node beneath oldPath, in one transaction.
	MoveSubtree(ctx context.Context, l core.Location, oldPath string) error
}

// Service is the write side of the storage tree. It owns the rules that keep a
// node's id, its code and its materialised path telling the same story.
type Service struct {
	store Store
}

// NewService returns a Service writing through store.
func NewService(store Store) *Service {
	return &Service{store: store}
}

// Create inserts l beneath parentID and returns it as stored, with its id, path
// and creation time filled in.
//
// An empty parentID creates a site — a whole physical place, which is the one
// kind that has no parent and the one that must say which city it is in.
func (s *Service) Create(ctx context.Context, parentID string, l core.Location) (core.Location, error) {
	parent, err := s.parent(ctx, parentID)
	if err != nil {
		return core.Location{}, fmt.Errorf("location: create under %q: %w", parentID, err)
	}
	l.ParentID = parentID
	if err := l.Validate(parent); err != nil {
		return core.Location{}, fmt.Errorf("location: create %q: %w", l.Code, err)
	}
	l.ID = uuid.NewString()
	// A site's path is its bare code; deeper nodes hang their code off the
	// parent's already-materialised path, so the address is composed once here
	// and never recomputed by walking the tree.
	l.Path = parent.ChildPath(l.Code)
	l.CreatedAt = time.Now().UTC()
	if err := s.store.CreateLocation(ctx, l); err != nil {
		return core.Location{}, err
	}
	return l, nil
}

// Rename changes a node's own code and re-addresses everything beneath it.
//
// The node keeps its id, so nothing that points at it — stock above all — is
// affected. Only the human address changes.
func (s *Service) Rename(ctx context.Context, id, newCode string) error {
	node, err := s.store.Location(ctx, id)
	if err != nil {
		return fmt.Errorf("location: rename %s: %w", id, err)
	}
	parent, err := s.parent(ctx, node.ParentID)
	if err != nil {
		return fmt.Errorf("location: rename %s: %w", id, err)
	}
	oldPath := node.Path
	node.Code = newCode
	if err := node.Validate(parent); err != nil {
		return fmt.Errorf("location: rename %q: %w", oldPath, err)
	}
	node.Path = parent.ChildPath(newCode)
	return s.store.MoveSubtree(ctx, node, oldPath)
}

// Move re-parents a node, taking its whole subtree with it.
//
// An empty newParentID promotes the node to a site, which the domain allows
// only for a node that is already of that kind.
func (s *Service) Move(ctx context.Context, id, newParentID string) error {
	node, err := s.store.Location(ctx, id)
	if err != nil {
		return fmt.Errorf("location: move %s: %w", id, err)
	}
	newParent, err := s.parent(ctx, newParentID)
	if err != nil {
		return fmt.Errorf("location: move %s: %w", id, err)
	}

	// Refuse a move into the node's own subtree. The result would be a ring with
	// no site at its top: unreachable from any root, and impossible to address,
	// since every path in it would have to contain itself. The materialised path
	// answers the question without walking anything — a descendant's path is the
	// node's path followed by a separator — and the separator is what keeps the
	// unrelated sibling "AB" out of "A"'s subtree.
	if newParent != nil &&
		(newParent.ID == node.ID || strings.HasPrefix(newParent.Path, node.Path+"/")) {
		return fmt.Errorf("%w: %q is inside %q", ErrCycle, newParent.Path, node.Path)
	}

	oldPath := node.Path
	node.ParentID = newParentID
	// Only the moved node's own kind is re-checked against its new parent: the
	// kinds inside the subtree keep their relative order wherever the subtree
	// hangs, so a valid subtree stays valid.
	if err := node.Validate(newParent); err != nil {
		return fmt.Errorf("location: move %q: %w", oldPath, err)
	}
	node.Path = newParent.ChildPath(node.Code)
	return s.store.MoveSubtree(ctx, node, oldPath)
}

// Delete removes an empty node.
//
// It refuses a node that still encloses other locations ([ErrHasChildren]) and
// a node that still holds stock ([ErrLocationOccupied]). The order matters: the
// database refuses both with the same foreign-key error, so ruling out children
// first is what makes the surviving refusal mean "stock".
func (s *Service) Delete(ctx context.Context, id string) error {
	// Children("") means "the sites", so an empty id would report every site as
	// this node's children and refuse for the wrong reason.
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("%w: delete needs a location id", core.ErrInvalid)
	}
	children, err := s.store.Children(ctx, id)
	if err != nil {
		return fmt.Errorf("location: delete %s: %w", id, err)
	}
	if len(children) > 0 {
		return fmt.Errorf("%w: %s encloses %d location(s)", ErrHasChildren, id, len(children))
	}
	if err := s.store.DeleteLocation(ctx, id); err != nil {
		if restrictedByReference(err) {
			return fmt.Errorf("%w: %s: %w", ErrLocationOccupied, id, err)
		}
		return err
	}
	return nil
}

// Place answers "who has this, and where is it" for one node, resolving the
// custodian, contact, city and country it inherits from the site above it.
func (s *Service) Place(ctx context.Context, id string) (core.Placement, error) {
	node, err := s.store.Location(ctx, id)
	if err != nil {
		return core.Placement{}, fmt.Errorf("location: place %s: %w", id, err)
	}
	ancestors, err := s.store.Ancestors(ctx, id)
	if err != nil {
		return core.Placement{}, fmt.Errorf("location: place %s: %w", id, err)
	}
	return node.Where(ancestors), nil
}

// UpdateDetails changes what a place IS without changing where it is.
//
// ⚠ IT DELIBERATELY IGNORES code, parent and path on the supplied value. Those
// three are the node's ADDRESS, and changing an address means rewriting every
// descendant's materialised path in the same transaction — which is what
// [Service.Rename] and [Service.Move] exist to do. A details update that also
// wrote a new code would set the path of this node and leave its children
// pointing at the old one, and nothing would report it: the tree would simply
// start answering "where is this" with a path that resolves to nothing.
//
// City and country matter more than they look: everything beneath inherits them,
// so correcting a site's city corrects every shelf inside it at once.
func (s *Service) UpdateDetails(ctx context.Context, id string, d core.Location) error {
	node, err := s.store.Location(ctx, id)
	if err != nil {
		return fmt.Errorf("location: update %s: %w", id, err)
	}

	node.Label = strings.TrimSpace(d.Label)
	node.Custodian = strings.TrimSpace(d.Custodian)
	node.CustodianContact = strings.TrimSpace(d.CustodianContact)
	node.City = strings.TrimSpace(d.City)
	node.Country = strings.TrimSpace(d.Country)
	node.Notes = strings.TrimSpace(d.Notes)

	// Validate against the node's real parent, not a supplied one, so the kind
	// and city rules are checked against where it actually sits.
	var parent *core.Location
	if node.ParentID != "" {
		p, err := s.store.Location(ctx, node.ParentID)
		if err != nil {
			return fmt.Errorf("location: update %s: %w", id, err)
		}
		parent = &p
	}
	if err := node.Validate(parent); err != nil {
		return err
	}
	return s.store.UpdateLocation(ctx, node)
}

// parent loads the enclosing node, or nil when parentID is empty. A nil parent
// is what the domain reads as "this is a site", and [core.Location.ChildPath]
// and [core.Location.Validate] both accept it.
func (s *Service) parent(ctx context.Context, parentID string) (*core.Location, error) {
	if parentID == "" {
		return nil, nil
	}
	p, err := s.store.Location(ctx, parentID)
	if err != nil {
		return nil, err
	}
	return &p, nil
}
