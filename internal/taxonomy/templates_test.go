package taxonomy

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
)

// TestEveryShippedTemplateIsValid is the check that makes a template worth
// shipping at all.
//
// ⚠ A TEMPLATE IS DATA WE WROTE, AND NOTHING ELSE READS IT UNTIL AN OPERATOR
// PRESSES THE BUTTON. A choice with no options, a kind that does not exist, a
// blank label — every one of those compiles, and the first person to find out is
// somebody watching their taxonomy half-appear. This walks the same Validate the
// service walks, so a typo fails here instead of there.
func TestEveryShippedTemplateIsValid(t *testing.T) {
	for _, tpl := range Templates() {
		if strings.TrimSpace(tpl.Code) == "" || strings.TrimSpace(tpl.Name) == "" {
			t.Errorf("a template has no code or no name: %+v", tpl)
			continue
		}
		if strings.TrimSpace(tpl.Summary) == "" {
			t.Errorf("template %q has no summary; the operator is asked to press a "+
				"button that builds a whole tree with nothing saying what is in it", tpl.Code)
		}

		root := core.Category{Code: tpl.Code, Name: tpl.Name}
		if err := root.Validate(); err != nil {
			t.Errorf("template %q's root is not a valid category: %v", tpl.Code, err)
		}
		checkFields(t, tpl.Code, tpl.Fields)

		for _, child := range tpl.Children {
			c := core.Category{Code: child.Code, Name: child.Name}
			if err := c.Validate(); err != nil {
				t.Errorf("template %q child %q is not a valid category: %v", tpl.Code, child.Code, err)
			}
			checkFields(t, tpl.Code+"/"+child.Code, child.Fields)
		}
	}
}

// checkFields validates one category's questions and refuses two that share a
// code, which the database would refuse anyway — at the point where half the
// tree is already committed.
func checkFields(t *testing.T, where string, fields []core.CategoryField) {
	t.Helper()
	seen := map[string]bool{}
	for _, f := range fields {
		if err := f.Validate(); err != nil {
			t.Errorf("%s asks an invalid question %q: %v", where, f.Code, err)
		}
		code := strings.ToLower(strings.TrimSpace(f.Code))
		if seen[code] {
			t.Errorf("%s asks %q twice; UNIQUE(category_id, code) refuses the second, "+
				"and by then the category itself is already inserted", where, code)
		}
		seen[code] = true
		if f.Kind != core.FieldNumber && f.Unit != "" {
			t.Errorf("%s question %q is a %s carrying the unit %q, which is only "+
				"rendered beside a number", where, f.Code, f.Kind, f.Unit)
		}
	}
}

// TestATemplateBuildsItsWholeTreeInOnePress is the behaviour M asked for.
func TestATemplateBuildsItsWholeTreeInOnePress(t *testing.T) {
	repo, svc := newTestRepo(t)
	ctx := context.Background()

	pc, ok := Template("pc")
	if !ok {
		t.Fatal("no PC parts template; Template() should match case-insensitively")
	}
	if err := svc.ApplyTemplate(ctx, pc); err != nil {
		t.Fatalf("apply PC template: %v", err)
	}

	tree, err := repo.AllCategories(ctx)
	if err != nil {
		t.Fatalf("read tree: %v", err)
	}
	if len(tree) != pc.Nodes() {
		t.Fatalf("the template built %d categories, not the %d it advertises", len(tree), pc.Nodes())
	}
	// Path order is tree order, so the root sorts first and its children hang
	// off its own path rather than sitting beside it as second roots.
	if tree[0].Path != "PC" {
		t.Errorf("the root is at %q, not PC", tree[0].Path)
	}
	for _, c := range tree[1:] {
		if !strings.HasPrefix(c.Path, "PC/") {
			t.Errorf("%q is not inside the root the template made; a template that "+
				"scatters roots is worse than one that makes none", c.Path)
		}
		if c.ParentID != tree[0].ID {
			t.Errorf("%q has no parent id pointing at the root, so the tree is only "+
				"correct in its paths", c.Path)
		}
	}
}

// TestATemplatesChildInheritsTheRootsQuestions is the reason a template with
// children is shaped this way rather than repeating the shared questions.
func TestATemplatesChildInheritsTheRootsQuestions(t *testing.T) {
	repo, svc := newTestRepo(t)
	ctx := context.Background()

	pc, _ := Template("PC")
	if err := svc.ApplyTemplate(ctx, pc); err != nil {
		t.Fatalf("apply: %v", err)
	}
	tree, err := repo.AllCategories(ctx)
	if err != nil {
		t.Fatalf("read tree: %v", err)
	}

	var gpu core.Category
	for _, c := range tree {
		if c.Path == "PC/GPU" {
			gpu = c
		}
	}
	if gpu.ID == "" {
		t.Fatal("the template made no PC/GPU")
	}

	fields, err := repo.ResolvedFields(ctx, gpu.ID)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	byCode := map[string]core.CategoryField{}
	for _, f := range fields {
		byCode[f.Code] = f
	}
	// Inherited from the root: asked of a graphics card without anybody writing
	// them there.
	for _, code := range []string{"brand", "model", "mpn", "tested", "grade"} {
		if _, ok := byCode[code]; !ok {
			t.Errorf("a graphics card is not asked %q, which the root defines for "+
				"every component", code)
		}
	}
	// Its own, which its siblings do not get.
	if _, ok := byCode["vram"]; !ok {
		t.Error("a graphics card is not asked its VRAM")
	}
	if f := byCode["vram"]; f.Unit != "GB" {
		t.Errorf("VRAM carries the unit %q rather than GB", f.Unit)
	}
	// And NOT a sibling's: a card has no wattage rating of the kind a power
	// supply does, and asking anyway is what trains an operator to skip
	// questions.
	if _, ok := byCode["wattage"]; ok {
		t.Error("a graphics card is asked the power supply's wattage question; the " +
			"whole reason this template has children is that its questions are not " +
			"all true of every component")
	}
}

// TestApplyingATemplateTwiceIsRefusedAndChangesNothing pins the refusal an
// operator will actually meet.
func TestApplyingATemplateTwiceIsRefusedAndChangesNothing(t *testing.T) {
	repo, svc := newTestRepo(t)
	ctx := context.Background()

	car, _ := Template("CAR")
	if err := svc.ApplyTemplate(ctx, car); err != nil {
		t.Fatalf("first apply: %v", err)
	}
	before, err := repo.AllCategories(ctx)
	if err != nil {
		t.Fatalf("read tree: %v", err)
	}

	err = svc.ApplyTemplate(ctx, car)
	if err == nil {
		t.Fatal("applying the same template twice succeeded; a root's path holds " +
			"exactly one node, so the second tree cannot exist and the operator has " +
			"to be told rather than left to find out")
	}
	// The handler branches on this to say WHICH code is in the way, so the
	// sentinel is part of the contract rather than an implementation detail.
	if !isTaken(err) {
		t.Errorf("the refusal is %v, which the handler cannot recognise as a "+
			"duplicate root; it would fall through to \"something went wrong\"", err)
	}

	after, err := repo.AllCategories(ctx)
	if err != nil {
		t.Fatalf("re-read tree: %v", err)
	}
	if len(after) != len(before) {
		t.Errorf("a refused template left the tree at %d categories, up from %d",
			len(after), len(before))
	}
}

// TestAFailedTemplateLeavesNothingBehind is the one that proves CreateTree's
// transaction, rather than trusting that it is there.
//
// ⚠ THE DUPLICATE-ROOT CASE CANNOT PROVE IT: that fails on the very first
// statement, so a rollback and no transaction at all are indistinguishable. This
// template inserts its root and its first question successfully and then fails
// on a second question sharing a code — the shape where a missing transaction
// leaves a half-built tree that looks finished.
func TestAFailedTemplateLeavesNothingBehind(t *testing.T) {
	repo, svc := newTestRepo(t)
	ctx := context.Background()

	broken := core.CategoryTemplate{
		Code: "BOAT", Name: "Boat parts", Summary: "deliberately malformed",
		Fields: []core.CategoryField{
			{Code: "hull", Label: "Hull number", Kind: core.FieldText},
			{Code: "hull", Label: "Hull number again", Kind: core.FieldText},
		},
	}
	if err := svc.ApplyTemplate(ctx, broken); err == nil {
		t.Fatal("a template asking one question twice was accepted")
	}

	tree, err := repo.AllCategories(ctx)
	if err != nil {
		t.Fatalf("read tree: %v", err)
	}
	if len(tree) != 0 {
		t.Fatalf("a template that failed part-way left %d categories behind. A "+
			"half-applied template is worse than a refused one: it looks finished, "+
			"so the operator cannot tell what arrived and cannot safely retry",
			len(tree))
	}
}

// isTaken reports whether err is the tree refusing a duplicate address, by
// either of the two sentinels that can carry it. A ROOT's duplicate arrives as
// ErrPathTaken because UNIQUE(parent_id, code) is inert when the parent is NULL;
// a child's arrives as ErrCodeTaken.
func isTaken(err error) bool {
	return errors.Is(err, ErrPathTaken) || errors.Is(err, ErrCodeTaken)
}
