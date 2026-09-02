package store

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ProviderRow is a provider as the admin API sees it: the database row without
// its credentials, which are never returned together because no endpoint may
// reveal credential material.
type ProviderRow struct {
	ID        string
	Name      string
	Preset    string
	Kind      string
	BaseURL   string
	AuthStyle string
	Priority  int
	Enabled   bool
	Region    string
	Project   string
	// Location is Vertex's regional endpoint. It is set at creation and not
	// patchable: changing it moves every catalogued model to a different host.
	Location string
	// FreeModelsOnly narrows what a discovery sweep imports for this provider
	// to the models it can show are free. It is a filter on the catalogue, not
	// on routing: a paid model already in the catalogue stays routable until
	// the next sweep drops it.
	FreeModelsOnly bool
}

// ProviderPatch is a partial update. Every field is a pointer because a partial
// update has to distinguish "set this to its zero value" from "do not touch
// it", and priority 0 is a legal value meaning last resort.
type ProviderPatch struct {
	Name     *string `json:"name"`
	BaseURL  *string `json:"base_url"`
	Priority *int    `json:"priority"`
	Enabled  *bool   `json:"enabled"`
	Region   *string `json:"region"`
	Project  *string `json:"project"`
	// FreeModelsOnly is patchable so an operator can change their mind without
	// deleting a provider they cannot recreate: the set is defined in code.
	FreeModelsOnly *bool `json:"free_models_only"`
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func (d *DB) CreateProvider(ctx context.Context, p ProviderRow) error {
	enabled := 0
	if p.Enabled {
		enabled = 1
	}
	if p.AuthStyle == "" {
		// Matches the column default. Sent explicitly so a row created here has
		// the same shape as one created by ImportFromConfig.
		p.AuthStyle = "bearer"
	}
	if _, err := d.Write.ExecContext(ctx,
		`INSERT INTO providers
		   (id, name, preset, kind, base_url, auth_style, priority, enabled,
		    region, project, location, free_models_only, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Name, p.Preset, p.Kind, p.BaseURL, p.AuthStyle,
		p.Priority, enabled, p.Region, p.Project, p.Location,
		boolToInt(p.FreeModelsOnly), time.Now().UnixMilli()); err != nil {
		return fmt.Errorf("create provider %q: %w", p.ID, err)
	}
	return nil
}

// UpdateProvider applies a partial update, and reports an error when the id does
// not exist rather than succeeding silently. A PATCH against a deleted provider
// that returned success is how a settings screen shows an edit that never
// happened.
func (d *DB) UpdateProvider(ctx context.Context, id string, patch ProviderPatch) error {
	sets := make([]string, 0, 6)
	args := make([]any, 0, 7)
	// Fixed order rather than a map range: the generated SQL has to be
	// deterministic or SQLite's statement cache misses on every call.
	if patch.Name != nil {
		sets, args = append(sets, "name = ?"), append(args, *patch.Name)
	}
	if patch.BaseURL != nil {
		sets, args = append(sets, "base_url = ?"), append(args, *patch.BaseURL)
	}
	if patch.Priority != nil {
		sets, args = append(sets, "priority = ?"), append(args, *patch.Priority)
	}
	if patch.Enabled != nil {
		v := 0
		if *patch.Enabled {
			v = 1
		}
		sets, args = append(sets, "enabled = ?"), append(args, v)
	}
	if patch.Region != nil {
		sets, args = append(sets, "region = ?"), append(args, *patch.Region)
	}
	if patch.Project != nil {
		sets, args = append(sets, "project = ?"), append(args, *patch.Project)
	}
	if patch.FreeModelsOnly != nil {
		sets = append(sets, "free_models_only = ?")
		args = append(args, boolToInt(*patch.FreeModelsOnly))
	}
	if len(sets) == 0 {
		// An empty patch is a client bug, not a no-op to absorb: it means the
		// UI sent a form it did not fill in.
		return fmt.Errorf("update provider %q: the patch names no fields", id)
	}
	args = append(args, id)

	res, err := d.Write.ExecContext(ctx,
		`UPDATE providers SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
	if err != nil {
		return fmt.Errorf("update provider %q: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update provider %q: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("update provider %q: no such provider", id)
	}
	return nil
}

// DeleteProvider removes the provider. Its credentials, models and discovery
// state go with it through the schema's own ON DELETE CASCADE, which foreign
// keys being on makes real — relisting the children here would be a second
// place to update when a table is added.
//
// It runs on the Sync handle rather than Write because the cascade removes
// credential rows, and credential durability is what that handle exists for: a
// delete lost to a power failure leaves a secret the operator believes is gone.
func (d *DB) DeleteProvider(ctx context.Context, id string) error {
	res, err := d.Sync.ExecContext(ctx, `DELETE FROM providers WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete provider %q: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete provider %q: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("delete provider %q: no such provider", id)
	}
	return nil
}

// DeleteCredential removes one key. On the Sync handle for the same reason
// AddCredential writes there: a credential change lost to a power failure is
// the one kind of lost write that matters here.
func (d *DB) DeleteCredential(ctx context.Context, providerID, keyID string) error {
	res, err := d.Sync.ExecContext(ctx,
		`DELETE FROM provider_keys WHERE provider_id = ? AND id = ?`, providerID, keyID)
	if err != nil {
		return fmt.Errorf("delete credential %q: %w", keyID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete credential %q: %w", keyID, err)
	}
	if n == 0 {
		return fmt.Errorf("delete credential %q: no such credential", keyID)
	}
	return nil
}

// ProviderRows lists every provider, ordered by priority descending then id so
// two calls return the same order and the settings screen does not reshuffle
// between polls.
func (d *DB) ProviderRows(ctx context.Context) ([]ProviderRow, error) {
	rows, err := d.Read.QueryContext(ctx,
		`SELECT id, name, preset, kind, base_url, auth_style, priority, enabled,
		        region, project, location, free_models_only
		   FROM providers ORDER BY priority DESC, id`)
	if err != nil {
		return nil, fmt.Errorf("list providers: %w", err)
	}
	defer rows.Close()

	out := make([]ProviderRow, 0)
	for rows.Next() {
		var p ProviderRow
		var enabled int
		var freeOnly int
		if err := rows.Scan(&p.ID, &p.Name, &p.Preset, &p.Kind, &p.BaseURL,
			&p.AuthStyle, &p.Priority, &enabled, &p.Region, &p.Project, &p.Location,
			&freeOnly); err != nil {
			return nil, fmt.Errorf("list providers: %w", err)
		}
		p.FreeModelsOnly = freeOnly == 1
		p.Enabled = enabled != 0
		out = append(out, p)
	}
	return out, rows.Err()
}
