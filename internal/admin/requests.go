package admin

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/darkraise/darkrouter/internal/store"
)

// queryInt reads one optional integer query parameter. A value that is
// present and unparseable is an error rather than a silent zero: a client
// that sent since_ms=yesterday would otherwise get the unfiltered log and
// never learn why.
func queryInt(r *http.Request, name string) (int64, bool, error) {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return 0, false, nil
	}
	n, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, false, fmt.Errorf("%s must be an integer", name)
	}
	return n, true, nil
}

func filtersFrom(r *http.Request) (RequestFilters, error) {
	q := r.URL.Query()
	f := RequestFilters{
		Provider:  q.Get("provider"),
		Model:     q.Get("model"),
		Status:    q.Get("status"),
		Alias:     q.Get("alias"),
		Surface:   q.Get("surface"),
		ErrorCode: q.Get("error_code"),
		Source:    q.Get("source"),
	}
	var err error
	if f.SinceMs, _, err = queryInt(r, "since_ms"); err != nil {
		return f, err
	}
	if f.UntilMs, _, err = queryInt(r, "until_ms"); err != nil {
		return f, err
	}
	return f, nil
}

type requestView struct {
	ID              string `json:"id"`
	TSMs            int64  `json:"ts_ms"`
	Dialect         string `json:"dialect"`
	Surface         string `json:"surface"`
	Model           string `json:"model"`
	Alias           string `json:"alias,omitempty"`
	Provider        string `json:"provider,omitempty"`
	FinalModel      string `json:"final_model,omitempty"`
	Status          string `json:"status"`
	Source          string `json:"source"`
	TokensIn        int64  `json:"tokens_in"`
	TokensOut       int64  `json:"tokens_out"`
	CacheReadTokens int64  `json:"cache_read_tokens"`
	ReasoningTokens int64  `json:"reasoning_tokens"`
	CostMicros      *int64 `json:"cost_micros"`
	TTFTMs          *int64 `json:"ttft_ms"`
	TotalMs         *int64 `json:"total_ms"`
	ErrorCode       string `json:"error_code,omitempty"`
	Attempts        int    `json:"attempts"`
	// Which rendering served: "passthrough" when the fast path carried the
	// body through untouched, "ir" when it was translated. Empty when nothing
	// served at all.
	Path string `json:"path,omitempty"`
}

func (s *Server) handleListRequests(w http.ResponseWriter, r *http.Request) {
	f, err := filtersFrom(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	q := store.RequestQuery{
		Provider: f.Provider, Model: f.Model, Status: f.Status,
		Alias: f.Alias, Surface: f.Surface, ErrorCode: f.ErrorCode,
		Source: f.Source, SinceMs: f.SinceMs, UntilMs: f.UntilMs,
	}
	limit, _, err := queryInt(r, "limit")
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	q.Limit = int(limit)
	if c := r.URL.Query().Get("cursor"); c != "" {
		ts, id, err := decodeCursor(c, f)
		if err != nil {
			// 400 rather than a silent first page: the client asked for a
			// specific position and got something else, and it should know.
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		q.AfterTS, q.AfterID = ts, id
	}

	rows, err := s.deps.DB.ListRequests(r.Context(), q)
	if err != nil {
		internalError(w, r, err)
		return
	}

	out := make([]requestView, 0, len(rows))
	for _, row := range rows {
		out = append(out, requestView{
			ID: row.ID, TSMs: row.TSMs, Dialect: row.Dialect, Surface: row.Surface,
			Model: row.RequestedModel, Alias: row.ResolvedAlias,
			Provider: row.FinalProviderID, FinalModel: row.FinalModel,
			Status: row.Status, Source: row.Source,
			TokensIn: row.TokensIn, TokensOut: row.TokensOut,
			CacheReadTokens: row.CacheReadTokens, ReasoningTokens: row.ReasoningTokens,
			CostMicros: row.CostMicros, TTFTMs: row.TTFTMs, TotalMs: row.TotalMs,
			ErrorCode: row.ErrorCode, Attempts: row.Attempts, Path: row.Path,
		})
	}

	body := map[string]any{"requests": out}
	// The next cursor is minted from the last row of this page under this
	// page's filters, which is what makes it invalid if the filters change.
	// A short page is the last one, and carries no cursor: a client that
	// followed one would fetch an empty page to learn what the length
	// already said.
	if len(rows) > 0 && len(rows) == store.PageSize(q.Limit) {
		last := rows[len(rows)-1]
		body["next_cursor"] = encodeCursor(last.TSMs, last.ID, f)
	}
	writeJSON(w, http.StatusOK, body)
}

func (s *Server) handleRequestTrace(w http.ResponseWriter, r *http.Request) {
	tr, ok, err := s.deps.DB.RequestTrace(r.Context(), r.PathValue("id"))
	if err != nil {
		internalError(w, r, err)
		return
	}
	if !ok {
		// 404 rather than an empty trace: an operator following a stale link
		// must learn the row is gone rather than see a blank drawer that looks
		// like a rendering bug.
		writeError(w, http.StatusNotFound, "no such request")
		return
	}

	attempts := make([]map[string]any, 0, len(tr.Attempts))
	for _, a := range tr.Attempts {
		// The label is what the operator named the credential; the id stands
		// in only when the credential is gone and there is nothing else to
		// show.
		label := a.KeyLabel
		if label == "" {
			label = a.KeyID
		}
		attempts = append(attempts, map[string]any{
			"seq": a.Seq, "provider": a.ProviderID, "key_label": label,
			"model": a.Model, "outcome": a.Outcome, "status_code": a.StatusCode,
			"latency_ms": a.LatencyMs, "error": a.Error, "path": a.Path,
			"tokens_in": a.TokensIn, "tokens_out": a.TokensOut, "cost_micros": a.CostMicros,
		})
	}
	bodies := make([]map[string]any, 0, len(tr.Bodies))
	for _, b := range tr.Bodies {
		bodies = append(bodies, map[string]any{"kind": b.Kind, "content": b.Content})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id": tr.ID, "ts_ms": tr.TSMs, "dialect": tr.Dialect, "surface": tr.Surface,
		"model": tr.RequestedModel, "alias": tr.ResolvedAlias,
		"provider": tr.FinalProviderID, "final_model": tr.FinalModel,
		"status": tr.Status, "error_code": tr.ErrorCode,
		"source": tr.Source, "path": tr.Path,
		"tokens_in": tr.TokensIn, "tokens_out": tr.TokensOut, "cache_read_tokens": tr.CacheReadTokens,
		// Output tokens spent reasoning rather than answering. Reported
		// separately because they are billed inside tokens_out and are often
		// most of it, and because they are the one signal that a turn reasoned
		// that does not depend on what the provider's wire called the text --
		// a passthrough reply carries the upstream's own field names, and
		// those disagree across providers.
		"reasoning_tokens": tr.ReasoningTokens,
		"cost_micros":      tr.CostMicros, "ttft_ms": tr.TTFTMs, "total_ms": tr.TotalMs,
		// Three separate lists, deliberately. Attempts alone explains a
		// failover; candidates and skips explain the routing decision.
		"candidates":            tr.Candidates,
		"skips":                 tr.Skips,
		"attempts":              attempts,
		"warnings":              tr.Warnings,
		"surface_meta":          tr.SurfaceMeta,
		"response_bytes":        tr.ResponseBytes,
		"response_content_type": tr.ResponseContentType,
		"bodies":                bodies,
	})
}
