package store

import "context"

// GetSetting reads one row of the settings table, reporting whether it exists.
func (d *DB) GetSetting(ctx context.Context, key string) (string, bool, error) {
	return getSetting(ctx, d.Read, key)
}

// PutSetting writes one row, replacing any value the key already had.
func (d *DB) PutSetting(ctx context.Context, key, value string) error {
	return putSetting(ctx, d.Write, key, value)
}

// DeleteSetting removes one row. A missing key is not an error: the caller
// wanted it gone, and it is.
func (d *DB) DeleteSetting(ctx context.Context, key string) error {
	if _, err := d.Write.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, key); err != nil {
		return err
	}
	return nil
}
