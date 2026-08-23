package admin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/catalog"
	"github.com/darkraise/darkrouter/internal/health"
	"github.com/darkraise/darkrouter/internal/provider"
	"github.com/darkraise/darkrouter/internal/store"
)

// probeTimeout bounds one probe. It is generous — a cold provider on a slow link
// is the case the button exists for — but finite, because a hung probe holds the
// per-provider mutex and the operator's click looks ignored.
const probeTimeout = 30 * time.Second

// maxProbeBody bounds the listing read, matching what discovery uses. A listing
// endpoint that streams unbounded data must not exhaust memory here either.
const maxProbeBody = 8 << 20

func (s *Server) handleProbe(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.deps.Key == nil {
		writeError(w, http.StatusServiceUnavailable, "no keyring")
		return
	}

	rows, err := s.deps.DB.ProviderRows(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var row store.ProviderRow
	var found bool
	for _, p := range rows {
		if p.ID == id {
			row, found = p, true
		}
	}
	if !found {
		writeError(w, http.StatusNotFound, fmt.Sprintf("no provider %q", id))
		return
	}
	creds, err := s.deps.DB.Credentials(r.Context(), s.deps.Key, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(creds) == 0 {
		// A refusal rather than a failed probe: there is nothing to test, and
		// reporting "credential invalid" for a provider with no credential
		// would send the operator looking for the wrong problem.
		writeError(w, http.StatusBadRequest, "this provider has no credential to test")
		return
	}
	cred := creds[0]

	// One probe per provider at a time. Spec §4.3: a double-click must issue
	// one probe, because two racing on the same credential produce two answers
	// and the operator cannot tell which is current.
	lock := s.probes.get(id)
	lock.Lock()
	defer lock.Unlock()

	ctx, cancel := context.WithTimeout(r.Context(), probeTimeout)
	defer cancel()

	started := time.Now()
	kind, count, perr := s.runProbe(ctx, row, cred)
	latency := time.Since(started).Milliseconds()

	if perr != nil {
		// 200 with ok:false. A rejected key is an answer, not a server error,
		// and a 500 would make the settings screen show "something broke" for
		// the one outcome the button exists to discover.
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": false, "probe": kind, "latency_ms": latency, "error": perr.Error(),
		})
		return
	}

	s.clearCooldowns(r.Context(), id, cred.ID)
	// Spec §4.3: a successful probe triggers an on-demand discovery pass, so a
	// newly added provider's models appear without waiting for the sweep.
	if s.deps.Disc != nil {
		s.deps.Disc.Trigger(id)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "probe": kind, "latency_ms": latency, "model_count": count,
	})
}

// clearCooldowns resets the ladder after a successful probe.
//
// Spec §4.3 requires it, or the operator reads "probe OK" beside "still
// cooling". health.Breaker.Record already deletes the entry on OutcomeSuccess,
// so no new breaker API is needed — but it has to be recorded against BOTH key
// shapes. A credential-level cooldown is stored under a key with an EMPTY
// model; a triple cooldown under one with a model. Clearing only one leaves the
// other cooling, which is exactly the confusion the probe exists to remove.
func (s *Server) clearCooldowns(ctx context.Context, providerID, keyID string) {
	if s.deps.Breaker == nil {
		return
	}
	s.deps.Breaker.Record(healthKey(providerID, keyID, ""),
		health.Signal{Outcome: adapter.OutcomeSuccess})
	if s.deps.Catalog == nil {
		return
	}
	for _, m := range s.deps.Catalog.Snapshot().Offering(providerID) {
		s.deps.Breaker.Record(healthKey(providerID, keyID, m),
			health.Signal{Outcome: adapter.OutcomeSuccess})
	}
}

// runProbe performs the real upstream call.
//
// It reuses phase 6's own listing request builder and parser rather than
// building its own, which is what keeps the button honest: it exercises the same
// path discovery does, so "probe OK" means discovery will work. A probe that
// built its own request would stop being evidence of anything.
//
// It deliberately bypasses the circuit breaker. Spec §4.3: the most common
// purpose is checking whether a cooling provider has recovered, and a probe that
// refused because the provider is cooling would answer a question nobody asked.
func (s *Server) runProbe(ctx context.Context, row store.ProviderRow,
	cred store.Credential) (kind string, modelCount int, err error) {

	preset := s.deps.Presets[row.Preset]
	pr, err := catalog.ProbeFor(provider.Provider{
		ID: row.ID, Kind: row.Kind, BaseURL: row.BaseURL, Preset: row.Preset,
	}, preset, cred.Secret)
	if err != nil {
		// No listing endpoint for this kind. Spec §4.3's fallback is a
		// one-token completion, which spends real money and consumes quota;
		// every kind that ships today has a listing endpoint, so the fallback
		// is reported rather than implemented against a path nothing exercises.
		return "completion", 0, fmt.Errorf(
			"this provider kind has no listing endpoint: %w", err)
	}

	req, err := catalog.BuildListRequest(ctx, pr)
	if err != nil {
		return "listing", 0, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "listing", 0, err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return "listing", 0, errors.New("the provider rejected this credential: " + resp.Status)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "listing", 0, errors.New("the provider returned " + resp.Status)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxProbeBody))
	if err != nil {
		return "listing", 0, err
	}
	models, err := catalog.ParseList(pr.Kind, body)
	if err != nil {
		return "listing", 0, err
	}
	return "listing", len(models), nil
}
