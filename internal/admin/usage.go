package admin

import (
	"net/http"
	"time"

	"github.com/darkraise/darkrouter/internal/auth"
	"github.com/darkraise/darkrouter/internal/store"
)

// overviewWindow is what "requests per minute" and the error rate are measured
// over. Five minutes rather than one: a homelab gateway can go a minute without
// traffic, and a rate computed over an empty minute reads as an outage.
const overviewWindow = 5 * time.Minute

// startOfUTCDay is the day boundary the daily rollup uses (strftime's
// '%Y-%m-%d' on a unixepoch is a UTC calendar day). A tile computed on a
// different day boundary than the usage chart would disagree with it about
// when "today" started.
func startOfUTCDay(t time.Time) time.Time {
	t = t.UTC()
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

type tileView struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	State       string `json:"state"`
	Cooling     int    `json:"cooling"`
	Credentials int    `json:"credentials"`
	Enabled     bool   `json:"enabled"`
	NeedsAuth   bool   `json:"needs_reauth"`
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	rows, err := s.deps.DB.ProviderRows(r.Context())
	if err != nil {
		internalError(w, r, err)
		return
	}
	// Counts and states only, so the overview's poll never touches the
	// keyring.
	summaries, err := s.deps.DB.CredentialSummaries(r.Context())
	if err != nil {
		internalError(w, r, err)
		return
	}

	// Breaker entries are per triple. Folded per provider here because spec §6
	// asks for provider-level signals: forty models cooling on one dead
	// credential is one dead provider, and forty red dots would say otherwise.
	cooling := map[string]int{}
	if s.deps.Breaker != nil {
		now := time.Now()
		for _, e := range s.deps.Breaker.Snapshot() {
			if e.CoolingUntil.After(now) {
				cooling[e.Key.ProviderID]++
			}
		}
	}

	tiles := make([]tileView, 0, len(rows))
	for _, p := range rows {
		t := tileView{ID: p.ID, Name: p.Name, Enabled: p.Enabled, Cooling: cooling[p.ID]}
		creds := summaries[p.ID]
		t.Credentials = len(creds)
		for _, c := range creds {
			// The one state only the operator can fix. Everything else
			// either recovers on its own or is a provider's problem, so
			// it is called out rather than folded into "degraded".
			if !c.Enabled {
				t.NeedsAuth = true
			}
		}
		switch {
		case !p.Enabled:
			t.State = "disabled"
		// A keyless provider with no credentials is configured: there is
		// nothing left for an operator to add, and calling it unconfigured
		// sends them looking for a key that does not exist.
		case t.Credentials == 0 && auth.IsKeyless(p.AuthStyle):
			t.State = "healthy"
		case t.Credentials == 0:
			t.State = "unconfigured"
		case t.Cooling > 0:
			t.State = "degraded"
		default:
			t.State = "healthy"
		}
		tiles = append(tiles, t)
	}

	stats, err := s.deps.DB.RecentStats(r.Context(), overviewWindow)
	if err != nil {
		internalError(w, r, err)
		return
	}
	var errRate float64
	if stats.Requests > 0 {
		errRate = float64(stats.Errors) / float64(stats.Requests)
	}

	p50, p95, err := s.deps.DB.LatencyPercentiles(r.Context(), overviewWindow)
	if err != nil {
		// A percentile failure must not fail the overview: the tile renders
		// the bare number it renders today.
		p50, p95 = 0, 0
	}
	failovers, err := s.deps.DB.RecentFailovers(r.Context(), overviewWindow, 5)
	if err != nil {
		failovers = []store.FailoverRow{}
	}
	edges, err := s.deps.DB.FailoverEdges(r.Context(), overviewWindow)
	if err != nil {
		// Same reasoning as the percentiles: a graph that loses its return
		// arcs is worse than an overview that fails to load.
		edges = []store.FailoverEdge{}
	}
	series, err := s.deps.DB.UsageBy(r.Context(), 30, store.UsageByDayOnly)
	if err != nil {
		series = []store.UsageRow{}
	}

	// The strip's other figures describe the live window; spend is labelled
	// as the day's, so it is sourced from the day rather than from
	// overviewWindow, or a busy gateway would report a few minutes of spend
	// as though it were the whole day.
	spendMicros, spendPriced, spendEstimated, err := s.deps.DB.SpendSince(r.Context(), startOfUTCDay(time.Now()))
	if err != nil {
		internalError(w, r, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"providers":        tiles,
		"requests_per_min": float64(stats.Requests) / (float64(stats.WindowSec) / 60),
		"error_rate":       errRate,
		"window_sec":       stats.WindowSec,
		"today_spend": map[string]any{
			"micros":    spendMicros,
			"priced":    spendPriced,
			"estimated": spendEstimated,
		},
		"latency":        map[string]any{"p50_ms": p50, "p95_ms": p95},
		"series":         series,
		"failovers":      failovers,
		"failover_edges": edges,
	})
}

// usageDimensions is the closed set of group_by values. An unknown one is a
// 400 rather than a silent fall back to the day-only rollup: a caller that
// misspells the dimension would otherwise render one series and never learn
// it asked for the wrong thing.
var usageDimensions = map[string]store.UsageDimension{
	"":         store.UsageByDayOnly,
	"provider": store.UsageByProvider,
	"model":    store.UsageByModel,
	"alias":    store.UsageByAlias,
}

func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	days := 30
	if n, set, err := queryInt(r, "days"); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	} else if set {
		if n < 1 || n > 365 {
			writeError(w, http.StatusBadRequest, "days must be between 1 and 365")
			return
		}
		days = int(n)
	}
	groupBy := r.URL.Query().Get("group_by")
	dim, ok := usageDimensions[groupBy]
	if !ok {
		writeError(w, http.StatusBadRequest,
			"group_by must be one of provider, model, alias")
		return
	}

	rows, err := s.deps.DB.UsageBy(r.Context(), days, dim)
	if err != nil {
		internalError(w, r, err)
		return
	}
	out := make([]map[string]any, 0, len(rows))
	priced := false
	for _, u := range rows {
		if u.CostMicros != nil {
			priced = true
		}
		row := map[string]any{
			"day": u.Day, "requests": u.Requests, "attempts": u.Attempts,
			"tokens_in": u.TokensIn, "tokens_out": u.TokensOut,
			"cost_micros": u.CostMicros,
		}
		if groupBy != "" {
			row["key"] = u.Key
		}
		out = append(out, row)
	}
	resp := map[string]any{"days": out, "priced": priced}
	// Omitted only when there is no group_by: existing consumers parse this
	// response today and must see the exact shape they always have.
	if groupBy != "" {
		resp["group_by"] = groupBy
	}
	writeJSON(w, http.StatusOK, resp)
}
