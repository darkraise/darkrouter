// Package catalog describes what each provider offers. Phase 3 defines the
// narrow Reader the router needs; phase 6 supplies the real implementation
// backed by models.dev sync and live discovery.
package catalog

import (
	"github.com/darkraise/darkrouter/internal/ir"
	"github.com/darkraise/darkrouter/internal/provider"
)

// Source records where a model's metadata came from. It matters because
// inferred metadata is a guess, and the router treats a guess differently from
// a fact.
type Source string

const (
	SourceModelsDev  Source = "models_dev"
	SourceDiscovered Source = "discovered"
	SourceInferred   Source = "inferred"
	SourceOverride   Source = "override"
)

// Capabilities is what a model can do. Known distinguishes "we checked and it
// cannot" from "we never found out".
type Capabilities struct {
	Tools     bool
	Vision    bool
	Reasoning bool
	Known     bool
}

// Satisfies reports whether these capabilities meet a requirement.
//
// Unknown capabilities satisfy everything, per master design §6.4: a provider's
// own error is clearer than Darkrouter silently refusing to route, and
// hard-filtering on guessed metadata would make every local model unroutable
// for agentic traffic. The router records a warning instead.
func (c Capabilities) Satisfies(need Capabilities) bool {
	if !c.Known {
		return true
	}
	if need.Tools && !c.Tools {
		return false
	}
	if need.Vision && !c.Vision {
		return false
	}
	if need.Reasoning && !c.Reasoning {
		return false
	}
	return true
}

// Model is one model as offered by one provider.
type Model struct {
	ProviderID   string
	ModelID      string
	Publisher    string
	Surfaces     []ir.Surface
	Capabilities Capabilities
	Source       Source
}

func (m Model) DeclaresSurface(s ir.Surface) bool {
	for _, have := range m.Surfaces {
		if have == s {
			return true
		}
	}
	return false
}

// Inferred reports whether this model's metadata is a guess. The router admits
// these and warns.
func (m Model) Inferred() bool { return m.Source == SourceInferred }

// Reader is the narrow view the router needs. Defining it here rather than
// importing phase 6's package is what lets phase 3 be written first.
type Reader interface {
	// Lookup returns the model as offered by one provider.
	Lookup(providerID, modelID string) (Model, bool)
	// Offering returns the provider ids offering modelID, in the order the
	// provider set gave them — which is priority order.
	Offering(modelID string) []string
}

type providerReader struct {
	byProvider map[string]map[string]Model
	offering   map[string][]string
}

// FromProviders builds a Reader over the configured fleet. Phase 3 has no
// catalog tables, so every model declares only the llm surface and its
// capabilities are inferred.
func FromProviders(ps []provider.Provider) Reader {
	r := &providerReader{
		byProvider: make(map[string]map[string]Model, len(ps)),
		offering:   make(map[string][]string),
	}
	for _, p := range ps {
		models := make(map[string]Model, len(p.Models))
		for _, id := range p.Models {
			models[id] = Model{
				ProviderID: p.ID,
				ModelID:    id,
				Surfaces:   []ir.Surface{ir.SurfaceLLM},
				Source:     SourceInferred,
			}
			r.offering[id] = append(r.offering[id], p.ID)
		}
		r.byProvider[p.ID] = models
	}
	return r
}

func (r *providerReader) Lookup(providerID, modelID string) (Model, bool) {
	models, ok := r.byProvider[providerID]
	if !ok {
		return Model{}, false
	}
	m, ok := models[modelID]
	return m, ok
}

func (r *providerReader) Offering(modelID string) []string {
	return r.offering[modelID]
}
