package admin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/adapter/bedrock"
	"github.com/darkraise/darkrouter/internal/adapter/vertex"
	"github.com/darkraise/darkrouter/internal/auth"
	"github.com/darkraise/darkrouter/internal/catalog"
	"github.com/darkraise/darkrouter/internal/health"
	"github.com/darkraise/darkrouter/internal/ir"
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
	// Walked from All() rather than Offering(): Offering maps a MODEL to the
	// providers serving it, which is the reverse of what is needed here and
	// would silently return nothing, leaving every triple cooling behind a
	// passing probe.
	for _, m := range s.deps.Catalog.Snapshot().All() {
		if m.ProviderID != providerID {
			continue
		}
		s.deps.Breaker.Record(healthKey(providerID, keyID, m.ModelID),
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

	// Spec §6: the probe extends to all three strategies and reports what
	// specifically failed — signature, permission, expiry, or reachability —
	// because "it doesn't work" is not actionable for any of them.
	style := row.AuthStyle
	if style == "" {
		style = preset.Auth.Style
	}
	switch style {
	case auth.StyleSigV4:
		return s.probeSigV4(ctx, row, cred)
	case auth.StyleGCPSA:
		return s.probeGCP(ctx, row, cred)
	case auth.StyleOAuth:
		return s.probeOAuth(ctx, row, cred)
	}

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

// authTargetFor is the provider half of a strategy resolution.
func authTargetFor(row store.ProviderRow, style string) auth.Target {
	return auth.Target{
		ProviderID: row.ID, Style: style, Preset: row.Preset,
		Region: row.Region, Project: row.Project, Location: row.Location,
	}
}

// probeSigV4 makes a real ListFoundationModels call, which exercises the
// signature, the region and the endpoint in one request. A signature mistake, a
// wrong region and a missing IAM permission all produce different statuses, and
// naming which one arrived is the difference between a fix and a guess.
func (s *Server) probeSigV4(ctx context.Context, row store.ProviderRow,
	cred store.Credential) (string, int, error) {

	if s.deps.Auth == nil {
		return "signature", 0, errors.New("the sigv4 strategy is not wired")
	}
	az, err := s.deps.Auth.For(ctx, authTargetFor(row, auth.StyleSigV4),
		auth.Credential{ID: cred.ID, Kind: cred.Kind, Secret: cred.Secret})
	if err != nil {
		return "signature", 0, err
	}
	models, err := bedrock.NewLister(s.httpClient()).List(ctx, catalog.Probe{
		ProviderID: row.ID, Kind: row.Kind, BaseURL: row.BaseURL,
		Region: row.Region, Authorize: az,
	})
	if err != nil {
		return classifyAWSProbe(err), 0, err
	}
	return "listing", len(models), nil
}

// classifyAWSProbe names what failed. A 403 is permission, not signature: the
// signature validated and the policy did not allow the call, which is a
// different fix from a wrong region or a revoked key.
func classifyAWSProbe(err error) string {
	text := strings.ToLower(err.Error())
	switch {
	case strings.Contains(text, "403"), strings.Contains(text, "accessdenied"):
		return "permission"
	case strings.Contains(text, "401"), strings.Contains(text, "unrecognizedclient"),
		strings.Contains(text, "invalidsignature"):
		return "signature"
	case strings.Contains(text, "no such host"), strings.Contains(text, "region"):
		return "region"
	}
	return "reachability"
}

// probeGCP exchanges a token and then generates a single token against one
// catalogued model. Spec §6: Vertex has no listing, so reachability has to be
// confirmed by an actual call.
func (s *Server) probeGCP(ctx context.Context, row store.ProviderRow,
	cred store.Credential) (string, int, error) {

	if s.deps.Auth == nil {
		return "expiry", 0, errors.New("the gcp-sa strategy is not wired")
	}
	az, err := s.deps.Auth.For(ctx, authTargetFor(row, auth.StyleGCPSA),
		auth.Credential{ID: cred.ID, Kind: cred.Kind, Secret: cred.Secret})
	if err != nil {
		return "expiry", 0, err
	}

	model, ok := s.oneCataloguedModel(row.ID)
	if !ok {
		// Reporting a generic failure here would send the operator looking at
		// credentials for a catalog that has not been seeded yet.
		return "completion", 0, errors.New(
			"this provider has no catalogued model to probe; it is seeded on the next discovery pass")
	}

	req, warns, err := vertex.New().BuildRequest(ctx, &adapter.Target{
		Project: row.Project, Location: row.Location,
		Publisher: model.publisher, Model: model.id,
	}, oneTokenProbe())
	_ = warns
	if err != nil {
		return "completion", 0, err
	}
	if err := az(ctx, req); err != nil {
		// The token exchange failed, not the endpoint. Different fix.
		return "expiry", 0, err
	}
	resp, err := s.httpClient().Do(req)
	if err != nil {
		return "reachability", 0, err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return "permission", 0, errors.New("vertex rejected this credential: " + resp.Status)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "reachability", 0, errors.New("vertex returned " + resp.Status)
	}
	return "completion", 1, nil
}

// oneTokenProbe is the cheapest generation that still exercises the endpoint.
func oneTokenProbe() *ir.Request {
	one := 1
	return &ir.Request{
		MaxTokens: &one,
		Messages: []ir.Message{{
			Role: ir.RoleUser, Content: []ir.ContentBlock{{Type: ir.BlockText, Text: "hi"}},
		}},
	}
}

type probeModel struct{ id, publisher string }

func (s *Server) oneCataloguedModel(providerID string) (probeModel, bool) {
	if s.deps.Catalog == nil {
		return probeModel{}, false
	}
	for _, m := range s.deps.Catalog.Snapshot().All() {
		if m.ProviderID == providerID {
			return probeModel{id: m.ModelID, publisher: m.Publisher}, true
		}
	}
	return probeModel{}, false
}

// probeOAuth refreshes under the per-account mutex.
//
// It goes through Manager.For and the returned authorizer rather than a private
// refresh call, which is how spec §5.2's "the credential probe shares that
// mutex" holds: sharing is automatic when the probe uses the same path a
// request does, and impossible to guarantee when it does not.
func (s *Server) probeOAuth(ctx context.Context, row store.ProviderRow,
	cred store.Credential) (string, int, error) {

	if s.deps.Auth == nil {
		return "refresh", 0, errors.New("the oauth strategy is not wired")
	}
	az, err := s.deps.Auth.For(ctx, authTargetFor(row, auth.StyleOAuth),
		auth.Credential{ID: cred.ID, Kind: cred.Kind, Secret: cred.Secret})
	if err != nil {
		return "refresh", 0, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://refresh.invalid", nil)
	if err != nil {
		return "refresh", 0, err
	}
	if err := az(ctx, req); err != nil {
		if errors.Is(err, auth.ErrNeedsReconnect) {
			return "refresh", 0, fmt.Errorf(
				"this account must be reconnected: the provider refused the refresh")
		}
		return "refresh", 0, err
	}
	return "refresh", 1, nil
}
