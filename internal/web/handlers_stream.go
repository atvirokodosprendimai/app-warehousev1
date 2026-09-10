package web

import (
	"net/http"
	"time"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/web/render"
	"github.com/atvirokodosprendimai/app-warehousev1/internal/web/view"
)

// TopicInbox is the bus topic for staff submissions arriving or being decided.
//
// Unlike an offer, which is notified per id, this is a single shared topic —
// which is the right shape when the semantic really is broadcast-to-all: every
// administrator's banner changes on every submission, so a per-id subject would
// just be a topic with one subscriber set fanned out by hand.
const TopicInbox = "inbox"

// GetStream is the ONE SSE connection a page opens.
//
// Every page opens exactly this endpoint, and the query says what the page needs
// beyond the things every page needs. The server then owns the cadence and
// decides what to push: there is no client-side timer anywhere in this
// application, and no second connection per widget.
//
// What every page gets is the inbox banner. What a page asks for with ?offer=ID
// is that offer's cards, which is how the editor stays live in a second tab.
func (a *App) GetStream(w http.ResponseWriter, r *http.Request) {
	u, _ := UserFrom(r.Context())
	offerID := r.URL.Query().Get("offer")

	// Only an administrator can act on the inbox, so only an administrator
	// subscribes to it. A staff user's stream carries their page's fragments and
	// nothing about a queue they cannot open.
	var inbox <-chan struct{}
	if u.IsAdmin {
		ch, unsubscribe := a.Bus.Subscribe(TopicInbox)
		defer unsubscribe()
		inbox = ch
	}

	var offerEvents <-chan struct{}
	if offerID != "" {
		ch, unsubscribe := a.Bus.Subscribe(offerID)
		defer unsubscribe()
		offerEvents = ch
	}

	// ⚠ Logged HERE rather than left to the request logger, which writes when a
	// response FINISHES. This one finishes when the tab closes, so on a busy
	// afternoon the single longest-lived request on every page would be the one
	// piece of traffic that never appeared while it was happening.
	a.Log.Info("stream opened",
		"user", u.Email,
		"offer", offerID,
		"inbox", u.IsAdmin,
		"ip", r.RemoteAddr,
	)
	defer a.Log.Info("stream closed", "user", u.Email, "offer", offerID)

	sse := render.NewSSE(w, r)
	ticker := time.NewTicker(render.Heartbeat)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			// The tab closed or navigated away. Returning releases every
			// subscription through the deferred unsubscribes.
			return

		case <-inbox:
			// Re-read rather than trusting a count carried on the wire. Two
			// submissions arriving together both signal, and both re-reads produce
			// the current number, so the later one wins and is right.
			if n, err := a.openSubmissions(r); err == nil {
				_ = sse.PatchElementTempl(view.InboxBanner(n))
			}

		case <-offerEvents:
			d, err := a.offerDetail(r, offerID)
			if err != nil {
				return
			}
			_ = sse.PatchElementTempl(view.OfferStatusCard(d))
			_ = sse.PatchElementTempl(view.OfferPhotosCard(d))
			_ = sse.PatchElementTempl(view.MarginLine(d.Row))
			_ = sse.PatchElementTempl(view.OfferCartsCard(d))
			// ⚠ The card is rendered unconditionally with a stable id, which is
			// what makes this patch land at all: an SSE patch REPLACES an element
			// already in the document, so a card that appeared only once it had
			// content could never be patched into existence.
			_ = sse.PatchElementTempl(view.OfferMarketplaceCard(d))
			// ⚠ THE QUESTIONS CARD TRAVELS WITH ITS SIGNALS. Filing an offer under a
			// category makes a set of controls appear that were not on the page when
			// it loaded, so nothing has seeded their signals — and an input bound to
			// a signal that does not exist renders blank however right its markup is,
			// which reads as "the answers I just saved are gone". If-missing is what
			// makes this safe to send on EVERY offer event: it seeds a card that has
			// just arrived and leaves a half-typed one alone.
			_ = sse.PatchElementTempl(view.OfferFieldsCard(d))
			if sig := view.FieldSignalValues(d); len(sig) > 0 {
				_ = sse.MarshalAndPatchSignalsIfMissing(sig)
			}

		case <-ticker.C:
			// An idle connection through a proxy is dropped without either end
			// being told, and a page that has silently stopped updating looks
			// exactly like one where nothing has happened.
			if sse.IsClosed() {
				return
			}
		}
	}
}

// openSubmissions counts the proposals still awaiting a decision.
func (a *App) openSubmissions(r *http.Request) (int, error) {
	if a.Submissions == nil {
		return 0, nil
	}
	return a.Submissions.CountOpen(r.Context())
}

// NotifyInbox tells every administrator's open page that the queue changed.
//
// It is called AFTER the write it describes has been persisted. Publishing first
// would let a banner announce a submission that failed to save.
func (a *App) NotifyInbox() {
	if a.Bus != nil {
		a.Bus.Broadcast(TopicInbox)
	}
}
