package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/store"
)

// seedModelRow inserts a model row, creating its provider first: models.provider_id
// is a foreign key, so a provider-less insert would fail before the state we're
// testing ever comes into play.
func seedModelRow(t *testing.T, db *store.DB, providerID, modelID, state string, streak int) {
	t.Helper()
	ctx := context.Background()
	var exists int
	if err := db.Read.QueryRowContext(ctx,
		`SELECT count(*) FROM providers WHERE id = ?`, providerID).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists == 0 {
		if err := db.CreateProvider(ctx, store.ProviderRow{
			ID: providerID, Name: providerID, Kind: "openaicompat",
			BaseURL: "https://" + providerID + "/v1", Enabled: true,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO models (provider_id, model_id, state, missing_streak)
		 VALUES (?, ?, ?, ?)`, providerID, modelID, state, streak); err != nil {
		t.Fatal(err)
	}
}

// Named apart from the handler's discoveryHealthView: both live in package
// admin, and one name would not compile.
type discoveryRollup struct {
	ProviderID       string `json:"provider_id"`
	Total            int    `json:"total"`
	Live             int    `json:"live"`
	Stale            int    `json:"stale"`
	RemovedUpstream  int    `json:"removed_upstream"`
	MaxMissingStreak int    `json:"max_missing_streak"`
	FilteredOut      int    `json:"filtered_out"`
}

func discoveryHealth(t *testing.T, s *Server) []discoveryRollup {
	t.Helper()
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "GET", "/api/health/discovery", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/health/discovery = %d: %s", w.Code, w.Body.String())
	}
	var out struct {
		Providers []discoveryRollup `json:"providers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v\n%s", err, w.Body.String())
	}
	return out.Providers
}

func TestDiscoveryHealthRollsUpPerProvider(t *testing.T) {
	s, db := testServerFull(t)
	seedModelRow(t, db, "groq", "m1", "live", 0)
	seedModelRow(t, db, "groq", "m2", "live", 0)
	seedModelRow(t, db, "groq", "m3", "stale", 3)
	seedModelRow(t, db, "nebius", "m4", "removed_upstream", 9)

	got := discoveryHealth(t, s)
	if len(got) != 2 {
		t.Fatalf("want two providers, got %+v", got)
	}
	// Sorted by id: a map iteration order would reshuffle the panel on every
	// poll, which reads as the numbers changing.
	if got[0].ProviderID != "groq" || got[1].ProviderID != "nebius" {
		t.Fatalf("not sorted by provider: %+v", got)
	}
	g := got[0]
	if g.Total != 3 || g.Live != 2 || g.Stale != 1 || g.MaxMissingStreak != 3 {
		t.Fatalf("groq rollup wrong: %+v", g)
	}
	if got[1].RemovedUpstream != 1 || got[1].MaxMissingStreak != 9 {
		t.Fatalf("nebius rollup wrong: %+v", got[1])
	}
}

func TestDiscoveryHealthIsEmptyBeforeAnySweep(t *testing.T) {
	// An empty array, not null: a provider with no catalogued rows has never
	// been discovered, and a zero row would claim a sweep had run and found
	// nothing.
	s, _ := testServerFull(t)
	if got := discoveryHealth(t, s); len(got) != 0 {
		t.Fatalf("want an empty list, got %+v", got)
	}
}

func TestAWhollyFilteredSweepIsNotSilence(t *testing.T) {
	// The case the filtered count exists for. A provider whose every model is
	// paid, swept under the free-models filter, imports nothing — and before
	// the count it dropped out of the rollup entirely, reading exactly like a
	// provider discovery had never visited. Those need different fixes.
	s, db := testServerFull(t)
	ctx := context.Background()
	if err := db.CreateProvider(ctx, store.ProviderRow{
		ID: "paid-only", Kind: "openaicompat", BaseURL: "https://x.example",
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.RecordDiscoverySuccess(ctx, "paid-only", nil, 12, time.Now()); err != nil {
		t.Fatal(err)
	}

	got := discoveryHealth(t, s)
	if len(got) != 1 {
		t.Fatalf("want one rollup for a provider that swept, got %+v", got)
	}
	if got[0].ProviderID != "paid-only" || got[0].Total != 0 || got[0].FilteredOut != 12 {
		t.Fatalf("rollup wrong: %+v", got[0])
	}
}

func TestDiscoveryHealthRequiresASession(t *testing.T) {
	s, _ := testServerFull(t)
	r := httptest.NewRequest("GET", "/api/health/discovery", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", w.Code)
	}
}
