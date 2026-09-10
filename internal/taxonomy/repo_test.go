package taxonomy

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
	"github.com/atvirokodosprendimai/app-warehousev1/internal/store"
	"github.com/atvirokodosprendimai/app-warehousev1/migrations"
)

// newTestRepo opens a fresh database, runs the REAL migrations over it, and
// returns a Repo and a Service wired the way the application wires them.
//
// ⚠ The embedded migrations, never a copy of the CREATE TABLE statements.
// ADR-013 exists because four packages in this repository did carry such a copy
// with a comment asking whoever changed the schema to keep it in step, and they
// drifted at the first opportunity — a suite green against a schema production
// does not have. A fixture that restates the schema is a second source of truth
// and the test reading it cannot tell when it stopped being true.
//
// store.Open also gives these tests the real handle split: one writer with
// _txlock=immediate, and a reader carrying query_only(1) so the driver REFUSES a
// write on a read path rather than merely being asked not to.
func newTestRepo(t *testing.T) (*Repo, *Service) {
	t.Helper()

	db, err := store.Open(filepath.Join(t.TempDir(), "taxonomy_test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := store.Migrate(db.Write, migrations.FS); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	r := NewRepo(db.Read, db.Write)
	return r, NewService(r)
}

// mustCategory creates a node or fails the test.
func mustCategory(t *testing.T, s *Service, parentID, code, name string) core.Category {
	t.Helper()
	c, err := s.Create(context.Background(), parentID, core.Category{Code: code, Name: name})
	if err != nil {
		t.Fatalf("create %q under %q: %v", code, parentID, err)
	}
	return c
}

// mustField attaches a question or fails the test.
func mustField(t *testing.T, s *Service, categoryID, code, label string, kind core.FieldKind, pos int) core.CategoryField {
	t.Helper()
	f, err := s.AddField(context.Background(), categoryID, core.CategoryField{
		Code: code, Label: label, Kind: kind, Position: pos,
	})
	if err != nil {
		t.Fatalf("add field %q to %q: %v", code, categoryID, err)
	}
	return f
}

// mustOffer inserts a bare draft directly, because this package has no reason to
// depend on internal/offer just to have something to hang an answer off.
func mustOffer(t *testing.T, r *Repo, id, sku string) string {
	t.Helper()
	_, err := r.write.ExecContext(context.Background(),
		`INSERT INTO offers (id, sku, title, created_at, updated_at)
		 VALUES (?,?,?,'2026-09-07T00:00:00Z','2026-09-07T00:00:00Z')`, id, sku, "a thing")
	if err != nil {
		t.Fatalf("insert offer %s: %v", id, err)
	}
	return id
}

// TestFieldsInheritFromEveryAncestor is ADR-021's Enforced-by check.
//
// It is the whole record in one assertion: a node three deep is asked its own
// questions AND both ancestors', root first. Nothing is copied down the tree, so
// this can only pass if the read actually walks the ancestors.
func TestFieldsInheritFromEveryAncestor(t *testing.T) {
	r, s := newTestRepo(t)
	ctx := context.Background()

	car := mustCategory(t, s, "", "CAR", "Car parts")
	engine := mustCategory(t, s, car.ID, "ENGINE", "Engine")
	turbo := mustCategory(t, s, engine.ID, "TURBO", "Turbocharger")

	// Positions are deliberately out of insertion order, so that a read which
	// merely returned rows in the order they were written would fail.
	mustField(t, s, car.ID, "vin", "VIN", core.FieldText, 1)
	mustField(t, s, car.ID, "year", "Year", core.FieldNumber, 0)
	mustField(t, s, engine.ID, "engine_code", "Engine code", core.FieldText, 0)
	mustField(t, s, turbo.ID, "turbo_number", "Turbo number", core.FieldText, 0)

	got, err := r.ResolvedFields(ctx, turbo.ID)
	if err != nil {
		t.Fatalf("ResolvedFields: %v", err)
	}

	want := []string{"Year", "VIN", "Engine code", "Turbo number"}
	if len(got) != len(want) {
		t.Fatalf("ResolvedFields returned %d fields, want %d: %+v", len(got), len(want), got)
	}
	for i, label := range want {
		if got[i].Label != label {
			t.Errorf("field %d = %q, want %q — root-first, then position within each "+
				"level, which is the order a person thinks in", i, got[i].Label, label)
		}
	}

	// Each resolved field says which level it came from, so a screen can tell an
	// operator why they are being asked.
	if got[0].CategoryPath != "CAR" {
		t.Errorf("Year came from %q, want %q", got[0].CategoryPath, "CAR")
	}
	if got[3].CategoryPath != "CAR/ENGINE/TURBO" {
		t.Errorf("Turbo number came from %q, want %q", got[3].CategoryPath, "CAR/ENGINE/TURBO")
	}

	// A node higher up is asked strictly less, which is the other half of
	// inheritance and would pass vacuously if the walk went the wrong way.
	up, err := r.ResolvedFields(ctx, car.ID)
	if err != nil {
		t.Fatalf("ResolvedFields(car): %v", err)
	}
	if len(up) != 2 {
		t.Errorf("the root resolves %d fields, want 2 — inheritance runs DOWN the tree "+
			"only, and a root inheriting its descendants' questions would be every "+
			"question in the warehouse on every offer", len(up))
	}
}

// TestAddingAFieldToAParentReachesOffersAlreadyFiled is the case copying fields
// down at write time would lose, and it is the reason the parent ADR rejects
// that design.
func TestAddingAFieldToAParentReachesOffersAlreadyFiled(t *testing.T) {
	r, s := newTestRepo(t)
	ctx := context.Background()

	car := mustCategory(t, s, "", "CAR", "Car parts")
	turbo := mustCategory(t, s, car.ID, "TURBO", "Turbocharger")
	offer := mustOffer(t, r, "o1", "WH0000001")

	before, err := r.OfferFields(ctx, offer, turbo.ID)
	if err != nil {
		t.Fatalf("OfferFields before: %v", err)
	}
	if len(before) != 0 {
		t.Fatalf("a category with no fields asks %d questions, want 0", len(before))
	}

	// The field is added to the PARENT, after the offer already exists.
	mustField(t, s, car.ID, "vin", "VIN", core.FieldText, 0)

	after, err := r.OfferFields(ctx, offer, turbo.ID)
	if err != nil {
		t.Fatalf("OfferFields after: %v", err)
	}
	if len(after) != 1 || after[0].Label != "VIN" {
		t.Fatalf("adding a field to the parent did not reach the offer already filed "+
			"beneath it: got %+v", after)
	}
}

// TestMovingASubtreeRewritesEveryDescendantPath pins the trap ADR-005 paid for
// once already: the prefix match must be substr, not LIKE.
//
// "A_B" is a legal code and "_" is a LIKE wildcard, so a LIKE-based rewrite would
// silently catch the unrelated root "AXB" and re-address a subtree nobody
// touched. The assertion on the SIBLING is the one that matters — a rewrite that
// moved everything would pass on the moved subtree alone.
func TestMovingASubtreeRewritesEveryDescendantPath(t *testing.T) {
	r, s := newTestRepo(t)
	ctx := context.Background()

	moved := mustCategory(t, s, "", "A_B", "Underscored")
	movedChild := mustCategory(t, s, moved.ID, "CHILD", "Child of the underscored")

	bystander := mustCategory(t, s, "", "AXB", "Matches A_B under LIKE")
	bystanderChild := mustCategory(t, s, bystander.ID, "CHILD", "Child of the bystander")

	if err := s.Rename(ctx, moved.ID, "RENAMED"); err != nil {
		t.Fatalf("rename: %v", err)
	}

	got, err := r.Category(ctx, movedChild.ID)
	if err != nil {
		t.Fatalf("read moved child: %v", err)
	}
	if got.Path != "RENAMED/CHILD" {
		t.Errorf("the moved subtree's child is at %q, want %q — a descendant whose path "+
			"still names the old address is a path that lies", got.Path, "RENAMED/CHILD")
	}

	untouched, err := r.Category(ctx, bystanderChild.ID)
	if err != nil {
		t.Fatalf("read bystander child: %v", err)
	}
	if untouched.Path != "AXB/CHILD" {
		t.Errorf("the BYSTANDER subtree moved too: %q, want %q. \"_\" is a LIKE wildcard "+
			"and \"A_B\" is a legal code, so the descendant rewrite must use "+
			"substr(path,1,n), which has no metacharacters", untouched.Path, "AXB/CHILD")
	}
}

// TestASecondRootWithTheSameCodeIsRefused pins the constraint that actually does
// the work at the root.
//
// UNIQUE(parent_id, code) is INERT there, because SQL treats every NULL as
// distinct from every other, so two roots called "CAR" would both insert. It is
// UNIQUE(path) that refuses them — and the refusal therefore arrives as
// ErrPathTaken rather than ErrCodeTaken, which is why a handler has to match
// both.
func TestASecondRootWithTheSameCodeIsRefused(t *testing.T) {
	_, s := newTestRepo(t)
	ctx := context.Background()

	mustCategory(t, s, "", "CAR", "Car parts")
	_, err := s.Create(ctx, "", core.Category{Code: "CAR", Name: "Car parts again"})
	if err == nil {
		t.Fatal("a second root with the same code was accepted; UNIQUE(parent_id, code) " +
			"cannot catch it because every NULL parent is distinct, so UNIQUE(path) " +
			"has to be there and evidently is not")
	}
	if !errors.Is(err, ErrPathTaken) {
		t.Errorf("duplicate root refused as %v, want ErrPathTaken — at the root the path "+
			"index is what fires, and a handler highlighting the code field matches "+
			"both sentinels for exactly this reason", err)
	}

	// A duplicate DEEPER node takes the other branch, which is the asymmetry the
	// sentinels exist to describe.
	car, err := s.store.CategoryChildren(ctx, "")
	if err != nil || len(car) != 1 {
		t.Fatalf("expected exactly one root, got %+v (%v)", car, err)
	}
	mustCategory(t, s, car[0].ID, "ENGINE", "Engine")
	_, err = s.Create(ctx, car[0].ID, core.Category{Code: "ENGINE", Name: "Engine again"})
	if !errors.Is(err, ErrCodeTaken) {
		t.Errorf("duplicate sibling refused as %v, want ErrCodeTaken", err)
	}
}

// TestAValueSurvivesRenamingItsField is why answers are keyed by field ID rather
// than by code.
func TestAValueSurvivesRenamingItsField(t *testing.T) {
	r, s := newTestRepo(t)
	ctx := context.Background()

	car := mustCategory(t, s, "", "CAR", "Car parts")
	vin := mustField(t, s, car.ID, "vin", "VIN", core.FieldText, 0)
	offer := mustOffer(t, r, "o1", "WH0000001")

	if err := s.Answer(ctx, offer, vin.ID, "WVWZZZ1JZXW000001"); err != nil {
		t.Fatalf("answer: %v", err)
	}

	vin.Code = "chassis"
	vin.Label = "Chassis number"
	if err := s.UpdateField(ctx, vin); err != nil {
		t.Fatalf("rename field: %v", err)
	}

	got, err := r.OfferFields(ctx, offer, car.ID)
	if err != nil {
		t.Fatalf("OfferFields: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d fields, want 1", len(got))
	}
	if got[0].Value != "WVWZZZ1JZXW000001" {
		t.Errorf("the answer did not survive the rename: value = %q. Answers are keyed "+
			"by field ID precisely so that renaming the question cannot orphan them",
			got[0].Value)
	}
	if got[0].Label != "Chassis number" {
		t.Errorf("label = %q, want the new one", got[0].Label)
	}
}

// TestDeletingAFieldDeletesItsValues pins the cascade, which is deliberate: an
// answer whose question no longer exists cannot be interpreted.
func TestDeletingAFieldDeletesItsValues(t *testing.T) {
	r, s := newTestRepo(t)
	ctx := context.Background()

	car := mustCategory(t, s, "", "CAR", "Car parts")
	vin := mustField(t, s, car.ID, "vin", "VIN", core.FieldText, 0)
	one := mustOffer(t, r, "o1", "WH0000001")
	two := mustOffer(t, r, "o2", "WH0000002")

	for _, id := range []string{one, two} {
		if err := s.Answer(ctx, id, vin.ID, "something"); err != nil {
			t.Fatalf("answer for %s: %v", id, err)
		}
	}

	n, err := s.DeleteField(ctx, vin.ID)
	if err != nil {
		t.Fatalf("delete field: %v", err)
	}
	if n != 2 {
		t.Errorf("DeleteField reported %d answers taken, want 2 — the count is read "+
			"BEFORE the delete so a screen can warn about a deliberate loss before it "+
			"happens", n)
	}

	var left int
	if err := r.read.QueryRowContext(ctx,
		`SELECT count(*) FROM offer_field_values`).Scan(&left); err != nil {
		t.Fatalf("count: %v", err)
	}
	if left != 0 {
		t.Errorf("%d answers outlived their question; the cascade is not in effect", left)
	}
}

// TestAnEmptyAnswerIsNotStored keeps "never answered" and "answered with
// nothing" one state, because no screen could tell the two apart.
func TestAnEmptyAnswerIsNotStored(t *testing.T) {
	r, s := newTestRepo(t)
	ctx := context.Background()

	car := mustCategory(t, s, "", "CAR", "Car parts")
	vin := mustField(t, s, car.ID, "vin", "VIN", core.FieldText, 0)
	offer := mustOffer(t, r, "o1", "WH0000001")

	if err := s.Answer(ctx, offer, vin.ID, "abc"); err != nil {
		t.Fatalf("answer: %v", err)
	}
	if err := s.Answer(ctx, offer, vin.ID, "   "); err != nil {
		t.Fatalf("blank answer: %v", err)
	}

	var n int
	if err := r.read.QueryRowContext(ctx,
		`SELECT count(*) FROM offer_field_values`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Errorf("a blank answer left %d row(s) behind; clearing a field must delete "+
			"the row rather than store an empty string", n)
	}
}

// TestACategoryWithChildrenIsNotDeleted checks that the refusal an operator sees
// names the thing they have to deal with, rather than the database's bare
// FOREIGN KEY message.
func TestACategoryWithChildrenIsNotDeleted(t *testing.T) {
	_, s := newTestRepo(t)
	ctx := context.Background()

	car := mustCategory(t, s, "", "CAR", "Car parts")
	mustCategory(t, s, car.ID, "ENGINE", "Engine")

	if err := s.Delete(ctx, car.ID); !errors.Is(err, ErrHasChildren) {
		t.Errorf("delete of a node with children = %v, want ErrHasChildren", err)
	}
}

// TestACategoryCannotMoveIntoItsOwnSubtree keeps the tree a tree.
func TestACategoryCannotMoveIntoItsOwnSubtree(t *testing.T) {
	_, s := newTestRepo(t)
	ctx := context.Background()

	car := mustCategory(t, s, "", "CAR", "Car parts")
	engine := mustCategory(t, s, car.ID, "ENGINE", "Engine")

	if err := s.Move(ctx, car.ID, engine.ID); !errors.Is(err, ErrCycle) {
		t.Errorf("moving a node into its own child = %v, want ErrCycle", err)
	}
	if err := s.Move(ctx, car.ID, car.ID); !errors.Is(err, ErrCycle) {
		t.Errorf("moving a node into itself = %v, want ErrCycle", err)
	}
}

// TestAnOfferWithNoCategoryIsAskedNothing pins that a category is optional,
// which ADR-004 and ADR-019 both require of the intake flow.
func TestAnOfferWithNoCategoryIsAskedNothing(t *testing.T) {
	r, _ := newTestRepo(t)
	ctx := context.Background()

	offer := mustOffer(t, r, "o1", "WH0000001")
	got, err := r.OfferFields(ctx, offer, "")
	if err != nil {
		t.Fatalf("OfferFields with no category: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("an uncategorised offer is asked %d questions, want 0", len(got))
	}
}
