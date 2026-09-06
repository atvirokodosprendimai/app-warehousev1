package offer

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
)

// mustCreate inserts an offer and fails the test if it cannot.
func mustCreate(t *testing.T, r *Repo, o core.Offer) core.Offer {
	t.Helper()
	if err := r.CreateOffer(context.Background(), o); err != nil {
		t.Fatalf("create offer %s: %v", o.SKU, err)
	}
	return o
}

// mustAddPhoto inserts a photo row at an explicit position.
func mustAddPhoto(t *testing.T, r *Repo, id, offerID string, position int) core.Photo {
	t.Helper()
	p := core.Photo{
		ID:          id,
		OfferID:     offerID,
		Position:    position,
		Filename:    id + ".jpg",
		ContentType: "image/jpeg",
		ByteSize:    3,
		SHA256:      "abc",
		CreatedAt:   baseTime,
	}
	if err := r.AddPhoto(context.Background(), p); err != nil {
		t.Fatalf("add photo %s: %v", id, err)
	}
	return p
}

func TestOfferReturnsItsPhotosInPositionOrder(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	o := mustCreate(t, r, newOffer("o1", "SKU-1", "Vintage Lamp", baseTime))
	// Inserted out of order on purpose: ordering must come from the query, not
	// from insertion luck.
	mustAddPhoto(t, r, "p-third", o.ID, 2)
	mustAddPhoto(t, r, "p-first", o.ID, 0)
	mustAddPhoto(t, r, "p-second", o.ID, 1)

	got, err := r.Offer(ctx, "o1")
	if err != nil {
		t.Fatalf("Offer: %v", err)
	}
	want := []string{"p-first", "p-second", "p-third"}
	if len(got.Photos) != len(want) {
		t.Fatalf("Offer photos = %d, want %d", len(got.Photos), len(want))
	}
	for i, id := range want {
		if got.Photos[i].ID != id {
			t.Errorf("photo[%d] = %q, want %q", i, got.Photos[i].ID, id)
		}
		if got.Photos[i].Position != i {
			t.Errorf("photo[%d].Position = %d, want %d", i, got.Photos[i].Position, i)
		}
	}
	if got.PrimaryPhoto().ID != "p-first" {
		t.Errorf("PrimaryPhoto = %q, want p-first", got.PrimaryPhoto().ID)
	}
}

func TestOfferBySKUReturnsPhotosAndTheSameOffer(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	mustCreate(t, r, newOffer("o1", "SKU-1", "Vintage Lamp", baseTime))
	mustAddPhoto(t, r, "p1", "o1", 0)

	got, err := r.OfferBySKU(ctx, "SKU-1")
	if err != nil {
		t.Fatalf("OfferBySKU: %v", err)
	}
	if got.ID != "o1" {
		t.Errorf("ID = %q, want o1", got.ID)
	}
	if len(got.Photos) != 1 || got.Photos[0].ID != "p1" {
		t.Errorf("Photos = %+v, want exactly p1", got.Photos)
	}
}

func TestOfferAndOfferBySKUWrapErrNotFound(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	if _, err := r.Offer(ctx, "missing"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("Offer error = %v, want core.ErrNotFound", err)
	}
	if _, err := r.OfferBySKU(ctx, "missing"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("OfferBySKU error = %v, want core.ErrNotFound", err)
	}
}

func TestOffersPutsEachPhotoOnItsOwnOfferInOrder(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	// Three offers, distinct intake times so "newest first" is deterministic.
	mustCreate(t, r, newOffer("o1", "SKU-1", "One", baseTime))
	mustCreate(t, r, newOffer("o2", "SKU-2", "Two", baseTime.Add(time.Minute)))
	mustCreate(t, r, newOffer("o3", "SKU-3", "Three", baseTime.Add(2*time.Minute)))

	// Interleaved and out of position order, so a query that grouped by
	// insertion order or dropped the ORDER BY would produce a different answer.
	mustAddPhoto(t, r, "o2-b", "o2", 1)
	mustAddPhoto(t, r, "o1-b", "o1", 1)
	mustAddPhoto(t, r, "o3-a", "o3", 0)
	mustAddPhoto(t, r, "o1-a", "o1", 0)
	mustAddPhoto(t, r, "o2-a", "o2", 0)
	mustAddPhoto(t, r, "o1-c", "o1", 2)

	got, err := r.Offers(ctx, core.OfferFilter{})
	if err != nil {
		t.Fatalf("Offers: %v", err)
	}
	if diff := fmt.Sprint(ids(got)); diff != "[o3 o2 o1]" {
		t.Fatalf("order = %s, want [o3 o2 o1] (newest first)", diff)
	}

	want := map[string][]string{
		"o1": {"o1-a", "o1-b", "o1-c"},
		"o2": {"o2-a", "o2-b"},
		"o3": {"o3-a"},
	}
	for _, o := range got {
		var have []string
		for _, p := range o.Photos {
			if p.OfferID != o.ID {
				t.Errorf("photo %s landed on offer %s but belongs to %s", p.ID, o.ID, p.OfferID)
			}
			have = append(have, p.ID)
		}
		if fmt.Sprint(have) != fmt.Sprint(want[o.ID]) {
			t.Errorf("offer %s photos = %v, want %v", o.ID, have, want[o.ID])
		}
	}
}

func TestOffersZeroFilterReturnsEverything(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	mustCreate(t, r, newOffer("o1", "SKU-1", "One", baseTime))
	sold := baseTime.Add(time.Hour)
	o2 := newOffer("o2", "SKU-2", "Two", baseTime.Add(time.Minute))
	o2.Status = core.StatusSold
	o2.Sold = core.Money{Minor: 1000, Currency: "EUR"}
	o2.SoldAt = &sold
	mustCreate(t, r, o2)
	o3 := newOffer("o3", "SKU-3", "Three", baseTime.Add(2*time.Minute))
	o3.Status = core.StatusArchived
	mustCreate(t, r, o3)

	got, err := r.Offers(ctx, core.OfferFilter{})
	if err != nil {
		t.Fatalf("Offers: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("zero filter returned %d offers, want 3", len(got))
	}
}

func TestOffersFilterByStatusNarrows(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	mustCreate(t, r, newOffer("draft1", "SKU-1", "One", baseTime))
	listed := newOffer("listed1", "SKU-2", "Two", baseTime.Add(time.Minute))
	listed.Status = core.StatusListed
	listed.Shop = core.Money{Minor: 2500, Currency: "EUR"}
	mustCreate(t, r, listed)
	archived := newOffer("archived1", "SKU-3", "Three", baseTime.Add(2*time.Minute))
	archived.Status = core.StatusArchived
	mustCreate(t, r, archived)

	got, err := r.Offers(ctx, core.OfferFilter{Status: []core.Status{core.StatusListed, core.StatusArchived}})
	if err != nil {
		t.Fatalf("Offers: %v", err)
	}
	if s := fmt.Sprint(ids(got)); s != "[archived1 listed1]" {
		t.Errorf("status filter = %s, want [archived1 listed1]", s)
	}
}

func TestOffersFilterByQueryIsCaseInsensitiveAcrossSKUTitleDescription(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	bySKU := newOffer("o1", "LAMP-001", "Something else", baseTime)
	mustCreate(t, r, bySKU)
	byTitle := newOffer("o2", "SKU-2", "Vintage Lamp", baseTime.Add(time.Minute))
	mustCreate(t, r, byTitle)
	byDesc := newOffer("o3", "SKU-3", "Chair", baseTime.Add(2*time.Minute))
	byDesc.Description = "Comes with a matching lamp"
	mustCreate(t, r, byDesc)
	mustCreate(t, r, newOffer("o4", "SKU-4", "Table", baseTime.Add(3*time.Minute)))

	for _, q := range []string{"lamp", "LAMP", "LaMp"} {
		got, err := r.Offers(ctx, core.OfferFilter{Query: q})
		if err != nil {
			t.Fatalf("Offers(%q): %v", q, err)
		}
		if s := fmt.Sprint(ids(got)); s != "[o3 o2 o1]" {
			t.Errorf("Query %q = %s, want [o3 o2 o1]", q, s)
		}
	}
}

func TestOffersQueryDoesNotTreatUnderscoreAsAWildcard(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	mustCreate(t, r, newOffer("o1", "A_B", "Underscore", baseTime))
	mustCreate(t, r, newOffer("o2", "AXB", "Sibling", baseTime.Add(time.Minute)))

	got, err := r.Offers(ctx, core.OfferFilter{Query: "A_B"})
	if err != nil {
		t.Fatalf("Offers: %v", err)
	}
	if s := fmt.Sprint(ids(got)); s != "[o1]" {
		t.Errorf("Query %q = %s, want [o1] only — %q is a LIKE wildcard unless escaped", "A_B", s, "_")
	}
}

func TestOffersLocationPathPrefixEscapesLikeWildcards(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	// Sibling sites whose paths differ only in the character LIKE treats as
	// "any one character".
	insertLocation(t, r, "loc-underscore", "A_B", "A_B")
	insertLocation(t, r, "loc-sibling", "AXB", "AXB")
	insertLocation(t, r, "loc-child", "A_B-S1", "A_B/S1")

	inPrefix := newOffer("o1", "SKU-1", "At A_B", baseTime)
	inPrefix.LocationID = "loc-underscore"
	mustCreate(t, r, inPrefix)

	sibling := newOffer("o2", "SKU-2", "At AXB", baseTime.Add(time.Minute))
	sibling.LocationID = "loc-sibling"
	mustCreate(t, r, sibling)

	child := newOffer("o3", "SKU-3", "Below A_B", baseTime.Add(2*time.Minute))
	child.LocationID = "loc-child"
	mustCreate(t, r, child)

	unshelved := newOffer("o4", "SKU-4", "Nowhere yet", baseTime.Add(3*time.Minute))
	mustCreate(t, r, unshelved)

	got, err := r.Offers(ctx, core.OfferFilter{LocationPathPrefix: "A_B"})
	if err != nil {
		t.Fatalf("Offers: %v", err)
	}
	if s := fmt.Sprint(ids(got)); s != "[o3 o1]" {
		t.Errorf("LocationPathPrefix %q = %s, want [o3 o1]: the sibling AXB must not match "+
			"and the unshelved offer has no location at all", "A_B", s)
	}
}

func TestOffersUnshelvedOfferStoresNullLocation(t *testing.T) {
	r := newTestRepo(t)

	mustCreate(t, r, newOffer("o1", "SKU-1", "Nowhere yet", baseTime))

	var n int
	if err := r.write.QueryRow(
		`SELECT COUNT(*) FROM offers WHERE location_id IS NULL`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 1 {
		t.Errorf("offers with NULL location_id = %d, want 1: an empty LocationID must be "+
			"NULL, not a foreign key pointing at nothing", n)
	}
	got, err := r.Offer(context.Background(), "o1")
	if err != nil {
		t.Fatalf("Offer: %v", err)
	}
	if got.LocationID != "" {
		t.Errorf("LocationID = %q, want empty", got.LocationID)
	}
}

func TestOffersFilterNeedsPricingIsTheResearchQueue(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	mustCreate(t, r, newOffer("unpriced", "SKU-1", "Waiting on research", baseTime))

	priced := newOffer("priced", "SKU-2", "Researched", baseTime.Add(time.Minute))
	priced.Shop = core.Money{Minor: 2500, Currency: "EUR"}
	mustCreate(t, r, priced)

	listedUnpriced := newOffer("archived", "SKU-3", "Withdrawn", baseTime.Add(2*time.Minute))
	listedUnpriced.Status = core.StatusArchived
	mustCreate(t, r, listedUnpriced)

	got, err := r.Offers(ctx, core.OfferFilter{NeedsPricing: true})
	if err != nil {
		t.Fatalf("Offers: %v", err)
	}
	if s := fmt.Sprint(ids(got)); s != "[unpriced]" {
		t.Errorf("NeedsPricing = %s, want [unpriced]", s)
	}
}

func TestOffersLimitAndOffsetPage(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	mustCreate(t, r, newOffer("o1", "SKU-1", "One", baseTime))
	mustCreate(t, r, newOffer("o2", "SKU-2", "Two", baseTime.Add(time.Minute)))
	mustCreate(t, r, newOffer("o3", "SKU-3", "Three", baseTime.Add(2*time.Minute)))

	first, err := r.Offers(ctx, core.OfferFilter{Limit: 2})
	if err != nil {
		t.Fatalf("Offers: %v", err)
	}
	if s := fmt.Sprint(ids(first)); s != "[o3 o2]" {
		t.Errorf("page 1 = %s, want [o3 o2]", s)
	}

	second, err := r.Offers(ctx, core.OfferFilter{Limit: 2, Offset: 2})
	if err != nil {
		t.Fatalf("Offers: %v", err)
	}
	if s := fmt.Sprint(ids(second)); s != "[o1]" {
		t.Errorf("page 2 = %s, want [o1]", s)
	}
}

func TestOffersAppliesADefaultLimitWhenNoneIsGiven(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	for i := 0; i < defaultLimit+5; i++ {
		mustCreate(t, r, newOffer(
			fmt.Sprintf("o%03d", i),
			fmt.Sprintf("SKU-%03d", i),
			"Bulk",
			baseTime.Add(time.Duration(i)*time.Minute)))
	}

	got, err := r.Offers(ctx, core.OfferFilter{})
	if err != nil {
		t.Fatalf("Offers: %v", err)
	}
	if len(got) != defaultLimit {
		t.Errorf("zero-limit page = %d offers, want the default cap of %d", len(got), defaultLimit)
	}
}

func TestOffersCombinesFilters(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	insertLocation(t, r, "loc", "KAUNAS", "KAUNAS")

	wanted := newOffer("wanted", "SKU-1", "Vintage Lamp", baseTime)
	wanted.LocationID = "loc"
	mustCreate(t, r, wanted)

	wrongTitle := newOffer("wrong-title", "SKU-2", "Chair", baseTime.Add(time.Minute))
	wrongTitle.LocationID = "loc"
	mustCreate(t, r, wrongTitle)

	wrongStatus := newOffer("wrong-status", "SKU-3", "Vintage Lamp", baseTime.Add(2*time.Minute))
	wrongStatus.LocationID = "loc"
	wrongStatus.Status = core.StatusArchived
	mustCreate(t, r, wrongStatus)

	mustCreate(t, r, newOffer("wrong-place", "SKU-4", "Vintage Lamp", baseTime.Add(3*time.Minute)))

	got, err := r.Offers(ctx, core.OfferFilter{
		Status:             []core.Status{core.StatusDraft},
		LocationPathPrefix: "KAUNAS",
		Query:              "lamp",
		NeedsPricing:       true,
	})
	if err != nil {
		t.Fatalf("Offers: %v", err)
	}
	if s := fmt.Sprint(ids(got)); s != "[wanted]" {
		t.Errorf("combined filter = %s, want [wanted]", s)
	}
}

func TestCreateOfferMapsDuplicateSKUToErrSKUTaken(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	mustCreate(t, r, newOffer("o1", "SKU-1", "One", baseTime))

	err := r.CreateOffer(ctx, newOffer("o2", "SKU-1", "Two", baseTime))
	if !errors.Is(err, ErrSKUTaken) {
		t.Fatalf("CreateOffer with a duplicate SKU = %v, want ErrSKUTaken", err)
	}
	if n := countRows(t, r, "offers"); n != 1 {
		t.Errorf("offers = %d, want 1: the rejected insert must leave nothing behind", n)
	}
}

func TestUpdateOfferPersistsAndKeepsCreatedAt(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	o := mustCreate(t, r, newOffer("o1", "SKU-1", "Before", baseTime))

	o.Title = "After"
	o.Shop = core.Money{Minor: 4999, Currency: "EUR"}
	o.Owner = core.Money{Minor: 3000, Currency: "EUR"}
	o.CreatedAt = baseTime.Add(100 * time.Hour) // must be ignored
	o.UpdatedAt = baseTime.Add(time.Hour)
	if err := r.UpdateOffer(ctx, o); err != nil {
		t.Fatalf("UpdateOffer: %v", err)
	}

	got, err := r.Offer(ctx, "o1")
	if err != nil {
		t.Fatalf("Offer: %v", err)
	}
	if got.Title != "After" {
		t.Errorf("Title = %q, want After", got.Title)
	}
	if got.Shop != (core.Money{Minor: 4999, Currency: "EUR"}) {
		t.Errorf("Shop = %+v, want 4999 EUR", got.Shop)
	}
	if !got.CreatedAt.Equal(baseTime) {
		t.Errorf("CreatedAt = %v, want %v: an update must not rewrite intake time",
			got.CreatedAt, baseTime)
	}
	if !got.UpdatedAt.Equal(baseTime.Add(time.Hour)) {
		t.Errorf("UpdatedAt = %v, want %v", got.UpdatedAt, baseTime.Add(time.Hour))
	}
}

func TestUpdateDeleteAndDeletePhotoReportNotFound(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	err := r.UpdateOffer(ctx, newOffer("missing", "SKU-X", "Ghost", baseTime))
	if !errors.Is(err, core.ErrNotFound) {
		t.Errorf("UpdateOffer on a missing row = %v, want core.ErrNotFound", err)
	}
	if err := r.DeleteOffer(ctx, "missing"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("DeleteOffer on a missing row = %v, want core.ErrNotFound", err)
	}
	if err := r.DeletePhoto(ctx, "missing"); !errors.Is(err, core.ErrNotFound) {
		t.Errorf("DeletePhoto on a missing row = %v, want core.ErrNotFound", err)
	}
}

func TestDeleteOfferCascadesPhotoRows(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	mustCreate(t, r, newOffer("o1", "SKU-1", "One", baseTime))
	mustAddPhoto(t, r, "p1", "o1", 0)
	mustAddPhoto(t, r, "p2", "o1", 1)

	if err := r.DeleteOffer(ctx, "o1"); err != nil {
		t.Fatalf("DeleteOffer: %v", err)
	}
	if n := countRows(t, r, "offer_photos"); n != 0 {
		t.Errorf("offer_photos = %d, want 0 after the cascade", n)
	}
}

func TestReorderPhotosRewritesEveryPosition(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	mustCreate(t, r, newOffer("o1", "SKU-1", "One", baseTime))
	mustAddPhoto(t, r, "a", "o1", 0)
	mustAddPhoto(t, r, "b", "o1", 1)
	mustAddPhoto(t, r, "c", "o1", 2)

	if err := r.ReorderPhotos(ctx, "o1", []string{"c", "a", "b"}); err != nil {
		t.Fatalf("ReorderPhotos: %v", err)
	}
	got, err := r.Offer(ctx, "o1")
	if err != nil {
		t.Fatalf("Offer: %v", err)
	}
	var order []string
	for i, p := range got.Photos {
		order = append(order, p.ID)
		if p.Position != i {
			t.Errorf("photo %s at index %d has position %d", p.ID, i, p.Position)
		}
	}
	if fmt.Sprint(order) != "[c a b]" {
		t.Errorf("order = %v, want [c a b]", order)
	}
}

func TestReorderPhotosRefusesAnythingButTheWholeSet(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	mustCreate(t, r, newOffer("o1", "SKU-1", "One", baseTime))
	mustCreate(t, r, newOffer("o2", "SKU-2", "Two", baseTime.Add(time.Minute)))
	mustAddPhoto(t, r, "a", "o1", 0)
	mustAddPhoto(t, r, "b", "o1", 1)
	mustAddPhoto(t, r, "foreign", "o2", 0)

	cases := map[string][]string{
		"missing an id": {"a"},
		"foreign id":    {"a", "foreign"},
		"duplicate id":  {"a", "a"},
		"empty list":    {},
	}
	for name, list := range cases {
		t.Run(name, func(t *testing.T) {
			err := r.ReorderPhotos(ctx, "o1", list)
			if !errors.Is(err, core.ErrInvalid) {
				t.Fatalf("ReorderPhotos(%v) = %v, want core.ErrInvalid", list, err)
			}
			got, err := r.Offer(ctx, "o1")
			if err != nil {
				t.Fatalf("Offer: %v", err)
			}
			if len(got.Photos) != 2 || got.Photos[0].ID != "a" || got.Photos[1].ID != "b" {
				t.Errorf("a refused reorder changed the order: %+v", got.Photos)
			}
			if got.Photos[0].Position != 0 || got.Photos[1].Position != 1 {
				t.Errorf("a refused reorder changed positions: %d, %d",
					got.Photos[0].Position, got.Photos[1].Position)
			}
		})
	}
}

func TestZeroMoneyRoundTripsWithoutInventingACurrency(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	mustCreate(t, r, newOffer("o1", "SKU-1", "Unpriced draft", baseTime))

	got, err := r.Offer(ctx, "o1")
	if err != nil {
		t.Fatalf("Offer: %v", err)
	}
	for name, m := range map[string]core.Money{"Shop": got.Shop, "Owner": got.Owner, "Sold": got.Sold} {
		if m != (core.Money{}) {
			t.Errorf("%s = %+v, want the zero Money — a price nobody set must not acquire "+
				"a currency on the way to disk", name, m)
		}
	}
	if got.SoldAt != nil {
		t.Errorf("SoldAt = %v, want nil", got.SoldAt)
	}
	if !got.NeedsPricing() {
		t.Error("NeedsPricing = false, want true for an unpriced draft")
	}
}

func TestMoneyAndSoldAtRoundTrip(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	sold := time.Date(2026, 9, 1, 14, 30, 0, 0, time.UTC)
	o := newOffer("o1", "SKU-1", "Sold thing", baseTime)
	o.Status = core.StatusSold
	o.Shop = core.Money{Minor: 4999, Currency: "EUR"}
	o.Owner = core.Money{Minor: 3000, Currency: "PLN"}
	o.Sold = core.Money{Minor: 4500, Currency: "EUR"}
	o.SoldAt = &sold
	mustCreate(t, r, o)

	got, err := r.Offer(ctx, "o1")
	if err != nil {
		t.Fatalf("Offer: %v", err)
	}
	if got.Shop != (core.Money{Minor: 4999, Currency: "EUR"}) {
		t.Errorf("Shop = %+v", got.Shop)
	}
	if got.Owner != (core.Money{Minor: 3000, Currency: "PLN"}) {
		t.Errorf("Owner = %+v", got.Owner)
	}
	if got.Sold != (core.Money{Minor: 4500, Currency: "EUR"}) {
		t.Errorf("Sold = %+v", got.Sold)
	}
	if got.SoldAt == nil || !got.SoldAt.Equal(sold) {
		t.Fatalf("SoldAt = %v, want %v", got.SoldAt, sold)
	}
	if got.SoldAt.Location() != time.UTC {
		t.Errorf("SoldAt location = %v, want UTC", got.SoldAt.Location())
	}
}

func TestCountByStatus(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	mustCreate(t, r, newOffer("d1", "SKU-1", "One", baseTime))
	mustCreate(t, r, newOffer("d2", "SKU-2", "Two", baseTime.Add(time.Minute)))
	listed := newOffer("l1", "SKU-3", "Three", baseTime.Add(2*time.Minute))
	listed.Status = core.StatusListed
	listed.Shop = core.Money{Minor: 100, Currency: "EUR"}
	mustCreate(t, r, listed)

	got, err := r.CountByStatus(ctx)
	if err != nil {
		t.Fatalf("CountByStatus: %v", err)
	}
	if got[core.StatusDraft] != 2 {
		t.Errorf("draft = %d, want 2", got[core.StatusDraft])
	}
	if got[core.StatusListed] != 1 {
		t.Errorf("listed = %d, want 1", got[core.StatusListed])
	}
	if _, ok := got[core.StatusSold]; ok {
		t.Errorf("sold is present as %d; an empty status is absent and reads as 0",
			got[core.StatusSold])
	}
}

func TestLikeEscape(t *testing.T) {
	cases := map[string]string{
		"A_B":   `A\_B`,
		"50%":   `50\%`,
		`back\`: `back\\`,
		"plain": "plain",
	}
	for in, want := range cases {
		if got := likeEscape(in); got != want {
			t.Errorf("likeEscape(%q) = %q, want %q", in, got, want)
		}
	}
}
