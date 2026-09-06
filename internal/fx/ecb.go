// Package fx fetches, stores and applies the European Central Bank's daily euro
// reference rates.
//
// The business keeps its books in [core.BaseCurrency] but offers may be priced
// and sold in other currencies. Reporting a USD sale in EUR needs the rate that
// applied ON THE SALE DATE, not today's, so rates are stored one row per day per
// currency and looked up by date. Nothing here is called at report time: the
// report reads the table, and [Service.StartDaily] is what keeps the table fed.
//
// The ECB feed is the source because it is free, needs no key, and is published
// against EUR — the same base the books use, so no cross rate is ever implied.
package fx

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
)

// DefaultURL is the ECB's daily euro reference rate feed.
const DefaultURL = "https://www.ecb.europa.eu/stats/eurofxref/eurofxref-daily.xml"

// requestTimeout bounds one fetch. The feed is a few kilobytes of static XML, so
// anything slower than this is an outage rather than a slow day, and a refresh
// that hangs would hold a goroutine until the process restarts.
const requestTimeout = 15 * time.Second

// maxBodyBytes bounds how much of the response is decoded. The real document is
// under 3 KB; the limit exists so that a proxy returning something enormous
// cannot exhaust memory on a host we do not control.
const maxBodyBytes = 1 << 20

// userAgent identifies this application to the ECB, which asks that automated
// clients be attributable.
const userAgent = "app-warehousev1/1.0 (+https://github.com/atvirokodosprendimai/app-warehousev1)"

// Client fetches daily reference rates from the ECB.
type Client struct {
	http *http.Client
	url  string
}

// NewClient returns a Client fetching from url using httpClient.
//
// Both arguments may be zero: an empty url falls back to [DefaultURL] and a nil
// httpClient to [http.DefaultClient]. They are parameters at all so that a test
// can point the client at an [net/http/httptest.Server] instead of the live ECB.
func NewClient(httpClient *http.Client, url string) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	if url == "" {
		url = DefaultURL
	}
	return &Client{http: httpClient, url: url}
}

// Fetch returns one [core.Rate] per quoted currency in the current feed.
//
// AsOf carries the feed's own publication date, so a fetch on a Sunday yields
// Friday's date rather than today's. FetchedAt is the moment of retrieval in
// UTC, shared by every rate in the batch.
//
// An empty result is always an error, never an empty slice: rates that quietly
// fail to arrive would leave the table stale while every report kept succeeding,
// which is exactly the failure nobody notices.
func (c *Client) Fetch(ctx context.Context) ([]core.Rate, error) {
	ctx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return nil, fmt.Errorf("fx: build request for %s: %w", c.url, err)
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/xml")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fx: fetch %s: %w", c.url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fx: fetch %s: unexpected status %s", c.url, resp.Status)
	}

	var env ecbEnvelope
	if err := xml.NewDecoder(io.LimitReader(resp.Body, maxBodyBytes)).Decode(&env); err != nil {
		return nil, fmt.Errorf("fx: decode %s: %w", c.url, err)
	}

	fetchedAt := time.Now().UTC()
	rates := env.rates(fetchedAt)
	if len(rates) == 0 {
		return nil, fmt.Errorf("fx: fetch %s: feed carried no rates", c.url)
	}
	return rates, nil
}

// ecbEnvelope is the gesmes:Envelope document wrapping the feed.
//
// The field tags name local elements only. The document puts Envelope in the
// gesmes namespace and the Cube tree in the ECB's own, and encoding/xml matches
// an unqualified tag against any namespace — so decoding by element name sees
// through the prefixes instead of having to reproduce them.
type ecbEnvelope struct {
	XMLName xml.Name  `xml:"Envelope"`
	Cubes   []ecbCube `xml:"Cube"`
}

// ecbCube is one node of the feed's Cube tree.
//
// All three levels — the outer container, the dated day, and the per-currency
// leaf — are spelled <Cube> and are distinguished only by which attributes they
// carry, so one recursive type models the whole tree.
type ecbCube struct {
	Time     string    `xml:"time,attr"`
	Currency string    `xml:"currency,attr"`
	Rate     string    `xml:"rate,attr"`
	Children []ecbCube `xml:"Cube"`
}

// rates flattens the Cube tree into one core.Rate per quoted currency.
//
// The rate is carried across as THE VERBATIM ATTRIBUTE STRING. Parsing it to a
// float and re-rendering would silently rewrite the published figure — "0.86720"
// coming back as "0.8672", and worse for values a binary float cannot hold —
// and core.Rate.Rate is a decimal string precisely so that never happens.
func (e ecbEnvelope) rates(fetchedAt time.Time) []core.Rate {
	var out []core.Rate
	for _, outer := range e.Cubes {
		for _, day := range outer.Children {
			if day.Time == "" {
				continue
			}
			for _, leaf := range day.Children {
				if leaf.Currency == "" || leaf.Rate == "" {
					continue
				}
				out = append(out, core.Rate{
					AsOf:      day.Time,
					Quote:     leaf.Currency,
					Rate:      leaf.Rate,
					FetchedAt: fetchedAt,
				})
			}
		}
	}
	return out
}
