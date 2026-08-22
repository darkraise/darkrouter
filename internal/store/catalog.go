package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// ModelCapabilities is the JSON shape of the models.capabilities column.
type ModelCapabilities struct {
	Tools     bool `json:"tools"`
	Vision    bool `json:"vision"`
	Reasoning bool `json:"reasoning"`
}

// ModelRow is one models row, decoded.
type ModelRow struct {
	ProviderID         string
	ModelID            string
	Publisher          string
	Surfaces           []string
	Capabilities       ModelCapabilities
	CapabilitiesSource string
	ContextWindow      int
	MaxOutputTokens    int

	InputMicrosPerMTok     int64
	OutputMicrosPerMTok    int64
	CacheReadMicrosPerMTok int64
	// PriceKnown separates "free" from "we never found out". Both read back as
	// zero, and the UI shows them differently.
	PriceKnown bool

	State         string
	MissingStreak int
	LastSeenAt    time.Time
	DiscoveredAt  time.Time
}

// ModelOverride is one model_overrides row. Every field is a pointer or a nil
// slice, because the table is per-field: an override that sets capabilities
// must not silently reset the context window.
type ModelOverride struct {
	ProviderID    string
	ModelID       string
	Surfaces      []string
	Capabilities  *ModelCapabilities
	ContextWindow *int
}

// MetadataRow is what the models.dev sync writes. It carries no lifecycle
// fields by construction: a metadata refresh must not resurrect a model
// discovery retired.
type MetadataRow struct {
	ProviderID             string
	ModelID                string
	Publisher              string
	Surfaces               []string
	Capabilities           ModelCapabilities
	CapabilitiesSource     string
	ContextWindow          int
	MaxOutputTokens        int
	InputMicrosPerMTok     int64
	OutputMicrosPerMTok    int64
	CacheReadMicrosPerMTok int64
}

const modelColumns = `provider_id, model_id, publisher, surfaces, capabilities,
	capabilities_source, context_window, max_output_tokens,
	input_price_micros_per_mtok, output_price_micros_per_mtok,
	cache_read_price_micros_per_mtok, discovered_at, state,
	missing_streak, last_seen_at`

// Models returns every catalogued model, including the ones discovery has
// retired. The snapshot filters; the store reports.
func (d *DB) Models(ctx context.Context) ([]ModelRow, error) {
	rows, err := d.Read.QueryContext(ctx,
		`SELECT `+modelColumns+` FROM models ORDER BY provider_id, model_id`)
	if err != nil {
		return nil, fmt.Errorf("list models: %w", err)
	}
	defer rows.Close()

	var out []ModelRow
	for rows.Next() {
		var (
			r                          ModelRow
			surfaces, caps             string
			ctxWin, maxOut             sql.NullInt64
			inPrice, outPrice, cachePr sql.NullInt64
			discovered, lastSeen       sql.NullInt64
		)
		if err := rows.Scan(&r.ProviderID, &r.ModelID, &r.Publisher, &surfaces, &caps,
			&r.CapabilitiesSource, &ctxWin, &maxOut, &inPrice, &outPrice, &cachePr,
			&discovered, &r.State, &r.MissingStreak, &lastSeen); err != nil {
			return nil, fmt.Errorf("scan model: %w", err)
		}
		// A malformed JSON column is a corrupt row, not a fatal error: one bad
		// row must not make the whole catalog unreadable. It degrades to the
		// zero value, which the merge then treats as unknown.
		_ = json.Unmarshal([]byte(surfaces), &r.Surfaces)
		_ = json.Unmarshal([]byte(caps), &r.Capabilities)

		r.ContextWindow = int(ctxWin.Int64)
		r.MaxOutputTokens = int(maxOut.Int64)
		r.InputMicrosPerMTok = inPrice.Int64
		r.OutputMicrosPerMTok = outPrice.Int64
		r.CacheReadMicrosPerMTok = cachePr.Int64
		r.PriceKnown = inPrice.Valid || outPrice.Valid
		if lastSeen.Valid {
			r.LastSeenAt = time.UnixMilli(lastSeen.Int64).UTC()
		}
		if discovered.Valid {
			r.DiscoveredAt = time.UnixMilli(discovered.Int64).UTC()
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// ModelOverrides returns the operator's per-(provider, model) corrections.
func (d *DB) ModelOverrides(ctx context.Context) ([]ModelOverride, error) {
	rows, err := d.Read.QueryContext(ctx,
		`SELECT provider_id, model_id, surfaces, capabilities, context_window
		   FROM model_overrides ORDER BY provider_id, model_id`)
	if err != nil {
		return nil, fmt.Errorf("list model overrides: %w", err)
	}
	defer rows.Close()

	var out []ModelOverride
	for rows.Next() {
		var (
			o              ModelOverride
			surfaces, caps sql.NullString
			ctxWin         sql.NullInt64
		)
		if err := rows.Scan(&o.ProviderID, &o.ModelID, &surfaces, &caps, &ctxWin); err != nil {
			return nil, fmt.Errorf("scan model override: %w", err)
		}
		if surfaces.Valid {
			_ = json.Unmarshal([]byte(surfaces.String), &o.Surfaces)
		}
		if caps.Valid {
			var c ModelCapabilities
			if json.Unmarshal([]byte(caps.String), &c) == nil {
				o.Capabilities = &c
			}
		}
		if ctxWin.Valid {
			n := int(ctxWin.Int64)
			o.ContextWindow = &n
		}
		out = append(out, o)
	}
	return out, rows.Err()
}

// UpsertMetadata writes the models.dev half of a model's record.
//
// It is an UPDATE rather than an upsert on purpose. models.dev knows models a
// given provider does not offer, and inserting them would make the catalog
// claim reachability it has no evidence for — existence is discovery's to
// decide, per spec section 7. The columns it does not name are equally
// deliberate: state, missing_streak and last_seen_at belong to discovery.
func (d *DB) UpsertMetadata(ctx context.Context, rows []MetadataRow) error {
	if len(rows) == 0 {
		return nil
	}
	tx, err := d.Write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin metadata write: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	stmt, err := tx.PrepareContext(ctx,
		`UPDATE models SET
		    publisher = ?, surfaces = ?, capabilities = ?, capabilities_source = ?,
		    context_window = ?, max_output_tokens = ?,
		    input_price_micros_per_mtok = ?, output_price_micros_per_mtok = ?,
		    cache_read_price_micros_per_mtok = ?
		  WHERE provider_id = ? AND model_id = ?`)
	if err != nil {
		return fmt.Errorf("prepare metadata write: %w", err)
	}
	defer stmt.Close()

	for _, r := range rows {
		surfaces, err := json.Marshal(nonEmptySurfaces(r.Surfaces))
		if err != nil {
			return err
		}
		caps, err := json.Marshal(r.Capabilities)
		if err != nil {
			return err
		}
		if _, err := stmt.ExecContext(ctx,
			r.Publisher, string(surfaces), string(caps), r.CapabilitiesSource,
			nullableInt(r.ContextWindow), nullableInt(r.MaxOutputTokens),
			nullableInt64(r.InputMicrosPerMTok), nullableInt64(r.OutputMicrosPerMTok),
			nullableInt64(r.CacheReadMicrosPerMTok),
			r.ProviderID, r.ModelID); err != nil {
			return fmt.Errorf("write metadata for %s/%s: %w", r.ProviderID, r.ModelID, err)
		}
	}
	return tx.Commit()
}

// nonEmptySurfaces keeps the column's invariant: every model serves at least
// the llm surface unless something said otherwise.
func nonEmptySurfaces(s []string) []string {
	if len(s) == 0 {
		return []string{"llm"}
	}
	return s
}

// nullableInt writes NULL for zero, because zero context window and unknown
// context window are different facts and the column is how they stay apart.
func nullableInt(n int) any {
	if n == 0 {
		return nil
	}
	return n
}

func nullableInt64(n int64) any {
	if n == 0 {
		return nil
	}
	return n
}
