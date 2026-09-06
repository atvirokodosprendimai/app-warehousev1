package fx

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// serveFixture starts a test server answering every request with body, and
// returns a Client pointed at it.
func serveFixture(t *testing.T, status int, body string) *Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return NewClient(srv.Client(), srv.URL)
}

func TestNewClientDefaultsBothArguments(t *testing.T) {
	c := NewClient(nil, "")
	if c.url != DefaultURL {
		t.Errorf("url = %q, want %q", c.url, DefaultURL)
	}
	if c.http != http.DefaultClient {
		t.Error("http client = nil, want http.DefaultClient")
	}
}

func TestFetchParsesTheDailyFeed(t *testing.T) {
	c := serveFixture(t, http.StatusOK, ecbFixture)

	before := time.Now().UTC()
	rates, err := c.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	after := time.Now().UTC()

	if len(rates) != fixtureCurrencies {
		t.Fatalf("got %d rates, want %d", len(rates), fixtureCurrencies)
	}

	// The rate strings are asserted VERBATIM. GBP is the one that matters:
	// parsing "0.86720" to a float and re-rendering it yields "0.8672", so this
	// assertion fails the moment anyone introduces a float round-trip.
	want := map[string]string{
		"USD": "1.1712",
		"JPY": "172.53",
		"GBP": "0.86720",
		"PLN": "4.2528",
	}
	got := make(map[string]string, len(rates))
	for _, r := range rates {
		got[r.Quote] = r.Rate

		if r.AsOf != fixtureDay {
			t.Errorf("%s AsOf = %q, want %q", r.Quote, r.AsOf, fixtureDay)
		}
		if r.FetchedAt.Before(before) || r.FetchedAt.After(after) {
			t.Errorf("%s FetchedAt = %v, want between %v and %v",
				r.Quote, r.FetchedAt, before, after)
		}
		if r.FetchedAt.Location() != time.UTC {
			t.Errorf("%s FetchedAt location = %v, want UTC", r.Quote, r.FetchedAt.Location())
		}
	}
	for quote, wantRate := range want {
		if got[quote] != wantRate {
			t.Errorf("%s rate = %q, want %q", quote, got[quote], wantRate)
		}
	}
}

func TestFetchSharesOneFetchedAtAcrossTheBatch(t *testing.T) {
	c := serveFixture(t, http.StatusOK, ecbFixture)

	rates, err := c.Fetch(context.Background())
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	for _, r := range rates[1:] {
		if !r.FetchedAt.Equal(rates[0].FetchedAt) {
			t.Fatalf("%s FetchedAt = %v, want %v (one instant per batch)",
				r.Quote, r.FetchedAt, rates[0].FetchedAt)
		}
	}
}

func TestFetchSendsAUserAgent(t *testing.T) {
	var gotAgent, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAgent = r.Header.Get("User-Agent")
		gotMethod = r.Method
		_, _ = w.Write([]byte(ecbFixture))
	}))
	t.Cleanup(srv.Close)

	if _, err := NewClient(srv.Client(), srv.URL).Fetch(context.Background()); err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if gotMethod != http.MethodGet {
		t.Errorf("method = %q, want GET", gotMethod)
	}
	if gotAgent != userAgent {
		t.Errorf("User-Agent = %q, want %q", gotAgent, userAgent)
	}
}

func TestFetchRejectsANonOKStatus(t *testing.T) {
	c := serveFixture(t, http.StatusServiceUnavailable, "down for maintenance")

	rates, err := c.Fetch(context.Background())
	if err == nil {
		t.Fatalf("Fetch returned %d rates, want an error", len(rates))
	}
	if !strings.Contains(err.Error(), "503") {
		t.Errorf("error = %q, want it to name the status", err)
	}
}

func TestFetchRejectsMalformedXML(t *testing.T) {
	c := serveFixture(t, http.StatusOK, `<gesmes:Envelope><Cube time='2026-09-04'`)

	if rates, err := c.Fetch(context.Background()); err == nil {
		t.Fatalf("Fetch returned %d rates, want an error", len(rates))
	}
}

func TestFetchRejectsADocumentThatIsNotTheFeed(t *testing.T) {
	// Well-formed XML of the wrong shape must not read as "no rates today".
	c := serveFixture(t, http.StatusOK, `<html><body>Service moved</body></html>`)

	if rates, err := c.Fetch(context.Background()); err == nil {
		t.Fatalf("Fetch returned %d rates, want an error", len(rates))
	}
}

func TestFetchRejectsAFeedWithNoRates(t *testing.T) {
	// An empty day that read as success would leave the table stale while every
	// report kept succeeding — the whole point of erroring here.
	c := serveFixture(t, http.StatusOK,
		`<gesmes:Envelope xmlns:gesmes="http://www.gesmes.org/xml/2002-08-01">`+
			`<Cube><Cube time='2026-09-04'></Cube></Cube></gesmes:Envelope>`)

	rates, err := c.Fetch(context.Background())
	if err == nil {
		t.Fatalf("Fetch returned %d rates, want an error", len(rates))
	}
	if !strings.Contains(err.Error(), "no rates") {
		t.Errorf("error = %q, want it to say the feed carried no rates", err)
	}
}

func TestFetchHonoursACancelledContext(t *testing.T) {
	c := serveFixture(t, http.StatusOK, ecbFixture)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := c.Fetch(ctx); err == nil {
		t.Fatal("Fetch with a cancelled context returned no error")
	}
}
