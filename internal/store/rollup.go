package store

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"time"
)

// Rollup recomputes usage_daily for yesterday and today in UTC.
//
// Two days rather than one: a request logged just after midnight belongs to
// yesterday, and the batching writer may not have flushed it before the day
// turned. Recomputing yesterday on every run is what finalizes it.
func (d *DB) Rollup(ctx context.Context, now time.Time) error {
	utc := now.UTC()
	startOfToday := time.Date(utc.Year(), utc.Month(), utc.Day(), 0, 0, 0, 0, time.UTC)
	from := startOfToday.AddDate(0, 0, -1)
	to := startOfToday.AddDate(0, 0, 1)

	// The whole window is recomputed and upserted, so running this repeatedly
	// converges rather than accumulating.
	_, err := d.Write.ExecContext(ctx,
		`WITH attempt_usage AS (
		     SELECT request_id,
		            coalesce(sum(tokens_in), 0)  AS a_in,
		            coalesce(sum(tokens_out), 0) AS a_out,
		            CASE WHEN count(cost_micros) = 0 THEN NULL
		                 ELSE sum(cost_micros) END AS a_cost
		       FROM request_attempts
		      GROUP BY request_id
		 )
		 INSERT INTO usage_daily (day, provider_id, model, alias, requests, tokens_in, tokens_out, cost_micros)
		 SELECT strftime('%Y-%m-%d', r.ts / 1000, 'unixepoch') AS day,
		        r.final_provider_id,
		        r.final_model,
		        r.resolved_alias,
		        count(*),
		        -- Attempt usage REPLACES the request's own counts rather than
		        -- adding to them: the served attempt already carries what the
		        -- request reports, so adding both would double it. A request
		        -- with no attempt rows falls back to its own.
		        --
		        -- The coalesce must happen per row, before sum() runs: sum()
		        -- ignores NULL inputs rather than propagating them, so
		        -- coalesce(sum(au.a_in), sum(r.tokens_in), 0) would let one
		        -- attempt-bearing request in a group make sum(au.a_in)
		        -- non-NULL, discarding every attempt-less request's own
		        -- tokens_in in that same group instead of adding it.
		        coalesce(sum(coalesce(au.a_in,  r.tokens_in)),  0),
		        coalesce(sum(coalesce(au.a_out, r.tokens_out)), 0),
		        -- NULL rather than 0 when nothing in the group is priced:
		        -- zero would report the day's spend as genuinely nothing.
		        CASE WHEN count(coalesce(au.a_cost, r.cost_micros)) = 0 THEN NULL
		             ELSE sum(coalesce(au.a_cost, r.cost_micros)) END
		   FROM requests r
		   LEFT JOIN attempt_usage au ON au.request_id = r.id
		  WHERE r.ts >= ? AND r.ts < ?
		    AND r.final_provider_id <> ''
		  GROUP BY day, r.final_provider_id, r.final_model, r.resolved_alias
		 ON CONFLICT(day, provider_id, model, alias) DO UPDATE SET
		        requests    = excluded.requests,
		        tokens_in   = excluded.tokens_in,
		        tokens_out  = excluded.tokens_out,
		        cost_micros = excluded.cost_micros`,
		from.UnixMilli(), to.UnixMilli())
	if err != nil {
		return fmt.Errorf("rollup: %w", err)
	}
	return nil
}

// RunRollup runs the rollup on an interval until ctx is cancelled.
//
// The interval is jittered so that a restart of several services does not line
// every worker up on the same instant.
func RunRollup(ctx context.Context, d *DB, interval time.Duration) error {
	if interval <= 0 {
		interval = time.Hour
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(jitter(interval)):
			if err := d.Rollup(ctx, time.Now()); err != nil {
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
