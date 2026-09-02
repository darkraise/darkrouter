package admin

import (
	"context"
	"net/http"

	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/ir"
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
	if !decodeJSON(w, r, 64<<10, &aliases) {
		return
	}
	if aliases == nil {
		aliases = map[string][]string{}
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
		internalError(w, r, err)
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
	if !decodeJSON(w, r, 16<<10, &body) {
		return
	}
	if cold := restartOnlyIn(&body); len(cold) > 0 {
		writeError(w, http.StatusBadRequest,
			joinFields(cold)+" takes effect on restart and cannot be written here")
		return
	}
	next, err := s.mergedPolicy(&body)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.deps.DB.PutPolicy(r.Context(), next); err != nil {
		internalError(w, r, err)
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

// overrideBody is the wire shape both ways. Every field is omitempty: an
// override sets only what it sets, and a null for the rest would read as
// "cleared" rather than "not overridden".
type overrideBody struct {
	Surfaces      []string                 `json:"surfaces,omitempty"`
	Capabilities  *store.ModelCapabilities `json:"capabilities,omitempty"`
	ContextWindow *int                     `json:"context_window,omitempty"`
}

// validSurfaces is the closed surface vocabulary an override may name.
var validSurfaces = map[string]bool{
	string(ir.SurfaceLLM): true, string(ir.SurfaceEmbedding): true,
	string(ir.SurfaceImage): true, string(ir.SurfaceTTS): true,
	string(ir.SurfaceSTT): true, string(ir.SurfaceRerank): true,
	string(ir.SurfaceModeration): true,
}

func (s *Server) handleGetOverride(w http.ResponseWriter, r *http.Request) {
	providerID, modelID := r.PathValue("provider"), r.PathValue("model")
	rows, err := s.deps.DB.ModelOverrides(r.Context())
	if err != nil {
		internalError(w, r, err)
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
	if !decodeJSON(w, r, 16<<10, &body) {
		return
	}
	for _, sf := range body.Surfaces {
		if !validSurfaces[sf] {
			writeError(w, http.StatusBadRequest, "unknown surface "+sf)
			return
		}
	}
	if body.ContextWindow != nil && *body.ContextWindow <= 0 {
		writeError(w, http.StatusBadRequest, "context_window must be positive")
		return
	}
	if err := s.deps.DB.PutModelOverride(r.Context(), store.ModelOverride{
		ProviderID: providerID, ModelID: modelID,
		Surfaces: body.Surfaces, Capabilities: body.Capabilities,
		ContextWindow: body.ContextWindow,
	}); err != nil {
		internalError(w, r, err)
		return
	}
	s.rebuildCatalog(afterCommit(r))
	writeJSON(w, http.StatusOK, body)
}

func (s *Server) handleDeleteOverride(w http.ResponseWriter, r *http.Request) {
	providerID, modelID := r.PathValue("provider"), r.PathValue("model")
	if err := s.deps.DB.DeleteModelOverride(r.Context(), providerID, modelID); err != nil {
		writeStoreError(w, r, err)
		return
	}
	s.rebuildCatalog(afterCommit(r))
	w.WriteHeader(http.StatusNoContent)
}

// afterCommit is the context a post-write reload runs under. The write has
// landed; a client that disconnects while the router is republishing must
// not leave the gateway serving a provider set that predates a row it holds.
func afterCommit(r *http.Request) context.Context {
	return context.WithoutCancel(r.Context())
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
