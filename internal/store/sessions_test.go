package store

import (
	"context"
	"testing"
	"time"
)

func TestSessionIDsAreHashedAtRest(t *testing.T) {
	// A copy of the database file must not carry every live cookie value.
	db := migrated(t)
	ctx := context.Background()
	if err := db.CreateSession(ctx, "raw-cookie-value", time.Hour); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.Read.QueryRowContext(ctx,
		`SELECT count(*) FROM sessions WHERE id = 'raw-cookie-value'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Error("the raw session id was stored")
	}
	if err := db.Read.QueryRowContext(ctx,
		`SELECT count(*) FROM sessions WHERE id = ?`, HashSessionID("raw-cookie-value")).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Error("the hashed session id was not stored")
	}
	rows, err := db.SessionRows(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != HashSessionID("raw-cookie-value") {
		t.Errorf("SessionRows = %+v, want the digest", rows)
	}
}

func TestTouchIsThrottled(t *testing.T) {
	// The console polls several endpoints every few seconds; an UPDATE per
	// poll would put a write on the single writer for each one.
	db := migrated(t)
	ctx := context.Background()
	if err := db.CreateSession(ctx, "s", time.Hour); err != nil {
		t.Fatal(err)
	}
	read := func() int64 {
		var v int64
		if err := db.Read.QueryRowContext(ctx,
			`SELECT expires_at FROM sessions WHERE id = ?`, HashSessionID("s")).Scan(&v); err != nil {
			t.Fatal(err)
		}
		return v
	}
	before := read()
	ok, err := db.TouchSession(ctx, "s", time.Hour)
	if err != nil || !ok {
		t.Fatalf("touch = %v, %v", ok, err)
	}
	if read() != before {
		t.Error("a touch within the interval rewrote the expiry")
	}
	// Backdated past the interval: the next touch must write.
	if _, err := db.Write.ExecContext(ctx,
		`UPDATE sessions SET expires_at = ? WHERE id = ?`,
		time.Now().Add(time.Hour-6*time.Minute).UnixMilli(), HashSessionID("s")); err != nil {
		t.Fatal(err)
	}
	stale := read()
	if _, err := db.TouchSession(ctx, "s", time.Hour); err != nil {
		t.Fatal(err)
	}
	if read() <= stale {
		t.Error("a touch past the interval did not slide the expiry")
	}
}

func TestASessionDiesAtItsAbsoluteAge(t *testing.T) {
	// Sliding alone lets a cookie exercised every few days live forever.
	db := migrated(t)
	ctx := context.Background()
	if err := db.CreateSession(ctx, "old", time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write.ExecContext(ctx,
		`UPDATE sessions SET created_at = ? WHERE id = ?`,
		time.Now().Add(-SessionMaxAge-time.Minute).UnixMilli(), HashSessionID("old")); err != nil {
		t.Fatal(err)
	}
	ok, err := db.TouchSession(ctx, "old", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("a session older than SessionMaxAge validated")
	}
	if rows, _ := db.SessionRows(ctx, time.Now()); len(rows) != 0 {
		t.Errorf("SessionRows listed an over-age session: %+v", rows)
	}
	n, err := db.SweepSessions(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("sweep removed %d rows, want the over-age one", n)
	}
}

func TestRevokeSessionTakesTheStoredID(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	if err := db.CreateSession(ctx, "victim", time.Hour); err != nil {
		t.Fatal(err)
	}
	if ok, err := db.RevokeSession(ctx, "nope"); err != nil || ok {
		t.Fatalf("revoke unknown = %v, %v", ok, err)
	}
	ok, err := db.RevokeSession(ctx, HashSessionID("victim"))
	if err != nil || !ok {
		t.Fatalf("revoke = %v, %v", ok, err)
	}
	if live, _ := db.TouchSession(ctx, "victim", time.Hour); live {
		t.Error("a revoked session still validates")
	}
}
