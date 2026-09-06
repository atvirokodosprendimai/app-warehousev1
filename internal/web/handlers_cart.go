package web

import (
	"net/http"

	"github.com/starfederation/datastar-go/datastar"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/export"
	"github.com/atvirokodosprendimai/app-warehousev1/internal/web/render"
	"github.com/atvirokodosprendimai/app-warehousev1/internal/web/view"
)

// cartSignals is what the batches screen sends.
type cartSignals struct {
	Name string `json:"cartName"`
	Note string `json:"cartNote"`
}

// GetCarts lists the saved batches.
func (a *App) GetCarts(w http.ResponseWriter, r *http.Request) {
	carts, err := a.Carts.Carts(r.Context())
	if err != nil {
		a.Log.Error("list carts", "err", err)
		http.Error(w, "Could not list batches.", http.StatusInternalServerError)
		return
	}
	c := view.Carts{Page: a.page(r, "Batches", "carts"), Carts: carts}
	_ = view.PageShell(c.Page, nil, view.CartsScreen(c)).Render(r.Context(), w)
}

// PostCarts creates a batch.
func (a *App) PostCarts(w http.ResponseWriter, r *http.Request) {
	var in cartSignals
	if err := datastar.ReadSignals(r, &in); err != nil {
		a.flash(w, r, "carts-msg", "error", "Could not read the form.")
		return
	}

	if _, err := a.Cart.Create(r.Context(), in.Name, in.Note); err != nil {
		a.flash(w, r, "carts-msg", "error", a.userMessage(err))
		return
	}

	carts, err := a.Carts.Carts(r.Context())
	if err != nil {
		a.flash(w, r, "carts-msg", "error", a.userMessage(err))
		return
	}

	sse := render.NewSSE(w, r)
	_ = sse.PatchElementTempl(view.CartList(carts))
	_ = sse.PatchElementTempl(view.Flash("carts-msg", "ok", "Batch created."))
	// Clear the fields so the next batch does not start with the last one's name
	// still in the box, which reads as though nothing was saved.
	_ = sse.MarshalAndPatchSignals(cartSignals{})
}

// GetCart renders one batch, split into what an export will and will not carry.
func (a *App) GetCart(w http.ResponseWriter, r *http.Request) {
	id := param(r, "id")

	cart, err := a.Carts.Cart(r.Context(), id)
	if err != nil {
		a.notFound(w, r, err)
		return
	}
	send, held, err := a.Cart.ExportSet(r.Context(), id)
	if err != nil {
		a.notFound(w, r, err)
		return
	}

	s := view.CartScreen{
		Page:     a.page(r, cart.Name, "carts"),
		Cart:     cart,
		Send:     send,
		Held:     held,
		Profiles: export.Names(),
	}
	_ = view.PageShell(s.Page, nil, view.CartDetailScreen(s)).Render(r.Context(), w)
}

// PostCartItem puts an offer into a batch.
func (a *App) PostCartItem(w http.ResponseWriter, r *http.Request) {
	cartID, offerID := param(r, "id"), param(r, "offerID")

	if err := a.Cart.Add(r.Context(), cartID, offerID); err != nil {
		a.flash(w, r, "offer-flash", "error", a.userMessage(err))
		return
	}
	a.repaintAfterCartChange(w, r, cartID, offerID)
}

// DeleteCartItem takes an offer out of a batch.
func (a *App) DeleteCartItem(w http.ResponseWriter, r *http.Request) {
	cartID, offerID := param(r, "id"), param(r, "offerID")

	if err := a.Cart.Remove(r.Context(), cartID, offerID); err != nil {
		a.flash(w, r, "offer-flash", "error", a.userMessage(err))
		return
	}
	a.repaintAfterCartChange(w, r, cartID, offerID)
}

// repaintAfterCartChange re-renders whichever screen the change came from.
//
// The same two endpoints serve the offer page's batch chips and the batch page's
// remove buttons, and the two want different fragments back. The caller says
// which with a query parameter rather than the handler guessing from a referer,
// which is optional and can be stripped by a proxy.
func (a *App) repaintAfterCartChange(w http.ResponseWriter, r *http.Request, cartID, offerID string) {
	if r.URL.Query().Get("from") == "offer" {
		a.repaintOfferCarts(w, r, offerID)
		return
	}
	// Default: the batch page. Adding happens from the offer page, so an add with
	// no marker is also from there — but a removal from the batch page needs the
	// batch re-rendered, and that is the case the marker distinguishes.
	if r.Method == http.MethodPost {
		a.repaintOfferCarts(w, r, offerID)
		return
	}
	a.repaintCart(w, r, cartID)
}

// repaintOfferCarts re-renders the offer page's batch card.
func (a *App) repaintOfferCarts(w http.ResponseWriter, r *http.Request, offerID string) {
	d, err := a.offerDetail(r, offerID)
	if err != nil {
		a.flash(w, r, "offer-flash", "error", a.userMessage(err))
		return
	}
	sse := render.NewSSE(w, r)
	_ = sse.PatchElementTempl(view.OfferCartsCard(d))
}

// repaintCart re-renders the batch page.
func (a *App) repaintCart(w http.ResponseWriter, r *http.Request, cartID string) {
	cart, err := a.Carts.Cart(r.Context(), cartID)
	if err != nil {
		a.flash(w, r, "cart-msg", "error", a.userMessage(err))
		return
	}
	send, held, err := a.Cart.ExportSet(r.Context(), cartID)
	if err != nil {
		a.flash(w, r, "cart-msg", "error", a.userMessage(err))
		return
	}
	s := view.CartScreen{
		Page:     a.page(r, cart.Name, "carts"),
		Cart:     cart,
		Send:     send,
		Held:     held,
		Profiles: export.Names(),
	}
	sse := render.NewSSE(w, r)
	// The whole screen rather than a row: removing an item can move others
	// between the "ready" and "held back" tables, and sending the enclosing
	// fragment is both simpler and cheaper than reasoning about which moved.
	_ = sse.PatchElementTempl(view.CartDetailScreen(s))
}
