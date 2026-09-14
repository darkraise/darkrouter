package admin

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

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
	// Lets a save name the exact table it was read against: PUT echoes this
	// back as If-Match, and a table another admin has since changed answers
	// 409 rather than silently taking the edit.
	w.Header().Set("ETag", `"`+config.AliasesRevision(aliases)+`"`)
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
	// Not nil: the write path reads nil as "leave the alias table alone", and
	// an operator who deleted the last chain meant the opposite.
	if aliases == nil {
		aliases = map[string][]string{}
	}
	patch := config.Patch{Aliases: aliases}
	// Optional: a caller that never read the ETag gets today's behaviour
	// rather than a refusal it has no way to satisfy.
	if match := strings.Trim(r.Header.Get("If-Match"), `"`); match != "" {
		patch.AliasesRevision = &match
	}
	s.commitConfig(w, r, patch)
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
	s.commitConfig(w, r, config.Patch{Set: policyPatch(&body)})
}

// policyPatch turns the policy endpoint's shape into registry keys. It is the
// whole of what makes that endpoint a view rather than a second write path: a
// field it does not mention is untouched, and one it mentions as empty is a
// reset, which is what the console's "clear the box" has always meant.
func policyPatch(p *policyWrite) map[string]string {
	set := map[string]string{}
	if p.Cooldown != nil {
		if p.Cooldown.TripAfter != nil {
			set["policy.cooldown.trip_after"] = strconv.Itoa(*p.Cooldown.TripAfter)
		}
		if p.Cooldown.Max != nil {
			set["policy.cooldown.max"] = *p.Cooldown.Max
		}
	}
	if p.Retry != nil && p.Retry.MaxAttempts != nil {
		set["policy.retry.max_attempts"] = strconv.Itoa(*p.Retry.MaxAttempts)
	}
	if p.Timeout != nil {
		for key, v := range map[string]*string{
			"policy.timeout.connect":    p.Timeout.Connect,
			"policy.timeout.first_byte": p.Timeout.FirstByte,
			"policy.timeout.total":      p.Timeout.Total,
			"policy.timeout.idle":       p.Timeout.Idle,
		} {
			if v != nil {
				set[key] = *v
			}
		}
	}
	return set
}

// overrideBody is the wire shape both ways. Every field is omitempty: an
// override sets only what it sets, and a null for the rest would read as
// "cleared" rather than "not overridden".
type overrideBody struct {
	Surfaces      []string                 `json:"surfaces,omitempty"`
	Capabilities  *store.ModelCapabilities `json:"capabilities,omitempty"`
	ContextWindow *int                     `json:"context_window,omitempty"`
}

// overrideView is the GET shape: the override plus the catalog's own
// capabilities for this (provider, model). The stored capabilities are three
// plain bools, so an editor that changes one has to fill in the other two, and
// the model list folds providers together and cannot say what this one
// serves. PUT refuses the extra field as unknown.
type overrideView struct {
	overrideBody
	CatalogCapabilities *store.ModelCapabilities `json:"catalog_capabilities,omitempty"`
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
	view := overrideView{CatalogCapabilities: s.catalogCapabilities(providerID, modelID)}
	for _, o := range rows {
		if o.ProviderID == providerID && o.ModelID == modelID {
			view.overrideBody = overrideBody{
				Surfaces: o.Surfaces, Capabilities: o.Capabilities,
				ContextWindow: o.ContextWindow,
			}
			break
		}
	}
	// An absent override is the ordinary state of most catalog rows, so it
	// is a body without override fields rather than a 404: browsers log
	// every 404 as an error, and the console opens this for any model an
	// operator inspects.
	writeJSON(w, http.StatusOK, view)
}

// catalogCapabilities is what the merged catalog holds for one (provider,
// model), or nil when it holds nothing. Under a capabilities override it is
// the override itself, since the override wins the merge.
func (s *Server) catalogCapabilities(providerID, modelID string) *store.ModelCapabilities {
	if s.deps.Catalog == nil {
		return nil
	}
	m, ok := s.deps.Catalog.Snapshot().Lookup(providerID, modelID)
	if !ok {
		return nil
	}
	return &store.ModelCapabilities{
		Tools: m.Capabilities.Tools, Vision: m.Capabilities.Vision,
		Reasoning: m.Capabilities.Reasoning,
	}
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
	if err := s.rebuildCatalog(afterCommit(r)); err != nil {
		writeRoutingNotUpdated(w)
		return
	}
	writeJSON(w, http.StatusOK, body)
}

func (s *Server) handleDeleteOverride(w http.ResponseWriter, r *http.Request) {
	providerID, modelID := r.PathValue("provider"), r.PathValue("model")
	if err := s.deps.DB.DeleteModelOverride(r.Context(), providerID, modelID); err != nil {
		writeStoreError(w, r, err)
		return
	}
	if err := s.rebuildCatalog(afterCommit(r)); err != nil {
		writeRoutingNotUpdated(w)
		return
	}
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
func (s *Server) rebuildCatalog(ctx context.Context) error {
	if s.deps.Catalog == nil {
		return nil
	}
	if err := s.deps.Catalog.Rebuild(ctx); err != nil {
		slog.Error("catalog rebuild after a committed change failed", "err", err)
		return errRoutingNotUpdated
	}
	return nil
}
