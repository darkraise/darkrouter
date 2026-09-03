package store

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func lifecycleDB(t *testing.T) (*DB, context.Context) {
	t.Helper()
	ctx := context.Background()
	db := migrated(t)
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO providers (id, kind, base_url, created_at) VALUES ('p', 'openaicompat', 'http://x', 0)`); err != nil {
		t.Fatal(err)
	}
	return db, ctx
}

func modelState(t *testing.T, db *DB, id string) (string, int) {
	t.Helper()
	var state string
	var streak int
	if err := db.Read.QueryRowContext(context.Background(),
		`SELECT state, missing_streak FROM models WHERE provider_id = 'p' AND model_id = ?`, id).
		Scan(&state, &streak); err != nil {
		t.Fatalf("model %q: %v", id, err)
	}
	return state, streak
}

var t0 = time.Unix(1_700_000_000, 0).UTC()

func TestSuccessInsertsNewModels(t *testing.T) {
	db, ctx := lifecycleDB(t)
	if err := db.RecordDiscoverySuccess(ctx, "p", []DiscoveredModel{
		{ModelID: "a", ContextWindow: 8192, MaxOutputTokens: 4096},
		{ModelID: "b"},
	}, nil, t0); err != nil {
		t.Fatal(err)
	}
	if state, streak := modelState(t, db, "a"); state != "live" || streak != 0 {
		t.Errorf("a = (%q, %d)", state, streak)
	}
	rows, _ := db.Models(ctx)
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	for _, r := range rows {
		if r.ModelID == "a" && (r.ContextWindow != 8192 || r.MaxOutputTokens != 4096) {
			t.Errorf("a limits = (%d, %d)", r.ContextWindow, r.MaxOutputTokens)
		}
		if r.DiscoveredAt.IsZero() {
			t.Errorf("%s: discovered_at not stamped", r.ModelID)
		}
		if !r.LastSeenAt.Equal(t0) {
			t.Errorf("%s: last seen = %v, want %v", r.ModelID, r.LastSeenAt, t0)
		}
	}
}

func TestThreeFailuresMarkStaleAndNeverRemove(t *testing.T) {
	// The case this whole asymmetry exists for: a provider that times out must
	// not empty its catalog. Stale is still routable.
	db, ctx := lifecycleDB(t)
	if err := db.RecordDiscoverySuccess(ctx, "p", []DiscoveredModel{{ModelID: "a"}}, nil, t0); err != nil {
		t.Fatal(err)
	}

	for i := 1; i <= 2; i++ {
		if err := db.RecordDiscoveryFailure(ctx, "p", t0, "dial timeout"); err != nil {
			t.Fatal(err)
		}
		if state, _ := modelState(t, db, "a"); state != "live" {
			t.Fatalf("after %d failures state = %q, want live", i, state)
		}
	}
	if err := db.RecordDiscoveryFailure(ctx, "p", t0, "dial timeout"); err != nil {
		t.Fatal(err)
	}
	state, streak := modelState(t, db, "a")
	if state != "stale" {
		t.Errorf("after 3 failures state = %q, want stale", state)
	}
	// A failure is not evidence of removal, so the omission counter must not
	// have moved.
	if streak != 0 {
		t.Errorf("missing_streak = %d after failures only, want 0", streak)
	}

	// A fourth failure must not escalate stale to removed.
	if err := db.RecordDiscoveryFailure(ctx, "p", t0, "dial timeout"); err != nil {
		t.Fatal(err)
	}
	if state, _ := modelState(t, db, "a"); state != "stale" {
		t.Errorf("after 4 failures state = %q, want stale", state)
	}
}

func TestThreeOmissionsRemoveButOneDoesNot(t *testing.T) {
	db, ctx := lifecycleDB(t)
	if err := db.RecordDiscoverySuccess(ctx, "p", []DiscoveredModel{{ModelID: "a"}, {ModelID: "b"}}, nil, t0); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 2; i++ {
		if err := db.RecordDiscoverySuccess(ctx, "p", []DiscoveredModel{{ModelID: "a"}}, nil, t0); err != nil {
			t.Fatal(err)
		}
		state, streak := modelState(t, db, "b")
		if state != "live" || streak != i {
			t.Fatalf("after %d omissions b = (%q, %d)", i, state, streak)
		}
	}
	if err := db.RecordDiscoverySuccess(ctx, "p", []DiscoveredModel{{ModelID: "a"}}, nil, t0); err != nil {
		t.Fatal(err)
	}
	if state, streak := modelState(t, db, "b"); state != "removed_upstream" || streak != 3 {
		t.Errorf("after 3 omissions b = (%q, %d)", state, streak)
	}
	if state, _ := modelState(t, db, "a"); state != "live" {
		t.Errorf("a = %q; a listed model was affected by its neighbour", state)
	}
}

func TestReappearanceClearsBothCounters(t *testing.T) {
	// Spec §5.1: recovery clears both. A model that comes back must route
	// again, and a provider that recovers must not carry its failure count
	// into the next outage.
	db, ctx := lifecycleDB(t)
	if err := db.RecordDiscoverySuccess(ctx, "p", []DiscoveredModel{{ModelID: "a"}, {ModelID: "b"}}, nil, t0); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := db.RecordDiscoverySuccess(ctx, "p", []DiscoveredModel{{ModelID: "a"}}, nil, t0); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.RecordDiscoveryFailure(ctx, "p", t0, "boom"); err != nil {
		t.Fatal(err)
	}
	if state, _ := modelState(t, db, "b"); state != "removed_upstream" {
		t.Fatalf("setup failed: b = %q", state)
	}

	if err := db.RecordDiscoverySuccess(ctx, "p", []DiscoveredModel{{ModelID: "a"}, {ModelID: "b"}}, nil, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if state, streak := modelState(t, db, "b"); state != "live" || streak != 0 {
		t.Errorf("after reappearance b = (%q, %d), want (live, 0)", state, streak)
	}
	states, err := db.DiscoveryStates(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if states["p"].ConsecutiveFailures != 0 {
		t.Errorf("consecutive failures = %d after a success, want 0", states["p"].ConsecutiveFailures)
	}
	if !states["p"].LastSuccessAt.Equal(t0.Add(time.Hour)) {
		t.Errorf("last success = %v", states["p"].LastSuccessAt)
	}
}

func TestSuccessRestoresStaleModels(t *testing.T) {
	db, ctx := lifecycleDB(t)
	if err := db.RecordDiscoverySuccess(ctx, "p", []DiscoveredModel{{ModelID: "a"}}, nil, t0); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := db.RecordDiscoveryFailure(ctx, "p", t0, "boom"); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.RecordDiscoverySuccess(ctx, "p", []DiscoveredModel{{ModelID: "a"}}, nil, t0); err != nil {
		t.Fatal(err)
	}
	if state, _ := modelState(t, db, "a"); state != "live" {
		t.Errorf("state = %q after recovery, want live", state)
	}
}

func TestFailureCountIsPerProvider(t *testing.T) {
	db, ctx := lifecycleDB(t)
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO providers (id, kind, base_url, created_at) VALUES ('q', 'openaicompat', 'http://y', 0)`); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordDiscoverySuccess(ctx, "q", []DiscoveredModel{{ModelID: "z"}}, nil, t0); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordDiscoverySuccess(ctx, "p", []DiscoveredModel{{ModelID: "a"}}, nil, t0); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := db.RecordDiscoveryFailure(ctx, "p", t0, "boom"); err != nil {
			t.Fatal(err)
		}
	}
	var state string
	if err := db.Read.QueryRowContext(ctx,
		`SELECT state FROM models WHERE provider_id = 'q'`).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "live" {
		t.Errorf("provider q went %q because provider p failed", state)
	}
}

func TestDiscoveredCapabilitiesAreRecordedAsDiscovered(t *testing.T) {
	// Spec §6: a runtime that reports its own capabilities produces a fact,
	// not an inference, and the router filters on it.
	db, ctx := lifecycleDB(t)
	if err := db.RecordDiscoverySuccess(ctx, "p", []DiscoveredModel{
		{ModelID: "a", Capabilities: &ModelCapabilities{Tools: true}},
		{ModelID: "b"},
	}, nil, t0); err != nil {
		t.Fatal(err)
	}
	rows, _ := db.Models(ctx)
	for _, r := range rows {
		switch r.ModelID {
		case "a":
			if r.CapabilitiesSource != "discovered" || !r.Capabilities.Tools {
				t.Errorf("a = (%q, %+v)", r.CapabilitiesSource, r.Capabilities)
			}
		case "b":
			// A probe that reported nothing must not overwrite whatever the
			// sync knows with a confident-looking false.
			if r.CapabilitiesSource != "inferred" {
				t.Errorf("b source = %q, want inferred", r.CapabilitiesSource)
			}
		}
	}
}

func TestSuccessDoesNotClobberSyncedMetadata(t *testing.T) {
	// A probe that reports a bare id must leave models.dev's prices and limits
	// alone. Overwriting them with zeroes on every tick is a silent loss that
	// only shows up as an empty price column.
	db, ctx := lifecycleDB(t)
	if err := db.RecordDiscoverySuccess(ctx, "p", []DiscoveredModel{{ModelID: "a"}}, nil, t0); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertMetadata(ctx, []MetadataRow{{
		ProviderID: "p", ModelID: "a", ContextWindow: 200_000,
		InputMicrosPerMTok: 5_000_000, CapabilitiesSource: "models_dev",
		Capabilities: ModelCapabilities{Tools: true},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordDiscoverySuccess(ctx, "p", []DiscoveredModel{{ModelID: "a"}}, nil, t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	rows, _ := db.Models(ctx)
	r := rows[0]
	if r.ContextWindow != 200_000 || r.InputMicrosPerMTok != 5_000_000 {
		t.Errorf("discovery clobbered synced metadata: %+v", r)
	}
	if r.CapabilitiesSource != "models_dev" || !r.Capabilities.Tools {
		t.Errorf("discovery clobbered synced capabilities: (%q, %+v)", r.CapabilitiesSource, r.Capabilities)
	}
}

func TestFilteredModelsLeaveTheCatalogue(t *testing.T) {
	// A model the operator's import filter excluded is one the provider still
	// lists. Leaving it to the omission sweep would retire it as
	// removed_upstream -- a claim about the provider that is false -- and the
	// row would stay on screen as a model this provider offers.
	db, ctx := lifecycleDB(t)
	if err := db.RecordDiscoverySuccess(ctx, "p",
		[]DiscoveredModel{{ModelID: "free"}, {ModelID: "paid"}}, nil, t0); err != nil {
		t.Fatal(err)
	}

	if err := db.RecordDiscoverySuccess(ctx, "p",
		[]DiscoveredModel{{ModelID: "free"}}, []string{"paid"}, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	var n int
	if err := db.Read.QueryRowContext(ctx,
		`SELECT count(*) FROM models WHERE provider_id = 'p' AND model_id = 'paid'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("the filtered model still has %d rows, want none", n)
	}
	if state, streak := modelState(t, db, "free"); state != "live" || streak != 0 {
		t.Errorf("free = (%q, %d); the kept model was affected", state, streak)
	}
}

func TestWideningTheFilterReimportsTheModel(t *testing.T) {
	// Deleting rather than retiring loses nothing: an operator who turns the
	// filter off gets the model back on the next sweep, by the same path a
	// genuinely new model takes.
	db, ctx := lifecycleDB(t)
	if err := db.RecordDiscoverySuccess(ctx, "p",
		[]DiscoveredModel{{ModelID: "free"}}, []string{"paid"}, t0); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordDiscoverySuccess(ctx, "p",
		[]DiscoveredModel{{ModelID: "free"}, {ModelID: "paid"}}, nil, t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if state, streak := modelState(t, db, "paid"); state != "live" || streak != 0 {
		t.Errorf("paid = (%q, %d), want (live, 0)", state, streak)
	}
}

func TestFilteredCountIsWhatTheSweepDropped(t *testing.T) {
	db, ctx := lifecycleDB(t)
	if err := db.RecordDiscoverySuccess(ctx, "p",
		[]DiscoveredModel{{ModelID: "free"}}, []string{"a", "b", "c"}, t0); err != nil {
		t.Fatal(err)
	}
	var filtered int
	if err := db.Read.QueryRowContext(ctx,
		`SELECT filtered_out FROM provider_discovery WHERE provider_id = 'p'`).Scan(&filtered); err != nil {
		t.Fatal(err)
	}
	if filtered != 3 {
		t.Errorf("filtered_out = %d, want 3", filtered)
	}
}

func TestSuccessWritesPricesOnlyForPricedModels(t *testing.T) {
	db, ctx := lifecycleDB(t)

	// "kept" already carries a models.dev price. Its listing quotes none, so
	// the sweep must leave every one of its numbers alone.
	if err := db.RecordDiscoverySuccess(ctx, "p",
		[]DiscoveredModel{{ModelID: "kept"}, {ModelID: "priced"}, {ModelID: "free"}}, nil, t0); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertMetadata(ctx, []MetadataRow{{
		ProviderID: "p", ModelID: "kept",
		InputMicrosPerMTok: 3_000_000, OutputMicrosPerMTok: 15_000_000,
		PriceKnown: true, PriceSource: "models_dev",
	}}); err != nil {
		t.Fatal(err)
	}

	if err := db.RecordDiscoverySuccess(ctx, "p", []DiscoveredModel{
		{ModelID: "kept"},
		{ModelID: "priced", Pricing: &ModelPricing{
			InputMicrosPerMTok: 750_000, OutputMicrosPerMTok: 3_750_000,
			CacheReadMicrosPerMTok: int64Ptr(75_000), CacheWriteMicrosPerMTok: int64Ptr(41_667),
		}},
		{ModelID: "free", Pricing: &ModelPricing{}},
	}, nil, t0); err != nil {
		t.Fatal(err)
	}

	rows, err := db.Models(ctx)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]ModelRow{}
	for _, r := range rows {
		by[r.ModelID] = r
	}

	if got := by["kept"]; got.InputMicrosPerMTok != 3_000_000 ||
		got.OutputMicrosPerMTok != 15_000_000 || got.PriceSource != "models_dev" || !got.PriceKnown {
		t.Errorf("kept = %+v, want its models.dev price untouched", got)
	}

	got := by["priced"]
	if got.InputMicrosPerMTok != 750_000 || got.OutputMicrosPerMTok != 3_750_000 ||
		got.CacheReadMicrosPerMTok != 75_000 || got.CacheWriteMicrosPerMTok != 41_667 {
		t.Errorf("priced rates = %+v", got)
	}
	if !got.PriceKnown || got.PriceSource != "discovered" {
		t.Errorf("priced stamp = (%v, %q), want (true, \"discovered\")", got.PriceKnown, got.PriceSource)
	}

	// A free model is priced, not unpriced: its zeroes are what the seller
	// quoted, and price_known is what tells the two apart.
	if free := by["free"]; !free.PriceKnown || free.PriceSource != "discovered" ||
		free.InputMicrosPerMTok != 0 {
		t.Errorf("free = %+v, want a known zero price stamped discovered", free)
	}
}

func int64Ptr(v int64) *int64 { return &v }

func cacheColumns(t *testing.T, db *DB, id string) (sql.NullInt64, sql.NullInt64) {
	t.Helper()
	var read, write sql.NullInt64
	if err := db.Read.QueryRowContext(context.Background(),
		`SELECT cache_read_price_micros_per_mtok, cache_write_price_micros_per_mtok
		   FROM models WHERE provider_id = 'p' AND model_id = ?`, id).Scan(&read, &write); err != nil {
		t.Fatal(err)
	}
	return read, write
}

func TestSuccessLeavesAnUnquotedCacheRateAlone(t *testing.T) {
	db, ctx := lifecycleDB(t)
	if err := db.RecordDiscoverySuccess(ctx, "p",
		[]DiscoveredModel{{ModelID: "naga"}, {ModelID: "indexed"}}, nil, t0); err != nil {
		t.Fatal(err)
	}
	// "indexed" already carries models.dev's cache-write rate; "naga" has
	// never had one.
	if err := db.UpsertMetadata(ctx, []MetadataRow{{
		ProviderID: "p", ModelID: "indexed",
		InputMicrosPerMTok: 3_000_000, OutputMicrosPerMTok: 15_000_000,
		CacheWriteMicrosPerMTok: 3_750_000,
		PriceKnown:              true, PriceSource: "models_dev",
	}}); err != nil {
		t.Fatal(err)
	}

	// naga quotes no cache-write rate for any model.
	quoted := &ModelPricing{
		InputMicrosPerMTok: 75_000, OutputMicrosPerMTok: 235_000,
		CacheReadMicrosPerMTok: int64Ptr(8_000),
	}
	if err := db.RecordDiscoverySuccess(ctx, "p", []DiscoveredModel{
		{ModelID: "naga", Pricing: quoted},
		{ModelID: "indexed", Pricing: quoted},
	}, nil, t0); err != nil {
		t.Fatal(err)
	}

	// Migration 0016: an absent price and a zero price are different facts, and
	// a provider that publishes no cache-write rate must not read as free.
	read, write := cacheColumns(t, db, "naga")
	if !read.Valid || read.Int64 != 8_000 {
		t.Errorf("naga cache read = %+v, want 8000", read)
	}
	if write.Valid {
		t.Errorf("naga cache write = %d, want NULL", write.Int64)
	}

	// The index knew a cache-write rate this listing does not mention, so the
	// listing must not flatten it.
	if _, write = cacheColumns(t, db, "indexed"); !write.Valid || write.Int64 != 3_750_000 {
		t.Errorf("indexed cache write = %+v, want models.dev's 3750000 kept", write)
	}
}
