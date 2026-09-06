package cart

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
)

// Service is the cart aggregate's write side: the only place a saved batch
// changes.
//
// It holds a core.CartStore because it both reads and writes — a rename is a
// read-modify-validate-write over the whole cart, so core.Cart.Validate sees the
// complete state rather than the one field that moved.
type Service struct {
	store core.CartStore
}

// NewService returns a Service writing carts through store.
func NewService(store core.CartStore) *Service {
	return &Service{store: store}
}

// Create starts a new saved batch under a name the operator chose.
//
// The name is required and the note is not: a batch that cannot be told from the
// others in the list is unusable, whereas what it is for is usually decided
// after the first item has gone in.
func (s *Service) Create(ctx context.Context, name, note string) (core.Cart, error) {
	t := now()
	c := core.Cart{
		ID:        uuid.NewString(),
		Name:      strings.TrimSpace(name),
		Note:      strings.TrimSpace(note),
		CreatedAt: t,
		UpdatedAt: t,
	}
	if err := c.Validate(); err != nil {
		return core.Cart{}, err
	}
	if err := s.store.CreateCart(ctx, c); err != nil {
		return core.Cart{}, err
	}
	return c, nil
}

// Rename changes a cart's name and note, refusing a blank name.
//
// The two move together because they are edited on one form: sending them
// separately would let saving the name clear the note the operator had already
// written.
func (s *Service) Rename(ctx context.Context, id, name, note string) error {
	c, err := s.store.Cart(ctx, id)
	if err != nil {
		return err
	}
	c.Name = strings.TrimSpace(name)
	c.Note = strings.TrimSpace(note)
	c.UpdatedAt = now()
	if err := c.Validate(); err != nil {
		return err
	}
	return s.store.UpdateCart(ctx, c)
}

// Add puts an offer in a cart, at the end. Adding one the cart already holds is
// a no-op, so a click on a page that has gone stale is not an error the operator
// has to read and dismiss.
func (s *Service) Add(ctx context.Context, cartID, offerID string) error {
	return s.store.AddToCart(ctx, cartID, offerID)
}

// Remove takes an offer out of a cart, and is likewise a no-op when it is not
// there.
func (s *Service) Remove(ctx context.Context, cartID, offerID string) error {
	return s.store.RemoveFromCart(ctx, cartID, offerID)
}

// Reorder rewrites the batch's order. offerIDsInOrder must be exactly the cart's
// current membership; anything else is [ErrNotCartMembership] rather than a
// partial reorder that would lose an item's place.
func (s *Service) Reorder(ctx context.Context, cartID string, offerIDsInOrder []string) error {
	return s.store.ReorderCart(ctx, cartID, offerIDsInOrder)
}

// Delete discards a saved batch. The offers in it are untouched: a cart is a
// selection, and throwing the selection away is not a decision about stock.
func (s *Service) Delete(ctx context.Context, id string) error {
	return s.store.DeleteCart(ctx, id)
}

// ExportSet resolves a cart and splits it into what a marketplace export will
// publish and what it will not.
//
// ⚠ BOTH halves are returned, and the caller is expected to show the second one
// rather than discard it. A filtered listing may quietly omit whatever does not
// qualify, because the operator never named its members one by one — but a
// cart's members were each chosen by hand, so dropping one without saying so
// loses an item the operator believes they are sending. core.HoldReason gives
// the sentence to print beside each held offer.
func (s *Service) ExportSet(ctx context.Context, cartID string) (send []core.Offer, held []core.Offer, err error) {
	c, err := s.store.Contents(ctx, cartID)
	if err != nil {
		return nil, nil, err
	}
	send, held = c.Exportable()
	return send, held, nil
}
