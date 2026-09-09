// Package marketplace stores the lists of values an operator picks from when
// saying where an item is listed — eBay's category, its condition code, the city
// it dispatches from (ADR-022).
//
// ⚠ IT HOLDS THE MENU, NOT THE CHOICE. What one OFFER chose lives in
// `offer_marketplace_values` and is owned by [internal/offer], because it is part
// of that aggregate. This package owns only what may be chosen.
//
// ⚠ AND IT IS NOT [internal/taxonomy]. That package holds the operator's own tree
// of what a thing IS and its questions are inherited down a hierarchy. These
// lists are flat, per export profile, and describe what a MARKETPLACE demands.
// ADR-022's first task exists because those two ideas were one word apart.
//
// There is no Service beside this Repo, unlike taxonomy. Nothing here spans more
// than one row: an option is added, removed or listed, and the invariants that
// matter live in [core.MarketplaceOption.Validate] and in the table's own UNIQUE
// constraint. A service layer would be a file that forwards.
package marketplace

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
)

// ErrValueTaken reports an option whose value is already offered for the same
// profile and field.
//
// It is a named sentinel rather than a raw constraint error because the operator
// has to be told which of their two entries is the duplicate; a wrapped SQLite
// "UNIQUE constraint failed" reaches the screen as "Something went wrong".
var ErrValueTaken = errors.New("marketplace: that value is already on the list")

// Repo reads and writes the option lists.
type Repo struct {
	read  *sql.DB
	write *sql.DB
}

// NewRepo returns a Repo over the two handles [internal/store] opens: a pooled
// reader carrying query_only, and the single-connection writer.
func NewRepo(read, write *sql.DB) *Repo { return &Repo{read: read, write: write} }

// Options returns everything on offer for one profile, ordered for display.
//
// The order is position, then label, then value. Position is the operator's;
// label and value break ties so the same list never renders in two orders — two
// rows sharing a position is the ordinary state after adding several without
// reordering, not an error.
//
// An empty result is the ordinary case for a fresh installation and is not an
// error: it means the dropdown has nothing to offer yet, which the editor renders
// as a free-text box rather than an empty menu.
func (r *Repo) Options(ctx context.Context, profile string) ([]core.MarketplaceOption, error) {
	profile = strings.ToLower(strings.TrimSpace(profile))

	rows, err := r.read.QueryContext(ctx, `
		SELECT id, profile, field, value, label, position
		FROM marketplace_options
		WHERE profile = ?
		ORDER BY position, label, value`, profile)
	if err != nil {
		return nil, fmt.Errorf("marketplace: list options for %s: %w", profile, err)
	}
	defer rows.Close()

	var out []core.MarketplaceOption
	for rows.Next() {
		var o core.MarketplaceOption
		if err := rows.Scan(&o.ID, &o.Profile, &o.Field, &o.Value, &o.Label, &o.Position); err != nil {
			return nil, fmt.Errorf("marketplace: list options for %s: %w", profile, err)
		}
		out = append(out, o)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("marketplace: list options for %s: %w", profile, err)
	}
	return out, nil
}

// AddOption puts one value on a profile's list, allocating an id when the caller
// has not.
//
// It validates before writing, so an option that could never reach a marketplace
// is refused with a sentence rather than stored and discovered later. A value
// already on the same list is [ErrValueTaken].
func (r *Repo) AddOption(ctx context.Context, o core.MarketplaceOption) (core.MarketplaceOption, error) {
	o.Profile = strings.ToLower(strings.TrimSpace(o.Profile))
	o.Field = core.MarketplaceField(strings.ToLower(strings.TrimSpace(string(o.Field))))
	o.Value = strings.TrimSpace(o.Value)
	o.Label = strings.TrimSpace(o.Label)

	if err := o.Validate(); err != nil {
		return core.MarketplaceOption{}, err
	}
	if o.ID == "" {
		o.ID = uuid.NewString()
	}

	_, err := r.write.ExecContext(ctx, `
		INSERT INTO marketplace_options (id, profile, field, value, label, position)
		VALUES (?, ?, ?, ?, ?, ?)`,
		o.ID, o.Profile, string(o.Field), o.Value, o.Label, o.Position)
	if err != nil {
		// The UNIQUE constraint is the one failure the operator can act on, so it
		// gets a name. Everything else is this deployment's problem, not theirs.
		if strings.Contains(err.Error(), "UNIQUE constraint failed") {
			return core.MarketplaceOption{}, fmt.Errorf("%w: %q is already offered for %s %s",
				ErrValueTaken, o.Value, o.Profile, o.Field)
		}
		return core.MarketplaceOption{}, fmt.Errorf("marketplace: add option: %w", err)
	}
	return o, nil
}

// RemoveOption takes one value off a list.
//
// Removing an option an offer has already chosen does NOT change that offer: the
// chosen value lives on the offer, and withdrawing it from the menu is not a
// decision to re-list stock somewhere else. The offer keeps exporting what it
// says until somebody changes it.
//
// A missing id is not an error. Two people removing the same row is the ordinary
// double-submit, and reporting the second as a failure would be reporting the
// state they both wanted.
func (r *Repo) RemoveOption(ctx context.Context, id string) error {
	if _, err := r.write.ExecContext(ctx,
		`DELETE FROM marketplace_options WHERE id = ?`, id); err != nil {
		return fmt.Errorf("marketplace: remove option %s: %w", id, err)
	}
	return nil
}
