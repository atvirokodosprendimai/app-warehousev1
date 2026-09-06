package web

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/starfederation/datastar-go/datastar"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
	"github.com/atvirokodosprendimai/app-warehousev1/internal/export"
	"github.com/atvirokodosprendimai/app-warehousev1/internal/web/render"
	"github.com/atvirokodosprendimai/app-warehousev1/internal/web/view"
)

// GetWarehouse renders the storage tree.
func (a *App) GetWarehouse(w http.ResponseWriter, r *http.Request) {
	nodes, err := a.locationNodes(r)
	if err != nil {
		a.Log.Error("load warehouse", "err", err)
		http.Error(w, "Could not load storage.", http.StatusInternalServerError)
		return
	}
	t := view.LocationTree{Page: a.page(r, "Warehouse", "warehouse"), Nodes: nodes}
	_ = view.PageShell(t.Page, view.AddPlaceAction(""), view.WarehouseScreen(t)).Render(r.Context(), w)
}

// locationNodes builds the flattened tree with each node's resolved placement
// and stock count.
func (a *App) locationNodes(r *http.Request) ([]view.LocationNode, error) {
	ctx := r.Context()
	all, err := a.Locations.AllLocations(ctx)
	if err != nil {
		return nil, err
	}

	// Index by path so a node's ancestors can be assembled without a query each.
	// AllLocations is ordered by path, so a parent always precedes its children.
	byPath := make(map[string]core.Location, len(all))
	for _, l := range all {
		byPath[l.Path] = l
	}

	nodes := make([]view.LocationNode, 0, len(all))
	for _, l := range all {
		segments := strings.Split(l.Path, "/")
		depth := len(segments) - 1

		ancestors := make([]core.Location, 0, depth)
		for i := 1; i <= depth; i++ {
			if anc, ok := byPath[strings.Join(segments[:i], "/")]; ok {
				ancestors = append(ancestors, anc)
			}
		}

		n := view.LocationNode{Location: l, Depth: depth, Where: l.Where(ancestors)}
		// Stock at or beneath this node. The prefix filter is the repository's
		// job; asking per node is acceptable because a storage tree is small and
		// the alternative is a second aggregate nobody has asked for yet.
		if held, err := a.Offers.Offers(ctx, core.OfferFilter{
			LocationPathPrefix: l.Path, Limit: 10000,
		}); err == nil {
			n.Stock = len(held)
		}
		nodes = append(nodes, n)
	}
	return nodes, nil
}

// GetNewLocation renders the add-a-place screen.
//
// A `parent` query parameter preselects the enclosing place, so "add" from a row
// in the warehouse table lands on a form that already knows where you are adding
// to. Without it the operator has to find the same place again in a select box
// they just clicked next to.
func (a *App) GetNewLocation(w http.ResponseWriter, r *http.Request) {
	parents, err := a.Locations.AllLocations(r.Context())
	if err != nil {
		a.Log.Warn("locations unavailable", "err", err)
	}

	preset := strings.TrimSpace(r.URL.Query().Get("parent"))
	if preset != "" {
		// Validate it rather than reflecting it into the form: an id that names
		// nothing would render a select with a phantom option selected, and the
		// create would then fail for a reason the operator cannot see.
		if _, err := a.Locations.Location(r.Context(), preset); err != nil {
			preset = ""
		}
	}

	p := a.page(r, "Add a place", "warehouse")
	_ = view.PageShell(p, nil, view.NewLocationScreen(parents, preset)).Render(r.Context(), w)
}

// locationSignals is what the add-a-place screen sends.
type locationSignals struct {
	Parent    string `json:"locParent"`
	Kind      string `json:"locKind"`
	Code      string `json:"locCode"`
	Label     string `json:"locLabel"`
	Custodian string `json:"locCustodian"`
	Contact   string `json:"locContact"`
	City      string `json:"locCity"`
	Country   string `json:"locCountry"`
}

// PostWarehouse adds a node to the storage tree.
func (a *App) PostWarehouse(w http.ResponseWriter, r *http.Request) {
	var in locationSignals
	if err := datastar.ReadSignals(r, &in); err != nil {
		a.flash(w, r, "loc-msg", "error", "Could not read the form.")
		return
	}

	kind, err := core.ParseKind(in.Kind)
	if err != nil {
		a.flash(w, r, "loc-msg", "error", a.userMessage(err))
		return
	}

	l := core.Location{
		Kind:             kind,
		Code:             strings.TrimSpace(in.Code),
		Label:            strings.TrimSpace(in.Label),
		Custodian:        strings.TrimSpace(in.Custodian),
		CustodianContact: strings.TrimSpace(in.Contact),
		City:             strings.TrimSpace(in.City),
		Country:          strings.TrimSpace(in.Country),
	}

	created, err := a.Location.Create(r.Context(), in.Parent, l)
	if err != nil {
		a.flash(w, r, "loc-msg", "error", a.userMessage(err))
		return
	}

	sse := render.NewSSE(w, r)
	_ = sse.ExecuteScript("window.location.href = '/warehouse'")
	a.Log.Info("location created", "path", created.Path)
}

// GetPhoto serves a photograph.
//
// ⚠ THIS ROUTE IS DELIBERATELY UNAUTHENTICATED, and that is the feature rather
// than an oversight. Shopify and eBay fetch listing images from their own
// servers, with no session and no credential of ours — an authenticated photo
// URL produces a listing with no pictures and no error message anywhere. The
// UUID in the path IS the capability: it is unguessable, it is not enumerable,
// and it is only ever published for an offer the operator chose to list.
func (a *App) GetPhoto(w http.ResponseWriter, r *http.Request) {
	name := param(r, "name")

	// The URL carries an extension so that a fetcher which sniffs the path
	// rather than the header still gets it right; the store is keyed by the bare
	// UUID.
	id := name
	if i := strings.LastIndexByte(name, '.'); i > 0 {
		id = name[:i]
	}

	data, err := a.Blobs.Open(r.Context(), id)
	if err != nil {
		// A bad id and a missing blob are both simply "not here" to a fetcher.
		// Distinguishing them would confirm which ids exist.
		http.NotFound(w, r)
		return
	}

	ct := http.DetectContentType(data)
	w.Header().Set("Content-Type", ct)
	// The bytes at a UUID never change — a new photograph gets a new id — so this
	// can be cached hard. That matters: a marketplace may re-fetch an image many
	// times across a listing's life.
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, name, time.Time{}, bytesReader(data))
}

// GetExport renders the export screen.
func (a *App) GetExport(w http.ResponseWriter, r *http.Request) {
	p := a.page(r, "Export", "export")
	base := a.Cfg.PublicBaseURL
	absolute := strings.HasPrefix(base, "http://") || strings.HasPrefix(base, "https://")

	carts, err := a.Carts.Carts(r.Context())
	if err != nil {
		a.Log.Warn("carts unavailable", "err", err)
	}

	// Work out what a catalogue export would and would not carry, and show BOTH
	// before anything is clicked. The exporter refuses an offer it cannot render
	// rather than dropping it, so without this the operator meets that refusal as
	// a failed download naming a SKU — after choosing to export, with nothing on
	// screen explaining which items are the problem or why.
	send, held := a.catalogue(r)

	_ = view.PageShell(p, nil,
		view.ExportScreen(a.exportProfiles(), base, absolute, carts, len(send), held)).Render(r.Context(), w)
}

// exportOptions builds the marketplace settings for an export run.
func (a *App) exportOptions() export.Options {
	opt := a.Cfg.Export
	if opt.Currency == "" {
		opt.Currency = core.BaseCurrency
	}
	opt.BaseURL = a.Cfg.PublicBaseURL
	return opt
}

// exportProfiles reports which marketplaces this deployment can actually export
// to, and why not when it cannot.
//
// ★ It asks each exporter rather than restating its requirements here. Every
// profile validates its options BEFORE it looks at a single offer, so rendering
// an empty export to io.Discard is precisely an options check — and it costs
// nothing, because a refusal happens before any row is written.
//
// The alternative was for this package to know that eBay needs a category and an
// item location. That is a second copy of a rule owned somewhere else, and the
// copy goes stale the first time a profile gains a required field: the page
// would go on offering a download that then fails with a raw 400, which is the
// exact defect this replaces.
func (a *App) exportProfiles() []view.ExportProfile {
	opt := a.exportOptions()
	out := make([]view.ExportProfile, 0, len(export.Names()))

	for _, name := range export.Names() {
		exp, err := export.For(name)
		if err != nil {
			continue
		}
		p := view.ExportProfile{Name: name, Ready: true}
		if err := exp.Write(io.Discard, nil, opt); err != nil {
			p.Ready = false
			p.Problem = err.Error()
		}
		out = append(out, p)
	}
	return out
}

// catalogue returns everything a whole-catalogue export would publish, and
// everything it is holding back.
func (a *App) catalogue(r *http.Request) (send []core.Offer, held []core.Offer) {
	offers, err := a.Offers.Offers(r.Context(), core.OfferFilter{
		Status: []core.Status{core.StatusListed, core.StatusPending},
		Limit:  10000,
	})
	if err != nil {
		a.Log.Error("catalogue", "err", err)
		return nil, nil
	}
	return core.PartitionExportable(offers)
}

// GetExportFile streams a marketplace CSV.
//
// A `cart` query parameter exports exactly one saved batch; without it, the
// whole catalogue's exportable stock is exported.
func (a *App) GetExportFile(w http.ResponseWriter, r *http.Request) {
	name := param(r, "file")
	profile := strings.TrimSuffix(name, ".csv")

	exp, err := export.For(profile)
	if err != nil {
		http.Error(w, a.userMessage(err), http.StatusNotFound)
		return
	}

	offers, filename, err := a.exportSet(r, profile)
	if err != nil {
		// An export is a download, not a datastar action, so a failure here is a
		// real status code the browser will show rather than an HTML fragment.
		http.Error(w, a.userMessage(err), http.StatusBadRequest)
		return
	}

	opt := a.exportOptions()

	// Render into memory first. Write streams straight to w, and a failure
	// halfway through would otherwise leave a 200 carrying half a catalogue that
	// imports without complaint.
	var buf writeBuffer
	if err := exp.Write(&buf, offers, opt); err != nil {
		http.Error(w, a.userMessage(err), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(buf.Bytes())
}

// exportSet chooses what an export run covers.
func (a *App) exportSet(r *http.Request, profile string) ([]core.Offer, string, error) {
	cartID := strings.TrimSpace(r.URL.Query().Get("cart"))
	if cartID == "" {
		// Only the ready ones. What is being held back is shown on the export
		// page rather than being discovered as a failed download, and it is the
		// same partition that page rendered — one function, so the page cannot
		// promise something the exporter then refuses.
		send, _ := a.catalogue(r)
		if len(send) == 0 {
			return nil, "", errors.New("nothing is ready to export yet — the export " +
				"page lists what each item is waiting for")
		}
		return send, profile + "-all.csv", nil
	}

	send, _, err := a.Cart.ExportSet(r.Context(), cartID)
	if err != nil {
		return nil, "", err
	}
	if len(send) == 0 {
		return nil, "", errors.New("nothing in that batch can be exported yet — " +
			"open it to see what is holding each item back")
	}
	return send, profile + "-" + cartID + ".csv", nil
}

// bytesReader adapts a byte slice for http.ServeContent, which needs a
// ReadSeeker so it can answer a Range request — which is how a fetcher resumes a
// partially downloaded image rather than starting again.
func bytesReader(b []byte) *bytes.Reader { return bytes.NewReader(b) }

// writeBuffer is where an export is rendered before any of it reaches the client.
//
// The exporter streams, so writing straight to the response would mean a failure
// halfway through leaves a 200 carrying half a catalogue — and a truncated CSV
// imports without complaint, listing whatever happened to fit.
type writeBuffer = bytes.Buffer
