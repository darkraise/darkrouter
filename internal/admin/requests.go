package admin

import (
	"net/http"
	"strconv"

	"github.com/darkraise/darkrouter/internal/store"
)

func filtersFrom(r *http.Request) RequestFilters {
	q := r.URL.Query()
	f := RequestFilters{
		Provider: q.Get("provider"),
		Model:    q.Get("model"),
		Status:   q.Get("status"),
		Alias:    q.Get("alias"),
		Surface:  q.Get("surface"),
	}
	f.SinceMs, _ = strconv.ParseInt(q.Get("since_ms"), 10, 64)
	f.UntilMs, _ = strconv.ParseInt(q.Get("until_ms"), 10, 64)
	return f
}

type requestView struct {
	ID         string `json:"id"`
	TSMs       int64  `json:"ts_ms"`
	Dialect    string `json:"dialect"`
	Surface    string `json:"surface"`
	Model      string `json:"model"`
	Alias      string `json:"alias,omitempty"`
	Provider   string `json:"provider,omitempty"`
	FinalModel string `json:"final_model,omitempty"`
	Status     string `json:"status"`
	TokensIn   int64  `json:"tokens_in"`
	TokensOut  int64  `json:"tokens_out"`
	CostMicros *int64 `json:"cost_micros"`
	TTFTMs     *int64 `json:"ttft_ms"`
	TotalMs    *int64 `json:"total_ms"`
	ErrorCode  string `json:"error_code,omitempty"`
	Attempts   int    `json:"attempts"`
}

func (s *Server) handleListRequests(w http.ResponseWriter, r *http.Request) {
	f := filtersFrom(r)
	q := store.RequestQuery{
		Provider: f.Provider, Model: f.Model, Status: f.Status,
		Alias: f.Alias, Surface: f.Surface,
		SinceMs: f.SinceMs, UntilMs: f.UntilMs,
	}
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil {
		q.Limit = n
	}
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
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	out := make([]requestView, 0, len(rows))
	for _, row := range rows {
		out = append(out, requestView{
			ID: row.ID, TSMs: row.TSMs, Dialect: row.Dialect, Surface: row.Surface,
			Model: row.RequestedModel, Alias: row.ResolvedAlias,
			Provider: row.FinalProviderID, FinalModel: row.FinalModel,
			Status: row.Status, TokensIn: row.TokensIn, TokensOut: row.TokensOut,
			CostMicros: row.CostMicros, TTFTMs: row.TTFTMs, TotalMs: row.TotalMs,
			ErrorCode: row.ErrorCode, Attempts: row.Attempts,
		})
	}

	body := map[string]any{"requests": out}
	// The next cursor is minted from the last row of this page under this
	// page's filters, which is what makes it invalid if the filters change.
	if len(rows) > 0 {
		last := rows[len(rows)-1]
		body["next_cursor"] = encodeCursor(last.TSMs, last.ID, f)
	}
	writeJSON(w, http.StatusOK, body)
}

func (s *Server) handleRequestTrace(w http.ResponseWriter, r *http.Request) {
	tr, ok, err := s.deps.DB.RequestTrace(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
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
		attempts = append(attempts, map[string]any{
			"seq": a.Seq, "provider": a.ProviderID, "key_label": a.KeyID,
			"model": a.Model, "outcome": a.Outcome, "status_code": a.StatusCode,
			"latency_ms": a.LatencyMs, "error": a.Error,
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
		"tokens_in": tr.TokensIn, "tokens_out": tr.TokensOut,
		"cost_micros": tr.CostMicros, "ttft_ms": tr.TTFTMs, "total_ms": tr.TotalMs,
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
