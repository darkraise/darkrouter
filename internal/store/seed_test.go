package store

import (
	"context"
	"testing"
)

func offer(ids ...string) []SeedProvider {
	out := make([]SeedProvider, 0, len(ids))
	for _, id := range ids {
		out = append(out, SeedProvider{
			ID: id, Name: id, Kind: "openaicompat",
			BaseURL: "https://" + id + ".example/v1", AuthStyle: "optional",
			FreeModelsOnly: true,
		})
	}
	return out
}

func providerRow(t *testing.T, d *DB, id string) (style string, free int, ok bool) {
	t.Helper()
	err := d.Read.QueryRowContext(context.Background(),
		`SELECT auth_style, free_models_only FROM providers WHERE id = ?`, id).Scan(&style, &free)
	if err != nil {
		return "", 0, false
	}
	return style, free, true
}

func TestSeedingAddsAProviderThatNeedsNoCredential(t *testing.T) {
	db := migrated(t)
	res, err := SeedProviders(context.Background(), db, offer("aihorde", "opencode"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Added) != 2 {
		t.Fatalf("added = %v, want both", res.Added)
	}
	style, free, ok := providerRow(t, db, "aihorde")
	if !ok {
		t.Fatal("no row was written")
	}
	if style != "optional" {
		t.Errorf("auth_style = %q, want the preset's", style)
	}
	// The whole point of seeding one of these: it imports what an operator
	// holding no key can actually use.
	if free != 1 {
		t.Errorf("free_models_only = %d, want 1", free)
	}
}

func TestASeededProviderTheOperatorDeletedStaysDeleted(t *testing.T) {
	// The failure this guards against is a gateway that argues with its
	// operator: delete a provider, restart, and it is back.
	ctx := context.Background()
	db := migrated(t)
	if _, err := SeedProviders(ctx, db, offer("aihorde")); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write.ExecContext(ctx, `DELETE FROM providers WHERE id = 'aihorde'`); err != nil {
		t.Fatal(err)
	}

	res, err := SeedProviders(ctx, db, offer("aihorde"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Added) != 0 {
		t.Errorf("added = %v on the second run", res.Added)
	}
	if _, _, ok := providerRow(t, db, "aihorde"); ok {
		t.Error("the deleted provider came back")
	}
}

func TestSeedingLeavesAProviderTheOperatorAlreadyMade(t *testing.T) {
	// Same id, their settings. Overwriting it would silently rewrite a base
	// URL or a filter somebody chose.
	ctx := context.Background()
	db := migrated(t)
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO providers (id, preset, kind, base_url, auth_style, free_models_only, created_at)
		 VALUES ('opencode', 'opencode', 'openaicompat', 'https://mine.example/v1', 'bearer', 0, 0)`,
	); err != nil {
		t.Fatal(err)
	}

	res, err := SeedProviders(ctx, db, offer("opencode"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Added) != 0 {
		t.Errorf("added = %v over an existing provider", res.Added)
	}
	style, free, _ := providerRow(t, db, "opencode")
	if style != "bearer" || free != 0 {
		t.Errorf("row was rewritten: style=%q free=%d", style, free)
	}
	// And it is not reconsidered on the next start either, or deleting theirs
	// would hand them the release's version of the same id.
	if _, err := db.Write.ExecContext(ctx, `DELETE FROM providers WHERE id = 'opencode'`); err != nil {
		t.Fatal(err)
	}
	if res, err := SeedProviders(ctx, db, offer("opencode")); err != nil {
		t.Fatal(err)
	} else if len(res.Added) != 0 {
		t.Errorf("added = %v after the operator removed their own", res.Added)
	}
}

func TestAPresetThatBecomesSelfServingLaterIsOfferedThen(t *testing.T) {
	// The marker records what has been offered, not that seeding has run once:
	// a release that reclassifies a provider as needing no credential should
	// still be able to offer it.
	ctx := context.Background()
	db := migrated(t)
	if _, err := SeedProviders(ctx, db, offer("aihorde")); err != nil {
		t.Fatal(err)
	}
	res, err := SeedProviders(ctx, db, offer("aihorde", "uncloseai"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Added) != 1 || res.Added[0] != "uncloseai" {
		t.Fatalf("added = %v, want only the new one", res.Added)
	}
	if len(res.Skipped) != 1 || res.Skipped[0] != "aihorde" {
		t.Errorf("skipped = %v", res.Skipped)
	}
}

func TestSeedingNothingWritesNothing(t *testing.T) {
	ctx := context.Background()
	db := migrated(t)
	if _, err := SeedProviders(ctx, db, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := getSetting(ctx, db.Read, settingSeededProviders); err != nil {
		t.Fatal(err)
	} else if ok {
		t.Error("a marker was written for an empty offer")
	}
}
