package web

import (
	"net/http"
	"strings"

	"github.com/starfederation/datastar-go/datastar"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
	"github.com/atvirokodosprendimai/app-warehousev1/internal/web/render"
	"github.com/atvirokodosprendimai/app-warehousev1/internal/web/view"
)

// submitSignals is what the staff submission form sends.
type submitSignals struct {
	Title    string `json:"subTitle"`
	Note     string `json:"subNote"`
	Amount   string `json:"subAmount"`
	Currency string `json:"subCurrency"`
}

// GetSubmit renders the staff submission form and that person's own history.
//
// Everyone signed in can reach this, administrators included: an admin who finds
// something worth taking should be able to put it through the same queue rather
// than needing a different route for the same act.
func (a *App) GetSubmit(w http.ResponseWriter, r *http.Request) {
	u, _ := UserFrom(r.Context())

	mine, err := a.Submissions.Submissions(r.Context(), core.SubmissionFilter{
		SubmittedBy: u.ID, Limit: 50,
	})
	if err != nil {
		a.Log.Warn("own submissions unavailable", "err", err)
	}

	s := view.Submit{
		Page:       a.page(r, "Offer something", "submit"),
		Mine:       mine,
		Currencies: core.KnownCurrencies(),
	}
	_ = view.PageShell(s.Page, nil, view.SubmitScreen(s)).Render(r.Context(), w)
}

// PostSubmit records a staff proposal and tells the administrators.
func (a *App) PostSubmit(w http.ResponseWriter, r *http.Request) {
	var in submitSignals
	if err := datastar.ReadSignals(r, &in); err != nil {
		a.flash(w, r, "submit-msg", "error", "Could not read the form.")
		return
	}
	u, _ := UserFrom(r.Context())

	// An asking price is optional: "what will you give me for this" is a
	// legitimate submission, and demanding a number makes people invent one.
	asking, err := parsePrice(in.Amount, in.Currency)
	if err != nil {
		a.flash(w, r, "submit-msg", "error", a.userMessage(err))
		return
	}

	sub, err := a.Submission.Submit(r.Context(), u, in.Title, in.Note, asking)
	if err != nil {
		a.flash(w, r, "submit-msg", "error", a.userMessage(err))
		return
	}

	// Persist first, then publish. Announcing a submission that failed to save
	// would put a number on every administrator's banner with nothing behind it.
	a.NotifyInbox()

	sse := render.NewSSE(w, r)
	_ = sse.ExecuteScript("window.location.href = '/submit/" + sub.ID + "'")
}

// GetSubmission renders one proposal.
//
// A staff member may see their OWN; an administrator may see any. That check is
// here rather than in the router because it depends on the row, not the route.
func (a *App) GetSubmission(w http.ResponseWriter, r *http.Request) {
	u, _ := UserFrom(r.Context())

	sub, err := a.Submissions.Submission(r.Context(), param(r, "id"))
	if err != nil {
		a.notFound(w, r, err)
		return
	}
	if !u.IsAdmin && sub.SubmittedBy != u.ID {
		// Not "forbidden": telling somebody a submission exists but is not theirs
		// is itself a disclosure. To them it simply is not there.
		http.NotFound(w, r)
		return
	}

	locs, err := a.Locations.AllLocations(r.Context())
	if err != nil {
		a.Log.Warn("locations unavailable", "err", err)
	}

	d := view.SubmissionDetail{
		Page:       a.page(r, sub.Title, "inbox"),
		Submission: sub,
		Locations:  locs,
		Currencies: core.KnownCurrencies(),
		CanDecide:  u.IsAdmin,
	}
	_ = view.PageShell(d.Page, nil, view.SubmissionScreen(d)).Render(r.Context(), w)
}

// PostSubmissionPhoto attaches a photograph to a proposal.
func (a *App) PostSubmissionPhoto(w http.ResponseWriter, r *http.Request) {
	id := param(r, "id")
	u, _ := UserFrom(r.Context())

	sub, err := a.Submissions.Submission(r.Context(), id)
	if err != nil {
		a.notFound(w, r, err)
		return
	}
	if !u.IsAdmin && sub.SubmittedBy != u.ID {
		http.NotFound(w, r)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	if err := r.ParseMultipartForm(maxPhotoBytes); err != nil {
		a.flash(w, r, "sub-flash", "error", "That upload was too large or could not be read.")
		return
	}

	var failed []string
	for _, fhs := range r.MultipartForm.File {
		for _, fh := range fhs {
			f := &multipartFile{fh: fh}
			data, name, ct, err := f.read()
			if err != nil {
				failed = append(failed, name+": "+err.Error())
				continue
			}
			if _, err := a.Submission.AddPhoto(r.Context(), id, name, ct, data); err != nil {
				failed = append(failed, name+": "+a.userMessage(err))
			}
		}
	}
	a.NotifyInbox()
	a.repaintSubmission(w, r, id, failed)
}

// GetInbox is the administrator's queue.
func (a *App) GetInbox(w http.ResponseWriter, r *http.Request) {
	f := core.SubmissionFilter{Limit: 200}
	if r.URL.Query().Get("all") != "1" {
		f.OpenOnly = true
	}

	subs, err := a.Submissions.Submissions(r.Context(), f)
	if err != nil {
		a.Log.Error("inbox", "err", err)
		http.Error(w, "Could not load the inbox.", http.StatusInternalServerError)
		return
	}

	in := view.Inbox{
		Page:        a.page(r, "Inbox", "inbox"),
		Submissions: subs,
		ShowAll:     f.OpenOnly == false,
	}
	_ = view.PageShell(in.Page, nil, view.InboxScreen(in)).Render(r.Context(), w)
}

// PostStartReview marks a proposal as being looked at.
//
// It exists so that two administrators do not both ring the same person: the
// queue shows who picked it up.
func (a *App) PostStartReview(w http.ResponseWriter, r *http.Request) {
	u, _ := UserFrom(r.Context())
	id := param(r, "id")

	if err := a.Submission.StartReview(r.Context(), u, id); err != nil {
		a.flash(w, r, "sub-flash", "error", a.userMessage(err))
		return
	}
	a.NotifyInbox()
	a.repaintSubmission(w, r, id, nil)
}

// decideSignals is what the decide panel sends.
type decideSignals struct {
	Note        string `json:"decNote"`
	ShopAmount  string `json:"decShopAmount"`
	ShopCur     string `json:"decShopCurrency"`
	OwnerAmount string `json:"decOwnerAmount"`
	OwnerCur    string `json:"decOwnerCurrency"`
	Location    string `json:"decLocation"`
	SKU         string `json:"decSku"`
	Condition   string `json:"decCondition"`
	Description string `json:"decDescription"`
}

// PostDecline refuses a proposal, with a reason the submitter can read.
func (a *App) PostDecline(w http.ResponseWriter, r *http.Request) {
	var in decideSignals
	if err := datastar.ReadSignals(r, &in); err != nil {
		a.flash(w, r, "sub-flash", "error", "Could not read the form.")
		return
	}
	u, _ := UserFrom(r.Context())
	id := param(r, "id")

	if strings.TrimSpace(in.Note) == "" {
		// Said here as well as in the domain, because this is where somebody can
		// act on it: a bare "no" sends the same item back next week.
		a.flash(w, r, "sub-flash", "error",
			"Give a reason. Without one they will offer the same thing again.")
		return
	}

	if err := a.Submission.Decline(r.Context(), u, id, in.Note); err != nil {
		a.flash(w, r, "sub-flash", "error", a.userMessage(err))
		return
	}
	a.NotifyInbox()
	a.repaintSubmission(w, r, id, nil)
}

// PostAccept turns a proposal into a real, sellable offer.
//
// This is the point of the whole queue: the administrator has rung the submitter
// and agreed a price, and the item now becomes something an export can carry.
func (a *App) PostAccept(w http.ResponseWriter, r *http.Request) {
	var in decideSignals
	if err := datastar.ReadSignals(r, &in); err != nil {
		a.flash(w, r, "sub-flash", "error", "Could not read the form.")
		return
	}
	u, _ := UserFrom(r.Context())
	id := param(r, "id")

	shop, err := parsePrice(in.ShopAmount, in.ShopCur)
	if err != nil {
		a.flash(w, r, "sub-flash", "error", "Shop price: "+a.userMessage(err))
		return
	}
	owner, err := parsePrice(in.OwnerAmount, in.OwnerCur)
	if err != nil {
		a.flash(w, r, "sub-flash", "error", "Owner price: "+a.userMessage(err))
		return
	}

	conv := core.Conversion{
		SKU:         strings.TrimSpace(in.SKU),
		Shop:        shop,
		Owner:       owner,
		LocationID:  strings.TrimSpace(in.Location),
		Condition:   strings.TrimSpace(in.Condition),
		Description: in.Description,
		Note:        strings.TrimSpace(in.Note),
	}

	offer, err := a.Submission.Accept(r.Context(), u, id, conv)
	if err != nil {
		a.flash(w, r, "sub-flash", "error", a.userMessage(err))
		return
	}
	a.NotifyInbox()

	sse := render.NewSSE(w, r)
	_ = sse.ExecuteScript("window.location.href = '/offers/" + offer.ID + "'")
}

// repaintSubmission re-renders the submission screen after a change.
func (a *App) repaintSubmission(w http.ResponseWriter, r *http.Request, id string, problems []string) {
	u, _ := UserFrom(r.Context())

	sub, err := a.Submissions.Submission(r.Context(), id)
	if err != nil {
		a.flash(w, r, "sub-flash", "error", a.userMessage(err))
		return
	}
	locs, err := a.Locations.AllLocations(r.Context())
	if err != nil {
		a.Log.Warn("locations unavailable", "err", err)
	}

	d := view.SubmissionDetail{
		Page:       a.page(r, sub.Title, "inbox"),
		Submission: sub,
		Locations:  locs,
		Currencies: core.KnownCurrencies(),
		CanDecide:  u.IsAdmin,
	}

	sse := render.NewSSE(w, r)
	_ = sse.PatchElementTempl(view.SubmissionBody(d))
	if len(problems) > 0 {
		_ = sse.PatchElementTempl(view.Flash("sub-flash", "error",
			strings.Join(problems, "; ")))
		return
	}
	_ = sse.PatchElementTempl(view.Flash("sub-flash", "ok", "Saved."))
}
