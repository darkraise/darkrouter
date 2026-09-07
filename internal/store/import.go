package store

import (
	"context"
	"fmt"
	"time"
)

const settingProvidersImportedAt = "providers_imported_at"

// ImportResult reports what the import decided. Imported is false for the
// ordinary case of a database that has already been through it.
type ImportResult struct {
	Imported  bool
	Providers int
	At        time.Time
}

// ImportedAt returns when the first-run import ran, if it ever did.
func ImportedAt(ctx context.Context, d *DB) (time.Time, bool, error) {
	raw, ok, err := getSetting(ctx, d.Read, settingProvidersImportedAt)
	if err != nil || !ok {
		return time.Time{}, false, err
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, false, fmt.Errorf("stored import marker is not a timestamp: %w", err)
	}
	return t, true, nil
}
