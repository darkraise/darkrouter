package admin

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/store"
)

// The alias and policy endpoints are focused views of what /api/config already
// serves. They share its store methods and its validation rather than owning a
// second write path, because two paths that can disagree is the failure worth
// designing out.

func (s *Server) handleAliases(w http.ResponseWriter, r *http.Request) {
	if s.deps.Config == nil {
		writeError(w, http.StatusServiceUnavailable, "no configuration store")
		return
	}
	aliases := s.deps.Config.Current().Aliases
	if aliases == nil {
		aliases = map[string][]string{}
	}
	writeJSON(w, http.StatusOK, aliases)
}

func (s *Server) handlePutAliases(w http.ResponseWriter, r *http.Request) {
	if s.deps.Config == nil || s.deps.DB == nil {
		writeError(w, http.StatusServiceUnavailable, "no configuration store")
		return
	}
	var aliases map[string][]string
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&aliases); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := config.ValidateAliases(aliases); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.aliasTargetsExist(r.Context(), aliases); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.deps.DB.PutAliases(r.Context(), aliases); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.republish(w)
}

func (s *Server) handlePolicy(w http.ResponseWriter, r *http.Request) {
	if s.deps.Config == nil {
		writeError(w, http.StatusServiceUnavailable, "no configuration store")
		return
	}
	writeJSON(w, http.StatusOK, policyBlock(s.deps.Config.Current().Policy))
}

func (s *Server) handlePutPolicy(w http.ResponseWriter, r *http.Request) {
	if s.deps.Config == nil || s.deps.DB == nil {
		writeError(w, http.StatusServiceUnavailable, "no configuration store")
		return
	}
	var body policyWrite
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if cold := restartOnlyIn(&body); len(cold) > 0 {
		writeError(w, http.StatusBadRequest,
			joinFields(cold)+" takes effect on restart and cannot be written here")
		return
	}
	next := s.deps.Config.Current().Policy
	if err := applyPolicyWrite(&next, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.deps.DB.PutPolicy(r.Context(), next); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.republish(w)
}

// republish reloads so the next snapshot a request takes carries the write.
// The overlay is what pulls it back out of SQLite.
func (s *Server) republish(w http.ResponseWriter) {
	if err := s.deps.Config.Reload(); err != nil {
		writeJSON(w, http.StatusOK, map[string]any{
			"valid": false, "error": err.Error(),
			"serving": "the previous configuration is still serving",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"valid": true})
}

type overrideBody struct {
	Surfaces      []string                 `json:"surfaces"`
	Capabilities  *store.ModelCapabilities `json:"capabilities"`
	ContextWindow *int                     `json:"context_window"`
}

func (s *Server) handleGetOverride(w http.ResponseWriter, r *http.Request) {
	providerID, modelID := r.PathValue("provider"), r.PathValue("model")
	rows, err := s.deps.DB.ModelOverrides(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, o := range rows {
		if o.ProviderID == providerID && o.ModelID == modelID {
			writeJSON(w, http.StatusOK, overrideBody{
				Surfaces: o.Surfaces, Capabilities: o.Capabilities,
				ContextWindow: o.ContextWindow,
			})
			return
		}
	}
	writeError(w, http.StatusNotFound, "no override for "+providerID+"/"+modelID)
}

func (s *Server) handlePutOverride(w http.ResponseWriter, r *http.Request) {
	providerID, modelID := r.PathValue("provider"), r.PathValue("model")
	// Checked rather than trusted: model_overrides cascades on providers, so
	// a row for an unknown provider would be accepted here and vanish later
	// with nothing to explain it.
	if !s.providerExists(r.Context(), w, providerID) {
		return
	}
	var body overrideBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.deps.DB.PutModelOverride(r.Context(), store.ModelOverride{
		ProviderID: providerID, ModelID: modelID,
		Surfaces: body.Surfaces, Capabilities: body.Capabilities,
		ContextWindow: body.ContextWindow,
	}); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.rebuildCatalog(r.Context())
	writeJSON(w, http.StatusOK, body)
}

func (s *Server) handleDeleteOverride(w http.ResponseWriter, r *http.Request) {
	providerID, modelID := r.PathValue("provider"), r.PathValue("model")
	if err := s.deps.DB.DeleteModelOverride(r.Context(), providerID, modelID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.rebuildCatalog(r.Context())
	w.WriteHeader(http.StatusNoContent)
}

// rebuildCatalog folds the write into the merged snapshot the router reads.
// Without it an override sits in a table nothing consults until an unrelated
// worker next rebuilds, which is up to a discovery interval away.
func (s *Server) rebuildCatalog(ctx context.Context) {
	if s.deps.Catalog != nil {
		_ = s.deps.Catalog.Rebuild(ctx)
	}
}

func joinFields(fields []string) string {
	out := ""
	for i, f := range fields {
		if i > 0 {
			out += ", "
		}
		out += f
	}
	return out
}
