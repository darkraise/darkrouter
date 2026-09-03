package admin

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strings"

	"github.com/darkraise/darkrouter/internal/auth"
	"github.com/darkraise/darkrouter/internal/health"
	"github.com/darkraise/darkrouter/internal/store"
)

// providerIDPattern bounds a provider id to what a URL path segment, a log
// line and an alias target can all carry unescaped.
var providerIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// maxPriority bounds priority. The value is an ordering, and a thousand
// distinct ranks is more than any provider set needs.
const maxPriority = 1000

// authStyles is the closed vocabulary a provider row may carry.
var authStyles = []string{
	auth.StyleBearer, auth.StyleXAPIKey, auth.StyleAPIKey, auth.StyleQueryParam,
	auth.StyleNone, auth.StyleOptional, auth.StyleAnonymous,
	auth.StyleSigV4, auth.StyleGCPSA, auth.StyleOAuth,
}

func validBaseURL(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// validateProviderRow checks the fields a create or a patch can set. The
// kind check is skipped when no registry was supplied, which is a test
// building a server without an executor.
func (s *Server) validateProviderRow(row store.ProviderRow) error {
	if !providerIDPattern.MatchString(row.ID) {
		return fmt.Errorf("id must match %s", providerIDPattern.String())
	}
	if !validBaseURL(row.BaseURL) {
		return fmt.Errorf("base_url must be an http or https URL")
	}
	if s.deps.Kinds != nil && !slices.Contains(s.deps.Kinds, row.Kind) {
		return fmt.Errorf("kind %q is not one this build serves", row.Kind)
	}
	if !slices.Contains(authStyles, row.AuthStyle) {
		return fmt.Errorf("auth_style %q is not one of %s", row.AuthStyle, strings.Join(authStyles, ", "))
	}
	if row.Priority < 0 || row.Priority > maxPriority {
		return fmt.Errorf("priority must be between 0 and %d", maxPriority)
	}
	return nil
}

// maskSecret renders a credential for display. It shows the last four characters
// and nothing else.
//
// A short secret is masked entirely rather than partially: showing three of four
// characters of a four-character key is not a mask. The suffix exists so an
// operator can tell two keys apart, which four characters achieves and eight
// would not improve.
func maskSecret(secret string) string {
	if len(secret) <= 4 {
		return "****"
	}
	return "…" + secret[len(secret)-4:]
}

// healthKey builds the breaker key. It is a helper rather than a literal because
// the empty Model is load-bearing: a credential-level cooldown is stored under a
// key with no model, and a triple cooldown under one with a model. Getting that
// backwards makes a cooling credential look available.
func healthKey(providerID, keyID, model string) health.Key {
	return health.Key{ProviderID: providerID, KeyID: keyID, Model: model}
}

type credentialView struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Masked  string `json:"masked"`
	Enabled bool   `json:"enabled"`
	Cooling bool   `json:"cooling"`
	Kind    string `json:"kind"`
	// OAuth-only metadata. A static key has neither, and omitting them is what
	// keeps the table honest about which rows have an account behind them.
	// Neither is secret: this is metadata about a token, not the token.
	ExpiresAt *int64 `json:"expires_at,omitempty"`
	Scope     string `json:"scope,omitempty"`
}

type providerView struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Preset   string `json:"preset"`
	Kind     string `json:"kind"`
	BaseURL  string `json:"base_url"`
	Priority int    `json:"priority"`
	Enabled  bool   `json:"enabled"`
	// AuthStyle tells the dashboard which credential form to show. A static
	// key form is useless for an oauth provider: there is no key to type.
	AuthStyle   string           `json:"auth_style"`
	Credentials []credentialView `json:"credentials"`
	// FreeModelsOnly narrows what the next discovery sweep imports. The
	// console shows it wherever it shows what a provider can serve.
	FreeModelsOnly bool `json:"free_models_only"`
	// AllowUnsanctionedFree lets this provider's `avoid`-graded free models be
	// imported and routed to. The console explains the risk beside the control.
	AllowUnsanctionedFree bool `json:"allow_unsanctioned_free"`
}

func (s *Server) handleListProviders(w http.ResponseWriter, r *http.Request) {
	rows, err := s.deps.DB.ProviderRows(r.Context())
	if err != nil {
		internalError(w, r, err)
		return
	}
	out := make([]providerView, 0, len(rows))
	for _, p := range rows {
		v, err := s.providerView(r.Context(), p)
		if err != nil {
			internalError(w, r, err)
			return
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": out})
}

// providerView renders one row with its credentials. The secrets are
// decrypted here because the masked suffix is part of the view; every other
// reader of credentials takes the summary and never touches the keyring.
func (s *Server) providerView(ctx context.Context, p store.ProviderRow) (providerView, error) {
	v := providerView{
		ID: p.ID, Name: p.Name, Preset: p.Preset, Kind: p.Kind,
		BaseURL: p.BaseURL, Priority: p.Priority, Enabled: p.Enabled,
		AuthStyle: p.AuthStyle, Credentials: []credentialView{},
		FreeModelsOnly: p.FreeModelsOnly, AllowUnsanctionedFree: p.AllowUnsanctionedFree,
	}
	if s.deps.Key == nil {
		return v, nil
	}
	creds, err := s.deps.DB.Credentials(ctx, s.deps.Key, p.ID)
	if err != nil {
		return providerView{}, err
	}
	for _, c := range creds {
		v.Credentials = append(v.Credentials, s.credentialView(p.ID, c))
	}
	return v, nil
}

// credentialView masks at the point of decryption. The plaintext never
// reaches a struct that gets marshalled, which is the only way "never returns
// credential material" survives a refactor.
func (s *Server) credentialView(providerID string, c store.Credential) credentialView {
	return credentialView{
		ID: c.ID, Label: c.Label, Masked: maskSecret(c.Secret),
		Enabled: c.Enabled, Cooling: s.cooling(providerID, c.ID),
		Kind: c.Kind, ExpiresAt: c.ExpiresAt, Scope: c.Scope,
	}
}

// cooling reports whether the breaker is holding this credential down. It is
// per-credential rather than per-triple because the settings screen shows one
// row per credential and "some of its models are cooling" is not a state a
// checkbox can render.
func (s *Server) cooling(providerID, keyID string) bool {
	if s.deps.Breaker == nil {
		return false
	}
	return !s.deps.Breaker.Available(healthKey(providerID, keyID, ""))
}

type createProviderBody struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Preset  string `json:"preset"`
	Kind    string `json:"kind"`
	BaseURL string `json:"base_url"`
	// AuthStyle overrides the preset's. Without it a provider created for a
	// kind whose preset this build does not ship falls back to bearer, and a
	// signed credential is never signed.
	AuthStyle string `json:"auth_style"`
	Priority  int    `json:"priority"`
	Enabled   *bool  `json:"enabled"`
	Region    string `json:"region"`
	Project   string `json:"project"`
	// FreeModelsOnly is settable at creation because the wizard asks for it
	// before the first sweep runs, which is the only time the answer changes
	// what the catalogue ever held.
	FreeModelsOnly bool `json:"free_models_only"`
	// AllowUnsanctionedFree lets this provider's `avoid`-graded free models be
	// imported and routed to. The console explains the risk beside the control.
	AllowUnsanctionedFree bool `json:"allow_unsanctioned_free"`
	// Location is set at creation only: changing it moves every catalogued
	// model to a different endpoint, which is a new provider rather than an
	// edit to this one.
	Location string `json:"location"`
}

func (s *Server) handleCreateProvider(w http.ResponseWriter, r *http.Request) {
	var body createProviderBody
	if !decodeJSON(w, r, 64<<10, &body) {
		return
	}
	if body.ID == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}

	row := store.ProviderRow{
		ID: body.ID, Name: body.Name, Preset: body.Preset,
		Kind: body.Kind, BaseURL: body.BaseURL, AuthStyle: body.AuthStyle,
		Priority: body.Priority,
		Region:   body.Region, Project: body.Project, Location: body.Location,
		Enabled:               body.Enabled == nil || *body.Enabled,
		FreeModelsOnly:        body.FreeModelsOnly,
		AllowUnsanctionedFree: body.AllowUnsanctionedFree,
	}
	// From a preset the operator supplies an id and a key and nothing else,
	// which is the whole reason presets ship. Explicit values still win, so a
	// preset can be used as a starting point.
	if body.Preset != "" {
		p, ok := s.deps.Presets[body.Preset]
		if !ok {
			writeError(w, http.StatusBadRequest,
				fmt.Sprintf("preset %q is not a shipped preset", body.Preset))
			return
		}
		if row.Kind == "" {
			row.Kind = p.Kind
		}
		if row.BaseURL == "" {
			row.BaseURL = p.BaseURL
		}
		if row.Name == "" {
			row.Name = p.Name
		}
		if row.AuthStyle == "" {
			row.AuthStyle = p.Auth.Style
		}
	}
	if row.Kind == "" || row.BaseURL == "" {
		writeError(w, http.StatusBadRequest,
			"kind and base_url are required unless a preset supplies them")
		return
	}
	if row.Name == "" {
		row.Name = row.ID
	}
	if row.AuthStyle == "" {
		// The column default, applied here so the vocabulary check below
		// sees the value the row will carry.
		row.AuthStyle = auth.StyleBearer
	}
	if err := s.validateProviderRow(row); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := s.deps.DB.CreateProvider(r.Context(), row); err != nil {
		writeStoreError(w, r, err)
		return
	}
	s.reloadProviders(afterCommit(r))
	// A keyless provider is discoverable the moment it exists: the sweep needs
	// one of the provider's own keys, and this one has none to need. Waiting a
	// quarter of an hour for its first models is the same gap the first
	// credential closes for a keyed provider.
	if auth.IsKeyless(row.AuthStyle) && s.deps.Disc != nil {
		s.deps.Disc.Trigger(row.ID)
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": row.ID})
}

func (s *Server) handlePatchProvider(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// One decode into the patch: its pointer fields are what carry "named"
	// versus "absent", and an absent field decodes to nil.
	var patch store.ProviderPatch
	if !decodeJSON(w, r, 64<<10, &patch) {
		return
	}
	if patch.Name == nil && patch.BaseURL == nil && patch.Priority == nil &&
		patch.Enabled == nil && patch.Region == nil && patch.Project == nil &&
		patch.FreeModelsOnly == nil && patch.AllowUnsanctionedFree == nil {
		// An empty patch is a client bug, not a no-op to absorb: it means the
		// UI sent a form it did not fill in.
		writeError(w, http.StatusBadRequest, "the patch names no fields")
		return
	}
	// Validated as the row it would produce, so the same rules apply to a
	// patch as to a create.
	current, err := s.deps.DB.ProviderByID(r.Context(), id)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	next := current
	if patch.BaseURL != nil {
		next.BaseURL = *patch.BaseURL
	}
	if patch.Priority != nil {
		next.Priority = *patch.Priority
	}
	if err := s.validateProviderRow(next); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.deps.DB.UpdateProvider(r.Context(), id, patch); err != nil {
		writeStoreError(w, r, err)
		return
	}
	s.reloadProviders(afterCommit(r))
	updated, err := s.deps.DB.ProviderByID(r.Context(), id)
	if err != nil {
		writeStoreError(w, r, err)
		return
	}
	view, err := s.providerView(r.Context(), updated)
	if err != nil {
		internalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (s *Server) handleDeleteProvider(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// Read before the delete: the cascade takes the credential rows, and
	// their ids are what the auth manager caches under.
	summaries, err := s.deps.DB.CredentialSummaries(r.Context())
	if err != nil {
		internalError(w, r, err)
		return
	}
	if err := s.deps.DB.DeleteProvider(r.Context(), id); err != nil {
		writeStoreError(w, r, err)
		return
	}
	for _, c := range summaries[id] {
		s.forgetCredential(c.ID)
	}
	s.probes.drop(id)
	s.reloadProviders(afterCommit(r))
	w.WriteHeader(http.StatusNoContent)
}

// reloadProviders pushes the mutation into the running router. Without it the
// change is in the database and the gateway keeps serving the old provider set
// until something else happens to reload.
func (s *Server) reloadProviders(ctx context.Context) {
	if s.deps.Src == nil {
		return
	}
	// A reload failure is not reported to the caller: the mutation succeeded
	// and the database is the source of truth. The next natural reload picks it
	// up, and reporting a 500 for a write that landed would be worse.
	_ = s.deps.Src.Reload(ctx)
	// Reloading the source is not enough. Provider identity, order and
	// enablement are baked into the catalog snapshot when it is built, so
	// without this the operator's change reaches routing only when some
	// unrelated worker next rebuilds — up to a discovery interval away.
	if s.deps.Catalog != nil {
		_ = s.deps.Catalog.Rebuild(ctx)
	}
}

// forgetCredential drops any in-memory state the auth manager derived from a
// credential's old secret. Reached through an assertion rather than through
// AuthResolver so that interface stays the one method the probe needs.
func (s *Server) forgetCredential(credID string) {
	if f, ok := s.deps.Auth.(interface{ Forget(string) }); ok {
		f.Forget(credID)
	}
}

type addCredentialBody struct {
	Label  string `json:"label"`
	Secret string `json:"secret"`
}

func (s *Server) handleAddCredential(w http.ResponseWriter, r *http.Request) {
	if s.deps.Key == nil {
		writeError(w, http.StatusServiceUnavailable, "no keyring")
		return
	}
	var body addCredentialBody
	if !decodeJSON(w, r, 64<<10, &body) {
		return
	}
	if body.Secret == "" {
		writeError(w, http.StatusBadRequest, "secret is required")
		return
	}
	if body.Label == "" {
		body.Label = "default"
	}
	providerID := r.PathValue("id")
	if _, err := s.deps.DB.ProviderByID(r.Context(), providerID); err != nil {
		writeStoreError(w, r, err)
		return
	}
	// Counted before the write, so "was there anything here" is answerable
	// afterwards. A provider with no credential cannot be swept at all: the
	// discoverer needs one of the provider's own keys to ask what it serves,
	// so the first key is the moment the provider becomes discoverable.
	before, cerr := s.deps.DB.CredentialSummaries(r.Context())
	firstCredential := cerr == nil && len(before[providerID]) == 0

	id, err := s.deps.DB.AddCredential(r.Context(), s.deps.Key, store.Credential{
		ProviderID: providerID, Label: body.Label,
		Secret: body.Secret, Enabled: true,
	})
	if err != nil {
		internalError(w, r, err)
		return
	}
	s.reloadProviders(afterCommit(r))
	// Only on the first one. A bulk import of twenty keys would otherwise ask
	// the provider to list its models twenty times, against a rate limit the
	// operator has just finished telling us they care about — and the second
	// key does not change what the provider lists.
	if firstCredential && s.deps.Disc != nil {
		s.deps.Disc.Trigger(providerID)
	}
	// The id and the label, never the secret — not even the one just supplied.
	// Echoing it back would put it in a response body, a proxy log and a
	// browser's network panel for no reason.
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "label": body.Label})
}

func (s *Server) handleDeleteCredential(w http.ResponseWriter, r *http.Request) {
	keyID := r.PathValue("keyId")
	if err := s.deps.DB.DeleteCredential(r.Context(), r.PathValue("id"), keyID); err != nil {
		writeStoreError(w, r, err)
		return
	}
	// The auth manager caches an OAuth account under the credential id; a
	// deleted credential must not keep presenting the token it minted.
	s.forgetCredential(keyID)
	s.reloadProviders(afterCommit(r))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePresets(w http.ResponseWriter, r *http.Request) {
	type presetView struct {
		ID       string   `json:"id"`
		Name     string   `json:"name"`
		Kind     string   `json:"kind"`
		BaseURL  string   `json:"base_url"`
		Surfaces []string `json:"surfaces"`
		AuthKind string   `json:"auth_kind"`
		Website  string   `json:"website"`
		FreeTier bool     `json:"free_tier"`
	}
	out := make([]presetView, 0, len(s.deps.Presets))
	for id, p := range s.deps.Presets {
		out = append(out, presetView{
			ID: id, Name: p.Name, Kind: p.Kind, BaseURL: p.BaseURL,
			Surfaces: p.Surfaces, AuthKind: p.Auth.Style,
			Website: p.Website, FreeTier: p.FreeTier,
		})
	}
	// Sorted by id: a map iteration order would reshuffle the create form's
	// dropdown on every poll.
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	writeJSON(w, http.StatusOK, map[string]any{"presets": out})
}
