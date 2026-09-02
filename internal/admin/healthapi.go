package admin

import (
	"context"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/darkraise/darkrouter/internal/store"
)

// breakerEntry is one credential's cooldown state. Every field comes straight
// from health.Entry: the breaker already tracks exactly what §8.2 asks for, so
// this is a serialization rather than a second source of truth.
type breakerEntry struct {
	ProviderID          string `json:"provider_id"`
	KeyID               string `json:"key_id"`
	Model               string `json:"model"`
	CoolingUntil        string `json:"cooling_until,omitempty"`
	BackoffLevel        int    `json:"backoff_level"`
	ConsecutiveFailures int    `json:"consecutive_failures"`
}

func (s *Server) handleHealthProviders(w http.ResponseWriter, r *http.Request) {
	// Initialized rather than declared: a nil slice marshals to null, and the
	// console reads this as an array.
	out := []breakerEntry{}
	if s.deps.Breaker != nil {
		for _, e := range s.deps.Breaker.Snapshot() {
			view := breakerEntry{
				ProviderID:          e.Key.ProviderID,
				KeyID:               e.Key.KeyID,
				Model:               e.Key.Model,
				BackoffLevel:        e.BackoffLevel,
				ConsecutiveFailures: e.ConsecutiveFailures,
			}
			if !e.CoolingUntil.IsZero() {
				view.CoolingUntil = e.CoolingUntil.UTC().Format(time.RFC3339)
			}
			out = append(out, view)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": out})
}

func (s *Server) handleBreakerReset(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.providerExists(r.Context(), w, id) {
		return
	}
	// Every credential, not just the provider-level key: a cooldown is tracked
	// per credential, so resetting only the empty key would leave the entry
	// that is actually cooling untouched. Only the ids are needed, so the
	// keyring is not.
	summaries, err := s.deps.DB.CredentialSummaries(r.Context())
	if err != nil {
		internalError(w, r, err)
		return
	}
	creds := summaries[id]
	// Reset means "stop cooling", not "prove it works": clearCooldowns records
	// a success rather than spending the half-open probe, which is the
	// difference between this endpoint and POST /api/providers/{id}/test.
	s.clearCooldowns(r.Context(), id, "")
	for _, c := range creds {
		s.clearCooldowns(r.Context(), id, c.ID)
	}
	writeJSON(w, http.StatusOK, map[string]any{"reset": id, "credentials": len(creds)})
}

func (s *Server) handleForceDiscover(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !s.providerExists(r.Context(), w, id) {
		return
	}
	if s.deps.Disc == nil {
		writeError(w, http.StatusServiceUnavailable, "discovery is not running")
		return
	}
	// Triggered rather than awaited: a sweep talks to an upstream, and holding
	// the request open for it would make the button feel broken on a slow one.
	s.deps.Disc.Trigger(id)
	writeJSON(w, http.StatusAccepted, map[string]any{"triggered": id})
}

// handleForceCatalogSync starts a sync and answers at once. The sync fetches
// a document from the network with its own timeout; holding the request open
// for it made the button look broken on a slow link and tied the outcome to
// a client that may have gone away. A failure is logged the way the
// scheduled sync's is, and the previous metadata keeps serving.
func (s *Server) handleForceCatalogSync(w http.ResponseWriter, r *http.Request) {
	if s.deps.Sync == nil {
		writeError(w, http.StatusServiceUnavailable, "the catalog syncer is not running")
		return
	}
	go func() {
		if err := s.deps.Sync.SyncOnce(context.Background()); err != nil {
			log.Printf("admin: catalog sync: %v (serving the previous metadata)", err)
		}
	}()
	writeJSON(w, http.StatusAccepted, map[string]any{"triggered": true})
}

// providerExists writes a 404 and reports false when the id names nothing.
// Checked before acting so a typo in a path is an error rather than a silent
// no-op on a provider that was deleted.
func (s *Server) providerExists(ctx context.Context, w http.ResponseWriter, id string) bool {
	if _, err := s.deps.DB.ProviderByID(ctx, id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "no provider named "+id)
			return false
		}
		log.Printf("admin: read provider %q: %v", id, err)
		writeError(w, http.StatusInternalServerError, internalMessage)
		return false
	}
	return true
}
