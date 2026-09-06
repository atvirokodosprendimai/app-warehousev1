package offer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
)

// ErrNoShopPrice reports an attempt to move an offer into a status that is
// exported to a marketplace while it has no shop price.
//
// It is the guard that stops the research step being skipped. Without it a draft
// goes straight to listed and reaches Shopify and eBay at 0.00, which is a real
// offer to a real buyer.
var ErrNoShopPrice = errors.New("offer: a shop price is required before listing")

// allowedPhotoTypes are the image types a marketplace fetcher will actually
// render. Anything else would be published as a broken image, so it is refused
// at intake rather than discovered by a customer.
var allowedPhotoTypes = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/webp": true,
	"image/gif":  true,
}

// Service is the offer aggregate's write side: the only place an offer changes.
//
// It holds a core.OfferStore because it both reads and writes — every mutation
// here is a read-modify-validate-write over a whole offer, so the domain rules
// in core.Offer.Validate see the complete state rather than one changed field.
type Service struct {
	store core.OfferStore
	blobs core.BlobStore
}

// NewService returns a Service writing offers through store and photo bytes
// through blobs.
func NewService(store core.OfferStore, blobs core.BlobStore) *Service {
	return &Service{store: store, blobs: blobs}
}

// Create starts an offer from the intake flow: photograph, title, shelve — and
// price later, once research says what the item can fetch.
//
// Only a title is required. The offer is a draft with quantity 1 and no price of
// any kind. Demanding a price here would push the operator to type a
// placeholder, and a placeholder that later reaches a listed status ships to a
// marketplace as a real price.
//
// When sku is empty one is generated as "WH-<YYYYMMDD>-<8 uppercase hex>": the
// prefix marks it as ours, the intake date makes a drawer of printed labels sort
// by the day the items arrived, and the eight hex digits come from a fresh UUID
// so two operators taking stock in at once cannot collide. It is uppercase and
// hyphenated because it is read off a label and typed back in by hand.
func (s *Service) Create(ctx context.Context, title, sku string) (core.Offer, error) {
	sku = strings.TrimSpace(sku)
	t := now()
	if sku == "" {
		sku = generateSKU(t)
	}
	o := core.Offer{
		ID:        uuid.NewString(),
		SKU:       sku,
		Title:     strings.TrimSpace(title),
		Status:    core.StatusDraft,
		Quantity:  1,
		CreatedAt: t,
		UpdatedAt: t,
	}
	if err := o.Validate(); err != nil {
		return core.Offer{}, err
	}
	if err := s.store.CreateOffer(ctx, o); err != nil {
		return core.Offer{}, err
	}
	return o, nil
}

// Update validates o and persists it, stamping UpdatedAt.
//
// CreatedAt is ignored by the write, so a caller editing a form-shaped struct
// cannot accidentally rewrite when the item was taken in.
func (s *Service) Update(ctx context.Context, o core.Offer) error {
	o.UpdatedAt = now()
	if err := o.Validate(); err != nil {
		return err
	}
	return s.store.UpdateOffer(ctx, o)
}

// SetPrices records the research step's two answers together: shop, the price we
// publish and the only one an export may emit, and owner, what the item's owner
// wants to receive, which never leaves this system.
func (s *Service) SetPrices(ctx context.Context, id string, shop, owner core.Money) error {
	o, err := s.store.Offer(ctx, id)
	if err != nil {
		return err
	}
	o.Shop = shop
	o.Owner = owner
	o.UpdatedAt = now()
	if err := o.Validate(); err != nil {
		return err
	}
	return s.store.UpdateOffer(ctx, o)
}

// MarkSold records a completed sale: status, sold price and sale date move as
// one unit.
//
// They are one call deliberately. A sold offer with no date cannot be attributed
// to a reporting period, and both core.Offer.Validate and the schema's CHECK
// refuse that pair — so setting them one at a time would either fail halfway and
// leave the offer wrong, or need the row to pass through a state the database
// will not store.
func (s *Service) MarkSold(ctx context.Context, id string, price core.Money, when time.Time) error {
	if when.IsZero() {
		return fmt.Errorf("%w: a sale needs its date, or the revenue cannot be attributed "+
			"to a period", core.ErrInvalid)
	}
	o, err := s.store.Offer(ctx, id)
	if err != nil {
		return err
	}
	sold := when.UTC().Truncate(time.Second)
	o.Status = core.StatusSold
	o.Sold = price
	o.SoldAt = &sold
	o.UpdatedAt = now()
	if err := o.Validate(); err != nil {
		return err
	}
	return s.store.UpdateOffer(ctx, o)
}

// SetStatus moves an offer through its lifecycle.
//
// It refuses a move to an exported status while there is no shop price, with
// [ErrNoShopPrice]. Every other rule is core.Offer.Validate's, including the one
// that a sold offer needs its date — which is why a sale is recorded through
// [Service.MarkSold] rather than here.
func (s *Service) SetStatus(ctx context.Context, id string, st core.Status) error {
	o, err := s.store.Offer(ctx, id)
	if err != nil {
		return err
	}
	if st.Exportable() && o.Shop.IsZero() {
		return fmt.Errorf("offer %s cannot become %s: %w", id, st, ErrNoShopPrice)
	}
	o.Status = st
	o.UpdatedAt = now()
	if err := o.Validate(); err != nil {
		return err
	}
	return s.store.UpdateOffer(ctx, o)
}

// AddPhoto stores an image for an offer and appends it after the ones already
// there.
//
// ⚠ THE BLOB IS WRITTEN BEFORE THE ROW, and the order is the whole point. The
// photo's id is also its public URL segment, and that URL is handed to Shopify
// and eBay, which fetch the image from their own servers. A row written first
// whose blob write then failed would publish a URL that 404s on a live
// marketplace listing. A blob written first whose row write fails is an
// unreferenced file that nothing serves and nobody sees.
func (s *Service) AddPhoto(ctx context.Context, offerID, filename, contentType string, data []byte) (core.Photo, error) {
	contentType = strings.ToLower(strings.TrimSpace(contentType))
	if !allowedPhotoTypes[contentType] {
		return core.Photo{}, fmt.Errorf("%w: %q is not an image type a marketplace will "+
			"fetch", core.ErrInvalid, contentType)
	}
	if len(data) == 0 {
		return core.Photo{}, fmt.Errorf("%w: empty image", core.ErrInvalid)
	}

	o, err := s.store.Offer(ctx, offerID)
	if err != nil {
		return core.Photo{}, err
	}

	sum := sha256.Sum256(data)
	p := core.Photo{
		ID:          uuid.NewString(),
		OfferID:     o.ID,
		Position:    nextPosition(o.Photos),
		Filename:    filename,
		ContentType: contentType,
		ByteSize:    int64(len(data)),
		SHA256:      hex.EncodeToString(sum[:]),
		CreatedAt:   now(),
	}
	if err := s.blobs.Put(ctx, p.ID, data); err != nil {
		return core.Photo{}, fmt.Errorf("offer: store photo bytes: %w", err)
	}
	if err := s.store.AddPhoto(ctx, p); err != nil {
		return core.Photo{}, fmt.Errorf("offer: store photo row: %w", err)
	}
	return p, nil
}

// RemovePhoto deletes a photo: ROW FIRST, bytes second.
//
// It is the mirror of [Service.AddPhoto] and for the same reason. Once the row
// is gone nothing references the bytes, so a leftover blob is unreferenced
// garbage; a row pointing at deleted bytes is a broken image on a live listing.
//
// A blob that has already gone is not a failure. The port promises Delete is a
// no-op in that case, and returning an error would make the caller retry a
// deletion that has already succeeded.
func (s *Service) RemovePhoto(ctx context.Context, photoID string) error {
	if err := s.store.DeletePhoto(ctx, photoID); err != nil {
		return err
	}
	_ = s.blobs.Delete(ctx, photoID)
	return nil
}

// Delete removes an offer and then its photo bytes.
//
// The photo rows go with the offer through the schema's ON DELETE CASCADE. The
// bytes live outside the database and cannot join its transaction, so they are
// removed afterwards, best effort: a blob left behind by a failure here is
// unreferenced and harmless, and re-running converges because deleting a
// missing blob is a no-op.
func (s *Service) Delete(ctx context.Context, id string) error {
	o, err := s.store.Offer(ctx, id)
	if err != nil {
		return err
	}
	if err := s.store.DeleteOffer(ctx, id); err != nil {
		return err
	}
	for _, p := range o.Photos {
		_ = s.blobs.Delete(ctx, p.ID)
	}
	return nil
}

// nextPosition returns the first free display position after the photos an offer
// already has.
//
// It is the highest position plus one rather than the count, because a deletion
// leaves a gap and renumbering on delete would move the primary image.
func nextPosition(photos []core.Photo) int {
	next := 0
	for _, p := range photos {
		if p.Position >= next {
			next = p.Position + 1
		}
	}
	return next
}

// generateSKU builds the fallback SKU described on [Service.Create].
func generateSKU(t time.Time) string {
	return fmt.Sprintf("WH-%s-%s", t.UTC().Format("20060102"),
		strings.ToUpper(uuid.NewString()[:8]))
}

// now is the write side's clock, truncated to the second because that is the
// resolution the RFC3339 columns store. Without the truncation the struct a
// caller is handed back would not equal the one the next read returns.
func now() time.Time { return time.Now().UTC().Truncate(time.Second) }
