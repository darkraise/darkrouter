// Package provider exposes configured upstreams to the router.
//
// Source is an interface from Phase 1 so that Phase 2 can swap the YAML
// implementation for a SQLite one without touching consumers.
package provider

import (
	"context"
	"hash/fnv"
	"sort"

	"github.com/darkraise/darkrouter/internal/config"
)

type Provider struct {
	ID      string
	Kind    string
	BaseURL string
	APIKey  string
	// KeyID identifies the credential row the APIKey came from. The circuit
	// breaker is keyed on (provider_id, key_id, model), so health recorded in
	// phase 2 stays valid once phase 3 starts choosing among credentials.
	// YAMLSource leaves it empty: config credentials have no row.
	KeyID    string
	Priority int
	Models   []string
}

type Source interface {
	Providers(context.Context) ([]Provider, error)
	Revision() uint64
}

type YAMLSource struct {
	store *config.Store
}

func NewYAMLSource(s *config.Store) *YAMLSource { return &YAMLSource{store: s} }

func (s *YAMLSource) Providers(context.Context) ([]Provider, error) {
	cfg := s.store.Current()
	out := make([]Provider, 0, len(cfg.Providers))
	for _, p := range cfg.Providers {
		out = append(out, Provider{
			ID: p.ID, Kind: p.Kind, BaseURL: p.BaseURL,
			APIKey: p.APIKey, Priority: p.Priority, Models: p.Models,
		})
	}
	return out, nil
}

// Revision changes when the provider set changes, so callers can cache.
func (s *YAMLSource) Revision() uint64 {
	h := fnv.New64a()
	for _, p := range s.store.Current().Providers {
		_, _ = h.Write([]byte(p.ID))
		_, _ = h.Write([]byte(p.BaseURL))
		for _, m := range p.Models {
			_, _ = h.Write([]byte(m))
		}
	}
	return h.Sum64()
}

// Resolve returns the provider serving model, ordered by priority descending
// then declaration order. Phase 3 replaces this with an ordered candidate list;
// Phase 1 takes the first match.
func Resolve(ps []Provider, model string) (Provider, bool) {
	idx := make([]int, 0, len(ps))
	for i, p := range ps {
		for _, m := range p.Models {
			if m == model {
				idx = append(idx, i)
				break
			}
		}
	}
	if len(idx) == 0 {
		return Provider{}, false
	}
	sort.SliceStable(idx, func(a, b int) bool {
		return ps[idx[a]].Priority > ps[idx[b]].Priority
	})
	return ps[idx[0]], true
}

var _ Source = (*YAMLSource)(nil)
