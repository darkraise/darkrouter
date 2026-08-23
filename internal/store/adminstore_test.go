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
