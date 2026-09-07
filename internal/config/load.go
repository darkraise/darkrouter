package config

import (
	"fmt"
	"net/url"
	"strings"
	"time"
)

// ApplyDefaults fills every unset field with its compiled default. The
// database loader starts from a zero Config and calls this before overlaying
// stored rows, which is what makes an absent row mean "the default".
func ApplyDefaults(c *Config) { applyDefaults(c) }

func applyDefaults(c *Config) {
	if c.Server.ProxyListen == "" {
		c.Server.ProxyListen = ":18080"
	}
	if c.Server.AdminListen == "" {
		c.Server.AdminListen = ":18081"
	}
	if c.Server.MaxBodyBytes == 0 {
		c.Server.MaxBodyBytes = 33554432
	}
	if c.Server.ShutdownGrace == 0 {
		c.Server.ShutdownGrace = 10 * time.Second
	}
	if c.Server.SSE.MaxLineBytes == 0 {
		c.Server.SSE.MaxLineBytes = 1048576
	}
	if c.Server.SSE.MaxPrecommitBytes == 0 {
		c.Server.SSE.MaxPrecommitBytes = 1048576
	}
	if c.Policy.Timeout.Connect == 0 {
		c.Policy.Timeout.Connect = 10 * time.Second
	}
	if c.Policy.Timeout.FirstByte == 0 {
		c.Policy.Timeout.FirstByte = 60 * time.Second
	}
	if c.Policy.Timeout.Total == 0 {
		c.Policy.Timeout.Total = 10 * time.Minute
	}
	if c.Policy.Timeout.Idle == 0 {
		c.Policy.Timeout.Idle = 120 * time.Second
	}
	if c.Policy.Cooldown.TripAfter == nil {
		n := 3
		c.Policy.Cooldown.TripAfter = &n
	}
	if c.Policy.Cooldown.Max == 0 {
		c.Policy.Cooldown.Max = 15 * time.Minute
	}
	if c.Log.Retention == 0 {
		c.Log.Retention = 720 * time.Hour
	}
	if c.Capture.MaxBytes == 0 {
		c.Capture.MaxBytes = 256000
	}
	if c.Capture.Retention == 0 {
		c.Capture.Retention = 72 * time.Hour
	}
	if c.Catalog.ModelsDevURL == "" {
		c.Catalog.ModelsDevURL = "https://models.dev/api.json"
	}
	if c.Catalog.SyncInterval == 0 {
		c.Catalog.SyncInterval = 12 * time.Hour
	}
	if c.Catalog.SyncTimeout == 0 {
		c.Catalog.SyncTimeout = 30 * time.Second
	}
	if c.Catalog.FreeCatalogURL == "" {
		c.Catalog.FreeCatalogURL = "https://raw.githubusercontent.com/diegosouzapw/OmniRoute/HEAD/open-sse/config/freeModelCatalog.data.ts"
	}
	if c.Catalog.FreeCatalogInterval == 0 {
		// Daily. The upstream list changes when someone runs a research pass,
		// which is weeks apart.
		c.Catalog.FreeCatalogInterval = 24 * time.Hour
	}
	if c.Catalog.FreeCatalogSync == nil {
		on := true
		c.Catalog.FreeCatalogSync = &on
	}
	if c.Catalog.LiteLLMURL == "" {
		c.Catalog.LiteLLMURL = "https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json"
	}
	if c.Catalog.LiteLLMInterval == 0 {
		// Daily. Rate changes are announced days ahead of taking effect.
		c.Catalog.LiteLLMInterval = 24 * time.Hour
	}
	if c.Catalog.LiteLLMSync == nil {
		on := true
		c.Catalog.LiteLLMSync = &on
	}
	if c.Catalog.SeedFreeProviders == nil {
		on := true
		c.Catalog.SeedFreeProviders = &on
	}
	if c.Catalog.Discovery.Enabled == nil {
		on := true
		c.Catalog.Discovery.Enabled = &on
	}
	if c.Catalog.Discovery.Interval == 0 {
		c.Catalog.Discovery.Interval = 15 * time.Minute
	}
	if c.Catalog.Discovery.Timeout == 0 {
		c.Catalog.Discovery.Timeout = 15 * time.Second
	}
	if c.Catalog.Discovery.Concurrency == 0 {
		c.Catalog.Discovery.Concurrency = 8
	}
	if c.Playground.SaveConversations == nil {
		on := true
		c.Playground.SaveConversations = &on
	}
	if c.Media.Inline == nil {
		on := true
		c.Media.Inline = &on
	}
	if c.Policy.Retry.MaxAttempts == 0 {
		c.Policy.Retry.MaxAttempts = 4
	}
}

// NormalizeDomain is normalizeDomain for a value arriving from outside this
// package. The database loader calls it on a stored server.public_url before
// validation runs: a bare domain is how an operator writes the setting, and
// validate demands an absolute URL, so without this step a stored
// "llm.example.com" would be refused and take the whole configuration down
// with it.
func NormalizeDomain(v string) string { return normalizeDomain(v) }

// normalizeDomain turns a bare domain into the URL the rest of the system
// expects. An operator setting this is naming the address the outside world
// uses, and writes it the way it is spoken -- "llm.example.com", not a scheme
// and a trailing slash -- so the scheme is supplied here rather than demanded
// of them.
//
// https, because a domain reachable from outside this machine is behind
// something terminating TLS, and the one guess that is dangerous to get wrong
// is the one that sends a client's token in the clear. A deployment that
// really is plain HTTP writes the scheme out.
//
// The test for "already a URL" is the separator, not url.Parse: Parse reads
// "llm.example.com:8443" as scheme "llm.example.com" with an opaque body, so
// asking it whether a scheme is present answers yes for a host and a port.
func normalizeDomain(v string) string {
	v = strings.TrimSpace(v)
	if v == "" || strings.Contains(v, "://") {
		return v
	}
	// Anything that cannot be a hostname is left alone for validate to name:
	// silently prefixing "/v1" would turn a wrong value into a valid URL
	// pointing somewhere nobody asked for.
	if strings.HasPrefix(v, "/") {
		return v
	}
	return "https://" + v
}

// RuleError is a validation failure that no single key caused. It carries every
// key that took part, because reverting one of them at load time is a choice
// that has to be made from the whole set rather than guessed from a message.
type RuleError struct {
	Rule string
	Keys []string
	Err  error
}

func (e RuleError) Error() string { return e.Err.Error() }
func (e RuleError) Unwrap() error { return e.Err }

// Validate runs every rule against an assembled Config. The database loader
// needs it, and it is the one validator: the write path calls it too, so a
// value refused on save is never a value a later load would accept.
func Validate(c *Config) error { return validate(c) }

func validate(c *Config) error {
	// Dereferencing TripAfter is safe: every caller runs applyDefaults first.
	if *c.Policy.Cooldown.TripAfter < 1 {
		return fmt.Errorf("policy.cooldown.trip_after must be at least 1")
	}
	if c.Policy.Cooldown.Max <= 0 {
		return fmt.Errorf("policy.cooldown.max must be positive")
	}
	// The daily rollup recomputes yesterday and today, so anything it can
	// still rewrite must still be in the log. Two days is the exact point
	// where pruning can no longer reach a row the rollup would rebuild.
	if c.Log.Retention < 48*time.Hour {
		return fmt.Errorf("log.retention must be at least 48h, got %s", c.Log.Retention)
	}
	if c.Capture.Retention <= 0 {
		return fmt.Errorf("capture.retention must be positive")
	}
	if c.Capture.MaxBytes < 0 {
		return fmt.Errorf("capture.max_bytes must not be negative")
	}
	if c.Policy.Retry.MaxAttempts < 1 {
		return fmt.Errorf("policy.retry.max_attempts must be at least 1")
	}
	if c.Server.ShutdownGrace <= 0 {
		return fmt.Errorf("server.shutdown_grace must be positive")
	}
	if c.Server.PublicURL != "" {
		u, err := url.Parse(c.Server.PublicURL)
		// A scheme-relative or path-only value would be pasted straight into a
		// client's base_url and fail there instead, one layer further from the
		// mistake. A bare domain is not in that group -- normalizeDomain has
		// already turned it into a URL by the time this runs.
		if err != nil || !u.IsAbs() || u.Host == "" {
			return fmt.Errorf("server.public_url must be a domain or an absolute URL, got %q", c.Server.PublicURL)
		}
		if u.RawQuery != "" || u.Fragment != "" {
			return fmt.Errorf("server.public_url must not carry a query or fragment, got %q", c.Server.PublicURL)
		}
	}
	t := c.Policy.Timeout
	for _, d := range []struct {
		name string
		v    time.Duration
	}{
		{"connect", t.Connect}, {"first_byte", t.FirstByte}, {"total", t.Total}, {"idle", t.Idle},
	} {
		if d.v <= 0 {
			return fmt.Errorf("policy.timeout.%s must be positive", d.name)
		}
	}
	// The budget gate refuses to start an attempt unless the remaining total
	// covers connect + first_byte, so a smaller total would start nothing.
	if t.Total < t.Connect+t.FirstByte {
		return RuleError{
			Rule: "timeout budget",
			Keys: []string{"policy.timeout.total", "policy.timeout.connect", "policy.timeout.first_byte"},
			Err: fmt.Errorf("policy.timeout.total (%s) must be at least connect + first_byte (%s)",
				t.Total, t.Connect+t.FirstByte),
		}
	}
	if err := ValidateAliases(c.Aliases); err != nil {
		return err
	}
	return nil
}

// ValidateAliases applies the shape rules an alias chain must satisfy wherever
// it arrives from. Exported so the admin API enforces the same rules the
// loader does rather than a second, drifting copy of them.
func ValidateAliases(aliases map[string][]string) error {
	for name, targets := range aliases {
		if name == "" {
			return fmt.Errorf("alias: name is required")
		}
		if len(targets) == 0 {
			return fmt.Errorf("alias %q: at least one target is required", name)
		}
		for _, tgt := range targets {
			if strings.TrimSpace(tgt) == "" {
				return fmt.Errorf("alias %q: target must not be empty", name)
			}
		}
	}
	return nil
}
