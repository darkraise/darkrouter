package admin

import (
	"net/http"
	"strconv"
	"time"

	"github.com/darkraise/darkrouter/internal/store"
)

// overviewWindow is what "requests per minute" and the error rate are measured
// over. Five minutes rather than one: a homelab gateway can go a minute without
// traffic, and a rate computed over an empty minute reads as an outage.
const overviewWindow = 5 * time.Minute

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
		writeError(w, http.StatusInternalServerError, err.Error())
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
		if s.deps.Key != nil {
			creds, cerr := s.deps.DB.Credentials(r.Context(), s.deps.Key, p.ID)
			if cerr == nil {
				t.Credentials = len(creds)
				for _, c := range creds {
					// The one state only the operator can fix. Everything else
					// either recovers on its own or is a provider's problem, so
					// it is called out rather than folded into "degraded".
					if !c.Enabled {
						t.NeedsAuth = true
					}
				}
			}
		}
		switch {
		case !p.Enabled:
			t.State = "disabled"
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
		writeError(w, http.StatusInternalServerError, err.Error())
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
	series, err := s.deps.DB.UsageBy(r.Context(), 30, store.UsageByDayOnly)
	if err != nil {
		series = []store.UsageRow{}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"providers":        tiles,
		"requests_per_min": float64(stats.Requests) / (float64(stats.WindowSec) / 60),
		"error_rate":       errRate,
		"window_sec":       stats.WindowSec,
		"today_spend": map[string]any{
			"micros": stats.CostMicros,
			// An unpriced model leaves cost_micros NULL rather than zero, so a
			// summed total of zero is ambiguous between "no spend" and "no
			// price data for what ran". PricedRows disambiguates: it is what
			// zero actually means.
			"priced": stats.PricedRows > 0,
		},
		"latency":   map[string]any{"p50_ms": p50, "p95_ms": p95},
		"series":    series,
		"failovers": failovers,
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
	if n, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil {
		days = n
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
		writeError(w, http.StatusInternalServerError, err.Error())
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
