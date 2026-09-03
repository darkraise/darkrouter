package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"time"
)

// The two thresholds from spec §5.1. Three successful confirmations is the
// middle between union-forever, which leaves retired models routable and
// wastes an attempt on every request, and replace-on-success, which breaks
// every alias on one flaky listing.
const (
	FailuresBeforeStale    = 3
	OmissionsBeforeRemoved = 3
)

// DiscoveredModel is one model a probe reported. Capabilities is nil unless a
// capability probe actually read them from the runtime; a nil is "the probe did
// not say", which must not overwrite what the sync knows.
type DiscoveredModel struct {
	ModelID         string
	ContextWindow   int
	MaxOutputTokens int
	Capabilities    *ModelCapabilities

	// Pricing is nil unless the listing quoted a price. Nil and a zeroed
	// struct are different claims: the second is a free model, which is most
	// of what an aggregator serves.
	Pricing *ModelPricing

	// Publisher selects Vertex's request builder. Empty for a model that came
	// from a listing endpoint, which is every kind but vertex.
	Publisher string
}

// ModelPricing is the four rates a listing quoted, in the catalog's unit of
// micro-dollars per million tokens.
// The two cache rates are pointers because most listings quote neither, and
// migration 0016 states what a zero would claim instead: a provider that
// publishes no cache-write rate must not read as free. Input and output are
// bare because a price with either one missing is not recorded at all.
type ModelPricing struct {
	InputMicrosPerMTok      int64
	OutputMicrosPerMTok     int64
	CacheReadMicrosPerMTok  *int64
	CacheWriteMicrosPerMTok *int64
}

// DiscoveryState is one provider's probe bookkeeping.
type DiscoveryState struct {
	ProviderID          string
	ConsecutiveFailures int
	LastAttemptAt       time.Time
	LastSuccessAt       time.Time
	LastError           string
}

// DiscoveryStates returns every provider's bookkeeping, keyed by provider id.
func (d *DB) DiscoveryStates(ctx context.Context) (map[string]DiscoveryState, error) {
	rows, err := d.Read.QueryContext(ctx,
		`SELECT provider_id, consecutive_failures, last_attempt_at, last_success_at, last_error
		   FROM provider_discovery`)
	if err != nil {
		return nil, fmt.Errorf("read discovery state: %w", err)
	}
	defer rows.Close()

	out := map[string]DiscoveryState{}
	for rows.Next() {
		var (
			s                DiscoveryState
			attempt, success sql.NullInt64
		)
		if err := rows.Scan(&s.ProviderID, &s.ConsecutiveFailures, &attempt, &success, &s.LastError); err != nil {
			return nil, fmt.Errorf("scan discovery state: %w", err)
		}
		if attempt.Valid {
			s.LastAttemptAt = time.UnixMilli(attempt.Int64).UTC()
		}
		if success.Valid {
			s.LastSuccessAt = time.UnixMilli(success.Int64).UTC()
		}
		out[s.ProviderID] = s
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read discovery state: %w", err)
	}
	return out, nil
}

// RecordDiscoverySuccess applies one successful listing.
//
// Everything listed becomes live with its counters cleared, including a model
// previously retired — spec §5.1's "recovery clears both". Everything the
// provider still has a row for and this listing omitted has its omission
// counter advanced, and crosses to removed_upstream on the third.
//
// `dropped` names the models the listing did carry and the operator's import
// filter excluded. Those rows are deleted outright rather than left to the
// omission sweep: the provider still lists them, so retiring them as
// removed_upstream would record a fact about the provider that is not true and
// would leave every one of them on screen as a model this provider offers. A
// filter the operator later widens re-imports them on the next sweep, which is
// the same path a genuinely new model takes.
//
// The whole update is one transaction: a crash between the upserts and the
// omission sweep would otherwise leave models both listed and counted absent.
func (d *DB) RecordDiscoverySuccess(ctx context.Context, providerID string,
	seen []DiscoveredModel, dropped []string, at time.Time) error {

	ms := at.UTC().UnixMilli()
	tx, err := d.Write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin discovery write: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// The upsert names only the columns discovery owns. context_window and
	// max_output_tokens are written on insert and refreshed only when the
	// probe actually carried them, so a probe reporting a bare id cannot
	// overwrite models.dev's numbers with zeroes.
	up, err := tx.PrepareContext(ctx,
		`INSERT INTO models (provider_id, model_id, state, surfaces, missing_streak,
		                     last_seen_at, discovered_at, context_window, max_output_tokens,
		                     publisher)
		 VALUES (?, ?, 'live', '["llm"]', 0, ?, ?, ?, ?, ?)
		 ON CONFLICT(provider_id, model_id) DO UPDATE SET
		     state             = 'live',
		     missing_streak    = 0,
		     last_seen_at      = excluded.last_seen_at,
		     discovered_at     = coalesce(models.discovered_at, excluded.discovered_at),
		     context_window    = coalesce(excluded.context_window, models.context_window),
		     max_output_tokens = coalesce(excluded.max_output_tokens, models.max_output_tokens),
		     -- Empty means "the probe did not say", which for every listing
		     -- kind is always. Overwriting a seeded publisher with it would
		     -- send every Vertex request to the Google builder.
		     publisher         = CASE WHEN excluded.publisher = '' THEN models.publisher
		                              ELSE excluded.publisher END`)
	if err != nil {
		return fmt.Errorf("prepare model upsert: %w", err)
	}
	defer up.Close()

	caps, err := tx.PrepareContext(ctx,
		`UPDATE models SET capabilities = ?, capabilities_source = 'discovered'
		  WHERE provider_id = ? AND model_id = ?`)
	if err != nil {
		return fmt.Errorf("prepare capability write: %w", err)
	}
	defer caps.Close()

	// A price the seller quoted about itself outranks every index, so it takes
	// the row's stamp with it. The statement runs only for a model whose
	// listing carried one: a probe that reported a bare id must leave the
	// price models.dev supplied exactly where it is.
	price, err := tx.PrepareContext(ctx,
		`UPDATE models
		    SET input_price_micros_per_mtok      = ?,
		        output_price_micros_per_mtok     = ?,
		        -- An unquoted cache rate leaves the column as it was. Writing
		        -- the NULL would discard a figure models.dev knows and this
		        -- listing simply does not mention.
		        cache_read_price_micros_per_mtok  = coalesce(?, cache_read_price_micros_per_mtok),
		        cache_write_price_micros_per_mtok = coalesce(?, cache_write_price_micros_per_mtok),
		        price_known  = 1,
		        price_source = 'discovered'
		  WHERE provider_id = ? AND model_id = ?`)
	if err != nil {
		return fmt.Errorf("prepare price write: %w", err)
	}
	defer price.Close()

	ids := make([]any, 0, len(seen))
	for _, m := range seen {
		if m.ModelID == "" {
			continue
		}
		if _, err := up.ExecContext(ctx, providerID, m.ModelID, ms, ms,
			nullableInt(m.ContextWindow), nullableInt(m.MaxOutputTokens), m.Publisher); err != nil {
			return fmt.Errorf("upsert model %q: %w", m.ModelID, err)
		}
		if m.Capabilities != nil {
			blob, err := json.Marshal(*m.Capabilities)
			if err != nil {
				return err
			}
			if _, err := caps.ExecContext(ctx, string(blob), providerID, m.ModelID); err != nil {
				return fmt.Errorf("write capabilities for %q: %w", m.ModelID, err)
			}
		}
		if m.Pricing != nil {
			if _, err := price.ExecContext(ctx,
				m.Pricing.InputMicrosPerMTok, m.Pricing.OutputMicrosPerMTok,
				m.Pricing.CacheReadMicrosPerMTok, m.Pricing.CacheWriteMicrosPerMTok,
				providerID, m.ModelID); err != nil {
				return fmt.Errorf("write price for %q: %w", m.ModelID, err)
			}
		}
		ids = append(ids, m.ModelID)
	}

	// The filtered models leave before the omission sweep runs, so a model the
	// operator excluded is neither kept nor counted as one the provider
	// stopped listing.
	if len(dropped) > 0 {
		args := make([]any, 0, len(dropped)+1)
		args = append(args, providerID)
		for _, id := range dropped {
			args = append(args, id)
		}
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM models WHERE provider_id = ? AND model_id IN (`+
				placeholders(len(dropped))+`)`, args...); err != nil {
			return fmt.Errorf("drop filtered models: %w", err)
		}
	}

	// The omission sweep. A model this listing did not name has its counter
	// advanced; the third omission retires it. Building the NOT IN list from
	// placeholders rather than string concatenation keeps a model id that
	// contains a quote from becoming SQL.
	args := append([]any{providerID}, ids...)
	sweep := `UPDATE models
	             SET missing_streak = missing_streak + 1,
	                 state = CASE WHEN missing_streak + 1 >= ` + strconv.Itoa(OmissionsBeforeRemoved) + `
	                              THEN 'removed_upstream' ELSE state END
	           WHERE provider_id = ?`
	if len(ids) > 0 {
		sweep += ` AND model_id NOT IN (` + placeholders(len(ids)) + `)`
	}
	if _, err := tx.ExecContext(ctx, sweep, args...); err != nil {
		return fmt.Errorf("sweep omitted models: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO provider_discovery
		     (provider_id, consecutive_failures, last_attempt_at, last_success_at,
		      last_error, filtered_out)
		 VALUES (?, 0, ?, ?, '', ?)
		 ON CONFLICT(provider_id) DO UPDATE SET
		     consecutive_failures = 0,
		     last_attempt_at      = excluded.last_attempt_at,
		     last_success_at      = excluded.last_success_at,
		     last_error           = '',
		     filtered_out         = excluded.filtered_out`,
		providerID, ms, ms, len(dropped)); err != nil {
		return fmt.Errorf("record discovery success: %w", err)
	}
	return tx.Commit()
}

// RecordDiscoveryFailure applies one failed probe.
//
// It never touches missing_streak and never retires anything. A provider that
// times out must not empty its half of the catalog: after three consecutive
// failures its live models go stale, which is a display state rather than a
// routing one. The breaker, not the catalog, is what avoids a provider that is
// actually broken.
func (d *DB) RecordDiscoveryFailure(ctx context.Context, providerID string,
	at time.Time, cause string) error {

	ms := at.UTC().UnixMilli()
	tx, err := d.Write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin discovery write: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO provider_discovery (provider_id, consecutive_failures, last_attempt_at, last_error)
		 VALUES (?, 1, ?, ?)
		 ON CONFLICT(provider_id) DO UPDATE SET
		     consecutive_failures = provider_discovery.consecutive_failures + 1,
		     last_attempt_at      = excluded.last_attempt_at,
		     last_error           = excluded.last_error`,
		providerID, ms, cause); err != nil {
		return fmt.Errorf("record discovery failure: %w", err)
	}

	var failures int
	if err := tx.QueryRowContext(ctx,
		`SELECT consecutive_failures FROM provider_discovery WHERE provider_id = ?`,
		providerID).Scan(&failures); err != nil {
		return fmt.Errorf("read failure count: %w", err)
	}
	if failures >= FailuresBeforeStale {
		// Only live rows move. A removed_upstream model stays removed: a
		// failed probe is not evidence it came back.
		if _, err := tx.ExecContext(ctx,
			`UPDATE models SET state = 'stale' WHERE provider_id = ? AND state = 'live'`,
			providerID); err != nil {
			return fmt.Errorf("mark models stale: %w", err)
		}
	}
	return tx.Commit()
}

func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	buf := make([]byte, 0, n*3)
	for i := 0; i < n; i++ {
		if i > 0 {
			buf = append(buf, ',', ' ')
		}
		buf = append(buf, '?')
	}
	return string(buf)
}
