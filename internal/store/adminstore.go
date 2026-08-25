package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// CreateSession writes a new session row. The caller mints the id; this does not
// generate one, because the id is a security-relevant value and the code that
// chooses its entropy should be the code that owns the decision.
func (d *DB) CreateSession(ctx context.Context, id string, ttl time.Duration) error {
	now := time.Now()
	if _, err := d.Write.ExecContext(ctx,
		`INSERT INTO sessions (id, created_at, expires_at) VALUES (?, ?, ?)`,
		id, now.UnixMilli(), now.Add(ttl).UnixMilli()); err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

// TouchSession validates a session and slides its expiry in one statement.
//
// The expiry test lives in the WHERE rather than in a read followed by a
// comparison: two concurrent requests on a session expiring this instant must
// not disagree about whether it is alive, and a read-then-write leaves exactly
// that window open. It also means an expired row can never be extended by the
// call that just decided it was dead.
//
// A miss is reported as false rather than as an error, because a missing session
// and a database failure are different things to the caller: the first renders
// the login screen, the second is a 500.
func (d *DB) TouchSession(ctx context.Context, id string, ttl time.Duration) (bool, error) {
	now := time.Now()
	res, err := d.Write.ExecContext(ctx,
		`UPDATE sessions SET expires_at = ? WHERE id = ? AND expires_at > ?`,
		now.Add(ttl).UnixMilli(), id, now.UnixMilli())
	if err != nil {
		return false, fmt.Errorf("touch session: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("touch session: %w", err)
	}
	return n > 0, nil
}

// DeleteSession removes the row. Spec §3: logout deletes rather than only
// clearing the cookie, because a cleared cookie leaves a valid session id in the
// database for anyone who copied it.
func (d *DB) DeleteSession(ctx context.Context, id string) error {
	if _, err := d.Write.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// SweepSessions prunes expired rows and reports how many went. It runs at
// startup: sessions outlive the process, so nothing else would ever remove them.
func (d *DB) SweepSessions(ctx context.Context) (int, error) {
	res, err := d.Write.ExecContext(ctx,
		`DELETE FROM sessions WHERE expires_at <= ?`, time.Now().UnixMilli())
	if err != nil {
		return 0, fmt.Errorf("sweep sessions: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("sweep sessions: %w", err)
	}
	return int(n), nil
}

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
		    region, project, location, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Name, p.Preset, p.Kind, p.BaseURL, p.AuthStyle,
		p.Priority, enabled, p.Region, p.Project, p.Location, time.Now().UnixMilli()); err != nil {
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
		        region, project, location
		   FROM providers ORDER BY priority DESC, id`)
	if err != nil {
		return nil, fmt.Errorf("list providers: %w", err)
	}
	defer rows.Close()

	out := make([]ProviderRow, 0)
	for rows.Next() {
		var p ProviderRow
		var enabled int
		if err := rows.Scan(&p.ID, &p.Name, &p.Preset, &p.Kind, &p.BaseURL,
			&p.AuthStyle, &p.Priority, &enabled, &p.Region, &p.Project, &p.Location); err != nil {
			return nil, fmt.Errorf("list providers: %w", err)
		}
		p.Enabled = enabled != 0
		out = append(out, p)
	}
	return out, rows.Err()
}

// maxRequestPage caps a page server-side. A client asking for a million rows is
// asking the gateway to build a million-row JSON array in memory. A larger
// request is clamped rather than refused, because refusing makes a UI bug look
// like a server outage.
const maxRequestPage = 200

// RequestQuery is one page request. AfterTS and AfterID together are the keyset
// position; an empty AfterID means the first page.
type RequestQuery struct {
	Limit   int
	AfterTS int64
	AfterID string

	Provider string
	Model    string
	Status   string
	Alias    string
	Surface  string
	SinceMs  int64
	UntilMs  int64
}

// RequestSummary is one row of the table. It carries TSMs and ID because the
// next cursor is built from the last row of the page and there is nowhere else
// to get them.
type RequestSummary struct {
	ID              string
	TSMs            int64
	Dialect         string
	Surface         string
	RequestedModel  string
	ResolvedAlias   string
	FinalProviderID string
	FinalModel      string
	Status          string
	TokensIn        int64
	TokensOut       int64
	CostMicros      *int64
	TTFTMs          *int64
	TotalMs         *int64
	ErrorCode       string
	Attempts        int
}

// ListRequests returns one keyset page, newest first.
//
// The predicate is the lexicographic tuple (ts, id) < (cursor_ts, cursor_id),
// written expanded rather than as a row value because SQLite uses the composite
// index more reliably that way. The tie-break on id is what makes the order
// total: request ids are ULIDs, lexicographically ordered by time, so two rows
// in the same millisecond still have a defined position and a page boundary
// there neither repeats nor skips.
func (d *DB) ListRequests(ctx context.Context, q RequestQuery) ([]RequestSummary, error) {
	limit := q.Limit
	if limit <= 0 || limit > maxRequestPage {
		limit = maxRequestPage
	}

	where := []string{"1 = 1"}
	args := []any{}
	if q.AfterID != "" {
		where = append(where, "(r.ts < ? OR (r.ts = ? AND r.id < ?))")
		args = append(args, q.AfterTS, q.AfterTS, q.AfterID)
	}
	// A fixed sequence rather than a map range: the generated SQL has to be
	// deterministic or SQLite's statement cache misses on every call, and the
	// order matches RequestFilters.Hash so the two stay legible together.
	for _, f := range []struct {
		col string
		val string
	}{
		{"r.final_provider_id", q.Provider},
		{"r.final_model", q.Model},
		{"r.status", q.Status},
		{"r.resolved_alias", q.Alias},
		{"r.surface", q.Surface},
	} {
		if f.val != "" {
			where = append(where, f.col+" = ?")
			args = append(args, f.val)
		}
	}
	if q.SinceMs > 0 {
		where = append(where, "r.ts >= ?")
		args = append(args, q.SinceMs)
	}
	if q.UntilMs > 0 {
		where = append(where, "r.ts <= ?")
		args = append(args, q.UntilMs)
	}
	args = append(args, limit)

	rows, err := d.Read.QueryContext(ctx,
		`SELECT r.id, r.ts, r.dialect, r.surface, r.requested_model, r.resolved_alias,
		        r.final_provider_id, r.final_model, r.status,
		        r.tokens_in, r.tokens_out, r.cost_micros, r.ttft_ms, r.total_ms, r.error_code,
		        (SELECT count(*) FROM request_attempts a WHERE a.request_id = r.id)
		   FROM requests r
		  WHERE `+strings.Join(where, " AND ")+`
		  ORDER BY r.ts DESC, r.id DESC
		  LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("list requests: %w", err)
	}
	defer rows.Close()

	out := make([]RequestSummary, 0, limit)
	for rows.Next() {
		var s RequestSummary
		if err := rows.Scan(&s.ID, &s.TSMs, &s.Dialect, &s.Surface, &s.RequestedModel,
			&s.ResolvedAlias, &s.FinalProviderID, &s.FinalModel, &s.Status,
			&s.TokensIn, &s.TokensOut, &s.CostMicros, &s.TTFTMs, &s.TotalMs,
			&s.ErrorCode, &s.Attempts); err != nil {
			return nil, fmt.Errorf("list requests: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// RequestTrace is one request in full: what the router produced, what it
// rejected, and what actually ran.
//
// Candidates, Skips and Attempts are three different facts and the drawer shows
// all three. Attempts alone explains a failover; the other two explain the
// routing decision, which is the harder question and the reason spec §6 calls
// this screen the one worth building.
type RequestTrace struct {
	RequestSummary

	Candidates  []string
	Skips       []string
	Warnings    []string
	SurfaceMeta map[string]any

	ResponseBytes       int64
	ResponseContentType string

	Attempts []AttemptRecord
	// Bodies is always non-nil. capture.bodies has a retention sweep and no
	// writer, so this is empty today; the query exists so the drawer works the
	// day a writer lands, and the empty case renders as "not captured".
	Bodies []RequestBody
}

// RequestBody is one captured body. The table stores request and response in
// two columns of one row; they are presented as a list because the drawer
// ranges over them and a two-field struct would need the same branch twice.
type RequestBody struct {
	Kind    string
	Content string
}

// RequestTrace reads one request with everything attached.
//
// A miss is reported as false rather than as an error: an operator following a
// stale link must learn the row is gone, and a 500 would say the server broke.
func (d *DB) RequestTrace(ctx context.Context, id string) (*RequestTrace, bool, error) {
	var tr RequestTrace
	var traceJSON, warningsJSON, metaJSON string

	err := d.Read.QueryRowContext(ctx,
		`SELECT id, ts, dialect, surface, requested_model, resolved_alias,
		        final_provider_id, final_model, status,
		        tokens_in, tokens_out, cost_micros, ttft_ms, total_ms, error_code,
		        candidates_json, warnings_json, surface_meta_json,
		        response_bytes, response_content_type
		   FROM requests WHERE id = ?`, id).Scan(
		&tr.ID, &tr.TSMs, &tr.Dialect, &tr.Surface, &tr.RequestedModel, &tr.ResolvedAlias,
		&tr.FinalProviderID, &tr.FinalModel, &tr.Status,
		&tr.TokensIn, &tr.TokensOut, &tr.CostMicros, &tr.TTFTMs, &tr.TotalMs, &tr.ErrorCode,
		&traceJSON, &warningsJSON, &metaJSON,
		&tr.ResponseBytes, &tr.ResponseContentType)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read trace %q: %w", id, err)
	}

	// Candidates and skips travel in one column, as the log writer packs them.
	var trace struct {
		Candidates []string `json:"candidates"`
		Skips      []string `json:"skips"`
	}
	if err := json.Unmarshal([]byte(traceJSON), &trace); err != nil {
		return nil, false, fmt.Errorf("read trace %q: %w", id, err)
	}
	tr.Candidates, tr.Skips = trace.Candidates, trace.Skips
	if tr.Candidates == nil {
		tr.Candidates = []string{}
	}
	if tr.Skips == nil {
		tr.Skips = []string{}
	}
	if err := json.Unmarshal([]byte(warningsJSON), &tr.Warnings); err != nil {
		return nil, false, fmt.Errorf("read trace %q: %w", id, err)
	}
	if tr.Warnings == nil {
		tr.Warnings = []string{}
	}
	if err := json.Unmarshal([]byte(metaJSON), &tr.SurfaceMeta); err != nil {
		return nil, false, fmt.Errorf("read trace %q: %w", id, err)
	}

	rows, err := d.Read.QueryContext(ctx,
		`SELECT seq, provider_id, key_id, model, outcome, status_code, latency_ms, error, path
		   FROM request_attempts WHERE request_id = ? ORDER BY seq`, id)
	if err != nil {
		return nil, false, fmt.Errorf("read attempts %q: %w", id, err)
	}
	defer rows.Close()
	tr.Attempts = []AttemptRecord{}
	for rows.Next() {
		var a AttemptRecord
		if err := rows.Scan(&a.Seq, &a.ProviderID, &a.KeyID, &a.Model,
			&a.Outcome, &a.StatusCode, &a.LatencyMs, &a.Error, &a.Path); err != nil {
			return nil, false, fmt.Errorf("read attempts %q: %w", id, err)
		}
		tr.Attempts = append(tr.Attempts, a)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}

	// Non-nil so the drawer can range over it. Empty today: capture.bodies has
	// no writer, which phase 5 recorded and phase 7 does not change.
	tr.Bodies = []RequestBody{}
	var reqBody, respBody string
	berr := d.Read.QueryRowContext(ctx,
		`SELECT request_json, response_json FROM request_bodies WHERE request_id = ?`,
		id).Scan(&reqBody, &respBody)
	if berr == nil {
		if reqBody != "" {
			tr.Bodies = append(tr.Bodies, RequestBody{Kind: "request", Content: reqBody})
		}
		if respBody != "" {
			tr.Bodies = append(tr.Bodies, RequestBody{Kind: "response", Content: respBody})
		}
	} else if !errors.Is(berr, sql.ErrNoRows) {
		return nil, false, fmt.Errorf("read bodies %q: %w", id, berr)
	}
	return &tr, true, nil
}

// UsageDay is one row of the usage chart.
type UsageDay struct {
	Day        string
	Requests   int64
	TokensIn   int64
	TokensOut  int64
	CostMicros *int64
}

// UsageDimension is the column usage rolls up by. The zero value aggregates
// across everything, which is what UsageByDay reported before there was a
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
	Key string
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
	             sum(requests), sum(tokens_in), sum(tokens_out),
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
		if err := rows.Scan(&r.Day, &r.Key, &r.Requests,
			&r.TokensIn, &r.TokensOut, &r.CostMicros); err != nil {
			return nil, fmt.Errorf("usage by: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// UsageByDay rolls usage_daily up across every dimension, oldest first. Its
// signature and its ordering are unchanged; only its implementation moved.
func (d *DB) UsageByDay(ctx context.Context, days int) ([]UsageDay, error) {
	rows, err := d.UsageBy(ctx, days, UsageByDayOnly)
	if err != nil {
		return nil, err
	}
	out := make([]UsageDay, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.UsageDay)
	}
	return out, nil
}

// RecentStats is the overview's headline numbers over a window.
type RecentStats struct {
	Requests  int64
	Errors    int64
	WindowSec int64
	// PricedRows counts rows carrying a cost. Zero means nothing computes
	// pricing, which the overview reports rather than showing a confident zero.
	PricedRows int64
	CostMicros int64
}

func (d *DB) RecentStats(ctx context.Context, window time.Duration) (RecentStats, error) {
	since := time.Now().Add(-window).UnixMilli()
	var s RecentStats
	s.WindowSec = int64(window.Seconds())
	// coalesce on the sums: SUM over no rows is NULL, and scanning that into an
	// int64 fails rather than yielding zero.
	err := d.Read.QueryRowContext(ctx,
		`SELECT count(*),
		        coalesce(sum(CASE WHEN status != 'success' THEN 1 ELSE 0 END), 0),
		        coalesce(sum(CASE WHEN cost_micros IS NOT NULL THEN 1 ELSE 0 END), 0),
		        coalesce(sum(cost_micros), 0)
		   FROM requests WHERE ts >= ?`, since).
		Scan(&s.Requests, &s.Errors, &s.PricedRows, &s.CostMicros)
	if err != nil {
		return s, fmt.Errorf("recent stats: %w", err)
	}
	return s, nil
}
