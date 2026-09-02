package store

import (
	"context"
	"fmt"
	"time"
)

// UsageDay is one row of the usage chart.
type UsageDay struct {
	Day        string `json:"day"`
	Requests   int64  `json:"requests"`
	Attempts   int64  `json:"attempts"`
	TokensIn   int64  `json:"tokens_in"`
	TokensOut  int64  `json:"tokens_out"`
	CostMicros *int64 `json:"cost_micros"`
}

// UsageDimension is the column usage rolls up by. The zero value aggregates
// across everything, which is what the rollup reported before there was a
// choice.
type UsageDimension int

const (
	UsageByDayOnly UsageDimension = iota
	UsageByProvider
	UsageByModel
	UsageByAlias
)

// column is the SQL identifier for a dimension. It returns "" for the
// day-only case, and the caller must not interpolate anything else -- these
// are fixed identifiers, never user input.
func (d UsageDimension) column() string {
	switch d {
	case UsageByProvider:
		return "provider_id"
	case UsageByModel:
		return "model"
	case UsageByAlias:
		return "alias"
	default:
		return ""
	}
}

// UsageRow is a UsageDay plus the value of the dimension it was grouped by.
// Key is empty for UsageByDayOnly.
type UsageRow struct {
	UsageDay
	Key string `json:"key,omitempty"`
}

// UsageBy rolls usage_daily up over the last `days` days, split by one
// dimension, oldest first because a chart reads left to right.
func (d *DB) UsageBy(ctx context.Context, days int, dim UsageDimension) ([]UsageRow, error) {
	if days <= 0 || days > 365 {
		days = 30
	}
	col := dim.column()
	sel, group := "'' AS k", "day"
	if col != "" {
		sel, group = col+" AS k", "day, "+col
	}
	// The LIMIT is on DAYS, not on rows. Grouping by a dimension multiplies
	// the row count by that dimension's cardinality, so a row limit would
	// silently return thirty rows covering four days once eight providers
	// are in play.
	q := `SELECT day, ` + sel + `,
	             sum(requests), sum(attempts), sum(tokens_in), sum(tokens_out),
	             CASE WHEN count(cost_micros) = 0 THEN NULL ELSE sum(cost_micros) END
	        FROM usage_daily
	       WHERE day IN (SELECT day FROM usage_daily
	                      GROUP BY day ORDER BY day DESC LIMIT ?)
	       GROUP BY ` + group + `
	       ORDER BY day, k`
	rows, err := d.Read.QueryContext(ctx, q, days)
	if err != nil {
		return nil, fmt.Errorf("usage by: %w", err)
	}
	defer rows.Close()
	out := []UsageRow{}
	for rows.Next() {
		var r UsageRow
		if err := rows.Scan(&r.Day, &r.Key, &r.Requests, &r.Attempts,
			&r.TokensIn, &r.TokensOut, &r.CostMicros); err != nil {
			return nil, fmt.Errorf("usage by: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RecentStats is the overview's headline numbers over a window.
type RecentStats struct {
	Requests  int64
	Errors    int64
	WindowSec int64
}

func (d *DB) RecentStats(ctx context.Context, window time.Duration) (RecentStats, error) {
	since := time.Now().Add(-window).UnixMilli()
	var s RecentStats
	s.WindowSec = int64(window.Seconds())
	// coalesce on the sums: SUM over no rows is NULL, and scanning that into an
	// int64 fails rather than yielding zero.
	err := d.Read.QueryRowContext(ctx,
		`SELECT count(*),
		        coalesce(sum(CASE WHEN status != 'success' THEN 1 ELSE 0 END), 0)
		   FROM requests WHERE ts >= ?`, since).
		Scan(&s.Requests, &s.Errors)
	if err != nil {
		return s, fmt.Errorf("recent stats: %w", err)
	}
	return s, nil
}

// SpendSince sums cost from since through now.
//
// Cost is sourced the same way RecentStats and the daily rollup source it:
// from each attempt's own cost, falling back to the request's own cost_micros
// for requests with no attempt rows. Diverging from that shape here would
// make this figure disagree with the usage chart about what a day cost.
func (d *DB) SpendSince(ctx context.Context, since time.Time) (*int64, bool, error) {
	sinceMs := since.UnixMilli()
	var pricedRows, cost int64
	err := d.Read.QueryRowContext(ctx,
		`SELECT coalesce(sum(CASE WHEN c IS NOT NULL THEN 1 ELSE 0 END), 0), coalesce(sum(c), 0)
		   FROM (
		     SELECT a.cost_micros AS c
		       FROM requests r
		       JOIN request_attempts a ON a.request_id = r.id
		      WHERE r.ts >= ?
		     UNION ALL
		     SELECT r.cost_micros
		       FROM requests r
		      WHERE r.ts >= ?
		        AND r.final_provider_id <> ''
		        AND NOT EXISTS (
		              SELECT 1 FROM request_attempts a WHERE a.request_id = r.id)
		   )`, sinceMs, sinceMs).
		Scan(&pricedRows, &cost)
	if err != nil {
		return nil, false, fmt.Errorf("spend since: %w", err)
	}
	// A nil pointer here rather than a zero: an unpriced model leaves
	// cost_micros NULL, and a summed zero is ambiguous between "no spend" and
	// "no price data for what ran".
	if pricedRows == 0 {
		return nil, false, nil
	}
	return &cost, true, nil
}

// LatencyPercentiles returns p50 and p95 of total_ms over the window.
//
// Computed in SQL with a window function rather than by loading the rows: a
// busy window is tens of thousands of requests and the overview polls every
// three seconds.
func (d *DB) LatencyPercentiles(ctx context.Context, window time.Duration) (int64, int64, error) {
	since := time.Now().Add(-window).UnixMilli()
	row := d.Read.QueryRowContext(ctx,
		`WITH ranked AS (
		     SELECT total_ms,
		            row_number() OVER (ORDER BY total_ms) AS rn,
		            count(*)     OVER ()                  AS n
		       FROM requests
		      WHERE ts >= ? AND total_ms IS NOT NULL
		 )
		 -- Nearest-rank: the ceil(n*p)-th value. percent_rank() is
		 -- (rank-1)/(n-1), so "pr >= 0.50" over 100 values returns the 51st,
		 -- not the 50th -- one position high on every sample, on a tile an
		 -- operator reads as p50. Integer division truncates, so
		 -- (n*50 + 99)/100 is ceil(n*50/100).
		 SELECT coalesce((SELECT total_ms FROM ranked WHERE rn = (n * 50 + 99) / 100), 0),
		        coalesce((SELECT total_ms FROM ranked WHERE rn = (n * 95 + 99) / 100), 0)`,
		since)
	var p50, p95 int64
	if err := row.Scan(&p50, &p95); err != nil {
		return 0, 0, fmt.Errorf("latency percentiles: %w", err)
	}
	return p50, p95, nil
}

// FailoverRow is one request the router had to walk past a candidate for.
type FailoverRow struct {
	ID              string `json:"id"`
	TS              int64  `json:"ts"`
	Alias           string `json:"alias"`
	FinalProviderID string `json:"final_provider_id"`
	FinalModel      string `json:"final_model"`
	Attempts        int    `json:"attempts"`
	TotalMs         int64  `json:"total_ms"`
}

// RecentFailovers returns the newest requests within window that took more
// than one attempt. Bounded the same way its RecentStats and
// LatencyPercentiles siblings are: the overview polls this every few
// seconds, and without a window it joins and groups the entire request
// history on every call, plus a quiet gateway would show a month-old
// failover as though it just happened.
func (d *DB) RecentFailovers(ctx context.Context, window time.Duration, limit int) ([]FailoverRow, error) {
	if limit <= 0 {
		limit = 5
	}
	since := time.Now().Add(-window).UnixMilli()
	rows, err := d.Read.QueryContext(ctx,
		`SELECT r.id, r.ts, r.resolved_alias, r.final_provider_id, r.final_model,
		        count(a.seq) AS attempts, coalesce(r.total_ms, 0)
		   FROM requests r
		   JOIN request_attempts a ON a.request_id = r.id
		  WHERE r.ts >= ?
		  GROUP BY r.id
		 HAVING attempts > 1
		  ORDER BY r.ts DESC
		  LIMIT ?`, since, limit)
	if err != nil {
		return nil, fmt.Errorf("recent failovers: %w", err)
	}
	defer rows.Close()
	out := []FailoverRow{}
	for rows.Next() {
		var f FailoverRow
		if err := rows.Scan(&f.ID, &f.TS, &f.Alias, &f.FinalProviderID,
			&f.FinalModel, &f.Attempts, &f.TotalMs); err != nil {
			return nil, fmt.Errorf("recent failovers: %w", err)
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// FailoverEdge counts requests that reached `ToProviderID` after
// `FromProviderID` refused them.
type FailoverEdge struct {
	FromProviderID string `json:"from_provider_id"`
	ToProviderID   string `json:"to_provider_id"`
	Requests       int64  `json:"requests"`
}

// FailoverEdges aggregates who handed work to whom over the window.
//
// RecentFailovers answers "which requests failed over" and names only where
// they ended. The overview's routing graph draws a return from the provider
// that refused to the one that served, which needs the pair -- so this walks
// the attempts rather than the request rows.
func (d *DB) FailoverEdges(ctx context.Context, window time.Duration) ([]FailoverEdge, error) {
	since := time.Now().Add(-window).UnixMilli()
	rows, err := d.Read.QueryContext(ctx,
		`SELECT failed.provider_id, served.provider_id, count(*)
		   FROM request_attempts failed
		   JOIN request_attempts served
		     ON served.request_id = failed.request_id
		    AND served.outcome = 'success'
		   JOIN requests r ON r.id = failed.request_id
		  WHERE r.ts >= ?
		    AND failed.outcome <> 'success'
		    AND failed.provider_id <> served.provider_id
		  GROUP BY failed.provider_id, served.provider_id
		  ORDER BY count(*) DESC, failed.provider_id, served.provider_id`, since)
	if err != nil {
		return nil, fmt.Errorf("failover edges: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []FailoverEdge{}
	for rows.Next() {
		var e FailoverEdge
		if err := rows.Scan(&e.FromProviderID, &e.ToProviderID, &e.Requests); err != nil {
			return nil, fmt.Errorf("scan failover edge: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
