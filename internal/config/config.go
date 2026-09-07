// Package config holds the running configuration: its shape, its compiled
// defaults, the rules it must satisfy, and the store that publishes it.
package config

import "time"

type Config struct {
	Server    ServerConfig
	Providers []ProviderConfig
	// Aliases map a friendly name to an ordered fallback chain. Order is the
	// chain order, so a map of slices is the right shape: the values are
	// ordered even though the keys are not.
	Aliases    map[string][]string
	Policy     PolicyConfig
	Log        LogConfig
	Capture    CaptureConfig
	Catalog    CatalogConfig
	Media      MediaConfig
	Playground PlaygroundConfig

	// Warnings are non-fatal findings from validation. They are surfaced on
	// /healthz rather than rejecting the configuration. Broader than Skipped: a
	// restart-pending notice lands here too, and that alone must not make the
	// configuration report invalid.
	Warnings []string
	// Skipped names settings whose stored value could not be used and was
	// reverted to its compiled default -- a bad parse, a broken cross-key
	// rule, or the wholesale fallback. This, not Warnings, is what makes a
	// configuration invalid: the process is running a value the operator did
	// not choose.
	Skipped []string
}

type ServerConfig struct {
	ProxyListen string
	AdminListen string
	// PublicURL is the address clients outside the process reach the gateway
	// at, which the process itself cannot derive: a published container port,
	// a reverse proxy's hostname and a path prefix are all applied after the
	// listener binds. Empty means the console falls back to guessing from the
	// page it was served on.
	PublicURL     string
	ProxyToken    string
	MaxBodyBytes  int64
	ShutdownGrace time.Duration
	SSE           SSEConfig
}

type SSEConfig struct {
	MaxLineBytes int
	// MaxPrecommitBytes bounds what one attempt may buffer before committing.
	// The first_byte deadline alone is not enough: a provider can emit
	// megabytes inside sixty seconds.
	MaxPrecommitBytes int
}

type ProviderConfig struct {
	ID   string
	Kind string
	// Preset names the shipped catalog entry this provider is an instance of.
	// It is how quirks, surfaces, model traits and the models.dev join key are
	// reached at request time; without it a provider is a base URL and a key.
	Preset   string
	BaseURL  string
	APIKey   string
	Priority int
	Models   []string
}

type PolicyConfig struct {
	Cooldown CooldownConfig
	Retry    RetryConfig
	Timeout  TimeoutConfig
}

// RetryConfig carries only max_attempts: outcome classification is fixed
// rather than configurable, so there is nothing else to tune.
type RetryConfig struct {
	MaxAttempts int
}

// CooldownConfig governs the circuit breaker. TripAfter counts consecutive
// failures rather than a rate, because a rate needs a window that a homelab's
// traffic never fills. It is a pointer so that an explicit 0 can be rejected
// rather than silently replaced by the default.
type CooldownConfig struct {
	TripAfter *int
	Max       time.Duration
}

type LogConfig struct {
	Retention time.Duration
}

// CaptureConfig controls request and response body capture, off by default
// because bodies carry whatever the user sent.
type CaptureConfig struct {
	Bodies    bool
	MaxBytes  int64
	Retention time.Duration
}

type TimeoutConfig struct {
	Connect   time.Duration
	FirstByte time.Duration
	Total     time.Duration
	Idle      time.Duration
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
var RestartOnly = restartOnlyNames()

// restartOnlyFields pairs each restart-only field with the value a reload
// compares it by. One table rather than two lists, because a name present in
// only one of them is the failure this fixes: the console offered a field as
// hot-reloadable while the process went on using the old value, or the reload
// warned about a field nothing said was cold.
var restartOnlyFields = []struct {
	name string
	// value must return something comparable. A pointer field is normalized,
	// since two reloads produce different pointers to the same bool.
	value func(*Config) any
}{
	{"policy.timeout.connect", func(c *Config) any { return c.Policy.Timeout.Connect }},
	{"policy.timeout.first_byte", func(c *Config) any { return c.Policy.Timeout.FirstByte }},
	{"catalog.models_dev_url", func(c *Config) any { return c.Catalog.ModelsDevURL }},
	{"catalog.sync_interval", func(c *Config) any { return c.Catalog.SyncInterval }},
	{"catalog.sync_timeout", func(c *Config) any { return c.Catalog.SyncTimeout }},
	{"catalog.free_catalog_interval", func(c *Config) any { return c.Catalog.FreeCatalogInterval }},
	{"catalog.free_catalog_url", func(c *Config) any { return c.Catalog.FreeCatalogURL }},
	{"catalog.free_catalog_sync", func(c *Config) any { return optionalBool(c.Catalog.FreeCatalogSync) }},
	{"catalog.litellm_interval", func(c *Config) any { return c.Catalog.LiteLLMInterval }},
	{"catalog.litellm_url", func(c *Config) any { return c.Catalog.LiteLLMURL }},
	{"catalog.litellm_sync", func(c *Config) any { return optionalBool(c.Catalog.LiteLLMSync) }},
	// Whether the free-provider seed runs at all is read once at startup.
	{"catalog.seed_free_providers", func(c *Config) any { return optionalBool(c.Catalog.SeedFreeProviders) }},
	{"catalog.discovery.interval", func(c *Config) any { return c.Catalog.Discovery.Interval }},
	// The sweeper builds its HTTP client from the timeout and sizes its
	// semaphore from the concurrency, both once, when it is constructed.
	{"catalog.discovery.timeout", func(c *Config) any { return c.Catalog.Discovery.Timeout }},
	{"catalog.discovery.concurrency", func(c *Config) any { return c.Catalog.Discovery.Concurrency }},
	// Not just the interval: whether the sweeper is constructed at all is
	// decided once, at startup, from this.
	{"catalog.discovery.enabled", func(c *Config) any { return optionalBool(c.Catalog.Discovery.Enabled) }},
	// The adapters map is constructed once at startup and the Gemini adapter
	// captures its fetcher there.
	{"media.inline", func(c *Config) any { return optionalBool(c.Media.Inline) }},
}

func restartOnlyNames() []string {
	out := make([]string, 0, len(restartOnlyFields))
	for _, f := range restartOnlyFields {
		out = append(out, f.name)
	}
	return out
}

// optionalBool makes an absent key and an explicit value comparable, which
// matters because absent is what the defaults read as "on".
func optionalBool(p *bool) [2]bool {
	if p == nil {
		return [2]bool{false, false}
	}
	return [2]bool{true, *p}
}

// CatalogConfig governs the two background workers that keep the model catalog
// current.
type CatalogConfig struct {
	ModelsDevURL string
	SyncInterval time.Duration
	SyncTimeout  time.Duration

	// FreeCatalogURL is the curated free-tier list the import filter reads.
	// Free-tier membership cannot be derived from prices, so it is somebody's
	// hand-maintained list, and staying current with it means re-reading what
	// they publish.
	FreeCatalogURL      string
	FreeCatalogInterval time.Duration
	// FreeCatalogSync is a pointer so an explicit false is distinguishable
	// from an absent key. An operator who does not want the gateway reaching
	// GitHub on a schedule turns it off and keeps the catalogue its release
	// shipped with.
	FreeCatalogSync *bool

	// LiteLLMURL is the community price index. It prices models models.dev
	// does not cover, and it is joined in memory rather than stored, so the
	// only way to stay current with a rate change is to re-read it.
	LiteLLMURL      string
	LiteLLMInterval time.Duration
	// LiteLLMSync is a pointer so an explicit false is distinguishable from an
	// absent key. An operator who does not want the gateway reaching GitHub on
	// a schedule turns it off and prices only from models.dev.
	LiteLLMSync *bool

	Discovery DiscoveryConfig

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
	SeedFreeProviders *bool
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

// LiteLLMSyncEnabled reports whether the daily price refresh runs. Absent
// means on: without it every model models.dev does not cover bills as
// unpriced, which is invisible where a refused outbound call is not.
func (c CatalogConfig) LiteLLMSyncEnabled() bool {
	return c.LiteLLMSync == nil || *c.LiteLLMSync
}

// MediaConfig governs media the gateway fetches on a client's behalf.
//
// Inlining means Darkrouter issues requests to client-supplied addresses,
// which the fetcher constrains but cannot make risk-free. An operator who does
// not want that outbound traffic needs a way to say so.
type MediaConfig struct {
	// Inline is a pointer so an explicit false is distinguishable from an
	// absent key, which is what lets the default be on.
	Inline *bool
}

// MediaInline reports the effective setting: absent means on.
func (c *Config) MediaInline() bool {
	return c.Media.Inline == nil || *c.Media.Inline
}

// PlaygroundConfig governs what the console's playground is allowed to keep.
//
// It is the operator's own typing rather than traffic passing through the
// gateway, which is why it is a separate key from capture.bodies rather than
// covered by it.
type PlaygroundConfig struct {
	// SaveConversations is a pointer for the same reason Discovery.Enabled is:
	// the default is on, so an explicit false has to be distinguishable from a
	// key with no stored row.
	SaveConversations *bool
}

// SaveConversations reports the effective setting: absent means on.
func (c *Config) SaveConversations() bool {
	return c.Playground.SaveConversations == nil || *c.Playground.SaveConversations
}

type DiscoveryConfig struct {
	// Enabled is a pointer so an explicit false is distinguishable from an
	// absent key, which is what lets the default be on. Discovery is outbound
	// traffic the gateway initiates on the operator's behalf, so it needs an
	// off switch that is not "delete every provider".
	Enabled  *bool
	Interval time.Duration
	Timeout  time.Duration
	// Concurrency is the cap across the whole discovery fleet, not per
	// provider: forty providers must not open forty connections on boot.
	Concurrency int
}
