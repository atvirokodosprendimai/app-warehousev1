// Package taxonomy stores the operator's own tree of what a thing IS, the
// questions each node asks, and the answers an offer gives them.
//
// It is deliberately the same shape as [internal/location]: an adjacency list
// plus a materialised path, where a node's id is immutable and its path is the
// human address that has to be rewritten the moment anything above it moves.
// Reading that package first is the cheapest way to understand this one, and the
// three traps its comments record — substr rather than LIKE, rune offsets rather
// than byte offsets, and UNIQUE(path) rather than UNIQUE(parent_id, code) at the
// root — apply here unchanged.
//
// What is NEW here is INHERITANCE. A field defined on "Car parts" is asked of
// everything beneath it, resolved at READ time from the ancestor walk. Nothing
// is ever copied down, so adding a question to a parent reaches the offers
// already filed below it — the case that matters most, and the one a copy-down
// design loses.
//
// ⚠ This is NOT the marketplace category. `offer_categories` (ADR-016) says
// where to LIST an item on eBay; this package says what the item IS.
package taxonomy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
)

// Repo reads and writes the taxonomy in SQLite.
//
// It holds the two handles the application opens over one SQLite file: reads are
// served by the many-reader handle, every mutation goes to the single-writer
// handle. The read handle is opened query_only, so a write attempted on it is
// refused by the driver rather than by this type's discipline.
type Repo struct {
	read  *sql.DB
	write *sql.DB
}

// NewRepo returns a Repo serving reads from read and writes through write.
func NewRepo(read, write *sql.DB) *Repo {
	return &Repo{read: read, write: write}
}

// Repo implements both the domain port and the transactional subtree move the
// service needs; the assertions keep that a compile error rather than a runtime
// surprise.
var (
	_ core.TaxonomyStore = (*Repo)(nil)
	_ Store              = (*Repo)(nil)
)

// categoryColumns is the column list every category read shares, in the order
// [scanCategory] expects.
const categoryColumns = `id, parent_id, code, path, name, position`

// fieldColumns is the stored column list for a field. CategoryPath is NOT here:
// it is not stored, it is filled in by the reads that resolve inheritance so a
// field can say which level it came from without a second query.
const fieldColumns = `id, category_id, code, label, kind, unit, options, required, export, position`

// rowScanner is satisfied by both *sql.Row and *sql.Rows, so one scan function
// serves the single-row and the multi-row reads.
type rowScanner interface {
	Scan(dest ...any) error
}

// scanCategory reads one row in categoryColumns order.
func scanCategory(s rowScanner) (core.Category, error) {
	var (
		c      core.Category
		parent sql.NullString
	)
	if err := s.Scan(&c.ID, &parent, &c.Code, &c.Path, &c.Name, &c.Position); err != nil {
		return core.Category{}, err
	}
	c.ParentID = parent.String
	return c, nil
}

// scanCategories drains rows into a slice.
func scanCategories(rows *sql.Rows) ([]core.Category, error) {
	defer rows.Close()
	out := []core.Category{}
	for rows.Next() {
		c, err := scanCategory(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// parentValue maps an empty parent id to SQL NULL, which is how a root is
// stored.
func parentValue(parentID string) any {
	if parentID == "" {
		return nil
	}
	return parentID
}

// boolInt renders a Go bool for a STRICT INTEGER column. The conversion is
// explicit because a STRICT table refuses a value whose storage class is not the
// declared one, and what a driver binds a bool as is the driver's business.
func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// Category returns one node by id, or an error wrapping [core.ErrNotFound].
func (r *Repo) Category(ctx context.Context, id string) (core.Category, error) {
	row := r.read.QueryRowContext(ctx,
		`SELECT `+categoryColumns+` FROM categories WHERE id = ?`, id)
	c, err := scanCategory(row)
	if errors.Is(err, sql.ErrNoRows) {
		return core.Category{}, fmt.Errorf("category %s: %w", id, core.ErrNotFound)
	}
	if err != nil {
		return core.Category{}, fmt.Errorf("taxonomy: read %s: %w", id, err)
	}
	return c, nil
}

// CategoryChildren returns the direct children of parentID.
//
// An EMPTY parentID returns the roots, because a root's parent_id is NULL and
// "= NULL" matches nothing in SQL — the two cases need different statements, not
// different arguments.
func (r *Repo) CategoryChildren(ctx context.Context, parentID string) ([]core.Category, error) {
	var (
		rows *sql.Rows
		err  error
	)
	if parentID == "" {
		rows, err = r.read.QueryContext(ctx,
			`SELECT `+categoryColumns+` FROM categories WHERE parent_id IS NULL ORDER BY position, name`)
	} else {
		rows, err = r.read.QueryContext(ctx,
			`SELECT `+categoryColumns+` FROM categories WHERE parent_id = ? ORDER BY position, name`, parentID)
	}
	if err != nil {
		return nil, fmt.Errorf("taxonomy: children of %q: %w", parentID, err)
	}
	out, err := scanCategories(rows)
	if err != nil {
		return nil, fmt.Errorf("taxonomy: children of %q: %w", parentID, err)
	}
	return out, nil
}

// categoryChain walks parent_id upward from a node. depth counts hops from the
// node, so the root carries the LARGEST depth and ORDER BY depth DESC yields
// root-first — the order inheritance resolves in, general question before
// specific one, which is also the order a person thinks in.
const categoryChain = `
WITH RECURSIVE chain(depth, id, parent_id, code, path, name, position) AS (
    SELECT 0, ` + categoryColumns + ` FROM categories WHERE id = ?
    UNION ALL
    SELECT c.depth + 1, k.id, k.parent_id, k.code, k.path, k.name, k.position
    FROM categories k JOIN chain c ON k.id = c.parent_id
)`

// CategoryAncestors returns the chain from the root down to, but excluding, id.
//
// A root has no ancestors and an unknown id has none either; neither is an
// error, because the caller has already loaded the node it is asking about.
func (r *Repo) CategoryAncestors(ctx context.Context, id string) ([]core.Category, error) {
	rows, err := r.read.QueryContext(ctx,
		categoryChain+`
SELECT `+categoryColumns+` FROM chain WHERE depth > 0 ORDER BY depth DESC`, id)
	if err != nil {
		return nil, fmt.Errorf("taxonomy: ancestors of %s: %w", id, err)
	}
	out, err := scanCategories(rows)
	if err != nil {
		return nil, fmt.Errorf("taxonomy: ancestors of %s: %w", id, err)
	}
	return out, nil
}

// AllCategories returns every node ordered by path.
//
// Path order is tree order: a node sorts immediately before its own subtree and
// after its earlier siblings, so a picker renders the result as an indented tree
// without sorting or grouping it again.
func (r *Repo) AllCategories(ctx context.Context) ([]core.Category, error) {
	rows, err := r.read.QueryContext(ctx,
		`SELECT `+categoryColumns+` FROM categories ORDER BY path`)
	if err != nil {
		return nil, fmt.Errorf("taxonomy: list: %w", err)
	}
	out, err := scanCategories(rows)
	if err != nil {
		return nil, fmt.Errorf("taxonomy: list: %w", err)
	}
	return out, nil
}

// scanField reads one field row. path is the category path the read resolved
// alongside it, which may be empty when the read did not join it.
func scanField(s rowScanner, extra ...any) (core.CategoryField, error) {
	var (
		f        core.CategoryField
		kind     string
		required int
		export   int
	)
	dest := []any{&f.ID, &f.CategoryID, &f.Code, &f.Label, &kind, &f.Unit, &f.Options,
		&required, &export, &f.Position}
	dest = append(dest, extra...)
	if err := s.Scan(dest...); err != nil {
		return core.CategoryField{}, err
	}
	f.Kind = core.FieldKind(kind)
	f.Required = required != 0
	f.Export = export != 0
	return f, nil
}

// OwnFields returns the fields defined ON categoryID, without its ancestors'.
func (r *Repo) OwnFields(ctx context.Context, categoryID string) ([]core.CategoryField, error) {
	rows, err := r.read.QueryContext(ctx,
		`SELECT `+fieldColumns+` FROM category_fields WHERE category_id = ? ORDER BY position, label`,
		categoryID)
	if err != nil {
		return nil, fmt.Errorf("taxonomy: own fields of %s: %w", categoryID, err)
	}
	defer rows.Close()
	out := []core.CategoryField{}
	for rows.Next() {
		f, err := scanField(rows)
		if err != nil {
			return nil, fmt.Errorf("taxonomy: own fields of %s: %w", categoryID, err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// ResolvedFields returns every field categoryID asks — its own and each
// ancestor's — root first, and by position within each level.
//
// This is the inheritance, and it is resolved HERE rather than stored: nothing
// is copied down the tree, so a field added to a parent is asked of every offer
// already filed beneath it the next time one is read.
func (r *Repo) ResolvedFields(ctx context.Context, categoryID string) ([]core.CategoryField, error) {
	if categoryID == "" {
		return []core.CategoryField{}, nil
	}
	rows, err := r.read.QueryContext(ctx,
		categoryChain+`
SELECT f.id, f.category_id, f.code, f.label, f.kind, f.unit, f.options, f.required,
       f.export, f.position, ch.path
FROM category_fields f JOIN chain ch ON ch.id = f.category_id
ORDER BY ch.depth DESC, f.position, f.label`, categoryID)
	if err != nil {
		return nil, fmt.Errorf("taxonomy: resolved fields of %s: %w", categoryID, err)
	}
	defer rows.Close()
	out := []core.CategoryField{}
	for rows.Next() {
		var path string
		f, err := scanField(rows, &path)
		if err != nil {
			return nil, fmt.Errorf("taxonomy: resolved fields of %s: %w", categoryID, err)
		}
		f.CategoryPath = path
		out = append(out, f)
	}
	return out, rows.Err()
}

// OfferFields returns [Repo.ResolvedFields] for categoryID with offerID's
// answers filled in.
//
// A LEFT JOIN, not an inner one: a question nobody has answered yet must still
// be asked, and it is the ordinary state of a freshly filed offer.
func (r *Repo) OfferFields(ctx context.Context, offerID, categoryID string) ([]core.OfferField, error) {
	if categoryID == "" {
		return []core.OfferField{}, nil
	}
	rows, err := r.read.QueryContext(ctx,
		categoryChain+`
SELECT f.id, f.category_id, f.code, f.label, f.kind, f.unit, f.options, f.required,
       f.export, f.position, ch.path, COALESCE(v.value, '')
FROM category_fields f
JOIN chain ch ON ch.id = f.category_id
LEFT JOIN offer_field_values v ON v.field_id = f.id AND v.offer_id = ?
ORDER BY ch.depth DESC, f.position, f.label`, categoryID, offerID)
	if err != nil {
		return nil, fmt.Errorf("taxonomy: fields of offer %s: %w", offerID, err)
	}
	defer rows.Close()
	out := []core.OfferField{}
	for rows.Next() {
		var path, value string
		f, err := scanField(rows, &path, &value)
		if err != nil {
			return nil, fmt.Errorf("taxonomy: fields of offer %s: %w", offerID, err)
		}
		f.CategoryPath = path
		out = append(out, core.OfferField{CategoryField: f, Value: value})
	}
	return out, rows.Err()
}

// CountFieldValues reports how many stored answers a field has.
//
// It exists so that deleting a question can say what it will take with it BEFORE
// it does. The cascade is deliberate — an answer whose question no longer exists
// cannot be interpreted — but a deliberate loss still has to be visible.
func (r *Repo) CountFieldValues(ctx context.Context, fieldID string) (int, error) {
	var n int
	err := r.read.QueryRowContext(ctx,
		`SELECT count(*) FROM offer_field_values WHERE field_id = ?`, fieldID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("taxonomy: count values of %s: %w", fieldID, err)
	}
	return n, nil
}

// CreateCategory inserts a node exactly as given. Assigning the id and composing
// the path are [Service.Create]'s job, so a caller cannot insert a node whose
// path disagrees with its parent's.
func (r *Repo) CreateCategory(ctx context.Context, c core.Category) error {
	_, err := r.write.ExecContext(ctx,
		`INSERT INTO categories (`+categoryColumns+`) VALUES (?,?,?,?,?,?)`,
		c.ID, parentValue(c.ParentID), c.Code, c.Path, c.Name, c.Position)
	if err != nil {
		return fmt.Errorf("taxonomy: insert %q: %w", c.Path, uniqueViolation(err))
	}
	return nil
}

// UpdateCategory writes the mutable columns of c.
//
// It re-addresses ONLY the row it is given. A node's path is a prefix of every
// path beneath it, so changing path or parent here would leave the descendants
// pointing at an address that no longer exists — use [Service.Rename] or
// [Service.Move], which rewrite the subtree in one transaction.
func (r *Repo) UpdateCategory(ctx context.Context, c core.Category) error {
	res, err := r.write.ExecContext(ctx,
		`UPDATE categories SET name = ?, position = ? WHERE id = ?`,
		c.Name, c.Position, c.ID)
	if err != nil {
		return fmt.Errorf("taxonomy: update %s: %w", c.ID, uniqueViolation(err))
	}
	return affectedOne(res, c.ID)
}

// DeleteCategory removes one node.
//
// The database refuses while a child still points at it, but its FOREIGN KEY
// error does not say WHICH reference held it. Classifying it is [Service.Delete]'s
// job: it rules out children first, so any refusal that survives is stock.
func (r *Repo) DeleteCategory(ctx context.Context, id string) error {
	res, err := r.write.ExecContext(ctx, `DELETE FROM categories WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("taxonomy: delete %s: %w", id, err)
	}
	return affectedOne(res, id)
}

// CreateField attaches a question to a node.
func (r *Repo) CreateField(ctx context.Context, f core.CategoryField) error {
	_, err := r.write.ExecContext(ctx,
		`INSERT INTO category_fields (`+fieldColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?)`,
		f.ID, f.CategoryID, f.Code, f.Label, string(f.Kind), f.Unit, f.Options,
		boolInt(f.Required), boolInt(f.Export), f.Position)
	if err != nil {
		return fmt.Errorf("taxonomy: insert field %q: %w", f.Code, uniqueViolation(err))
	}
	return nil
}

// UpdateField rewrites a question.
//
// The id and the category never move, because answers are keyed by the id and
// moving a question between levels would change who is asked it — that is a
// delete and a create, and it should look like one.
func (r *Repo) UpdateField(ctx context.Context, f core.CategoryField) error {
	res, err := r.write.ExecContext(ctx,
		`UPDATE category_fields SET code = ?, label = ?, kind = ?, unit = ?, options = ?,
		     required = ?, export = ?, position = ?
		 WHERE id = ?`,
		f.Code, f.Label, string(f.Kind), f.Unit, f.Options,
		boolInt(f.Required), boolInt(f.Export), f.Position, f.ID)
	if err != nil {
		return fmt.Errorf("taxonomy: update field %s: %w", f.ID, uniqueViolation(err))
	}
	return affectedOne(res, f.ID)
}

// DeleteField removes a question and, by cascade, every answer to it.
func (r *Repo) DeleteField(ctx context.Context, id string) error {
	res, err := r.write.ExecContext(ctx, `DELETE FROM category_fields WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("taxonomy: delete field %s: %w", id, err)
	}
	return affectedOne(res, id)
}

// SetFieldValue stores one answer, keyed by FIELD ID.
//
// An empty value DELETES the row rather than storing a blank string, so "never
// answered" and "answered with nothing" stay one state instead of two that no
// screen could tell apart.
func (r *Repo) SetFieldValue(ctx context.Context, offerID, fieldID, value string) error {
	if strings.TrimSpace(value) == "" {
		_, err := r.write.ExecContext(ctx,
			`DELETE FROM offer_field_values WHERE offer_id = ? AND field_id = ?`, offerID, fieldID)
		if err != nil {
			return fmt.Errorf("taxonomy: clear value %s/%s: %w", offerID, fieldID, err)
		}
		return nil
	}
	_, err := r.write.ExecContext(ctx,
		`INSERT INTO offer_field_values (offer_id, field_id, value) VALUES (?,?,?)
		 ON CONFLICT (offer_id, field_id) DO UPDATE SET value = excluded.value`,
		offerID, fieldID, value)
	if err != nil {
		return fmt.Errorf("taxonomy: set value %s/%s: %w", offerID, fieldID, err)
	}
	return nil
}

// MoveSubtree re-addresses c and everything beneath it in ONE transaction.
//
// c must already carry its new ParentID, Code and Path; oldPath is the address it
// is leaving. Splitting the two updates across transactions would leave a window
// in which a descendant's path names a node that is no longer there, and a reader
// in that window gets a wrong answer rather than an error.
func (r *Repo) MoveSubtree(ctx context.Context, c core.Category, oldPath string) error {
	tx, err := r.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("taxonomy: begin move of %q: %w", oldPath, err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op once Commit has succeeded

	res, err := tx.ExecContext(ctx,
		`UPDATE categories SET parent_id = ?, code = ?, path = ? WHERE id = ?`,
		parentValue(c.ParentID), c.Code, c.Path, c.ID)
	if err != nil {
		return fmt.Errorf("taxonomy: re-address %q as %q: %w", oldPath, c.Path, uniqueViolation(err))
	}
	if err := affectedOne(res, c.ID); err != nil {
		return err
	}

	// The descendants are exactly the rows whose path begins with the old path
	// plus a separator. The separator matters: without it, renaming "CAR" would
	// also catch the unrelated root "CARAVAN".
	//
	// ⚠ The prefix is matched with substr and NOT with LIKE. "_" and "%" are LIKE
	// wildcards and this schema forbids only "/" in a code, so a real code like
	// "A_B" turned into a LIKE pattern would silently match "AXB" and rewrite a
	// subtree nobody touched. substr has no metacharacters: nothing to escape and
	// nothing to get wrong.
	//
	// ⚠ SQLite's substr and length count CHARACTERS, not bytes, so the offsets are
	// RUNE counts. A node called "ŠASI" would otherwise slice one rune short of
	// its separator and match none of its own children.
	oldPrefix := oldPath + "/"
	prefixLen := utf8.RuneCountInString(oldPrefix)
	if _, err := tx.ExecContext(ctx,
		`UPDATE categories SET path = ? || substr(path, ?) WHERE substr(path, 1, ?) = ?`,
		c.Path+"/", prefixLen+1, prefixLen, oldPrefix); err != nil {
		return fmt.Errorf("taxonomy: re-address subtree of %q: %w", oldPath, uniqueViolation(err))
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("taxonomy: commit move of %q: %w", oldPath, err)
	}
	return nil
}

// CreateTree inserts a whole starter tree — every category and every question
// they ask — in ONE transaction.
//
// ⚠ ALL OR NOTHING IS THE WHOLE POINT. A template is thirty-odd statements, and
// a half-applied one looks exactly like a finished one: the operator would have
// to work out which of seven categories and twenty-three questions had arrived
// before they could safely try again. The refusal that actually happens here is
// a duplicate root, which fails on the first statement — but only a transaction
// makes that true of the thirty after it as well.
//
// Categories must arrive PARENTS FIRST. Each row carries a path composed from
// its parent's, so the ordering is the caller's to get right; there is no second
// pass here that could fix it up.
func (r *Repo) CreateTree(ctx context.Context, cats []core.Category, fields []core.CategoryField) error {
	tx, err := r.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("taxonomy: begin tree of %d categories: %w", len(cats), err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op once Commit has succeeded

	for _, c := range cats {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO categories (`+categoryColumns+`) VALUES (?,?,?,?,?,?)`,
			c.ID, parentValue(c.ParentID), c.Code, c.Path, c.Name, c.Position); err != nil {
			return fmt.Errorf("taxonomy: insert %q: %w", c.Path, uniqueViolation(err))
		}
	}
	for _, f := range fields {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO category_fields (`+fieldColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?)`,
			f.ID, f.CategoryID, f.Code, f.Label, string(f.Kind), f.Unit, f.Options,
			boolInt(f.Required), boolInt(f.Export), f.Position); err != nil {
			return fmt.Errorf("taxonomy: insert question %q: %w", f.Code, uniqueViolation(err))
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("taxonomy: commit tree of %d categories: %w", len(cats), err)
	}
	return nil
}

// affectedOne turns "no such row" into [core.ErrNotFound]. A write that matched
// nothing is a failure the caller has to hear about: silently succeeding is how a
// double-submitted delete reads as a delete that worked.
func affectedOne(res sql.Result, id string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("taxonomy: rows affected for %s: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("taxonomy %s: %w", id, core.ErrNotFound)
	}
	return nil
}

// uniqueViolation maps SQLite's UNIQUE refusals onto the package's sentinels,
// leaving anything else untouched.
//
// ⚠ Which index fires is NOT symmetric between a root and a deeper node.
// UNIQUE(parent_id, code) is inert at the root, because SQL treats every NULL
// parent_id as distinct from every other, so a duplicate ROOT arrives as
// [ErrPathTaken] — which for a root is the same statement, since a root's path
// IS its code. A handler highlighting the code field should match both.
func uniqueViolation(err error) error {
	switch msg := err.Error(); {
	case strings.Contains(msg, "UNIQUE constraint failed: categories.parent_id, categories.code"):
		return fmt.Errorf("%w: %w", ErrCodeTaken, err)
	case strings.Contains(msg, "UNIQUE constraint failed: categories.path"):
		return fmt.Errorf("%w: %w", ErrPathTaken, err)
	case strings.Contains(msg, "UNIQUE constraint failed: category_fields.category_id, category_fields.code"):
		return fmt.Errorf("%w: %w", ErrFieldCodeTaken, err)
	default:
		return err
	}
}

// restrictedByReference reports whether err is the database refusing to remove a
// row something still points at.
func restrictedByReference(err error) bool {
	return err != nil && strings.Contains(err.Error(), "FOREIGN KEY constraint failed")
}
