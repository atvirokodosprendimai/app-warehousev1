package web

import (
	"net/http"
	"time"

	"github.com/starfederation/datastar-go/datastar"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
	"github.com/atvirokodosprendimai/app-warehousev1/internal/web/render"
	"github.com/atvirokodosprendimai/app-warehousev1/internal/web/view"
)

// sessionCartKey is the session field holding which cart Add targets.
//
// Per person rather than global: two people going round the warehouse are
// building different carts, and a shared "current" would have each of them
// dropping items into the other's.
const sessionCartKey = "active_cart"

// cartBarSignals is what the offers screen's cart control sends.
type cartBarSignals struct {
	ActiveCart string `json:"activeCart"`
}

// activeCart returns the cart Add should target, and whether it still exists.
//
// It re-reads the cart rather than trusting the session, because the id there
// may name a cart somebody has since deleted — and adding to a deleted cart
// would otherwise fail with a foreign-key error rather than quietly picking a
// live one.
func (a *App) activeCart(r *http.Request, carts []core.Cart) string {
	want := a.Sessions.GetString(r.Context(), sessionCartKey)
	for _, c := range carts {
		if c.ID == want {
			return want
		}
	}
	if len(carts) > 0 {
		// Fall back to the most recently touched, which is almost always the one
		// being worked on.
		return carts[0].ID
	}
	return ""
}

// PostCartActive records which cart the Add buttons target.
func (a *App) PostCartActive(w http.ResponseWriter, r *http.Request) {
	var in cartBarSignals
	if err := datastar.ReadSignals(r, &in); err != nil {
		a.flash(w, r, "offer-flash", "error", "Could not read the selection.")
		return
	}
	a.Sessions.Put(r.Context(), sessionCartKey, in.ActiveCart)
	a.repaintCartBar(w, r, in.ActiveCart)
}

// PostCartAdd puts an offer into the active cart.
//
// ⚠ IT CREATES A CART WHEN THERE IS NONE, rather than refusing. That is the flow
// as described: you add things, and then you name what you have collected. An
// empty state that says "create a cart first" makes the first useful action a
// detour through another screen, which is precisely how the feature became
// undiscoverable in the first place.
func (a *App) PostCartAdd(w http.ResponseWriter, r *http.Request) {
	var in cartBarSignals
	if err := datastar.ReadSignals(r, &in); err != nil {
		a.Log.Warn("cart signals unreadable", "err", err)
	}
	offerID := param(r, "offerID")

	carts, err := a.Carts.Carts(r.Context())
	if err != nil {
		a.flash(w, r, "offer-flash", "error", a.userMessage(err))
		return
	}

	cartID := in.ActiveCart
	if !cartExists(carts, cartID) {
		cartID = a.activeCart(r, carts)
	}

	created := false
	if cartID == "" {
		c, err := a.Cart.Create(r.Context(), defaultCartName(now()), "")
		if err != nil {
			a.flash(w, r, "offer-flash", "error", a.userMessage(err))
			return
		}
		cartID = c.ID
		created = true
	}

	if err := a.Cart.Add(r.Context(), cartID, offerID); err != nil {
		a.flash(w, r, "offer-flash", "error", a.userMessage(err))
		return
	}
	a.Sessions.Put(r.Context(), sessionCartKey, cartID)

	sse := render.NewSSE(w, r)
	a.patchCartBar(sse, r, cartID)
	if created {
		// The select had no value to bind to before this; tell the page which cart
		// it is now working with, or the next Add would create a second one.
		_ = sse.MarshalAndPatchSignals(cartBarSignals{ActiveCart: cartID})
	}
}

// defaultCartName names a cart nobody has named yet.
//
// Dated rather than "Untitled", so a person who collects things on two days can
// tell the two apart before renaming either.
func defaultCartName(t time.Time) string {
	return "Cart " + t.Format("2 Jan")
}

// cartExists reports whether id names one of the carts.
func cartExists(carts []core.Cart, id string) bool {
	for _, c := range carts {
		if c.ID == id {
			return true
		}
	}
	return false
}

// repaintCartBar re-renders the cart control on its own.
func (a *App) repaintCartBar(w http.ResponseWriter, r *http.Request, cartID string) {
	sse := render.NewSSE(w, r)
	a.patchCartBar(sse, r, cartID)
}

// patchCartBar sends the cart control down an already-open stream.
func (a *App) patchCartBar(sse *datastar.ServerSentEventGenerator, r *http.Request, cartID string) {
	carts, err := a.Carts.Carts(r.Context())
	if err != nil {
		a.Log.Warn("carts unavailable", "err", err)
		return
	}
	_ = sse.PatchElementTempl(view.CartBar(view.OfferList{
		Carts:      carts,
		ActiveCart: cartID,
	}))
}
