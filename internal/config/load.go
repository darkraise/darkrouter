package config

import (
	"fmt"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

var envRef = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// Load reads and parses path. lookup resolves ${ENV} references; pass
// os.LookupEnv in production.
func Load(path string, lookup func(string) (string, bool)) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(data, lookup)
}

func Parse(data []byte, lookup func(string) (string, bool)) (*Config, error) {
	if lookup == nil {
		lookup = func(string) (string, bool) { return "", false }
	}
	var c Config
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true) // unknown keys are a validation failure
	if err := dec.Decode(&c); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}
	// Collected before defaults are applied, from a second pass over the raw
	// document: the struct cannot answer "was this written?" afterwards.
	c.FileKeys = documentKeys(data)
	applyDefaults(&c)
	if err := interpolate(&c, lookup); err != nil {
		return nil, err
	}
	if err := validate(&c); err != nil {
		return nil, err
	}
	return &c, nil
}

func applyDefaults(c *Config) {
	if c.Server.ProxyListen == "" {
		c.Server.ProxyListen = ":8080"
	}
	if c.Server.AdminListen == "" {
		c.Server.AdminListen = ":8081"
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
	if c.Policy.Retry.MaxAttempts == 0 {
		c.Policy.Retry.MaxAttempts = 4
	}
}

// resolve replaces ${VAR} references. It reports the names it could not resolve
// so the caller can decide whether the field was required.
func resolve(s string, lookup func(string) (string, bool)) (string, []string) {
	var missing []string
	out := envRef.ReplaceAllStringFunc(s, func(ref string) string {
		name := ref[2 : len(ref)-1]
		if v, ok := lookup(name); ok {
			return v
		}
		missing = append(missing, name)
		return ""
	})
	return out, missing
}

func interpolate(c *Config, lookup func(string) (string, bool)) error {
	// proxy_token is optional: an unset variable means authentication is off,
	// not a broken config. The shipped example references it.
	if v, missing := resolve(c.Server.ProxyToken, lookup); len(missing) > 0 {
		c.Server.ProxyToken = ""
	} else {
		c.Server.ProxyToken = v
	}
	for i := range c.Providers {
		v, missing := resolve(c.Providers[i].APIKey, lookup)
		if len(missing) > 0 {
			return fmt.Errorf("provider %q: unresolved environment reference %s",
				c.Providers[i].ID, strings.Join(missing, ", "))
		}
		c.Providers[i].APIKey = v
	}
	return nil
}

func validate(c *Config) error {
	// Dereferencing TripAfter is safe: Parse runs applyDefaults first.
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
		return fmt.Errorf("policy.timeout.total (%s) must be at least connect + first_byte (%s)",
			t.Total, t.Connect+t.FirstByte)
	}
	if err := ValidateAliases(c.Aliases); err != nil {
		return err
	}

	seen := make(map[string]bool, len(c.Providers))
	models := make(map[string][]string)
	order := []string{}
	for _, p := range c.Providers {
		if p.ID == "" {
			return fmt.Errorf("provider: id is required")
		}
		if seen[p.ID] {
			return fmt.Errorf("provider %q: duplicate id", p.ID)
		}
		seen[p.ID] = true
		if p.BaseURL == "" {
			return fmt.Errorf("provider %q: base_url is required", p.ID)
		}
		u, err := url.Parse(p.BaseURL)
		if err != nil || !u.IsAbs() {
			return fmt.Errorf("provider %q: base_url must be an absolute URL", p.ID)
		}
		if p.Kind == "" {
			return fmt.Errorf("provider %q: kind is required", p.ID)
		}
		for _, m := range p.Models {
			if _, ok := models[m]; !ok {
				order = append(order, m)
			}
			models[m] = append(models[m], p.ID)
		}
	}
	// Ambiguity is the useful case, not an error: phase 3 turns it into a
	// fallback chain. Name it so the resolution is never a surprise.
	for _, m := range order {
		if ids := models[m]; len(ids) > 1 {
			c.Warnings = append(c.Warnings,
				fmt.Sprintf("model %q is offered by %s; the highest-priority provider wins", m, strings.Join(ids, ", ")))
		}
	}
	return nil
}

// documentKeys flattens a YAML document to the dotted key paths it carries.
// Sequences are leaves: a list's indices are not configuration keys, and the
// config API reports a block like providers: as one source, not one per entry.
func documentKeys(data []byte) []string {
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil
	}
	var out []string
	var walk func(prefix string, node map[string]any)
	walk = func(prefix string, node map[string]any) {
		for k, v := range node {
			path := k
			if prefix != "" {
				path = prefix + "." + k
			}
			out = append(out, path)
			if child, ok := v.(map[string]any); ok {
				walk(path, child)
			}
		}
	}
	walk("", raw)
	sort.Strings(out)
	return out
}

// ValidateAliases applies the shape rules an alias chain must satisfy wherever
// it arrives from. Exported so the admin API enforces the same rules as the
// file rather than a second, drifting copy of them.
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
