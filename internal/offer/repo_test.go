package offer

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
		ParentID:    offerID,
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
			if p.ParentID != o.ID {
				t.Errorf("photo %s landed on offer %s but belongs to %s", p.ID, o.ID, p.ParentID)
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
		// All three fields are searched, and the one that does not mention a lamp
		// is not returned. The SET is asserted separately from the ORDER, because
		// they are different properties and only one of them changed when this
		// moved from LIKE to a full-text index.
		if len(got) != 3 {
			t.Fatalf("Query %q = %s, want three matches", q, fmt.Sprint(ids(got)))
		}
		found := map[string]bool{}
		for _, o := range got {
			found[o.ID] = true
		}
		for _, want := range []string{"o1", "o2", "o3"} {
			if !found[want] {
				t.Errorf("Query %q did not find %s: got %s", q, want, fmt.Sprint(ids(got)))
			}
		}
		if found["o4"] {
			t.Errorf("Query %q returned the unrelated offer o4", q)
		}
	}
}

// TestSearchOrdersByRelevanceNotRecency pins the ordering property that a plain
// listing does NOT have.
//
// o3 is the newest row and o2 is the best match. If somebody restores the
// created_at ordering for searches, the newest row leads and an exact title
// match is buried under whatever came in this morning — which is the whole
// reason the query builder switches ordering when a search term is present.
func TestSearchOrdersByRelevanceNotRecency(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	mustCreate(t, r, newOffer("o1", "LAMP-001", "Something else", baseTime))
	mustCreate(t, r, newOffer("o2", "SKU-2", "Vintage Lamp", baseTime.Add(time.Minute)))
	desc := newOffer("o3", "SKU-3", "Chair", baseTime.Add(2*time.Minute))
	desc.Description = "Comes with a matching lamp somewhere in this much longer description"
	mustCreate(t, r, desc)

	got, err := r.Offers(ctx, core.OfferFilter{Query: "lamp"})
	if err != nil {
		t.Fatalf("Offers: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("no matches")
	}
	if got[0].ID == "o3" {
		t.Errorf("the newest row led the results (%s), so the search is ordered by "+
			"date rather than relevance", fmt.Sprint(ids(got)))
	}

	// And the contrast: with no search term, recency IS the order.
	all, err := r.Offers(ctx, core.OfferFilter{})
	if err != nil {
		t.Fatalf("Offers: %v", err)
	}
	if all[0].ID != "o3" {
		t.Errorf("an unfiltered listing = %s, want the newest (o3) first",
			fmt.Sprint(ids(all)))
	}
}

// TestSearchMatchesWordsInAnyOrderAndAcrossFields is what a full-text index buys
// over a substring match, and it is the reason the change was made: "brass lamp"
// has to find "Lamp, brass" even though that substring appears nowhere in it.
func TestSearchMatchesWordsInAnyOrderAndAcrossFields(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	reordered := newOffer("o1", "SKU-1", "Lamp, brass", baseTime)
	mustCreate(t, r, reordered)
	split := newOffer("o2", "SKU-2", "Desk lamp", baseTime.Add(time.Minute))
	split.Description = "solid brass, rewired"
	mustCreate(t, r, split)
	mustCreate(t, r, newOffer("o3", "SKU-3", "Brass doorknob", baseTime.Add(2*time.Minute)))

	got, err := r.Offers(ctx, core.OfferFilter{Query: "brass lamp"})
	if err != nil {
		t.Fatalf("Offers: %v", err)
	}
	found := map[string]bool{}
	for _, o := range got {
		found[o.ID] = true
	}
	if !found["o1"] {
		t.Error("a title with the words in the other order was not found")
	}
	if !found["o2"] {
		t.Error("a match split between title and description was not found")
	}
	if found["o3"] {
		t.Error("an offer matching only one of the two words was returned; the terms " +
			"are joined with AND, so adding a word must narrow the search")
	}
}

// TestSearchIndexFollowsUpdatesAndDeletes is the test that matters most for an
// external-content index, because nothing else notices when it goes wrong.
//
// The index does not update itself: three triggers keep it in step. If one is
// missing or names the wrong columns, the search simply starts returning stale
// or deleted rows, with no error anywhere and no failing build.
func TestSearchIndexFollowsUpdatesAndDeletes(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	o := newOffer("o1", "SKU-1", "Vintage brass lamp", baseTime)
	mustCreate(t, r, o)

	// Present under its original title.
	got, err := r.Offers(ctx, core.OfferFilter{Query: "brass"})
	if err != nil || len(got) != 1 {
		t.Fatalf("before update: got %v, err %v", ids(got), err)
	}

	// Retitle it. The old word must stop matching and the new one must start.
	o.Title = "Oak dining chair"
	o.UpdatedAt = baseTime.Add(time.Hour)
	if err := r.UpdateOffer(ctx, o); err != nil {
		t.Fatalf("UpdateOffer: %v", err)
	}
	if got, err = r.Offers(ctx, core.OfferFilter{Query: "brass"}); err != nil {
		t.Fatalf("after update: %v", err)
	} else if len(got) != 0 {
		t.Errorf("the old title still matches after a rename, so the update trigger "+
			"is not removing the previous index entry: got %v", ids(got))
	}
	if got, err = r.Offers(ctx, core.OfferFilter{Query: "oak"}); err != nil {
		t.Fatalf("after update: %v", err)
	} else if len(got) != 1 {
		t.Errorf("the new title does not match after a rename, so the update trigger "+
			"is not adding the new index entry: got %v", ids(got))
	}

	// Delete it. It must leave the index too.
	if err := r.DeleteOffer(ctx, "o1"); err != nil {
		t.Fatalf("DeleteOffer: %v", err)
	}
	if got, err = r.Offers(ctx, core.OfferFilter{Query: "oak"}); err != nil {
		t.Fatalf("after delete: %v", err)
	} else if len(got) != 0 {
		t.Errorf("a deleted offer is still in the search index: got %v", ids(got))
	}
}

// TestSearchSurvivesPunctuationInTheQuery guards the raw-string trap: FTS5 MATCH
// takes a query LANGUAGE, so a stray quote is a syntax error and a bare "AND" or
// "NOT" is an operator. Passing what somebody typed straight through fails the
// whole search rather than finding nothing.
func TestSearchSurvivesPunctuationInTheQuery(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()
	mustCreate(t, r, newOffer("o1", "SKU-1", `The "good" chair`, baseTime))

	for _, q := range []string{`"`, `good"`, `AND`, `NOT`, `chair OR`, `*`, `-`} {
		if _, err := r.Offers(ctx, core.OfferFilter{Query: q}); err != nil {
			t.Errorf("Offers(%q) failed instead of simply matching nothing: %v", q, err)
		}
	}

	// And a quoted word still finds its row rather than erroring.
	got, err := r.Offers(ctx, core.OfferFilter{Query: `good`})
	if err != nil {
		t.Fatalf("Offers: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("got %v, want the one matching offer", ids(got))
	}
}

// TestFilterByPriceRange covers the bounds and the currency rule.
func TestFilterByPriceRange(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	priced := func(id, sku string, minor int64, cur string, at time.Time) {
		t.Helper()
		o := newOffer(id, sku, "Item "+id, at)
		o.Shop = core.Money{Minor: minor, Currency: cur}
		mustCreate(t, r, o)
	}
	priced("cheap", "SKU-1", 1000, "EUR", baseTime)                    // 10.00 EUR
	priced("mid", "SKU-2", 5000, "EUR", baseTime.Add(time.Minute))     // 50.00 EUR
	priced("dear", "SKU-3", 20000, "EUR", baseTime.Add(2*time.Minute)) // 200.00 EUR
	priced("yen", "SKU-4", 5000, "JPY", baseTime.Add(3*time.Minute))   // 5000 JPY

	eur := func(minor int64) *core.Money { return &core.Money{Minor: minor, Currency: "EUR"} }

	got, err := r.Offers(ctx, core.OfferFilter{PriceMin: eur(2000), PriceMax: eur(100000)})
	if err != nil {
		t.Fatalf("Offers: %v", err)
	}
	found := map[string]bool{}
	for _, o := range got {
		found[o.ID] = true
	}
	if found["cheap"] {
		t.Error("an offer below the minimum was returned")
	}
	if !found["mid"] || !found["dear"] {
		t.Errorf("a bounded range missed offers inside it: %v", ids(got))
	}
	// ★ The currency rule. 5000 JPY has the SAME minor units as 50.00 EUR, so a
	// bound that ignored the currency would return it as though it were fifty
	// euros. That is the whole reason a bound carries its currency.
	if found["yen"] {
		t.Error("a JPY offer matched a EUR price range: minor units were compared " +
			"across currencies, so 5000 JPY passed as 50.00 EUR")
	}

	// An open upper bound.
	if got, err = r.Offers(ctx, core.OfferFilter{PriceMin: eur(10000)}); err != nil {
		t.Fatalf("Offers: %v", err)
	} else if len(got) != 1 || got[0].ID != "dear" {
		t.Errorf("min-only filter = %v, want just the dear one", ids(got))
	}

	// An open lower bound.
	if got, err = r.Offers(ctx, core.OfferFilter{PriceMax: eur(1500)}); err != nil {
		t.Fatalf("Offers: %v", err)
	} else if len(got) != 1 || got[0].ID != "cheap" {
		t.Errorf("max-only filter = %v, want just the cheap one", ids(got))
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

// TestOffersFilterNeedsDescribingMatchesThePredicate is ADR-019's queue, and it
// asserts the thing that actually breaks.
//
// The queue exists twice — once as SQL in this package and once as
// core.Offer.NeedsDescribing in Go — and nothing forces the two to stay in step.
// When they drift, the sidebar count and the list it links to disagree: the
// operator is either promised work they cannot find, or shown work nobody told
// them about. So this compares the two against each other rather than against a
// hand-written expected list, which would keep passing while both sides drifted
// together.
func TestOffersFilterNeedsDescribingMatchesThePredicate(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	mustCreate(t, r, newOffer("unnamed", "SKU-1", "", baseTime))
	// A title of nothing but spaces is not a title. SQL must agree with the domain
	// about that, or the two queues differ by exactly the rows somebody has pressed
	// the space bar in.
	mustCreate(t, r, newOffer("spaces", "SKU-2", "   ", baseTime.Add(time.Minute)))
	mustCreate(t, r, newOffer("named", "SKU-3", "Vintage brass desk lamp", baseTime.Add(2*time.Minute)))
	// Undescribed but no longer a draft: out of the queue, because the queue is
	// work somebody is expected to pick up.
	archived := newOffer("archived", "SKU-4", "", baseTime.Add(3*time.Minute))
	archived.Status = core.StatusArchived
	mustCreate(t, r, archived)

	got, err := r.Offers(ctx, core.OfferFilter{NeedsDescribing: true})
	if err != nil {
		t.Fatalf("Offers: %v", err)
	}

	inQueue := map[string]bool{}
	for _, o := range got {
		inQueue[o.ID] = true
		if !o.NeedsDescribing() {
			t.Errorf("%s is in the SQL queue but NeedsDescribing() is false — the clause and "+
				"the predicate have drifted apart", o.ID)
		}
	}

	all, err := r.Offers(ctx, core.OfferFilter{})
	if err != nil {
		t.Fatalf("Offers(all): %v", err)
	}
	if len(all) != 4 {
		t.Fatalf("the fixture holds %d offers, want 4 — the sweep below would prove nothing", len(all))
	}
	for _, o := range all {
		if o.NeedsDescribing() && !inQueue[o.ID] {
			t.Errorf("%s satisfies NeedsDescribing() but the SQL queue omits it, so the "+
				"sidebar would count work the listing does not show", o.ID)
		}
	}
	if len(got) != 2 {
		t.Errorf("the queue holds %d rows, want 2 — the empty title and the whitespace one. "+
			"A queue of 0 would make both sweeps above vacuous", len(got))
	}
}

// TestPublishingAnUntitledOfferIsRefusedByTheDatabase proves ADR-019's second
// gate, the one no Go path can bypass.
//
// The domain refuses this as well, which is exactly why the row is written with
// raw SQL here: the whole point of the trigger is the writer that never calls
// Validate — a migration, a future importer, or somebody at a sqlite3 prompt.
// ADR-004 established the doubling up for the price; this is the same argument
// for the title.
func TestPublishingAnUntitledOfferIsRefusedByTheDatabase(t *testing.T) {
	r := newTestRepo(t)
	ts := formatTime(baseTime)

	// An untitled DRAFT must be accepted, or the refusal below would be happening
	// for the wrong reason and this test would pass against a database that simply
	// rejects empty titles everywhere.
	if _, err := r.write.Exec(
		`INSERT INTO offers (id, sku, title, status, quantity, shop_minor, created_at, updated_at)
		 VALUES ('d1', 'SKU-D1', '', 'draft', 1, 0, ?, ?)`, ts, ts); err != nil {
		t.Fatalf("an untitled DRAFT was refused by the database, which ADR-019 permits: %v", err)
	}

	// Publishing it is what must fail.
	_, err := r.write.Exec(`UPDATE offers SET status = 'listed', shop_minor = 4500 WHERE id = 'd1'`)
	if err == nil {
		t.Fatal("the database accepted a listed offer with no title: the trigger from " +
			"migration 00008 is missing or was dropped, and a marketplace would be sent a " +
			"listing headed by an empty string")
	}
	if !strings.Contains(err.Error(), "needs a title") {
		t.Errorf("refused with %v, want the trigger's own message — a different error means "+
			"something else refused this row and the trigger is still unproven", err)
	}

	// The INSERT arm of the same rule, and a whitespace-only title, which trim()
	// has to treat as absent exactly as the domain does.
	if _, err := r.write.Exec(
		`INSERT INTO offers (id, sku, title, status, quantity, shop_minor, created_at, updated_at)
		 VALUES ('l1', 'SKU-L1', '   ', 'listed', 1, 4500, ?, ?)`, ts, ts); err == nil {
		t.Error("the database accepted an INSERT of a listed offer whose title is only spaces")
	}

	// The row must still be a draft: a refused UPDATE that half-applied would be
	// worse than one that never ran.
	var status string
	if err := r.write.QueryRow(`SELECT status FROM offers WHERE id = 'd1'`).Scan(&status); err != nil {
		t.Fatalf("read back: %v", err)
	}
	if status != "draft" {
		t.Errorf("status = %q after the refused update, want draft", status)
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

// TestOfferRoundTripsItsPerProfileCategories pins ADR-016's per-offer half.
//
// eBay's and Allegro's categories are per ITEM, so one category per export run
// means one export per category — which is what M's own error message argued
// when it said a warehouse that sells anything has no defensible default.
func TestOfferRoundTripsItsPerProfileCategories(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	o := newOffer("o1", "WH0000001", "Brass desk lamp", baseTime)
	if err := r.CreateOffer(ctx, o); err != nil {
		t.Fatalf("CreateOffer: %v", err)
	}

	// A fresh offer has none, and that is a valid state: it exports on the
	// configured default rather than refusing.
	got, err := r.Offer(ctx, "o1")
	if err != nil {
		t.Fatalf("Offer: %v", err)
	}
	if len(got.Marketplace) != 0 {
		t.Errorf("a new offer has marketplace values %v, want none — a category must not be "+
			"required at intake", got.Marketplace)
	}

	if err := r.SetMarketplaceValue(ctx, "o1", "ebay", core.MarketplaceCategory, "11450"); err != nil {
		t.Fatalf("SetMarketplaceValue: %v", err)
	}
	if err := r.SetMarketplaceValue(ctx, "o1", "shopify", core.MarketplaceCategory, "Lighting"); err != nil {
		t.Fatalf("SetMarketplaceValue (shopify): %v", err)
	}

	got, err = r.Offer(ctx, "o1")
	if err != nil {
		t.Fatalf("Offer: %v", err)
	}
	if got := got.MarketplaceValue("ebay", core.MarketplaceCategory); got != "11450" {
		t.Errorf("ebay category = %q, want %q", got, "11450")
	}
	// Two profiles, two different KINDS of value on one offer — a number and a
	// name. That is why this is a table with a TEXT column and not one column.
	if got := got.MarketplaceValue("shopify", core.MarketplaceCategory); got != "Lighting" {
		t.Errorf("shopify category = %q, want %q", got, "Lighting")
	}

	// Setting it again replaces rather than duplicating.
	if err := r.SetMarketplaceValue(ctx, "o1", "ebay", core.MarketplaceCategory, "20081"); err != nil {
		t.Fatalf("SetMarketplaceValue (replace): %v", err)
	}
	got, _ = r.Offer(ctx, "o1")
	if got := got.MarketplaceValue("ebay", core.MarketplaceCategory); got != "20081" {
		t.Errorf("ebay category = %q after replacing, want %q", got, "20081")
	}

	// An empty value CLEARS it, so an operator can go back to the default
	// without a second verb.
	if err := r.SetMarketplaceValue(ctx, "o1", "ebay", core.MarketplaceCategory, ""); err != nil {
		t.Fatalf("SetMarketplaceValue (clear): %v", err)
	}
	got, _ = r.Offer(ctx, "o1")
	if _, ok := got.Marketplace[core.MarketplaceKey{Profile: "ebay", Field: core.MarketplaceCategory}]; ok {
		t.Errorf("the ebay category survived being cleared: %v", got.Marketplace)
	}
}

// TestOffersLoadEveryRowsCategoriesInOneQuery refuses the N+1 the list path
// would otherwise grow, mirroring how photos are already loaded.
func TestOffersLoadEveryRowsCategoriesInOneQuery(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	for i, id := range []string{"o1", "o2", "o3"} {
		o := newOffer(id, "WH000000"+itoaTest(i+1), "Item "+id, baseTime.Add(time.Duration(i)*time.Minute))
		if err := r.CreateOffer(ctx, o); err != nil {
			t.Fatalf("CreateOffer %s: %v", id, err)
		}
	}
	if err := r.SetMarketplaceValue(ctx, "o1", "ebay", core.MarketplaceCategory, "11450"); err != nil {
		t.Fatalf("SetMarketplaceValue: %v", err)
	}
	if err := r.SetMarketplaceValue(ctx, "o3", "ebay", core.MarketplaceCategory, "20081"); err != nil {
		t.Fatalf("SetMarketplaceValue: %v", err)
	}

	got, err := r.Offers(ctx, core.OfferFilter{})
	if err != nil {
		t.Fatalf("Offers: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("Offers returned %d, want 3", len(got))
	}

	byID := map[string]core.Offer{}
	for _, o := range got {
		byID[o.ID] = o
	}
	if got := byID["o1"].MarketplaceValue("ebay", core.MarketplaceCategory); got != "11450" {
		t.Errorf("o1 ebay = %q, want %q", got, "11450")
	}
	if got := byID["o3"].MarketplaceValue("ebay", core.MarketplaceCategory); got != "20081" {
		t.Errorf("o3 ebay = %q, want %q", got, "20081")
	}
	// The offer in the middle has none, and must not inherit a neighbour's — the
	// failure a per-row loop or a bad join produces.
	if len(byID["o2"].Marketplace) != 0 {
		t.Errorf("o2 has marketplace values %v, want none", byID["o2"].Marketplace)
	}
}

// TestDeletingAnOfferCascadesItsCategories proves the row does not outlive the
// offer it describes.
//
// It asserts behaviourally rather than by querying the table: an orphaned row
// would be picked up by the NEXT offer to be created with the same id, which is
// the consequence that actually matters and the one a foreign key without
// CASCADE would produce.
func TestDeletingAnOfferCascadesItsCategories(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	if err := r.CreateOffer(ctx, newOffer("o1", "WH0000001", "First", baseTime)); err != nil {
		t.Fatalf("CreateOffer: %v", err)
	}
	if err := r.SetMarketplaceValue(ctx, "o1", "ebay", core.MarketplaceCategory, "11450"); err != nil {
		t.Fatalf("SetMarketplaceValue: %v", err)
	}
	if err := r.DeleteOffer(ctx, "o1"); err != nil {
		t.Fatalf("DeleteOffer: %v", err)
	}

	if err := r.CreateOffer(ctx, newOffer("o1", "WH0000002", "Second", baseTime)); err != nil {
		t.Fatalf("CreateOffer (reuse): %v", err)
	}
	got, err := r.Offer(ctx, "o1")
	if err != nil {
		t.Fatalf("Offer: %v", err)
	}
	if len(got.Marketplace) != 0 {
		t.Errorf("a new offer reusing a deleted id inherited %v — the category row "+
			"outlived the offer it described", got.Marketplace)
	}
}

// itoaTest is a local digit helper so the table above reads as data.
func itoaTest(n int) string { return string(rune('0' + n)) }
