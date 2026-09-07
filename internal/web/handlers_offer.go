package web

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
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

	// The New offer button is rendered by the shell itself, on every page, so
	// passing it here would render it twice.
	_ = view.PageShell(d.Page, nil, view.DashboardScreen(d)).Render(ctx, w)
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
	case f.NeedsDescribing:
		nav, title = "describing", "Needs describing"
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
	_ = view.PageShell(l.Page, nil, view.OffersScreen(l)).Render(r.Context(), w)
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
		NeedsDescribing:    q.Get("needs_describing") == "1",
		Limit:              200,
	}
	for _, s := range q["status"] {
		if st, err := core.ParseStatus(s); err == nil {
			f.Status = append(f.Status, st)
		}
	}
	return f
}

// PostOffers creates an empty draft and sends the operator to its photographs.
//
// ⚠ IT READS NOTHING, deliberately (ADR-020). There is no body to parse and no
// signals to declare, which is what lets the button live in the top bar on every
// page rather than only on a screen that seeded the right signals.
//
// Every question the deleted intake screen asked was already optional: the title
// since ADR-019, the reference because ADR-010 has the database allocate one, and
// the location because the editor carries its own card for it. Asking them bought
// nothing and cost the operator a form while they were holding the object.
func (a *App) PostOffers(w http.ResponseWriter, r *http.Request) {
	o, err := a.Offer.Create(r.Context(), "", "")
	if err != nil {
		// The button is global, so the message has to land somewhere that exists on
		// every page — #app-flash in the layout, not the intake screen's old slot.
		a.flash(w, r, "app-flash", "error", a.userMessage(err))
		return
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

	// The taxonomy tree and this offer's resolved questions. ⚠ Both are loaded
	// HERE, in the read model both the page load and the SSE loop go through, so
	// answering a question and having the page re-render cannot disagree about
	// which questions there were.
	//
	// Neither is fatal. A picker with no options and a card with no questions are
	// both legible states; taking the offer page down because a taxonomy read
	// failed is not.
	if a.Taxonomies != nil {
		if cats, err := a.Taxonomies.AllCategories(r.Context()); err == nil {
			d.Categories = cats
		} else {
			a.Log.Warn("categories unavailable", "err", err)
		}
		if o.CategoryID != "" {
			if fields, err := a.Taxonomies.OfferFields(r.Context(), id, o.CategoryID); err == nil {
				d.Fields = fields
			} else {
				a.Log.Warn("offer fields unavailable", "err", err)
			}
		}
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
	// Sku is editable since ADR-020: deleting the intake screen removed the only
	// place a reference could be typed, so it moved to the Details card.
	Sku string `json:"offerSku"`
	// Quantity is how many of the thing we hold. It is a STRING here for the same
	// reason every other number on this screen is: the input is `type="text"` with
	// an inputmode, so the signal arrives as a string. A `type="number"` input
	// would send a JSON number and fail to unmarshal into this struct.
	Quantity string `json:"offerQuantity"`
	// Category is the node in the OPERATOR's tree that says what this thing is
	// (ADR-021). ⚠ Not EbayCategory below, which says where to list it.
	//
	// ⚠ A POINTER, and it is the only field on this card that needs to be. The
	// others treat empty as "the client did not send one", because none of them
	// can be legitimately cleared. This one CAN: the picker's first option is
	// "not filed", so an empty value is a choice the operator made — and a plain
	// string cannot tell that choice apart from a payload that omitted the key.
	// nil is absent; "" is cleared.
	//
	// Not hypothetical: the smoke walk caught it. A later save of the Details card
	// with a partial body silently un-filed an offer that had just been
	// categorised, and its custom columns then vanished from the export with
	// nothing reporting anything anywhere.
	Category     *string `json:"offerCategory"`
	SoldAmount   string  `json:"soldAmount"`
	SoldCurrency string  `json:"soldCurrency"`
	SoldDate     string  `json:"soldDate"`
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
	// An empty reference means "the client did not send one", never "clear it":
	// the column is UNIQUE and NOT NULL, and Service.Create already allocated a
	// value, so blanking it here would be a write nobody asked for.
	if strings.TrimSpace(in.Sku) != "" {
		o.SKU = strings.TrimSpace(in.Sku)
	}
	// ⚠ An empty quantity means "the client did not send one", never "we have none
	// of it". Both exporters write this number — eBay's `*Quantity` and Shopify's
	// `Variant Inventory Qty` — so silently coercing a blank to 0 would take a
	// live listing out of stock on a marketplace because a payload omitted a field.
	if q := strings.TrimSpace(in.Quantity); q != "" {
		n, err := strconv.Atoi(q)
		if err != nil || n < 0 {
			a.flash(w, r, "offer-flash", "error",
				"Quantity must be a whole number, 0 or more.")
			return
		}
		o.Quantity = n
	}
	// The taxonomy category, unlike the reference and the quantity, IS clearable:
	// it is a <select> whose first option is "not filed", so an empty value is a
	// choice the operator made rather than a field the payload omitted. Un-filing
	// something is a legitimate edit and there is no other control for it — which
	// is exactly why the signal is a pointer: nil means the key was absent and the
	// filing must be left alone.
	if in.Category != nil {
		o.CategoryID = strings.TrimSpace(*in.Category)
	}

	if err := a.Offer.Update(r.Context(), o); err != nil {
		a.flash(w, r, "offer-flash", "error", a.userMessage(err))
		return
	}
	a.Broadcast(id)
	a.flash(w, r, "offer-flash", "ok", "Saved.")
}

// PostOfferFields stores this offer's answers to whatever its category asks.
//
// ⚠ IT ITERATES THE FIELDS THE CATEGORY DEFINES, NOT THE SIGNALS THE CLIENT
// SENT. The page binds one signal per field, named for the field's id, and every
// unprefixed signal on the screen rides along with the request — so trusting the
// payload's key set would mean writing whatever a caller chose to name. Reading
// the resolved list first and looking each one up is what keeps the write bounded
// by the operator's own taxonomy.
func (a *App) PostOfferFields(w http.ResponseWriter, r *http.Request) {
	// A map rather than a struct, because the signal names are the operator's
	// field ids and no Go type can be written for a set that is data.
	var in map[string]any
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
	if o.CategoryID == "" {
		a.flash(w, r, "offer-flash", "error",
			"File this under a category first, and its questions will appear here.")
		return
	}

	fields, err := a.Taxonomies.OfferFields(r.Context(), id, o.CategoryID)
	if err != nil {
		a.flash(w, r, "offer-flash", "error", a.userMessage(err))
		return
	}
	for _, f := range fields {
		raw, ok := in[view.FieldSignal(f.ID)]
		if !ok {
			// The client did not send this one. Leaving the stored answer alone is
			// the only safe reading: a payload that omits a field has said nothing
			// about it, and treating silence as "clear it" would erase answers
			// whenever the markup and the handler disagree about a name.
			continue
		}
		if err := a.Taxonomy.Answer(r.Context(), id, f.ID, signalText(raw)); err != nil {
			a.flash(w, r, "offer-flash", "error", a.userMessage(err))
			return
		}
	}
	a.Broadcast(id)
	a.flash(w, r, "offer-flash", "ok", "Saved.")
}

// signalText renders one datastar signal as the text a field value is stored as.
//
// ⚠ A CHECKBOX SENDS A JSON BOOLEAN, NOT A STRING. Every other control on this
// screen sends a string, and a bool arriving where a string was expected is
// exactly the mismatch that makes a yes/no answer silently never save. A number
// is handled for the same reason: nothing binds one today, but a future
// type="number" input would send one, and returning "" for it would look like
// the operator had cleared the field.
func signalText(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case bool:
		if t {
			return "1"
		}
		return ""
	case float64:
		return strconv.FormatFloat(t, 'f', -1, 64)
	case nil:
		return ""
	default:
		return strings.TrimSpace(fmt.Sprint(t))
	}
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
