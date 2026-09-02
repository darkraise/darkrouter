package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

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
	// Source separates console traffic from a client's: "proxy" or "console".
	Source    string
	Alias     string
	Surface   string
	ErrorCode string
	SinceMs   int64
	UntilMs   int64
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
	Source          string
	TokensIn        int64
	TokensOut       int64
	CacheReadTokens int64
	// Output tokens the model spent reasoning rather than answering. Billed
	// as output tokens and frequently most of them, so a consumer that reads
	// only TokensOut cannot say where the spend went.
	ReasoningTokens int64
	CostMicros      *int64
	TTFTMs          *int64
	TotalMs         *int64
	ErrorCode       string
	Attempts        int
	Path            string
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
		{"r.source", q.Source},
		{"r.resolved_alias", q.Alias},
		{"r.surface", q.Surface},
		{"r.error_code", q.ErrorCode},
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
		        r.final_provider_id, r.final_model, r.status, r.source,
		        r.tokens_in, r.tokens_out, r.cache_read_tokens, r.reasoning_tokens,
		        r.cost_micros, r.ttft_ms, r.total_ms, r.error_code,
		        (SELECT count(*) FROM request_attempts a WHERE a.request_id = r.id),
		        coalesce((SELECT a.path FROM request_attempts a
		                   WHERE a.request_id = r.id
		                   ORDER BY a.seq DESC LIMIT 1), '')
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
			&s.ResolvedAlias, &s.FinalProviderID, &s.FinalModel, &s.Status, &s.Source,
			&s.TokensIn, &s.TokensOut, &s.CacheReadTokens, &s.ReasoningTokens,
			&s.CostMicros, &s.TTFTMs, &s.TotalMs,
			&s.ErrorCode, &s.Attempts, &s.Path); err != nil {
			return nil, fmt.Errorf("list requests: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list requests: %w", err)
	}
	return out, nil
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
		        tokens_in, tokens_out, cache_read_tokens, reasoning_tokens,
		        cost_micros, ttft_ms, total_ms, error_code,
		        candidates_json, warnings_json, surface_meta_json,
		        response_bytes, response_content_type
		   FROM requests WHERE id = ?`, id).Scan(
		&tr.ID, &tr.TSMs, &tr.Dialect, &tr.Surface, &tr.RequestedModel, &tr.ResolvedAlias,
		&tr.FinalProviderID, &tr.FinalModel, &tr.Status,
		&tr.TokensIn, &tr.TokensOut, &tr.CacheReadTokens, &tr.ReasoningTokens,
		&tr.CostMicros, &tr.TTFTMs, &tr.TotalMs, &tr.ErrorCode,
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

	// The label is joined at read time so a renamed credential shows its
	// current name; a deleted one leaves the label empty and the id stands.
	rows, err := d.Read.QueryContext(ctx,
		`SELECT a.seq, a.provider_id, a.key_id, coalesce(k.label, ''), a.model,
		        a.outcome, a.status_code, a.latency_ms, a.error, a.path,
		        a.tokens_in, a.tokens_out, a.cost_micros
		   FROM request_attempts a
		   LEFT JOIN provider_keys k ON k.id = a.key_id
		  WHERE a.request_id = ? ORDER BY a.seq`, id)
	if err != nil {
		return nil, false, fmt.Errorf("read attempts %q: %w", id, err)
	}
	defer rows.Close()
	tr.Attempts = []AttemptRecord{}
	for rows.Next() {
		var a AttemptRecord
		if err := rows.Scan(&a.Seq, &a.ProviderID, &a.KeyID, &a.KeyLabel, &a.Model,
			&a.Outcome, &a.StatusCode, &a.LatencyMs, &a.Error, &a.Path,
			&a.TokensIn, &a.TokensOut, &a.CostMicros); err != nil {
			return nil, false, fmt.Errorf("read attempts %q: %w", id, err)
		}
		tr.Attempts = append(tr.Attempts, a)
	}
	if err := rows.Err(); err != nil {
		return nil, false, fmt.Errorf("read attempts %q: %w", id, err)
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
