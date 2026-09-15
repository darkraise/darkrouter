package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/adapter/bedrock"
	geminiadapter "github.com/darkraise/darkrouter/internal/adapter/gemini"
	"github.com/darkraise/darkrouter/internal/adapter/vertex"
	"github.com/darkraise/darkrouter/internal/auth"
	"github.com/darkraise/darkrouter/internal/catalog"
	"github.com/darkraise/darkrouter/internal/health"
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/provider"
	"github.com/darkraise/darkrouter/internal/redact"
	"github.com/darkraise/darkrouter/internal/store"
)

// probeTimeout bounds one probe. It is generous — a cold provider on a slow link
// is the case the button exists for — but finite, because a hung probe holds the
// per-provider mutex and the operator's click looks ignored.
const probeTimeout = 30 * time.Second

func (s *Server) handleProbe(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.deps.Key == nil {
		writeError(w, http.StatusServiceUnavailable, "no keyring")
		return
	}

	row, err := s.deps.DB.ProviderByID(r.Context(), id)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	creds, err := s.deps.DB.Credentials(r.Context(), s.deps.Key, id)
	if err != nil {
		internalError(w, r, err)
		return
	}
	style := row.AuthStyle
	if style == "" {
		style = s.deps.Presets[row.Preset].Auth.Style
	}
	if len(creds) == 0 && !auth.IsKeyless(style) {
		// A refusal rather than a failed probe: there is nothing to test, and
		// reporting "credential invalid" for a provider with no credential
		// would send the operator looking for the wrong problem.
		//
		// A keyless provider is the exception rather than an omission: its
		// listing endpoint answers an unauthenticated request, and an
		// anonymous one answers the published key, so in both cases there is
		// something to test after all.
		writeError(w, http.StatusBadRequest, "this provider has no credential to test")
		return
	}
	// ?key= names one credential. Without it the first is tested, which is what
	// the provider-level probe has always meant. The import path needs the
	// narrow form: it is checking the key it just wrote, and creds[0] is
	// whichever key happened to sort first.
	var cred store.Credential
	if len(creds) > 0 {
		cred = creds[0]
	}
	if want := r.URL.Query().Get("key"); want != "" {
		found := false
		for _, c := range creds {
			if c.ID == want {
				cred, found = c, true
			}
		}
		if !found {
			writeError(w, http.StatusNotFound, fmt.Sprintf("no credential %q", want))
			return
		}
	}

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
		//
		// rejected separates a refusal of the credential from a probe that
		// could not finish — a timeout, a rate limit, an outage — so a caller
		// deciding whether to discard the key does not discard a good one.
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": false, "probe": kind, "latency_ms": latency,
			"error":    redact.Error(perr, cred.Secret).Error(),
			"rejected": errors.As(perr, new(rejectedCredential)),
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

// rejectedCredential marks a probe failure in which the provider answered and
// refused the credential itself.
type rejectedCredential struct{ error }

func (e rejectedCredential) Unwrap() error { return e.error }

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

	base, err := provider.ResolveBaseURL(row.BaseURL, cred.AccountID)
	if err != nil {
		return "listing", 0, err
	}
	pr, err := catalog.ProbeFor(provider.Provider{
		ID: row.ID, Kind: row.Kind, BaseURL: base, Preset: row.Preset, AuthStyle: style,
	}, preset, preset.Auth.Secret(cred.Secret))
	if err != nil {
		// No listing endpoint for this kind. Spec §4.3's fallback is a
		// one-token completion, which spends real money and consumes quota;
		// every kind that ships today has a listing endpoint, so the fallback
		// is reported rather than implemented against a path nothing exercises.
		return "completion", 0, fmt.Errorf(
			"this provider kind has no listing endpoint: %w", err)
	}

	count, err := s.countListing(ctx, pr)
	if err != nil {
		return listingProbeKind(err), count, err
	}
	return "listing", count, nil
}

// countListing reads every page of a listing through discovery's own loop, so
// the count is the number of models discovery will import rather than one page
// of them.
func (s *Server) countListing(ctx context.Context, pr catalog.Probe) (int, error) {
	models, err := catalog.ListPages(ctx, s.httpClient(), pr, classifyProbeListing)
	return len(models), err
}

// refusedPermission marks a probe the provider refused without refusing the
// credential: the key authenticated, or may have, and something about the
// account, project or key restrictions stopped the call.
type refusedPermission struct{ error }

func (e refusedPermission) Unwrap() error { return e.error }

// unauthorizedWithoutBadKey reports whether a 401's message blames something
// other than the key itself.
func unauthorizedWithoutBadKey(message string) bool {
	m := strings.ToLower(message)
	return strings.Contains(m, "ip not authorized") || strings.Contains(m, "allowlist") ||
		strings.Contains(m, "member of an organization") ||
		strings.Contains(m, "insufficient permissions") || strings.Contains(m, "missing scopes")
}

// listingProbeKind names what a failed listing probe found.
func listingProbeKind(err error) string {
	if errors.As(err, new(refusedPermission)) {
		return "permission"
	}
	return "listing"
}

func classifyProbeListing(resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode == http.StatusUnauthorized {
		why := upstreamMessage(bytes.NewReader(raw))
		// OpenAI also answers 401 for a key that is fine: a request from
		// outside the project's IP allowlist, an account with no
		// organization, or a restricted key missing a permission. Its
		// documentation gives those no error code, so the message is all
		// there is to tell them from a bad key.
		if unauthorizedWithoutBadKey(why) {
			return refusedPermission{errors.New("the provider refused this call: " + resp.Status +
				": " + why + "; the credential was not refused, so check the key's " +
				"permissions, the account's organization and the IP allowlist")}
		}
		msg := "the provider rejected this credential: " + resp.Status
		if why != "" {
			msg += ": " + why
		}
		return rejectedCredential{errors.New(msg)}
	}
	// Google refuses an unknown or expired API key with a 400, naming the
	// refusal only in the ErrorInfo reason.
	if geminiadapter.APIKeyInvalid(raw) {
		return rejectedCredential{errors.New(
			"the provider rejected this credential: " + resp.Status + ": " +
				upstreamMessage(bytes.NewReader(raw)))}
	}
	// A 403 is not a bad key. OpenAI sends one for an unsupported country,
	// Anthropic for a key without a permission, and Gemini for a disabled API,
	// a key restriction, or a key it reports as leaked — a flag Google has
	// raised on working keys. A new key would meet every one of them again.
	if resp.StatusCode == http.StatusForbidden {
		msg := "the provider refused this call: " + resp.Status
		if why := upstreamMessage(bytes.NewReader(raw)); why != "" {
			msg += ": " + why
		}
		return refusedPermission{errors.New(msg +
			"; the credential was not refused, so check the account's permissions, " +
			"region and any restrictions on the key")}
	}
	// The status alone is a poor answer when the provider said something
	// specific: "Bad Gateway" for a local CLI that is merely logged out
	// sends the operator looking for a network problem, when the reply
	// already said to run auggie login.
	if why := upstreamMessage(bytes.NewReader(raw)); why != "" {
		return errors.New(resp.Status + ": " + why)
	}
	return errors.New("the provider returned " + resp.Status)
}

// upstreamMessage reads the message out of an OpenAI-shaped error body, which
// is what most providers and every darkrouter-side transport return. Anything
// else yields the empty string and the caller keeps the status alone: a page of
// HTML in a toast is worse than no detail at all.
func upstreamMessage(r io.Reader) string {
	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.NewDecoder(io.LimitReader(r, 64<<10)).Decode(&body); err != nil {
		return ""
	}
	msg := strings.TrimSpace(body.Error.Message)
	if len(msg) > 300 {
		msg = msg[:300] + "…"
	}
	return msg
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
		kind := classifyAWSProbe(err)
		// AWS also answers a skewed host clock with InvalidSignatureException.
		// That key is good; the clock is not.
		if kind == "signature" && !strings.Contains(strings.ToLower(err.Error()), "signature expired") {
			err = rejectedCredential{err}
		}
		return kind, 0, err
	}
	return "listing", len(models), nil
}

// classifyAWSProbe names what failed. A 403 is permission, not signature: the
// signature validated and the policy did not allow the call, which is a
// different fix from a wrong region or a revoked key.
//
// The error type is read first where AWS sent one: an unrecognised key and a
// bad signature both arrive as a 403, so the status alone would call them
// permission failures.
func classifyAWSProbe(err error) string {
	var le *bedrock.ListError
	if errors.As(err, &le) {
		switch le.Type {
		case "UnrecognizedClientException", "InvalidSignatureException":
			return "signature"
		case "AccessDeniedException":
			return "permission"
		}
	}
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
		//
		// Only one invalid_grant is the key itself: Google documents "Invalid
		// JWT Signature." as a key not associated with the account, or deleted,
		// disabled or expired. The same code also names a skewed host clock.
		if gcpKeySignatureRefused(err) {
			err = rejectedCredential{err}
		}
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
	if resp.StatusCode == http.StatusUnauthorized {
		return "permission", 0, rejectedCredential{
			errors.New("vertex rejected this credential: " + resp.Status)}
	}
	// Not rejected: Google answers 403 to a key that authenticated but whose
	// project has the Vertex AI API disabled, lacks the IAM role, or has not
	// enabled the model. Discarding the key would not fix any of those.
	if resp.StatusCode == http.StatusForbidden {
		return "permission", 0, errors.New("vertex refused this call: " + resp.Status +
			"; check that the Vertex AI API is enabled, the service account has an IAM role " +
			"allowing it, and the model is enabled in the project")
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "reachability", 0, errors.New("vertex returned " + resp.Status)
	}
	return "completion", 1, nil
}

func gcpKeySignatureRefused(err error) bool {
	var re *oauth2.RetrieveError
	if !errors.As(err, &re) {
		return false
	}
	code, desc := re.ErrorCode, re.ErrorDescription
	// The JWT flow returns the body without parsing RFC 6749's fields.
	if code == "" {
		var body struct {
			Error       string `json:"error"`
			Description string `json:"error_description"`
		}
		if json.Unmarshal(re.Body, &body) != nil {
			return false
		}
		code, desc = body.Error, body.Description
	}
	return code == "invalid_grant" && strings.HasPrefix(desc, "Invalid JWT Signature")
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
	pr, err := catalog.ProbeFor(provider.Provider{
		ID: row.ID, Kind: row.Kind, BaseURL: row.BaseURL, Preset: row.Preset,
		AuthStyle: auth.StyleOAuth,
	}, s.deps.Presets[row.Preset], "")
	if err != nil {
		return "completion", 0, fmt.Errorf(
			"this provider kind has no listing endpoint: %w", err)
	}
	// Authorized here rather than inside the listing, so a refused refresh is
	// reported as one. A cached, unexpired token refreshes nothing, which is
	// why the listing still has to be sent: only the provider can say whether
	// that token was revoked.
	refreshed := false
	pr.Authorize = func(ctx context.Context, req *http.Request) error {
		if err := az(ctx, req); err != nil {
			return err
		}
		refreshed = true
		return nil
	}
	count, err := s.countListing(ctx, pr)
	if err != nil && !refreshed {
		if errors.Is(err, auth.ErrNeedsReconnect) {
			return "refresh", 0, rejectedCredential{fmt.Errorf(
				"this account must be reconnected: the provider refused the refresh")}
		}
		return "refresh", 0, err
	}
	if err != nil {
		return listingProbeKind(err), count, err
	}
	return "listing", count, nil
}
