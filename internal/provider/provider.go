// Package provider exposes configured upstreams to the router.
//
// Source is an interface so the SQLite-backed source the gateway runs on and
// the config-backed one tests build are interchangeable to every consumer.
package provider

import (
	"context"
	"hash/fnv"

	"github.com/darkraise/darkrouter/internal/config"
)

// Credential is one usable key for a provider. Secret is plaintext and lives
// only in memory; the store decrypts once at load.
type Credential struct {
	ID      string
	Secret  string
	Enabled bool

	// Kind is static, sigv4, gcp_sa or oauth. It says how to read Secret: a
	// bare key, a service-account document, or a marshalled token.
	Kind string
}

type Provider struct {
	ID      string
	Kind    string
	BaseURL string

	// Preset names the shipped entry this provider was created from, or is
	// empty for an uncatalogued one. It is how quirks, surfaces, model traits
	// and the models.dev join key are reached at request time.
	Preset string
	// AuthStyle is the provider row's override of its preset's style.
	AuthStyle string

	// Credentials are every enabled credential, ordered by id. Credential
	// rotation happens before advancing to the next provider, so the router
	// needs all of them rather than a chosen one.
	Credentials []Credential

	Priority int
	Models   []string

	// Region, Project and Location are the endpoint properties bedrock and
	// vertex need. They have been columns on providers since migration 0001
	// and, until phase 8, nothing read them.
	Region   string
	Project  string
	Location string

	// FreeModelsOnly narrows what a discovery sweep imports to the models it
	// can show are free. It is carried on the provider because the sweep is
	// the only thing that reads it: routing never consults it, so a paid model
	// already in the catalogue stays routable until the next sweep drops it.
	FreeModelsOnly bool
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
			ID: p.ID, Kind: p.Kind, BaseURL: p.BaseURL, Preset: p.Preset,
			// A config credential has no database row, so its id is empty. The
			// breaker keys on that empty id, which is what phase 2 already did.
			Credentials: []Credential{{ID: "", Secret: p.APIKey, Enabled: true}},
			Priority:    p.Priority, Models: p.Models,
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

var _ Source = (*YAMLSource)(nil)
