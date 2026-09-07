package store

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/config"
)

// seedSetting stores one row, so a test names the key and value it cares about
// rather than the plumbing.
func seedSetting(t *testing.T, d *DB, key, value string) {
	t.Helper()
	if err := putSetting(context.Background(), d.Write, key, value); err != nil {
		t.Fatal(err)
	}
}

func boot() config.Bootstrap {
	return config.Bootstrap{ProxyListen: ":18080", AdminListen: ":18081", ProxyToken: "sekrit"}
}

func TestLoadConfigUsesDefaultsWhenNothingIsStored(t *testing.T) {
	db := migrated(t)
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
	// An empty shared token admits every unauthenticated request when no
	// per-client token exists, so losing it here opens the gateway.
	if c.Server.ProxyToken != "sekrit" {
		t.Errorf("proxy_token = %q, want the bootstrap value", c.Server.ProxyToken)
	}
}

func TestLoadConfigAppliesAStoredRow(t *testing.T) {
	db := migrated(t)
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
	db := migrated(t)
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
	if !slices.Contains(c.Skipped, "log.retention") {
		t.Errorf("skipped = %v, want log.retention -- this is what makes /healthz report invalid", c.Skipped)
	}
}

// A cross-key rule names no single culprit, so every key in it reverts
// together. Reverting one of the three would produce a config the operator did
// not ask for either, and which one you picked would depend on write order.
func TestLoadConfigRevertsEveryKeyInAFailedRule(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	for k, v := range map[string]string{
		"policy.timeout.connect":    "30s",
		"policy.timeout.first_byte": "90s",
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
	// The revert path rebuilds the config from scratch, so it is where a
	// refactor would drop the bootstrap fields without any other test noticing.
	if c.Server.ProxyToken != "sekrit" {
		t.Errorf("proxy_token = %q, want the bootstrap value to survive a revert", c.Server.ProxyToken)
	}
	if len(c.Warnings) == 0 {
		t.Error("a reverted rule must warn")
	}
	for _, k := range []string{"policy.timeout.connect", "policy.timeout.first_byte", "policy.timeout.total"} {
		if !slices.Contains(c.Skipped, k) {
			t.Errorf("skipped = %v, want all three reverted keys named", c.Skipped)
		}
	}
}

// Rows belonging to the keyring, the CSRF secret and the import markers share
// this table. Reading them as config would be a bug that only shows up as a
// mystery warning.
func TestLoadConfigIgnoresForeignRows(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	if err := putSetting(ctx, db.Write, "csrf_secret", "not-a-config-value"); err != nil {
		t.Fatal(err)
	}
	if err := putSetting(ctx, db.Write, "log.retention", "100h"); err != nil {
		t.Fatal(err)
	}

	// Asserted on configRows rather than on the warnings LoadConfig produces:
	// ApplyConfigRows walks the registry, not the rows, so a foreign row is
	// silent either way and only this can see the filter disappear.
	rows, err := configRows(ctx, db.Read)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := rows["csrf_secret"]; ok {
		t.Error("csrf_secret reached the registry; the settings table is shared, so reads must filter by membership")
	}
	if rows["log.retention"] != "100h" {
		t.Errorf("log.retention = %q, want the stored row to survive the filter", rows["log.retention"])
	}

	c, err := LoadConfig(ctx, db, boot())
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Warnings) != 0 {
		t.Errorf("warnings = %v, want none for a foreign row", c.Warnings)
	}
	if len(c.Skipped) != 0 {
		t.Errorf("skipped = %v, want none for a foreign row", c.Skipped)
	}
}

// A stored bare domain is how an operator writes server.public_url; validate
// demands an absolute URL. Normalising on the way out of the registry is what
// keeps the two from disagreeing -- and a disagreement here is not a rejected
// field, it is the whole configuration reverting.
func TestAStoredBareDomainLoadsAsAURL(t *testing.T) {
	d := migrated(t)
	seedSetting(t, d, "server.public_url", "llm.example.com")

	c, err := LoadConfig(context.Background(), d, config.Bootstrap{})
	if err != nil {
		t.Fatal(err)
	}
	if c.Server.PublicURL != "https://llm.example.com" {
		t.Errorf("public_url = %q, want https://llm.example.com", c.Server.PublicURL)
	}
	if len(c.Warnings) != 0 {
		t.Errorf("warnings = %v; a bare domain is a supported spelling, not a fault", c.Warnings)
	}
	if len(c.Skipped) != 0 {
		t.Errorf("skipped = %v; a bare domain is a supported spelling, not a fault", c.Skipped)
	}
}

// A single-key rule failure must cost that key, not every key. Reverting all 32
// discards an operator's whole configuration over one bad value, and the loader
// already knows which key the message names.
func TestASingleBadKeyDoesNotRevertTheOthers(t *testing.T) {
	d := migrated(t)
	// Absolute, so the registry's normalisation leaves it alone, and refused by
	// validate because it carries a query.
	seedSetting(t, d, "server.public_url", "https://api.example.com?key=x")
	seedSetting(t, d, "log.retention", "1000h")

	c, err := LoadConfig(context.Background(), d, config.Bootstrap{})
	if err != nil {
		t.Fatal(err)
	}
	if c.Server.PublicURL != "" {
		t.Errorf("public_url = %q, want the default after its rule failed", c.Server.PublicURL)
	}
	if c.Log.Retention != 1000*time.Hour {
		t.Errorf("log.retention = %v, want the stored 1000h; one bad key must not "+
			"revert an unrelated one", c.Log.Retention)
	}
	if len(c.Warnings) == 0 {
		t.Error("a reverted key must say so")
	}
	if !slices.Contains(c.Skipped, "server.public_url") {
		t.Errorf("skipped = %v, want server.public_url", c.Skipped)
	}
	if slices.Contains(c.Skipped, "log.retention") {
		t.Errorf("skipped = %v; log.retention was never reverted", c.Skipped)
	}
}

// strconv and time.ParseDuration quote the offending value into their error,
// so a substring match on the warning reverted whichever other key that value
// happened to spell -- one bad row silently taking a good one with it.
func TestAnUnparseableValueRevertsOnlyItsOwnKey(t *testing.T) {
	d := migrated(t)
	ctx := context.Background()
	if err := putSetting(ctx, d.Write, "log.retention", "1000h"); err != nil {
		t.Fatal(err)
	}
	if err := putSetting(ctx, d.Write, "capture.max_bytes", "log.retention"); err != nil {
		t.Fatal(err)
	}

	c, err := LoadConfig(ctx, d, config.Bootstrap{})
	if err != nil {
		t.Fatal(err)
	}
	if got := c.Log.Retention; got != 1000*time.Hour {
		t.Errorf("log.retention = %v, want the stored 1000h: its own row parses fine", got)
	}
	if slices.Contains(c.Skipped, "log.retention") {
		t.Errorf("skipped = %v, want log.retention absent", c.Skipped)
	}
	if !slices.Contains(c.Skipped, "capture.max_bytes") {
		t.Errorf("skipped = %v, want the unparseable capture.max_bytes in it", c.Skipped)
	}
}

// The write path builds its base from the rows inside its own transaction, so
// the assembly has to be reachable without a database. This is that seam.
func TestBuildConfigRevertsAKeyItCannotParse(t *testing.T) {
	c, warnings, skipped, err := buildConfig(
		map[string]string{"log.retention": "not-a-duration", "capture.max_bytes": "4096"},
		config.Bootstrap{}, nil)
	if err != nil {
		t.Fatalf("buildConfig: %v", err)
	}
	if len(skipped) != 1 || skipped[0] != "log.retention" {
		t.Fatalf("skipped = %v, want just log.retention", skipped)
	}
	if len(warnings) != 1 {
		t.Errorf("warnings = %v, want one", warnings)
	}
	if c.Log.Retention != 720*time.Hour {
		t.Errorf("log.retention = %s, want the compiled default", c.Log.Retention)
	}
	// The unrelated row still applies. A bad key reverts itself, not the save.
	if c.Capture.MaxBytes != 4096 {
		t.Errorf("capture.max_bytes = %d, want 4096", c.Capture.MaxBytes)
	}
}

func TestBuildConfigTakesItsListenAddressesFromTheBootstrap(t *testing.T) {
	c, _, _, err := buildConfig(nil, config.Bootstrap{
		ProxyListen: "127.0.0.1:1", AdminListen: "127.0.0.1:2", ProxyToken: "sekrit",
	}, nil)
	if err != nil {
		t.Fatalf("buildConfig: %v", err)
	}
	if c.Server.ProxyListen != "127.0.0.1:1" || c.Server.AdminListen != "127.0.0.1:2" {
		t.Errorf("listen = %q/%q, want the bootstrap's", c.Server.ProxyListen, c.Server.AdminListen)
	}
	if c.Server.ProxyToken != "sekrit" {
		t.Errorf("ProxyToken = %q, want the bootstrap's", c.Server.ProxyToken)
	}
}

// Aliases are validated with everything else when they are supplied. Nothing
// can revert them, so a broken set exhausts the loop and comes back as the
// error the caller must not paper over -- which is why the write path checks
// them before it gets here.
func TestBuildConfigFailsOnAnAliasSetNoKeyCanFix(t *testing.T) {
	_, _, _, err := buildConfig(nil, config.Bootstrap{},
		map[string][]string{"fast": {}})
	if err == nil {
		t.Fatal("buildConfig accepted an alias chain with no targets")
	}
}
