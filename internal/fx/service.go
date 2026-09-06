package fx

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
)

// refreshInterval is how often [Service.StartDaily] re-fetches. The ECB
// publishes once a working day at around 16:00 CET, so a daily poll is the
// natural cadence and a missed day is covered by RateOn's fallback anyway.
const refreshInterval = 24 * time.Hour

// Service keeps the rate table fed and converts money using it.
type Service struct {
	store  core.RateStore
	client *Client
}

// NewService returns a Service storing into store and fetching through client.
func NewService(store core.RateStore, client *Client) *Service {
	return &Service{store: store, client: client}
}

// Refresh fetches the current feed, stores it, and reports how many rates landed.
//
// It is safe to call repeatedly on the same day: [Repo.SaveRates] upserts, so a
// second call over the same publication updates the rows rather than adding a
// duplicate set.
func (s *Service) Refresh(ctx context.Context) (int, error) {
	rates, err := s.client.Fetch(ctx)
	if err != nil {
		return 0, fmt.Errorf("fx: refresh: %w", err)
	}
	if err := s.store.SaveRates(ctx, rates); err != nil {
		return 0, fmt.Errorf("fx: refresh: %w", err)
	}
	return len(rates), nil
}

// Convert converts m into target using the rate in force on the day of on.
//
// The rate is read from storage, never fetched, so a report over a year of sales
// makes no network calls and gives the same answer every time it is run.
//
// The arithmetic itself is [core.Rate.Convert]'s: it rounds half-up to the
// target currency's exponent and refuses a cross rate, and reimplementing either
// here would give the same numbers two definitions.
func (s *Service) Convert(ctx context.Context, m core.Money, target string, on time.Time) (core.Money, error) {
	if m.Currency == target {
		return m, nil
	}

	// Stored rates are EUR-based, so the row to look up is whichever side of the
	// pair is not the base currency. A pair with no base side at all is a cross
	// rate, and core.Rate.Convert is what refuses it below.
	quote := target
	if quote == core.BaseCurrency {
		quote = m.Currency
	}

	// The day is taken in on's own location: a sale timestamped just after
	// midnight locally belongs to that local day, and shifting it to UTC first
	// would book it against the previous day's rate.
	day := on.Format(dayLayout)

	rate, err := s.store.RateOn(ctx, day, quote)
	if err != nil {
		return core.Money{}, fmt.Errorf("fx: convert %s to %s on %s: %w",
			m.Currency, target, day, err)
	}

	out, err := rate.Convert(m, target)
	if err != nil {
		return core.Money{}, fmt.Errorf("fx: convert %s to %s on %s: %w",
			m.Currency, target, day, err)
	}
	return out, nil
}

// StartDaily refreshes once immediately, then every 24 hours, and returns a stop
// function that blocks until the refresher has exited.
//
// The goroutine ends on whichever comes first: ctx being cancelled or stop being
// called. A failed refresh is logged and left for the next tick — the ECB being
// unreachable must not stop the application serving, because every rate it has
// already stored is still perfectly good.
func (s *Service) StartDaily(ctx context.Context) func() {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})

	go func() {
		defer close(done)
		ticker := time.NewTicker(refreshInterval)
		defer ticker.Stop()
		for {
			s.refreshOnce(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()

	return func() {
		cancel()
		<-done
	}
}

// refreshOnce runs one scheduled refresh and reports the outcome to the log.
func (s *Service) refreshOnce(ctx context.Context) {
	n, err := s.Refresh(ctx)
	switch {
	case ctx.Err() != nil:
		// Shutting down. A fetch cut short by our own cancellation is not a
		// failure, and logging it as one trains people to ignore the message.
	case err != nil:
		slog.Default().ErrorContext(ctx, "fx: daily rate refresh failed", "error", err)
	default:
		slog.Default().InfoContext(ctx, "fx: rates refreshed", "rates", n)
	}
}
