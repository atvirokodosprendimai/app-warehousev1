package cart

import (
	"context"
	"errors"
	"testing"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
)

// TestCreateAssignsAnIdAndStampsTimestamps checks the service supplies what the
// caller does not: an identifier and the two times.
func TestCreateAssignsAnIdAndStampsTimestamps(t *testing.T) {
	r := newTestRepo(t)
	s := NewService(r)
	ctx := context.Background()

	got, err := s.Create(ctx, "  eBay batch — September  ", "  for Rita  ")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if got.ID == "" {
		t.Fatal("create returned a cart with no id")
	}
	if got.Name != "eBay batch — September" {
		t.Errorf("name = %q, want it trimmed to %q", got.Name, "eBay batch — September")
	}
	if got.Note != "for Rita" {
		t.Errorf("note = %q, want %q", got.Note, "for Rita")
	}
	if got.CreatedAt.IsZero() || !got.CreatedAt.Equal(got.UpdatedAt) {
		t.Errorf("timestamps = %s / %s, want both set and equal at creation",
			got.CreatedAt, got.UpdatedAt)
	}

	back, err := r.Cart(ctx, got.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if back.Name != got.Name || !back.CreatedAt.Equal(got.CreatedAt) {
		t.Errorf("stored cart = %+v, want it to match the returned %+v", back, got)
	}
}

// TestCreateRefusesABlankName checks the rule core.Cart.Validate states: a batch
// that cannot be told from the others in the list is not usable.
func TestCreateRefusesABlankName(t *testing.T) {
	r := newTestRepo(t)
	s := NewService(r)

	_, err := s.Create(context.Background(), "   ", "note")
	if !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("error = %v, want core.ErrInvalid", err)
	}
	if n := countRows(t, r, "carts"); n != 0 {
		t.Errorf("carts rows = %d, want 0 — a rejected create must write nothing", n)
	}
}

// TestRenameChangesNameAndNoteTogether checks the pair moves as one, and that a
// blank name is refused without disturbing what is stored.
func TestRenameChangesNameAndNoteTogether(t *testing.T) {
	r := newTestRepo(t)
	s := NewService(r)
	ctx := context.Background()

	c, err := s.Create(ctx, "September", "first pass")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := s.Rename(ctx, c.ID, "  October  ", "  second pass  "); err != nil {
		t.Fatalf("rename: %v", err)
	}
	got, err := r.Cart(ctx, c.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.Name != "October" || got.Note != "second pass" {
		t.Fatalf("name/note = %q/%q, want %q/%q", got.Name, got.Note, "October", "second pass")
	}

	if err := s.Rename(ctx, c.ID, "  ", "anything"); !errors.Is(err, core.ErrInvalid) {
		t.Fatalf("blank rename = %v, want core.ErrInvalid", err)
	}
	got, err = r.Cart(ctx, c.ID)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.Name != "October" || got.Note != "second pass" {
		t.Errorf("name/note = %q/%q after a refused rename, want them unchanged at %q/%q",
			got.Name, got.Note, "October", "second pass")
	}
}

// TestRenameOfAnUnknownCartIsNotFound checks the read that precedes the write is
// the one that reports a missing cart.
func TestRenameOfAnUnknownCartIsNotFound(t *testing.T) {
	s := NewService(newTestRepo(t))
	if err := s.Rename(context.Background(), "nope", "New", ""); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("error = %v, want core.ErrNotFound", err)
	}
}

// TestServiceWritesReachTheStore walks add, reorder, remove and delete through
// the service and checks each landed, so a method that quietly dropped its call
// would fail here.
func TestServiceWritesReachTheStore(t *testing.T) {
	r := newTestRepo(t)
	s := NewService(r)
	ctx := context.Background()
	seedThreeOffers(t, r)

	c, err := s.Create(ctx, "batch", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	for _, id := range []string{"off-a", "off-b", "off-c"} {
		if err := s.Add(ctx, c.ID, id); err != nil {
			t.Fatalf("add %s: %v", id, err)
		}
	}

	if err := s.Reorder(ctx, c.ID, []string{"off-c", "off-a", "off-b"}); err != nil {
		t.Fatalf("reorder: %v", err)
	}
	contents, err := r.Contents(ctx, c.ID)
	if err != nil {
		t.Fatalf("contents: %v", err)
	}
	want := []string{"off-c", "off-a", "off-b"}
	if ids := offerIDs(contents.Offers); !equalStrings(ids, want) {
		t.Fatalf("order = %v, want %v", ids, want)
	}

	if err := s.Reorder(ctx, c.ID, []string{"off-c", "off-a"}); !errors.Is(err, ErrNotCartMembership) {
		t.Errorf("short reorder = %v, want ErrNotCartMembership", err)
	}

	if err := s.Remove(ctx, c.ID, "off-a"); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if n := countRows(t, r, "cart_items"); n != 2 {
		t.Errorf("cart_items rows = %d, want 2", n)
	}

	if err := s.Delete(ctx, c.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := r.Cart(ctx, c.ID); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("cart after delete = %v, want core.ErrNotFound", err)
	}
}

// TestExportSetHoldsEachUnexportableOfferWithItsOwnReason checks the split and
// every reason core.HoldReason can give.
//
// It runs against [stubStore] rather than the database because two of the four
// held cases cannot be STORED: the offers table's CHECK forbids a listed offer
// with no price, and since ADR-019 a trigger forbids a listed offer with no
// title. Those are exactly the rows "no shop price yet" and "no title yet"
// describe, and a gate you cannot reach through a supported write path is still
// worth asserting — it is the one that catches a row which arrived some other
// way. Each held offer here fails for one reason only, so the assertion names
// which.
func TestExportSetHoldsEachUnexportableOfferWithItsOwnReason(t *testing.T) {
	photo := func(offerID string) []core.Photo {
		return []core.Photo{{ID: "p-" + offerID, OfferID: offerID, ContentType: "image/jpeg"}}
	}
	priced := core.Money{Minor: 1200, Currency: "EUR"}

	store := stubStore{contents: core.CartContents{
		Cart: core.Cart{ID: "cart-1", Name: "batch"},
		Offers: []core.Offer{
			// Held for its status alone: named, priced and photographed.
			{ID: "off-draft", Title: "Brass lamp", Status: core.StatusDraft, Shop: priced, Photos: photo("off-draft")},
			// Exportable and photographed, but never priced.
			{ID: "off-unpriced", Title: "Oak chair", Status: core.StatusListed, Photos: photo("off-unpriced")},
			// Ready except that a marketplace would show no image.
			{ID: "off-nophoto", Title: "Enamel sign", Status: core.StatusListed, Shop: priced},
			// Photographed and priced, and nobody has named it yet (ADR-019) — a
			// marketplace would receive a listing headed by an empty string.
			{ID: "off-untitled", Status: core.StatusListed, Shop: priced, Photos: photo("off-untitled")},
			// The one that goes.
			{ID: "off-good", Title: "Vintage desk lamp", Status: core.StatusListed, Shop: priced, Photos: photo("off-good")},
		},
	}}

	send, held, err := NewService(store).ExportSet(context.Background(), "cart-1")
	if err != nil {
		t.Fatalf("export set: %v", err)
	}

	if ids := offerIDs(send); !equalStrings(ids, []string{"off-good"}) {
		t.Fatalf("send = %v, want [off-good]", ids)
	}
	wantHeld := []string{"off-draft", "off-unpriced", "off-nophoto", "off-untitled"}
	if ids := offerIDs(held); !equalStrings(ids, wantHeld) {
		t.Fatalf("held = %v, want %v", ids, wantHeld)
	}

	wantReasons := map[string]string{
		"off-draft":    "status is Draft",
		"off-unpriced": "no shop price yet",
		"off-nophoto":  "no photographs",
		"off-untitled": "no title yet",
	}
	for _, o := range held {
		if got := core.HoldReason(o); got != wantReasons[o.ID] {
			t.Errorf("hold reason for %s = %q, want %q", o.ID, got, wantReasons[o.ID])
		}
	}
	for _, o := range send {
		if got := core.HoldReason(o); got != "" {
			t.Errorf("hold reason for a sent offer %s = %q, want empty", o.ID, got)
		}
	}
}

// TestExportSetOverTheRepoReturnsBothHalvesInCartOrder is the same contract end
// to end.
//
// The held half is returned rather than dropped because every member of a cart
// was chosen by hand: an export that silently omitted one would lose an item the
// operator believes they are sending.
func TestExportSetOverTheRepoReturnsBothHalvesInCartOrder(t *testing.T) {
	r := newTestRepo(t)
	s := NewService(r)
	ctx := context.Background()

	insertOffer(t, r, newListed("off-good", "SKU-good", "Ready", 1200, baseTime))
	insertPhoto(t, r, "p-good", "off-good", 0)

	draft := newDraft("off-draft", "SKU-draft", "Not finished", baseTime)
	draft.Shop = core.Money{Minor: 900, Currency: "EUR"}
	insertOffer(t, r, draft)
	insertPhoto(t, r, "p-draft", "off-draft", 0)

	insertOffer(t, r, newListed("off-nophoto", "SKU-nophoto", "No pictures", 1500, baseTime))

	c, err := s.Create(ctx, "batch", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	for _, id := range []string{"off-draft", "off-good", "off-nophoto"} {
		if err := s.Add(ctx, c.ID, id); err != nil {
			t.Fatalf("add %s: %v", id, err)
		}
	}

	send, held, err := s.ExportSet(ctx, c.ID)
	if err != nil {
		t.Fatalf("export set: %v", err)
	}
	if ids := offerIDs(send); !equalStrings(ids, []string{"off-good"}) {
		t.Fatalf("send = %v, want [off-good]", ids)
	}
	if ids := offerIDs(held); !equalStrings(ids, []string{"off-draft", "off-nophoto"}) {
		t.Fatalf("held = %v, want [off-draft off-nophoto] in cart order", ids)
	}
	if got := core.HoldReason(held[0]); got != "status is Draft" {
		t.Errorf("hold reason for off-draft = %q, want %q", got, "status is Draft")
	}
	if got := core.HoldReason(held[1]); got != "no photographs" {
		t.Errorf("hold reason for off-nophoto = %q, want %q", got, "no photographs")
	}
	// The offer that goes must carry its images: the exporter reads them, and a
	// nil slice publishes a listing with no pictures.
	if len(send[0].Photos) != 1 || send[0].Photos[0].ID != "p-good" {
		t.Errorf("sent offer photos = %v, want [p-good]", photoIDs(send[0].Photos))
	}
}

// TestExportSetOfAnUnknownCartIsNotFound checks the error travels rather than
// arriving as an empty export.
func TestExportSetOfAnUnknownCartIsNotFound(t *testing.T) {
	s := NewService(newTestRepo(t))
	send, held, err := s.ExportSet(context.Background(), "nope")
	if !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("error = %v, want core.ErrNotFound", err)
	}
	if send != nil || held != nil {
		t.Errorf("send/held = %v/%v, want both nil on error", send, held)
	}
}
