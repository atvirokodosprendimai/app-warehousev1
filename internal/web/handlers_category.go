package web

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/starfederation/datastar-go/datastar"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
	"github.com/atvirokodosprendimai/app-warehousev1/internal/taxonomy"
	"github.com/atvirokodosprendimai/app-warehousev1/internal/web/render"
	"github.com/atvirokodosprendimai/app-warehousev1/internal/web/view"
)

// categorySignals is what the taxonomy screen sends.
//
// ⚠ Every name here is camelCase in the JSON and kebab-case in its data-bind
// attribute suffix. They are the same signal: HTML lowercases attribute names,
// so data-bind:catCode binds "catcode" and misses this struct entirely, with no
// error anywhere.
type categorySignals struct {
	// The selected node's editable fields.
	CatCode   string `json:"catCode"`
	CatName   string `json:"catName"`
	CatParent string `json:"catParent"`

	// The new-node form.
	NewCode   string `json:"newCode"`
	NewName   string `json:"newName"`
	NewParent string `json:"newParent"`

	// The field form. FieldID empty means "add"; set means "edit that one".
	FieldID       string `json:"fieldID"`
	FieldCode     string `json:"fieldCode"`
	FieldLabel    string `json:"fieldLabel"`
	FieldKind     string `json:"fieldKind"`
	FieldUnit     string `json:"fieldUnit"`
	FieldOptions  string `json:"fieldOptions"`
	FieldPosition string `json:"fieldPosition"`
	// ⚠ BOOLS, not strings. These two are checkboxes, and a checkbox binds a JSON
	// boolean — declaring them as strings is how a tick silently never saves.
	FieldRequired bool `json:"fieldRequired"`
	FieldExport   bool `json:"fieldExport"`
}

// GetTaxonomy renders the screen an operator shapes their own vocabulary on.
func (a *App) GetTaxonomy(w http.ResponseWriter, r *http.Request) {
	t, err := a.taxonomyScreen(r)
	if err != nil {
		a.Log.Error("taxonomy", "err", err)
		http.Error(w, "Could not load the categories.", http.StatusInternalServerError)
		return
	}
	_ = view.PageShell(t.Page, nil, view.TaxonomyScreen(t)).Render(r.Context(), w)
}

// taxonomyScreen builds the read model.
//
// The selection lives in the QUERY STRING rather than in a signal, exactly as the
// offers filter does, so an edited category is a real address somebody can
// bookmark, reload and send to a colleague.
func (a *App) taxonomyScreen(r *http.Request) (view.Taxonomy, error) {
	tree, err := a.Taxonomies.AllCategories(r.Context())
	if err != nil {
		return view.Taxonomy{}, err
	}
	t := view.Taxonomy{
		Page:        a.page(r, "Categories", "categories"),
		Tree:        tree,
		Kinds:       core.FieldKinds(),
		ValueCounts: map[string]int{},
		// ⚠ SET HERE, IN THE LITERAL, AND NOT BESIDE THE LAST RETURN. This
		// function has THREE exits — no selection, a stale bookmark, and the full
		// read — and the templates are wanted most on the first of them, which is
		// the empty screen a new installation opens on. Filling this in further
		// down left the button rendered on every screen except the one it exists
		// for, and no Go test could see it: they build this struct by hand.
		Templates: taxonomy.Templates(),
	}

	at := strings.TrimSpace(r.URL.Query().Get("at"))
	if at == "" {
		return t, nil
	}
	sel, err := a.Taxonomies.Category(r.Context(), at)
	if err != nil {
		// A stale bookmark to a deleted category renders the unselected screen
		// rather than an error page: the tree beside it is still useful, and the
		// operator can see for themselves that the node is gone.
		a.Log.Warn("selected category unavailable", "id", at, "err", err)
		return t, nil
	}
	t.Selected, t.HasSelection = sel, true

	if own, err := a.Taxonomies.OwnFields(r.Context(), sel.ID); err == nil {
		t.Own = own
		for _, f := range own {
			if n, err := a.Taxonomies.CountFieldValues(r.Context(), f.ID); err == nil {
				t.ValueCounts[f.ID] = n
			}
		}
	}
	// Inherited is the resolved set minus this node's own. Computing it by
	// subtraction rather than by a second walk keeps one definition of "resolved"
	// — the repository's — instead of two that can disagree.
	if all, err := a.Taxonomies.ResolvedFields(r.Context(), sel.ID); err == nil {
		for _, f := range all {
			if f.CategoryID != sel.ID {
				t.Inherited = append(t.Inherited, f)
			}
		}
	}
	if kids, err := a.Taxonomies.CategoryChildren(r.Context(), sel.ID); err == nil {
		t.Children = len(kids)
	}

	if id := strings.TrimSpace(r.URL.Query().Get("field")); id != "" {
		for _, f := range t.Own {
			if f.ID == id {
				t.Editing = f
				break
			}
		}
	}
	return t, nil
}

// PostCategoryTemplate takes a whole starter tree in one press.
//
// ⚠ THE DUPLICATE-ROOT REFUSAL IS ANSWERED HERE RATHER THAN LEFT TO
// userMessage, because it is the one an operator will actually meet — pressing
// the button twice, or pressing it on a warehouse that already trades in cars —
// and the generic path would hand them a wrapped SQLite UNIQUE message. A
// refusal somebody is expected to act on has to say what to do next.
func (a *App) PostCategoryTemplate(w http.ResponseWriter, r *http.Request) {
	t, ok := taxonomy.Template(param(r, "code"))
	if !ok {
		a.flash(w, r, "cat-flash", "error", "No such template.")
		return
	}
	if err := a.Taxonomy.ApplyTemplate(r.Context(), t); err != nil {
		if errors.Is(err, taxonomy.ErrPathTaken) || errors.Is(err, taxonomy.ErrCodeTaken) {
			a.flash(w, r, "cat-flash", "error", "There is already a "+t.Code+
				" category, and a template only ever creates a new tree — nothing was "+
				"changed. Rename or remove that one first if you want to start over.")
			return
		}
		a.flash(w, r, "cat-flash", "error", a.userMessage(err))
		return
	}
	a.repaintTaxonomy(w, r, t.Name+" added — "+
		count(t.Nodes(), "category", "categories")+" and "+
		count(t.Questions(), "question", "questions")+
		". All of it is yours now: rename it, delete what you do not ask, add what is missing.")
}

// count renders a number with the right form of its noun, because "1 questions"
// in a message about what just happened to somebody's data reads as carelessness
// exactly where they are deciding whether to trust it.
func count(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// PostCategories creates a node.
func (a *App) PostCategories(w http.ResponseWriter, r *http.Request) {
	var in categorySignals
	if err := datastar.ReadSignals(r, &in); err != nil {
		a.flash(w, r, "cat-flash", "error", "Could not read the form.")
		return
	}
	c, err := a.Taxonomy.Create(r.Context(), strings.TrimSpace(in.NewParent), core.Category{
		Code: in.NewCode,
		Name: in.NewName,
	})
	if err != nil {
		a.flash(w, r, "cat-flash", "error", a.userMessage(err))
		return
	}
	// Land on the new node, because the next thing anybody does after making a
	// category is attach a question to it.
	sse := render.NewSSE(w, r)
	_ = sse.ExecuteScript("window.location.href = '/categories?at=" + c.ID + "'")
}

// PostCategory saves the selected node's name, code and parent.
//
// The three are separate operations in the service because they have different
// consequences — a name touches one row, a code or a parent re-addresses a whole
// subtree — but they are one Save button, because that is one edit as far as the
// operator is concerned.
func (a *App) PostCategory(w http.ResponseWriter, r *http.Request) {
	var in categorySignals
	if err := datastar.ReadSignals(r, &in); err != nil {
		a.flash(w, r, "cat-flash", "error", "Could not read the form.")
		return
	}
	id := param(r, "id")

	if err := a.Taxonomy.SetName(r.Context(), id, in.CatName); err != nil {
		a.flash(w, r, "cat-flash", "error", a.userMessage(err))
		return
	}
	if err := a.Taxonomy.Rename(r.Context(), id, in.CatCode); err != nil {
		a.flash(w, r, "cat-flash", "error", a.userMessage(err))
		return
	}
	if err := a.Taxonomy.Move(r.Context(), id, strings.TrimSpace(in.CatParent)); err != nil {
		a.flash(w, r, "cat-flash", "error", a.userMessage(err))
		return
	}
	a.repaintTaxonomy(w, r, "Saved. Anything inside it has a new address too.")
}

// PostCategoryDelete removes a node that has nothing inside it.
func (a *App) PostCategoryDelete(w http.ResponseWriter, r *http.Request) {
	if err := a.Taxonomy.Delete(r.Context(), param(r, "id")); err != nil {
		a.flash(w, r, "cat-flash", "error", a.userMessage(err))
		return
	}
	sse := render.NewSSE(w, r)
	_ = sse.ExecuteScript("window.location.href = '/categories'")
}

// PostCategoryFields adds a question to a node, or rewrites one it already asks.
func (a *App) PostCategoryFields(w http.ResponseWriter, r *http.Request) {
	var in categorySignals
	if err := datastar.ReadSignals(r, &in); err != nil {
		a.flash(w, r, "cat-flash", "error", "Could not read the form.")
		return
	}
	id := param(r, "id")

	kind, err := core.ParseFieldKind(in.FieldKind)
	if err != nil {
		a.flash(w, r, "cat-flash", "error", a.userMessage(err))
		return
	}
	pos := 0
	if s := strings.TrimSpace(in.FieldPosition); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			a.flash(w, r, "cat-flash", "error", "Order must be a whole number, 0 or more.")
			return
		}
		pos = n
	}
	// ⚠ CategoryID is deliberately NOT set here. Service.AddField takes the
	// category as its own argument and assigns it, so a value set here would be
	// overwritten and would read as load-bearing when it is not — a mutation run
	// found exactly that: blanking it changed nothing, because nothing downstream
	// reads it. UpdateField does not move a question between levels either.
	f := core.CategoryField{
		ID:       strings.TrimSpace(in.FieldID),
		Code:     in.FieldCode,
		Label:    in.FieldLabel,
		Kind:     kind,
		Unit:     strings.TrimSpace(in.FieldUnit),
		Options:  in.FieldOptions,
		Position: pos,
		Required: in.FieldRequired,
		Export:   in.FieldExport,
	}

	// An id means the operator opened an existing question to edit; the id is kept
	// so the answers already given against it stay readable.
	if f.ID != "" {
		if err := a.Taxonomy.UpdateField(r.Context(), f); err != nil {
			a.flash(w, r, "cat-flash", "error", a.userMessage(err))
			return
		}
		sse := render.NewSSE(w, r)
		_ = sse.ExecuteScript("window.location.href = '/categories?at=" + id + "'")
		return
	}
	if _, err := a.Taxonomy.AddField(r.Context(), id, f); err != nil {
		a.flash(w, r, "cat-flash", "error", a.userMessage(err))
		return
	}
	sse := render.NewSSE(w, r)
	_ = sse.ExecuteScript("window.location.href = '/categories?at=" + id + "'")
}

// PostFieldDelete removes a question, and with it every answer to it.
func (a *App) PostFieldDelete(w http.ResponseWriter, r *http.Request) {
	n, err := a.Taxonomy.DeleteField(r.Context(), param(r, "fieldID"))
	if err != nil {
		a.flash(w, r, "cat-flash", "error", a.userMessage(err))
		return
	}
	msg := "Question deleted."
	if n > 0 {
		msg = "Question deleted, and the " + strconv.Itoa(n) + " answers given to it."
	}
	a.repaintTaxonomy(w, r, msg)
}

// repaintTaxonomy re-renders the whole screen after a change.
//
// The whole screen, not one card: a rename re-addresses every descendant, so the
// paths shown beside the tree are now different strings.
func (a *App) repaintTaxonomy(w http.ResponseWriter, r *http.Request, note string) {
	t, err := a.taxonomyScreen(r)
	if err != nil {
		a.flash(w, r, "cat-flash", "error", a.userMessage(err))
		return
	}
	sse := render.NewSSE(w, r)
	_ = sse.PatchElementTempl(view.TaxonomyBody(t))
	_ = sse.PatchElementTempl(view.Flash("cat-flash", "ok", note))
}
