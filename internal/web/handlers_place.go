package web

import (
	"net/http"
	"strings"

	"github.com/starfederation/datastar-go/datastar"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
	"github.com/atvirokodosprendimai/app-warehousev1/internal/web/render"
	"github.com/atvirokodosprendimai/app-warehousev1/internal/web/view"
)

// placeSignals is what the edit-a-place screen sends.
type placeSignals struct {
	Code      string `json:"placeCode"`
	Parent    string `json:"placeParent"`
	Label     string `json:"placeLabel"`
	Custodian string `json:"placeCustodian"`
	Contact   string `json:"placeContact"`
	City      string `json:"placeCity"`
	Country   string `json:"placeCountry"`
	Notes     string `json:"placeNotes"`
}

// GetPlace renders one storage place for editing.
func (a *App) GetPlace(w http.ResponseWriter, r *http.Request) {
	d, err := a.placeDetail(r, param(r, "id"))
	if err != nil {
		a.notFound(w, r, err)
		return
	}
	_ = view.PageShell(d.Page, nil, view.PlaceScreen(d)).Render(r.Context(), w)
}

// placeDetail builds the edit-a-place read model.
func (a *App) placeDetail(r *http.Request, id string) (view.PlaceDetail, error) {
	ctx := r.Context()

	node, err := a.Locations.Location(ctx, id)
	if err != nil {
		return view.PlaceDetail{}, err
	}

	all, err := a.Locations.AllLocations(ctx)
	if err != nil {
		return view.PlaceDetail{}, err
	}

	d := view.PlaceDetail{
		Page:  a.page(r, node.Path, "warehouse"),
		Place: node,
	}

	// Somewhere it could be moved to. A node may not move into itself or into
	// anything beneath it, and offering those would be offering a cycle the
	// service is only going to refuse.
	prefix := node.Path + "/"
	for _, l := range all {
		if l.ID == node.ID || strings.HasPrefix(l.Path, prefix) {
			continue
		}
		d.Parents = append(d.Parents, l)
	}

	if ancestors, err := a.Locations.Ancestors(ctx, node.ID); err == nil {
		d.Where = node.Where(ancestors)
	}
	if kids, err := a.Locations.Children(ctx, node.ID); err == nil {
		d.Children = len(kids)
	}
	// Stock at or beneath here decides whether deleting is even offered.
	if held, err := a.Offers.Offers(ctx, core.OfferFilter{
		LocationPathPrefix: node.Path, Limit: 10000,
	}); err == nil {
		d.Stock = len(held)
	}
	return d, nil
}

// PostPlaceDetails saves what a place IS, without moving it.
func (a *App) PostPlaceDetails(w http.ResponseWriter, r *http.Request) {
	var in placeSignals
	if err := datastar.ReadSignals(r, &in); err != nil {
		a.flash(w, r, "place-flash", "error", "Could not read the form.")
		return
	}
	id := param(r, "id")

	err := a.Location.UpdateDetails(r.Context(), id, core.Location{
		Label:            in.Label,
		Custodian:        in.Custodian,
		CustodianContact: in.Contact,
		City:             in.City,
		Country:          in.Country,
		Notes:            in.Notes,
	})
	if err != nil {
		a.flash(w, r, "place-flash", "error", a.userMessage(err))
		return
	}
	a.repaintPlace(w, r, id, "Saved.")
}

// PostPlaceRename changes a place's code, and with it the address of everything
// beneath it.
func (a *App) PostPlaceRename(w http.ResponseWriter, r *http.Request) {
	var in placeSignals
	if err := datastar.ReadSignals(r, &in); err != nil {
		a.flash(w, r, "place-flash", "error", "Could not read the form.")
		return
	}
	id := param(r, "id")

	if err := a.Location.Rename(r.Context(), id, strings.TrimSpace(in.Code)); err != nil {
		a.flash(w, r, "place-flash", "error", a.userMessage(err))
		return
	}
	// Every descendant's path changed with it, so the whole screen is re-rendered
	// rather than the one field: the paths shown beside the children are now
	// different strings.
	a.repaintPlace(w, r, id, "Renamed. Everything inside it has a new address too.")
}

// PostPlaceMove moves a place, and everything in it, somewhere else.
func (a *App) PostPlaceMove(w http.ResponseWriter, r *http.Request) {
	var in placeSignals
	if err := datastar.ReadSignals(r, &in); err != nil {
		a.flash(w, r, "place-flash", "error", "Could not read the form.")
		return
	}
	id := param(r, "id")

	if err := a.Location.Move(r.Context(), id, strings.TrimSpace(in.Parent)); err != nil {
		a.flash(w, r, "place-flash", "error", a.userMessage(err))
		return
	}
	a.repaintPlace(w, r, id, "Moved. The stock did not go anywhere — only its address changed.")
}

// PostPlaceDelete removes an empty place.
//
// The service refuses one that still has children or still holds stock, and this
// handler does not second-guess it: emptying a shelf has to be a deliberate act
// of moving the stock, never a side effect of tidying the tree.
func (a *App) PostPlaceDelete(w http.ResponseWriter, r *http.Request) {
	id := param(r, "id")

	if err := a.Location.Delete(r.Context(), id); err != nil {
		a.flash(w, r, "place-flash", "error", a.userMessage(err))
		return
	}
	sse := render.NewSSE(w, r)
	_ = sse.ExecuteScript("window.location.href = '/warehouse'")
}

// repaintPlace re-renders the edit screen after a change.
func (a *App) repaintPlace(w http.ResponseWriter, r *http.Request, id, note string) {
	d, err := a.placeDetail(r, id)
	if err != nil {
		a.flash(w, r, "place-flash", "error", a.userMessage(err))
		return
	}
	sse := render.NewSSE(w, r)
	_ = sse.PatchElementTempl(view.PlaceBody(d))
	_ = sse.PatchElementTempl(view.Flash("place-flash", "ok", note))
}
