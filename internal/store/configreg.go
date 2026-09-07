package store

import (
	"fmt"
	"strconv"
	"time"

	"github.com/darkraise/darkrouter/internal/config"
)

// configField is one stored key. It generalises policyField from the policy
// block to the whole Config, so there is one table describing how every
// setting serialises rather than one per block.
//
// Durations serialise the way time.ParseDuration reads them, not as a
// nanosecond count: an operator reads these in the settings screen and writes
// them back.
type configField struct {
	key string
	get func(*config.Config) string
	set func(*config.Config, string) error
	// validate is the bound this key carries on its own, checked against the
	// Config the setter has already written to. Nil for a key whose only rules
	// are in config.Validate, which is most of them.
	//
	// It runs on both paths, which is the point: a bound the save enforced and
	// the loader did not would let a row into the database that every later
	// start silently accepted.
	validate func(*config.Config) error
}

// maxRetryAttempts bounds policy.retry.max_attempts. Past ten, a failing
// request walks the whole candidate list several times over and a client waits
// minutes for an error it could have had in seconds.
const maxRetryAttempts = 10

var configRegistry = buildConfigRegistry()

func buildConfigRegistry() []configField {
	str := func(key string, ref func(*config.Config) *string) configField {
		return configField{
			key: key,
			get: func(c *config.Config) string { return *ref(c) },
			set: func(c *config.Config, v string) error { *ref(c) = v; return nil },
		}
	}
	duration := func(key string, ref func(*config.Config) *time.Duration) configField {
		return configField{
			key: key,
			get: func(c *config.Config) string { return ref(c).String() },
			set: func(c *config.Config, v string) error {
				d, err := time.ParseDuration(v)
				if err != nil {
					return err
				}
				*ref(c) = d
				return nil
			},
		}
	}
	integer := func(key string, ref func(*config.Config) *int) configField {
		return configField{
			key: key,
			get: func(c *config.Config) string { return strconv.Itoa(*ref(c)) },
			set: func(c *config.Config, v string) error {
				n, err := strconv.Atoi(v)
				if err != nil {
					return err
				}
				*ref(c) = n
				return nil
			},
		}
	}
	integer64 := func(key string, ref func(*config.Config) *int64) configField {
		return configField{
			key: key,
			get: func(c *config.Config) string { return strconv.FormatInt(*ref(c), 10) },
			set: func(c *config.Config, v string) error {
				n, err := strconv.ParseInt(v, 10, 64)
				if err != nil {
					return err
				}
				*ref(c) = n
				return nil
			},
		}
	}
	boolean := func(key string, ref func(*config.Config) *bool) configField {
		return configField{
			key: key,
			get: func(c *config.Config) string { return strconv.FormatBool(*ref(c)) },
			set: func(c *config.Config, v string) error {
				b, err := strconv.ParseBool(v)
				if err != nil {
					return err
				}
				*ref(c) = b
				return nil
			},
		}
	}
	// An optional bool is nil when nothing has set it. A stored row always
	// means "set", so the setter allocates and the getter normalises nil to
	// the compiled default the caller has already applied.
	optBool := func(key string, ref func(*config.Config) **bool) configField {
		return configField{
			key: key,
			get: func(c *config.Config) string {
				if p := *ref(c); p != nil {
					return strconv.FormatBool(*p)
				}
				return "false"
			},
			set: func(c *config.Config, v string) error {
				b, err := strconv.ParseBool(v)
				if err != nil {
					return err
				}
				*ref(c) = &b
				return nil
			},
		}
	}
	optInt := func(key string, ref func(*config.Config) **int) configField {
		return configField{
			key: key,
			get: func(c *config.Config) string {
				if p := *ref(c); p != nil {
					return strconv.Itoa(*p)
				}
				return "0"
			},
			set: func(c *config.Config, v string) error {
				n, err := strconv.Atoi(v)
				if err != nil {
					return err
				}
				*ref(c) = &n
				return nil
			},
		}
	}

	// domain is str for a value an operator writes as a hostname. The stored
	// string is normalised on the way in, so "llm.example.com" reaches
	// validation as the URL it means rather than as a value validation
	// refuses.
	domain := func(key string, ref func(*config.Config) *string) configField {
		f := str(key, ref)
		set := f.set
		f.set = func(c *config.Config, v string) error { return set(c, config.NormalizeDomain(v)) }
		return f
	}

	withValidate := func(f configField, v func(*config.Config) error) configField {
		f.validate = v
		return f
	}

	return []configField{
		domain("server.public_url", func(c *config.Config) *string { return &c.Server.PublicURL }),
		integer64("server.max_body_bytes", func(c *config.Config) *int64 { return &c.Server.MaxBodyBytes }),
		duration("server.shutdown_grace", func(c *config.Config) *time.Duration { return &c.Server.ShutdownGrace }),
		integer("server.sse.max_line_bytes", func(c *config.Config) *int { return &c.Server.SSE.MaxLineBytes }),
		integer("server.sse.max_precommit_bytes", func(c *config.Config) *int { return &c.Server.SSE.MaxPrecommitBytes }),

		optInt("policy.cooldown.trip_after", func(c *config.Config) **int { return &c.Policy.Cooldown.TripAfter }),
		duration("policy.cooldown.max", func(c *config.Config) *time.Duration { return &c.Policy.Cooldown.Max }),
		withValidate(
			integer("policy.retry.max_attempts", func(c *config.Config) *int { return &c.Policy.Retry.MaxAttempts }),
			func(c *config.Config) error {
				if n := c.Policy.Retry.MaxAttempts; n < 1 || n > maxRetryAttempts {
					return fmt.Errorf("policy.retry.max_attempts must be between 1 and %d", maxRetryAttempts)
				}
				return nil
			}),
		duration("policy.timeout.connect", func(c *config.Config) *time.Duration { return &c.Policy.Timeout.Connect }),
		duration("policy.timeout.first_byte", func(c *config.Config) *time.Duration { return &c.Policy.Timeout.FirstByte }),
		duration("policy.timeout.total", func(c *config.Config) *time.Duration { return &c.Policy.Timeout.Total }),
		duration("policy.timeout.idle", func(c *config.Config) *time.Duration { return &c.Policy.Timeout.Idle }),

		duration("log.retention", func(c *config.Config) *time.Duration { return &c.Log.Retention }),

		boolean("capture.bodies", func(c *config.Config) *bool { return &c.Capture.Bodies }),
		integer64("capture.max_bytes", func(c *config.Config) *int64 { return &c.Capture.MaxBytes }),
		duration("capture.retention", func(c *config.Config) *time.Duration { return &c.Capture.Retention }),

		str("catalog.models_dev_url", func(c *config.Config) *string { return &c.Catalog.ModelsDevURL }),
		duration("catalog.sync_interval", func(c *config.Config) *time.Duration { return &c.Catalog.SyncInterval }),
		duration("catalog.sync_timeout", func(c *config.Config) *time.Duration { return &c.Catalog.SyncTimeout }),
		str("catalog.free_catalog_url", func(c *config.Config) *string { return &c.Catalog.FreeCatalogURL }),
		duration("catalog.free_catalog_interval", func(c *config.Config) *time.Duration { return &c.Catalog.FreeCatalogInterval }),
		optBool("catalog.free_catalog_sync", func(c *config.Config) **bool { return &c.Catalog.FreeCatalogSync }),
		str("catalog.litellm_url", func(c *config.Config) *string { return &c.Catalog.LiteLLMURL }),
		duration("catalog.litellm_interval", func(c *config.Config) *time.Duration { return &c.Catalog.LiteLLMInterval }),
		optBool("catalog.litellm_sync", func(c *config.Config) **bool { return &c.Catalog.LiteLLMSync }),
		optBool("catalog.seed_free_providers", func(c *config.Config) **bool { return &c.Catalog.SeedFreeProviders }),
		optBool("catalog.discovery.enabled", func(c *config.Config) **bool { return &c.Catalog.Discovery.Enabled }),
		duration("catalog.discovery.interval", func(c *config.Config) *time.Duration { return &c.Catalog.Discovery.Interval }),
		duration("catalog.discovery.timeout", func(c *config.Config) *time.Duration { return &c.Catalog.Discovery.Timeout }),
		integer("catalog.discovery.concurrency", func(c *config.Config) *int { return &c.Catalog.Discovery.Concurrency }),

		optBool("media.inline", func(c *config.Config) **bool { return &c.Media.Inline }),
		optBool("playground.save_conversations", func(c *config.Config) **bool { return &c.Playground.SaveConversations }),
	}
}

var configByKey = func() map[string]configField {
	m := make(map[string]configField, len(configRegistry))
	for _, f := range configRegistry {
		m[f.key] = f
	}
	return m
}()

// ConfigKeys lists every stored key, in registry order.
func ConfigKeys() []string {
	out := make([]string, 0, len(configRegistry))
	for _, f := range configRegistry {
		out = append(out, f.key)
	}
	return out
}

func ConfigKeyKnown(key string) bool { _, ok := configByKey[key]; return ok }

// ConfigRowsFor serialises every key, whether or not it differs from the
// default. Callers that only want the differences compare against a defaulted
// Config themselves; the reconciliation pass is the one that does.
func ConfigRowsFor(c *config.Config) map[string]string {
	out := make(map[string]string, len(configRegistry))
	for _, f := range configRegistry {
		out[f.key] = f.get(c)
	}
	return out
}

// ApplyConfigRows overlays stored rows onto c, one key at a time. A row this
// binary does not know is ignored, and a row it cannot parse leaves the
// compiled default in place and returns a warning naming it.
//
// Per-key rather than all-or-nothing: the file era could reject a whole
// document because an operator could edit the file, and a stored row inside a
// read-only container cannot be edited without the sqlite CLI.
func ApplyConfigRows(c *config.Config, rows map[string]string) []string {
	var warnings []string
	for _, f := range configRegistry {
		v, ok := rows[f.key]
		if !ok {
			continue
		}
		// Judged on a copy. The setter has already landed the value by the
		// time a validator can look at it, and putting the old one back
		// through the setter would turn a nil *bool into a pointer to false.
		// A shallow copy is enough: every setter writes a scalar or a freshly
		// allocated pointer, so it shares nothing with the original.
		scratch := *c
		if err := f.set(&scratch, v); err != nil {
			warnings = append(warnings,
				fmt.Sprintf("stored %s is unusable (%v); using the default", f.key, err))
			continue
		}
		if f.validate != nil {
			if err := f.validate(&scratch); err != nil {
				warnings = append(warnings,
					fmt.Sprintf("stored %s is unusable (%v); using the default", f.key, err))
				continue
			}
		}
		*c = scratch
	}
	return warnings
}
