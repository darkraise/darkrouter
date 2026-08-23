package store

import (
	"context"
	"testing"
	"time"
)

func TestASessionRoundTrips(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	if err := db.CreateSession(ctx, "sess-1", time.Hour); err != nil {
		t.Fatal(err)
	}
	ok, err := db.TouchSession(ctx, "sess-1", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("a live session did not validate")
	}
}

func TestAnUnknownSessionIsAMissRatherThanAnError(t *testing.T) {
	// The two mean different things to the caller: a miss renders the login
	// screen, an error is a 500. Collapsing them makes an outage look like a
	// logout.
	db := migrated(t)
	ok, err := db.TouchSession(context.Background(), "never-existed", time.Hour)
	if err != nil {
		t.Fatalf("a miss was reported as an error: %v", err)
	}
	if ok {
		t.Error("an unknown session validated")
	}
}

func TestTouchExtendsTheExpiry(t *testing.T) {
	// Spec §3: the expiry slides. Without this an operator is logged out
	// thirty days after logging in regardless of use.
	db := migrated(t)
	ctx := context.Background()
	if err := db.CreateSession(ctx, "sess-2", time.Minute); err != nil {
		t.Fatal(err)
	}
	var before int64
	if err := db.Read.QueryRowContext(ctx,
		`SELECT expires_at FROM sessions WHERE id = 'sess-2'`).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := db.TouchSession(ctx, "sess-2", 48*time.Hour); err != nil {
		t.Fatal(err)
	}
	var after int64
	if err := db.Read.QueryRowContext(ctx,
		`SELECT expires_at FROM sessions WHERE id = 'sess-2'`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after <= before {
		t.Errorf("expiry did not slide: %d -> %d", before, after)
	}
}

func TestAnExpiredSessionDoesNotValidate(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	if err := db.CreateSession(ctx, "sess-3", -time.Minute); err != nil {
		t.Fatal(err)
	}
	ok, err := db.TouchSession(ctx, "sess-3", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("an expired session validated")
	}
}

func TestAnExpiredSessionIsNotResurrectedByTouch(t *testing.T) {
	// The expiry check lives in the UPDATE's WHERE. A read-then-write would
	// extend the row it just decided was dead.
	db := migrated(t)
	ctx := context.Background()
	if err := db.CreateSession(ctx, "sess-4", -time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := db.TouchSession(ctx, "sess-4", 48*time.Hour); err != nil {
		t.Fatal(err)
	}
	ok, err := db.TouchSession(ctx, "sess-4", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("an expired session came back to life")
	}
}

func TestDeleteSessionRemovesTheRow(t *testing.T) {
	// Spec §3: logout deletes the row rather than only clearing the cookie.
	// A cleared cookie leaves a valid session id in the database for anyone
	// who copied it.
	db := migrated(t)
	ctx := context.Background()
	if err := db.CreateSession(ctx, "sess-5", time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteSession(ctx, "sess-5"); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.Read.QueryRowContext(ctx,
		`SELECT count(*) FROM sessions WHERE id = 'sess-5'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d rows remain after logout", n)
	}
}

func TestSweepRemovesOnlyExpiredSessions(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	if err := db.CreateSession(ctx, "live", time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateSession(ctx, "dead", -time.Hour); err != nil {
		t.Fatal(err)
	}
	n, err := db.SweepSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("swept %d rows, want 1", n)
	}
	ok, _ := db.TouchSession(ctx, "live", time.Hour)
	if !ok {
		t.Error("the sweep removed a live session")
	}
}

func TestCreateAndReadAProvider(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	if err := db.CreateProvider(ctx, ProviderRow{
		ID: "p1", Name: "P One", Preset: "groq", Kind: "openaicompat",
		BaseURL: "https://x/v1", Priority: 7, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := db.ProviderRows(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != "p1" || rows[0].Preset != "groq" {
		t.Fatalf("rows = %+v", rows)
	}
	if rows[0].Priority != 7 || !rows[0].Enabled {
		t.Errorf("row = %+v", rows[0])
	}
	if rows[0].AuthStyle != "bearer" {
		t.Errorf("auth style = %q; a row created here must match one from the importer",
			rows[0].AuthStyle)
	}
}

func TestCreatingADuplicateProviderIsAnError(t *testing.T) {
	// The settings screen turns this into "that id is taken" rather than a
	// silent overwrite of a working provider.
	db := migrated(t)
	ctx := context.Background()
	p := ProviderRow{ID: "p1", Name: "P", Kind: "openaicompat", BaseURL: "https://x/v1"}
	if err := db.CreateProvider(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateProvider(ctx, p); err == nil {
		t.Error("a duplicate id was accepted")
	}
}

func TestUpdateTouchesOnlyWhatThePatchNames(t *testing.T) {
	// A value struct cannot tell "set priority to 0" from "leave it alone",
	// and 0 is a legal priority meaning last resort.
	db := migrated(t)
	ctx := context.Background()
	if err := db.CreateProvider(ctx, ProviderRow{
		ID: "p1", Name: "P", Kind: "openaicompat",
		BaseURL: "https://x/v1", Priority: 7, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	zero := 0
	if err := db.UpdateProvider(ctx, "p1", ProviderPatch{Priority: &zero}); err != nil {
		t.Fatal(err)
	}
	rows, _ := db.ProviderRows(ctx)
	if rows[0].Priority != 0 {
		t.Errorf("priority = %d, want 0", rows[0].Priority)
	}
	if rows[0].BaseURL != "https://x/v1" {
		t.Errorf("base url = %q; an untouched field changed", rows[0].BaseURL)
	}
	if !rows[0].Enabled {
		t.Error("enabled changed; the patch did not name it")
	}
}

func TestAnEmptyPatchIsAnError(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	if err := db.CreateProvider(ctx, ProviderRow{
		ID: "p1", Name: "P", Kind: "openaicompat", BaseURL: "https://x/v1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.UpdateProvider(ctx, "p1", ProviderPatch{}); err == nil {
		t.Error("an empty patch succeeded; the UI sent a form it did not fill in")
	}
}

func TestUpdatingAnUnknownProviderIsAnError(t *testing.T) {
	db := migrated(t)
	enabled := false
	if err := db.UpdateProvider(context.Background(), "nope",
		ProviderPatch{Enabled: &enabled}); err == nil {
		t.Error("patching a provider that does not exist succeeded")
	}
}

func TestDeleteCascadesToCredentialsAndModels(t *testing.T) {
	// A provider row without its credentials cannot serve; a credential
	// without its provider is a decryptable secret nobody can account for.
	// The schema's ON DELETE CASCADE does this, which foreign keys being on
	// makes real — this is the test that proves the pragma is actually set.
	db := migrated(t)
	ctx := context.Background()
	key, err := OpenKeyring(ctx, db, "master")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateProvider(ctx, ProviderRow{
		ID: "p1", Name: "P", Kind: "openaicompat", BaseURL: "https://x/v1", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddCredential(ctx, key, Credential{
		ProviderID: "p1", Label: "k", Secret: "sk-x", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO models (provider_id, model_id, state, last_seen_at)
		 VALUES ('p1','m','live',1)`); err != nil {
		t.Fatal(err)
	}

	if err := db.DeleteProvider(ctx, "p1"); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`SELECT count(*) FROM providers WHERE id = 'p1'`,
		`SELECT count(*) FROM provider_keys WHERE provider_id = 'p1'`,
		`SELECT count(*) FROM models WHERE provider_id = 'p1'`,
	} {
		var n int
		if err := db.Read.QueryRowContext(ctx, q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 0 {
			t.Errorf("%s left %d rows", q, n)
		}
	}
}

func TestDeletingAnUnknownProviderIsAnError(t *testing.T) {
	if err := migrated(t).DeleteProvider(context.Background(), "nope"); err == nil {
		t.Error("deleting a provider that does not exist succeeded")
	}
}

func TestDeleteCredentialLeavesTheProvider(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	key, err := OpenKeyring(ctx, db, "master")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CreateProvider(ctx, ProviderRow{
		ID: "p1", Name: "P", Kind: "openaicompat", BaseURL: "https://x/v1", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	id, err := db.AddCredential(ctx, key, Credential{
		ProviderID: "p1", Label: "k", Secret: "sk-x", Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteCredential(ctx, "p1", id); err != nil {
		t.Fatal(err)
	}
	creds, err := db.Credentials(ctx, key, "p1")
	if err != nil {
		t.Fatal(err)
	}
	if len(creds) != 0 {
		t.Errorf("credentials = %+v", creds)
	}
	rows, _ := db.ProviderRows(ctx)
	if len(rows) != 1 {
		t.Error("deleting a credential removed its provider")
	}
}
