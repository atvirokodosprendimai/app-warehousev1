package web

import (
	"embed"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

// assets holds the stylesheet, served from the binary so a deployment is one
// file with no directory of static content beside it to get out of step.
//
//go:embed assets
var assets embed.FS

// Routes builds the HTTP handler.
//
// The route table is the clearest statement of the application's trust
// boundaries, so it is written in one place rather than scattered across
// per-feature files: the three groups below are "anyone", "signed in" and
// "administrator", and nothing else is reachable.
func (a *App) Routes() http.Handler {
	r := chi.NewRouter()

	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	// RequestLogger before Recoverer, so a panic is recorded against its request
	// with the request_id attached rather than only as Recoverer's stack dump.
	r.Use(middleware.RequestLogger(slogFormatter{log: a.Log}))
	r.Use(middleware.Recoverer)
	// The session middleware must wrap everything that reads a session, which is
	// everything: the sign-in page writes one and the rest read it.
	r.Use(a.Sessions.LoadAndSave)

	// ---- public ----
	//
	// Photographs are deliberately here rather than behind the session. Shopify
	// and eBay fetch listing images from their own servers with no credential of
	// ours, so an authenticated image URL yields a listing with no pictures and
	// no error. The unguessable UUID in the path is the capability.
	r.Get("/p/{name}", a.GetPhoto)
	r.Handle("/assets/*", staticHandler())

	r.Get("/login", a.GetLogin)
	r.Post("/login", a.PostLogin)
	r.Get("/bootstrap", a.GetBootstrap)
	r.Post("/bootstrap", a.PostBootstrap)
	r.Get("/logout", a.GetLogout)

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// ---- signed in ----
	r.Group(func(r chi.Router) {
		r.Use(a.requireUser)

		r.Get("/", a.GetDashboard)

		r.Get("/offers", a.GetOffers)
		// The live search asks for just the table. Status stays in the page URL
		// and the text and price terms ride as signals.
		r.Get("/offers/rows", a.GetOfferRows)
		r.Post("/offers", a.PostOffers)
		r.Get("/offers/{id}", a.GetOffer)
		r.Post("/offers/{id}", a.PostOffer)
		r.Post("/offers/{id}/prices", a.PostPrices)
		r.Post("/offers/{id}/status/{status}", a.PostStatus)
		r.Get("/offers/{id}/sold-dialog", a.GetSoldDialog)
		r.Post("/offers/{id}/sold", a.PostSold)
		r.Post("/offers/{id}/location", a.PostOfferLocation)
		r.Post("/offers/{id}/category", a.PostOfferCategory)
		// ⚠ Two routes, two different meanings of "category". `/category` above is
		// the MARKETPLACE one (ADR-016): where to list this on eBay. `/fields` here
		// stores the answers to whatever the operator's OWN taxonomy asks about it
		// (ADR-021). The offer's taxonomy node itself is saved by `POST /offers/{id}`
		// with the rest of the Details card.
		r.Post("/offers/{id}/fields", a.PostOfferFields)
		r.Post("/offers/{id}/photos", a.PostPhotos)
		// One SSE endpoint for the whole application. The query says what this
		// page needs beyond what every page needs; the server decides the rest.
		r.Get("/stream", a.GetStream)
		r.Delete("/photos/{id}", a.DeletePhoto)
		r.Get("/modal/close", a.GetModalClose)

		r.Get("/carts", a.GetCarts)
		// Adding from the offers listing, which is where a cart is actually built.
		r.Post("/carts/add/{offerID}", a.PostCartAdd)
		r.Post("/carts/active", a.PostCartActive)
		r.Post("/carts", a.PostCarts)
		r.Get("/carts/{id}", a.GetCart)
		r.Post("/carts/{id}/items/{offerID}", a.PostCartItem)
		r.Delete("/carts/{id}/items/{offerID}", a.DeleteCartItem)

		r.Get("/warehouse", a.GetWarehouse)
		r.Get("/warehouse/new", a.GetNewLocation)
		r.Post("/warehouse", a.PostWarehouse)
		r.Get("/warehouse/{id}", a.GetPlace)
		r.Post("/warehouse/{id}/details", a.PostPlaceDetails)
		r.Post("/warehouse/{id}/rename", a.PostPlaceRename)
		r.Post("/warehouse/{id}/move", a.PostPlaceMove)
		r.Post("/warehouse/{id}/delete", a.PostPlaceDelete)

		r.Get("/export", a.GetExport)
		r.Get("/export/{file}", a.GetExportFile)

		// Anyone signed in may offer the warehouse something. Reading one back is
		// gated on the ROW rather than the route — a submitter sees their own, an
		// administrator sees any — so those checks live in the handler.
		r.Get("/submit", a.GetSubmit)
		r.Post("/submit", a.PostSubmit)
		r.Get("/submit/{id}", a.GetSubmission)
		r.Post("/submit/{id}/photos", a.PostSubmissionPhoto)
	})

	// ---- administrator ----
	r.Group(func(r chi.Router) {
		r.Use(a.requireUser, a.requireAdmin)

		r.Get("/inbox", a.GetInbox)
		r.Get("/settings", a.GetSettings)
		r.Post("/settings", a.PostSettings)
		// ⚠ ADMIN, deliberately. A cataloguer FILES an offer under a category;
		// changing what categories EXIST changes what every offer in the warehouse
		// can say about itself, which is the same standing as changing a setting.
		r.Get("/categories", a.GetTaxonomy)
		r.Post("/categories", a.PostCategories)
		// One press builds a whole starter tree, so it is a POST on its own path
		// rather than a variant of the create above: what it does to the taxonomy
		// is not what typing one category does.
		r.Post("/categories/template/{code}", a.PostCategoryTemplate)
		r.Post("/categories/{id}", a.PostCategory)
		r.Post("/categories/{id}/delete", a.PostCategoryDelete)
		r.Post("/categories/{id}/fields", a.PostCategoryFields)
		r.Post("/fields/{fieldID}/delete", a.PostFieldDelete)
		r.Post("/submit/{id}/review", a.PostStartReview)
		r.Post("/submit/{id}/decline", a.PostDecline)
		r.Post("/submit/{id}/accept", a.PostAccept)
	})

	// ---- administrator ----
	r.Group(func(r chi.Router) {
		r.Use(a.requireUser, a.requireAdmin)

		r.Get("/users", a.GetUsers)
		r.Post("/users", a.PostUsers)
	})

	return r
}

// staticHandler serves the embedded assets with a long cache lifetime.
func staticHandler() http.Handler {
	fs := http.FileServer(http.FS(assets))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The stylesheet changes only when the binary does, so a browser may hold
		// it for a while — but not forever, because there is no content hash in
		// the URL to bust it with after a deploy.
		w.Header().Set("Cache-Control", "public, max-age=3600")
		fs.ServeHTTP(w, r)
	})
}

// ReadHeaderTimeout bounds how long a client may take to send its headers.
//
// ⚠ There is deliberately NO WriteTimeout on the server this is used with. An
// SSE response is meant to last as long as the page is open, and a write
// deadline applies to the whole response — so any non-zero value silently kills
// every healthy stream at that mark, with no error in the handler and nothing in
// the log. Streams clear their own deadline in render.NewSSE; leaving the server
// default at zero is the other half of that.
const ReadHeaderTimeout = 10 * time.Second
