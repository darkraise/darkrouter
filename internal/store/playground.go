package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// PlaygroundPreset is a named request configuration.
//
// Config is carried as raw JSON rather than a struct: the store is not the
// authority on what a request setting is, and decoding it here would silently
// drop any field the console had learned and this build had not.
type PlaygroundPreset struct {
	ID        string
	Name      string
	Dialect   string
	Model     string
	Config    json.RawMessage
	CreatedAt time.Time
	UpdatedAt time.Time
}

func newPresetID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate preset id: %w", err)
	}
	return hex.EncodeToString(b), nil
}

func (d *DB) CreatePlaygroundPreset(
	ctx context.Context, name, dialect, model string, config json.RawMessage,
) (PlaygroundPreset, error) {
	id, err := newPresetID()
	if err != nil {
		return PlaygroundPreset{}, err
	}
	now := time.Now().UTC()
	p := PlaygroundPreset{
		ID: id, Name: name, Dialect: dialect, Model: model,
		Config: config, CreatedAt: now, UpdatedAt: now,
	}
	_, err = d.Write.ExecContext(ctx,
		`INSERT INTO playground_presets (id, name, dialect, model, config, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Name, p.Dialect, p.Model, string(p.Config), now.Unix(), now.Unix())
	if err != nil {
		return PlaygroundPreset{}, fmt.Errorf("store playground preset: %w", err)
	}
	return p, nil
}

func (d *DB) PlaygroundPresets(ctx context.Context) ([]PlaygroundPreset, error) {
	rows, err := d.Read.QueryContext(ctx,
		`SELECT id, name, dialect, model, config, created_at, updated_at
		   FROM playground_presets ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("list playground presets: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []PlaygroundPreset{}
	for rows.Next() {
		p, err := scanPreset(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// PlaygroundPresetByName finds the preset a name already belongs to, so a save
// that would clash can offer to overwrite that row rather than reporting a
// constraint failure.
func (d *DB) PlaygroundPresetByName(ctx context.Context, name string) (PlaygroundPreset, bool, error) {
	row := d.Read.QueryRowContext(ctx,
		`SELECT id, name, dialect, model, config, created_at, updated_at
		   FROM playground_presets WHERE name = ?`, name)
	p, err := scanPreset(row)
	if errors.Is(err, sql.ErrNoRows) {
		return PlaygroundPreset{}, false, nil
	}
	if err != nil {
		return PlaygroundPreset{}, false, err
	}
	return p, true, nil
}

func (d *DB) UpdatePlaygroundPreset(
	ctx context.Context, id, name, dialect, model string, config json.RawMessage,
) (bool, error) {
	res, err := d.Write.ExecContext(ctx,
		`UPDATE playground_presets
		    SET name = ?, dialect = ?, model = ?, config = ?, updated_at = ?
		  WHERE id = ?`,
		name, dialect, model, string(config), time.Now().UTC().Unix(), id)
	if err != nil {
		return false, fmt.Errorf("update playground preset: %w", err)
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

func (d *DB) DeletePlaygroundPreset(ctx context.Context, id string) (bool, error) {
	res, err := d.Write.ExecContext(ctx, `DELETE FROM playground_presets WHERE id = ?`, id)
	if err != nil {
		return false, fmt.Errorf("delete playground preset: %w", err)
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// scanner is what *sql.Row and *sql.Rows have in common, so one scan serves
// the lookup and the listing.
type scanner interface{ Scan(dest ...any) error }

func scanPreset(s scanner) (PlaygroundPreset, error) {
	var (
		p                = PlaygroundPreset{}
		cfg              string
		created, updated int64
	)
	if err := s.Scan(&p.ID, &p.Name, &p.Dialect, &p.Model, &cfg, &created, &updated); err != nil {
		return PlaygroundPreset{}, err
	}
	p.Config = json.RawMessage(cfg)
	p.CreatedAt = time.Unix(created, 0).UTC()
	p.UpdatedAt = time.Unix(updated, 0).UTC()
	return p, nil
}
