package fx

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
)

// testFetchedAt is a fixed retrieval instant, at second precision so that a
// round-trip through the TEXT column can be compared exactly.
var testFetchedAt = time.Date(2026, 9, 4, 16, 30, 0, 0, time.UTC)

// rate builds a core.Rate for a test.
func rate(asOf, quote, value string) core.Rate {
	return core.Rate{AsOf: asOf, Quote: quote, Rate: value, FetchedAt: testFetchedAt}
}

// save stores rates, failing the test if the write does not succeed.
func save(t *testing.T, r *Repo, rates ...core.Rate) {
	t.Helper()
	if err := r.SaveRates(context.Background(), rates); err != nil {
		t.Fatalf("SaveRates: %v", err)
	}
}

// TestRateOnFallsBackToTheMostRecentEarlierDay is the reason RateOn exists in
// this shape: 2026-09-04 is a Friday and 2026-09-06 is the Sunday after it, and
// the ECB publishes nothing in between.
func TestRateOnFallsBackToTheMostRecentEarlierDay(t *testing.T) {
	repo, _ := newTestRepo(t)
	save(t, repo, rate("2026-09-04", "USD", "1.1712"))

	got, err := repo.RateOn(context.Background(), "2026-09-06", "USD")
	if err != nil {
		t.Fatalf("RateOn(Sunday): %v", err)
	}
	if got.AsOf != "2026-09-04" {
		t.Errorf("AsOf = %q, want Friday's %q", got.AsOf, "2026-09-04")
	}
	if got.Rate != "1.1712" {
		t.Errorf("Rate = %q, want %q", got.Rate, "1.1712")
	}
	if got.Quote != "USD" {
		t.Errorf("Quote = %q, want USD", got.Quote)
	}
	if !got.FetchedAt.Equal(testFetchedAt) {
		t.Errorf("FetchedAt = %v, want %v", got.FetchedAt, testFetchedAt)
	}
}

func TestRateOnPrefersAnExactDay(t *testing.T) {
	repo, _ := newTestRepo(t)
	save(t, repo,
		rate("2026-09-04", "USD", "1.1712"),
		rate("2026-09-07", "USD", "1.1801"),
	)

	got, err := repo.RateOn(context.Background(), "2026-09-07", "USD")
	if err != nil {
		t.Fatalf("RateOn: %v", err)
	}
	if got.AsOf != "2026-09-07" || got.Rate != "1.1801" {
		t.Errorf("got %s/%s, want 2026-09-07/1.1801", got.AsOf, got.Rate)
	}
}

// TestRateOnNeverReachesForward guards the one-sided bound: a sale cannot have
// settled at a rate published after it happened.
func TestRateOnNeverReachesForward(t *testing.T) {
	repo, _ := newTestRepo(t)
	save(t, repo,
		rate("2026-09-04", "USD", "1.1712"),
		rate("2026-09-07", "USD", "1.1801"),
	)

	got, err := repo.RateOn(context.Background(), "2026-09-06", "USD")
	if err != nil {
		t.Fatalf("RateOn: %v", err)
	}
	if got.AsOf != "2026-09-04" || got.Rate != "1.1712" {
		t.Errorf("got %s/%s, want the earlier 2026-09-04/1.1712", got.AsOf, got.Rate)
	}
}

func TestRateOnIgnoresOtherCurrencies(t *testing.T) {
	repo, _ := newTestRepo(t)
	save(t, repo,
		rate("2026-09-04", "USD", "1.1712"),
		rate("2026-09-05", "GBP", "0.86720"),
	)

	got, err := repo.RateOn(context.Background(), "2026-09-06", "USD")
	if err != nil {
		t.Fatalf("RateOn: %v", err)
	}
	if got.AsOf != "2026-09-04" || got.Quote != "USD" || got.Rate != "1.1712" {
		t.Errorf("got %s %s/%s, want 2026-09-04 USD/1.1712", got.AsOf, got.Quote, got.Rate)
	}
}

func TestRateOnWithoutAnEarlierRateIsNotFound(t *testing.T) {
	repo, _ := newTestRepo(t)
	save(t, repo, rate("2026-09-07", "USD", "1.1801"))

	_, err := repo.RateOn(context.Background(), "2026-09-04", "USD")
	if !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("error = %v, want one wrapping core.ErrNotFound", err)
	}
}

func TestSaveRatesUpsertsInsteadOfDuplicating(t *testing.T) {
	repo, db := newTestRepo(t)
	later := testFetchedAt.Add(2 * time.Hour)

	save(t, repo, rate("2026-09-04", "USD", "1.1712"))
	save(t, repo, core.Rate{
		AsOf: "2026-09-04", Quote: "USD", Rate: "1.1750", FetchedAt: later,
	})

	if n := countRates(t, db); n != 1 {
		t.Fatalf("fx_rates holds %d rows, want 1", n)
	}
	got, err := repo.RateOn(context.Background(), "2026-09-04", "USD")
	if err != nil {
		t.Fatalf("RateOn: %v", err)
	}
	if got.Rate != "1.1750" {
		t.Errorf("Rate = %q, want the re-fetched %q", got.Rate, "1.1750")
	}
	if !got.FetchedAt.Equal(later) {
		t.Errorf("FetchedAt = %v, want the re-fetched %v", got.FetchedAt, later)
	}
}

func TestSaveRatesWritesEveryRateInTheBatch(t *testing.T) {
	repo, db := newTestRepo(t)
	save(t, repo,
		rate("2026-09-04", "USD", "1.1712"),
		rate("2026-09-04", "JPY", "172.53"),
		rate("2026-09-04", "GBP", "0.86720"),
	)

	if n := countRates(t, db); n != 3 {
		t.Fatalf("fx_rates holds %d rows, want 3", n)
	}
	got, err := repo.RateOn(context.Background(), "2026-09-04", "GBP")
	if err != nil {
		t.Fatalf("RateOn(GBP): %v", err)
	}
	// The trailing zero must survive storage as well as parsing.
	if got.Rate != "0.86720" {
		t.Errorf("GBP rate = %q, want the verbatim %q", got.Rate, "0.86720")
	}
}

func TestSaveRatesWithNothingToSaveIsANoOp(t *testing.T) {
	repo, db := newTestRepo(t)

	if err := repo.SaveRates(context.Background(), nil); err != nil {
		t.Fatalf("SaveRates(nil): %v", err)
	}
	if n := countRates(t, db); n != 0 {
		t.Fatalf("fx_rates holds %d rows, want 0", n)
	}
}

func TestLatestRatesReturnsTheNewestRowPerQuote(t *testing.T) {
	repo, _ := newTestRepo(t)
	save(t, repo,
		rate("2026-09-04", "USD", "1.1712"),
		rate("2026-09-07", "USD", "1.1801"),
		rate("2026-09-04", "GBP", "0.86720"),
	)

	got, err := repo.LatestRates(context.Background())
	if err != nil {
		t.Fatalf("LatestRates: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d rates, want 2 (one per currency)", len(got))
	}
	// Ordered by currency code, so GBP comes first.
	if got[0].Quote != "GBP" || got[0].AsOf != "2026-09-04" || got[0].Rate != "0.86720" {
		t.Errorf("got[0] = %s %s/%s, want GBP 2026-09-04/0.86720",
			got[0].Quote, got[0].AsOf, got[0].Rate)
	}
	if got[1].Quote != "USD" || got[1].AsOf != "2026-09-07" || got[1].Rate != "1.1801" {
		t.Errorf("got[1] = %s %s/%s, want USD 2026-09-07/1.1801",
			got[1].Quote, got[1].AsOf, got[1].Rate)
	}
}

func TestLatestRatesOnAnEmptyTableReturnsNothing(t *testing.T) {
	repo, _ := newTestRepo(t)

	got, err := repo.LatestRates(context.Background())
	if err != nil {
		t.Fatalf("LatestRates: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("got %d rates, want 0", len(got))
	}
}
