package store

import (
	"context"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/config"
)

func boot() config.Bootstrap {
	return config.Bootstrap{ProxyListen: ":18080", AdminListen: ":18081"}
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
}

// A cross-key rule names no single culprit, so every key in it reverts
// together. Reverting one of the three would produce a config the operator did
// not ask for either, and which one you picked would depend on write order.
func TestLoadConfigRevertsEveryKeyInAFailedRule(t *testing.T) {
	db := migrated(t)
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
	db := migrated(t)
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
