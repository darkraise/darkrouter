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
