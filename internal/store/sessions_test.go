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
	uid := seedUser(t, db, "u1", "alice")
	if err := db.CreateSession(ctx, "raw-cookie-value", uid, time.Hour); err != nil {
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
	rows, err := db.SessionRows(ctx, uid, time.Now())
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
	uid := seedUser(t, db, "u1", "alice")
	if err := db.CreateSession(ctx, "s", uid, time.Hour); err != nil {
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
	_, ok, err := db.TouchSession(ctx, "s", time.Hour)
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
	if _, _, err := db.TouchSession(ctx, "s", time.Hour); err != nil {
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
	uid := seedUser(t, db, "u1", "alice")
	if err := db.CreateSession(ctx, "old", uid, time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write.ExecContext(ctx,
		`UPDATE sessions SET created_at = ? WHERE id = ?`,
		time.Now().Add(-SessionMaxAge-time.Minute).UnixMilli(), HashSessionID("old")); err != nil {
		t.Fatal(err)
	}
	_, ok, err := db.TouchSession(ctx, "old", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("a session older than SessionMaxAge validated")
	}
	if rows, _ := db.SessionRows(ctx, uid, time.Now()); len(rows) != 0 {
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
	uid := seedUser(t, db, "u1", "alice")
	if err := db.CreateSession(ctx, "victim", uid, time.Hour); err != nil {
		t.Fatal(err)
	}
	if ok, err := db.RevokeSession(ctx, uid, "nope"); err != nil || ok {
		t.Fatalf("revoke unknown = %v, %v", ok, err)
	}
	ok, err := db.RevokeSession(ctx, uid, HashSessionID("victim"))
	if err != nil || !ok {
		t.Fatalf("revoke = %v, %v", ok, err)
	}
	if _, live, _ := db.TouchSession(ctx, "victim", time.Hour); live {
		t.Error("a revoked session still validates")
	}
}

// seedUser makes an account to own a session.
func seedUser(t *testing.T, db *DB, id, name string) string {
	t.Helper()
	if _, err := db.ClaimFirstUser(context.Background(), id, name, "hash"); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestTouchSessionReturnsTheOwner(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	uid := seedUser(t, db, "u1", "alice")
	if err := db.CreateSession(ctx, "cookie-1", uid, time.Hour); err != nil {
		t.Fatal(err)
	}
	got, ok, err := db.TouchSession(ctx, "cookie-1", time.Hour)
	if err != nil || !ok {
		t.Fatalf("TouchSession: %v ok=%v", err, ok)
	}
	if got != uid {
		t.Errorf("owner = %q, want %q", got, uid)
	}
}

func TestTouchSessionFailsClosedWhenTheOwnerIsGone(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	uid := seedUser(t, db, "u1", "alice")
	if err := db.CreateSession(ctx, "cookie-1", uid, time.Hour); err != nil {
		t.Fatal(err)
	}
	// Foreign keys make this unreachable in normal running. A restored backup
	// that de-synced the two tables is where "unreachable" stops being true,
	// and the guard must answer 401 rather than hand a handler an empty owner.
	//
	// PRAGMA foreign_keys is per-connection, so both statements go down one
	// connection taken from the pool rather than two Execs that may land on
	// different ones.
	conn, err := db.Write.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys=off`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, uid); err != nil {
		t.Fatal(err)
	}
	_, ok, err := db.TouchSession(ctx, "cookie-1", time.Hour)
	if err != nil {
		t.Fatalf("an orphaned session must not be an error: %v", err)
	}
	if ok {
		t.Error("ok = true for a session whose owner no longer exists")
	}
}

func TestSessionRowsAreScopedToOneOwner(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	a := seedUser(t, db, "u1", "alice")
	// A second account, created directly: ClaimFirstUser only makes the first.
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO users (id, username, username_lc, password_hash, role, created_at)
		 VALUES ('u2','bob','bob','hash','member',0)`); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateSession(ctx, "cookie-a", a, time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateSession(ctx, "cookie-b", "u2", time.Hour); err != nil {
		t.Fatal(err)
	}
	rows, err := db.SessionRows(ctx, a, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1: a listing shows only the caller's own sessions", len(rows))
	}
	if rows[0].UserID != a {
		t.Errorf("owner = %q, want %q", rows[0].UserID, a)
	}
}

func TestRevokeSessionCannotReachAnotherAccount(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	a := seedUser(t, db, "u1", "alice")
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO users (id, username, username_lc, password_hash, role, created_at)
		 VALUES ('u2','bob','bob','hash','member',0)`); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateSession(ctx, "cookie-b", "u2", time.Hour); err != nil {
		t.Fatal(err)
	}
	gone, err := db.RevokeSession(ctx, a, HashSessionID("cookie-b"))
	if err != nil {
		t.Fatal(err)
	}
	if gone {
		t.Error("one account revoked another account's session")
	}
}

func TestDeleteSessionsExceptSparesOtherAccounts(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	a := seedUser(t, db, "u1", "alice")
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO users (id, username, username_lc, password_hash, role, created_at)
		 VALUES ('u2','bob','bob','hash','member',0)`); err != nil {
		t.Fatal(err)
	}
	for _, s := range []struct{ id, uid string }{
		{"a1", a}, {"a2", a}, {"b1", "u2"},
	} {
		if err := db.CreateSession(ctx, s.id, s.uid, time.Hour); err != nil {
			t.Fatal(err)
		}
	}
	n, err := db.DeleteSessionsExcept(ctx, a, "a1")
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("revoked %d, want 1: only the caller's other sessions", n)
	}
	if _, ok, _ := db.TouchSession(ctx, "b1", time.Hour); !ok {
		t.Error("another account's session was revoked by a password change")
	}
}

func TestDeletingAUserRevokesTheirSessions(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	uid := seedUser(t, db, "u1", "alice")
	if err := db.CreateSession(ctx, "cookie-1", uid, time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, uid); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.Read.QueryRowContext(ctx, `SELECT count(*) FROM sessions`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("sessions left = %d, want 0: the cascade must revoke them", n)
	}
}
