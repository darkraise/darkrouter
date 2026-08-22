package provider

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/crypto"
	"github.com/darkraise/darkrouter/internal/store"
)

func newTestDB(t *testing.T) (*store.DB, *crypto.Key) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx := context.Background()
	if err := db.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	key, err := store.OpenKeyring(ctx, db, "master")
	if err != nil {
		t.Fatal(err)
	}
	return db, key
}

func seed(t *testing.T, db *store.DB, key *crypto.Key, id string, priority int, enabled bool, models ...string) string {
	t.Helper()
	ctx := context.Background()
	on := 0
	if enabled {
		on = 1
	}
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO providers (id, kind, base_url, priority, enabled, created_at)
		 VALUES (?, 'openaicompat', ?, ?, ?, 0)`,
		id, "https://"+id+".example/v1", priority, on); err != nil {
		t.Fatal(err)
	}
	for _, m := range models {
		if _, err := db.Write.ExecContext(ctx,
			`INSERT INTO models (provider_id, model_id) VALUES (?, ?)`, id, m); err != nil {
			t.Fatal(err)
		}
	}
	keyID, err := db.AddCredential(ctx, key, store.Credential{
		ProviderID: id, Secret: "sk-" + id, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	return keyID
}

func TestSQLSourceLoadsProvidersWithDecryptedCredentials(t *testing.T) {
	db, key := newTestDB(t)
	keyID := seed(t, db, key, "groq", 10, true, "model-a", "model-b")

	src := NewSQLSource(db, key)
	if err := src.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	ps, err := src.Providers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 1 {
		t.Fatalf("got %d providers, want 1", len(ps))
	}
	p := ps[0]
	if p.ID != "groq" {
		t.Errorf("provider = %+v", p)
	}
	if len(p.Credentials) != 1 || p.Credentials[0].ID != keyID || p.Credentials[0].Secret != "sk-groq" {
		t.Errorf("credentials = %+v", p.Credentials)
	}
	if p.Priority != 10 || p.BaseURL != "https://groq.example/v1" {
		t.Errorf("provider = %+v", p)
	}
	if len(p.Models) != 2 || p.Models[0] != "model-a" || p.Models[1] != "model-b" {
		t.Errorf("models = %v", p.Models)
	}
}

func TestSQLSourceSkipsDisabledProviders(t *testing.T) {
	db, key := newTestDB(t)
	seed(t, db, key, "on", 0, true, "m")
	seed(t, db, key, "off", 0, false, "m")

	src := NewSQLSource(db, key)
	if err := src.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	ps, _ := src.Providers(context.Background())
	if len(ps) != 1 || ps[0].ID != "on" {
		t.Fatalf("got %+v, want only the enabled provider", ps)
	}
}

// A provider with no usable credential cannot serve. Including it would make
// every request against it fail at the transport layer instead of being skipped.
func TestSQLSourceSkipsProvidersWithNoEnabledCredential(t *testing.T) {
	db, key := newTestDB(t)
	ctx := context.Background()
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO providers (id, kind, base_url, created_at) VALUES ('bare', 'openaicompat', 'https://x', 0)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO models (provider_id, model_id) VALUES ('bare', 'm')`); err != nil {
		t.Fatal(err)
	}

	src := NewSQLSource(db, key)
	if err := src.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	ps, _ := src.Providers(ctx)
	if len(ps) != 0 {
		t.Fatalf("got %+v, want no providers", ps)
	}
}

func TestSQLSourceReportsAnUndecryptableCredential(t *testing.T) {
	db, key := newTestDB(t)
	keyID := seed(t, db, key, "groq", 0, true, "m")
	// Corrupt the ciphertext. This must fail loudly at load time, not present
	// as a provider that mysteriously fails every request.
	if _, err := db.Write.ExecContext(context.Background(),
		`UPDATE provider_keys SET ciphertext = x'deadbeef' WHERE id = ?`, keyID); err != nil {
		t.Fatal(err)
	}

	src := NewSQLSource(db, key)
	err := src.Reload(context.Background())
	if err == nil {
		t.Fatal("expected a corrupt credential to fail the load")
	}
	if !strings.Contains(err.Error(), "groq") {
		t.Errorf("the error should name the provider, got: %v", err)
	}
}

func TestSQLSourceRevisionChangesWithTheProviderSet(t *testing.T) {
	db, key := newTestDB(t)
	seed(t, db, key, "groq", 0, true, "m")
	ctx := context.Background()

	src := NewSQLSource(db, key)
	if err := src.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	before := src.Revision()

	// A reload with no change must keep the revision stable, or every consumer
	// cache is invalidated on every reload.
	if err := src.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	if src.Revision() != before {
		t.Error("revision changed without a change to the provider set")
	}

	seed(t, db, key, "second", 0, true, "m2")
	if err := src.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	if src.Revision() == before {
		t.Error("revision did not change after a provider was added")
	}
}

func TestSQLSourceProvidersBeforeReloadIsEmptyNotNil(t *testing.T) {
	db, key := newTestDB(t)
	src := NewSQLSource(db, key)
	ps, err := src.Providers(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ps == nil {
		t.Fatal("Providers must return an empty slice, not nil, before the first Reload")
	}
}

func TestSQLSourceLoadsEveryEnabledCredential(t *testing.T) {
	db, key := newTestDB(t)
	ctx := context.Background()
	first := seed(t, db, key, "groq", 0, true, "m")
	second, err := db.AddCredential(ctx, key, store.Credential{
		ProviderID: "groq", Secret: "sk-second", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}

	src := NewSQLSource(db, key)
	if err := src.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	ps, _ := src.Providers(ctx)
	if len(ps) != 1 {
		t.Fatalf("got %d providers, want 1", len(ps))
	}
	creds := ps[0].Credentials
	if len(creds) != 2 {
		t.Fatalf("got %d credentials, want 2", len(creds))
	}
	// Ordered by id, which is total and deterministic. It is NOT insertion
	// order: two ULIDs minted in the same millisecond share a timestamp prefix
	// and are separated only by their random component. Credential rotation
	// needs determinism, which id order gives; it does not need recency, which
	// the least-recently-used ordering supplies separately.
	if !sort.SliceIsSorted(creds, func(i, j int) bool { return creds[i].ID < creds[j].ID }) {
		t.Errorf("credentials are not in id order: %+v", creds)
	}
	byID := map[string]string{}
	for _, c := range creds {
		byID[c.ID] = c.Secret
	}
	if byID[first] != "sk-groq" {
		t.Errorf("first credential secret = %q", byID[first])
	}
	if byID[second] != "sk-second" {
		t.Errorf("second credential secret = %q", byID[second])
	}
}

func TestSQLSourceExcludesDisabledCredentials(t *testing.T) {
	db, key := newTestDB(t)
	ctx := context.Background()
	enabled := seed(t, db, key, "groq", 0, true, "m")
	if _, err := db.AddCredential(ctx, key, store.Credential{
		ProviderID: "groq", Secret: "sk-off", Enabled: false,
	}); err != nil {
		t.Fatal(err)
	}

	src := NewSQLSource(db, key)
	if err := src.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	ps, _ := src.Providers(ctx)
	if len(ps[0].Credentials) != 1 || ps[0].Credentials[0].ID != enabled {
		t.Errorf("credentials = %+v, want only the enabled one", ps[0].Credentials)
	}
}
