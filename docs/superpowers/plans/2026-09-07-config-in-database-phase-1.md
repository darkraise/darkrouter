# Configuration in the Database — Phase 1 (flag day) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

> **Implementer assignments:** each task names its implementer agent in an
> `**Implementer:**` line. When executing with
> superpowers:subagent-driven-development, REQUIRED SUB-SKILL:
> dcc-superpower-companions:dispatching-tiered-implementers. Under
> superpowers:executing-plans these lines are inert; ignore them.

**Goal:** Make the database the only place darkrouter reads its configuration from, with a small bootstrap set in the environment, and delete `darkrouter.yaml` support entirely.

**Architecture:** A typed registry maps each of 32 config keys to a getter, setter and serialiser over `*config.Config`, backed by rows in the existing `settings` table. `config.Store` stops reading a file and takes an injected loader, which `main` fills with a database read. Listen addresses, the master key and the shared proxy token come from the environment instead. Loading tolerates a bad row by reverting that key to its compiled default rather than refusing to start.

**Tech Stack:** Go 1.x, SQLite via `internal/store`, `gopkg.in/yaml.v3` (retained for `internal/catalog` presets only), Docker Compose.

**Spec:** `docs/superpowers/specs/2026-09-07-config-in-database-design.md`

## Global Constraints

- Keys keep their dotted block form: `catalog.sync_interval`, never `config.catalog.sync_interval`.
- Reads of the `settings` table filter by registry membership. Never `SELECT *`.
- An absent row means the compiled default. Writing an empty value is a `DELETE`.
- Settings content never fails startup. Structural failures (database will not open, migration fails) still abort.
- `DARKROUTER_PROXY_TOKEN` must keep authenticating after this phase. Losing it opens the gateway (`internal/server/server.go:418-421`).
- Every test must be proven able to fail: break the implementation, watch it go red, restore it.
- English only, in code, comments, commits and tests.
- Commit style: `<type>(<scope>): <subject>`, subject ≤50 chars, imperative, no period.

---

### Task 1: Bootstrap values from the environment

**Files:**
- Create: `internal/config/bootstrap.go`
- Create: `internal/config/bootstrap_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `config.Bootstrap` struct with fields `ProxyListen string`, `AdminListen string`, `ProxyToken string`; `config.BootstrapFrom(lookup func(string) (string, bool)) Bootstrap`.

**Implementer:** dcc-superpower-companions:impl-sonnet-low
**Evaluation:** files 1 - spec 0 - coupling 0 - risk 0 = 1
**Approach:** inline - skip 2: the environment-variable pattern is already established at `cmd/darkrouter/main.go:47,59` and `internal/server/server.go:265`

- [ ] **Step 1: Write the failing test**

```go
package config

import "testing"

func env(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func TestBootstrapFallsBackToDefaults(t *testing.T) {
	b := BootstrapFrom(env(nil))
	if b.ProxyListen != ":18080" || b.AdminListen != ":18081" {
		t.Errorf("listen = %q/%q, want the defaults", b.ProxyListen, b.AdminListen)
	}
	// Empty means proxy authentication is off, which is why a machine that has
	// never set it still starts.
	if b.ProxyToken != "" {
		t.Errorf("ProxyToken = %q, want empty", b.ProxyToken)
	}
}

func TestBootstrapReadsTheEnvironment(t *testing.T) {
	b := BootstrapFrom(env(map[string]string{
		"DARKROUTER_PROXY_LISTEN": "127.0.0.1:9000",
		"DARKROUTER_ADMIN_LISTEN": "127.0.0.1:9001",
		"DARKROUTER_PROXY_TOKEN":  "sekrit",
	}))
	if b.ProxyListen != "127.0.0.1:9000" || b.AdminListen != "127.0.0.1:9001" {
		t.Errorf("listen = %q/%q, want the environment's", b.ProxyListen, b.AdminListen)
	}
	if b.ProxyToken != "sekrit" {
		t.Errorf("ProxyToken = %q, want the environment's", b.ProxyToken)
	}
}

// An operator who exports an empty variable meant "unset", not "listen on the
// port named by the empty string", which would fail to bind.
func TestBootstrapTreatsAnEmptyListenAsUnset(t *testing.T) {
	b := BootstrapFrom(env(map[string]string{"DARKROUTER_PROXY_LISTEN": "  "}))
	if b.ProxyListen != ":18080" {
		t.Errorf("ProxyListen = %q, want the default", b.ProxyListen)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestBootstrap -v`
Expected: FAIL, `undefined: BootstrapFrom`

- [ ] **Step 3: Write minimal implementation**

```go
package config

import "strings"

// Bootstrap is the configuration that cannot live in the database, because it
// is what the process needs before the database is open or in order to be
// reachable at all. Everything else is a row in `settings`.
type Bootstrap struct {
	ProxyListen string
	AdminListen string
	// ProxyToken is the shared inbound secret. It stays an environment
	// variable rather than a stored row because it is a credential, and
	// because the process already reads its other credentials this way.
	ProxyToken string
}

func BootstrapFrom(lookup func(string) (string, bool)) Bootstrap {
	get := func(name, def string) string {
		v, ok := lookup(name)
		if !ok {
			return def
		}
		// An exported-but-empty variable reads as unset. The alternative is
		// binding to the empty string, which fails at listen time with an
		// error that names neither the variable nor this decision.
		if v = strings.TrimSpace(v); v == "" {
			return def
		}
		return v
	}
	return Bootstrap{
		ProxyListen: get("DARKROUTER_PROXY_LISTEN", ":18080"),
		AdminListen: get("DARKROUTER_ADMIN_LISTEN", ":18081"),
		ProxyToken:  get("DARKROUTER_PROXY_TOKEN", ""),
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -run TestBootstrap -v`
Expected: PASS, all three tests

- [ ] **Step 5: Prove the tests can fail**

Change the `ProxyListen` default to `":9999"`, run the tests, confirm `TestBootstrapFallsBackToDefaults` and `TestBootstrapTreatsAnEmptyListenAsUnset` go red, then restore.

- [ ] **Step 6: Commit**

```bash
git add internal/config/bootstrap.go internal/config/bootstrap_test.go
git commit -m "feat(config): read bootstrap values from the environment"
```

---

### Task 2: Name the one cross-key rule in a typed error

**Files:**
- Modify: `internal/config/load.go` (the `validate` function, around line 282)
- Modify: `internal/config/load_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `config.RuleError` with fields `Rule string` and `Keys []string`, implementing `error`; `validate` returns one for the timeout-budget rule.

**Implementer:** dcc-superpower-companions:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 0 - risk 1 = 2
**Approach:** inline - skip 2: a typed error wrapping an existing message is the standard Go shape and the codebase already uses `errors.Is`/`errors.As` in `internal/store/keyring.go:43`

- [ ] **Step 1: Write the failing test**

```go
func TestTimeoutBudgetFailureNamesItsKeys(t *testing.T) {
	// The loader has to know which keys to revert when a stored pair is
	// unusable. A bare error message names no culprit, and reverting the
	// wrong key produces a config the operator did not ask for either.
	_, err := Parse([]byte("policy:\n  timeout:\n    connect: 30s\n    first_byte: 60s\n    total: 40s\n"), nil)
	if err == nil {
		t.Fatal("a total below connect + first_byte must be refused")
	}
	var re RuleError
	if !errors.As(err, &re) {
		t.Fatalf("error is %T, want a RuleError naming the keys", err)
	}
	want := []string{"policy.timeout.total", "policy.timeout.connect", "policy.timeout.first_byte"}
	if !slices.Equal(re.Keys, want) {
		t.Errorf("Keys = %v, want %v", re.Keys, want)
	}
}
```

Add `"errors"` and `"slices"` to the test file's imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestTimeoutBudget -v`
Expected: FAIL, `undefined: RuleError`

- [ ] **Step 3: Write minimal implementation**

In `internal/config/load.go`, add above `validate`:

```go
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
```

Then replace the timeout-budget check inside `validate`:

```go
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -v`
Expected: PASS. The existing timeout tests still pass because `RuleError.Error()` returns the same message they match on.

- [ ] **Step 5: Prove the test can fail**

Drop the `Keys` field from the returned `RuleError`, run the test, confirm it goes red on the `slices.Equal` check, then restore.

- [ ] **Step 6: Commit**

```bash
git add internal/config/load.go internal/config/load_test.go
git commit -m "feat(config): name the keys a cross-key rule failure covers"
```

---

### Task 3: The config registry

**Files:**
- Create: `internal/store/configreg.go`
- Create: `internal/store/configreg_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: `store.ConfigKeys() []string`; `store.ConfigRowsFor(c *config.Config) map[string]string`; `store.ApplyConfigRows(c *config.Config, rows map[string]string) []string` returning one warning string per key it could not apply; `store.ConfigKeyKnown(key string) bool`.

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 0 = 3
**Approach:** inline - skip 2: this generalises the existing `policyFields` registry at `internal/store/configstore.go:105` rather than choosing a new shape

- [ ] **Step 1: Write the failing test**

```go
package store

import (
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/config"
)

// Every key must survive a round trip through the strings the settings table
// stores, or the console shows one value and the process runs another.
func TestConfigRegistryRoundTripsEveryKey(t *testing.T) {
	src := &config.Config{}
	config.ApplyDefaults(src)
	src.Server.PublicURL = "https://llm.example.com"
	src.Catalog.SyncInterval = 7 * time.Hour
	src.Capture.Bodies = true

	rows := ConfigRowsFor(src)
	if len(rows) != len(ConfigKeys()) {
		t.Fatalf("rows = %d, want one per key (%d)", len(rows), len(ConfigKeys()))
	}

	dst := &config.Config{}
	config.ApplyDefaults(dst)
	if warn := ApplyConfigRows(dst, rows); len(warn) > 0 {
		t.Fatalf("round trip warned: %v", warn)
	}
	if dst.Server.PublicURL != src.Server.PublicURL {
		t.Errorf("public_url = %q, want %q", dst.Server.PublicURL, src.Server.PublicURL)
	}
	if dst.Catalog.SyncInterval != src.Catalog.SyncInterval {
		t.Errorf("sync_interval = %v, want %v", dst.Catalog.SyncInterval, src.Catalog.SyncInterval)
	}
	if !dst.Capture.Bodies {
		t.Error("capture.bodies lost its value")
	}
}

// A row an older or newer build wrote is not an override this binary can
// apply, and must not reach the config.
func TestConfigRegistryIgnoresAnUnknownKey(t *testing.T) {
	c := &config.Config{}
	config.ApplyDefaults(c)
	if warn := ApplyConfigRows(c, map[string]string{"catalog.not_a_key": "1"}); len(warn) != 0 {
		t.Errorf("warnings = %v, want none for an unknown key", warn)
	}
	if ConfigKeyKnown("catalog.not_a_key") {
		t.Error("an unknown key must not report as known")
	}
}

// A value that will not parse names itself and leaves the default in place.
// The alternative is a container that will not start over one bad row.
func TestConfigRegistryWarnsAndKeepsTheDefault(t *testing.T) {
	c := &config.Config{}
	config.ApplyDefaults(c)
	warn := ApplyConfigRows(c, map[string]string{"catalog.sync_interval": "not-a-duration"})
	if len(warn) != 1 {
		t.Fatalf("warnings = %v, want exactly one", warn)
	}
	if c.Catalog.SyncInterval != 12*time.Hour {
		t.Errorf("sync_interval = %v, want the default kept", c.Catalog.SyncInterval)
	}
}

func TestConfigRegistryHasNoDuplicateKeys(t *testing.T) {
	seen := map[string]bool{}
	for _, k := range ConfigKeys() {
		if seen[k] {
			t.Errorf("duplicate key %q", k)
		}
		seen[k] = true
	}
	if len(seen) != 32 {
		t.Errorf("registry holds %d keys, want the 32 the spec enumerates", len(seen))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestConfigRegistry -v`
Expected: FAIL, `undefined: ConfigRowsFor`

- [ ] **Step 3: Export the defaults pass**

In `internal/config/load.go`, add:

```go
// ApplyDefaults fills every unset field with its compiled default. The
// database loader starts from a zero Config and calls this before overlaying
// stored rows, which is what makes an absent row mean "the default".
func ApplyDefaults(c *Config) { applyDefaults(c) }
```

- [ ] **Step 4: Write the registry**

Create `internal/store/configreg.go`:

```go
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
}

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

	return []configField{
		str("server.public_url", func(c *config.Config) *string { return &c.Server.PublicURL }),
		integer64("server.max_body_bytes", func(c *config.Config) *int64 { return &c.Server.MaxBodyBytes }),
		duration("server.shutdown_grace", func(c *config.Config) *time.Duration { return &c.Server.ShutdownGrace }),
		integer("server.sse.max_line_bytes", func(c *config.Config) *int { return &c.Server.SSE.MaxLineBytes }),
		integer("server.sse.max_precommit_bytes", func(c *config.Config) *int { return &c.Server.SSE.MaxPrecommitBytes }),

		optInt("policy.cooldown.trip_after", func(c *config.Config) **int { return &c.Policy.Cooldown.TripAfter }),
		duration("policy.cooldown.max", func(c *config.Config) *time.Duration { return &c.Policy.Cooldown.Max }),
		integer("policy.retry.max_attempts", func(c *config.Config) *int { return &c.Policy.Retry.MaxAttempts }),
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
		if err := f.set(c, v); err != nil {
			warnings = append(warnings,
				fmt.Sprintf("stored %s is unusable (%v); using the default", f.key, err))
		}
	}
	return warnings
}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/store/ -run TestConfigRegistry -v`
Expected: PASS, all four tests

- [ ] **Step 6: Prove the tests can fail**

Remove the `catalog.discovery.timeout` entry from the registry, run the tests, confirm `TestConfigRegistryHasNoDuplicateKeys` reports 31 keys, then restore.

- [ ] **Step 7: Commit**

```bash
git add internal/store/configreg.go internal/store/configreg_test.go internal/config/load.go
git commit -m "feat(store): add a registry for every stored config key"
```

---

### Task 4: Load the config from the database

**Files:**
- Create: `internal/store/configload.go`
- Create: `internal/store/configload_test.go`

**Interfaces:**
- Consumes: `store.ApplyConfigRows`, `store.ConfigKeyKnown` (Task 3); `config.RuleError` (Task 2); `config.Bootstrap` (Task 1); `config.ApplyDefaults` (Task 3).
- Produces: `store.LoadConfig(ctx context.Context, d *DB, boot config.Bootstrap) (*config.Config, error)`.

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5
**Approach:** inline - skip 3: the spec fixes the tie-break rule (every key in a failed rule reverts together), so nothing is undecided

- [ ] **Step 1: Write the failing test**

```go
package store

import (
	"context"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/store/storetest"
)

func boot() config.Bootstrap {
	return config.Bootstrap{ProxyListen: ":18080", AdminListen: ":18081"}
}

func TestLoadConfigUsesDefaultsWhenNothingIsStored(t *testing.T) {
	db := storetest.Migrated(t)
	c, err := LoadConfig(context.Background(), db, boot())
	if err != nil {
		t.Fatalf("an empty database must load: %v", err)
	}
	if c.Catalog.SyncInterval != 12*time.Hour {
		t.Errorf("sync_interval = %v, want the default", c.Catalog.SyncInterval)
	}
	if c.Server.ProxyListen != ":18080" {
		t.Errorf("proxy_listen = %q, want the bootstrap value", c.Server.ProxyListen)
	}
}

func TestLoadConfigAppliesAStoredRow(t *testing.T) {
	db := storetest.Migrated(t)
	ctx := context.Background()
	if err := putSetting(ctx, db.Write, "log.retention", "100h"); err != nil {
		t.Fatal(err)
	}
	c, err := LoadConfig(ctx, db, boot())
	if err != nil {
		t.Fatal(err)
	}
	if c.Log.Retention != 100*time.Hour {
		t.Errorf("log.retention = %v, want the stored 100h", c.Log.Retention)
	}
}

// The headline behaviour of this phase: one bad row must not stop the process.
// A crash loop here is fixed only with the sqlite CLI, inside a container that
// runs read_only with every capability dropped.
func TestLoadConfigSurvivesAnUnparseableRow(t *testing.T) {
	db := storetest.Migrated(t)
	ctx := context.Background()
	if err := putSetting(ctx, db.Write, "log.retention", "banana"); err != nil {
		t.Fatal(err)
	}
	c, err := LoadConfig(ctx, db, boot())
	if err != nil {
		t.Fatalf("a bad row must not fail the load: %v", err)
	}
	if c.Log.Retention != 720*time.Hour {
		t.Errorf("log.retention = %v, want the default", c.Log.Retention)
	}
	if len(c.Warnings) == 0 {
		t.Error("a skipped key must warn; silence makes it undiagnosable")
	}
}

// A cross-key rule names no single culprit, so every key in it reverts
// together. Reverting one of the three would produce a config the operator did
// not ask for either, and which one you picked would depend on write order.
func TestLoadConfigRevertsEveryKeyInAFailedRule(t *testing.T) {
	db := storetest.Migrated(t)
	ctx := context.Background()
	for k, v := range map[string]string{
		"policy.timeout.connect":    "30s",
		"policy.timeout.first_byte": "60s",
		"policy.timeout.total":      "40s",
	} {
		if err := putSetting(ctx, db.Write, k, v); err != nil {
			t.Fatal(err)
		}
	}
	c, err := LoadConfig(ctx, db, boot())
	if err != nil {
		t.Fatalf("an unusable pair must not fail the load: %v", err)
	}
	if c.Policy.Timeout.Connect != 10*time.Second ||
		c.Policy.Timeout.FirstByte != 60*time.Second ||
		c.Policy.Timeout.Total != 10*time.Minute {
		t.Errorf("timeouts = %v/%v/%v, want all three back at their defaults",
			c.Policy.Timeout.Connect, c.Policy.Timeout.FirstByte, c.Policy.Timeout.Total)
	}
	if len(c.Warnings) == 0 {
		t.Error("a reverted rule must warn")
	}
}

// Rows belonging to the keyring, the CSRF secret and the import markers share
// this table. Reading them as config would be a bug that only shows up as a
// mystery warning.
func TestLoadConfigIgnoresForeignRows(t *testing.T) {
	db := storetest.Migrated(t)
	ctx := context.Background()
	if err := putSetting(ctx, db.Write, "csrf_secret", "not-a-config-value"); err != nil {
		t.Fatal(err)
	}
	c, err := LoadConfig(ctx, db, boot())
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Warnings) != 0 {
		t.Errorf("warnings = %v, want none for a foreign row", c.Warnings)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestLoadConfig -v`
Expected: FAIL, `undefined: LoadConfig`

- [ ] **Step 3: Write the loader**

Create `internal/store/configload.go`:

```go
package store

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/darkraise/darkrouter/internal/config"
)

// LoadConfig builds the running configuration: compiled defaults, then the
// bootstrap environment, then the stored rows, then validation.
//
// It never fails on settings content. A row that will not parse, or a rule no
// single key broke, reverts the keys involved to their defaults and appends a
// warning. Only a database that cannot be read is an error, because that is
// not something an operator can fix from the console either way.
func LoadConfig(ctx context.Context, d *DB, boot config.Bootstrap) (*config.Config, error) {
	rows, err := configRows(ctx, d)
	if err != nil {
		return nil, err
	}

	build := func(skip map[string]bool) (*config.Config, []string) {
		c := &config.Config{}
		config.ApplyDefaults(c)
		c.Server.ProxyListen = boot.ProxyListen
		c.Server.AdminListen = boot.AdminListen
		c.Server.ProxyToken = boot.ProxyToken
		use := make(map[string]string, len(rows))
		for k, v := range rows {
			if !skip[k] {
				use[k] = v
			}
		}
		return c, ApplyConfigRows(c, use)
	}

	skip := map[string]bool{}
	c, warnings := build(skip)

	// At most two passes are needed: the first reports every key that would
	// not parse, the second retries without them. A rule failure then reverts
	// its own keys and is retried once more, and a rule that still fails with
	// every one of its keys at the compiled default is a bug in the defaults,
	// not in the stored data, so it is returned.
	if len(warnings) > 0 {
		for _, w := range warnings {
			for _, k := range ConfigKeys() {
				// ApplyConfigRows builds each warning from the key itself, so
				// naming it is what identifies the key to drop.
				if strings.Contains(w, k) {
					skip[k] = true
				}
			}
		}
		c, _ = build(skip)
	}

	for attempt := 0; attempt < 2; attempt++ {
		err := config.Validate(c)
		if err == nil {
			c.Warnings = append(c.Warnings, warnings...)
			return c, nil
		}
		var re config.RuleError
		if !errors.As(err, &re) {
			// A single-key rule. Nothing distinguishes which stored row caused
			// it, so every stored key reverts and the process runs on
			// defaults rather than refusing to start.
			c, _ = build(allKeys())
			warnings = append(warnings,
				fmt.Sprintf("stored configuration is unusable (%v); every key reverted to its default", err))
			continue
		}
		for _, k := range re.Keys {
			skip[k] = true
		}
		warnings = append(warnings,
			fmt.Sprintf("stored %v broke the %s rule; all of them reverted to their defaults", re.Keys, re.Rule))
		c, _ = build(skip)
	}

	if err := config.Validate(c); err != nil {
		return nil, fmt.Errorf("compiled defaults do not validate: %w", err)
	}
	c.Warnings = append(c.Warnings, warnings...)
	return c, nil
}

func allKeys() map[string]bool {
	m := make(map[string]bool, len(configRegistry))
	for _, f := range configRegistry {
		m[f.key] = true
	}
	return m
}

// configRows reads only the keys this binary knows. The settings table is
// shared with the keyring, the CSRF secret and the import markers, so a
// SELECT * would hand foreign rows to the registry.
func configRows(ctx context.Context, d *DB) (map[string]string, error) {
	out := map[string]string{}
	rows, err := d.Read.QueryContext(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		return nil, fmt.Errorf("read stored configuration: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, fmt.Errorf("scan stored configuration: %w", err)
		}
		if ConfigKeyKnown(k) {
			out[k] = v
		}
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Export the validator**

In `internal/config/load.go`, add:

```go
// Validate runs every rule against an assembled Config. The database loader
// needs it, and it is the one validator: the write path calls it too, so a
// value refused on save is never a value a later load would accept.
func Validate(c *Config) error { return validate(c) }
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/store/ -run TestLoadConfig -v`
Expected: PASS, all five tests

- [ ] **Step 6: Prove the tests can fail**

Make `ApplyConfigRows` return the parse error instead of a warning, so `LoadConfig` propagates it. Run the tests, confirm `TestLoadConfigSurvivesAnUnparseableRow` goes red, then restore.

- [ ] **Step 7: Commit**

```bash
git add internal/store/configload.go internal/store/configload_test.go internal/config/load.go
git commit -m "feat(store): load configuration from the database"
```

---

### Task 5: Reconcile stored rows that equal the default

**Files:**
- Create: `internal/store/configreconcile.go`
- Create: `internal/store/configreconcile_test.go`

**Interfaces:**
- Consumes: `store.ConfigRowsFor` (Task 3); `store.configRows` and `config.ApplyDefaults` (Task 4).
- Produces: `store.ReconcileConfig(ctx context.Context, d *DB) (int, error)` returning the number of rows deleted.

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 3 = 5
**Approach:** inline - skip 3: the spec settles both the rule and the accepted trade-off (an explicit value equal to the default becomes indistinguishable from unset)

- [ ] **Step 1: Write the failing test**

```go
package store

import (
	"context"
	"testing"

	"github.com/darkraise/darkrouter/internal/store/storetest"
)

// ImportConfigOnce materialised all seven policy keys on every deployment that
// ever started, so without this pass the console shows them as chosen values
// the operator never picked, and a reset can never take.
func TestReconcileDeletesRowsEqualToTheDefault(t *testing.T) {
	db := storetest.Migrated(t)
	ctx := context.Background()
	if err := putSetting(ctx, db.Write, "policy.retry.max_attempts", "4"); err != nil {
		t.Fatal(err)
	}
	if err := putSetting(ctx, db.Write, "log.retention", "100h"); err != nil {
		t.Fatal(err)
	}
	n, err := ReconcileConfig(ctx, db)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("deleted %d rows, want 1", n)
	}
	if _, ok, _ := getSetting(ctx, db.Read, "policy.retry.max_attempts"); ok {
		t.Error("a row equal to the default must be deleted")
	}
	if _, ok, _ := getSetting(ctx, db.Read, "log.retention"); !ok {
		t.Error("a row that differs from the default must be kept")
	}
}

func TestReconcileDropsTheImportMarker(t *testing.T) {
	db := storetest.Migrated(t)
	ctx := context.Background()
	if err := putSetting(ctx, db.Write, settingConfigImportedAt, "2026-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if _, err := ReconcileConfig(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := getSetting(ctx, db.Read, settingConfigImportedAt); ok {
		t.Error("the import marker outlived the import and must go")
	}
}

// Rows that belong to other subsystems share this table and must survive.
func TestReconcileLeavesForeignRowsAlone(t *testing.T) {
	db := storetest.Migrated(t)
	ctx := context.Background()
	if err := putSetting(ctx, db.Write, "csrf_secret", "keep-me"); err != nil {
		t.Fatal(err)
	}
	if _, err := ReconcileConfig(ctx, db); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := getSetting(ctx, db.Read, "csrf_secret"); !ok {
		t.Fatal("reconciliation deleted a row it does not own")
	}
}

func TestReconcileIsANoOpOnAFreshDatabase(t *testing.T) {
	db := storetest.Migrated(t)
	n, err := ReconcileConfig(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("deleted %d rows on a fresh database, want 0", n)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/store/ -run TestReconcile -v`
Expected: FAIL, `undefined: ReconcileConfig`

- [ ] **Step 3: Write minimal implementation**

Create `internal/store/configreconcile.go`:

```go
package store

import (
	"context"
	"fmt"

	"github.com/darkraise/darkrouter/internal/config"
)

// ReconcileConfig deletes stored rows whose value equals the compiled default,
// so an upgraded database and a fresh one describe the same state.
//
// This makes startup write to the database, which it did not do before. It is
// safe because there is one process and one SQLite file, and it is a no-op on
// a database that has just been migrated.
//
// It deliberately loses one distinction: a value an operator set explicitly
// which happens to equal the default becomes indistinguishable from one never
// set, so it will float if a later release changes that default. The
// alternative is carrying a "set to the default on purpose" flag through the
// registry, the API and the console to serve a case nobody has asked for.
func ReconcileConfig(ctx context.Context, d *DB) (int, error) {
	defaults := &config.Config{}
	config.ApplyDefaults(defaults)
	want := ConfigRowsFor(defaults)

	stored, err := configRows(ctx, d)
	if err != nil {
		return 0, err
	}

	tx, err := d.Write.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin reconciliation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	deleted := 0
	for key, value := range stored {
		if want[key] != value {
			continue
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, key); err != nil {
			return 0, fmt.Errorf("delete redundant setting %q: %w", key, err)
		}
		deleted++
	}
	// The import it marked cannot happen again; leaving it behind is an
	// orphan row in a table this package now claims to own.
	if _, err := tx.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, settingConfigImportedAt); err != nil {
		return 0, fmt.Errorf("delete the import marker: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit reconciliation: %w", err)
	}
	return deleted, nil
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/store/ -run TestReconcile -v`
Expected: PASS, all four tests

- [ ] **Step 5: Prove the tests can fail**

Invert the comparison to `if want[key] == value { continue }`, run the tests, confirm `TestReconcileDeletesRowsEqualToTheDefault` and `TestReconcileLeavesForeignRowsAlone` both go red, then restore.

- [ ] **Step 6: Commit**

```bash
git add internal/store/configreconcile.go internal/store/configreconcile_test.go
git commit -m "feat(store): drop stored rows that match the default"
```

---

### Task 6: Give the store a boot snapshot

**Files:**
- Modify: `internal/config/store.go` (add fields and methods; leave `NewStore` alone)
- Modify: `internal/config/store_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: `(*Store).PendingRestart() []string`, comparing the boot snapshot with the current one.

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 1 = 3
**Approach:** inline - skip 2: `restartOnlyFields` already supplies the comparison table; only the baseline changes

- [ ] **Step 1: Write the failing test**

```go
// restartOnlyWarnings diffs consecutive snapshots, so the next unrelated save
// clears the warning while the process is still running the old value. The
// pending set has to be measured against boot, not against the last reload.
func TestPendingRestartSurvivesAnUnrelatedReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "darkrouter.yaml")
	writeFile(t, path, "catalog:\n  sync_interval: 12h\n")
	s, err := NewStore(path, env(nil))
	if err != nil {
		t.Fatal(err)
	}
	if got := s.PendingRestart(); len(got) != 0 {
		t.Fatalf("PendingRestart = %v at boot, want none", got)
	}

	writeFile(t, path, "catalog:\n  sync_interval: 6h\n")
	if err := s.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := s.PendingRestart(); len(got) != 1 || got[0] != "catalog.sync_interval" {
		t.Fatalf("PendingRestart = %v, want [catalog.sync_interval]", got)
	}

	// An unrelated hot-reloadable change must not clear it: the process is
	// still running the sync interval it booted with.
	writeFile(t, path, "catalog:\n  sync_interval: 6h\nlog:\n  retention: 100h\n")
	if err := s.Reload(); err != nil {
		t.Fatal(err)
	}
	if got := s.PendingRestart(); len(got) != 1 || got[0] != "catalog.sync_interval" {
		t.Fatalf("PendingRestart = %v after an unrelated save, want it still pending", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestPendingRestart -v`
Expected: FAIL, `s.PendingRestart undefined`

- [ ] **Step 3: Write minimal implementation**

In `internal/config/store.go`, add a field to `Store`:

```go
	// boot is the snapshot the process is actually running restart-only values
	// from. Pending-restart is boot versus current, never the diff between two
	// consecutive reloads: that diff is cleared by the next unrelated save
	// while the old value is still in force.
	boot atomic.Pointer[Config]
```

Set it in `NewStore`, immediately after `s.cur.Store(c)`:

```go
	s.boot.Store(c)
```

And add:

```go
// PendingRestart names every restart-only field whose stored value differs
// from the one this process started with.
func (s *Store) PendingRestart() []string {
	boot, cur := s.boot.Load(), s.cur.Load()
	if boot == nil || cur == nil {
		return nil
	}
	var out []string
	for _, f := range restartOnlyFields {
		if f.value(boot) != f.value(cur) {
			out = append(out, f.name)
		}
	}
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/config/ -v`
Expected: PASS, including the existing store tests

- [ ] **Step 5: Prove the test can fail**

Change `PendingRestart` to compare `s.cur` with itself, run the test, confirm it goes red at the second assertion, then restore.

- [ ] **Step 6: Commit**

```bash
git add internal/config/store.go internal/config/store_test.go
git commit -m "feat(config): track restart-only drift from boot"
```

---

### Task 7: Add a store constructor that takes an injected loader

**Files:**
- Modify: `internal/config/store.go`
- Modify: `internal/config/store_test.go`

**Interfaces:**
- Consumes: `(*Store).PendingRestart` (Task 6).
- Produces: `config.NewStoreFrom(load func() (*Config, error)) (*Store, error)`; `(*Store).Reload()` works through the injected loader when one is present.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4
**Approach:** inline - skip 2: injection mirrors `SetOverlay`, which exists for the same import-cycle reason

- [ ] **Step 1: Write the failing test**

```go
func TestNewStoreFromUsesTheInjectedLoader(t *testing.T) {
	n := 0
	s, err := NewStoreFrom(func() (*Config, error) {
		n++
		c := &Config{}
		applyDefaults(c)
		c.Log.Retention = time.Duration(n) * 100 * time.Hour
		return c, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if s.Current().Log.Retention != 100*time.Hour {
		t.Errorf("retention = %v, want the loader's first value", s.Current().Log.Retention)
	}
	if err := s.Reload(); err != nil {
		t.Fatal(err)
	}
	if s.Current().Log.Retention != 200*time.Hour {
		t.Errorf("retention = %v, want the loader's second value", s.Current().Log.Retention)
	}
}

// A loader that fails leaves the previous snapshot live. A reload that could
// take the gateway down is worse than one that does nothing.
func TestNewStoreFromKeepsTheOldSnapshotWhenTheLoaderFails(t *testing.T) {
	fail := false
	s, err := NewStoreFrom(func() (*Config, error) {
		if fail {
			return nil, errors.New("database is gone")
		}
		c := &Config{}
		applyDefaults(c)
		return c, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	fail = true
	if err := s.Reload(); err == nil {
		t.Fatal("a failing loader must report its error")
	}
	if s.Current() == nil {
		t.Fatal("the previous snapshot must stay live")
	}
	if s.LastError() == nil {
		t.Error("the failure must reach LastError, which /readyz reads")
	}
}
```

Add `"errors"` and `"time"` to the test file's imports if absent.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestNewStoreFrom -v`
Expected: FAIL, `undefined: NewStoreFrom`

- [ ] **Step 3: Write minimal implementation**

In `internal/config/store.go`, add a field to `Store`:

```go
	// load builds a fresh Config. Injected rather than called directly,
	// because this package may not import internal/store: store already
	// imports config and the reverse edge would close a cycle.
	load func() (*Config, error)
```

Add the constructor:

```go
// NewStoreFrom builds a store over an injected loader instead of a file.
func NewStoreFrom(load func() (*Config, error)) (*Store, error) {
	s := &Store{load: load}
	c, err := load()
	if err != nil {
		return nil, err
	}
	s.cur.Store(c)
	s.boot.Store(c)
	return s, nil
}
```

And in `Reload`, replace the `Load(s.path, s.lookup)` call with:

```go
	next, err := s.loadNext()
```

adding:

```go
func (s *Store) loadNext() (*Config, error) {
	if s.load != nil {
		return s.load()
	}
	return Load(s.path, s.lookup)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/config/ -v`
Expected: PASS, including every existing file-based store test

- [ ] **Step 5: Prove the tests can fail**

Make `Reload` store the new snapshot before checking the loader's error, run the tests, confirm `TestNewStoreFromKeepsTheOldSnapshotWhenTheLoaderFails` goes red, then restore.

- [ ] **Step 6: Commit**

```bash
git add internal/config/store.go internal/config/store_test.go
git commit -m "feat(config): allow a store over an injected loader"
```

---

### Task 8: Move every call site onto the injected loader

**Files:**
- Modify: `cmd/darkrouter/main.go`
- Modify: every test that calls `config.NewStore` (17 files, 27 sites — find them with the command in Step 1)
- Delete: the file-watching half of `internal/config/store.go` and its tests

**Interfaces:**
- Consumes: `config.NewStoreFrom` (Task 7), `store.LoadConfig` (Task 4), `store.ReconcileConfig` (Task 5), `config.BootstrapFrom` (Task 1).
- Produces: `config.NewStore`, `(*Store).Watch`, `(*Store).Path` no longer exist.

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 0 - spec 0 - coupling 3 - risk 2 = 5
**Approach:** inline - skip 3: the spec fixes the injected-loader shape; this is that decision applied across call sites

- [ ] **Step 1: List every call site**

```bash
grep -rn "config\.NewStore(\|[^.]NewStore(" --include="*.go" . | grep -v "func NewStore" | grep -v NewStoreFrom
```

Expected: 27 sites across 17 files.

- [ ] **Step 2: Add a test-only helper so the migration is one shape**

In `internal/config/store.go`:

```go
// NewStoreOf builds a store over a fixed Config. Tests that used to write a
// temporary YAML file to get a store use this instead; nothing in production
// calls it.
func NewStoreOf(c *Config) *Store {
	s := &Store{load: func() (*Config, error) { return c, nil }}
	s.cur.Store(c)
	s.boot.Store(c)
	return s
}
```

- [ ] **Step 3: Rewire main**

In `cmd/darkrouter/main.go`, replace the flag block and store construction:

```go
	fs := flag.NewFlagSet("darkrouter", flag.ExitOnError)
	// Accepted and ignored for one release. An operator who overrode the
	// container's command still passes it, and flag.ExitOnError would
	// otherwise refuse to start with a message explaining nothing.
	legacyConfig := fs.String("config", "", "deprecated; configuration now lives in the database")
	dbPath := fs.String("db", "", "path to the database file (default: darkrouter.db in the working directory)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dbPath == "" {
		if v, ok := os.LookupEnv("DARKROUTER_DB"); ok && strings.TrimSpace(v) != "" {
			*dbPath = v
		} else {
			*dbPath = "darkrouter.db"
		}
	}
	if *legacyConfig != "" {
		slog.Warn("-config is ignored; configuration now lives in the database",
			"path", *legacyConfig)
	}
```

Then, after `store.OpenKeyring`, replace the config-store construction:

```go
	if n, err := store.ReconcileConfig(context.Background(), db); err != nil {
		return err
	} else if n > 0 {
		slog.Info("dropped stored settings that matched the default", "count", n)
	}

	boot := config.BootstrapFrom(os.LookupEnv)
	cfgStore, err := config.NewStoreFrom(func() (*config.Config, error) {
		return store.LoadConfig(context.Background(), db, boot)
	})
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
```

Then delete, in this order:

1. The original `config.NewStore(*path, os.LookupEnv)` call near the top of `runServer`. The store is now built after `OpenKeyring`, which is the first point at which the database can answer.
2. The `cfgStore.Watch` goroutine registration and the `s.store.RecordError(s.store.Watch(c))` line in `internal/server/server.go` — there is no file to watch.
3. The `store.ImportFromConfig` and `store.ImportConfigOnce` calls. Neither has a file to import from, and `ReconcileConfig` now owns the marker row they wrote.

Keep `cfgStore.SetOverlay(...)`. `LoadConfig` reads the 32 scalar keys and not the alias table, so aliases still reach a snapshot through the overlay exactly as they do today.

- [ ] **Step 4: Migrate the tests**

Replace each `config.NewStore(path, lookup)` in a test with a `config.NewStoreOf(c)` over a literal `Config`. For the e2e harness at `internal/e2e/harness_test.go:85-95`, which wrote `proxy_listen: ":0"` to get an ephemeral port, set it through the bootstrap instead:

```go
	c := &config.Config{}
	config.ApplyDefaults(c)
	c.Server.ProxyListen = ":0"
	c.Server.AdminListen = ":0"
	cfgStore := config.NewStoreOf(c)
```

- [ ] **Step 5: Delete the file half**

From `internal/config/store.go` delete: `NewStore`, `Path`, `Watch`, `watch`, `realPath`, the `debounce` constant, the `path` and `lookup` fields, and `loadNext`'s fallback branch (it becomes `return s.load()`). Delete the watcher tests. From `internal/config/load.go` delete: `Load`, `Parse`'s file entry point, `interpolate`, `resolve`, `envRef`, `documentKeys`, and the `FileKeys` field on `Config`. Keep `applyDefaults`, `validate`, `Validate`, `ApplyDefaultsForTest`, `normalizeDomain` and `ValidateAliases`.

```bash
go mod tidy   # drops github.com/fsnotify/fsnotify
```

- [ ] **Step 6: Run the whole suite**

Run: `go build ./... && go test ./...`
Expected: PASS. Any remaining reference to `NewStore`, `Watch` or `FileKeys` is a compile error naming the file to fix.

- [ ] **Step 7: Prove the migration is real**

Run: `grep -rn "darkrouter.yaml" --include="*.go" .`
Expected: no matches outside comments.

- [ ] **Step 8: Commit**

```bash
git add -A
git commit -m "refactor(config): load from the database, drop the file"
```

---

### Task 9: Retire the listen addresses from the restart-only table

**Files:**
- Modify: `internal/config/config.go` (the `restartOnlyFields` table, lines 123-155)
- Modify: `internal/config/config_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: `restartOnlyFields` holds 17 entries; `catalog.seed_free_providers` is among them; neither listen address is.

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 1 = 3
**Approach:** inline - skip 4: one table plus its test, and being wrong costs a revert

- [ ] **Step 1: Write the failing test**

```go
func TestRestartOnlyCoversWhatTheDatabaseCanChange(t *testing.T) {
	got := map[string]bool{}
	for _, f := range restartOnlyFields {
		got[f.name] = true
	}
	// An environment variable cannot change under a running process, so
	// warning that it needs a restart is noise about an impossible event.
	for _, gone := range []string{"server.proxy_listen", "server.admin_listen"} {
		if got[gone] {
			t.Errorf("%s is bootstrap-only and must leave the table", gone)
		}
	}
	// Consumed once at startup (cmd/darkrouter/main.go), so a change to it
	// genuinely does need a restart, and the console would otherwise offer it
	// as hot.
	if !got["catalog.seed_free_providers"] {
		t.Error("catalog.seed_free_providers is decided once at startup and must be restart-only")
	}
	if len(restartOnlyFields) != 17 {
		t.Errorf("restartOnlyFields holds %d entries, want 17", len(restartOnlyFields))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/config/ -run TestRestartOnlyCovers -v`
Expected: FAIL, both listen addresses present and `seed_free_providers` absent, count 18

- [ ] **Step 3: Write minimal implementation**

In `internal/config/config.go`, delete these two entries from `restartOnlyFields`:

```go
	{"server.proxy_listen", func(c *Config) any { return c.Server.ProxyListen }},
	{"server.admin_listen", func(c *Config) any { return c.Server.AdminListen }},
```

and add, beside the other catalogue entries:

```go
	// Whether the free-provider seed runs at all is read once at startup.
	{"catalog.seed_free_providers", func(c *Config) any { return optionalBool(c.Catalog.SeedFreeProviders) }},
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/config/ ./internal/admin/ -v`
Expected: PASS. `internal/admin`'s restart-only tests read the same table, so they follow.

- [ ] **Step 5: Prove the test can fail**

Put `server.proxy_listen` back, run the test, confirm it goes red on both the membership and the count, then remove it again.

- [ ] **Step 6: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go
git commit -m "fix(config): correct the restart-only field set"
```

---

### Task 10: Move the container and compose files off the file

**Files:**
- Modify: `Dockerfile:122`
- Modify: `compose.yml`, `compose.prod.yml`
- Modify: `.env.example`
- Delete: `darkrouter.example.yaml`

**Interfaces:**
- Consumes: the `-config` no-op and `DARKROUTER_DB` default from Task 8.
- Produces: an image that starts with no configuration file present.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 2 = 4
**Approach:** inline - skip 3: the spec fixes the bootstrap set and the entrypoint change

- [ ] **Step 1: Change the entrypoint**

In `Dockerfile`, replace line 122:

```dockerfile
ENTRYPOINT ["darkrouter"]
```

`WORKDIR /data` is already set at line 116, so the database default of
`darkrouter.db` resolves to `/data/darkrouter.db` — the same path as before.

- [ ] **Step 2: Add the listen variables to the example environment**

In `.env.example`, replace the port comment block:

```bash
# Which published tag to run, and the host ports it is published on. The
# container listens on 18080 and 18081 internally unless these say otherwise.
#DARKROUTER_TAG=latest
#PROXY_PORT=18080
#ADMIN_PORT=18081

# The addresses the gateway and console bind inside the container. These are
# bootstrap values and cannot be set in the console: a bad value here would
# make the console that would fix it unreachable.
#DARKROUTER_PROXY_LISTEN=:18080
#DARKROUTER_ADMIN_LISTEN=:18081

# Where the database lives. Defaults to darkrouter.db in the working
# directory, which is /data in the container.
#DARKROUTER_DB=/data/darkrouter.db
```

- [ ] **Step 3: Drop the example config**

```bash
git rm darkrouter.example.yaml
```

- [ ] **Step 4: Check the compose files**

Neither `compose.yml` nor `compose.prod.yml` names the config file today — confirm with:

```bash
grep -n "darkrouter.yaml\|-config" compose.yml compose.prod.yml
```

Expected: no matches. If any appear, remove them.

- [ ] **Step 5: Build and run the image**

```bash
docker build -t darkraise/darkrouter:latest .
docker compose -f compose.prod.yml -f compose.uat.yml up -d darkrouter
sleep 10
docker ps --filter name=darkrouter --format '{{.Status}}'
curl -s http://localhost:8091/healthz
```

Expected: `healthy`, and `config_valid: true`. The `compose.uat.yml` overlay is required or the published image is pulled over the local build.

- [ ] **Step 6: Prove authentication survived**

This is the regression check for the hazard in the spec's §1.

```bash
curl -s -o /dev/null -w "%{http_code}\n" http://localhost:8090/v1/models
```

Expected: `401`. A `200` means `DARKROUTER_PROXY_TOKEN` stopped reaching the process and the gateway is open.

- [ ] **Step 7: Commit**

```bash
git add -A
git commit -m "feat(docker): start with no configuration file"
```

---

### Task 11: Report a skipped key on the health surface

**Files:**
- Modify: `internal/server/server.go` (the `/healthz` handler, around line 525)
- Modify: `internal/server/server_test.go`
- Modify: `cmd/darkrouter/main.go`

**Interfaces:**
- Consumes: `cfg.Warnings` populated by `store.LoadConfig` (Task 4).
- Produces: `/healthz` reports `config_valid: false` whenever a stored key was skipped; startup warns about a leftover `darkrouter.yaml`.

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 1 = 3
**Approach:** inline - skip 3: the spec fixes both behaviours

- [ ] **Step 1: Write the failing test**

```go
// Reporting a config as valid while the process quietly substituted three
// defaults makes the field useless. It is the only signal that a stored value
// is being ignored, because nothing else about a skipped key is visible.
func TestHealthzReportsASkippedKeyAsInvalid(t *testing.T) {
	c := &config.Config{}
	config.ApplyDefaults(c)
	c.Warnings = []string{"stored log.retention is unusable (bad duration); using the default"}
	s := testServerWithConfig(t, c)

	r := httptest.NewRequest("GET", "/healthz", nil)
	w := httptest.NewRecorder()
	s.AdminHandler().ServeHTTP(w, r)

	var body struct {
		ConfigValid bool     `json:"config_valid"`
		Warnings    []string `json:"warnings"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.ConfigValid {
		t.Error("config_valid = true while a stored key was skipped")
	}
	if len(body.Warnings) == 0 {
		t.Error("the reason a key was skipped must reach the caller")
	}
}

// A clean load still reports valid, or the field means nothing in the
// direction that matters.
func TestHealthzReportsACleanLoadAsValid(t *testing.T) {
	c := &config.Config{}
	config.ApplyDefaults(c)
	s := testServerWithConfig(t, c)

	r := httptest.NewRequest("GET", "/healthz", nil)
	w := httptest.NewRecorder()
	s.AdminHandler().ServeHTTP(w, r)

	var body struct {
		ConfigValid bool `json:"config_valid"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.ConfigValid {
		t.Error("config_valid = false on a clean load")
	}
}
```

Add a `testServerWithConfig(t *testing.T, c *config.Config) *Server` helper beside the existing server-test fixtures, building the server over `config.NewStoreOf(c)` (Task 8, Step 2).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/server/ -run TestHealthzReports -v`
Expected: FAIL, `config_valid = true while a stored key was skipped`

- [ ] **Step 3: Write minimal implementation**

In `internal/server/server.go`, replace the `config_valid` line:

```go
			// A skipped key is not a valid configuration. The process is
			// running a default the operator did not choose, and warnings is
			// the only place that fact appears.
			"config_valid": cfgErr == nil && len(cfg.Warnings) == 0,
```

- [ ] **Step 4: Warn about a leftover file**

In `cmd/darkrouter/main.go`, immediately after the database path is resolved:

```go
	// The file stopped being read in this release. Saying so once is what
	// turns "my settings reverted" into an obvious morning rather than a
	// confusing one.
	legacy := filepath.Join(filepath.Dir(*dbPath), "darkrouter.yaml")
	if _, err := os.Stat(legacy); err == nil {
		slog.Warn("a configuration file is present but no longer read; "+
			"settings now live in the database and are changed in the console",
			"path", legacy)
	}
```

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/server/ ./cmd/... -v`
Expected: PASS. The existing `server_test.go:56` assertion that `config_valid` is true still holds, because its fixture has no warnings.

- [ ] **Step 6: Prove the tests can fail**

Revert `config_valid` to `cfgErr == nil`, run the tests, confirm `TestHealthzReportsASkippedKeyAsInvalid` goes red, then restore.

- [ ] **Step 7: Commit**

```bash
git add internal/server/server.go internal/server/server_test.go cmd/darkrouter/main.go
git commit -m "feat(health): report a skipped setting as invalid"
```

---

## Out of scope for this phase

Phase 2 (the transactional write path and one validator), phase 3 (the console
editors) and phase 4 (the documentation rewrite) each get their own plan. Until
phase 2 lands, `PUT /api/config` and `PUT /api/policy` keep their current
behaviour: they write policy and aliases through `putPolicyTx` and
`PutAliases`, which still work, and the settings screen stays read-only for
everything else. That is a coherent shipping state — the gateway reads its
configuration from the database and the console reports it — and it is why
these phases are separable.
