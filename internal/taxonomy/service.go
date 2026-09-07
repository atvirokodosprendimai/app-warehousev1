package taxonomy

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
	"github.com/google/uuid"
)

// Errors a caller is expected to match on with [errors.Is]. Each names a refusal
// an operator can act on, which is why they are distinguishable rather than one
// "conflict".
var (
	// ErrCodeTaken reports that a sibling category already uses this code. Codes
	// only have to be unique among siblings, so the fix is a different code here,
	// not anywhere else in the tree.
	//
	// ⚠ At the ROOT this refusal arrives as ErrPathTaken instead: SQL treats every
	// NULL parent as distinct, so a root's code is guarded by the path index. A
	// handler highlighting the code field should match both.
	ErrCodeTaken = errors.New("taxonomy: code already used under this parent")

	// ErrPathTaken reports that the resulting materialised address is already in
	// use. A path resolves to exactly one node, so two cannot share one.
	ErrPathTaken = errors.New("taxonomy: path already in use")

	// ErrFieldCodeTaken reports that this category already asks a question under
	// that code.
	ErrFieldCodeTaken = errors.New("taxonomy: field code already used on this category")

	// ErrHasChildren reports a delete refused because the node still has
	// children. Removing it would leave them with no way up to a root.
	ErrHasChildren = errors.New("taxonomy: still has child categories")

	// ErrCategoryInUse reports a delete the database refused for a reason that is
	// not children. Offers filed here are set to no category rather than blocking
	// the delete, so in practice this means something new points at the tree.
	ErrCategoryInUse = errors.New("taxonomy: something still references this category")

	// ErrCycle reports a move that would put a node inside its own subtree,
	// leaving a ring of categories with no root above them.
	ErrCycle = errors.New("taxonomy: a category cannot be moved into itself")
)

// Store is what a [Service] writes through.
//
// It is the domain port plus the one operation a materialised-path tree cannot
// express through it: re-addressing a node and its whole subtree atomically.
// [core.TaxonomyWriter] can only update one row at a time, and a rename is not
// one row.
type Store interface {
	core.TaxonomyStore

	// MoveSubtree re-addresses c — which already carries its new ParentID, Code
	// and Path — together with every node beneath oldPath, in one transaction.
	MoveSubtree(ctx context.Context, c core.Category, oldPath string) error
}

// Service is the write side of the taxonomy. It owns the rules that keep a
// node's id, its code and its materialised path telling the same story.
type Service struct {
	store Store
}

// NewService returns a Service writing through store.
func NewService(store Store) *Service {
	return &Service{store: store}
}

// Tree returns every node ordered by path, which is tree order.
func (s *Service) Tree(ctx context.Context) ([]core.Category, error) {
	return s.store.AllCategories(ctx)
}

// Create inserts a node beneath parentID and returns it as stored, with its id
// and path filled in. An empty parentID creates a root.
func (s *Service) Create(ctx context.Context, parentID string, c core.Category) (core.Category, error) {
	parent, err := s.parent(ctx, parentID)
	if err != nil {
		return core.Category{}, fmt.Errorf("taxonomy: create under %q: %w", parentID, err)
	}
	c.Code = strings.ToUpper(strings.TrimSpace(c.Code))
	c.ParentID = parentID
	if err := c.Validate(); err != nil {
		return core.Category{}, fmt.Errorf("taxonomy: create %q: %w", c.Code, err)
	}
	c.ID = uuid.NewString()
	// A root's path is its bare code; a deeper node hangs its code off the
	// parent's already-materialised path, so an address is composed once here and
	// never recomputed by walking the tree.
	c.Path = parent.ChildPath(c.Code)
	if err := s.store.CreateCategory(ctx, c); err != nil {
		return core.Category{}, err
	}
	return c, nil
}

// Rename changes a node's own code and re-addresses everything beneath it.
//
// The node keeps its id, so nothing that points at it — offers above all — is
// affected. Only the human address changes.
func (s *Service) Rename(ctx context.Context, id, newCode string) error {
	c, err := s.store.Category(ctx, id)
	if err != nil {
		return err
	}
	code := strings.ToUpper(strings.TrimSpace(newCode))
	if code == c.Code {
		return nil
	}
	old := c
	c.Code = code
	if err := c.Validate(); err != nil {
		return fmt.Errorf("taxonomy: rename %s: %w", id, err)
	}
	parent, err := s.parent(ctx, c.ParentID)
	if err != nil {
		return fmt.Errorf("taxonomy: rename %s: %w", id, err)
	}
	c.Path = parent.ChildPath(c.Code)
	return s.store.MoveSubtree(ctx, c, old.Path)
}

// SetName changes what a human reads, which is not part of the address and so
// touches exactly one row.
func (s *Service) SetName(ctx context.Context, id, name string) error {
	c, err := s.store.Category(ctx, id)
	if err != nil {
		return err
	}
	c.Name = strings.TrimSpace(name)
	if err := c.Validate(); err != nil {
		return fmt.Errorf("taxonomy: rename %s: %w", id, err)
	}
	return s.store.UpdateCategory(ctx, c)
}

// Move re-parents a node and re-addresses its whole subtree.
//
// An empty newParentID makes it a root.
func (s *Service) Move(ctx context.Context, id, newParentID string) error {
	if id == newParentID {
		return fmt.Errorf("taxonomy: move %s: %w", id, ErrCycle)
	}
	c, err := s.store.Category(ctx, id)
	if err != nil {
		return err
	}
	parent, err := s.parent(ctx, newParentID)
	if err != nil {
		return fmt.Errorf("taxonomy: move %s: %w", id, err)
	}
	// A node cannot move into its own subtree. The materialised path is what
	// answers this without a walk: every descendant's path starts with this
	// node's path plus a separator, and nothing else does.
	if parent != nil && (parent.Path == c.Path || strings.HasPrefix(parent.Path, c.Path+"/")) {
		return fmt.Errorf("taxonomy: move %s: %w", id, ErrCycle)
	}
	old := c
	c.ParentID = newParentID
	c.Path = parent.ChildPath(c.Code)
	if c.Path == old.Path {
		return nil
	}
	return s.store.MoveSubtree(ctx, c, old.Path)
}

// Delete removes a node that has no children.
//
// Children are ruled out FIRST so that the error an operator sees names the
// thing they have to deal with. The database's own refusal is a bare FOREIGN KEY
// message that does not say which reference held the row.
func (s *Service) Delete(ctx context.Context, id string) error {
	kids, err := s.store.CategoryChildren(ctx, id)
	if err != nil {
		return err
	}
	if len(kids) > 0 {
		return fmt.Errorf("taxonomy: delete %s: %w", id, ErrHasChildren)
	}
	if err := s.store.DeleteCategory(ctx, id); err != nil {
		if restrictedByReference(err) {
			return fmt.Errorf("taxonomy: delete %s: %w", id, ErrCategoryInUse)
		}
		return err
	}
	return nil
}

// AddField attaches a question to a node and returns it as stored.
func (s *Service) AddField(ctx context.Context, categoryID string, f core.CategoryField) (core.CategoryField, error) {
	if _, err := s.store.Category(ctx, categoryID); err != nil {
		return core.CategoryField{}, err
	}
	f.CategoryID = categoryID
	f.Code = strings.ToLower(strings.TrimSpace(f.Code))
	f.Label = strings.TrimSpace(f.Label)
	if err := f.Validate(); err != nil {
		return core.CategoryField{}, fmt.Errorf("taxonomy: add field %q: %w", f.Code, err)
	}
	f.ID = uuid.NewString()
	if err := s.store.CreateField(ctx, f); err != nil {
		return core.CategoryField{}, err
	}
	return f, nil
}

// UpdateField rewrites a question, keeping its id so the answers already given
// against it stay readable.
func (s *Service) UpdateField(ctx context.Context, f core.CategoryField) error {
	f.Code = strings.ToLower(strings.TrimSpace(f.Code))
	f.Label = strings.TrimSpace(f.Label)
	if err := f.Validate(); err != nil {
		return fmt.Errorf("taxonomy: update field %s: %w", f.ID, err)
	}
	return s.store.UpdateField(ctx, f)
}

// DeleteField removes a question and every answer to it, reporting how many
// answers went with it.
//
// The count is read BEFORE the delete for the obvious reason, and returned
// rather than logged because the caller is the only thing that can tell the
// operator.
func (s *Service) DeleteField(ctx context.Context, id string) (int, error) {
	n, err := s.store.CountFieldValues(ctx, id)
	if err != nil {
		return 0, err
	}
	if err := s.store.DeleteField(ctx, id); err != nil {
		return 0, err
	}
	return n, nil
}

// Answer stores one offer's answer to one question.
func (s *Service) Answer(ctx context.Context, offerID, fieldID, value string) error {
	return s.store.SetFieldValue(ctx, offerID, fieldID, strings.TrimSpace(value))
}

// parent loads the enclosing node, or nil for a root.
func (s *Service) parent(ctx context.Context, parentID string) (*core.Category, error) {
	if parentID == "" {
		return nil, nil
	}
	p, err := s.store.Category(ctx, parentID)
	if err != nil {
		return nil, err
	}
	return &p, nil
}
