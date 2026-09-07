package web

import (
	"net/http"
	"strings"

	"github.com/starfederation/datastar-go/datastar"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
	"github.com/atvirokodosprendimai/app-warehousev1/internal/web/render"
	"github.com/atvirokodosprendimai/app-warehousev1/internal/web/view"
)

// settingsSignals is what the settings screen sends.
//
// Every field arrives on every save, because datastar signals are global and
// flattened — which is what makes the read-modify-write in PostSettings safe to
// write as a whole-struct save.
type settingsSignals struct {
	PublicBaseURL string `json:"setPublicBase"`
	EbayCategory  string `json:"setEbayCategory"`
	EbayCondition string `json:"setEbayCondition"`
	EbayLocation  string `json:"setEbayLocation"`
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

	// The start-up defaults, as a Marketplace so the same Resolve rule applies
	// here as at export time. Two places computing precedence differently is how
	// a settings screen ends up showing a value the export does not use.
	envEbay := core.Marketplace{
		Category:    a.Cfg.Export.Category,
		ConditionID: a.Cfg.Export.ConditionID,
		Location:    a.Cfg.Export.Location,
	}

	return view.SettingsView{
		Page: a.page(r, "Settings", "settings"),

		EbayStored:    stored.Ebay,
		EbayFromEnv:   envEbay,
		EbayEffective: stored.Ebay.Resolve(envEbay),
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

	// ⚠ READ-MODIFY-WRITE, not a fresh struct. SaveSettings writes EVERY key in
	// one transaction, so saving core.Settings{PublicBaseURL: base} would blank
	// the marketplace values that are not on this request. That was safe while
	// there was exactly one setting and stopped being safe the moment ADR-016
	// added three more.
	cur, err := a.Settings.Settings(r.Context())
	if err != nil {
		a.flash(w, r, "settings-msg", "error", a.userMessage(err))
		return
	}
	cur.PublicBaseURL = base
	cur.Ebay = core.Marketplace{
		Category:    strings.TrimSpace(in.EbayCategory),
		ConditionID: strings.TrimSpace(in.EbayCondition),
		Location:    strings.TrimSpace(in.EbayLocation),
	}

	if err := a.Settings.SaveSettings(r.Context(), cur); err != nil {
		a.flash(w, r, "settings-msg", "error", a.userMessage(err))
		return
	}
	a.Log.Info("settings changed", "public_base", base, "ebay_category", cur.Ebay.Category)

	// Re-render the whole panel rather than only a message: the saved value is
	// normalised (trailing slash removed, path rejected), so the box must show
	// what was actually stored rather than what was typed.
	s := a.settingsScreen(r, "Saved. Photo addresses in every future export will use this.")
	sse := render.NewSSE(w, r)
	_ = sse.PatchElementTempl(view.SettingsPanel(s))
}
