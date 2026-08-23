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

	// Warnings are non-fatal findings from validation. They are surfaced on
	// /healthz rather than rejecting the document.
	Warnings []string `yaml:"-"`
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
// listed because they configure a shared http.Transport built once at startup.
var RestartOnly = []string{
	"server.proxy_listen",
	"server.admin_listen",
	"policy.timeout.connect",
	"policy.timeout.first_byte",
}

// CatalogConfig governs the two background workers that keep the model catalog
// current.
type CatalogConfig struct {
	ModelsDevURL string        `yaml:"models_dev_url"`
	SyncInterval time.Duration `yaml:"sync_interval"`
	SyncTimeout  time.Duration `yaml:"sync_timeout"`

	Discovery DiscoveryConfig `yaml:"discovery"`
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
