package offer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
)

// newService wires a Service over a real SQLite repository and an in-memory
// blob store, which is the pair every service test needs.
func newService(t *testing.T) (*Service, *Repo, *fakeBlobs) {
	t.Helper()
	r := newTestRepo(t)
	b := newFakeBlobs()
	return NewService(r, b, &fakeSeq{}), r, b
}

// fakeSeq is a counter with no database behind it.
//
// The service's job is to ASK for a number and handle a collision; whether two
// concurrent asks can receive the same number is the allocator's job, and is
// tested against a real database in internal/sequence.
type fakeSeq struct {
	mu sync.Mutex
	n  int64
	// err, when set, is returned instead of a number.
	err error
	// from, when set, is where counting starts — for driving a collision with a
	// reference a test has already inserted by hand.
	from int64
}

func (f *fakeSeq) NextSequence(context.Context, string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return 0, f.err
	}
	if f.n == 0 && f.from != 0 {
		f.n = f.from - 1
	}
	f.n++
	return f.n, nil
}

// generatedSKU is the documented shape: WH followed by seven digits.
var generatedSKU = regexp.MustCompile(`^WH[0-9]{7}$`)

func TestCreateNeedsOnlyATitleAndLeavesTheOfferUnpriced(t *testing.T) {
	s, r, _ := newService(t)
	ctx := context.Background()

	got, err := s.Create(ctx, "  Vintage Lamp  ", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.Title != "Vintage Lamp" {
		t.Errorf("Title = %q, want the trimmed title", got.Title)
	}
	if got.Status != core.StatusDraft {
		t.Errorf("Status = %q, want draft", got.Status)
	}
	if got.Quantity != 1 {
		t.Errorf("Quantity = %d, want 1", got.Quantity)
	}
	if got.Shop != (core.Money{}) || got.Owner != (core.Money{}) || got.Sold != (core.Money{}) {
		t.Errorf("a fresh draft carries a price: shop=%+v owner=%+v sold=%+v",
			got.Shop, got.Owner, got.Sold)
	}
	if !generatedSKU.MatchString(got.SKU) {
		t.Errorf("generated SKU = %q, want the WH0000001 scheme — it is written on a "+
			"label by hand and typed back into a search box", got.SKU)
	}
	if got.ID == "" {
		t.Error("ID is empty")
	}

	stored, err := r.Offer(ctx, got.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if stored.SKU != got.SKU || stored.Title != got.Title {
		t.Errorf("stored = %+v, want the returned offer", stored)
	}
	if !stored.NeedsPricing() {
		t.Error("a fresh draft should be on the pricing queue")
	}
}

func TestCreateKeepsAnOperatorSuppliedSKU(t *testing.T) {
	s, _, _ := newService(t)

	got, err := s.Create(context.Background(), "Chair", " CHAIR-7 ")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got.SKU != "CHAIR-7" {
		t.Errorf("SKU = %q, want CHAIR-7", got.SKU)
	}
}

// TestCreateAcceptsAnUnnamedPhotographGroup is ADR-019, and it REPLACES
// TestCreateRefusesAnEmptyTitleAndWritesNothing, which asserted the opposite.
//
// The retired rule was that Create refused an empty title. ADR-019 moved that
// requirement to the publication boundary so one person can photograph stock and
// a second person name it afterwards — which makes an unnamed draft the FIRST
// state of every item arriving that way, rather than an error.
//
// ⚠ The retired test carried a SECOND property in its name — "and writes
// nothing" — that had nothing to do with the title: a create that fails must
// leave no row. Create can no longer fail validation at all (every field it sets
// is valid by construction), so that property is re-asserted below against a
// taken SKU, which is the failure that remains reachable. Deleting it with the
// title rule would have quietly dropped a check nobody was replacing.
func TestCreateAcceptsAnUnnamedPhotographGroup(t *testing.T) {
	s, r, _ := newService(t)
	ctx := context.Background()

	got, err := s.Create(ctx, "   ", "")
	if err != nil {
		t.Fatalf("Create with no title = %v, want an unnamed draft (ADR-019)", err)
	}
	if got.Title != "" {
		t.Errorf("Title = %q, want empty — a title of spaces is not a title, and the "+
			"describing queue trims before it compares", got.Title)
	}
	if !got.NeedsDescribing() {
		t.Error("an unnamed draft must land in the describing queue, or the person who " +
			"names things has no way to find the work")
	}
	// ADR-010: the reference is what gets written on the box, so it has to exist
	// before anybody names the item. Allocating it here rather than at describe
	// time is what lets the photographer label the box as they go.
	if got.SKU == "" {
		t.Error("no reference was allocated, so there is nothing to write on the box")
	}
	if n := countRows(t, r, "offers"); n != 1 {
		t.Errorf("offers = %d, want 1", n)
	}

	if _, err := s.Create(ctx, "Two", got.SKU); !errors.Is(err, ErrSKUTaken) {
		t.Fatalf("Create with a taken SKU = %v, want ErrSKUTaken", err)
	}
	if n := countRows(t, r, "offers"); n != 1 {
		t.Errorf("offers = %d after a refused create, want 1: a create that fails must "+
			"write nothing", n)
	}
}

func TestCreateSurfacesATakenSKU(t *testing.T) {
	s, _, _ := newService(t)
	ctx := context.Background()

	if _, err := s.Create(ctx, "One", "SKU-1"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	_, err := s.Create(ctx, "Two", "SKU-1")
	if !errors.Is(err, ErrSKUTaken) {
		t.Errorf("Create with a taken SKU = %v, want ErrSKUTaken", err)
	}
}

func TestUpdateValidatesBeforeWritingAndStampsUpdatedAt(t *testing.T) {
	s, r, _ := newService(t)
	ctx := context.Background()

	o, err := s.Create(ctx, "Before", "SKU-1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	// ⚠ The invalid field here USED to be the title, until ADR-019 made an
	// untitled draft legal. It is the SKU now, which is still refused — the
	// subject of this test is that Update validates BEFORE writing, so it needs
	// any invalid input, not that particular one. The title is changed at the same
	// time so the reload below can prove the rejected update reached nothing.
	bad := o
	bad.SKU = ""
	bad.Title = "Changed"
	if err := s.Update(ctx, bad); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("Update with no SKU = %v, want core.ErrInvalid", err)
	}
	unchanged, err := r.Offer(ctx, o.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if unchanged.Title != "Before" {
		t.Errorf("Title = %q, want Before: a rejected update must not reach the database",
			unchanged.Title)
	}

	ok := o
	ok.Title = "After"
	ok.UpdatedAt = time.Time{} // the service stamps this itself
	if err := s.Update(ctx, ok); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, err := r.Offer(ctx, o.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Title != "After" {
		t.Errorf("Title = %q, want After", got.Title)
	}
	if got.UpdatedAt.IsZero() {
		t.Error("UpdatedAt is zero; the service must stamp it")
	}
}

func TestSetPricesRecordsBothFigures(t *testing.T) {
	s, r, _ := newService(t)
	ctx := context.Background()

	o, err := s.Create(ctx, "Vintage Lamp", "SKU-1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	shop := core.Money{Minor: 4999, Currency: "EUR"}
	owner := core.Money{Minor: 3000, Currency: "EUR"}
	if err := s.SetPrices(ctx, o.ID, shop, owner); err != nil {
		t.Fatalf("SetPrices: %v", err)
	}

	got, err := r.Offer(ctx, o.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Shop != shop {
		t.Errorf("Shop = %+v, want %+v", got.Shop, shop)
	}
	if got.Owner != owner {
		t.Errorf("Owner = %+v, want %+v", got.Owner, owner)
	}
	if got.NeedsPricing() {
		t.Error("a priced draft is still on the pricing queue")
	}
	margin, err := got.Margin()
	if err != nil {
		t.Fatalf("Margin: %v", err)
	}
	if margin != (core.Money{Minor: 1999, Currency: "EUR"}) {
		t.Errorf("Margin = %+v, want 1999 EUR", margin)
	}
}

func TestSetPricesRefusesAnUnknownCurrency(t *testing.T) {
	s, r, _ := newService(t)
	ctx := context.Background()

	o, err := s.Create(ctx, "Vintage Lamp", "SKU-1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	err = s.SetPrices(ctx, o.ID, core.Money{Minor: 100, Currency: "XYZ"}, core.Money{})
	if !errors.Is(err, core.ErrBadMoney) {
		t.Fatalf("SetPrices with an unknown currency = %v, want core.ErrBadMoney", err)
	}
	got, err := r.Offer(ctx, o.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Shop != (core.Money{}) {
		t.Errorf("Shop = %+v, want the zero Money", got.Shop)
	}
}

func TestSetPricesOnAMissingOfferIsNotFound(t *testing.T) {
	s, _, _ := newService(t)

	err := s.SetPrices(context.Background(), "missing",
		core.Money{Minor: 1, Currency: "EUR"}, core.Money{})
	if !errors.Is(err, core.ErrNotFound) {
		t.Errorf("SetPrices on a missing offer = %v, want core.ErrNotFound", err)
	}
}

func TestMarkSoldMovesStatusPriceAndDateTogether(t *testing.T) {
	s, r, _ := newService(t)
	ctx := context.Background()

	o, err := s.Create(ctx, "Vintage Lamp", "SKU-1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := s.SetPrices(ctx, o.ID,
		core.Money{Minor: 4999, Currency: "EUR"},
		core.Money{Minor: 3000, Currency: "EUR"}); err != nil {
		t.Fatalf("SetPrices: %v", err)
	}

	when := time.Date(2026, 9, 1, 14, 30, 0, 0, time.UTC)
	price := core.Money{Minor: 4500, Currency: "EUR"}
	if err := s.MarkSold(ctx, o.ID, price, when); err != nil {
		t.Fatalf("MarkSold: %v", err)
	}

	got, err := r.Offer(ctx, o.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Status != core.StatusSold {
		t.Errorf("Status = %q, want sold", got.Status)
	}
	if got.Sold != price {
		t.Errorf("Sold = %+v, want %+v", got.Sold, price)
	}
	if got.SoldAt == nil || !got.SoldAt.Equal(when) {
		t.Fatalf("SoldAt = %v, want %v", got.SoldAt, when)
	}
	realised, err := got.Realised()
	if err != nil {
		t.Fatalf("Realised: %v", err)
	}
	if realised != (core.Money{Minor: 1500, Currency: "EUR"}) {
		t.Errorf("Realised = %+v, want 1500 EUR", realised)
	}
}

func TestMarkSoldWithNoDateLeavesTheOfferUnchanged(t *testing.T) {
	s, r, _ := newService(t)
	ctx := context.Background()

	o, err := s.Create(ctx, "Vintage Lamp", "SKU-1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := s.SetPrices(ctx, o.ID, core.Money{Minor: 4999, Currency: "EUR"}, core.Money{}); err != nil {
		t.Fatalf("SetPrices: %v", err)
	}
	before, err := r.Offer(ctx, o.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}

	err = s.MarkSold(ctx, o.ID, core.Money{Minor: 4500, Currency: "EUR"}, time.Time{})
	if !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("MarkSold with no date = %v, want core.ErrInvalid", err)
	}

	after, err := r.Offer(ctx, o.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if after.Status != before.Status {
		t.Errorf("Status = %q, want %q unchanged", after.Status, before.Status)
	}
	if after.Sold != (core.Money{}) {
		t.Errorf("Sold = %+v, want the zero Money: a rejected sale writes nothing", after.Sold)
	}
	if after.SoldAt != nil {
		t.Errorf("SoldAt = %v, want nil", after.SoldAt)
	}
	if !after.UpdatedAt.Equal(before.UpdatedAt) {
		t.Errorf("UpdatedAt moved from %v to %v on a rejected sale",
			before.UpdatedAt, after.UpdatedAt)
	}
}

func TestSetStatusRefusesAnExportedStatusWithoutAShopPrice(t *testing.T) {
	s, r, _ := newService(t)
	ctx := context.Background()

	o, err := s.Create(ctx, "Vintage Lamp", "SKU-1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	for _, st := range []core.Status{core.StatusListed, core.StatusPending} {
		if err := s.SetStatus(ctx, o.ID, st); !errors.Is(err, ErrNoShopPrice) {
			t.Errorf("SetStatus(%q) on an unpriced draft = %v, want ErrNoShopPrice", st, err)
		}
	}
	got, err := r.Offer(ctx, o.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Status != core.StatusDraft {
		t.Errorf("Status = %q, want draft", got.Status)
	}
}

func TestSetStatusAllowsListingOnceThePriceIsResearched(t *testing.T) {
	s, r, _ := newService(t)
	ctx := context.Background()

	o, err := s.Create(ctx, "Vintage Lamp", "SKU-1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := s.SetPrices(ctx, o.ID, core.Money{Minor: 4999, Currency: "EUR"}, core.Money{}); err != nil {
		t.Fatalf("SetPrices: %v", err)
	}
	if err := s.SetStatus(ctx, o.ID, core.StatusListed); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	got, err := r.Offer(ctx, o.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got.Status != core.StatusListed {
		t.Errorf("Status = %q, want listed", got.Status)
	}
	if !got.Status.Exportable() {
		t.Error("a listed offer should be exportable")
	}
}

func TestSetStatusToSoldWithoutADateIsRefused(t *testing.T) {
	s, _, _ := newService(t)
	ctx := context.Background()

	o, err := s.Create(ctx, "Vintage Lamp", "SKU-1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// A sale is MarkSold's job precisely because status alone cannot carry a date.
	if err := s.SetStatus(ctx, o.ID, core.StatusSold); !errors.Is(err, core.ErrInvalid) {
		t.Errorf("SetStatus(sold) = %v, want core.ErrInvalid", err)
	}
}

func TestAddPhotoWritesTheBlobBeforeTheRow(t *testing.T) {
	s, r, blobs := newService(t)
	ctx := context.Background()

	o, err := s.Create(ctx, "Vintage Lamp", "SKU-1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	putFailed := errors.New("disk full")
	blobs.putErr = putFailed

	_, err = s.AddPhoto(ctx, o.ID, "front.jpg", "image/jpeg", []byte{1, 2, 3})
	if !errors.Is(err, putFailed) {
		t.Fatalf("AddPhoto = %v, want the blob store's error", err)
	}
	if n := countRows(t, r, "offer_photos"); n != 0 {
		t.Errorf("offer_photos = %d, want 0: a row whose blob failed would publish a public "+
			"URL that 404s on a marketplace", n)
	}
	if blobs.count() != 0 {
		t.Errorf("blobs = %d, want 0", blobs.count())
	}
}

func TestAddPhotoStoresBytesHashAndPosition(t *testing.T) {
	s, r, blobs := newService(t)
	ctx := context.Background()

	o, err := s.Create(ctx, "Vintage Lamp", "SKU-1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	data := []byte("jpeg-bytes")
	first, err := s.AddPhoto(ctx, o.ID, "front.jpg", "image/jpeg", data)
	if err != nil {
		t.Fatalf("AddPhoto: %v", err)
	}
	second, err := s.AddPhoto(ctx, o.ID, "back.png", "image/png", []byte("png-bytes"))
	if err != nil {
		t.Fatalf("AddPhoto: %v", err)
	}

	if first.Position != 0 || second.Position != 1 {
		t.Errorf("positions = %d, %d; want 0, 1 appended in order",
			first.Position, second.Position)
	}
	sum := sha256.Sum256(data)
	if first.SHA256 != hex.EncodeToString(sum[:]) {
		t.Errorf("SHA256 = %q, want the sha256 of the bytes", first.SHA256)
	}
	if first.ByteSize != int64(len(data)) {
		t.Errorf("ByteSize = %d, want %d", first.ByteSize, len(data))
	}
	if first.ID == second.ID || first.ID == "" {
		t.Errorf("photo ids are not distinct: %q and %q", first.ID, second.ID)
	}
	if first.PublicPath() != "/p/"+first.ID+".jpg" {
		t.Errorf("PublicPath = %q", first.PublicPath())
	}
	if !blobs.has(first.ID) || !blobs.has(second.ID) {
		t.Error("the blob store is missing bytes for a stored photo")
	}

	got, err := r.Offer(ctx, o.ID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if len(got.Photos) != 2 || got.Photos[0].ID != first.ID || got.Photos[1].ID != second.ID {
		t.Errorf("reloaded photos = %+v, want %s then %s", got.Photos, first.ID, second.ID)
	}
}

func TestAddPhotoAppendsAfterAGapLeftByADeletion(t *testing.T) {
	s, _, _ := newService(t)
	ctx := context.Background()

	o, err := s.Create(ctx, "Vintage Lamp", "SKU-1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	first, err := s.AddPhoto(ctx, o.ID, "a.jpg", "image/jpeg", []byte("a"))
	if err != nil {
		t.Fatalf("AddPhoto: %v", err)
	}
	if _, err := s.AddPhoto(ctx, o.ID, "b.jpg", "image/jpeg", []byte("b")); err != nil {
		t.Fatalf("AddPhoto: %v", err)
	}
	// Removing the primary leaves position 1 occupied; the next photo must not
	// reuse a taken slot.
	if err := s.RemovePhoto(ctx, first.ID); err != nil {
		t.Fatalf("RemovePhoto: %v", err)
	}
	third, err := s.AddPhoto(ctx, o.ID, "c.jpg", "image/jpeg", []byte("c"))
	if err != nil {
		t.Fatalf("AddPhoto: %v", err)
	}
	if third.Position != 2 {
		t.Errorf("Position = %d, want 2 (highest existing plus one)", third.Position)
	}
}

func TestAddPhotoRejectsBadInput(t *testing.T) {
	s, r, blobs := newService(t)
	ctx := context.Background()

	o, err := s.Create(ctx, "Vintage Lamp", "SKU-1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	cases := map[string]struct {
		contentType string
		data        []byte
	}{
		"pdf":          {"application/pdf", []byte("x")},
		"svg":          {"image/svg+xml", []byte("x")},
		"empty type":   {"", []byte("x")},
		"empty body":   {"image/jpeg", nil},
		"zero-length":  {"image/jpeg", []byte{}},
		"html dressed": {"text/html", []byte("x")},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := s.AddPhoto(ctx, o.ID, "f", tc.contentType, tc.data)
			if !errors.Is(err, core.ErrInvalid) {
				t.Fatalf("AddPhoto = %v, want core.ErrInvalid", err)
			}
		})
	}
	if n := countRows(t, r, "offer_photos"); n != 0 {
		t.Errorf("offer_photos = %d, want 0", n)
	}
	if blobs.count() != 0 {
		t.Errorf("blobs = %d, want 0", blobs.count())
	}
}

func TestAddPhotoAcceptsEveryMarketplaceImageType(t *testing.T) {
	s, _, _ := newService(t)
	ctx := context.Background()

	o, err := s.Create(ctx, "Vintage Lamp", "SKU-1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	for _, ct := range []string{"image/jpeg", "image/png", "image/webp", "IMAGE/GIF"} {
		if _, err := s.AddPhoto(ctx, o.ID, "f", ct, []byte("x")); err != nil {
			t.Errorf("AddPhoto(%q) = %v, want success", ct, err)
		}
	}
}

func TestAddPhotoOnAMissingOfferWritesNoBlob(t *testing.T) {
	s, _, blobs := newService(t)

	_, err := s.AddPhoto(context.Background(), "missing", "f.jpg", "image/jpeg", []byte("x"))
	if !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("AddPhoto on a missing offer = %v, want core.ErrNotFound", err)
	}
	if blobs.count() != 0 {
		t.Errorf("blobs = %d, want 0", blobs.count())
	}
}

func TestRemovePhotoDeletesTheRowAndThenTheBytes(t *testing.T) {
	s, r, blobs := newService(t)
	ctx := context.Background()

	o, err := s.Create(ctx, "Vintage Lamp", "SKU-1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	p, err := s.AddPhoto(ctx, o.ID, "a.jpg", "image/jpeg", []byte("a"))
	if err != nil {
		t.Fatalf("AddPhoto: %v", err)
	}

	if err := s.RemovePhoto(ctx, p.ID); err != nil {
		t.Fatalf("RemovePhoto: %v", err)
	}
	if n := countRows(t, r, "offer_photos"); n != 0 {
		t.Errorf("offer_photos = %d, want 0", n)
	}
	if blobs.has(p.ID) {
		t.Error("the blob survived the removal")
	}
	if err := s.RemovePhoto(ctx, p.ID); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("RemovePhoto on a missing row = %v, want core.ErrNotFound", err)
	}
}

func TestRemovePhotoSucceedsWhenTheBlobIsAlreadyGone(t *testing.T) {
	s, r, blobs := newService(t)
	ctx := context.Background()

	o, err := s.Create(ctx, "Vintage Lamp", "SKU-1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	p, err := s.AddPhoto(ctx, o.ID, "a.jpg", "image/jpeg", []byte("a"))
	if err != nil {
		t.Fatalf("AddPhoto: %v", err)
	}

	// The row is authoritative: once it is gone the offer is correct, so a blob
	// store that cannot delete must not make the caller retry a done deletion.
	blobs.delErr = errors.New("blob store unreachable")
	if err := s.RemovePhoto(ctx, p.ID); err != nil {
		t.Fatalf("RemovePhoto = %v, want nil despite the blob failure", err)
	}
	if n := countRows(t, r, "offer_photos"); n != 0 {
		t.Errorf("offer_photos = %d, want 0", n)
	}
}

func TestDeleteRemovesTheOfferItsPhotoRowsAndItsBlobs(t *testing.T) {
	s, r, blobs := newService(t)
	ctx := context.Background()

	o, err := s.Create(ctx, "Vintage Lamp", "SKU-1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	p1, err := s.AddPhoto(ctx, o.ID, "a.jpg", "image/jpeg", []byte("a"))
	if err != nil {
		t.Fatalf("AddPhoto: %v", err)
	}
	p2, err := s.AddPhoto(ctx, o.ID, "b.jpg", "image/jpeg", []byte("b"))
	if err != nil {
		t.Fatalf("AddPhoto: %v", err)
	}

	if err := s.Delete(ctx, o.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := r.Offer(ctx, o.ID); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("Offer after Delete = %v, want core.ErrNotFound", err)
	}
	if n := countRows(t, r, "offer_photos"); n != 0 {
		t.Errorf("offer_photos = %d, want 0", n)
	}
	if blobs.has(p1.ID) || blobs.has(p2.ID) {
		t.Error("photo bytes survived the offer deletion")
	}
	if err := s.Delete(ctx, o.ID); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("Delete on a missing offer = %v, want core.ErrNotFound", err)
	}
}

// TestSetCategoryRefusesAnUnknownProfile stops a typo minting a row nothing
// will ever read.
//
// The failure it prevents is silent by construction: an operator sets a
// category against "ebey", sees no error, and finds the value ignored for ever
// — the same shape as a photo origin nobody could set, which is the reported
// bug ADR-016 exists to answer.
func TestSetCategoryRefusesAnUnknownProfile(t *testing.T) {
	svc, _, _ := newService(t)
	ctx := context.Background()

	o, err := svc.Create(ctx, "Brass desk lamp", "")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	err = svc.SetCategory(ctx, o.ID, "ebey", "11450")
	if !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("SetCategory with a typo'd profile = %v, want core.ErrInvalid", err)
	}

	// The real profiles are accepted, or the guard is refusing everything.
	for _, p := range core.KnownExportProfiles() {
		if err := svc.SetCategory(ctx, o.ID, p, "11450"); err != nil {
			t.Errorf("SetCategory(%q) = %v, want nil", p, err)
		}
	}

	// And an unknown offer is ErrNotFound rather than a driver foreign-key error.
	if err := svc.SetCategory(ctx, "no-such-offer", "ebay", "11450"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("SetCategory on a missing offer = %v, want core.ErrNotFound", err)
	}
}
