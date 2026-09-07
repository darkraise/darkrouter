// Package providertest supplies a fixed provider set to tests that need one.
//
// It replaces provider.YAMLSource, which read config.Config.Providers -- a
// field with no producer since the configuration moved into the database.
package providertest

import (
	"context"
	"hash/fnv"

	"github.com/darkraise/darkrouter/internal/provider"
)

// Source is a provider.Source over a fixed set.
type Source struct {
	providers []provider.Provider
}

func NewSource(ps ...provider.Provider) *Source { return &Source{providers: ps} }

func (s *Source) Providers(context.Context) ([]provider.Provider, error) {
	return s.providers, nil
}

// Revision changes when the provider set changes, so a caller that caches on
// it behaves the way it does against the SQL source.
func (s *Source) Revision() uint64 {
	h := fnv.New64a()
	for _, p := range s.providers {
		_, _ = h.Write([]byte(p.ID))
		_, _ = h.Write([]byte(p.BaseURL))
		for _, m := range p.Models {
			_, _ = h.Write([]byte(m))
		}
	}
	return h.Sum64()
}

var _ provider.Source = (*Source)(nil)

// Keyed builds a provider with one enabled credential, which is the shape
// every test that used the retired YAML source had: a config provider carried
// exactly one key and no row, so its credential id was empty.
func Keyed(id, kind, baseURL, secret string, models ...string) provider.Provider {
	return provider.Provider{
		ID: id, Kind: kind, BaseURL: baseURL, Models: models,
		Credentials: []provider.Credential{{Secret: secret, Enabled: true}},
	}
}
