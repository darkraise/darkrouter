// Package provider exposes configured upstreams to the router.
//
// Source is an interface so the SQLite-backed source the gateway runs on and
// the fixed one tests build are interchangeable to every consumer.
package provider

import "context"

// Credential is one usable key for a provider. Secret is plaintext and lives
// only in memory; the store decrypts once at load.
type Credential struct {
	ID      string
	Secret  string
	Enabled bool

	// Kind is static, sigv4, gcp_sa or oauth. It says how to read Secret: a
	// bare key, a service-account document, or a marshalled token.
	Kind string

	// AccountID fills the {account_id} placeholder a provider's base URL may
	// carry. It is per credential because it belongs to the same account the
	// secret does -- one provider row can hold keys from two Cloudflare
	// accounts, and each reaches a different endpoint.
	AccountID string
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

	// AllowUnsanctionedFree carries the operator's decision to accept free
	// models whose terms grade `avoid` — largely access the vendor has not
	// sanctioned. It rides on the provider because both the discovery sweep
	// and routing consult it: without the opt-in such a model is neither
	// imported nor routed to.
	AllowUnsanctionedFree bool
}

type Source interface {
	Providers(context.Context) ([]Provider, error)
	Revision() uint64
}
