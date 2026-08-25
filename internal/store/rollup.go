package store

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"time"

	"github.com/darkraise/darkrouter/internal/config"
)

// defaultLogRetention mirrors config's own default so a caller that forgets
// to pass a retention gets the safe behaviour rather than an empty window.
const defaultLogRetention = 720 * time.Hour

// Rollup recomputes usage_daily for yesterday and today in UTC.
//
// Two days rather than one: a request logged just after midnight belongs to
// yesterday, and the batching writer may not have flushed it before the day
// turned. Recomputing yesterday on every run is what finalizes it.
func (d *DB) Rollup(ctx context.Context, now time.Time, logRetention time.Duration) error {
	if logRetention <= 0 {
		logRetention = defaultLogRetention
	}
	utc := now.UTC()
	startOfToday := time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
	from := startOfToday.AddDate(0, 0, -1)
	to := startOfToday.AddDate(0, 0, 1)

	// A day may only be recomputed when none of its requests can have been
	// pruned. Every request in a day has a timestamp at or after that day's
	// midnight, so the day is safe exactly while its midnight is still inside
	// the retention window. Recomputing a day pruning has already eaten into
	// would replace a finalized total with a smaller one and leave nothing to
	// say it is incomplete.
	cutoff := utc.Add(-logRetention)
	cutoffDay := time.Date(cutoff.Year(), cutoff.Month(), cutoff.Day(),
		0, 0, 0, 0, time.UTC)
	if safeFrom := cutoffDay.AddDate(0, 0, 1); safeFrom.After(from) {
		from = safeFrom
	}
	if !from.Before(to) {
		// Retention is shorter than a day, so no day can be rolled up whole.
		// Saying so once per run beats an empty usage table with no
		// explanation.
		log.Printf("rollup: log.retention %s leaves no complete day to roll up", logRetention)
		return nil
	}

	// The window's rows are cleared rather than upserted. 0006 widened the key
	// with alias, so a recomputed group no longer matches the row a narrower
	// key wrote: upserting alone would leave the old row behind and double the
	// day permanently. But the clear only reaches days the requests table can
	// still rebuild: retention prunes requests well before usage_daily's
	// retention, so a finalized day can enter this window with nothing left
	// to recompute it from, and clearing it unconditionally would erase it
	// for good.
	tx, err := d.Write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("rollup: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`DELETE FROM usage_daily
		  WHERE day IN (
		        SELECT DISTINCT strftime('%Y-%m-%d', ts / 1000, 'unixepoch')
		          FROM requests
		         WHERE ts >= ? AND ts < ?)`,
		from.UnixMilli(), to.UnixMilli()); err != nil {
		return fmt.Errorf("rollup clear: %w", err)
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO usage_daily (day, provider_id, model, alias, requests, attempts, tokens_in, tokens_out, cost_micros)
		 SELECT day, provider_id, model, alias,
		        sum(is_served), sum(is_attempt), sum(t_in), sum(t_out),
		        CASE WHEN count(c) = 0 THEN NULL ELSE sum(c) END
		   FROM (
		     -- Attributed to the attempt's OWN provider: a failover's discarded
		     -- tokens were burned where they were tried, not where the retry
		     -- happened to succeed.
		     SELECT strftime('%Y-%m-%d', r.ts / 1000, 'unixepoch') AS day,
		            a.provider_id AS provider_id, a.model AS model,
		            r.resolved_alias AS alias,
		            -- Only the serving attempt counts as a request, so summing
		            -- this column across providers still equals the real
		            -- request count. Keyed on the outcome rather than on
		            -- matching the request's final provider: the pre-commit 400
		            -- retry re-attempts the SAME provider and model, so a
		            -- provider match identifies two rows where one served.
		            CASE WHEN a.outcome = 'success' THEN 1 ELSE 0 END AS is_served,
		            1 AS is_attempt,
		            coalesce(a.tokens_in, 0) AS t_in,
		            coalesce(a.tokens_out, 0) AS t_out,
		            a.cost_micros AS c
		       FROM requests r
		       JOIN request_attempts a ON a.request_id = r.id
		      WHERE r.ts >= ? AND r.ts < ?
		     UNION ALL
		     -- A request that predates attempt rows still has its own counts.
		     SELECT strftime('%Y-%m-%d', r.ts / 1000, 'unixepoch'),
		            r.final_provider_id, r.final_model, r.resolved_alias,
		            1, 0,
		            coalesce(r.tokens_in, 0), coalesce(r.tokens_out, 0),
		            r.cost_micros
		       FROM requests r
		      WHERE r.ts >= ? AND r.ts < ?
		        AND r.final_provider_id <> ''
		        AND NOT EXISTS (
		              SELECT 1 FROM request_attempts a WHERE a.request_id = r.id)
		   )
		  GROUP BY day, provider_id, model, alias`,
		from.UnixMilli(), to.UnixMilli(), from.UnixMilli(), to.UnixMilli()); err != nil {
		return fmt.Errorf("rollup: %w", err)
	}
	return tx.Commit()
}

// RunRollup runs the rollup on an interval until ctx is cancelled.
//
// The interval is jittered so that a restart of several services does not line
// every worker up on the same instant.
func RunRollup(ctx context.Context, d *DB, cfgStore *config.Store, interval time.Duration) error {
	if interval <= 0 {
		interval = time.Hour
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(jitter(interval)):
			cfg := cfgStore.Current()
			if err := d.Rollup(ctx, time.Now(), cfg.Log.Retention); err != nil {
				// Logged, not fatal: a missed rollup is recomputed on the next
				// run, because finalization is idempotent.
				log.Printf("rollup: %v", err)
			}
		}
	}
}

// jitter spreads a worker's wakeups over the last quarter of its interval.
func jitter(d time.Duration) time.Duration {
	if d <= 0 {
		return d
	}
	return d - time.Duration(rand.Int63n(int64(d/4)+1))
}
