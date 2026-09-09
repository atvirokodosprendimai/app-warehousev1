package marketplace

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
	"github.com/atvirokodosprendimai/app-warehousev1/internal/store"
	"github.com/atvirokodosprendimai/app-warehousev1/migrations"
)

// newTestRepo opens a database and runs the REAL migrations (ADR-013). Nothing
// here builds its own copy of the schema; the two packages that once did drifted
// at the first opportunity.
func newTestRepo(t *testing.T) *Repo {
	t.Helper()

	db, err := store.Open(filepath.Join(t.TempDir(), "marketplace_test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := store.Migrate(db.Write, migrations.FS); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return NewRepo(db.Read, db.Write)
}

func option(field core.MarketplaceField, value, label string, pos int) core.MarketplaceOption {
	return core.MarketplaceOption{
		Profile: "ebay", Field: field, Value: value, Label: label, Position: pos,
	}
}

// TestAnOptionListRefusesADuplicateValue pins the constraint that stops a
// dropdown holding two entries nobody can tell apart.
//
// ⚠ A DOUBLE-SUBMIT PRODUCES EXACTLY THIS, so it is not a hypothetical. The
// refusal has to carry a name the interface can turn into a sentence, or the
// operator sees "Something went wrong" and adds it a third time.
func TestAnOptionListRefusesADuplicateValue(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	first, err := r.AddOption(ctx, option(core.MarketplaceCategory, "20081", "Antiques", 0))
	if err != nil {
		t.Fatalf("AddOption: %v", err)
	}
	if first.ID == "" {
		t.Error("AddOption returned no id, so the caller cannot remove what it just added")
	}

	// The same value again, even under a different label — the VALUE is what
	// reaches eBay, so two rows carrying it are two ways to say one thing.
	_, err = r.AddOption(ctx, option(core.MarketplaceCategory, "20081", "Antiques & Art", 1))
	if err == nil {
		t.Fatal("a duplicate value was accepted; the dropdown now holds two entries that " +
			"export identically and the operator cannot tell which is which")
	}
	if !errors.Is(err, ErrValueTaken) {
		t.Errorf("error %v does not wrap ErrValueTaken, so the interface cannot say what "+
			"went wrong and renders the generic message", err)
	}

	// The SAME value under a different FIELD is a different thing entirely and
	// must be allowed: a condition code and a category number can collide.
	if _, err := r.AddOption(ctx, option(core.MarketplaceCondition, "20081", "Odd", 0)); err != nil {
		t.Errorf("the same value on a different field was refused: %v — the constraint is "+
			"per profile AND field, not per value", err)
	}
}

// TestOptionsComeBackInPositionOrder pins a total order.
//
// Two options sharing a position is the ordinary state after adding several
// without reordering. Without the tie-breaks the same list renders in two
// different orders on two reads, which reads as a bug in the screen.
func TestOptionsComeBackInPositionOrder(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	for _, o := range []core.MarketplaceOption{
		option(core.MarketplaceCategory, "11450", "Clothing", 2),
		option(core.MarketplaceCategory, "20081", "Antiques", 1),
		// Two more sharing position 3, ordered by label after it.
		option(core.MarketplaceCategory, "9355", "Zebra", 3),
		option(core.MarketplaceCategory, "625", "Cameras", 3),
	} {
		if _, err := r.AddOption(ctx, o); err != nil {
			t.Fatalf("AddOption %s: %v", o.Value, err)
		}
	}

	got, err := r.Options(ctx, "ebay")
	if err != nil {
		t.Fatalf("Options: %v", err)
	}
	want := []string{"Antiques", "Clothing", "Cameras", "Zebra"}
	if len(got) != len(want) {
		t.Fatalf("got %d options, want %d", len(got), len(want))
	}
	for i, w := range want {
		if got[i].Label != w {
			t.Errorf("option %d = %q, want %q — order is position, then label", i, got[i].Label, w)
		}
	}

	// Profile scoping: another profile's list must not leak into this one.
	if _, err := r.AddOption(ctx, core.MarketplaceOption{
		Profile: "recar", Field: core.MarketplaceCategory, Value: "silniki", Label: "Silniki",
	}); err != nil {
		t.Fatalf("AddOption (recar): %v", err)
	}
	got, _ = r.Options(ctx, "ebay")
	if len(got) != len(want) {
		t.Errorf("ebay's list grew to %d after adding a recar option; the lists are per "+
			"profile because the taxonomies are", len(got))
	}
}

// TestAnUnusableOptionIsRefusedBeforeItIsStored checks that validation runs on
// the way in rather than on the way out.
//
// ⚠ A stored option that cannot be exported is worse than a refused one: the
// operator picks it, the offer looks configured, and the CSV carries a blank cell
// in a column the marketplace requires.
func TestAnUnusableOptionIsRefusedBeforeItIsStored(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	for _, tc := range []struct {
		name string
		give core.MarketplaceOption
	}{
		{"blank value", option(core.MarketplaceCategory, "   ", "Antiques", 0)},
		{"blank label", option(core.MarketplaceCategory, "20081", "", 0)},
		{"unknown field", option("colour", "20081", "Antiques", 0)},
		{"unknown profile", core.MarketplaceOption{
			Profile: "gumtree", Field: core.MarketplaceCategory, Value: "x", Label: "X"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := r.AddOption(ctx, tc.give); err == nil {
				t.Fatalf("%s was stored", tc.name)
			}
		})
	}

	// And nothing reached the table — the positive control, without which every
	// case above would pass over a repository that refuses everything.
	got, err := r.Options(ctx, "ebay")
	if err != nil {
		t.Fatalf("Options: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("the list holds %d option(s) after four refusals: %v", len(got), got)
	}
	if _, err := r.AddOption(ctx, option(core.MarketplaceCategory, "20081", "Antiques", 0)); err != nil {
		t.Fatalf("a well-formed option was refused too, so the checks above prove nothing "+
			"about validation: %v", err)
	}
}

// TestRemovingAnOptionTakesItOffTheMenuAndNothingElse pins the deliberate
// narrowness of removal.
func TestRemovingAnOptionTakesItOffTheMenuAndNothingElse(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	added, err := r.AddOption(ctx, option(core.MarketplaceCategory, "20081", "Antiques", 0))
	if err != nil {
		t.Fatalf("AddOption: %v", err)
	}
	if err := r.RemoveOption(ctx, added.ID); err != nil {
		t.Fatalf("RemoveOption: %v", err)
	}
	got, _ := r.Options(ctx, "ebay")
	if len(got) != 0 {
		t.Errorf("the option survived removal: %v", got)
	}

	// Removing it again is not an error: two people pressing the same button is
	// the ordinary double-submit, and the state they both wanted is the state.
	if err := r.RemoveOption(ctx, added.ID); err != nil {
		t.Errorf("removing an already-removed option failed: %v", err)
	}
}
