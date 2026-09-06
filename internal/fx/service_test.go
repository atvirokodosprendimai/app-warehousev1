package fx

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
)

// countingServer serves body with status and reports each hit on the returned
// channel, so a test can wait for a background refresh instead of sleeping.
func countingServer(t *testing.T, status int, body string) (*httptest.Server, <-chan struct{}, *atomic.Int64) {
	t.Helper()
	hits := make(chan struct{}, 8)
	var count atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count.Add(1)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
		select {
		case hits <- struct{}{}:
		default:
		}
	}))
	t.Cleanup(srv.Close)
	return srv, hits, &count
}

func TestRefreshStoresEveryRateInTheFeed(t *testing.T) {
	repo, db := newTestRepo(t)
	srv, _, _ := countingServer(t, http.StatusOK, ecbFixture)
	svc := NewService(repo, NewClient(srv.Client(), srv.URL))

	n, err := svc.Refresh(context.Background())
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if n != fixtureCurrencies {
		t.Errorf("stored %d rates, want %d", n, fixtureCurrencies)
	}
	if got := countRates(t, db); got != fixtureCurrencies {
		t.Errorf("fx_rates holds %d rows, want %d", got, fixtureCurrencies)
	}

	// The feed's Friday rate must be reachable from the Sunday after it.
	got, err := repo.RateOn(context.Background(), "2026-09-06", "USD")
	if err != nil {
		t.Fatalf("RateOn: %v", err)
	}
	if got.AsOf != fixtureDay || got.Rate != "1.1712" {
		t.Errorf("got %s/%s, want %s/1.1712", got.AsOf, got.Rate, fixtureDay)
	}
}

// TestRefreshTwiceLeavesOneRowPerCurrency is the idempotence the upsert exists
// for: the daily job may well run more than once against one publication.
func TestRefreshTwiceLeavesOneRowPerCurrency(t *testing.T) {
	repo, db := newTestRepo(t)
	srv, _, hits := countingServer(t, http.StatusOK, ecbFixture)
	svc := NewService(repo, NewClient(srv.Client(), srv.URL))

	for i := range 2 {
		if _, err := svc.Refresh(context.Background()); err != nil {
			t.Fatalf("Refresh %d: %v", i+1, err)
		}
	}

	if hits.Load() != 2 {
		t.Errorf("server hit %d times, want 2", hits.Load())
	}
	if got := countRates(t, db); got != fixtureCurrencies {
		t.Errorf("fx_rates holds %d rows after two refreshes, want %d",
			got, fixtureCurrencies)
	}
}

func TestRefreshReportsAFetchFailure(t *testing.T) {
	repo, db := newTestRepo(t)
	srv, _, _ := countingServer(t, http.StatusBadGateway, "nope")
	svc := NewService(repo, NewClient(srv.Client(), srv.URL))

	n, err := svc.Refresh(context.Background())
	if err == nil {
		t.Fatalf("Refresh stored %d rates, want an error", n)
	}
	if n != 0 {
		t.Errorf("count = %d, want 0 on failure", n)
	}
	if got := countRates(t, db); got != 0 {
		t.Errorf("fx_rates holds %d rows, want 0", got)
	}
}

// TestConvertUsesTheRateInForceOnTheSaleDate is the whole reason rates are
// stored per day: the Monday rate exists in the table and must not be the one a
// Sunday sale is booked at.
func TestConvertUsesTheRateInForceOnTheSaleDate(t *testing.T) {
	repo, _ := newTestRepo(t)
	save(t, repo,
		rate("2026-09-04", "USD", "1.1712"),
		rate("2026-09-07", "USD", "1.5000"),
	)
	svc := NewService(repo, NewClient(nil, "http://unused.invalid"))

	sunday := time.Date(2026, 9, 6, 14, 0, 0, 0, time.UTC)

	// 100.00 USD at 1 EUR = 1.1712 USD is 85.38251... EUR, half-up to 85.38.
	got, err := svc.Convert(context.Background(),
		core.Money{Minor: 10000, Currency: "USD"}, "EUR", sunday)
	if err != nil {
		t.Fatalf("Convert USD to EUR: %v", err)
	}
	if got.Minor != 8538 || got.Currency != "EUR" {
		t.Errorf("got %d %s, want 8538 EUR (Friday's rate)", got.Minor, got.Currency)
	}

	// And the other direction: 100.00 EUR is exactly 117.12 USD.
	back, err := svc.Convert(context.Background(),
		core.Money{Minor: 10000, Currency: "EUR"}, "USD", sunday)
	if err != nil {
		t.Fatalf("Convert EUR to USD: %v", err)
	}
	if back.Minor != 11712 || back.Currency != "USD" {
		t.Errorf("got %d %s, want 11712 USD", back.Minor, back.Currency)
	}
}

func TestConvertTakesTheDayInTheGivenLocation(t *testing.T) {
	repo, _ := newTestRepo(t)
	save(t, repo,
		rate("2026-09-04", "USD", "1.1712"),
		rate("2026-09-06", "USD", "1.5000"),
	)
	svc := NewService(repo, NewClient(nil, "http://unused.invalid"))

	// 00:30 on Sunday in Vilnius is still Saturday in UTC. The sale happened on
	// Sunday for the person who made it, so Sunday's rate is the right one.
	vilnius := time.FixedZone("EET", 3*60*60)
	justAfterMidnight := time.Date(2026, 9, 6, 0, 30, 0, 0, vilnius)

	got, err := svc.Convert(context.Background(),
		core.Money{Minor: 10000, Currency: "EUR"}, "USD", justAfterMidnight)
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if got.Minor != 15000 {
		t.Errorf("got %d, want 15000 (Sunday's 1.5000, not Saturday's)", got.Minor)
	}
}

func TestConvertOfTheSameCurrencyNeedsNoRate(t *testing.T) {
	repo, _ := newTestRepo(t) // deliberately empty
	svc := NewService(repo, NewClient(nil, "http://unused.invalid"))

	in := core.Money{Minor: 1234, Currency: "EUR"}
	got, err := svc.Convert(context.Background(), in, "EUR", time.Now())
	if err != nil {
		t.Fatalf("Convert: %v", err)
	}
	if got != in {
		t.Errorf("got %v, want %v unchanged", got, in)
	}
}

func TestConvertRefusesACrossRate(t *testing.T) {
	repo, _ := newTestRepo(t)
	save(t, repo,
		rate("2026-09-04", "USD", "1.1712"),
		rate("2026-09-04", "GBP", "0.86720"),
	)
	svc := NewService(repo, NewClient(nil, "http://unused.invalid"))

	day := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	_, err := svc.Convert(context.Background(),
		core.Money{Minor: 10000, Currency: "USD"}, "GBP", day)
	if !errors.Is(err, core.ErrBadMoney) {
		t.Fatalf("error = %v, want one wrapping core.ErrBadMoney", err)
	}
}

func TestConvertReportsAMissingRate(t *testing.T) {
	repo, _ := newTestRepo(t)
	svc := NewService(repo, NewClient(nil, "http://unused.invalid"))

	_, err := svc.Convert(context.Background(),
		core.Money{Minor: 10000, Currency: "USD"}, "EUR",
		time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC))
	if !errors.Is(err, core.ErrNotFound) {
		t.Fatalf("error = %v, want one wrapping core.ErrNotFound", err)
	}
}

func TestStartDailyRefreshesImmediatelyAndStopsPromptly(t *testing.T) {
	quietLogs(t)
	repo, db := newTestRepo(t)
	srv, _, _ := countingServer(t, http.StatusOK, ecbFixture)
	svc := NewService(repo, NewClient(srv.Client(), srv.URL))

	stop := svc.StartDaily(context.Background())
	// An HTTP hit only says the refresh STARTED; wait for the rows to land.
	waitForRates(t, db, fixtureCurrencies)
	stopWithin(t, stop, 2*time.Second)

	got, err := repo.RateOn(context.Background(), "2026-09-06", "USD")
	if err != nil {
		t.Fatalf("RateOn after StartDaily: %v", err)
	}
	if got.AsOf != fixtureDay || got.Rate != "1.1712" {
		t.Errorf("got %s/%s, want %s/1.1712", got.AsOf, got.Rate, fixtureDay)
	}
}

// TestStartDailySurvivesAFailingFetch is the "the app still serves when the ECB
// is down" requirement: the refresher logs and keeps running rather than dying.
func TestStartDailySurvivesAFailingFetch(t *testing.T) {
	quietLogs(t)
	repo, db := newTestRepo(t)
	srv, hits, _ := countingServer(t, http.StatusInternalServerError, "boom")
	svc := NewService(repo, NewClient(srv.Client(), srv.URL))

	stop := svc.StartDaily(context.Background())
	waitForHit(t, hits)
	stopWithin(t, stop, 2*time.Second)

	if got := countRates(t, db); got != 0 {
		t.Errorf("fx_rates holds %d rows, want 0", got)
	}
}

func TestStartDailyExitsWhenTheContextIsCancelled(t *testing.T) {
	quietLogs(t)
	repo, _ := newTestRepo(t)
	srv, hits, _ := countingServer(t, http.StatusOK, ecbFixture)
	svc := NewService(repo, NewClient(srv.Client(), srv.URL))

	ctx, cancel := context.WithCancel(context.Background())
	stop := svc.StartDaily(ctx)
	waitForHit(t, hits)

	cancel()
	// stop must still return once the goroutine has noticed the cancellation.
	stopWithin(t, stop, 2*time.Second)
}

// waitForRates blocks until fx_rates holds want rows. It is how a test observes
// a background refresh FINISHING; the server hit only says one started.
func waitForRates(t *testing.T, db *sql.DB, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if countRates(t, db) == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("fx_rates never reached %d rows", want)
}

// waitForHit blocks until the test server has served one request.
func waitForHit(t *testing.T, hits <-chan struct{}) {
	t.Helper()
	select {
	case <-hits:
	case <-time.After(5 * time.Second):
		t.Fatal("the refresher never called the feed")
	}
}

// stopWithin asserts that stop returns inside d, which is what "exits promptly
// on cancellation" means in practice.
func stopWithin(t *testing.T, stop func(), d time.Duration) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(d):
		t.Fatalf("stop did not return within %v", d)
	}
}
