package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"log"
	"sync/atomic"
	"time"
)

// AttemptRecord is one upstream attempt. Phase 2 produces exactly one per
// request; phase 3's loop produces several.
type AttemptRecord struct {
	Seq        int
	ProviderID string
	KeyID      string
	Model      string
	Outcome    string
	StatusCode int
	LatencyMs  int64
	Error      string
	// Path is "passthrough" or "ir". Empty means "ir": a caller that predates
	// the fast path is describing the only rendering there was.
	Path string

	// Usage burned by this attempt, including one that failed before commit.
	// Without these, a failover's discarded tokens never reach usage_daily and
	// spend understates reality exactly when failover fires.
	TokensIn   int64
	TokensOut  int64
	CostMicros *int64
}

// RequestRecord is a complete request, built in memory by the handler and
// handed off. Nothing here may reference the http.Request: the record outlives
// the request it describes.
type RequestRecord struct {
	ID              string
	TS              time.Time
	Dialect         string
	Surface         string
	RequestedModel  string
	ResolvedAlias   string
	Candidates      []string
	Skips           []string
	FinalProviderID string
	FinalModel      string
	Status          string

	TokensIn         int64
	TokensOut        int64
	CacheReadTokens  int64
	CacheWriteTokens int64
	ReasoningTokens  int64

	// CostMicros is nil until pricing for the model exists, which is phase 6.
	// Zero would read as "this request was free".
	CostMicros *int64
	TTFTMs     *int64
	TotalMs    *int64

	ErrorCode string
	Warnings  []string

	// SurfaceMeta is the surface-specific detail spec §9 asks for: input count
	// and dimensions for embeddings, image count and size, audio duration and
	// voice, document count for rerank. One JSON column rather than nine,
	// because no two surfaces share a field.
	SurfaceMeta map[string]any

	// ResponseBytes and ResponseContentType apply to every surface. Spec §7:
	// a truncated binary body cannot be signalled in-band, so the byte count on
	// this row is the only place the truncation appears.
	ResponseBytes       int64
	ResponseContentType string

	Attempts []AttemptRecord
}

type LogOptions struct {
	Buffer     int
	BatchSize  int
	FlushEvery time.Duration
}

const (
	defaultLogBuffer     = 4096
	defaultLogBatchSize  = 128
	defaultLogFlushEvery = 250 * time.Millisecond
)

// LogWriter batches records into one transaction on a short timer or when the
// batch fills.
type LogWriter struct {
	db  *DB
	ch  chan *RequestRecord
	opt LogOptions

	dropped atomic.Int64
	written atomic.Int64
}

func NewLogWriter(db *DB, o LogOptions) *LogWriter {
	if o.Buffer <= 0 {
		o.Buffer = defaultLogBuffer
	}
	if o.BatchSize <= 0 {
		o.BatchSize = defaultLogBatchSize
	}
	if o.FlushEvery <= 0 {
		o.FlushEvery = defaultLogFlushEvery
	}
	return &LogWriter{db: db, ch: make(chan *RequestRecord, o.Buffer), opt: o}
}

// Log hands a record to the writer. It never blocks: a full channel drops the
// record and increments the counter reported on /healthz and /metrics.
//
// The trade is deliberate. Blocking the request path to guarantee a log line
// would make the gateway slower under exactly the load that produces the most
// interesting logs.
func (w *LogWriter) Log(r *RequestRecord) {
	if r == nil {
		return
	}
	select {
	case w.ch <- r:
	default:
		w.dropped.Add(1)
	}
}

// Dropped reports how many records were discarded. It counts records, not
// tokens or dollars: with a non-zero value, usage_daily is a lower bound.
func (w *LogWriter) Dropped() int64 { return w.dropped.Load() }

// Written reports how many records reached the database.
func (w *LogWriter) Written() int64 { return w.written.Load() }

// Run batches and writes until ctx is cancelled, then drains what is buffered
// and returns. The caller must not send on the channel after cancelling.
func (w *LogWriter) Run(ctx context.Context) error {
	ticker := time.NewTicker(w.opt.FlushEvery)
	defer ticker.Stop()

	batch := make([]*RequestRecord, 0, w.opt.BatchSize)
	for {
		select {
		case <-ctx.Done():
			w.drain(&batch)
			return nil
		case r := <-w.ch:
			batch = append(batch, r)
			if len(batch) >= w.opt.BatchSize {
				w.flush(&batch)
			}
		case <-ticker.C:
			w.flush(&batch)
		}
	}
}

// drain empties the channel after cancellation. Shutdown ordering guarantees
// in-flight requests have finished by the time this runs, so the channel has a
// bounded amount left in it and the loop terminates.
func (w *LogWriter) drain(batch *[]*RequestRecord) {
	for {
		select {
		case r := <-w.ch:
			*batch = append(*batch, r)
			if len(*batch) >= w.opt.BatchSize {
				w.flush(batch)
			}
		default:
			w.flush(batch)
			return
		}
	}
}

func (w *LogWriter) flush(batch *[]*RequestRecord) {
	if len(*batch) == 0 {
		return
	}
	// A failed flush is logged and dropped rather than retried. Retrying would
	// grow the batch without bound while the cause persists, and the drop
	// counter is what tells the operator the log is incomplete.
	written, err := w.writeBatch(context.Background(), *batch)
	if err != nil {
		w.dropped.Add(int64(len(*batch)))
		log.Printf("request log: dropped %d records: %v", len(*batch), err)
	} else {
		w.written.Add(int64(written))
		if skipped := len(*batch) - written; skipped > 0 {
			w.dropped.Add(int64(skipped))
		}
	}
	*batch = (*batch)[:0]
}

// writeBatch uses a background context rather than the cancelled shutdown one:
// the drain runs after cancellation, and a cancelled context would abort the
// very write the drain exists to perform. It returns how many records landed.
func (w *LogWriter) writeBatch(ctx context.Context, batch []*RequestRecord) (int, error) {
	tx, err := w.db.Write.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	reqStmt, err := tx.PrepareContext(ctx,
		`INSERT INTO requests (
		    id, ts, dialect, surface, requested_model, resolved_alias, candidates_json,
		    final_provider_id, final_model, status,
		    tokens_in, tokens_out, cache_read_tokens, cache_write_tokens, reasoning_tokens,
		    cost_micros, ttft_ms, total_ms, error_code, warnings_json,
		    surface_meta_json, response_bytes, response_content_type
		 ) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return 0, err
	}
	defer reqStmt.Close()

	attStmt, err := tx.PrepareContext(ctx,
		`INSERT INTO request_attempts
		    (request_id, seq, provider_id, key_id, model, outcome, status_code, latency_ms, error, path, tokens_in, tokens_out, cost_micros)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return 0, err
	}
	defer attStmt.Close()

	written := 0
	for _, r := range batch {
		if err := insertOne(ctx, reqStmt, attStmt, r); err != nil {
			// One malformed record must not cost the batch. Duplicate ids are
			// the realistic case, and they mean a bug elsewhere, not here.
			log.Printf("request log: skipped record %s: %v", r.ID, err)
			continue
		}
		written++
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return written, nil
}

func insertOne(ctx context.Context, reqStmt, attStmt *sql.Stmt, r *RequestRecord) error {
	// Candidates and skips travel together in one column: they are only ever
	// read together, and keeping them there leaves the phase 2 schema alone.
	trace, err := json.Marshal(struct {
		Candidates []string `json:"candidates"`
		Skips      []string `json:"skips"`
	}{nonNil(r.Candidates), nonNil(r.Skips)})
	if err != nil {
		return err
	}
	warnings, err := json.Marshal(nonNil(r.Warnings))
	if err != nil {
		return err
	}
	// The column is NOT NULL and defaults to an empty object, so a nil map must
	// encode as {} rather than null — otherwise every chat row fails to insert.
	meta := r.SurfaceMeta
	if meta == nil {
		meta = map[string]any{}
	}
	surfaceMeta, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	if _, err := reqStmt.ExecContext(ctx,
		r.ID, r.TS.UnixMilli(), r.Dialect, r.Surface, r.RequestedModel, r.ResolvedAlias,
		string(trace), r.FinalProviderID, r.FinalModel, r.Status,
		r.TokensIn, r.TokensOut, r.CacheReadTokens, r.CacheWriteTokens, r.ReasoningTokens,
		r.CostMicros, r.TTFTMs, r.TotalMs, r.ErrorCode, string(warnings),
		string(surfaceMeta), r.ResponseBytes, r.ResponseContentType,
	); err != nil {
		return err
	}
	for _, a := range r.Attempts {
		path := a.Path
		if path == "" {
			path = "ir"
		}
		if _, err := attStmt.ExecContext(ctx,
			r.ID, a.Seq, a.ProviderID, a.KeyID, a.Model, a.Outcome,
			a.StatusCode, a.LatencyMs, a.Error, path, a.TokensIn, a.TokensOut, a.CostMicros,
		); err != nil {
			return err
		}
	}
	return nil
}

// nonNil keeps a nil slice out of the JSON columns, which are NOT NULL and
// default to an empty array rather than "null".
func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
