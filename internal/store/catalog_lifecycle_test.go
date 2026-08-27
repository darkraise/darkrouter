package store

import (
	"context"
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
	}, 0, t0); err != nil {
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
	if err := db.RecordDiscoverySuccess(ctx, "p", []DiscoveredModel{{ModelID: "a"}}, 0, t0); err != nil {
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
	if err := db.RecordDiscoverySuccess(ctx, "p", []DiscoveredModel{{ModelID: "a"}, {ModelID: "b"}}, 0, t0); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 2; i++ {
		if err := db.RecordDiscoverySuccess(ctx, "p", []DiscoveredModel{{ModelID: "a"}}, 0, t0); err != nil {
			t.Fatal(err)
		}
		state, streak := modelState(t, db, "b")
		if state != "live" || streak != i {
			t.Fatalf("after %d omissions b = (%q, %d)", i, state, streak)
		}
	}
	if err := db.RecordDiscoverySuccess(ctx, "p", []DiscoveredModel{{ModelID: "a"}}, 0, t0); err != nil {
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
	if err := db.RecordDiscoverySuccess(ctx, "p", []DiscoveredModel{{ModelID: "a"}, {ModelID: "b"}}, 0, t0); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := db.RecordDiscoverySuccess(ctx, "p", []DiscoveredModel{{ModelID: "a"}}, 0, t0); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.RecordDiscoveryFailure(ctx, "p", t0, "boom"); err != nil {
		t.Fatal(err)
	}
	if state, _ := modelState(t, db, "b"); state != "removed_upstream" {
		t.Fatalf("setup failed: b = %q", state)
	}

	if err := db.RecordDiscoverySuccess(ctx, "p", []DiscoveredModel{{ModelID: "a"}, {ModelID: "b"}}, 0, t0.Add(time.Hour)); err != nil {
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
	if err := db.RecordDiscoverySuccess(ctx, "p", []DiscoveredModel{{ModelID: "a"}}, 0, t0); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := db.RecordDiscoveryFailure(ctx, "p", t0, "boom"); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.RecordDiscoverySuccess(ctx, "p", []DiscoveredModel{{ModelID: "a"}}, 0, t0); err != nil {
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
	if err := db.RecordDiscoverySuccess(ctx, "q", []DiscoveredModel{{ModelID: "z"}}, 0, t0); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordDiscoverySuccess(ctx, "p", []DiscoveredModel{{ModelID: "a"}}, 0, t0); err != nil {
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
	}, 0, t0); err != nil {
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
	if err := db.RecordDiscoverySuccess(ctx, "p", []DiscoveredModel{{ModelID: "a"}}, 0, t0); err != nil {
		t.Fatal(err)
	}
	if err := db.UpsertMetadata(ctx, []MetadataRow{{
		ProviderID: "p", ModelID: "a", ContextWindow: 200_000,
		InputMicrosPerMTok: 5_000_000, CapabilitiesSource: "models_dev",
		Capabilities: ModelCapabilities{Tools: true},
	}}); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordDiscoverySuccess(ctx, "p", []DiscoveredModel{{ModelID: "a"}}, 0, t0.Add(time.Minute)); err != nil {
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
