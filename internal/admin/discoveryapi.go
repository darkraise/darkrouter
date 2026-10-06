package admin

import (
	"net/http"
	"time"
)

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

	// The sweep bookkeeping. Without it a provider whose every sweep fails
	// reads exactly like one that answered with an empty list: "0 of 0 live"
	// for both, and the console called it healthy. Zero failures is a provider
	// whose last sweep succeeded.
	ConsecutiveFailures int        `json:"consecutive_failures"`
	LastError           string     `json:"last_error,omitempty"`
	LastSuccessAt       *time.Time `json:"last_success_at,omitempty"`
}

func (s *Server) handleDiscoveryHealth(w http.ResponseWriter, r *http.Request) {
	rows, err := s.deps.DB.DiscoveryHealth(r.Context())
	if err != nil {
		internalError(w, r, err)
		return
	}
	states, err := s.deps.DB.DiscoveryStates(r.Context())
	if err != nil {
		internalError(w, r, err)
		return
	}
	out := make([]discoveryHealthView, 0, len(rows))
	for _, row := range rows {
		v := discoveryHealthView{
			ProviderID: row.ProviderID, Total: row.Total, Live: row.Live,
			Stale: row.Stale, RemovedUpstream: row.RemovedUpstream,
			MaxMissingStreak: row.MaxMissingStreak, FilteredOut: row.FilteredOut,
		}
		if st, ok := states[row.ProviderID]; ok {
			v.ConsecutiveFailures = st.ConsecutiveFailures
			v.LastError = st.LastError
			if !st.LastSuccessAt.IsZero() {
				at := st.LastSuccessAt
				v.LastSuccessAt = &at
			}
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": out})
}
