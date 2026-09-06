package web

import (
	"net/http"

	"github.com/starfederation/datastar-go/datastar"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
	"github.com/atvirokodosprendimai/app-warehousev1/internal/web/render"
	"github.com/atvirokodosprendimai/app-warehousev1/internal/web/view"
)

// settingsSignals is what the settings screen sends.
type settingsSignals struct {
	PublicBaseURL string `json:"setPublicBase"`
}

// GetSettings renders the deployment settings.
func (a *App) GetSettings(w http.ResponseWriter, r *http.Request) {
	s := a.settingsScreen(r, "")
	_ = view.PageShell(s.Page, nil, view.SettingsScreen(s)).Render(r.Context(), w)
}

// settingsScreen builds the settings read model.
func (a *App) settingsScreen(r *http.Request, note string) view.SettingsView {
	base := a.publicBase(r.Context())

	var stored core.Settings
	if a.Settings != nil {
		if got, err := a.Settings.Settings(r.Context()); err == nil {
			stored = got
		}
	}

	return view.SettingsView{
		Page: a.page(r, "Settings", "settings"),
		// Current is what photo links are ACTUALLY built from right now,
		// wherever it came from — which is the question an operator has.
		Current: base,
		// FromEnvironment is shown so it is obvious whether the stored value is
		// doing anything, or whether the deployment is still running on its
		// start-up default.
		FromEnvironment: a.Cfg.PublicBaseURL,
		Stored:          stored.PublicBaseURL,
		UpdatedAt:       stored.UpdatedAt,
		Reachable:       core.ReachablePublicly(base),
		Note:            note,
	}
}

// PostSettings saves the deployment settings.
func (a *App) PostSettings(w http.ResponseWriter, r *http.Request) {
	var in settingsSignals
	if err := datastar.ReadSignals(r, &in); err != nil {
		a.flash(w, r, "settings-msg", "error", "Could not read the form.")
		return
	}
	if a.Settings == nil {
		a.flash(w, r, "settings-msg", "error", "Settings are not available in this build.")
		return
	}

	base, err := core.ValidatePublicBaseURL(in.PublicBaseURL)
	if err != nil {
		a.flash(w, r, "settings-msg", "error", a.userMessage(err))
		return
	}

	if err := a.Settings.SaveSettings(r.Context(), core.Settings{PublicBaseURL: base}); err != nil {
		a.flash(w, r, "settings-msg", "error", a.userMessage(err))
		return
	}
	a.Log.Info("public address changed", "value", base)

	// Re-render the whole panel rather than only a message: the saved value is
	// normalised (trailing slash removed, path rejected), so the box must show
	// what was actually stored rather than what was typed.
	s := a.settingsScreen(r, "Saved. Photo addresses in every future export will use this.")
	sse := render.NewSSE(w, r)
	_ = sse.PatchElementTempl(view.SettingsPanel(s))
}
