package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/darkraise/darkrouter/internal/health"
	"github.com/darkraise/darkrouter/internal/store"
)

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
}

func (s *Server) handleListProviders(w http.ResponseWriter, r *http.Request) {
	rows, err := s.deps.DB.ProviderRows(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]providerView, 0, len(rows))
	for _, p := range rows {
		v := providerView{
			ID: p.ID, Name: p.Name, Preset: p.Preset, Kind: p.Kind,
			BaseURL: p.BaseURL, Priority: p.Priority, Enabled: p.Enabled,
			AuthStyle: p.AuthStyle, Credentials: []credentialView{},
		}
		if s.deps.Key != nil {
			creds, cerr := s.deps.DB.Credentials(r.Context(), s.deps.Key, p.ID)
			if cerr != nil {
				writeError(w, http.StatusInternalServerError, cerr.Error())
				return
			}
			for _, c := range creds {
				// Masked at the point of decryption. The plaintext never
				// reaches a struct that gets marshalled, which is the only
				// way "never returns credential material" survives a refactor.
				v.Credentials = append(v.Credentials, credentialView{
					ID: c.ID, Label: c.Label, Masked: maskSecret(c.Secret),
					Enabled: c.Enabled, Cooling: s.cooling(p.ID, c.ID),
				})
			}
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": out})
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
	// Location is set at creation only: changing it moves every catalogued
	// model to a different endpoint, which is a new provider rather than an
	// edit to this one.
	Location string `json:"location"`
}

func (s *Server) handleCreateProvider(w http.ResponseWriter, r *http.Request) {
	var body createProviderBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
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
		Enabled: body.Enabled == nil || *body.Enabled,
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

	if err := s.deps.DB.CreateProvider(r.Context(), row); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") || strings.Contains(err.Error(), "PRIMARY KEY") {
			writeError(w, http.StatusConflict, fmt.Sprintf("provider %q already exists", row.ID))
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.reloadProviders(r.Context())
	writeJSON(w, http.StatusCreated, map[string]any{"id": row.ID})
}

func (s *Server) handlePatchProvider(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// One decode into the patch: its pointer fields are what carry "named"
	// versus "absent", and an absent field decodes to nil.
	var patch store.ProviderPatch
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&patch); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := s.deps.DB.UpdateProvider(r.Context(), id, patch); err != nil {
		if strings.Contains(err.Error(), "no such provider") {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.reloadProviders(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

func (s *Server) handleDeleteProvider(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// Computed before the delete, because afterwards there is no provider to
	// match aliases against.
	dangling := s.danglingAliases(id)

	if err := s.deps.DB.DeleteProvider(r.Context(), id); err != nil {
		if strings.Contains(err.Error(), "no such provider") {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.reloadProviders(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{
		"id": id, "dangling_aliases": dangling,
	})
}

// danglingAliases names the aliases that will point at nothing once providerID
// is gone.
//
// Master design §7 makes a dangling alias a warning rather than a validation
// error, and this is why: if it were an error, one UI delete would make every
// subsequent config reload fail, leaving the operator with a reload button that
// keeps failing and no way out but SSH. The delete succeeds and reports.
func (s *Server) danglingAliases(providerID string) []string {
	out := []string{}
	if s.deps.Config == nil {
		return out
	}
	for name, chain := range s.deps.Config.Current().Aliases {
		for _, target := range chain {
			p, _, found := strings.Cut(target, "/")
			if found && p == providerID {
				out = append(out, name)
				break
			}
		}
	}
	// Sorted so two deletes of the same shape produce the same dialog text.
	sort.Strings(out)
	return out
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
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.Secret == "" {
		writeError(w, http.StatusBadRequest, "secret is required")
		return
	}
	if body.Label == "" {
		body.Label = "default"
	}
	id, err := s.deps.DB.AddCredential(r.Context(), s.deps.Key, store.Credential{
		ProviderID: r.PathValue("id"), Label: body.Label,
		Secret: body.Secret, Enabled: true,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.reloadProviders(r.Context())
	// The id and the label, never the secret — not even the one just supplied.
	// Echoing it back would put it in a response body, a proxy log and a
	// browser's network panel for no reason.
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "label": body.Label})
}

func (s *Server) handleDeleteCredential(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.DB.DeleteCredential(r.Context(),
		r.PathValue("id"), r.PathValue("keyId")); err != nil {
		if strings.Contains(err.Error(), "no such credential") {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.reloadProviders(r.Context())
	writeJSON(w, http.StatusOK, map[string]any{"id": r.PathValue("keyId")})
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
