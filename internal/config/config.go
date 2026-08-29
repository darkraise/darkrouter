// Package config loads darkrouter.yaml, validates it, and hot-reloads it.
package config

import "time"

type Config struct {
	Server    ServerConfig     `yaml:"server"`
	Providers []ProviderConfig `yaml:"providers"`
	// Aliases map a friendly name to an ordered fallback chain. Order is the
	// chain order, so a map of slices is the right shape: the values are
	// ordered even though the keys are not.
	Aliases map[string][]string `yaml:"aliases"`
	Policy  PolicyConfig        `yaml:"policy"`
	Log     LogConfig           `yaml:"log"`
	Capture CaptureConfig       `yaml:"capture"`
	Catalog CatalogConfig       `yaml:"catalog"`
	Media   MediaConfig         `yaml:"media"`

	// Warnings are non-fatal findings from validation. They are surfaced on
	// /healthz rather than rejecting the document.
	Warnings []string `yaml:"-"`

	// FileKeys names, in dotted form, every key the document actually carried.
	// It is what lets the config API say a value came from the file rather
	// than from applyDefaults: a non-zero value proves neither, since a
	// default and a written value are indistinguishable once parsed.
	FileKeys []string `yaml:"-"`
}

type ServerConfig struct {
	ProxyListen   string        `yaml:"proxy_listen"`
	AdminListen   string        `yaml:"admin_listen"`
	ProxyToken    string        `yaml:"proxy_token"`
	MaxBodyBytes  int64         `yaml:"max_body_bytes"`
	ShutdownGrace time.Duration `yaml:"shutdown_grace"`
	SSE           SSEConfig     `yaml:"sse"`
}

type SSEConfig struct {
	MaxLineBytes int `yaml:"max_line_bytes"`
	// MaxPrecommitBytes bounds what one attempt may buffer before committing.
	// The first_byte deadline alone is not enough: a provider can emit
	// megabytes inside sixty seconds.
	MaxPrecommitBytes int `yaml:"max_precommit_bytes"`
}

type ProviderConfig struct {
	ID   string `yaml:"id"`
	Kind string `yaml:"kind"`
	// Preset names the shipped catalog entry this provider is an instance of.
	// It is how quirks, surfaces, model traits and the models.dev join key are
	// reached at request time; without it a provider is a base URL and a key.
	Preset   string   `yaml:"preset"`
	BaseURL  string   `yaml:"base_url"`
	APIKey   string   `yaml:"api_key"`
	Priority int      `yaml:"priority"`
	Models   []string `yaml:"models"`
}

type PolicyConfig struct {
	Cooldown CooldownConfig `yaml:"cooldown"`
	Retry    RetryConfig    `yaml:"retry"`
	Timeout  TimeoutConfig  `yaml:"timeout"`
}

// RetryConfig carries only max_attempts: outcome classification is fixed
// rather than configurable, so there is nothing else to tune.
type RetryConfig struct {
	MaxAttempts int `yaml:"max_attempts"`
}

// CooldownConfig governs the circuit breaker. TripAfter counts consecutive
// failures rather than a rate, because a rate needs a window that a homelab's
// traffic never fills. It is a pointer so that an explicit 0 can be rejected
// rather than silently replaced by the default.
type CooldownConfig struct {
	TripAfter *int          `yaml:"trip_after"`
	Max       time.Duration `yaml:"max"`
}

type LogConfig struct {
	Retention time.Duration `yaml:"retention"`
}

// CaptureConfig controls request and response body capture, off by default
// because bodies carry whatever the user sent.
type CaptureConfig struct {
	Bodies    bool          `yaml:"bodies"`
	MaxBytes  int64         `yaml:"max_bytes"`
	Retention time.Duration `yaml:"retention"`
}

type TimeoutConfig struct {
	Connect   time.Duration `yaml:"connect"`
	FirstByte time.Duration `yaml:"first_byte"`
	Total     time.Duration `yaml:"total"`
	Idle      time.Duration `yaml:"idle"`
}

// RestartOnly names the fields a hot reload cannot apply. A reload changing one
// is accepted with a warning rather than rejected or silently ignored.
//
// max_body_bytes is deliberately absent: the executor reads it from a fresh
// per-request snapshot, so it does hot-reload. connect and first_byte are
// listed because they configure a shared http.Transport built once at startup,
// and the catalog fields for the same reason one step out: each worker captures
// its options struct when it is constructed, and whether a worker starts at all
// is decided there too.
var RestartOnly = []string{
	"server.proxy_listen",
	"server.admin_listen",
	"policy.timeout.connect",
	"policy.timeout.first_byte",
	"catalog.sync_interval",
	"catalog.free_catalog_interval",
	"catalog.free_catalog_url",
	"catalog.free_catalog_sync",
	"catalog.discovery.interval",
	// The adapters map is constructed once at startup and the Gemini adapter
	// captures its fetcher there.
	"media.inline",
}

// CatalogConfig governs the two background workers that keep the model catalog
// current.
type CatalogConfig struct {
	ModelsDevURL string        `yaml:"models_dev_url"`
	SyncInterval time.Duration `yaml:"sync_interval"`
	SyncTimeout  time.Duration `yaml:"sync_timeout"`

	// FreeCatalogURL is the curated free-tier list the import filter reads.
	// Free-tier membership cannot be derived from prices, so it is somebody's
	// hand-maintained list, and staying current with it means re-reading what
	// they publish.
	FreeCatalogURL      string        `yaml:"free_catalog_url"`
	FreeCatalogInterval time.Duration `yaml:"free_catalog_interval"`
	// FreeCatalogSync is a pointer so an explicit false is distinguishable
	// from an absent key. An operator who does not want the gateway reaching
	// GitHub on a schedule turns it off and keeps the catalogue its release
	// shipped with.
	FreeCatalogSync *bool `yaml:"free_catalog_sync"`

	Discovery DiscoveryConfig `yaml:"discovery"`

	// SeedFreeProviders adds a provider on first start for every preset that
	// needs no credential, importing only their free models. A pointer so an
	// explicit false is distinguishable from an absent key, which is what lets
	// the default be on: a gateway that routes nothing until somebody opens
	// the console and clicks through a catalogue of two hundred presets is a
	// gateway that does not work out of the box.
	//
	// Off does not remove anything already seeded. Deleting a seeded provider
	// is how an operator declines one; the seeder records what it has offered
	// and never offers it twice.
	SeedFreeProviders *bool `yaml:"seed_free_providers"`
}

// SeedFreeProvidersEnabled reports whether first-start seeding runs.
func (c CatalogConfig) SeedFreeProvidersEnabled() bool {
	return c.SeedFreeProviders == nil || *c.SeedFreeProviders
}

// FreeCatalogSyncEnabled reports whether the daily refresh runs. Absent means
// on: a frozen catalogue silently drops models an operator can use for free,
// and that failure is invisible where a refused outbound call is not.
func (c CatalogConfig) FreeCatalogSyncEnabled() bool {
	return c.FreeCatalogSync == nil || *c.FreeCatalogSync
}

// MediaConfig governs media the gateway fetches on a client's behalf.
//
// Inlining means Darkrouter issues requests to client-supplied addresses,
// which the fetcher constrains but cannot make risk-free. An operator who does
// not want that outbound traffic needs a way to say so.
type MediaConfig struct {
	// Inline is a pointer so an explicit false is distinguishable from an
	// absent key, which is what lets the default be on.
	Inline *bool `yaml:"inline"`
}

// MediaInline reports the effective setting: absent means on.
func (c *Config) MediaInline() bool {
	return c.Media.Inline == nil || *c.Media.Inline
}

type DiscoveryConfig struct {
	// Enabled is a pointer so an explicit false is distinguishable from an
	// absent key, which is what lets the default be on. Discovery is outbound
	// traffic the gateway initiates on the operator's behalf, so it needs an
	// off switch that is not "delete every provider".
	Enabled  *bool         `yaml:"enabled"`
	Interval time.Duration `yaml:"interval"`
	Timeout  time.Duration `yaml:"timeout"`
	// Concurrency is the cap across the whole discovery fleet, not per
	// provider: forty providers must not open forty connections on boot.
	Concurrency int `yaml:"concurrency"`
}
