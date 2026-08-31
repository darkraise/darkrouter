package store

import (
	"context"
	"testing"
	"time"
)

func catalogDB(t *testing.T) *DB {
	t.Helper()
	db := migrated(t)
	if _, err := db.Write.ExecContext(context.Background(),
		`INSERT INTO providers (id, kind, base_url, created_at) VALUES ('p', 'openaicompat', 'http://x', 0)`); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestModelsReadsEveryColumn(t *testing.T) {
	ctx := context.Background()
	db := catalogDB(t)
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO models (provider_id, model_id, publisher, surfaces, capabilities,
		    capabilities_source, context_window, max_output_tokens,
		    input_price_micros_per_mtok, output_price_micros_per_mtok,
		    cache_read_price_micros_per_mtok, state, missing_streak, last_seen_at)
		 VALUES ('p', 'm', 'meta', '["llm","embedding"]',
		    '{"tools":true,"vision":false,"reasoning":true}', 'models_dev',
		    200000, 64000, 5000000, 25000000, 500000, 'stale', 2, 1700000000000)`); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Models(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	r := rows[0]
	if r.ProviderID != "p" || r.ModelID != "m" || r.Publisher != "meta" {
		t.Errorf("identity = %+v", r)
	}
	if len(r.Surfaces) != 2 || r.Surfaces[0] != "llm" || r.Surfaces[1] != "embedding" {
		t.Errorf("surfaces = %v", r.Surfaces)
	}
	if !r.Capabilities.Tools || r.Capabilities.Vision || !r.Capabilities.Reasoning {
		t.Errorf("capabilities = %+v", r.Capabilities)
	}
	if r.CapabilitiesSource != "models_dev" || r.State != "stale" || r.MissingStreak != 2 {
		t.Errorf("state = (%q, %q, %d)", r.CapabilitiesSource, r.State, r.MissingStreak)
	}
	if r.ContextWindow != 200000 || r.MaxOutputTokens != 64000 {
		t.Errorf("limits = (%d, %d)", r.ContextWindow, r.MaxOutputTokens)
	}
	if r.InputMicrosPerMTok != 5_000_000 || r.OutputMicrosPerMTok != 25_000_000 ||
		r.CacheReadMicrosPerMTok != 500_000 {
		t.Errorf("pricing = %+v", r)
	}
	if !r.LastSeenAt.Equal(time.UnixMilli(1700000000000).UTC()) {
		t.Errorf("last seen = %v", r.LastSeenAt)
	}
}

func TestModelsLeavesUnknownMetadataZero(t *testing.T) {
	// NULL means "we never found out" and must not read back as a real zero
	// price. A model priced at zero and a model of unknown price are different
	// facts, and the UI shows them differently.
	ctx := context.Background()
	db := catalogDB(t)
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO models (provider_id, model_id) VALUES ('p', 'm')`); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Models(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r := rows[0]
	if r.ContextWindow != 0 || r.MaxOutputTokens != 0 || r.InputMicrosPerMTok != 0 {
		t.Errorf("NULL columns did not read back as zero: %+v", r)
	}
	if r.PriceKnown {
		t.Error("PriceKnown is true with every price column NULL")
	}
	if !r.LastSeenAt.IsZero() {
		t.Errorf("last seen = %v, want zero", r.LastSeenAt)
	}
}

func TestUpsertMetadataRoundTripsCentPrices(t *testing.T) {
	// $0.14 per million must survive as 140000 micro-dollars. Storing
	// micro-dollars per *token* would truncate it to zero, which is the
	// specific bug master design section 11 fixes the unit to avoid.
	ctx := context.Background()
	db := catalogDB(t)
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO models (provider_id, model_id) VALUES ('p', 'm')`); err != nil {
		t.Fatal(err)
	}
	err := db.UpsertMetadata(ctx, []MetadataRow{{
		ProviderID: "p", ModelID: "m",
		ContextWindow: 8192, MaxOutputTokens: 4096,
		InputMicrosPerMTok: 140_000, OutputMicrosPerMTok: 280_000,
		Capabilities:       ModelCapabilities{Tools: true},
		CapabilitiesSource: "models_dev",
	}})
	if err != nil {
		t.Fatal(err)
	}
	rows, _ := db.Models(ctx)
	r := rows[0]
	if r.InputMicrosPerMTok != 140_000 {
		t.Errorf("input price = %d, want 140000", r.InputMicrosPerMTok)
	}
	if r.OutputMicrosPerMTok != 280_000 || !r.PriceKnown {
		t.Errorf("output price = %d, known = %v", r.OutputMicrosPerMTok, r.PriceKnown)
	}
	if r.ContextWindow != 8192 || !r.Capabilities.Tools || r.CapabilitiesSource != "models_dev" {
		t.Errorf("metadata = %+v", r)
	}
}

func TestUpsertMetadataNeverTouchesLifecycle(t *testing.T) {
	// A models.dev refresh must not resurrect a model discovery retired, nor
	// reset the streak that retired it.
	ctx := context.Background()
	db := catalogDB(t)
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO models (provider_id, model_id, state, missing_streak, last_seen_at)
		 VALUES ('p', 'm', 'removed_upstream', 3, 42)`); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertMetadata(ctx, []MetadataRow{{
		ProviderID: "p", ModelID: "m", ContextWindow: 4096,
		CapabilitiesSource: "models_dev",
	}}); err != nil {
		t.Fatal(err)
	}
	var state string
	var streak int
	var seen int64
	if err := db.Read.QueryRowContext(ctx,
		`SELECT state, missing_streak, last_seen_at FROM models WHERE model_id = 'm'`).
		Scan(&state, &streak, &seen); err != nil {
		t.Fatal(err)
	}
	if state != "removed_upstream" || streak != 3 || seen != 42 {
		t.Errorf("lifecycle changed: (%q, %d, %d)", state, streak, seen)
	}
}

func TestUpsertMetadataIgnoresUnknownModels(t *testing.T) {
	// models.dev knows models this provider does not offer. Inserting them
	// would make the catalog claim reachability it has no evidence for.
	ctx := context.Background()
	db := catalogDB(t)
	if err := db.UpsertMetadata(ctx, []MetadataRow{{
		ProviderID: "p", ModelID: "never-discovered", ContextWindow: 4096,
	}}); err != nil {
		t.Fatal(err)
	}
	rows, _ := db.Models(ctx)
	if len(rows) != 0 {
		t.Errorf("upsert invented %d rows", len(rows))
	}
}

func TestModelOverridesReadPartially(t *testing.T) {
	// An override sets one field and leaves the rest to the merge. A nil
	// pointer is "not overridden"; a zero value is a real override to zero.
	ctx := context.Background()
	db := catalogDB(t)
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO model_overrides (provider_id, model_id, capabilities)
		 VALUES ('p', 'm', '{"tools":true,"vision":true,"reasoning":false}')`); err != nil {
		t.Fatal(err)
	}
	ovs, err := db.ModelOverrides(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ovs) != 1 {
		t.Fatalf("got %d overrides", len(ovs))
	}
	o := ovs[0]
	if o.Capabilities == nil || !o.Capabilities.Tools || !o.Capabilities.Vision {
		t.Errorf("capabilities = %+v", o.Capabilities)
	}
	if o.ContextWindow != nil {
		t.Errorf("context window overridden to %v, want nil", *o.ContextWindow)
	}
	if o.Surfaces != nil {
		t.Errorf("surfaces overridden to %v, want nil", o.Surfaces)
	}
}

func TestUpsertMetadataRoundTripsCacheWritePricing(t *testing.T) {
	// A cache write is billed at its own rate, usually above the input rate.
	// Without a column for it the row prices every cached write at zero.
	ctx := context.Background()
	db := catalogDB(t)
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO models (provider_id, model_id) VALUES ('p', 'm')`); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertMetadata(ctx, []MetadataRow{{
		ProviderID: "p", ModelID: "m",
		InputMicrosPerMTok:      300_000,
		OutputMicrosPerMTok:     1_500_000,
		CacheReadMicrosPerMTok:  30_000,
		CacheWriteMicrosPerMTok: 375_000,
		CapabilitiesSource:      "models_dev",
	}}); err != nil {
		t.Fatal(err)
	}
	rows, _ := db.Models(ctx)
	if rows[0].CacheWriteMicrosPerMTok != 375_000 {
		t.Errorf("cache write price = %d, want 375000",
			rows[0].CacheWriteMicrosPerMTok)
	}
}
