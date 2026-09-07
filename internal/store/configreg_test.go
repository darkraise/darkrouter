package store

import (
	"strings"
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

// media.inline defaults to on, but its zero value is a nil pointer. If the
// registry read that nil as false, a defaulted config would round-trip into
// a stored row that turns media inlining off.
func TestConfigRegistryDefaultsOnBoolReadsAsTrue(t *testing.T) {
	c := &config.Config{}
	config.ApplyDefaults(c)

	rows := ConfigRowsFor(c)
	if got := rows["media.inline"]; got != "true" {
		t.Fatalf(`rows["media.inline"] = %q, want "true"`, got)
	}

	dst := &config.Config{}
	config.ApplyDefaults(dst)
	if warn := ApplyConfigRows(dst, rows); len(warn) > 0 {
		t.Fatalf("round trip warned: %v", warn)
	}
	if !dst.MediaInline() {
		t.Error("media.inline round-tripped from a defaulted config as off")
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

// LoadConfig identifies which key a warning names with strings.Contains, so a
// key that is a substring of another would silently revert both. Nothing about
// that failure is visible at the call site, which is why the invariant is
// asserted here rather than left to review.
func TestConfigKeysAreNotSubstringsOfEachOther(t *testing.T) {
	keys := ConfigKeys()
	for _, a := range keys {
		for _, b := range keys {
			if a != b && strings.Contains(b, a) {
				t.Errorf("key %q is a substring of %q; LoadConfig's warning matching would revert both", a, b)
			}
		}
	}
}

// The bound used to live in the admin handler, where a save was refused and a
// row written by hand was not. One validator means the loader refuses it too.
func TestApplyConfigRowsRevertsAnOutOfRangeRetryCount(t *testing.T) {
	c := &config.Config{}
	config.ApplyDefaults(c)
	warnings := ApplyConfigRows(c, map[string]string{"policy.retry.max_attempts": "20"})
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want exactly one", warnings)
	}
	// The prefix LoadConfig matches on to find the key it must revert.
	if !strings.HasPrefix(warnings[0], "stored policy.retry.max_attempts ") {
		t.Errorf("warning = %q, want it to open with the key", warnings[0])
	}
	if c.Policy.Retry.MaxAttempts != 4 {
		t.Errorf("MaxAttempts = %d, want the compiled default", c.Policy.Retry.MaxAttempts)
	}
}

func TestApplyConfigRowsAcceptsTheRetryCountAtTheCap(t *testing.T) {
	c := &config.Config{}
	config.ApplyDefaults(c)
	if w := ApplyConfigRows(c, map[string]string{"policy.retry.max_attempts": "10"}); len(w) != 0 {
		t.Fatalf("warnings = %v, want none", w)
	}
	if c.Policy.Retry.MaxAttempts != 10 {
		t.Errorf("MaxAttempts = %d, want 10", c.Policy.Retry.MaxAttempts)
	}
}

// A rejected value must leave nothing behind. The setter runs before the
// validator can see the result, so the pass has to work on a copy rather than
// undo itself afterwards.
func TestApplyConfigRowsLeavesAnUnrelatedKeyAlone(t *testing.T) {
	c := &config.Config{}
	config.ApplyDefaults(c)
	warnings := ApplyConfigRows(c, map[string]string{
		"policy.retry.max_attempts": "20",
		"log.retention":             "96h",
	})
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want exactly one", warnings)
	}
	if c.Log.Retention != 96*time.Hour {
		t.Errorf("log.retention = %s, want 96h", c.Log.Retention)
	}
}
