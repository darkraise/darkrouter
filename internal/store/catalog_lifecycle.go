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
	return out, rows.Err()
}

// RecordDiscoverySuccess applies one successful listing.
//
// Everything listed becomes live with its counters cleared, including a model
// previously retired — spec §5.1's "recovery clears both". Everything the
// provider still has a row for and this listing omitted has its omission
// counter advanced, and crosses to removed_upstream on the third.
//
// The whole update is one transaction: a crash between the upserts and the
// omission sweep would otherwise leave models both listed and counted absent.
func (d *DB) RecordDiscoverySuccess(ctx context.Context, providerID string,
	seen []DiscoveredModel, at time.Time) error {

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
		                     last_seen_at, discovered_at, context_window, max_output_tokens)
		 VALUES (?, ?, 'live', '["llm"]', 0, ?, ?, ?, ?)
		 ON CONFLICT(provider_id, model_id) DO UPDATE SET
		     state             = 'live',
		     missing_streak    = 0,
		     last_seen_at      = excluded.last_seen_at,
		     discovered_at     = coalesce(models.discovered_at, excluded.discovered_at),
		     context_window    = coalesce(excluded.context_window, models.context_window),
		     max_output_tokens = coalesce(excluded.max_output_tokens, models.max_output_tokens)`)
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

	ids := make([]any, 0, len(seen))
	for _, m := range seen {
		if m.ModelID == "" {
			continue
		}
		if _, err := up.ExecContext(ctx, providerID, m.ModelID, ms, ms,
			nullableInt(m.ContextWindow), nullableInt(m.MaxOutputTokens)); err != nil {
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
		ids = append(ids, m.ModelID)
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
		`INSERT INTO provider_discovery (provider_id, consecutive_failures, last_attempt_at, last_success_at, last_error)
		 VALUES (?, 0, ?, ?, '')
		 ON CONFLICT(provider_id) DO UPDATE SET
		     consecutive_failures = 0,
		     last_attempt_at      = excluded.last_attempt_at,
		     last_success_at      = excluded.last_success_at,
		     last_error           = ''`,
		providerID, ms, ms); err != nil {
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
