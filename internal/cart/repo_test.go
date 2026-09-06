package cart

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
)

// seedThreeOffers inserts off-a, off-b and off-c in that creation order, each
// with two photos whose positions run opposite to both their insertion order and
// their id order. A grouping that leaned on either would come back wrong.
func seedThreeOffers(t *testing.T, r *Repo) {
	t.Helper()
	for i, id := range []string{"off-a", "off-b", "off-c"} {
		insertOffer(t, r, newListed(id, "SKU-"+id, "Title "+id, 1000+int64(i),
			baseTime.Add(time.Duration(i)*time.Hour)))
		suffix := string(rune('a' + i))
		insertPhoto(t, r, "p"+suffix+"-1", id, 1)
		insertPhoto(t, r, "p"+suffix+"-2", id, 0)
	}
}

// TestContentsReturnsOffersInCartOrderEachWithItsPhotos is the test the export
// path rests on.
//
// Three offers, two photos each, held in an order that matches neither the
// creation order (a, b, c) nor its reverse (c, b, a), so a Contents that fell
// back to offers.created_at would fail rather than coincidentally pass. Every
// offer must arrive with its own photos in position order: the exporter reads
// offer.Photos, and nil there publishes a listing with no images.
func TestContentsReturnsOffersInCartOrderEachWithItsPhotos(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	seedThreeOffers(t, r)

	makeCart(t, r, "cart-1", "eBay batch — September", 0)
	for _, id := range []string{"off-c", "off-a", "off-b"} {
		if err := r.AddToCart(ctx, "cart-1", id); err != nil {
			t.Fatalf("add %s: %v", id, err)
		}
	}

	got, err := r.Contents(ctx, "cart-1")
	if err != nil {
		t.Fatalf("contents: %v", err)
	}

	if got.Cart.ID != "cart-1" {
		t.Errorf("cart id = %q, want %q", got.Cart.ID, "cart-1")
	}
	if got.Cart.Size() != 3 {
		t.Errorf("cart size = %d, want 3", got.Cart.Size())
	}

	wantOrder := []string{"off-c", "off-a", "off-b"}
	if ids := offerIDs(got.Offers); !equalStrings(ids, wantOrder) {
		t.Fatalf("offer order = %v, want %v (cart_items.position, not creation order)",
			ids, wantOrder)
	}

	// Photos must land on the offer that owns them, in position order — not in
	// insertion order and not in id order, both of which are the reverse here.
	wantPhotos := map[string][]string{
		"off-a": {"pa-2", "pa-1"},
		"off-b": {"pb-2", "pb-1"},
		"off-c": {"pc-2", "pc-1"},
	}
	for _, o := range got.Offers {
		if len(o.Photos) == 0 {
			t.Fatalf("offer %s came back with no photos; an export would publish it "+
				"with no images", o.ID)
		}
		if ids := photoIDs(o.Photos); !equalStrings(ids, wantPhotos[o.ID]) {
			t.Errorf("offer %s photos = %v, want %v", o.ID, ids, wantPhotos[o.ID])
		}
		for _, p := range o.Photos {
			if p.OfferID != o.ID {
				t.Errorf("photo %s is attached to offer %s but belongs to %s",
					p.ID, o.ID, p.OfferID)
			}
		}
		if o.Photos[0].Position != 0 {
			t.Errorf("offer %s first photo is at position %d, want 0",
				o.ID, o.Photos[0].Position)
		}
	}
}

// TestContentsFollowsPositionAfterAReorder pins the same ordering rule against a
// cart whose order was changed after assembly, where add order and position no
// longer agree.
func TestContentsFollowsPositionAfterAReorder(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	seedThreeOffers(t, r)
	makeCart(t, r, "cart-1", "batch", 0)
	for _, id := range []string{"off-a", "off-b", "off-c"} {
		if err := r.AddToCart(ctx, "cart-1", id); err != nil {
			t.Fatalf("add %s: %v", id, err)
		}
	}

	want := []string{"off-b", "off-c", "off-a"}
	if err := r.ReorderCart(ctx, "cart-1", want); err != nil {
		t.Fatalf("reorder: %v", err)
	}

	got, err := r.Contents(ctx, "cart-1")
	if err != nil {
		t.Fatalf("contents: %v", err)
	}
	if ids := offerIDs(got.Offers); !equalStrings(ids, want) {
		t.Errorf("offer order = %v, want %v", ids, want)
	}
}

// TestContentsOfAnEmptyCartIsTheCartAndNothingElse checks the empty case is a
// cart with no offers rather than an error.
func TestContentsOfAnEmptyCartIsTheCartAndNothingElse(t *testing.T) {
	r := newTestRepo(t)
	makeCart(t, r, "cart-1", "empty batch", 0)

	got, err := r.Contents(context.Background(), "cart-1")
	if err != nil {
		t.Fatalf("contents: %v", err)
	}
	if got.Cart.Name != "empty batch" {
		t.Errorf("cart name = %q, want %q", got.Cart.Name, "empty batch")
	}
	if len(got.Offers) != 0 {
		t.Errorf("offers = %v, want none", offerIDs(got.Offers))
	}
}

// TestUnknownCartIsNotFound covers both read paths that resolve a cart by id.
func TestUnknownCartIsNotFound(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	if _, err := r.Cart(ctx, "nope"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("Cart error = %v, want core.ErrNotFound", err)
	}
	if _, err := r.Contents(ctx, "nope"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("Contents error = %v, want core.ErrNotFound", err)
	}
}

// TestAddToCartIsIdempotentAndKeepsThePosition is the stale-page case: the
// button is visible on a view rendered seconds ago, so a second click must
// change nothing at all — not the row count, and not where the item sits.
func TestAddToCartIsIdempotentAndKeepsThePosition(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	seedThreeOffers(t, r)
	makeCart(t, r, "cart-1", "batch", 0)

	for _, id := range []string{"off-a", "off-b"} {
		if err := r.AddToCart(ctx, "cart-1", id); err != nil {
			t.Fatalf("add %s: %v", id, err)
		}
	}
	if err := r.AddToCart(ctx, "cart-1", "off-a"); err != nil {
		t.Fatalf("re-add off-a: %v", err)
	}

	if n := countRows(t, r, "cart_items"); n != 2 {
		t.Errorf("cart_items rows = %d, want 2", n)
	}
	pos := positions(t, r, "cart-1")
	if pos["off-a"] != 0 {
		t.Errorf("off-a position = %d, want 0 — a duplicate add moved it", pos["off-a"])
	}
	if pos["off-b"] != 1 {
		t.Errorf("off-b position = %d, want 1", pos["off-b"])
	}
}

// TestAddToCartAppendsAfterTheHighestPosition checks that a gap left by a
// removal is not reused: renumbering to close it would move items the operator
// never touched.
func TestAddToCartAppendsAfterTheHighestPosition(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	seedThreeOffers(t, r)
	insertOffer(t, r, newDraft("off-d", "SKU-d", "Fourth", baseTime))
	makeCart(t, r, "cart-1", "batch", 0)

	for _, id := range []string{"off-a", "off-b", "off-c"} {
		if err := r.AddToCart(ctx, "cart-1", id); err != nil {
			t.Fatalf("add %s: %v", id, err)
		}
	}
	if err := r.RemoveFromCart(ctx, "cart-1", "off-b"); err != nil {
		t.Fatalf("remove off-b: %v", err)
	}
	if err := r.AddToCart(ctx, "cart-1", "off-d"); err != nil {
		t.Fatalf("add off-d: %v", err)
	}

	want := map[string]int{"off-a": 0, "off-c": 2, "off-d": 3}
	got := positions(t, r, "cart-1")
	if len(got) != len(want) {
		t.Fatalf("positions = %v, want %v", got, want)
	}
	for id, p := range want {
		if got[id] != p {
			t.Errorf("%s position = %d, want %d", id, got[id], p)
		}
	}
}

// TestAddAndRemoveMoveUpdatedAtOnlyWhenSomethingChanged pins the "which batch
// was I working on" column. A duplicate add and an absent removal changed
// nothing, so promoting the cart to the top of the list would be a claim nobody
// made.
func TestAddAndRemoveMoveUpdatedAtOnlyWhenSomethingChanged(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	seedThreeOffers(t, r)
	makeCart(t, r, "cart-1", "batch", 0)

	if err := r.AddToCart(ctx, "cart-1", "off-a"); err != nil {
		t.Fatalf("add off-a: %v", err)
	}
	if got := updatedAt(t, r, "cart-1"); !got.After(baseTime) {
		t.Errorf("updated_at = %s, want later than %s after a real add", got, baseTime)
	}

	// Rewind, then repeat the add that changes nothing.
	setUpdatedAt(t, r, "cart-1", baseTime)
	if err := r.AddToCart(ctx, "cart-1", "off-a"); err != nil {
		t.Fatalf("re-add off-a: %v", err)
	}
	if got := updatedAt(t, r, "cart-1"); !got.Equal(baseTime) {
		t.Errorf("updated_at = %s after a duplicate add, want it left at %s", got, baseTime)
	}

	// Same for a removal that finds nothing to remove.
	if err := r.RemoveFromCart(ctx, "cart-1", "off-c"); err != nil {
		t.Fatalf("remove absent off-c: %v", err)
	}
	if got := updatedAt(t, r, "cart-1"); !got.Equal(baseTime) {
		t.Errorf("updated_at = %s after removing an absent offer, want it left at %s",
			got, baseTime)
	}
	if n := countRows(t, r, "cart_items"); n != 1 {
		t.Errorf("cart_items rows = %d, want 1", n)
	}

	// And a real removal does move it.
	if err := r.RemoveFromCart(ctx, "cart-1", "off-a"); err != nil {
		t.Fatalf("remove off-a: %v", err)
	}
	if got := updatedAt(t, r, "cart-1"); !got.After(baseTime) {
		t.Errorf("updated_at = %s, want later than %s after a real removal", got, baseTime)
	}
	if n := countRows(t, r, "cart_items"); n != 0 {
		t.Errorf("cart_items rows = %d, want 0", n)
	}
}

// TestReorderCartRewritesEveryPosition checks the happy path renumbers from
// zero, in the order given.
func TestReorderCartRewritesEveryPosition(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	seedThreeOffers(t, r)
	makeCart(t, r, "cart-1", "batch", 0)
	for _, id := range []string{"off-a", "off-b", "off-c"} {
		if err := r.AddToCart(ctx, "cart-1", id); err != nil {
			t.Fatalf("add %s: %v", id, err)
		}
	}

	if err := r.ReorderCart(ctx, "cart-1", []string{"off-c", "off-b", "off-a"}); err != nil {
		t.Fatalf("reorder: %v", err)
	}

	want := map[string]int{"off-c": 0, "off-b": 1, "off-a": 2}
	got := positions(t, r, "cart-1")
	for id, p := range want {
		if got[id] != p {
			t.Errorf("%s position = %d, want %d", id, got[id], p)
		}
	}
}

// TestReorderCartRefusesAnythingButTheExactMembership covers the three shapes a
// bad list takes. Each must be [ErrNotCartMembership] and must leave every
// position exactly where it was: a partial reorder silently loses an item's
// place in a batch the operator arranged by hand.
func TestReorderCartRefusesAnythingButTheExactMembership(t *testing.T) {
	cases := []struct {
		name string
		give []string
	}{
		{"an id is missing", []string{"off-a", "off-b"}},
		{"an id belongs to no cart", []string{"off-a", "off-b", "off-d"}},
		{"an id is listed twice", []string{"off-a", "off-a", "off-b"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newTestRepo(t)
			ctx := context.Background()
			seedThreeOffers(t, r)
			insertOffer(t, r, newDraft("off-d", "SKU-d", "Not in the cart", baseTime))
			makeCart(t, r, "cart-1", "batch", 0)
			for _, id := range []string{"off-a", "off-b", "off-c"} {
				if err := r.AddToCart(ctx, "cart-1", id); err != nil {
					t.Fatalf("add %s: %v", id, err)
				}
			}
			before := positions(t, r, "cart-1")

			err := r.ReorderCart(ctx, "cart-1", tc.give)
			if !errors.Is(err, ErrNotCartMembership) {
				t.Fatalf("error = %v, want ErrNotCartMembership", err)
			}
			// The sentinel also has to read as a form-level failure, or a stale
			// drag-and-drop becomes a 500 instead of a re-render.
			if !errors.Is(err, core.ErrInvalid) {
				t.Errorf("error = %v, want it to also match core.ErrInvalid", err)
			}

			after := positions(t, r, "cart-1")
			for id, p := range before {
				if after[id] != p {
					t.Errorf("%s moved from %d to %d despite the refusal", id, p, after[id])
				}
			}
		})
	}
}

// TestCartsListsMostRecentlyUpdatedFirstWithTheirItems checks both halves of the
// listing contract: the order, and that core.Cart.Size can be trusted on it.
func TestCartsListsMostRecentlyUpdatedFirstWithTheirItems(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	seedThreeOffers(t, r)

	makeCart(t, r, "cart-old", "September", 0)
	makeCart(t, r, "cart-mid", "October", time.Hour)
	makeCart(t, r, "cart-new", "November", 2*time.Hour)
	for _, id := range []string{"off-a", "off-b"} {
		if err := r.AddToCart(ctx, "cart-old", id); err != nil {
			t.Fatalf("add %s: %v", id, err)
		}
	}
	// The adds above bumped cart-old to now; rewind so the fixture's ordering is
	// the one under test.
	setUpdatedAt(t, r, "cart-old", baseTime)

	got, err := r.Carts(ctx)
	if err != nil {
		t.Fatalf("carts: %v", err)
	}
	want := []string{"cart-new", "cart-mid", "cart-old"}
	if ids := cartIDs(got); !equalStrings(ids, want) {
		t.Fatalf("order = %v, want %v", ids, want)
	}
	for _, c := range got {
		wantSize := 0
		if c.ID == "cart-old" {
			wantSize = 2
		}
		if c.Size() != wantSize {
			t.Errorf("cart %s size = %d, want %d", c.ID, c.Size(), wantSize)
		}
	}
}

// TestCartsHoldingReturnsOnlyTheCartsWithThatOffer is what stops the operator
// adding the same item to a second batch without being told.
func TestCartsHoldingReturnsOnlyTheCartsWithThatOffer(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	seedThreeOffers(t, r)
	makeCart(t, r, "cart-1", "one", 0)
	makeCart(t, r, "cart-2", "two", time.Hour)
	makeCart(t, r, "cart-3", "three", 2*time.Hour)

	if err := r.AddToCart(ctx, "cart-1", "off-a"); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := r.AddToCart(ctx, "cart-2", "off-a"); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := r.AddToCart(ctx, "cart-3", "off-b"); err != nil {
		t.Fatalf("add: %v", err)
	}
	setUpdatedAt(t, r, "cart-1", baseTime)
	setUpdatedAt(t, r, "cart-2", baseTime.Add(time.Hour))

	got, err := r.CartsHolding(ctx, "off-a")
	if err != nil {
		t.Fatalf("carts holding: %v", err)
	}
	want := []string{"cart-2", "cart-1"}
	if ids := cartIDs(got); !equalStrings(ids, want) {
		t.Errorf("carts holding off-a = %v, want %v", ids, want)
	}
	for _, c := range got {
		if !c.Has("off-a") {
			t.Errorf("cart %s came back without its items, so Has reports wrongly", c.ID)
		}
	}
}

// TestDeletingAnOfferCascadesItsCartItems proves foreign_keys(1) is doing its
// job: an offer that no longer exists cannot be in a batch, and a leftover row
// would make the cart's export fail on a dangling id rather than simply be one
// item shorter.
func TestDeletingAnOfferCascadesItsCartItems(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	seedThreeOffers(t, r)
	makeCart(t, r, "cart-1", "batch", 0)
	for _, id := range []string{"off-a", "off-b"} {
		if err := r.AddToCart(ctx, "cart-1", id); err != nil {
			t.Fatalf("add %s: %v", id, err)
		}
	}

	if _, err := r.write.Exec(`DELETE FROM offers WHERE id = ?`, "off-a"); err != nil {
		t.Fatalf("delete offer: %v", err)
	}

	if n := countRows(t, r, "cart_items"); n != 1 {
		t.Fatalf("cart_items rows = %d, want 1", n)
	}
	pos := positions(t, r, "cart-1")
	if _, still := pos["off-a"]; still {
		t.Errorf("off-a still in cart_items after its offer was deleted")
	}
	if pos["off-b"] != 1 {
		t.Errorf("off-b position = %d, want 1 — the survivor must not move", pos["off-b"])
	}
}

// TestDeleteCartTakesItsMembershipButNotItsOffers checks the other cascade, and
// that discarding a selection is not a decision about stock.
func TestDeleteCartTakesItsMembershipButNotItsOffers(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	seedThreeOffers(t, r)
	makeCart(t, r, "cart-1", "batch", 0)
	if err := r.AddToCart(ctx, "cart-1", "off-a"); err != nil {
		t.Fatalf("add: %v", err)
	}

	if err := r.DeleteCart(ctx, "cart-1"); err != nil {
		t.Fatalf("delete cart: %v", err)
	}
	if n := countRows(t, r, "cart_items"); n != 0 {
		t.Errorf("cart_items rows = %d, want 0", n)
	}
	if n := countRows(t, r, "offers"); n != 3 {
		t.Errorf("offers rows = %d, want 3 — deleting a cart must not delete stock", n)
	}
	if err := r.DeleteCart(ctx, "cart-1"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("second delete = %v, want core.ErrNotFound", err)
	}
}

// TestCreateAndUpdateCartRoundTrip checks the stored columns come back as they
// went in, and that a rename of a cart that is not there is core.ErrNotFound
// rather than a silent success.
func TestCreateAndUpdateCartRoundTrip(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	c := core.Cart{
		ID: "cart-1", Name: "eBay batch — September", Note: "for Rita",
		CreatedAt: baseTime, UpdatedAt: baseTime,
	}
	if err := r.CreateCart(ctx, c); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := r.Cart(ctx, "cart-1")
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.Name != c.Name || got.Note != c.Note {
		t.Errorf("read back name/note = %q/%q, want %q/%q", got.Name, got.Note, c.Name, c.Note)
	}
	if !got.CreatedAt.Equal(baseTime) || !got.UpdatedAt.Equal(baseTime) {
		t.Errorf("timestamps = %s/%s, want %s", got.CreatedAt, got.UpdatedAt, baseTime)
	}

	c.Name = "eBay batch — October"
	c.CreatedAt = baseTime.Add(100 * time.Hour) // must be ignored by the update
	c.UpdatedAt = baseTime.Add(time.Hour)
	if err := r.UpdateCart(ctx, c); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, err = r.Cart(ctx, "cart-1")
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if got.Name != "eBay batch — October" {
		t.Errorf("name = %q, want %q", got.Name, "eBay batch — October")
	}
	if !got.CreatedAt.Equal(baseTime) {
		t.Errorf("created_at = %s, want it left at %s", got.CreatedAt, baseTime)
	}

	c.ID = "nope"
	if err := r.UpdateCart(ctx, c); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("update of an unknown cart = %v, want core.ErrNotFound", err)
	}
}

// TestAddToCartRefusesAnUnknownCart records that the no-op path relies on the
// foreign key: without it, an add against a cart that does not exist would
// silently create an orphan membership row.
func TestAddToCartRefusesAnUnknownCart(t *testing.T) {
	r := newTestRepo(t)
	seedThreeOffers(t, r)

	if err := r.AddToCart(context.Background(), "nope", "off-a"); err == nil {
		t.Fatal("adding to an unknown cart succeeded; the foreign key is not armed")
	}
	if n := countRows(t, r, "cart_items"); n != 0 {
		t.Errorf("cart_items rows = %d, want 0", n)
	}
}

// TestTheReadHandleRefusesToWrite proves the test harness reproduces the
// application's query_only(1) reader, so every read-path assertion in this suite
// is made against a handle that could not have written even by accident.
func TestTheReadHandleRefusesToWrite(t *testing.T) {
	r := newTestRepo(t)

	_, err := r.read.Exec(
		`INSERT INTO carts (id, name, note, created_at, updated_at) VALUES (?, '', '', ?, ?)`,
		"sneaky", formatTime(baseTime), formatTime(baseTime))
	if err == nil {
		t.Fatal("the reader accepted a write; query_only(1) is not in effect")
	}
	if n := countRows(t, r, "carts"); n != 0 {
		t.Errorf("carts rows = %d, want 0", n)
	}
}
