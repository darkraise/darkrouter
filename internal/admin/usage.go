package admin

import (
	"net/http"
	"strconv"
	"time"
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

	writeJSON(w, http.StatusOK, map[string]any{
		"providers":        tiles,
		"requests_per_min": float64(stats.Requests) / (float64(stats.WindowSec) / 60),
		"error_rate":       errRate,
		"window_sec":       stats.WindowSec,
		"today_spend": map[string]any{
			"micros": stats.CostMicros,
			// Nothing computes cost yet: phase 5 recorded that it is blocked on
			// ir.Usage.InputTokens meaning different things across adapters. A
			// confident zero would read as "today was free".
			"priced": stats.PricedRows > 0,
		},
	})
}

func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	days := 30
	if n, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil {
		days = n
	}
	rows, err := s.deps.DB.UsageByDay(r.Context(), days)
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
		out = append(out, map[string]any{
			"day": u.Day, "requests": u.Requests,
			"tokens_in": u.TokensIn, "tokens_out": u.TokensOut,
			"cost_micros": u.CostMicros,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"days": out, "priced": priced})
}
