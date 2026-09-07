// Package settings stores the deployment values an administrator can change
// without restarting the application.
//
// It is a key/value table read as a handful of named strings. There is no
// service layer: the only rule these values have is validation, which belongs to
// the domain and lives on core.Settings, so a service here would be a pass-through.
package settings

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/atvirokodosprendimai/app-warehousev1/internal/core"
)

// Repo reads and writes settings.
//
// It holds the application's two handles: reads go through read, writes through
// write. The reader carries query_only(1), so a write taking the wrong path is
// refused by the driver rather than found in review.
type Repo struct {
	read  *sql.DB
	write *sql.DB
}

// NewRepo returns a Repo over the two handles.
func NewRepo(read, write *sql.DB) *Repo { return &Repo{read: read, write: write} }

// Settings returns the stored values.
//
// A missing row is not an error and not a zero value to be feared: it means
// "nothing has been set", and the caller falls back to its configured default.
// That is the ordinary state of a fresh installation, so returning ErrNotFound
// here would make every caller handle the normal case as an exception.
func (r *Repo) Settings(ctx context.Context) (core.Settings, error) {
	rows, err := r.read.QueryContext(ctx,
		`SELECT key, value, updated_at FROM settings`)
	if err != nil {
		return core.Settings{}, fmt.Errorf("settings: read: %w", err)
	}
	defer rows.Close()

	var out core.Settings
	for rows.Next() {
		var key, value, updated string
		if err := rows.Scan(&key, &value, &updated); err != nil {
			return core.Settings{}, fmt.Errorf("settings: scan: %w", err)
		}
		if t, err := time.Parse(time.RFC3339, updated); err == nil && t.After(out.UpdatedAt) {
			out.UpdatedAt = t
		}
		switch key {
		case core.SettingPublicBaseURL:
			out.PublicBaseURL = value
		case core.SettingEbayCategory:
			out.Ebay.Category = value
		case core.SettingEbayConditionID:
			out.Ebay.ConditionID = value
		case core.SettingEbayLocation:
			out.Ebay.Location = value
		}
		// An unknown key is ignored rather than refused: a downgrade should not
		// fail to start because a newer version left a row behind.
	}
	if err := rows.Err(); err != nil {
		return core.Settings{}, fmt.Errorf("settings: read: %w", err)
	}
	return out, nil
}

// SaveSettings writes the values, replacing whatever was there.
//
// Every key moves in ONE transaction. There is only one key today, and that is
// exactly when this is cheap to get right: a later second setting would
// otherwise arrive as a second UPDATE that can fail on its own, leaving the pair
// half-applied.
func (r *Repo) SaveSettings(ctx context.Context, s core.Settings) error {
	tx, err := r.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("settings: begin: %w", err)
	}
	defer tx.Rollback()

	now := time.Now().UTC().Format(time.RFC3339)
	pairs := map[string]string{
		core.SettingPublicBaseURL:   s.PublicBaseURL,
		core.SettingEbayCategory:    s.Ebay.Category,
		core.SettingEbayConditionID: s.Ebay.ConditionID,
		core.SettingEbayLocation:    s.Ebay.Location,
	}
	for key, value := range pairs {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO settings (key, value, updated_at) VALUES (?, ?, ?)
			ON CONFLICT (key) DO UPDATE SET value = excluded.value,
			                                updated_at = excluded.updated_at`,
			key, value, now); err != nil {
			return fmt.Errorf("settings: save %s: %w", key, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("settings: commit: %w", err)
	}
	return nil
}
