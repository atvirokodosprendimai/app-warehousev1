package web

import (
	"net/http"
	"strings"

	"github.com/starfederation/datastar-go/datastar"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
	"github.com/atvirokodosprendimai/app-warehousev1/internal/web/render"
	"github.com/atvirokodosprendimai/app-warehousev1/internal/web/view"
)

// searchSignals is what the find controls send.
type searchSignals struct {
	Query    string `json:"searchQuery"`
	PriceMin string `json:"priceMin"`
	PriceMax string `json:"priceMax"`
	Currency string `json:"priceCurrency"`
}

// GetOfferRows re-renders the offers table for the current search.
//
// The text and price terms arrive as SIGNALS while the status filter arrives in
// the QUERY STRING, and that split is deliberate rather than accidental: a
// status filter is a distinct listing worth bookmarking, and a search box
// changes on every keystroke and would fill the history with a URL per letter.
func (a *App) GetOfferRows(w http.ResponseWriter, r *http.Request) {
	// Signals are read BEFORE the stream is opened: opening it flushes the
	// response, after which the request body can no longer be read.
	var in searchSignals
	if err := datastar.ReadSignals(r, &in); err != nil {
		a.Log.Warn("search signals unreadable", "err", err)
	}

	f := offerFilterFrom(r)
	f.Query = strings.TrimSpace(in.Query)

	cur := strings.TrimSpace(in.Currency)
	if cur == "" {
		cur = core.BaseCurrency
	}
	f.PriceMin = priceBound(in.PriceMin, cur)
	f.PriceMax = priceBound(in.PriceMax, cur)

	offers, err := a.Offers.Offers(r.Context(), f)
	if err != nil {
		a.Log.Error("search", "err", err)
		a.flash(w, r, "offer-list", "error", "That search could not be run.")
		return
	}
	rows := a.rows(r.Context(), offers)

	sse := render.NewSSE(w, r)
	_ = sse.PatchElementTempl(view.OfferTable("offer-list", rows, true))
	// The count travels with the rows so the two cannot disagree — a "42
	// matching" above eleven rows is worse than no count at all.
	_ = sse.PatchElementTempl(view.OfferCount(len(rows)))
}

// priceBound parses one end of a price range, or returns nil for no bound.
//
// ⚠ An unparseable amount is treated as NO BOUND rather than as an error. These
// arrive on every keystroke, so "1" on the way to "12.50" is a transient state
// the operator passes through, not a mistake to interrupt them about — and
// "12." is not a number either. Refusing here would flash an error under
// somebody's hands while they type.
func priceBound(amount, currency string) *core.Money {
	if strings.TrimSpace(amount) == "" {
		return nil
	}
	m, err := core.ParseMoney(amount, currency)
	if err != nil {
		return nil
	}
	return &m
}
