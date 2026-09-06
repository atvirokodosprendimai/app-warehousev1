package offer

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
)

// TestSearchFindsAnOfferByTheNumberOnItsLabel is the half of the reference
// change that makes the short format worth having.
//
// The old format was found by pasting it. This one is found by TYPING it, from a
// box, in a warehouse — which means without the WH prefix, without the zero
// padding, and in whatever case the keyboard was in.
func TestSearchFindsAnOfferByTheNumberOnItsLabel(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	mustCreate(t, r, newOffer("o1", core.FormatSKU(42), "Vintage brass desk lamp", baseTime))
	mustCreate(t, r, newOffer("o2", core.FormatSKU(43), "Oak dining chair", baseTime.Add(time.Minute)))

	for _, q := range []string{"WH0000042", "wh0000042", "WH42", "wh42", "42", " 42 "} {
		got, err := r.Offers(ctx, core.OfferFilter{Query: q})
		if err != nil {
			t.Fatalf("Offers(%q): %v", q, err)
		}
		if len(got) != 1 || got[0].ID != "o1" {
			t.Errorf("Offers(%q) = %s, want just o1 — somebody reading a label will "+
				"not reproduce the padding", q, fmt.Sprint(ids(got)))
		}
	}
}

// TestSearchStillFindsTextWhenTheQueryLooksLikeANumber guards the arm that ORs
// the two conditions: a numeric query must still match a description that
// mentions the number, not only the reference column.
func TestSearchStillFindsTextWhenTheQueryLooksLikeANumber(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	mustCreate(t, r, newOffer("byref", core.FormatSKU(7), "Chair", baseTime))
	byText := newOffer("bytext", core.FormatSKU(800), "Marantz 2226", baseTime.Add(time.Minute))
	byText.Description = "receiver, model 7 chassis"
	mustCreate(t, r, byText)

	got, err := r.Offers(ctx, core.OfferFilter{Query: "7"})
	if err != nil {
		t.Fatalf("Offers: %v", err)
	}
	found := map[string]bool{}
	for _, o := range got {
		found[o.ID] = true
	}
	if !found["byref"] {
		t.Error("the offer whose reference is 7 was not found")
	}
	if !found["bytext"] {
		t.Error("the offer whose description mentions 7 was not found; the reference " +
			"match replaced the text search instead of joining it")
	}
}

// TestSearchByTextIsUnaffected pins that an ordinary query still takes the
// full-text path, rather than being mistaken for a reference.
func TestSearchByTextIsUnaffected(t *testing.T) {
	r := newTestRepo(t)
	ctx := context.Background()

	mustCreate(t, r, newOffer("o1", core.FormatSKU(1), "Vintage brass desk lamp", baseTime))
	mustCreate(t, r, newOffer("o2", core.FormatSKU(2), "Oak dining chair", baseTime.Add(time.Minute)))

	got, err := r.Offers(ctx, core.OfferFilter{Query: "brass lamp"})
	if err != nil {
		t.Fatalf("Offers: %v", err)
	}
	if len(got) != 1 || got[0].ID != "o1" {
		t.Errorf("Offers(\"brass lamp\") = %s, want just o1", fmt.Sprint(ids(got)))
	}
}
