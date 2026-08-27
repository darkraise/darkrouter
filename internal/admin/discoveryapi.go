package admin

import "net/http"

type discoveryHealthView struct {
	ProviderID       string `json:"provider_id"`
	Total            int    `json:"total"`
	Live             int    `json:"live"`
	Stale            int    `json:"stale"`
	RemovedUpstream  int    `json:"removed_upstream"`
	MaxMissingStreak int    `json:"max_missing_streak"`
	// FilteredOut is how many models the last sweep dropped before recording
	// it. Non-zero only under the free-models filter.
	FilteredOut int `json:"filtered_out"`
}

func (s *Server) handleDiscoveryHealth(w http.ResponseWriter, r *http.Request) {
	rows, err := s.deps.DB.DiscoveryHealth(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]discoveryHealthView, 0, len(rows))
	for _, row := range rows {
		out = append(out, discoveryHealthView{
			ProviderID: row.ProviderID, Total: row.Total, Live: row.Live,
			Stale: row.Stale, RemovedUpstream: row.RemovedUpstream,
			MaxMissingStreak: row.MaxMissingStreak, FilteredOut: row.FilteredOut,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": out})
}
