package web

import (
	"context"
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

// marketplaceOptionSignals is what the list editor's add-boxes send.
//
// One pair per field, named the way the view names them. They are written out
// rather than derived because a struct tag cannot be computed — and because a
// missing pair is then a compile error rather than a box whose value silently
// never arrives.
//
// ⚠ THE ZERO VALUE IS ALSO THE "CLEAR THE BOXES" MESSAGE, which is why the same
// type is used for reading and for patching back.
type marketplaceOptionSignals struct {
	ValueCategory  string `json:"optValueCategory"`
	LabelCategory  string `json:"optLabelCategory"`
	ValueCondition string `json:"optValueCondition"`
	LabelCondition string `json:"optLabelCondition"`
	ValueLocation  string `json:"optValueLocation"`
	LabelLocation  string `json:"optLabelLocation"`
}

// forField returns the pair belonging to one field.
//
// ⚠ THE DEFAULT RETURNS EMPTY RATHER THAN GUESSING. An unknown field has already
// been refused by the caller; returning a neighbouring field's value here would
// turn a routing mistake into a wrong row nobody could explain.
func (s marketplaceOptionSignals) forField(f core.MarketplaceField) (value, label string) {
	switch f {
	case core.MarketplaceCategory:
		return s.ValueCategory, s.LabelCategory
	case core.MarketplaceCondition:
		return s.ValueCondition, s.LabelCondition
	case core.MarketplaceLocation:
		return s.ValueLocation, s.LabelLocation
	}
	return "", ""
}

// ebayFromEnv is the start-up default, as a Marketplace.
func (a *App) ebayFromEnv() core.Marketplace {
	return core.Marketplace{
		Category:    a.Cfg.Export.Category,
		ConditionID: a.Cfg.Export.ConditionID,
		Location:    a.Cfg.Export.Location,
	}
}

// ebayDefaults is what an offer that chooses nothing will export under.
//
// ⚠ ONE FUNCTION, TWO SCREENS. The settings panel and the offer editor's
// "Use the default — …" label must name the same value; computing that
// precedence twice is exactly how a screen ends up promising a fallback the
// export does not use. A failed read yields the environment's values rather than
// nothing, because that is what the export would fall back to anyway.
func (a *App) ebayDefaults(ctx context.Context) core.Marketplace {
	env := a.ebayFromEnv()
	if a.Settings == nil {
		return env
	}
	stored, err := a.Settings.Settings(ctx)
	if err != nil {
		a.Log.Warn("settings unavailable for marketplace defaults", "err", err)
		return env
	}
	return stored.Ebay.Resolve(env)
}

// marketplaceOptions builds one profile's option lists, grouped by field.
//
// ⚠ IT ITERATES [core.MarketplaceFields], NOT the rows. A field with no options
// still gets a group, because a section that appeared only once it had content
// would be one nobody could add the first value to — which is the state every
// fresh installation is in.
//
// The profile is a parameter rather than a constant even though only eBay has
// must-haves today: T4 renders the same groups on the offer editor, and a second
// hard-coded "ebay" is how two screens start disagreeing about which marketplace
// they are configuring.
func (a *App) marketplaceOptions(ctx context.Context, profile string) []view.MarketplaceOptionGroup {
	titles := map[core.MarketplaceField]struct{ title, hint string }{
		core.MarketplaceCategory: {"eBay category",
			"The number eBay files a listing under. Put the name you know it by in the second box."},
		core.MarketplaceCondition: {"Condition",
			"eBay's own codes — 1000 is New, 3000 is Used."},
		core.MarketplaceLocation: {"Dispatches from",
			"Shown on every listing, and eBay quotes postage from it."},
	}

	var all []core.MarketplaceOption
	if a.Marketplace != nil {
		if got, err := a.Marketplace.Options(ctx, profile); err == nil {
			all = got
		} else {
			a.Log.Error("marketplace options", "err", err)
		}
	}

	out := make([]view.MarketplaceOptionGroup, 0, len(core.MarketplaceFields()))
	for _, f := range core.MarketplaceFields() {
		g := view.MarketplaceOptionGroup{Field: f, Title: titles[f].title, Hint: titles[f].hint}
		for _, o := range all {
			if o.Field == f {
				g.Options = append(g.Options, o)
			}
		}
		out = append(out, g)
	}
	return out
}

// PostMarketplaceOption puts one value on a list.
func (a *App) PostMarketplaceOption(w http.ResponseWriter, r *http.Request) {
	field := core.MarketplaceField(strings.ToLower(strings.TrimSpace(param(r, "field"))))
	if !core.ValidMarketplaceField(string(field)) {
		a.flash(w, r, "settings-msg", "error",
			"That is not a marketplace field this application knows.")
		return
	}
	var in marketplaceOptionSignals
	if err := datastar.ReadSignals(r, &in); err != nil {
		a.flash(w, r, "settings-msg", "error", "Could not read the form.")
		return
	}
	if a.Marketplace == nil {
		a.flash(w, r, "settings-msg", "error", "Lists are not available in this build.")
		return
	}

	value, label := in.forField(field)
	// A label nobody typed falls back to the value, so adding "Kaunas" once is
	// enough. It is a convenience, never a guess about what the value MEANS.
	if strings.TrimSpace(label) == "" {
		label = value
	}
	if _, err := a.Marketplace.AddOption(r.Context(), core.MarketplaceOption{
		Profile: "ebay", Field: field, Value: value, Label: label,
	}); err != nil {
		a.flash(w, r, "settings-msg", "error", a.userMessage(err))
		return
	}

	a.patchMarketplaceOptions(w, r, "Added to the list.")
}

// PostRemoveMarketplaceOption takes one value off a list.
func (a *App) PostRemoveMarketplaceOption(w http.ResponseWriter, r *http.Request) {
	if a.Marketplace == nil {
		a.flash(w, r, "settings-msg", "error", "Lists are not available in this build.")
		return
	}
	if err := a.Marketplace.RemoveOption(r.Context(), param(r, "id")); err != nil {
		a.flash(w, r, "settings-msg", "error", a.userMessage(err))
		return
	}
	// ⚠ Offers that already chose this value KEEP it. Withdrawing something from
	// the menu is not a decision to re-list stock somewhere else.
	a.patchMarketplaceOptions(w, r, "Removed from the list.")
}

// patchMarketplaceOptions re-renders the card, says so, and empties the boxes.
//
// ⚠ ONE SSE GENERATOR FOR ALL THREE. Every other handler here either flashes OR
// patches; opening a second generator on one response writes a second set of
// headers, so the message is patched through this one rather than through
// a.flash.
//
// ⚠ AND THE SIGNAL PATCH IS NOT only-if-missing. The boxes must end up EMPTY
// after a successful add; only-if-missing would leave what the operator just
// submitted sitting in them, one press away from being added twice.
func (a *App) patchMarketplaceOptions(w http.ResponseWriter, r *http.Request, note string) {
	s := a.settingsScreen(r, note)
	sse := render.NewSSE(w, r)
	_ = sse.PatchElementTempl(view.MarketplaceOptionsCard(s))
	_ = sse.PatchElementTempl(view.Flash("settings-msg", "ok", note))
	_ = sse.MarshalAndPatchSignals(marketplaceOptionSignals{})
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
	envEbay := a.ebayFromEnv()

	return view.SettingsView{
		Page: a.page(r, "Settings", "settings"),

		// ⚠ SET IN THE STRUCT LITERAL, not assigned after it. ADR-021 T5 shipped a
		// read model filled in beside ONE of a function's three exits, so the
		// controls rendered on every screen but the empty one they were for. This
		// function has one exit today; the literal is where it cannot grow a
		// second that forgets.
		Options: a.marketplaceOptions(r.Context(), "ebay"),

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
