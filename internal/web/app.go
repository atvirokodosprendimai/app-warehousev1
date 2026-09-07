// Package web is the HTTP boundary: routes, handlers, and the read models they
// hand to templ.
//
// Handlers follow the project's CQRS split. A WRITE goes through a domain
// service, which owns the rules; a READ goes straight to a repository, because a
// read model is a function of an id and has no rules to own. That asymmetry is
// the split, and it is visible in every handler below.
package web

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/alexedwards/scs/v2"
	"github.com/go-chi/chi/v5"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/auth"
	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
	"github.com/atvirokodosprendimai/app-warehousev1/internal/export"
	"github.com/atvirokodosprendimai/app-warehousev1/internal/location"
	"github.com/atvirokodosprendimai/app-warehousev1/internal/offer"
	"github.com/atvirokodosprendimai/app-warehousev1/internal/web/render"
	"github.com/atvirokodosprendimai/app-warehousev1/internal/web/view"
)

// Config is what the HTTP layer needs to know about its deployment.
type Config struct {
	// PublicBaseURL is the origin a marketplace fetches photos from.
	PublicBaseURL string
	// Export carries the marketplace-specific fields an export needs.
	Export export.Options
}

// App holds the handlers' dependencies.
//
// The read side and the write side are held SEPARATELY and typed differently on
// purpose. Reads are declared as core.*Reader, so a handler that meant to read
// cannot write even by accident — the interface it was given has no method that
// could. Writes go through the domain services.
type App struct {
	Sessions *scs.SessionManager
	Log      *slog.Logger
	Cfg      Config

	// Read ports. Deliberately the narrow half.
	Users     core.UserReader
	Offers    core.OfferReader
	Locations core.LocationReader
	Rates     core.RateReader
	Carts     core.CartReader

	// Write services.
	Auth     *auth.Service
	Offer    *offer.Service
	Location *location.Service
	Cart     CartService

	// Blobs serves photo bytes.
	Blobs core.BlobStore

	// Bus fans "this id changed" out to the open SSE streams in this process.
	// A nil Bus disables live updates rather than panicking, which keeps a test
	// that only exercises page rendering from having to build one.
	Bus *Bus

	// Submissions is the staff-proposal read side, used by the inbox and by the
	// live banner on every page.
	Submissions core.SubmissionReader
	// Submission is the proposal write side.
	Submission SubmissionService

	// Settings holds the deployment values an administrator can change while the
	// application is running.
	Settings core.SettingsStore
}

// publicBase returns the origin a marketplace fetches photographs from.
//
// The STORED setting wins over the environment, because the environment is set
// by whoever starts the process and the mistake is usually noticed by somebody
// else, after an export has already gone out. Falling back to the environment
// keeps a deployment that never opens the settings page working exactly as it
// did before.
//
// A read failure falls back rather than erroring: a photograph link built from a
// stale-but-configured origin is repairable, and refusing to render the page at
// all because a settings row could not be read is not.
func (a *App) publicBase(ctx context.Context) string {
	if a.Settings != nil {
		if s, err := a.Settings.Settings(ctx); err == nil && s.PublicBaseURL != "" {
			return s.PublicBaseURL
		} else if err != nil {
			a.Log.Warn("settings unreadable, using the configured default", "err", err)
		}
	}
	return a.Cfg.PublicBaseURL
}

// ebayMarketplace resolves eBay's per-run configuration for THIS request.
//
// It mirrors publicBase, and for the same reason: a value read once at start-up
// can only be corrected by whoever can restart the process, and it is discovered
// to be wrong after an export has already gone out. Resolving per request is
// what makes the Settings page take effect without a restart.
//
// ⚠ Resolution is per FIELD (see [core.Marketplace.Resolve]). An administrator
// who sets only the category must not thereby blank the condition and location
// the environment supplied.
func (a *App) ebayMarketplace(ctx context.Context) core.Marketplace {
	def := core.Marketplace{
		Category:    a.Cfg.Export.Category,
		ConditionID: a.Cfg.Export.ConditionID,
		Location:    a.Cfg.Export.Location,
	}
	if a.Settings != nil {
		s, err := a.Settings.Settings(ctx)
		if err != nil {
			a.Log.Warn("settings unreadable, using the configured eBay defaults", "err", err)
			return def
		}
		return s.Ebay.Resolve(def)
	}
	return def
}

// CartService is the slice of the cart write side the HTTP layer uses.
//
// It is declared here, at the consumer, rather than exported from the cart
// package, so that the handler depends on exactly what it calls. That is what
// "accept interfaces" buys: the cart package returns a concrete *Service and
// this package names the four methods it actually needs.
type CartService interface {
	Create(ctx context.Context, name, note string) (core.Cart, error)
	Rename(ctx context.Context, id, name, note string) error
	Add(ctx context.Context, cartID, offerID string) error
	Remove(ctx context.Context, cartID, offerID string) error
	Delete(ctx context.Context, id string) error
	ExportSet(ctx context.Context, cartID string) (send []core.Offer, held []core.Offer, err error)
}

// SubmissionService is the slice of the submission write side the HTTP layer
// uses, declared here at the consumer so the handler depends on exactly what it
// calls.
type SubmissionService interface {
	Submit(ctx context.Context, user core.User, title, note string, asking core.Money) (core.Submission, error)
	AddPhoto(ctx context.Context, submissionID, filename, contentType string, data []byte) (core.Photo, error)
	StartReview(ctx context.Context, admin core.User, id string) error
	Decline(ctx context.Context, admin core.User, id, reason string) error
	Accept(ctx context.Context, admin core.User, id string, c core.Conversion) (core.Offer, error)
}

// page builds the chrome every signed-in screen shares.
//
// The per-status counts are loaded here rather than by each screen, because the
// sidebar shows them on every page and a screen that forgot would render a
// sidebar full of zeroes that looks like an empty warehouse.
func (a *App) page(r *http.Request, title, nav string) view.Page {
	u, _ := UserFrom(r.Context())
	p := view.Page{Title: title, Nav: nav, User: u}

	counts, err := a.Offers.CountByStatus(r.Context())
	if err != nil {
		// A sidebar badge is not worth failing a page over; log it and render the
		// page without counts rather than showing the operator an error screen
		// because a decoration could not be computed.
		a.Log.Warn("status counts unavailable", "err", err)
		return p
	}
	p.Counts = counts

	pending, err := a.Offers.Offers(r.Context(), core.OfferFilter{NeedsPricing: true, Limit: 1000})
	if err == nil {
		p.NeedsPricing = len(pending)
	}

	// The describing queue's count, loaded exactly as the pricing one above and
	// with the same warn-and-continue handling: a sidebar badge is not worth
	// failing a page over.
	unnamed, err := a.Offers.Offers(r.Context(), core.OfferFilter{NeedsDescribing: true, Limit: 1000})
	if err == nil {
		p.NeedsDescribing = len(unnamed)
	}

	// Every page opens exactly one SSE connection, and this is it. A screen that
	// needs more than the shared fragments appends to this path rather than
	// opening a second stream.
	p.StreamPath = "/stream"

	// The banner is carried on every page because its job is to reach an
	// administrator who is looking somewhere else. Only an administrator can act
	// on the queue, so nobody else pays for the count.
	if u.IsAdmin && a.Submissions != nil {
		if n, err := a.Submissions.CountOpen(r.Context()); err == nil {
			p.InboxOpen = n
		}
	}
	return p
}

// row builds one listing row, resolving where the offer physically is.
func (a *App) row(ctx context.Context, o core.Offer) view.OfferRow {
	out := view.OfferRow{Offer: o}
	if o.LocationID == "" {
		return out
	}
	l, err := a.Locations.Location(ctx, o.LocationID)
	if err != nil {
		return out
	}
	out.LocationPath = l.Path
	ancestors, err := a.Locations.Ancestors(ctx, l.ID)
	if err != nil {
		// Without ancestors the custodian and city cannot be inherited, so report
		// only what this node states itself rather than inventing a placement.
		out.Where = l.Where(nil)
		return out
	}
	out.Where = l.Where(ancestors)
	return out
}

// rows builds a listing, resolving each offer's placement.
//
// Locations are cached for the length of the call: a page of fifty offers on one
// shelf would otherwise ask for that shelf and its ancestors fifty times.
func (a *App) rows(ctx context.Context, offers []core.Offer) []view.OfferRow {
	type placed struct {
		path  string
		where core.Placement
	}
	seen := map[string]placed{}
	out := make([]view.OfferRow, 0, len(offers))

	for _, o := range offers {
		row := view.OfferRow{Offer: o}
		if o.LocationID != "" {
			p, ok := seen[o.LocationID]
			if !ok {
				r := a.row(ctx, o)
				p = placed{path: r.LocationPath, where: r.Where}
				seen[o.LocationID] = p
			}
			row.LocationPath = p.path
			row.Where = p.where
		}
		out = append(out, row)
	}
	return out
}

// requireUser is middleware that resolves the signed-in user and refuses anyone
// else.
//
// It loads the user from the DATABASE on every request rather than trusting a
// copy in the session. A cached copy would keep somebody signed in with stale
// rights after an admin disabled them or removed their admin flag — the change
// would not take effect until the cookie expired, which is the opposite of what
// disabling an account means.
func (a *App) requireUser(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := a.Sessions.GetString(r.Context(), sessionUserKey)
		if id == "" {
			a.redirectToLogin(w, r)
			return
		}
		u, err := a.Users.User(r.Context(), id)
		if err != nil || !u.Active() {
			// The account is gone or disabled: drop the session rather than
			// leaving a cookie that will be rejected on every future request.
			_ = a.Sessions.Destroy(r.Context())
			a.redirectToLogin(w, r)
			return
		}
		next.ServeHTTP(w, r.WithContext(WithUser(r.Context(), u)))
	})
}

// requireAdmin refuses a non-administrator.
func (a *App) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, ok := UserFrom(r.Context())
		if !ok || !u.IsAdmin {
			http.Error(w, "Administrators only.", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// redirectToLogin sends the browser to sign in, or refuses a datastar request.
//
// A datastar action expects an SSE body, so redirecting one would leave the page
// looking like nothing happened. Answering 401 lets the client surface a real
// failure instead.
func (a *App) redirectToLogin(w http.ResponseWriter, r *http.Request) {
	if isDatastar(r) {
		http.Error(w, "Your session has expired. Reload the page to sign in again.",
			http.StatusUnauthorized)
		return
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// isDatastar reports whether the request came from a datastar action rather than
// a browser navigation.
func isDatastar(r *http.Request) bool {
	return r.Header.Get("Datastar-Request") != "" ||
		r.URL.Query().Get("datastar") != "" ||
		r.Header.Get("Accept") == "text/event-stream"
}

// userMessage turns a domain error into something worth showing an operator.
//
// Domain errors are written to be read by people, so most are passed through.
// Anything unrecognised is logged and replaced: an unexpected error's text can
// carry a file path, a query or a driver detail, and none of that belongs on a
// screen a stranger might be looking at.
func (a *App) userMessage(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, core.ErrInvalid),
		errors.Is(err, core.ErrBadMoney),
		errors.Is(err, core.ErrNotFound),
		errors.Is(err, auth.ErrBadCredentials),
		errors.Is(err, auth.ErrEmailTaken),
		errors.Is(err, auth.ErrNotPermitted),
		errors.Is(err, auth.ErrLastAdmin),
		errors.Is(err, auth.ErrBootstrapClosed),
		errors.Is(err, offer.ErrSKUTaken),
		errors.Is(err, offer.ErrNoShopPrice),
		errors.Is(err, location.ErrCodeTaken),
		errors.Is(err, location.ErrPathTaken),
		errors.Is(err, location.ErrHasChildren),
		errors.Is(err, location.ErrLocationOccupied),
		errors.Is(err, location.ErrCycle),
		errors.Is(err, export.ErrIncomplete),
		errors.Is(err, export.ErrOptions),
		errors.Is(err, export.ErrUnknownProfile):
		return err.Error()
	}
	a.Log.Error("unexpected error", "err", err)
	return "Something went wrong. The details are in the server log."
}

// flash sends a single message fragment into a named slot and nothing else.
//
// Every datastar response is a 200 carrying HTML; an error is a FRAGMENT, not a
// status code. A 4xx here would leave the page unchanged and the operator with
// no idea why nothing happened.
func (a *App) flash(w http.ResponseWriter, r *http.Request, slot, kind, text string) {
	sse := render.NewSSE(w, r)
	_ = sse.PatchElementTempl(view.Flash(slot, kind, text))
}

// param reads a URL parameter.
func param(r *http.Request, name string) string { return chi.URLParam(r, name) }

// now is the clock the handlers use, indirected so a test can pin it.
var now = func() time.Time { return time.Now().UTC() }
