package store

import (
	"context"
	"errors"
	"testing"

	"github.com/darkraise/darkrouter/internal/config"
)

func cfgWithProviders(ids ...string) *config.Config {
	c := &config.Config{}
	for _, id := range ids {
		c.Providers = append(c.Providers, config.ProviderConfig{
			ID: id, Kind: "openaicompat", BaseURL: "https://" + id + ".example/v1",
			APIKey: "sk-" + id, Priority: 7, Models: []string{id + "-model"},
		})
	}
	return c
}

func TestImportRunsOnAVirginDatabase(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	key, err := OpenKeyring(ctx, db, "master")
	if err != nil {
		t.Fatal(err)
	}

	res, err := ImportFromConfig(ctx, db, key, cfgWithProviders("groq", "cerebras"))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Imported || res.Providers != 2 {
		t.Fatalf("result = %+v", res)
	}

	var n int
	if err := db.Read.QueryRowContext(ctx, `SELECT count(*) FROM providers`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("providers = %d, want 2", n)
	}
	if err := db.Read.QueryRowContext(ctx, `SELECT count(*) FROM models`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("models = %d, want 2", n)
	}

	creds, err := db.Credentials(ctx, key, "groq")
	if err != nil {
		t.Fatal(err)
	}
	if len(creds) != 1 || creds[0].Secret != "sk-groq" {
		t.Errorf("credentials = %+v", creds)
	}

	if _, ok, err := ImportedAt(ctx, db); err != nil || !ok {
		t.Errorf("marker not written: ok=%v err=%v", ok, err)
	}
}

func TestImportDoesNotRunTwice(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	key, _ := OpenKeyring(ctx, db, "master")

	if _, err := ImportFromConfig(ctx, db, key, cfgWithProviders("groq")); err != nil {
		t.Fatal(err)
	}
	res, err := ImportFromConfig(ctx, db, key, cfgWithProviders("groq", "new"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Imported {
		t.Fatal("import ran a second time")
	}
	var n int
	if err := db.Read.QueryRowContext(ctx, `SELECT count(*) FROM providers`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("providers = %d, want 1 — the second import must not add rows", n)
	}
}

func TestImportSkipsWhenProvidersTableIsNotEmpty(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	key, _ := OpenKeyring(ctx, db, "master")
	seededProvider(t, db, "already-here")

	res, err := ImportFromConfig(ctx, db, key, cfgWithProviders("groq"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Imported {
		t.Fatal("import ran against a populated providers table")
	}
}

func TestImportSkipsWhenConfigHasNoProviders(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	key, _ := OpenKeyring(ctx, db, "master")

	res, err := ImportFromConfig(ctx, db, key, &config.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if res.Imported {
		t.Fatal("import ran with no providers: block")
	}
	if _, ok, _ := ImportedAt(ctx, db); ok {
		t.Fatal("a marker was written even though nothing was imported")
	}
}

// A crash mid-import must leave nothing behind. Otherwise the partial rows
// falsify the empty-table guard and the remaining providers are stranded
// forever, with no error to explain why half of them are missing.
func TestImportIsAtomicUnderFailure(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	key, _ := OpenKeyring(ctx, db, "master")

	boom := errors.New("simulated crash")
	_, err := importWithHook(ctx, db, key, cfgWithProviders("a", "b", "c"), func(i int) error {
		if i == 1 {
			return boom
		}
		return nil
	})
	if !errors.Is(err, boom) {
		t.Fatalf("expected the injected failure, got %v", err)
	}

	var n int
	if err := db.Read.QueryRowContext(ctx, `SELECT count(*) FROM providers`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("providers = %d after a failed import, want 0", n)
	}
	if err := db.Read.QueryRowContext(ctx, `SELECT count(*) FROM provider_keys`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("provider_keys = %d after a failed import, want 0", n)
	}
	if _, ok, _ := ImportedAt(ctx, db); ok {
		t.Error("the marker survived a failed import")
	}
}

func TestImportAbortsOnAnEmptyCredential(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	key, _ := OpenKeyring(ctx, db, "master")

	cfg := cfgWithProviders("groq", "broken")
	cfg.Providers[1].APIKey = "" // an unresolved ${ENV} reduces to this

	_, err := ImportFromConfig(ctx, db, key, cfg)
	if err == nil {
		t.Fatal("expected an empty credential to abort the import")
	}
	var n int
	if err := db.Read.QueryRowContext(ctx, `SELECT count(*) FROM providers`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("providers = %d, want 0 — an aborted import imports nothing", n)
	}
}

func TestImportWritesThePresetColumn(t *testing.T) {
	// providers.preset has existed since migration 0001 and nothing has ever
	// written it, so the catalog join and the rerank path both saw "".
	db := migrated(t)
	ctx := context.Background()
	key, err := OpenKeyring(ctx, db, "master")
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{Providers: []config.ProviderConfig{{
		ID: "cohere", Kind: "openaicompat", Preset: "cohere",
		BaseURL: "https://api.cohere.com/compatibility/v1", APIKey: "sk",
		Models: []string{"rerank-v3.5"},
	}}}
	if _, err := ImportFromConfig(ctx, db, key, cfg); err != nil {
		t.Fatal(err)
	}
	var got string
	if err := db.Read.QueryRowContext(ctx,
		`SELECT preset FROM providers WHERE id = 'cohere'`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != "cohere" {
		t.Errorf("preset = %q", got)
	}
}
