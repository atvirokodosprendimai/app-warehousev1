package view

import (
	"strings"
	"testing"

	"github.com/a-h/templ"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
	"github.com/atvirokodosprendimai/app-warehousev1/internal/taxonomy"
)

// taxonomyFixture is a two-level tree with the deeper node selected, one own
// question and one inherited from the level above.
func taxonomyFixture() Taxonomy {
	car := core.Category{ID: "c-car", Code: "CAR", Path: "CAR", Name: "Car parts"}
	engine := core.Category{ID: "c-eng", ParentID: "c-car", Code: "ENGINE", Path: "CAR/ENGINE", Name: "Engine"}
	return Taxonomy{
		Page:         Page{Title: "Categories"},
		Tree:         []core.Category{car, engine},
		Selected:     engine,
		HasSelection: true,
		Kinds:        core.FieldKinds(),
		Own: []core.CategoryField{
			{ID: "f-code", CategoryID: "c-eng", CategoryPath: "CAR/ENGINE",
				Code: "engine_code", Label: "Engine code", Kind: core.FieldText},
		},
		Inherited: []core.CategoryField{
			{ID: "f-vin", CategoryID: "c-car", CategoryPath: "CAR",
				Code: "vin", Label: "VIN", Kind: core.FieldText},
		},
		ValueCounts: map[string]int{"f-code": 3},
	}
}

// postAttr is the rendered data-on:click for a datastar POST whose URL was built
// from a Go expression.
//
// templ escapes a dynamic attribute value, so the apostrophes around the path
// arrive as &#39;. A static attribute — @post('/categories') with no id in it —
// is emitted verbatim instead. Both forms are correct; a test that assumed one
// of them everywhere would fail against markup that is fine.
func postAttr(path string) string {
	return `data-on:click="@post(&#39;` + path + `&#39;)"`
}

// TestACategoryTreeCanBeShaped checks the screen offers every operation the tree
// needs: making a node, renaming it, moving it, and removing it.
func TestACategoryTreeCanBeShaped(t *testing.T) {
	html := renderString(t, TaxonomyScreen(taxonomyFixture()))

	// The tree is rendered by path, which is tree order.
	for _, path := range []string{">CAR<", ">CAR/ENGINE<"} {
		if !strings.Contains(html, path) {
			t.Errorf("the tree does not list %s", path)
		}
	}
	// Creating.
	if !strings.Contains(html, `data-on:click="@post('/categories')"`) {
		t.Error("no control posts a new category")
	}
	for _, bind := range []string{"data-bind:new-code", "data-bind:new-name", "data-bind:new-parent"} {
		if !strings.Contains(html, bind) {
			t.Errorf("the new-category form is missing %q", bind)
		}
	}
	// Renaming and moving are one Save on the selected node.
	//
	// ⚠ The apostrophes are ESCAPED here and not in the assertion above, and the
	// difference is real rather than sloppy: a STATIC templ attribute is emitted
	// verbatim, while one built from a Go expression goes through templ's
	// escaper, so the same @post(...) reads differently depending on whether the
	// URL carries an id. Asserting the raw form here would fail against correct
	// markup and send the next reader looking for a bug in the template.
	if !strings.Contains(html, postAttr("/categories/c-eng")) {
		t.Error("no control saves the selected category")
	}
	for _, bind := range []string{"data-bind:cat-code", "data-bind:cat-name", "data-bind:cat-parent"} {
		if !strings.Contains(html, bind) {
			t.Errorf("the selected category's form is missing %q", bind)
		}
	}
	// Deleting, which this fixture allows because the node has no children.
	if !strings.Contains(html, postAttr("/categories/c-eng/delete")) {
		t.Error("no control deletes the selected category")
	}

	// A node whose subtree is not empty must NOT offer a delete: the database
	// refuses it with a bare FOREIGN KEY message, and a control that always errors
	// reads as a bug rather than as a rule.
	withKids := taxonomyFixture()
	withKids.Children = 2
	blocked := renderString(t, TaxonomyScreen(withKids))
	if strings.Contains(blocked, "/categories/c-eng/delete") {
		t.Error("a category with children still offers a delete button; the refusal " +
			"comes from the database as an unreadable FOREIGN KEY error, so the " +
			"screen has to say why instead of offering the control")
	}
	if !strings.Contains(blocked, "Move or delete the 2 categories") {
		t.Error("the screen does not say WHY the delete is unavailable, or how many " +
			"children are in the way")
	}
}

// TestInheritedFieldsAreShownAsInherited pins the half of the screen that is
// read-only on purpose.
func TestInheritedFieldsAreShownAsInherited(t *testing.T) {
	html := renderString(t, TaxonomyScreen(taxonomyFixture()))

	if !strings.Contains(html, "Inherited") {
		t.Fatal("the screen does not separate inherited questions from this " +
			"category's own; an operator cannot tell which ones editing here affects")
	}
	if !strings.Contains(html, "VIN") {
		t.Error("the inherited question is not shown at all, so the operator cannot " +
			"see the full set an offer under this node will be asked")
	}
	// It says where it came from.
	if !strings.Contains(html, ">CAR</span>") {
		t.Error("the inherited question does not name the level that defined it")
	}
	// And it offers no way to edit or delete it from here.
	if strings.Contains(html, "/fields/f-vin/delete") {
		t.Error("an INHERITED question offers a delete from a descendant's screen. " +
			"Deleting it there would remove it from every other subtree under that " +
			"ancestor too, without saying so")
	}
	if strings.Contains(html, "field=f-vin") {
		t.Error("an INHERITED question is editable from a descendant's screen; " +
			"editing it there would change it for every sibling subtree silently")
	}
	// This category's own question IS editable and deletable.
	if !strings.Contains(html, "field=f-code") {
		t.Error("this category's own question cannot be opened for editing")
	}
	if !strings.Contains(html, "/fields/f-code/delete") {
		t.Error("this category's own question cannot be deleted")
	}
}

// TestDeletingAFieldSaysWhatItTakes makes a deliberate loss visible before it
// happens.
func TestDeletingAFieldSaysWhatItTakes(t *testing.T) {
	html := renderString(t, TaxonomyScreen(taxonomyFixture()))

	if !strings.Contains(html, "Delete (3 answers)") {
		t.Error("the delete control does not say how many stored answers it will " +
			"take with it. The cascade is deliberate — an answer whose question no " +
			"longer exists cannot be interpreted — but a deliberate loss still has " +
			"to be visible before it is committed")
	}

	// With no answers stored there is nothing to warn about, and a "(0 answers)"
	// would be noise that trains people to ignore the warning that matters.
	none := taxonomyFixture()
	none.ValueCounts = map[string]int{}
	quiet := renderString(t, TaxonomyScreen(none))
	if strings.Contains(quiet, "0 answers") {
		t.Error("a question with no answers still warns about losing them; a warning " +
			"that always fires is a warning nobody reads")
	}

	// ⚠ Singular when there is one. Found by looking at the rendered screen: it
	// said "Delete (1 answers)". The warning is read at the moment somebody is
	// about to destroy data, and that kind of small wrongness makes a person
	// trust the sentence less exactly when they should be reading it carefully.
	one := taxonomyFixture()
	one.ValueCounts = map[string]int{"f-code": 1}
	single := renderString(t, TaxonomyScreen(one))
	if !strings.Contains(single, "Delete (1 answer)") {
		t.Error(`a single answer is announced as "1 answers"`)
	}
	if strings.Contains(single, "1 answers") {
		t.Error(`the plural leaked back in: "1 answers"`)
	}
}

// TestEveryFieldKindCanBeChosen checks the operator can actually reach all five.
func TestEveryFieldKindCanBeChosen(t *testing.T) {
	html := renderString(t, TaxonomyScreen(taxonomyFixture()))

	for _, k := range core.FieldKinds() {
		if !strings.Contains(html, `value="`+string(k)+`"`) {
			t.Errorf("the kind picker does not offer %q; a kind the domain accepts "+
				"and the interface cannot reach exists only in the tests", k)
		}
	}
	// The unit and the option list are what make number and choice worth having
	// as separate kinds at all.
	for _, bind := range []string{"data-bind:field-unit", "data-bind:field-options",
		"data-bind:field-position", "data-bind:field-required", "data-bind:field-export"} {
		if !strings.Contains(html, bind) {
			t.Errorf("the question form is missing %q", bind)
		}
	}
}

// TestTheExportTickIsOnTheQuestion pins M's request: choosing which of a
// category's questions reach a CSV is done where the question is defined.
func TestTheExportTickIsOnTheQuestion(t *testing.T) {
	html := renderString(t, TaxonomyScreen(taxonomyFixture()))

	if !strings.Contains(html, "data-bind:field-export") {
		t.Fatal("no control marks a question for export")
	}
	if !strings.Contains(html, "Send this to marketplaces") {
		t.Error("the export control does not say what it does in words an operator " +
			"would use")
	}

	// An exported question is visible as such in the list, without opening it.
	exported := taxonomyFixture()
	exported.Own[0].Export = true
	list := renderString(t, TaxonomyScreen(exported))
	if !strings.Contains(list, "exported") {
		t.Error("a question marked for export is not distinguishable in the list, " +
			"so the only way to audit what leaves the building is to open each one")
	}
}

// TestTheTaxonomyScreenIsReachableWithoutTypingAURL pins the navigation entry.
//
// ⚠ THIS TEST EXISTS BECAUSE THE TASK CLAIMED IT BEFORE IT WAS TRUE. T2's
// Reachability table said the entry was "asserted by rendering the shell" while
// nothing asserted anything — the same shape as ADR-020's rung 3, which said the
// New offer control was on every page while two handlers passed it and M found
// the gap by doing the job. A screen nobody can navigate to is a screen nobody
// uses, however well it renders when you reach it.
func TestTheTaxonomyScreenIsReachableWithoutTypingAURL(t *testing.T) {
	admin := renderString(t, PageShell(
		Page{Title: "Any page", User: core.User{DisplayName: "A", IsAdmin: true}},
		nil, templ.NopComponent))
	if !strings.Contains(admin, `href="/categories"`) {
		t.Error("an administrator's navigation has no Categories entry, so the only " +
			"way to shape the taxonomy is to know the URL by heart")
	}

	// And it stays behind the admin check in the markup as well as in the router:
	// showing a link that answers 403 teaches people the application is broken.
	staff := renderString(t, PageShell(
		Page{Title: "Any page", User: core.User{DisplayName: "S", IsAdmin: false}},
		nil, templ.NopComponent))
	if strings.Contains(staff, `href="/categories"`) {
		t.Error("a non-admin is shown a Categories link that will answer 403; a link " +
			"that always refuses reads as a bug rather than as a permission")
	}
}

// TestTheEmptyTaxonomyScreenSaysWhatToDo covers the state every new
// installation opens on.
func TestTheEmptyTaxonomyScreenSaysWhatToDo(t *testing.T) {
	html := renderString(t, TaxonomyScreen(Taxonomy{Kinds: core.FieldKinds()}))

	if !strings.Contains(html, "Nothing yet") {
		t.Error("an empty tree renders no explanation; a blank panel reads as " +
			"something that failed to load")
	}
	if !strings.Contains(html, `data-on:click="@post('/categories')"`) {
		t.Error("the empty screen offers no way to make the first category, which " +
			"is the only thing anybody can do from it")
	}
}

// TestAStarterTemplateIsOfferedOnTheScreenSomebodyStartsFrom is M's ask: one
// press that writes a trade's whole question set.
//
// ⚠ THE EMPTY SCREEN IS THE ONE THAT MATTERS. A template is for the operator who
// has nothing yet, and that is exactly the state in which a control tucked
// beside an existing tree would not be rendered at all.
func TestAStarterTemplateIsOfferedOnTheScreenSomebodyStartsFrom(t *testing.T) {
	empty := Taxonomy{Kinds: core.FieldKinds(), Templates: taxonomy.Templates()}
	html := renderString(t, TaxonomyScreen(empty))

	for _, want := range []string{"Car parts", "PC parts"} {
		if !strings.Contains(html, want) {
			t.Errorf("a brand-new installation is not offered the %q template", want)
		}
	}
	for _, code := range []string{"CAR", "PC"} {
		if !strings.Contains(html, postAttr("/categories/template/"+code)) {
			t.Errorf("nothing on the screen applies the %s template", code)
		}
	}
}

// TestATemplateSaysHowMuchItIsAboutToBuild pins the sentence under the button.
//
// Pressing an unfamiliar control that writes thirty rows into your own taxonomy
// is a leap; saying the size first is what makes it a decision instead.
func TestATemplateSaysHowMuchItIsAboutToBuild(t *testing.T) {
	f := taxonomyFixture()
	f.Templates = taxonomy.Templates()
	html := renderString(t, TaxonomyScreen(f))

	for _, tpl := range f.Templates {
		// Computed from the template rather than typed here: a test carrying its
		// own copy of the count stops agreeing the moment a question is added,
		// and then it is the test that is wrong.
		if !strings.Contains(html, plural(tpl.Nodes(), "category", "categories")) {
			t.Errorf("the %s template does not say how many categories it makes", tpl.Code)
		}
		if !strings.Contains(html, plural(tpl.Questions(), "question", "questions")) {
			t.Errorf("the %s template does not say how many questions it writes", tpl.Code)
		}
		if !strings.Contains(html, tpl.Summary) {
			t.Errorf("the %s template does not say what it is for", tpl.Code)
		}
	}

	// ⚠ Singular where it is one. The car template makes exactly one category,
	// and "1 categories" on the control that is asking for somebody's trust is
	// the same carelessness as the "1 answers" this screen already carried once.
	if !strings.Contains(html, "1 category,") {
		t.Error(`the one-node template announces itself as "1 categories"`)
	}
	if strings.Contains(html, "1 categories") {
		t.Error(`the plural leaked back in: "1 categories"`)
	}
}
