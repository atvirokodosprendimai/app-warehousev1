package web

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/starfederation/datastar-go/datastar"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
	"github.com/atvirokodosprendimai/app-warehousev1/internal/web/render"
	"github.com/atvirokodosprendimai/app-warehousev1/internal/web/view"
)

// maxPhotoBytes bounds one uploaded image.
//
// A phone photograph is a few megabytes; anything far larger is a mistake or an
// attempt to fill the disk, and the whole file is read into memory to be hashed
// and written atomically.
const maxPhotoBytes = 12 << 20

// maxUploadBytes bounds a whole multipart request, so a single upload of many
// files cannot exhaust memory even though each one is individually acceptable.
const maxUploadBytes = 64 << 20

// GetDashboard renders the landing screen.
func (a *App) GetDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	d := view.Dashboard{Page: a.page(r, "Dashboard", "dashboard")}

	recent, err := a.Offers.Offers(ctx, core.OfferFilter{Limit: 8})
	if err != nil {
		a.Log.Error("dashboard recent", "err", err)
		http.Error(w, "Could not load the dashboard.", http.StatusInternalServerError)
		return
	}
	d.Recent = a.rows(ctx, recent)

	pricing, err := a.Offers.Offers(ctx, core.OfferFilter{NeedsPricing: true, Limit: 5})
	if err == nil {
		d.Pricing = a.rows(ctx, pricing)
	}

	d.StockValue, d.OwedToHolders = a.stockTotals(r)
	d.SoldThisMonth = a.soldThisMonth(r)

	if rates, err := a.Rates.LatestRates(ctx); err == nil {
		for _, rt := range rates {
			if rt.Quote == "USD" {
				d.Rate = rt
				break
			}
		}
	}

	_ = view.PageShell(d.Page, view.NewOfferAction(), view.DashboardScreen(d)).Render(ctx, w)
}

// stockTotals sums what listed stock is priced at and what is owed on it.
//
// Offers priced in a currency other than the base are SKIPPED rather than
// converted, and the total is therefore an understatement rather than a guess.
// Converting would need a rate per offer and a decision about which day's, and
// burying that inside a headline figure makes it unreproducible.
func (a *App) stockTotals(r *http.Request) (stock core.Money, owed core.Money) {
	live, err := a.Offers.Offers(r.Context(), core.OfferFilter{
		Status: []core.Status{core.StatusListed, core.StatusPending},
		Limit:  10000,
	})
	if err != nil {
		a.Log.Warn("stock totals unavailable", "err", err)
		return core.Money{}, core.Money{}
	}
	stock = core.Money{Currency: core.BaseCurrency}
	owed = core.Money{Currency: core.BaseCurrency}
	for _, o := range live {
		if o.Shop.Currency == core.BaseCurrency {
			stock.Minor += o.Shop.Minor
		}
		if o.Owner.Currency == core.BaseCurrency {
			owed.Minor += o.Owner.Minor
		}
	}
	return stock, owed
}

// soldThisMonth totals realised sales in the current calendar month.
func (a *App) soldThisMonth(r *http.Request) core.Money {
	sold, err := a.Offers.Offers(r.Context(), core.OfferFilter{
		Status: []core.Status{core.StatusSold},
		Limit:  10000,
	})
	if err != nil {
		return core.Money{}
	}
	n := now()
	start := time.Date(n.Year(), n.Month(), 1, 0, 0, 0, 0, time.UTC)

	total := core.Money{Currency: core.BaseCurrency}
	for _, o := range sold {
		if o.SoldAt == nil || o.SoldAt.Before(start) {
			continue
		}
		if o.Sold.Currency == core.BaseCurrency {
			total.Minor += o.Sold.Minor
		}
	}
	return total
}

// GetOffers renders the offers listing.
func (a *App) GetOffers(w http.ResponseWriter, r *http.Request) {
	f := offerFilterFrom(r)

	offers, err := a.Offers.Offers(r.Context(), f)
	if err != nil {
		a.Log.Error("list offers", "err", err)
		http.Error(w, "Could not list offers.", http.StatusInternalServerError)
		return
	}

	nav := "offers"
	title := "Offers"
	switch {
	case f.NeedsPricing:
		nav, title = "pricing", "Awaiting pricing"
	case len(f.Status) == 1:
		nav, title = "status-"+string(f.Status[0]), f.Status[0].Label()
	}

	l := view.OfferList{
		Page:       a.page(r, title, nav),
		Rows:       a.rows(r.Context(), offers),
		Filter:     f,
		Total:      len(offers),
		Currencies: core.KnownCurrencies(),
	}

	// The cart the Add buttons target. Loaded here rather than in the fragment so
	// the whole screen is still a function of the request alone.
	if carts, err := a.Carts.Carts(r.Context()); err == nil {
		l.Carts = carts
		l.ActiveCart = a.activeCart(r, carts)
	}
	_ = view.PageShell(l.Page, view.NewOfferAction(), view.OffersScreen(l)).Render(r.Context(), w)
}

// offerFilterFrom reads a listing filter out of the query string.
//
// The filter lives in the URL rather than in a signal so that a filtered listing
// is a real, shareable, bookmarkable address that survives a reload — which is
// also why the chips are links.
func offerFilterFrom(r *http.Request) core.OfferFilter {
	q := r.URL.Query()
	f := core.OfferFilter{
		Query:              strings.TrimSpace(q.Get("q")),
		LocationPathPrefix: strings.TrimSpace(q.Get("at")),
		NeedsPricing:       q.Get("needs_pricing") == "1",
		Limit:              200,
	}
	for _, s := range q["status"] {
		if st, err := core.ParseStatus(s); err == nil {
			f.Status = append(f.Status, st)
		}
	}
	return f
}

// GetIntake renders the new-offer screen.
func (a *App) GetIntake(w http.ResponseWriter, r *http.Request) {
	locs, err := a.Locations.AllLocations(r.Context())
	if err != nil {
		a.Log.Warn("locations unavailable for intake", "err", err)
	}
	p := a.page(r, "New offer", "offers")
	_ = view.PageShell(p, nil, view.IntakeScreen(locs)).Render(r.Context(), w)
}

// intakeSignals is what the new-offer screen sends.
type intakeSignals struct {
	Title    string `json:"newTitle"`
	SKU      string `json:"newSku"`
	Location string `json:"newLocation"`
}

// PostOffers creates an offer from a title alone.
//
// No price is asked for and none is required. The flow is photograph, title,
// shelve — and price later, once the operator has found out what the thing can
// actually fetch.
func (a *App) PostOffers(w http.ResponseWriter, r *http.Request) {
	var in intakeSignals
	if err := datastar.ReadSignals(r, &in); err != nil {
		a.flash(w, r, "intake-msg", "error", "Could not read the form.")
		return
	}

	o, err := a.Offer.Create(r.Context(), in.Title, in.SKU)
	if err != nil {
		a.flash(w, r, "intake-msg", "error", a.userMessage(err))
		return
	}

	if in.Location != "" {
		o.LocationID = in.Location
		if err := a.Offer.Update(r.Context(), o); err != nil {
			// The offer exists; only the shelving failed. Say so precisely rather
			// than implying nothing was created, which would invite a duplicate.
			a.flash(w, r, "intake-msg", "error",
				"Created, but could not shelve it: "+a.userMessage(err))
			return
		}
	}

	sse := render.NewSSE(w, r)
	_ = sse.ExecuteScript("window.location.href = '/offers/" + o.ID + "'")
}

// GetOffer renders one offer.
func (a *App) GetOffer(w http.ResponseWriter, r *http.Request) {
	d, err := a.offerDetail(r, param(r, "id"))
	if err != nil {
		a.notFound(w, r, err)
		return
	}
	_ = view.PageShell(d.Page, nil, view.OfferScreen(d)).Render(r.Context(), w)
}

// offerDetail builds the single-offer read model.
//
// It is a function of the id alone and is called by both the page load and the
// SSE loop, so the two can never render different things from the same state.
func (a *App) offerDetail(r *http.Request, id string) (view.OfferDetail, error) {
	o, err := a.Offers.Offer(r.Context(), id)
	if err != nil {
		return view.OfferDetail{}, err
	}
	locs, err := a.Locations.AllLocations(r.Context())
	if err != nil {
		a.Log.Warn("locations unavailable", "err", err)
	}
	d := view.OfferDetail{
		Page:       a.page(r, o.Title, "offers"),
		Row:        a.row(r.Context(), o),
		Locations:  locs,
		Currencies: core.KnownCurrencies(),
		PublicBase: a.publicBase(r.Context()),
	}

	// The editor asks its own stream to carry this offer's cards as well, so a
	// second tab showing the same offer stays live without opening a second
	// connection.
	d.Page.StreamPath = "/stream?offer=" + id

	// Batch membership is resolved here rather than inside the card, so the whole
	// detail read model stays a function of the id alone — which is what lets the
	// SSE loop and the initial page render come from one call and therefore never
	// disagree. Neither lookup is fatal: a batch list is context, and losing it
	// should not take the offer page down with it.
	if carts, err := a.Carts.Carts(r.Context()); err == nil {
		d.Carts = carts
	}
	if holding, err := a.Carts.CartsHolding(r.Context(), id); err == nil {
		d.InCarts = holding
	}
	return d, nil
}

// notFound answers a missing aggregate.
func (a *App) notFound(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, core.ErrNotFound) {
		http.Error(w, "Not found.", http.StatusNotFound)
		return
	}
	a.Log.Error("load failed", "err", err)
	http.Error(w, "Could not load that.", http.StatusInternalServerError)
}

// offerSignals is what the offer editor sends.
type offerSignals struct {
	Title         string `json:"offerTitle"`
	Description   string `json:"offerDescription"`
	Condition     string `json:"offerCondition"`
	ShopAmount    string `json:"shopAmount"`
	ShopCurrency  string `json:"shopCurrency"`
	OwnerAmount   string `json:"ownerAmount"`
	OwnerCurrency string `json:"ownerCurrency"`
	Location      string `json:"offerLocation"`
	SoldAmount    string `json:"soldAmount"`
	SoldCurrency  string `json:"soldCurrency"`
	SoldDate      string `json:"soldDate"`
	// EbayCategory is this offer's own eBay category, overriding the default
	// configured at Settings. Empty means "use the default" — clearing it and
	// never having set one are deliberately the same state (ADR-016).
	//
	// Only eBay has one today because only eBay's resolution is wired; Shopify's
	// and Allegro's are deferred in docs/adr/BACKLOG.md.
	EbayCategory string `json:"offerEbayCategory"`
}

// PostOffer saves the editable text of an offer.
func (a *App) PostOffer(w http.ResponseWriter, r *http.Request) {
	var in offerSignals
	if err := datastar.ReadSignals(r, &in); err != nil {
		a.flash(w, r, "offer-flash", "error", "Could not read the form.")
		return
	}
	id := param(r, "id")

	o, err := a.Offers.Offer(r.Context(), id)
	if err != nil {
		a.flash(w, r, "offer-flash", "error", a.userMessage(err))
		return
	}
	o.Title = in.Title
	o.Description = in.Description
	o.Condition = in.Condition

	if err := a.Offer.Update(r.Context(), o); err != nil {
		a.flash(w, r, "offer-flash", "error", a.userMessage(err))
		return
	}
	a.Broadcast(id)
	a.flash(w, r, "offer-flash", "ok", "Saved.")
}

// PostPrices records the outcome of the pricing research step.
func (a *App) PostPrices(w http.ResponseWriter, r *http.Request) {
	var in offerSignals
	if err := datastar.ReadSignals(r, &in); err != nil {
		a.flash(w, r, "offer-flash", "error", "Could not read the form.")
		return
	}
	id := param(r, "id")

	shop, err := parsePrice(in.ShopAmount, in.ShopCurrency)
	if err != nil {
		a.flash(w, r, "offer-flash", "error", "Shop price: "+a.userMessage(err))
		return
	}
	owner, err := parsePrice(in.OwnerAmount, in.OwnerCurrency)
	if err != nil {
		a.flash(w, r, "offer-flash", "error", "Owner price: "+a.userMessage(err))
		return
	}

	if err := a.Offer.SetPrices(r.Context(), id, shop, owner); err != nil {
		a.flash(w, r, "offer-flash", "error", a.userMessage(err))
		return
	}
	a.Broadcast(id)

	d, err := a.offerDetail(r, id)
	if err != nil {
		a.flash(w, r, "offer-flash", "ok", "Saved.")
		return
	}
	sse := render.NewSSE(w, r)
	_ = sse.PatchElementTempl(view.Flash("offer-flash", "ok", "Prices saved."))
	_ = sse.PatchElementTempl(view.MarginLine(d.Row))
	_ = sse.PatchElementTempl(view.OfferStatusCard(d))
}

// PostOfferCategory records this offer's own eBay category, or clears it.
//
// It exists because eBay's categories are per ITEM: a lamp and a chair are not
// the same number, so a single value for a whole export file would mean one
// export per category. The default set at Settings covers everything that does
// not name its own (ADR-016).
func (a *App) PostOfferCategory(w http.ResponseWriter, r *http.Request) {
	var in offerSignals
	if err := datastar.ReadSignals(r, &in); err != nil {
		a.flash(w, r, "offer-flash", "error", "Could not read the form.")
		return
	}
	id := param(r, "id")

	if err := a.Offer.SetCategory(r.Context(), id, "ebay", in.EbayCategory); err != nil {
		a.flash(w, r, "offer-flash", "error", a.userMessage(err))
		return
	}
	a.Broadcast(id)

	msg := "eBay category saved."
	if strings.TrimSpace(in.EbayCategory) == "" {
		msg = "eBay category cleared — this offer will use the default from Settings."
	}
	sse := render.NewSSE(w, r)
	_ = sse.PatchElementTempl(view.Flash("offer-flash", "ok", msg))
}

// parsePrice reads one money field. An empty amount is a CLEARED price, not an
// error: removing a price an operator no longer stands behind is a legitimate
// edit, and refusing it would leave a stale figure they cannot delete.
func parsePrice(amount, currency string) (core.Money, error) {
	if strings.TrimSpace(amount) == "" {
		return core.Money{}, nil
	}
	return core.ParseMoney(amount, currency)
}

// PostStatus moves an offer through its lifecycle.
func (a *App) PostStatus(w http.ResponseWriter, r *http.Request) {
	id := param(r, "id")
	st, err := core.ParseStatus(param(r, "status"))
	if err != nil {
		a.flash(w, r, "offer-flash", "error", a.userMessage(err))
		return
	}
	if err := a.Offer.SetStatus(r.Context(), id, st); err != nil {
		a.flash(w, r, "offer-flash", "error", a.userMessage(err))
		return
	}
	a.Broadcast(id)

	d, err := a.offerDetail(r, id)
	if err != nil {
		a.flash(w, r, "offer-flash", "ok", "Status updated.")
		return
	}
	sse := render.NewSSE(w, r)
	_ = sse.PatchElementTempl(view.Flash("offer-flash", "ok", "Now "+st.Label()))
	_ = sse.PatchElementTempl(view.OfferStatusCard(d))
}

// GetSoldDialog renders the record-a-sale dialog into the page's empty modal.
func (a *App) GetSoldDialog(w http.ResponseWriter, r *http.Request) {
	d, err := a.offerDetail(r, param(r, "id"))
	if err != nil {
		a.flash(w, r, "offer-flash", "error", a.userMessage(err))
		return
	}
	sse := render.NewSSE(w, r)
	_ = sse.PatchElementTempl(view.SoldDialog(d))
}

// GetModalClose empties the modal mount. Sending an empty fragment by id is how
// the server closes something it opened.
func (a *App) GetModalClose(w http.ResponseWriter, r *http.Request) {
	sse := render.NewSSE(w, r)
	_ = sse.PatchElementTempl(view.CloseModal())
}

// PostSold records a completed sale.
func (a *App) PostSold(w http.ResponseWriter, r *http.Request) {
	var in offerSignals
	if err := datastar.ReadSignals(r, &in); err != nil {
		a.flash(w, r, "sold-msg", "error", "Could not read the form.")
		return
	}
	id := param(r, "id")

	price, err := core.ParseMoney(in.SoldAmount, in.SoldCurrency)
	if err != nil {
		a.flash(w, r, "sold-msg", "error", a.userMessage(err))
		return
	}
	day, err := core.NormalizeDay(in.SoldDate)
	if err != nil {
		a.flash(w, r, "sold-msg", "error", a.userMessage(err))
		return
	}
	when, err := time.Parse(core.DayLayout, day)
	if err != nil {
		a.flash(w, r, "sold-msg", "error", a.userMessage(err))
		return
	}

	if err := a.Offer.MarkSold(r.Context(), id, price, when); err != nil {
		a.flash(w, r, "sold-msg", "error", a.userMessage(err))
		return
	}
	a.Broadcast(id)

	d, err := a.offerDetail(r, id)
	if err != nil {
		a.flash(w, r, "sold-msg", "error", a.userMessage(err))
		return
	}
	sse := render.NewSSE(w, r)
	_ = sse.PatchElementTempl(view.CloseModal())
	_ = sse.PatchElementTempl(view.OfferStatusCard(d))
	_ = sse.PatchElementTempl(view.Flash("offer-flash", "ok", "Sale recorded."))
}

// PostOfferLocation moves an offer to a different place.
func (a *App) PostOfferLocation(w http.ResponseWriter, r *http.Request) {
	var in offerSignals
	if err := datastar.ReadSignals(r, &in); err != nil {
		a.flash(w, r, "offer-flash", "error", "Could not read the form.")
		return
	}
	id := param(r, "id")

	o, err := a.Offers.Offer(r.Context(), id)
	if err != nil {
		a.flash(w, r, "offer-flash", "error", a.userMessage(err))
		return
	}
	o.LocationID = in.Location
	if err := a.Offer.Update(r.Context(), o); err != nil {
		a.flash(w, r, "offer-flash", "error", a.userMessage(err))
		return
	}
	a.Broadcast(id)
	a.flash(w, r, "offer-flash", "ok", "Moved.")
}

// PostPhotos accepts one or more uploaded images.
//
// ⚠ This is the ONE endpoint in the application that takes form encoding rather
// than signals: image bytes cannot ride in a datastar signal.
func (a *App) PostPhotos(w http.ResponseWriter, r *http.Request) {
	id := param(r, "id")

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(maxPhotoBytes); err != nil {
		a.flash(w, r, "offer-flash", "error",
			"That upload was too large or could not be read.")
		return
	}

	var files []*multipartFile
	for _, fhs := range r.MultipartForm.File {
		for _, fh := range fhs {
			files = append(files, &multipartFile{fh: fh})
		}
	}
	if len(files) == 0 {
		a.flash(w, r, "offer-flash", "error", "No file was received.")
		return
	}

	var failed []string
	for _, f := range files {
		data, name, ct, err := f.read()
		if err != nil {
			failed = append(failed, name+": "+err.Error())
			continue
		}
		if _, err := a.Offer.AddPhoto(r.Context(), id, name, ct, data); err != nil {
			failed = append(failed, name+": "+a.userMessage(err))
		}
	}
	a.Broadcast(id)

	d, err := a.offerDetail(r, id)
	if err != nil {
		a.flash(w, r, "offer-flash", "error", a.userMessage(err))
		return
	}

	sse := render.NewSSE(w, r)
	_ = sse.PatchElementTempl(view.OfferPhotosCard(d))
	if len(failed) > 0 {
		_ = sse.PatchElementTempl(view.Flash("offer-flash", "error",
			"Some photographs were not added — "+strings.Join(failed, "; ")))
		return
	}
	_ = sse.PatchElementTempl(view.Flash("offer-flash", "ok", "Photographs added."))
}

// DeletePhoto removes one photograph.
func (a *App) DeletePhoto(w http.ResponseWriter, r *http.Request) {
	photoID := param(r, "id")

	// The offer id is needed to re-render, and the photo row is about to be gone,
	// so resolve it first.
	offerID := r.URL.Query().Get("offer")
	if err := a.Offer.RemovePhoto(r.Context(), photoID); err != nil {
		a.flash(w, r, "offer-flash", "error", a.userMessage(err))
		return
	}
	a.Broadcast(offerID)

	d, err := a.offerDetail(r, offerID)
	if err != nil {
		a.flash(w, r, "offer-flash", "ok", "Photograph removed.")
		return
	}
	sse := render.NewSSE(w, r)
	_ = sse.PatchElementTempl(view.OfferPhotosCard(d))
	_ = sse.PatchElementTempl(view.Flash("offer-flash", "ok", "Photograph removed."))
}

// Broadcast notifies open streams that an offer changed, if a bus is wired.
func (a *App) Broadcast(id string) {
	if a.Bus != nil && id != "" {
		a.Bus.Broadcast(id)
	}
}
