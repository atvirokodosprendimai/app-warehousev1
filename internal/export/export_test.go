package export

import (
	"bytes"
	"encoding/csv"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
)

// ownerMinor is the owner price every test offer carries: digits that appear
// nowhere else in a rendered file, so that searching the output for them is a
// meaningful leak check rather than a coincidence.
const (
	ownerMinor   = 3077    // renders as "30.77"
	ownerDecimal = "30.77" // what a leak would actually look like
)

// testOffer returns an offer that every profile will export, carrying n photos.
func testOffer(sku string, n int) core.Offer {
	o := core.Offer{
		ID:          "id-" + sku,
		SKU:         sku,
		Title:       "Vintage Brass Lamp",
		Description: "A brass lamp, rewired and tested.",
		Condition:   "used - good",
		Status:      core.StatusListed,
		Quantity:    2,
		Shop:        core.Money{Minor: 12500, Currency: "EUR"},
		Owner:       core.Money{Minor: ownerMinor, Currency: "EUR"},
		LocationID:  "loc-1",
	}
	for i := range n {
		o.Photos = append(o.Photos, core.Photo{
			ID:          fmt.Sprintf("photo-%s-%d", strings.ToLower(sku), i),
			OfferID:     o.ID,
			Position:    i,
			ContentType: "image/jpeg",
		})
	}
	return o
}

// testOptions returns options every profile accepts.
func testOptions() Options {
	return Options{
		BaseURL:     "https://warehouse.example.com",
		Currency:    "EUR",
		Category:    "20081",
		ConditionID: "3000",
		Location:    "Kaunas",
	}
}

// render writes offers and fails the test if the exporter refuses them.
func render(t *testing.T, e Exporter, offers []core.Offer, opt Options) string {
	t.Helper()
	var buf bytes.Buffer
	if err := e.Write(&buf, offers, opt); err != nil {
		t.Fatalf("%s.Write: unexpected error: %v", e.Name(), err)
	}
	return buf.String()
}

// records parses rendered CSV. csv.Reader also enforces a constant field count,
// so a profile that emits a short row fails here rather than at the marketplace.
func records(t *testing.T, out string) [][]string {
	t.Helper()
	recs, err := csv.NewReader(strings.NewReader(out)).ReadAll()
	if err != nil {
		t.Fatalf("output is not valid CSV: %v\n%s", err, out)
	}
	return recs
}

// headerLine returns the rendered header exactly as a marketplace reads it.
func headerLine(out string) string {
	line, _, _ := strings.Cut(out, "\n")
	return line
}

// exporters returns every registered profile, for tests that must hold for all.
func exporters(t *testing.T) []Exporter {
	t.Helper()
	out := make([]Exporter, 0, len(Names()))
	for _, name := range Names() {
		e, err := For(name)
		if err != nil {
			t.Fatalf("For(%q): %v", name, err)
		}
		out = append(out, e)
	}
	return out
}

func TestForResolvesEveryRegisteredProfile(t *testing.T) {
	tests := []struct {
		name string
		give string
		want string
	}{
		{"shopify", "shopify", "shopify"},
		{"ebay", "ebay", "ebay"},
		{"uppercase", "SHOPIFY", "shopify"},
		{"padded", "  ebay  ", "ebay"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e, err := For(tt.give)
			if err != nil {
				t.Fatalf("For(%q): %v", tt.give, err)
			}
			if got := e.Name(); got != tt.want {
				t.Errorf("For(%q).Name() = %q, want %q", tt.give, got, tt.want)
			}
		})
	}
}

func TestForRejectsUnknownProfile(t *testing.T) {
	_, err := For("amazon")
	if !errors.Is(err, ErrUnknownProfile) {
		t.Fatalf("For(\"amazon\") error = %v, want ErrUnknownProfile", err)
	}
	// The message has to say what IS available, or the caller's next guess is
	// as blind as the first.
	for _, want := range []string{"amazon", "ebay", "shopify"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

func TestNamesListsEveryProfileSorted(t *testing.T) {
	want := []string{"ebay", "shopify"}
	if got := Names(); !reflect.DeepEqual(got, want) {
		t.Errorf("Names() = %v, want %v", got, want)
	}
}

func TestWriteRejectsBaseURLAMarketplaceCannotFetch(t *testing.T) {
	bases := []struct {
		name string
		give string
	}{
		{"empty", ""},
		{"relative path", "/p"},
		{"host only", "warehouse.example.com"},
		{"scheme only", "https://"},
		{"wrong scheme", "ftp://warehouse.example.com"},
		{"protocol relative", "//warehouse.example.com"},
	}
	for _, e := range exporters(t) {
		for _, b := range bases {
			t.Run(e.Name()+"/"+b.name, func(t *testing.T) {
				opt := testOptions()
				opt.BaseURL = b.give
				var buf bytes.Buffer
				err := e.Write(&buf, []core.Offer{testOffer("LAMP-01", 1)}, opt)
				if !errors.Is(err, ErrOptions) {
					t.Fatalf("Write with base %q: error = %v, want ErrOptions", b.give, err)
				}
				if buf.Len() != 0 {
					t.Errorf("refused export still wrote %d bytes: %q", buf.Len(), buf.String())
				}
			})
		}
	}
}

func TestWriteAcceptsAbsoluteBaseURL(t *testing.T) {
	for _, e := range exporters(t) {
		for _, base := range []string{"https://warehouse.example.com", "http://warehouse.example.com"} {
			t.Run(e.Name()+"/"+base, func(t *testing.T) {
				opt := testOptions()
				opt.BaseURL = base
				out := render(t, e, []core.Offer{testOffer("LAMP-01", 1)}, opt)
				want := base + "/p/photo-lamp-01-0.jpg"
				if !strings.Contains(out, want) {
					t.Errorf("output does not contain %q:\n%s", want, out)
				}
			})
		}
	}
}

func TestWriteRejectsCurrencyItCannotRender(t *testing.T) {
	for _, e := range exporters(t) {
		for _, give := range []string{"", "   ", "XYZ"} {
			t.Run(fmt.Sprintf("%s/%q", e.Name(), give), func(t *testing.T) {
				opt := testOptions()
				opt.Currency = give
				var buf bytes.Buffer
				err := e.Write(&buf, []core.Offer{testOffer("LAMP-01", 1)}, opt)
				if !errors.Is(err, ErrOptions) {
					t.Fatalf("Write with currency %q: error = %v, want ErrOptions", give, err)
				}
				if buf.Len() != 0 {
					t.Errorf("refused export still wrote %d bytes", buf.Len())
				}
			})
		}
	}
}

func TestWriteAcceptsLowercaseCurrency(t *testing.T) {
	// An operator typing "eur" means EUR; a mismatch here would refuse a whole
	// correctly-priced catalogue.
	for _, e := range exporters(t) {
		t.Run(e.Name(), func(t *testing.T) {
			opt := testOptions()
			opt.Currency = "eur"
			out := render(t, e, []core.Offer{testOffer("LAMP-01", 1)}, opt)
			if n := len(records(t, out)); n != 2 {
				t.Errorf("got %d records, want header + 1 row", n)
			}
		})
	}
}

func TestWriteSkipsOffersThatAreNotExportable(t *testing.T) {
	// The unsellable offers deliberately have no price and no photos: if they
	// reached validation instead of the status filter, the export would fail
	// rather than quietly leave them out.
	unsellable := []core.Status{core.StatusDraft, core.StatusSold, core.StatusArchived}
	offers := []core.Offer{testOffer("LAMP-01", 1)}
	for _, st := range unsellable {
		o := testOffer("SKIP-"+string(st), 0)
		o.Status = st
		o.Shop = core.Money{}
		offers = append(offers, o)
	}

	for _, e := range exporters(t) {
		t.Run(e.Name(), func(t *testing.T) {
			out := render(t, e, offers, testOptions())
			if n := len(records(t, out)); n != 2 {
				t.Fatalf("got %d records, want header + 1 row:\n%s", n, out)
			}
			for _, st := range unsellable {
				if sku := "SKIP-" + string(st); strings.Contains(out, sku) {
					t.Errorf("%s offer %q reached the export:\n%s", st, sku, out)
				}
			}
		})
	}
}

func TestWriteEmitsHeaderWhenNothingIsExportable(t *testing.T) {
	// A header with no rows says "nothing is listed"; an empty file is
	// indistinguishable from a failed run.
	draft := testOffer("DRAFT-1", 0)
	draft.Status = core.StatusDraft
	draft.Shop = core.Money{}

	for _, e := range exporters(t) {
		t.Run(e.Name(), func(t *testing.T) {
			out := render(t, e, []core.Offer{draft}, testOptions())
			recs := records(t, out)
			if len(recs) != 1 {
				t.Fatalf("got %d records, want just the header:\n%s", len(recs), out)
			}
			if len(recs[0]) == 0 {
				t.Error("header row is empty")
			}
		})
	}
}

func TestWriteRefusesOffersItCannotRenderAndNamesThem(t *testing.T) {
	noPhotos := testOffer("NOPIC-1", 0)
	noPrice := testOffer("NOPRICE-1", 1)
	noPrice.Shop = core.Money{}
	foreign := testOffer("USD-1", 1)
	foreign.Shop = core.Money{Minor: 9900, Currency: "USD"}

	tests := []struct {
		name     string
		offers   []core.Offer
		wantSKUs []string
		wantText string
	}{
		{"no photos", []core.Offer{noPhotos}, []string{"NOPIC-1"}, "photos"},
		{"no shop price", []core.Offer{noPrice}, []string{"NOPRICE-1"}, "shop price"},
		{"foreign currency", []core.Offer{foreign}, []string{"USD-1"}, "USD"},
		{
			"every offender is named",
			[]core.Offer{noPhotos, testOffer("GOOD-1", 1), noPrice, foreign},
			[]string{"NOPIC-1", "NOPRICE-1", "USD-1"},
			"",
		},
	}

	for _, e := range exporters(t) {
		for _, tt := range tests {
			t.Run(e.Name()+"/"+tt.name, func(t *testing.T) {
				var buf bytes.Buffer
				err := e.Write(&buf, tt.offers, testOptions())
				if !errors.Is(err, ErrIncomplete) {
					t.Fatalf("Write error = %v, want ErrIncomplete", err)
				}
				for _, sku := range tt.wantSKUs {
					if !strings.Contains(err.Error(), sku) {
						t.Errorf("error does not name %q:\n%v", sku, err)
					}
				}
				if tt.wantText != "" && !strings.Contains(err.Error(), tt.wantText) {
					t.Errorf("error does not explain %q:\n%v", tt.wantText, err)
				}
				// A refused export must leave nothing behind: half a feed is
				// read as a whole one by whatever uploads it.
				if buf.Len() != 0 {
					t.Errorf("refused export wrote %d bytes: %q", buf.Len(), buf.String())
				}
			})
		}
	}
}

func TestWriteRefusesRatherThanConvertingCurrency(t *testing.T) {
	// Documented behaviour: a foreign price is an error, never a conversion,
	// because converting needs a dated rate this package does not have.
	foreign := testOffer("USD-1", 1)
	foreign.Shop = core.Money{Minor: 9900, Currency: "USD"}

	for _, e := range exporters(t) {
		t.Run(e.Name(), func(t *testing.T) {
			var buf bytes.Buffer
			err := e.Write(&buf, []core.Offer{foreign}, testOptions())
			if !errors.Is(err, ErrIncomplete) {
				t.Fatalf("Write error = %v, want ErrIncomplete", err)
			}
			for _, want := range []string{"USD", "EUR"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error does not name currency %q:\n%v", want, err)
				}
			}
			if strings.Contains(buf.String(), "99.00") {
				t.Error("the foreign price was rendered anyway")
			}
		})
	}
}

func TestExportersAreSafeForConcurrentUse(t *testing.T) {
	// For hands every caller the same instance, so the claim that they are
	// stateless is worth a race detector rather than a comment.
	offers := []core.Offer{testOffer("LAMP-01", 3), testOffer("DESK-02", 1)}
	opt := testOptions()

	for _, e := range exporters(t) {
		t.Run(e.Name(), func(t *testing.T) {
			want := render(t, e, offers, opt)
			outs := make([]string, 8)
			var wg sync.WaitGroup
			for i := range outs {
				wg.Add(1)
				go func() {
					defer wg.Done()
					shared, err := For(e.Name())
					if err != nil {
						return
					}
					var buf bytes.Buffer
					if err := shared.Write(&buf, offers, opt); err != nil {
						return
					}
					outs[i] = buf.String()
				}()
			}
			wg.Wait()
			for i, got := range outs {
				if got != want {
					t.Fatalf("goroutine %d rendered a different file:\n%s", i, got)
				}
			}
		})
	}
}

// TestTheRegistryMatchesTheProfilesCoreKnows refuses the drift ADR-016 creates.
//
// The profile names live in TWO places on purpose: this package registers the
// exporters, and core.KnownExportProfiles is what internal/offer validates a
// per-offer category against — and no package here imports a sibling, so the
// offer aggregate cannot ask this one.
//
// Two sources of truth is a drift risk, and this is the check that makes the
// drift loud instead of silent. Without it, adding a profile here and forgetting
// core would mean every category stored against the new marketplace is refused
// as a typo; adding it to core and forgetting here would mean categories stored
// against a marketplace that cannot be exported.
func TestTheRegistryMatchesTheProfilesCoreKnows(t *testing.T) {
	registered := Names()
	known := core.KnownExportProfiles()

	sort.Strings(registered)
	sort.Strings(known)

	if len(registered) != len(known) {
		t.Fatalf("export registers %v; core.KnownExportProfiles is %v — add the profile in "+
			"BOTH places, or a category stored against it is either refused as a typo or "+
			"stored for a marketplace nothing can export", registered, known)
	}
	for i := range registered {
		if registered[i] != known[i] {
			t.Errorf("profile %d: export has %q, core has %q", i, registered[i], known[i])
		}
	}
}

// TestNoRegisteredProfileEverExportsTheOwnerPrice is ADR-003's rule asked of the
// REGISTRY rather than of the two profiles somebody remembered to write a test
// for.
//
// ⚠ THIS CLOSES THE ONE GAP ADR-003 NAMED IN ITS OWN RISKS. `Shopify` and `EBay`
// each have a hand-written owner-price test, and a third profile would have
// neither until somebody thought to add one — which is exactly the moment nobody
// is thinking about the owner price. Written this way a new exporter is covered
// by existing in the registry: the loop finds it, and the day it leaks the
// business's private cost into a public catalogue this test fails without anyone
// having predicted it.
//
// ★ IT ASSERTS THE SHOP PRICE IS PRESENT TOO, AND THAT IS NOT DECORATION. A
// negative assertion — "this string does not appear" — is satisfied just as well
// by an empty file, a crashed exporter or a typo'd fixture as by the rule
// holding. Without the positive control this test would report ok on a run where
// nothing was exported at all.
func TestNoRegisteredProfileEverExportsTheOwnerPrice(t *testing.T) {
	if len(registry) == 0 {
		t.Fatal("the registry is empty, so this test asserts nothing about anything")
	}

	for _, e := range registry {
		t.Run(e.Name(), func(t *testing.T) {
			offer := testOffer("LAMP-01", 3)
			out := render(t, e, []core.Offer{offer}, testOptions())

			// The positive control first: if the shop price is missing, the
			// export did not happen and every check below is vacuous.
			if !strings.Contains(out, "125.00") {
				t.Fatalf("the shop price is absent from the %s export, so this "+
					"profile rendered nothing and the leak checks below would "+
					"pass over an empty file:\n%s", e.Name(), out)
			}

			for _, leak := range []string{ownerDecimal, "3077"} {
				if strings.Contains(out, leak) {
					t.Errorf("the owner price %q reaches the %s catalogue. That "+
						"figure is what the person whose warehouse this stock sits "+
						"in wants to be paid — publishing it hands a marketplace, "+
						"and every competitor reading it, the business's own "+
						"cost:\n%s", leak, e.Name(), out)
				}
			}

			// And cell by cell, in case a future column renders it in a shape the
			// substring check would not recognise.
			for r, row := range records(t, out) {
				for c, cell := range row {
					if cell == ownerDecimal || cell == "3077" {
						t.Errorf("the %s export carries the owner price alone in "+
							"row %d, column %d", e.Name(), r, c)
					}
				}
			}
		})
	}
}
