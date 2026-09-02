package store

import (
	"context"
	"fmt"
)

// GetSetting reads one row of the settings table, reporting whether it exists.
func (d *DB) GetSetting(ctx context.Context, key string) (string, bool, error) {
	return getSetting(ctx, d.Read, key)
}

// PutSetting writes one row, replacing any value the key already had.
func (d *DB) PutSetting(ctx context.Context, key, value string) error {
	return putSetting(ctx, d.Write, key, value)
}

// InitSetting writes value under key only when the key is absent and returns
// whatever the row holds afterwards. Two processes opening one database must
// not fail on the race, and whichever wrote first wins.
func (d *DB) InitSetting(ctx context.Context, key, value string) (string, error) {
	if _, err := d.Write.ExecContext(ctx,
		`INSERT OR IGNORE INTO settings (key, value) VALUES (?, ?)`, key, value); err != nil {
		return "", fmt.Errorf("init setting %q: %w", key, err)
	}
	stored, ok, err := getSetting(ctx, d.Read, key)
	if err != nil {
		return "", err
	}
	if !ok {
		return "", fmt.Errorf("init setting %q: the row vanished after insert", key)
	}
	return stored, nil
}

// DeleteSetting removes one row. A missing key is not an error: the caller
// wanted it gone, and it is.
func (d *DB) DeleteSetting(ctx context.Context, key string) error {
	if _, err := d.Write.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, key); err != nil {
		return err
	}
	return nil
}
