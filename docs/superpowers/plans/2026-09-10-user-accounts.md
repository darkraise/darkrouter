# User Accounts and First-Run Onboarding Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

> **Implementer assignments:** each task names its implementer agent in an
> `**Implementer:**` line. When executing with
> superpowers:subagent-driven-development, REQUIRED SUB-SKILL:
> dr-superpowers:dispatching-tiered-implementers. Under
> superpowers:executing-plans these lines are inert; ignore them.

**Goal:** Replace darkrouter's single shared admin password with named user accounts, where the first account created claims the console and becomes admin.

**Architecture:** A `users` table holds username and bcrypt hash. The `sessions` table gains a `user_id` foreign key, so every session knows whose it is. The route table is untouched — all 54 routes keep their existing guard, and identity is added underneath them rather than beside them. `DARKROUTER_ADMIN_PASSWORD_HASH`, the setup token, and the `hash-password` subcommand are deleted.

**Tech Stack:** Go 1.27, SQLite (STRICT tables, `modernc.org/sqlite`), React 19 + TypeScript + Vite, darkraise-ui 6.7.0, vitest.

**Spec:** [`../specs/2026-09-10-user-accounts-design.md`](../specs/2026-09-10-user-accounts-design.md)

## Global Constraints

- **Every Go command needs `export TMPDIR=/var/tmp/dr-build` and `export PATH=$PATH:/usr/local/go/bin`.** `/tmp` is a tmpfs that has crashed test workers.
- **Typography:** `text-sm` is the floor. Never `text-xs`, never a custom size. Only darkraise-ui's scale.
- **darkraise-ui is pinned to exactly `6.7.0`.** Never run `npm install`; `npm ci` only.
- **Tests must be able to fail.** Every test is proven by breaking the code and watching it go red, then restoring. A test that cannot fail is not done.
- **Password floor is 12 characters** (`minPasswordChars`, `internal/admin/sessionapi.go:25`). Ceiling is 72 bytes (`maxPasswordBytes`, `internal/admin/authapi.go:16`) because bcrypt reads no further.
- **One error message for every login failure.** Wrong username, wrong password and unclaimed console are indistinguishable in both wording and timing.
- **Commit messages:** `<type>(<scope>): <subject>`, subject ≤ 50 chars, imperative, no period.
- **English only**, in code, comments, tests and commits.

## Test helpers

The plan's tests are written against these. The first five already exist —
use them, do not invent parallels. The rest are added by Task 2 and Task 4 in
`internal/admin/fixtures_test.go`, and every later task consumes them.

**Already in the tree:**

| Helper | Where | Signature |
|---|---|---|
| `storetest.Migrated` | `internal/storetest` | `Migrated(t *testing.T) *store.DB` — a migrated, empty database |
| `testServer` | `internal/admin/fixtures_test.go` | the standard admin `*Server` |
| `do` | `internal/admin/fixtures_test.go:167` | `do(t, s, cookie *http.Cookie, token, method, path, body string) *httptest.ResponseRecorder` |
| `login` | `internal/admin/auth_test.go:54` | `login(t, s) (*http.Cookie, string)` — cookie and CSRF token |
| `loginAs` | `internal/admin/setupapi_test.go:32` | `loginAs(t, s, password string) (*http.Cookie, string)` |

Note that `internal/admin/auth_test.go` **already exists**; Task 4 adds to it
rather than creating it.

**Added by this plan.** Write these into `internal/admin/fixtures_test.go` as
part of Task 4, and into `internal/store/users_test.go` as part of Task 2:

```go
// mustHash is a bcrypt hash of password, or a fatal error.
func mustHash(t *testing.T, password string) string {
	t.Helper()
	h, err := HashPassword(password)
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// newServerWithSession returns a server whose console is claimed by one admin
// account, that account's id, and a raw session cookie value for it.
func newServerWithSession(t *testing.T) (*Server, string, string) {
	t.Helper()
	s := testServer(t)
	const uid = "u1"
	if _, err := s.deps.DB.ClaimFirstUser(t.Context(), uid, "alice", mustHash(t, "correct-horse-battery")); err != nil {
		t.Fatal(err)
	}
	const cookie = "cookie-1"
	if err := s.deps.DB.CreateSession(t.Context(), cookie, uid, sessionTTL); err != nil {
		t.Fatal(err)
	}
	return s, uid, cookie
}

// seedMember adds a second, non-admin account with a live session, and returns
// its id and raw cookie value.
func seedMember(t *testing.T, s *Server) (string, string) {
	t.Helper()
	const uid = "u2"
	if err := s.deps.DB.CreateUser(t.Context(), uid, "bob", mustHash(t, "correct-horse-battery"), store.RoleMember); err != nil {
		t.Fatal(err)
	}
	const cookie = "other"
	if err := s.deps.DB.CreateSession(t.Context(), cookie, uid, sessionTTL); err != nil {
		t.Fatal(err)
	}
	return uid, cookie
}

// seedSecondAccountWithSession is seedMember where only the session matters.
func seedSecondAccountWithSession(t *testing.T, s *Server) { t.Helper(); seedMember(t, s) }

// newServerWithMemberSession returns a server whose console is already claimed
// by an admin, plus a session cookie belonging to a non-admin account.
func newServerWithMemberSession(t *testing.T) (*Server, string) {
	t.Helper()
	s, _, _ := newServerWithSession(t)
	_, cookie := seedMember(t, s)
	return s, cookie
}
```

`seedMember` uses `CreateUser`, which Task 14 adds. Tasks 9 and 10 therefore
seed their second account with the direct `INSERT INTO users` shown in their
own steps; from Task 15 onward, `seedMember` is available and is used instead.

**Request shorthands.** The plan writes `postJSON`, `getJSON`, `postJSONAs`,
`getJSONAs`, `deleteAs` and `postJSONCrossSite` for readability. Each is a thin
wrapper over the existing `do`:

```go
func postJSON(t *testing.T, s *Server, path, body string) *httptest.ResponseRecorder {
	return do(t, s, nil, "", "POST", path, body)
}
func getJSON(t *testing.T, s *Server, path string) *httptest.ResponseRecorder {
	return do(t, s, nil, "", "GET", path, "")
}
func postJSONAs(t *testing.T, s *Server, path, body, cookie string) *httptest.ResponseRecorder {
	c := &http.Cookie{Name: sessionCookie, Value: cookie}
	return do(t, s, c, s.csrf.Token(cookie), "POST", path, body)
}
func getJSONAs(t *testing.T, s *Server, path, cookie string) *httptest.ResponseRecorder {
	c := &http.Cookie{Name: sessionCookie, Value: cookie}
	return do(t, s, c, s.csrf.Token(cookie), "GET", path, "")
}
func deleteAs(t *testing.T, s *Server, path, cookie string) *httptest.ResponseRecorder {
	c := &http.Cookie{Name: sessionCookie, Value: cookie}
	return do(t, s, c, s.csrf.Token(cookie), "DELETE", path, "")
}
```

`postJSONCrossSite` is `do` with the `Sec-Fetch-Site: cross-site` header set;
`do` already takes the request apart, so add a variant beside it rather than
duplicating its body.

Task 8's `openPopulatedTestDB`, `openEmptyTestDB` and `dbPathOf` are new to
`cmd/darkrouter/main_test.go`, which currently has no helpers of its own:
`openEmptyTestDB` is `storetest.Migrated(t)`, `openPopulatedTestDB` is that
plus one provider row, and `dbPathOf` returns the database's file path.


## Phase map

| Phase | Tasks | Must land together? |
|---|---|---|
| 1 — flag day | 1–13 | **Yes.** Any intermediate state has two authorities deciding who may log in. |
| 2 — account management | 14–16 | No |
| 3 — documentation sweep | 17–18 | No |

Task 13 belongs to phase 1 rather than phase 3 because `deploy.md`'s recovery section becomes actively dangerous the moment phase 1 ships.

---

### Task 1: Migration for users and session ownership

**Files:**
- Create: `internal/store/migrations/0023_users.sql`
- Test: `internal/store/migrate_test.go` (add one test to the existing file)

**Interfaces:**
- Consumes: nothing.
- Produces: the `users` table (`id`, `username`, `username_lc`, `password_hash`, `role`, `created_at`) and `sessions.user_id`, which every later task in phase 1 depends on.

**Implementer:** dr-superpowers:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 0 - risk 3 = 4
**Approach:** best-of-3 - session owner column over anonymous sessions and a stateless signed token

- [ ] **Step 1: Write the failing test**

Add to `internal/store/migrate_test.go`:

```go
func TestMigration23CreatesUsersAndOwnsSessions(t *testing.T) {
	db := storetest.Migrated(t)

	// users exists with the columns the auth path needs
	var n int
	if err := db.Read.QueryRow(
		`SELECT count(*) FROM pragma_table_info('users')
		  WHERE name IN ('id','username','username_lc','password_hash','role','created_at')`,
	).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 6 {
		t.Errorf("users columns = %d, want 6", n)
	}

	// sessions carries an owner, NOT NULL, with a cascading foreign key
	var notNull int
	if err := db.Read.QueryRow(
		`SELECT "notnull" FROM pragma_table_info('sessions') WHERE name = 'user_id'`,
	).Scan(&notNull); err != nil {
		t.Fatalf("sessions.user_id missing: %v", err)
	}
	if notNull != 1 {
		t.Error("sessions.user_id is nullable; a session with no owner must be unrepresentable")
	}
	var onDelete string
	if err := db.Read.QueryRow(
		`SELECT "on_delete" FROM pragma_foreign_key_list('sessions') WHERE "table" = 'users'`,
	).Scan(&onDelete); err != nil {
		t.Fatalf("no foreign key from sessions to users: %v", err)
	}
	if onDelete != "CASCADE" {
		t.Errorf("on delete = %q, want CASCADE", onDelete)
	}

	// username_lc is unique, so two accounts cannot differ only by case
	var uniq int
	if err := db.Read.QueryRow(
		`SELECT count(*) FROM pragma_index_list('users') WHERE "unique" = 1`,
	).Scan(&uniq); err != nil {
		t.Fatal(err)
	}
	if uniq == 0 {
		t.Error("no unique index on users; username_lc must be unique")
	}
}

func TestMigration23DropsTheSharedPasswordRows(t *testing.T) {
	db := storetest.Migrated(t)
	if _, err := db.Write.Exec(
		`INSERT INTO settings (key, value) VALUES ('admin.password_hash', 'x')`); err != nil {
		t.Fatal(err)
	}
	// Re-running migrations must not resurrect it, and a fresh database must
	// never carry it. The row is deleted by the migration itself.
	var n int
	if err := db.Read.QueryRow(
		`SELECT count(*) FROM settings
		  WHERE key IN ('admin.password_hash','admin.password_env_fingerprint')`,
	).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("test setup wrong: got %d rows", n)
	}
}
```

- [ ] **Step 2: Run it to make sure it fails**

```bash
export TMPDIR=/var/tmp/dr-build; export PATH=$PATH:/usr/local/go/bin
go test ./internal/store/ -run TestMigration23 -count=1 -v
```

Expected: FAIL — `sessions.user_id missing: sql: no rows in result set`.

- [ ] **Step 3: Write the migration**

Create `internal/store/migrations/0023_users.sql`:

```sql
-- The console authenticated one shared password. It now authenticates named
-- accounts, and a session records which account it belongs to.
--
-- sessions is dropped and recreated rather than altered: SQLite cannot add a
-- NOT NULL column without a constant default to a non-empty table, and a
-- nullable owner would invent a second "session with nobody behind it" state
-- to guard at every read. Live sessions end here, as they did in 0017 --
-- operators log in again once.
CREATE TABLE users (
  id            TEXT    PRIMARY KEY,
  username      TEXT    NOT NULL,
  username_lc   TEXT    NOT NULL,
  password_hash TEXT    NOT NULL,
  role          TEXT    NOT NULL DEFAULT 'member',
  created_at    INTEGER NOT NULL
) STRICT;

-- Case-insensitive uniqueness through a column the application lowercases,
-- not COLLATE NOCASE: the collation folds ASCII only, and a rule the reader
-- can see beats one hidden in an index definition.
CREATE UNIQUE INDEX idx_users_username_lc ON users(username_lc);

DROP TABLE sessions;

CREATE TABLE sessions (
  id         TEXT    PRIMARY KEY,
  user_id    TEXT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL
) STRICT;

CREATE INDEX idx_sessions_user ON sessions(user_id);

-- The shared password and the environment fingerprint that tracked it are
-- both retired. Nothing reads them after this release.
DELETE FROM settings
 WHERE key IN ('admin.password_hash', 'admin.password_env_fingerprint');
```

- [ ] **Step 4: Run the tests and make sure they pass**

```bash
export TMPDIR=/var/tmp/dr-build; export PATH=$PATH:/usr/local/go/bin
go test ./internal/store/ -run TestMigration23 -count=1 -v
```

Expected: PASS, both tests.

- [ ] **Step 5: Prove the tests can fail**

Change `ON DELETE CASCADE` to `ON DELETE NO ACTION` in the migration, re-run, confirm `TestMigration23CreatesUsersAndOwnsSessions` goes red, then restore. Do the same by making `user_id` nullable.

- [ ] **Step 6: Confirm the whole store package still builds**

```bash
export TMPDIR=/var/tmp/dr-build; export PATH=$PATH:/usr/local/go/bin
go build ./... 2>&1 | head -20
```

Expected: failures in `internal/store` and `internal/admin` about `CreateSession` — the session table changed shape and the callers have not caught up. That is the flag day; tasks 3 and 4 close it. Do not fix them here.

- [ ] **Step 7: Commit**

```bash
git add internal/store/migrations/0023_users.sql internal/store/migrate_test.go
git commit -m "feat(store): add users and session ownership"
```

---

### Task 2: User store methods

**Files:**
- Create: `internal/store/users.go`, `internal/store/users_test.go`

**Interfaces:**
- Consumes: the `users` table from Task 1.
- Produces:
  - `type User struct { ID, Username, UsernameLC, PasswordHash, Role string; CreatedAt time.Time }`
  - `func (d *DB) UserCount(ctx context.Context) (int, error)`
  - `func (d *DB) UserByUsername(ctx context.Context, username string) (User, bool, error)`
  - `func (d *DB) UserByID(ctx context.Context, id string) (User, bool, error)`
  - `func (d *DB) ClaimFirstUser(ctx context.Context, id, username, hash string) (bool, error)`
  - `func (d *DB) SetUserPassword(ctx context.Context, id, hash string) error`
  - `const RoleAdmin = "admin"`, `const RoleMember = "member"`

**Implementer:** dr-superpowers:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 2 = 4

- [ ] **Step 1: Write the failing test**

Create `internal/store/users_test.go`:

```go
package store

import (
	"context"
	"testing"
)

func TestClaimFirstUserSucceedsOnlyOnce(t *testing.T) {
	db := storetest.Migrated(t)
	ctx := context.Background()

	won, err := db.ClaimFirstUser(ctx, "id-1", "Alice", "hash-1")
	if err != nil {
		t.Fatal(err)
	}
	if !won {
		t.Fatal("the first claim on an empty database must win")
	}

	// A second claim loses even with a different name: the claim is for the
	// console, not for the username.
	won, err = db.ClaimFirstUser(ctx, "id-2", "bob", "hash-2")
	if err != nil {
		t.Fatal(err)
	}
	if won {
		t.Error("a second claim won; the console can only be claimed once")
	}

	n, err := db.UserCount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("UserCount = %d, want 1", n)
	}
}

func TestClaimFirstUserIsAdmin(t *testing.T) {
	db := storetest.Migrated(t)
	ctx := context.Background()
	if _, err := db.ClaimFirstUser(ctx, "id-1", "alice", "hash-1"); err != nil {
		t.Fatal(err)
	}
	u, ok, err := db.UserByID(ctx, "id-1")
	if err != nil || !ok {
		t.Fatalf("UserByID: %v ok=%v", err, ok)
	}
	if u.Role != RoleAdmin {
		t.Errorf("role = %q, want %q", u.Role, RoleAdmin)
	}
}

func TestUserByUsernameIsCaseInsensitive(t *testing.T) {
	db := storetest.Migrated(t)
	ctx := context.Background()
	if _, err := db.ClaimFirstUser(ctx, "id-1", "Alice", "hash-1"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Alice", "alice", "ALICE", "aLiCe"} {
		u, ok, err := db.UserByUsername(ctx, name)
		if err != nil {
			t.Fatal(err)
		}
		if !ok {
			t.Fatalf("%q did not match the stored account", name)
		}
		if u.ID != "id-1" {
			t.Errorf("%q matched %q, want id-1", name, u.ID)
		}
	}
	// The display spelling is preserved, not folded.
	u, _, _ := db.UserByUsername(ctx, "alice")
	if u.Username != "Alice" {
		t.Errorf("Username = %q, want the spelling as entered", u.Username)
	}
}

func TestUserByUsernameMissIsNotAnError(t *testing.T) {
	db := storetest.Migrated(t)
	_, ok, err := db.UserByUsername(context.Background(), "nobody")
	if err != nil {
		t.Fatalf("a miss must not be an error: %v", err)
	}
	if ok {
		t.Error("ok = true for an account that does not exist")
	}
}

func TestSetUserPasswordReplacesTheHash(t *testing.T) {
	db := storetest.Migrated(t)
	ctx := context.Background()
	if _, err := db.ClaimFirstUser(ctx, "id-1", "alice", "old"); err != nil {
		t.Fatal(err)
	}
	if err := db.SetUserPassword(ctx, "id-1", "new"); err != nil {
		t.Fatal(err)
	}
	u, _, _ := db.UserByID(ctx, "id-1")
	if u.PasswordHash != "new" {
		t.Errorf("hash = %q, want %q", u.PasswordHash, "new")
	}
}
```

- [ ] **Step 2: Run it to make sure it fails**

```bash
export TMPDIR=/var/tmp/dr-build; export PATH=$PATH:/usr/local/go/bin
go test ./internal/store/ -run TestUser -count=1 -v
```

Expected: FAIL — `undefined: RoleAdmin`, `db.ClaimFirstUser undefined`.

- [ ] **Step 3: Write the implementation**

Create `internal/store/users.go`:

```go
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Roles. The column exists so account management has somewhere to record who
// may manage accounts; nothing in the request path reads it.
const (
	RoleAdmin  = "admin"
	RoleMember = "member"
)

// User is one console account.
type User struct {
	ID           string
	Username     string
	UsernameLC   string
	PasswordHash string
	Role         string
	CreatedAt    time.Time
}

// normalizeUsername is the case-folding rule, in one place. Go rather than
// SQLite's lower() or COLLATE NOCASE: both fold ASCII only, and a rule the
// reader can see beats one hidden in an index definition.
func normalizeUsername(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// UserCount reports how many accounts exist. Zero means the console has not
// been claimed, which is what puts the first-run screen in front of a visitor.
func (d *DB) UserCount(ctx context.Context) (int, error) {
	var n int
	if err := d.Read.QueryRowContext(ctx, `SELECT count(*) FROM users`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count users: %w", err)
	}
	return n, nil
}

// ClaimFirstUser creates the founding admin, and reports whether this caller
// is the one that created it.
//
// The emptiness test lives inside the INSERT rather than in a read followed by
// a write, so two browsers racing at first boot cannot both be told they won.
// SQLite serializes writers, so the loser's INSERT sees the winner's row and
// affects nothing.
func (d *DB) ClaimFirstUser(ctx context.Context, id, username, hash string) (bool, error) {
	res, err := d.Write.ExecContext(ctx,
		`INSERT INTO users (id, username, username_lc, password_hash, role, created_at)
		 SELECT ?, ?, ?, ?, ?, ?
		  WHERE NOT EXISTS (SELECT 1 FROM users)`,
		id, username, normalizeUsername(username), hash, RoleAdmin, time.Now().UnixMilli())
	if err != nil {
		return false, fmt.Errorf("claim first user: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("claim first user: %w", err)
	}
	return n > 0, nil
}

const userColumns = `id, username, username_lc, password_hash, role, created_at`

func scanUser(row *sql.Row) (User, bool, error) {
	var (
		u       User
		created int64
	)
	err := row.Scan(&u.ID, &u.Username, &u.UsernameLC, &u.PasswordHash, &u.Role, &created)
	if errors.Is(err, sql.ErrNoRows) {
		// A miss is not an error: an unknown username renders a login failure,
		// a database fault renders a 500, and the caller must tell them apart.
		return User{}, false, nil
	}
	if err != nil {
		return User{}, false, fmt.Errorf("scan user: %w", err)
	}
	u.CreatedAt = time.UnixMilli(created).UTC()
	return u, true, nil
}

// UserByUsername looks an account up by its folded name.
func (d *DB) UserByUsername(ctx context.Context, username string) (User, bool, error) {
	return scanUser(d.Read.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM users WHERE username_lc = ?`,
		normalizeUsername(username)))
}

// UserByID looks an account up by its id, which is what a session carries.
func (d *DB) UserByID(ctx context.Context, id string) (User, bool, error) {
	return scanUser(d.Read.QueryRowContext(ctx,
		`SELECT `+userColumns+` FROM users WHERE id = ?`, id))
}

// SetUserPassword replaces one account's hash.
func (d *DB) SetUserPassword(ctx context.Context, id, hash string) error {
	if _, err := d.Write.ExecContext(ctx,
		`UPDATE users SET password_hash = ? WHERE id = ?`, hash, id); err != nil {
		return fmt.Errorf("set user password: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run the tests and make sure they pass**

```bash
export TMPDIR=/var/tmp/dr-build; export PATH=$PATH:/usr/local/go/bin
go test ./internal/store/ -run TestUser -count=1 -v
```

Expected: PASS, all five tests.

- [ ] **Step 5: Prove the tests can fail**

Remove the `WHERE NOT EXISTS` clause from `ClaimFirstUser` and confirm `TestClaimFirstUserSucceedsOnlyOnce` goes red. Drop `normalizeUsername` from `UserByUsername` and confirm `TestUserByUsernameIsCaseInsensitive` goes red. Restore both.

- [ ] **Step 6: Commit**

```bash
git add internal/store/users.go internal/store/users_test.go
git commit -m "feat(store): add user account methods"
```

---

### Task 3: Session methods carry an owner

**Files:**
- Modify: `internal/store/sessions.go:35-44` (`CreateSession`), `:55-83` (`TouchSession`), `:99-109` (`RevokeSession`), `:131-135` (`SessionRow`), `:138-168` (`SessionRows`), `:171-182` (`DeleteSessionsExcept`)
- Test: `internal/store/sessions_test.go`

**Interfaces:**
- Consumes: `users` and `sessions.user_id` from Task 1; `ClaimFirstUser` from Task 2 to make owners in tests.
- Produces:
  - `func (d *DB) CreateSession(ctx context.Context, id, userID string, ttl time.Duration) error`
  - `func (d *DB) TouchSession(ctx context.Context, id string, ttl time.Duration) (userID string, ok bool, err error)`
  - `func (d *DB) SessionRows(ctx context.Context, userID string, now time.Time) ([]SessionRow, error)`
  - `func (d *DB) RevokeSession(ctx context.Context, userID, hashedID string) (bool, error)`
  - `func (d *DB) DeleteSessionsExcept(ctx context.Context, userID, keep string) (int, error)`
  - `SessionRow` gains `UserID string`.

**Implementer:** dr-superpowers:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 2 = 4

- [ ] **Step 1: Write the failing test**

Add to `internal/store/sessions_test.go`:

```go
// seedUser makes an account to own a session.
func seedUser(t *testing.T, db *DB, id, name string) string {
	t.Helper()
	if _, err := db.ClaimFirstUser(context.Background(), id, name, "hash"); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestTouchSessionReturnsTheOwner(t *testing.T) {
	db := storetest.Migrated(t)
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
	db := storetest.Migrated(t)
	ctx := context.Background()
	uid := seedUser(t, db, "u1", "alice")
	if err := db.CreateSession(ctx, "cookie-1", uid, time.Hour); err != nil {
		t.Fatal(err)
	}
	// Foreign keys make this unreachable in normal running. A restored backup
	// that de-synced the two tables is where "unreachable" stops being true,
	// and the guard must answer 401 rather than hand a handler an empty owner.
	if _, err := db.Write.ExecContext(ctx,
		`PRAGMA foreign_keys=off; DELETE FROM users WHERE id = ?`, uid); err != nil {
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
	db := storetest.Migrated(t)
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
	db := storetest.Migrated(t)
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
	db := storetest.Migrated(t)
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
	db := storetest.Migrated(t)
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
```

- [ ] **Step 2: Run it to make sure it fails**

```bash
export TMPDIR=/var/tmp/dr-build; export PATH=$PATH:/usr/local/go/bin
go test ./internal/store/ -run 'TestTouchSession|TestSessionRows|TestRevokeSession|TestDeleteSessions|TestDeletingAUser' -count=1 -v
```

Expected: FAIL to compile — `too many arguments in call to db.CreateSession`.

- [ ] **Step 3: Widen the signatures**

In `internal/store/sessions.go`, replace `CreateSession`:

```go
// CreateSession writes a new session row for one account. The caller mints the
// id; this does not generate one, because the id is a security-relevant value
// and the code that chooses its entropy should be the code that owns it.
func (d *DB) CreateSession(ctx context.Context, id, userID string, ttl time.Duration) error {
	now := time.Now()
	if _, err := d.Write.ExecContext(ctx,
		`INSERT INTO sessions (id, user_id, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		HashSessionID(id), userID, now.UnixMilli(), now.Add(ttl).UnixMilli()); err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}
```

Replace the read at the head of `TouchSession` so it returns the owner, and join `users` so an orphaned session fails closed:

```go
func (d *DB) TouchSession(ctx context.Context, id string, ttl time.Duration) (string, bool, error) {
	now := time.Now()
	hashed := HashSessionID(id)
	var (
		userID  string
		expires int64
	)
	// The join is what makes an orphaned row a 401 rather than a handler
	// holding an empty owner. ON DELETE CASCADE should make it unreachable;
	// a restored backup that de-synced the tables is where that stops being
	// true, and failing closed there costs nothing.
	err := d.Read.QueryRowContext(ctx,
		`SELECT s.user_id, s.expires_at FROM sessions s
		   JOIN users u ON u.id = s.user_id
		  WHERE s.id = ? AND s.expires_at > ? AND s.created_at > ?`,
		hashed, now.UnixMilli(), now.Add(-SessionMaxAge).UnixMilli()).Scan(&userID, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("touch session: %w", err)
	}
	if time.UnixMilli(expires).Sub(now) < ttl-sessionTouchInterval {
		if _, err := d.Write.ExecContext(ctx,
			`UPDATE sessions SET expires_at = ? WHERE id = ? AND expires_at > ?`,
			now.Add(ttl).UnixMilli(), hashed, now.UnixMilli()); err != nil {
			return "", false, fmt.Errorf("touch session: %w", err)
		}
	}
	return userID, true, nil
}
```

Add `UserID` to `SessionRow`, and scope the three owner-sensitive queries:

```go
type SessionRow struct {
	ID        string
	UserID    string
	CreatedAt time.Time
	ExpiresAt time.Time
}

// SessionRows lists one account's live sessions, newest first. Scoped to the
// caller: a listing that showed every account's sessions would let any user
// revoke any other's, which is a permission model nobody asked for.
func (d *DB) SessionRows(ctx context.Context, userID string, now time.Time) ([]SessionRow, error) {
	rows, err := d.Read.QueryContext(ctx,
		`SELECT id, user_id, created_at, expires_at FROM sessions
		  WHERE user_id = ? AND expires_at > ? AND created_at > ?
		  ORDER BY created_at DESC, id`,
		userID, now.UnixMilli(), now.Add(-SessionMaxAge).UnixMilli())
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []SessionRow{}
	for rows.Next() {
		var (
			r                SessionRow
			created, expires int64
		)
		if err := rows.Scan(&r.ID, &r.UserID, &created, &expires); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		r.CreatedAt = time.UnixMilli(created).UTC()
		r.ExpiresAt = time.UnixMilli(expires).UTC()
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	return out, nil
}

// RevokeSession removes one of the caller's own rows by its stored id. The
// owner is in the WHERE rather than checked by the handler: a store method
// should not depend on every caller remembering to scope it.
func (d *DB) RevokeSession(ctx context.Context, userID, hashedID string) (bool, error) {
	res, err := d.Write.ExecContext(ctx,
		`DELETE FROM sessions WHERE id = ? AND user_id = ?`, hashedID, userID)
	if err != nil {
		return false, fmt.Errorf("revoke session: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("revoke session: %w", err)
	}
	return n > 0, nil
}

// DeleteSessionsExcept revokes every other session belonging to one account.
// It is what a password change uses: anything that also revoked the caller
// would log the operator out of the screen they just used, and anything that
// reached other accounts would sign out people whose password did not change.
func (d *DB) DeleteSessionsExcept(ctx context.Context, userID, keep string) (int, error) {
	res, err := d.Write.ExecContext(ctx,
		`DELETE FROM sessions WHERE user_id = ? AND id <> ?`, userID, HashSessionID(keep))
	if err != nil {
		return 0, fmt.Errorf("revoke sessions: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("revoke sessions: %w", err)
	}
	return int(n), nil
}
```

`SweepSessions` and `DeleteSession` are unchanged: expiry is global, and logout already names one exact cookie.

- [ ] **Step 4: Run the store tests**

```bash
export TMPDIR=/var/tmp/dr-build; export PATH=$PATH:/usr/local/go/bin
go test ./internal/store/ -count=1
```

Expected: PASS. `internal/admin` still does not compile; task 4 closes that.

- [ ] **Step 5: Prove the tests can fail**

Drop `AND user_id = ?` from `RevokeSession` and confirm `TestRevokeSessionCannotReachAnotherAccount` goes red. Drop the `JOIN users` from `TouchSession` and confirm `TestTouchSessionFailsClosedWhenTheOwnerIsGone` goes red. Restore both.

- [ ] **Step 6: Commit**

```bash
git add internal/store/sessions.go internal/store/sessions_test.go
git commit -m "feat(store): scope sessions to an owner"
```

---

### Task 4: The guard learns the caller

**Files:**
- Modify: `internal/admin/auth.go:9-43`
- Test: `internal/admin/auth_test.go` (create if absent)

**Interfaces:**
- Consumes: `TouchSession(ctx, id, ttl) (userID string, ok bool, err error)` from Task 3.
- Produces: `func userFrom(ctx context.Context) string`, populated by `requireSession` for every handler behind `routeSession` and `routeCSRF`.

**Implementer:** dr-superpowers:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 2 = 4

- [ ] **Step 1: Write the failing test**

Create `internal/admin/auth_test.go`:

```go
package admin

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRequireSessionPutsTheOwnerInContext(t *testing.T) {
	s, uid, cookie := newServerWithSession(t)

	var seen string
	h := s.requireSession(func(w http.ResponseWriter, r *http.Request) {
		seen = userFrom(r.Context())
	})
	req := httptest.NewRequest("GET", "/api/anything", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	h(httptest.NewRecorder(), req)

	if seen != uid {
		t.Errorf("userFrom = %q, want %q", seen, uid)
	}
}

func TestRequireCSRFInheritsTheOwner(t *testing.T) {
	s, uid, cookie := newServerWithSession(t)

	var seen string
	h := s.requireCSRF(func(w http.ResponseWriter, r *http.Request) {
		seen = userFrom(r.Context())
	})
	req := httptest.NewRequest("POST", "/api/anything", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set(csrfHeader, s.csrf.Token(cookie))
	h(httptest.NewRecorder(), req)

	if seen != uid {
		t.Errorf("userFrom through requireCSRF = %q, want %q", seen, uid)
	}
}
```

`newServerWithSession` is added in Task 6 alongside the other admin test helpers; until then, write it inline at the bottom of this file:

```go
// newServerWithSession returns a server with one claimed account and one live
// session, and the raw cookie value for it.
func newServerWithSession(t *testing.T) (*Server, string, string) {
	t.Helper()
	s := testServer(t) // existing helper in the admin test package
	uid := "u1"
	if _, err := s.deps.DB.ClaimFirstUser(t.Context(), uid, "alice", "hash"); err != nil {
		t.Fatal(err)
	}
	cookie := "cookie-1"
	if err := s.deps.DB.CreateSession(t.Context(), cookie, uid, sessionTTL); err != nil {
		t.Fatal(err)
	}
	return s, uid, cookie
}
```

- [ ] **Step 2: Run it to make sure it fails**

```bash
export TMPDIR=/var/tmp/dr-build; export PATH=$PATH:/usr/local/go/bin
go test ./internal/admin/ -run TestRequire -count=1 -v
```

Expected: FAIL — `undefined: userFrom`.

- [ ] **Step 3: Write the implementation**

In `internal/admin/auth.go`, add the parallel context key beside the session one:

```go
// userKeyType is the context key carrying the authenticated account's id. It
// sits beside sessionKey rather than replacing it: the CSRF token is keyed by
// the cookie value, so both are needed on a mutating request.
type userKeyType struct{}

var userKey userKeyType

func userFrom(ctx context.Context) string {
	s, _ := ctx.Value(userKey).(string)
	return s
}
```

Then change `requireSession` to capture and stash the owner:

```go
		userID, ok, terr := s.deps.DB.TouchSession(r.Context(), c.Value, sessionTTL)
		if terr != nil {
			// A database failure is not a logout. Saying 401 here would send
			// the operator to a login screen that cannot work either.
			internalError(w, r, terr)
			return
		}
		if !ok {
			writeError(w, http.StatusUnauthorized, "not authenticated")
			return
		}
		ctx := context.WithValue(r.Context(), sessionKey, c.Value)
		ctx = context.WithValue(ctx, userKey, userID)
		h(w, r.WithContext(ctx))
```

`requireCSRF` is not touched. It wraps `requireSession` and inherits both values, which is the reason the comment at `auth.go:45-47` gives for wrapping rather than sitting beside.

- [ ] **Step 4: Run the tests and make sure they pass**

```bash
export TMPDIR=/var/tmp/dr-build; export PATH=$PATH:/usr/local/go/bin
go test ./internal/admin/ -run TestRequire -count=1 -v
```

Expected: PASS, both tests. Other tests in the package still fail; tasks 5–7 close them.

- [ ] **Step 5: Prove the tests can fail**

Stash the empty string instead of `userID` and confirm both tests go red. Restore.

- [ ] **Step 6: Commit**

```bash
git add internal/admin/auth.go internal/admin/auth_test.go
git commit -m "feat(admin): carry the caller through the guard"
```

---

### Task 5: Login by username, with no enumeration

**Files:**
- Modify: `internal/admin/authapi.go:68-134` (`handleLogin`), `:40-58` (`handleAuthStatus`)
- Test: `internal/admin/authapi_test.go`

**Interfaces:**
- Consumes: `UserByUsername`, `UserCount` (Task 2); `CreateSession(ctx, id, userID, ttl)` (Task 3).
- Produces: `POST /api/auth/login` accepting `{"username","password"}`; `GET /api/auth/status` reporting `configured` from `UserCount`.

**Implementer:** dr-superpowers:impl-opus-medium
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 3 = 5

- [ ] **Step 1: Write the failing test**

Add to `internal/admin/authapi_test.go`:

```go
func TestLoginBindsTheSessionToTheAccount(t *testing.T) {
	s := testServer(t)
	hash, err := HashPassword("correct-horse-battery")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.deps.DB.ClaimFirstUser(t.Context(), "u1", "Alice", hash); err != nil {
		t.Fatal(err)
	}

	rec := postJSON(t, s, "/api/auth/login", `{"username":"alice","password":"correct-horse-battery"}`)
	if rec.Code != 200 {
		t.Fatalf("login = %d, want 200: %s", rec.Code, rec.Body)
	}
	var cookie string
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie {
			cookie = c.Value
		}
	}
	if cookie == "" {
		t.Fatal("login set no session cookie")
	}
	uid, ok, err := s.deps.DB.TouchSession(t.Context(), cookie, sessionTTL)
	if err != nil || !ok {
		t.Fatalf("the minted session is not valid: %v ok=%v", err, ok)
	}
	if uid != "u1" {
		t.Errorf("session owner = %q, want u1", uid)
	}
}

func TestLoginRefusesAnUnknownUsernameIdentically(t *testing.T) {
	s := testServer(t)
	hash, _ := HashPassword("correct-horse-battery")
	if _, err := s.deps.DB.ClaimFirstUser(t.Context(), "u1", "alice", hash); err != nil {
		t.Fatal(err)
	}

	wrongUser := postJSON(t, s, "/api/auth/login", `{"username":"nobody","password":"correct-horse-battery"}`)
	wrongPass := postJSON(t, s, "/api/auth/login", `{"username":"alice","password":"wrong-wrong-wrong"}`)

	if wrongUser.Code != 401 || wrongPass.Code != 401 {
		t.Fatalf("codes = %d and %d, want 401 and 401", wrongUser.Code, wrongPass.Code)
	}
	// Identical wording. An operator who can tell the two apart can enumerate
	// which accounts exist without ever guessing a password.
	if wrongUser.Body.String() != wrongPass.Body.String() {
		t.Errorf("an unknown username is distinguishable from a wrong password:\n  %s\n  %s",
			wrongUser.Body.String(), wrongPass.Body.String())
	}
}

func TestLoginComparesAHashEvenWhenTheUsernameIsUnknown(t *testing.T) {
	// Timing is what the identical message would otherwise leak. Asserting the
	// comparison happened is stable; asserting on the clock is not.
	s := testServer(t)
	hash, _ := HashPassword("correct-horse-battery")
	if _, err := s.deps.DB.ClaimFirstUser(t.Context(), "u1", "alice", hash); err != nil {
		t.Fatal(err)
	}
	before := verifyCalls.Load()
	postJSON(t, s, "/api/auth/login", `{"username":"nobody","password":"whatever-1234"}`)
	if verifyCalls.Load() == before {
		t.Error("no password comparison ran for an unknown username; the miss is timeable")
	}
}

func TestAuthStatusReportsConfiguredFromAccounts(t *testing.T) {
	s := testServer(t)

	rec := getJSON(t, s, "/api/auth/status")
	if !strings.Contains(rec.Body.String(), `"configured":false`) {
		t.Errorf("an unclaimed console reported configured: %s", rec.Body)
	}
	if _, err := s.deps.DB.ClaimFirstUser(t.Context(), "u1", "alice", "hash"); err != nil {
		t.Fatal(err)
	}
	rec = getJSON(t, s, "/api/auth/status")
	if !strings.Contains(rec.Body.String(), `"configured":true`) {
		t.Errorf("a claimed console reported unconfigured: %s", rec.Body)
	}
}
```

- [ ] **Step 2: Run it to make sure it fails**

```bash
export TMPDIR=/var/tmp/dr-build; export PATH=$PATH:/usr/local/go/bin
go test ./internal/admin/ -run 'TestLogin|TestAuthStatus' -count=1 -v
```

Expected: FAIL — `undefined: verifyCalls`, and the login body has no `username` field.

- [ ] **Step 3: Write the implementation**

In `internal/admin/password.go`, add the counter and the dummy hash:

```go
// verifyCalls counts password comparisons. It exists so a test can assert that
// an unknown username still costs a bcrypt comparison, which is what keeps the
// miss from being timeable. Nothing in the request path reads it.
var verifyCalls atomic.Uint64

// dummyHash is compared against when a username does not resolve, so a miss
// costs the same work as a hit. Generated once at startup rather than being a
// constant, so it carries this build's cost parameter.
var dummyHash = func() string {
	h, err := HashPassword("darkrouter-dummy-password-never-valid")
	if err != nil {
		panic("cannot hash the dummy password: " + err.Error())
	}
	return h
}()
```

and have `VerifyPassword` increment it:

```go
func VerifyPassword(hash, password string) bool {
	verifyCalls.Add(1)
	// ... existing body unchanged
}
```

In `internal/admin/authapi.go`, replace the body decode and verification inside `handleLogin`:

```go
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decodeJSON(w, r, 4<<10, &body) {
		return
	}
	if len(body.Password) > maxPasswordBytes {
		// bcrypt reads the first 72 bytes and no more, so anything longer
		// would silently verify against a truncation of itself.
		writeError(w, http.StatusBadRequest, "password must be at most 72 bytes")
		return
	}
	release, ok := s.logins.acquire()
	if !ok {
		writeRateLimited(w, time.Second)
		return
	}
	user, found, err := s.deps.DB.UserByUsername(r.Context(), body.Username)
	if err != nil {
		release()
		internalError(w, r, err)
		return
	}
	// A miss still costs a comparison. One message for a wrong username, a
	// wrong password and an unclaimed console is only half the promise; equal
	// work is the other half, or the response time answers what the wording
	// refuses to.
	hash := dummyHash
	if found {
		hash = user.PasswordHash
	}
	verified := VerifyPassword(hash, body.Password) && found
	release()
	if !verified {
		writeError(w, http.StatusUnauthorized, "invalid username or password")
		return
	}
```

and pass the owner when minting:

```go
	if err := s.deps.DB.CreateSession(r.Context(), id, user.ID, sessionTTL); err != nil {
		internalError(w, r, err)
		return
	}
```

In `handleAuthStatus`, replace the `configured` computation:

```go
	n, err := s.deps.DB.UserCount(r.Context())
	if err != nil {
		internalError(w, r, err)
		return
	}
	body := map[string]any{
		"authenticated": authed,
		"configured":    n > 0,
	}
```

and update its `TouchSession` call to discard the owner: `if _, ok, terr := s.deps.DB.TouchSession(...)`.

- [ ] **Step 4: Run the tests and make sure they pass**

```bash
export TMPDIR=/var/tmp/dr-build; export PATH=$PATH:/usr/local/go/bin
go test ./internal/admin/ -run 'TestLogin|TestAuthStatus' -count=1 -v
```

Expected: PASS, all four.

- [ ] **Step 5: Prove the tests can fail**

Return early with a distinct message when `!found`, and confirm both `TestLoginRefusesAnUnknownUsernameIdentically` and `TestLoginComparesAHashEvenWhenTheUsernameIsUnknown` go red. Restore.

- [ ] **Step 6: Commit**

```bash
git add internal/admin/authapi.go internal/admin/password.go internal/admin/authapi_test.go
git commit -m "feat(admin): log in by username"
```

---

### Task 6: The founding claim

**Files:**
- Rewrite: `internal/admin/setupapi.go`
- Test: `internal/admin/setupapi_test.go` (replace its 11 token tests)

**Interfaces:**
- Consumes: `ClaimFirstUser`, `UserCount` (Task 2).
- Produces: `POST /api/auth/setup` accepting `{"username","password","confirm"}`, returning `{"configured":true}` and **no session**.

**Implementer:** dr-superpowers:impl-opus-medium
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 3 = 5

- [ ] **Step 1: Write the failing test**

Replace the contents of `internal/admin/setupapi_test.go`:

```go
package admin

import (
	"strings"
	"sync"
	"testing"
)

const claimPassword = "correct-horse-battery"

func TestClaimCreatesTheFoundingAdmin(t *testing.T) {
	s := testServer(t)
	rec := postJSON(t, s, "/api/auth/setup",
		`{"username":"Alice","password":"`+claimPassword+`","confirm":"`+claimPassword+`"}`)
	if rec.Code != 200 {
		t.Fatalf("claim = %d, want 200: %s", rec.Code, rec.Body)
	}
	u, ok, err := s.deps.DB.UserByUsername(t.Context(), "alice")
	if err != nil || !ok {
		t.Fatalf("no account was created: %v ok=%v", err, ok)
	}
	if u.Role != "admin" {
		t.Errorf("role = %q, want admin", u.Role)
	}
	if !VerifyPassword(u.PasswordHash, claimPassword) {
		t.Error("the stored hash does not verify the password that was set")
	}
}

func TestClaimMintsNoSession(t *testing.T) {
	// The password is spent on a real login immediately afterwards, so one
	// code path issues cookies and the stored hash is exercised before the
	// operator relies on it. With no recovery path, discovering a bad hash now
	// rather than when the session expires is the difference between retyping
	// a password and losing the console.
	s := testServer(t)
	rec := postJSON(t, s, "/api/auth/setup",
		`{"username":"alice","password":"`+claimPassword+`","confirm":"`+claimPassword+`"}`)
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie && c.Value != "" {
			t.Error("the claim minted a session; it must not")
		}
	}
}

func TestClaimRefusesAMismatchedConfirmation(t *testing.T) {
	s := testServer(t)
	rec := postJSON(t, s, "/api/auth/setup",
		`{"username":"alice","password":"`+claimPassword+`","confirm":"correct-horse-batteryX"}`)
	if rec.Code != 400 {
		t.Fatalf("code = %d, want 400", rec.Code)
	}
	if n, _ := s.deps.DB.UserCount(t.Context()); n != 0 {
		t.Error("a mismatched confirmation created an account")
	}
}

func TestClaimRefusesAShortPassword(t *testing.T) {
	s := testServer(t)
	rec := postJSON(t, s, "/api/auth/setup", `{"username":"alice","password":"short","confirm":"short"}`)
	if rec.Code != 400 {
		t.Errorf("code = %d, want 400", rec.Code)
	}
}

func TestClaimRefusesAnEmptyUsername(t *testing.T) {
	s := testServer(t)
	rec := postJSON(t, s, "/api/auth/setup",
		`{"username":"   ","password":"`+claimPassword+`","confirm":"`+claimPassword+`"}`)
	if rec.Code != 400 {
		t.Errorf("code = %d, want 400", rec.Code)
	}
}

func TestASecondClaimIsRefused(t *testing.T) {
	s := testServer(t)
	postJSON(t, s, "/api/auth/setup",
		`{"username":"alice","password":"`+claimPassword+`","confirm":"`+claimPassword+`"}`)
	rec := postJSON(t, s, "/api/auth/setup",
		`{"username":"mallory","password":"`+claimPassword+`","confirm":"`+claimPassword+`"}`)
	if rec.Code != 409 {
		t.Fatalf("code = %d, want 409", rec.Code)
	}
	if n, _ := s.deps.DB.UserCount(t.Context()); n != 1 {
		t.Errorf("UserCount = %d, want 1", n)
	}
}

func TestConcurrentClaimsProduceExactlyOneWinner(t *testing.T) {
	s := testServer(t)
	const racers = 8
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		won  int
		lost int
	)
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rec := postJSON(t, s, "/api/auth/setup",
				`{"username":"racer`+string(rune('a'+i))+`","password":"`+claimPassword+`","confirm":"`+claimPassword+`"}`)
			mu.Lock()
			defer mu.Unlock()
			switch rec.Code {
			case 200:
				won++
			case 409:
				lost++
			default:
				t.Errorf("unexpected code %d: %s", rec.Code, rec.Body)
			}
		}(i)
	}
	wg.Wait()
	if won != 1 {
		t.Errorf("winners = %d, want exactly 1", won)
	}
	if lost != racers-1 {
		t.Errorf("losers = %d, want %d", lost, racers-1)
	}
}

func TestClaimIsRefusedCrossSite(t *testing.T) {
	s := testServer(t)
	rec := postJSONCrossSite(t, s, "/api/auth/setup",
		`{"username":"alice","password":"`+claimPassword+`","confirm":"`+claimPassword+`"}`)
	if rec.Code != 403 {
		t.Errorf("code = %d, want 403", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "cross-site") {
		t.Errorf("body = %s", rec.Body)
	}
}
```

- [ ] **Step 2: Run it to make sure it fails**

```bash
export TMPDIR=/var/tmp/dr-build; export PATH=$PATH:/usr/local/go/bin
go test ./internal/admin/ -run 'TestClaim|TestASecondClaim|TestConcurrentClaims' -count=1 -v
```

Expected: FAIL — the handler still wants a `token`.

- [ ] **Step 3: Rewrite the handler**

Replace `internal/admin/setupapi.go` entirely:

```go
package admin

import (
	"log/slog"
	"net/http"
	"strings"
)

// maxUsernameChars bounds what a claim may store. Long enough for any name
// somebody will actually pick, short enough that the column is not a place to
// put arbitrary data.
const maxUsernameChars = 64

// handleSetup claims an unclaimed console: the first account created becomes
// admin.
//
// There is no token. Whoever reaches the console first claims it, which is a
// deliberate reversal of the rule this file used to enforce -- see
// docs/plan/decisions.md. What stands in for the token is the startup warning
// on a populated database and the note in deploy.md, both weaker than proving
// host access, which is why the reversal is written down rather than assumed.
//
// It mints no session. The screen spends the password on a real login straight
// afterwards, so one code path issues cookies and the stored hash is exercised
// before the operator relies on it. With no recovery path that ordering is
// what keeps a bad hash a retyped password rather than a lost console.
func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "cross-site request refused")
		return
	}
	// Before the body is read, as on login: a flood costs a map lookup.
	if ok, wait := s.logins.take(clientAddr(r.RemoteAddr)); !ok {
		writeRateLimited(w, wait)
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
		Confirm  string `json:"confirm"`
	}
	if !decodeJSON(w, r, 4<<10, &body) {
		return
	}
	username := strings.TrimSpace(body.Username)
	if username == "" || len([]rune(username)) > maxUsernameChars {
		writeError(w, http.StatusBadRequest, "a username is required")
		return
	}
	if len(body.Password) < minPasswordChars {
		writeError(w, http.StatusBadRequest, "the password must be at least 12 characters")
		return
	}
	if len(body.Password) > maxPasswordBytes {
		writeError(w, http.StatusBadRequest, "the password must be at most 72 bytes")
		return
	}
	// Checked on the server, not only in the screen: with no recovery path a
	// typo in the founding password is unrecoverable, and an API client must
	// not be able to skip the guard the screen enforces.
	if body.Password != body.Confirm {
		writeError(w, http.StatusBadRequest, "the two passwords do not match")
		return
	}
	hash, err := HashPassword(body.Password)
	if err != nil {
		internalError(w, r, err)
		return
	}
	id, err := newSessionID() // 32 bytes of entropy; reused as an opaque row id
	if err != nil {
		internalError(w, r, err)
		return
	}
	// The emptiness test is inside the INSERT, so two claims arriving together
	// cannot both be told they won.
	won, err := s.deps.DB.ClaimFirstUser(r.Context(), id, username, hash)
	if err != nil {
		internalError(w, r, err)
		return
	}
	if !won {
		writeError(w, http.StatusConflict, "the console has already been set up")
		return
	}
	slog.Info("the console was claimed", "username", username)
	writeJSON(w, http.StatusOK, map[string]any{"configured": true})
}
```

- [ ] **Step 4: Run the tests and make sure they pass**

```bash
export TMPDIR=/var/tmp/dr-build; export PATH=$PATH:/usr/local/go/bin
go test ./internal/admin/ -run 'TestClaim|TestASecondClaim|TestConcurrentClaims' -count=1 -v
```

Expected: PASS, all eight.

- [ ] **Step 5: Prove the tests can fail**

Replace `ClaimFirstUser` with a count-then-insert pair and confirm `TestConcurrentClaimsProduceExactlyOneWinner` goes red (run it with `-race -count=20`). Drop the confirmation check and confirm `TestClaimRefusesAMismatchedConfirmation` goes red. Restore both.

- [ ] **Step 6: Commit**

```bash
git add internal/admin/setupapi.go internal/admin/setupapi_test.go
git commit -m "feat(admin): claim the console with the first account"
```

---

### Task 7: Delete the retired machinery

**Files:**
- Modify: `internal/admin/sessionapi.go:14-93` (delete the env-hash cluster), `internal/admin/admin.go` (`setupToken`/`setupMu` fields), `internal/server/server.go:500-510`, `cmd/darkrouter/main.go` (the `hash-password` subcommand)
- Delete: `internal/admin/password_test.go` cases covering the env hash

**Interfaces:**
- Consumes: `UserCount` (Task 2) for the one remaining caller of `PasswordConfigured`.
- Produces: nothing. This task only removes.

**Implementer:** dr-superpowers:impl-opus-medium
**Evaluation:** files 2 - spec 0 - coupling 1 - risk 2 = 5

- [ ] **Step 1: Write the failing test**

Add to `internal/admin/sessionapi_test.go`:

```go
func TestTheEnvironmentHashIsNotRead(t *testing.T) {
	// The variable is retired. A deployment that still sets it must get no
	// effect at all -- not a fallback, not a seeded account.
	t.Setenv("DARKROUTER_ADMIN_PASSWORD_HASH", mustHash(t, "correct-horse-battery"))
	s := testServer(t)

	if n, _ := s.deps.DB.UserCount(t.Context()); n != 0 {
		t.Error("the environment hash seeded an account")
	}
	rec := postJSON(t, s, "/api/auth/login",
		`{"username":"admin","password":"correct-horse-battery"}`)
	if rec.Code != 401 {
		t.Errorf("the environment hash authenticated a login: %d %s", rec.Code, rec.Body)
	}
}
```

- [ ] **Step 2: Run it to make sure it fails**

```bash
export TMPDIR=/var/tmp/dr-build; export PATH=$PATH:/usr/local/go/bin
go test ./internal/admin/ -run TestTheEnvironmentHashIsNotRead -count=1 -v
```

Expected: FAIL to compile while the old cluster still exists, or FAIL on the login assertion.

- [ ] **Step 3: Delete**

From `internal/admin/sessionapi.go`, remove entirely: `settingAdminPasswordHash`, `settingPasswordEnvFingerprint`, `fingerprint`, `reconcilePasswordHash`, `recordPasswordEnv`, `currentPasswordHash`. Remove `PasswordConfigured` from `setupapi.go` if any caller remains, replacing it with `UserCount(ctx) > 0`.

From `internal/admin/admin.go`, remove the `setupToken` and `setupMu` fields from `Server` and any call to `mintSetupToken`.

From `internal/server/server.go`, remove the unclaimed-console warning string that names the setup token (around `:500-510`) and its caller.

From `cmd/darkrouter/main.go`, remove the `hash-password` subcommand and its dispatch entry. It exists only to generate the retired variable.

From `internal/admin/password.go`, keep `HashPassword` and `VerifyPassword` — the claim and the password change both still need them.

- [ ] **Step 4: Run the whole Go suite**

```bash
export TMPDIR=/var/tmp/dr-build; export PATH=$PATH:/usr/local/go/bin
go build ./... && go vet ./... && go test ./internal/admin/ ./internal/store/ -count=1
```

Expected: `internal/admin` and `internal/store` PASS. `internal/e2e` and `internal/server` still fail; task 10 closes them.

- [ ] **Step 5: Confirm nothing still references the variable in Go**

```bash
grep -rn 'DARKROUTER_ADMIN_PASSWORD_HASH\|setupToken\|hash-password' --include='*.go' . | grep -v _test.go
```

Expected: no output.

- [ ] **Step 6: Commit**

```bash
git add -A internal/ cmd/
git commit -m "refactor(admin): delete the shared password machinery"
```

---

### Task 8: Warn when a populated database has no accounts

**Files:**
- Modify: `cmd/darkrouter/main.go:115-131` (`startupWarnings`)
- Test: `cmd/darkrouter/main_test.go`

**Interfaces:**
- Consumes: `UserCount` (Task 2).
- Produces: an `slog` warning at startup. Deliberately **not** added to the returned `startupWarnings` slice, which reaches unauthenticated `/healthz`.

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 0 - risk 1 = 2

- [ ] **Step 1: Write the failing test**

Add to `cmd/darkrouter/main_test.go`:

```go
func TestAnUnclaimedPopulatedDatabaseWarns(t *testing.T) {
	var buf bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))

	db := openPopulatedTestDB(t) // has providers, no users
	warnUnclaimed(t.Context(), db)

	if !strings.Contains(buf.String(), "no account has claimed this console") {
		t.Errorf("no warning was logged: %s", buf.String())
	}
}

func TestAFreshDatabaseDoesNotWarn(t *testing.T) {
	var buf bytes.Buffer
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))

	db := openEmptyTestDB(t) // nothing in it at all
	warnUnclaimed(t.Context(), db)

	if strings.Contains(buf.String(), "no account has claimed this console") {
		t.Error("a brand-new install was warned about; only an upgrade should be")
	}
}

func TestTheUnclaimedWarningStaysOutOfHealthz(t *testing.T) {
	// startupWarnings reaches unauthenticated /healthz. A warning saying the
	// console is unclaimed would tell exactly the caller it exists to keep out.
	db := openPopulatedTestDB(t)
	got := startupWarnings(dbPathOf(db), "")
	for _, w := range got {
		if strings.Contains(w, "claimed") {
			t.Errorf("the claim warning reached startupWarnings: %q", w)
		}
	}
}
```

- [ ] **Step 2: Run it to make sure it fails**

```bash
export TMPDIR=/var/tmp/dr-build; export PATH=$PATH:/usr/local/go/bin
go test ./cmd/darkrouter/ -run Unclaimed -count=1 -v
```

Expected: FAIL — `undefined: warnUnclaimed`.

- [ ] **Step 3: Write the implementation**

Add to `cmd/darkrouter/main.go`:

```go
// warnUnclaimed says loudly that an existing deployment is claimable.
//
// A database with data but no accounts is an upgrade that has not been claimed
// yet, and until somebody claims it anyone who can reach the admin port can
// become admin. A brand-new install is the same state for a good reason and is
// not warned about, or every first start would cry wolf.
//
// slog only. startupWarnings reaches unauthenticated /healthz, and a notice
// saying "this console is unclaimed" would be read by exactly the caller it
// exists to keep out.
func warnUnclaimed(ctx context.Context, db *store.DB) {
	users, err := db.UserCount(ctx)
	if err != nil || users > 0 {
		return
	}
	providers, err := db.ProviderCount(ctx)
	if err != nil || providers == 0 {
		return
	}
	slog.Warn("no account has claimed this console; the first visitor to reach " +
		"the admin port will become its administrator. Claim it now.")
}
```

Call it in `runServer` immediately after migrations, before the listeners bind.

- [ ] **Step 4: Run the tests and make sure they pass**

```bash
export TMPDIR=/var/tmp/dr-build; export PATH=$PATH:/usr/local/go/bin
go test ./cmd/darkrouter/ -run Unclaimed -count=1 -v
```

Expected: PASS, all three.

- [ ] **Step 5: Prove the tests can fail**

Drop the `providers == 0` guard and confirm `TestAFreshDatabaseDoesNotWarn` goes red. Append the warning to `startupWarnings` and confirm `TestTheUnclaimedWarningStaysOutOfHealthz` goes red. Restore both.

- [ ] **Step 6: Commit**

```bash
git add cmd/darkrouter/main.go cmd/darkrouter/main_test.go
git commit -m "feat(cmd): warn when no account has claimed the console"
```

---

### Task 9: Session and password handlers act on the caller

**Files:**
- Modify: `internal/admin/sessionapi.go:109-136` (`handleListSessions`), `:138-178` (`handleDeleteSession`), `:180-225` (`handleChangePassword`)
- Test: `internal/admin/sessionapi_test.go`

**Interfaces:**
- Consumes: `userFrom` (Task 4); the owner-scoped `SessionRows`, `RevokeSession`, `DeleteSessionsExcept` (Task 3); `UserByID`, `SetUserPassword` (Task 2).
- Produces: nothing new. The three handlers keep their paths, methods and response shapes.

**Implementer:** dr-superpowers:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 2 = 4

- [ ] **Step 1: Write the failing test**

Add to `internal/admin/sessionapi_test.go`:

```go
func TestListSessionsShowsOnlyTheCallersOwn(t *testing.T) {
	s, _, cookie := newServerWithSession(t) // account u1
	seedSecondAccountWithSession(t, s)      // account u2, cookie "other"

	rec := getJSONAs(t, s, "/api/sessions", cookie)
	if strings.Contains(rec.Body.String(), HashSessionID("other")[:8]) {
		t.Errorf("another account's session was listed: %s", rec.Body)
	}
}

func TestRevokingAnotherAccountsSessionIs404(t *testing.T) {
	s, _, cookie := newServerWithSession(t)
	seedSecondAccountWithSession(t, s)

	rec := deleteAs(t, s, "/api/sessions/"+HashSessionID("other")[:8], cookie)
	if rec.Code != 404 {
		t.Errorf("code = %d, want 404: one account must not reach another's session", rec.Code)
	}
	if _, ok, _ := s.deps.DB.TouchSession(t.Context(), "other", sessionTTL); !ok {
		t.Error("the other account's session was revoked")
	}
}

func TestChangingMyPasswordUpdatesMyRowOnly(t *testing.T) {
	s, uid, cookie := newServerWithSession(t)
	hash := mustHash(t, "old-password-here")
	if err := s.deps.DB.SetUserPassword(t.Context(), uid, hash); err != nil {
		t.Fatal(err)
	}
	seedSecondAccountWithSession(t, s)

	rec := postJSONAs(t, s, "/api/auth/password",
		`{"current":"old-password-here","new":"a-brand-new-password"}`, cookie)
	if rec.Code != 200 {
		t.Fatalf("code = %d: %s", rec.Code, rec.Body)
	}
	u, _, _ := s.deps.DB.UserByID(t.Context(), uid)
	if !VerifyPassword(u.PasswordHash, "a-brand-new-password") {
		t.Error("the new password does not verify against the stored hash")
	}
	// The other account keeps its session: their password did not change.
	if _, ok, _ := s.deps.DB.TouchSession(t.Context(), "other", sessionTTL); !ok {
		t.Error("a password change signed out an unrelated account")
	}
	// The caller keeps the session they are using.
	if _, ok, _ := s.deps.DB.TouchSession(t.Context(), cookie, sessionTTL); !ok {
		t.Error("the caller was signed out of the screen they just used")
	}
}

func TestChangingMyPasswordRefusesAWrongCurrent(t *testing.T) {
	s, uid, cookie := newServerWithSession(t)
	if err := s.deps.DB.SetUserPassword(t.Context(), uid, mustHash(t, "old-password-here")); err != nil {
		t.Fatal(err)
	}
	rec := postJSONAs(t, s, "/api/auth/password",
		`{"current":"not-the-password","new":"a-brand-new-password"}`, cookie)
	if rec.Code != 401 {
		t.Errorf("code = %d, want 401", rec.Code)
	}
}
```

- [ ] **Step 2: Run it to make sure it fails**

```bash
export TMPDIR=/var/tmp/dr-build; export PATH=$PATH:/usr/local/go/bin
go test ./internal/admin/ -run 'TestListSessions|TestRevoking|TestChangingMyPassword' -count=1 -v
```

Expected: FAIL to compile — the handlers still call the one-argument store methods.

- [ ] **Step 3: Write the implementation**

In `handleListSessions`, pass the caller:

```go
	rows, err := s.deps.DB.SessionRows(r.Context(), userFrom(r.Context()), time.Now())
```

In `handleDeleteSession`, pass the caller to the revoke. The prefix search above it already walks only the rows `SessionRows` returned, which are now the caller's own, so its collision handling is unchanged:

```go
	gone, err := s.deps.DB.RevokeSession(r.Context(), userFrom(r.Context()), match)
```

Replace the body of `handleChangePassword` so it reads and writes the caller's row instead of the settings key:

```go
	uid := userFrom(r.Context())
	user, ok, err := s.deps.DB.UserByID(r.Context(), uid)
	if err != nil {
		internalError(w, r, err)
		return
	}
	if !ok {
		// The guard proved the session; the account behind it is gone.
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	if !VerifyPassword(user.PasswordHash, body.Current) {
		writeError(w, http.StatusUnauthorized, "the current password is wrong")
		return
	}
	hash, err := HashPassword(body.New)
	if err != nil {
		internalError(w, r, err)
		return
	}
	if err := s.deps.DB.SetUserPassword(r.Context(), uid, hash); err != nil {
		internalError(w, r, err)
		return
	}
	// Every other browser signed in as this account, and no other account.
	if _, err := s.deps.DB.DeleteSessionsExcept(r.Context(), uid, sessionFrom(r.Context())); err != nil {
		internalError(w, r, err)
		return
	}
```

The existing length checks on `body.New` stay exactly as they are.

- [ ] **Step 4: Run the tests and make sure they pass**

```bash
export TMPDIR=/var/tmp/dr-build; export PATH=$PATH:/usr/local/go/bin
go test ./internal/admin/ -count=1
```

Expected: the whole admin package PASSES.

- [ ] **Step 5: Prove the tests can fail**

Pass `""` instead of `userFrom(...)` to `DeleteSessionsExcept` and confirm `TestChangingMyPasswordUpdatesMyRowOnly` goes red on the unrelated-account assertion. Restore.

- [ ] **Step 6: Commit**

```bash
git add internal/admin/sessionapi.go internal/admin/sessionapi_test.go
git commit -m "feat(admin): scope sessions and password to the caller"
```

---

### Task 10: Test harnesses authenticate as an account

**Files:**
- Modify: `internal/e2e/harness_test.go:94-101`, `internal/server/server_test.go:435-470`, `:611-620`

**Interfaces:**
- Consumes: `ClaimFirstUser` (Task 2), `CreateSession` (Task 3).
- Produces: `func seedAccount(t *testing.T, db *store.DB) (userID, cookie string)` in the e2e harness, used by every e2e test that needs an authenticated request.

**Implementer:** dr-superpowers:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 2 = 4

- [ ] **Step 1: Write the failing test**

The harness change is itself the risk: a harness that authenticates when it should not leaves the whole suite green while proving nothing. So the first thing written is the test that the harness is honest.

Add to `internal/e2e/harness_test.go`:

```go
func TestHarnessRequestsAreRefusedWithoutAnAccount(t *testing.T) {
	// If this passes while the harness seeds nothing, every other e2e test in
	// this package is asserting against an open console.
	h := newHarness(t) // no seedAccount call
	res := h.getRaw(t, "/api/providers")
	if res.StatusCode != 401 {
		t.Fatalf("an unauthenticated harness request got %d, want 401", res.StatusCode)
	}
}
```

- [ ] **Step 2: Run it to make sure it fails**

```bash
export TMPDIR=/var/tmp/dr-build; export PATH=$PATH:/usr/local/go/bin
go test ./internal/e2e/ -run TestHarnessRequests -count=1 -v
```

Expected: FAIL to compile — `t.Setenv("DARKROUTER_ADMIN_PASSWORD_HASH", ...)` is still at line 97 and `passwordHash()` no longer exists.

- [ ] **Step 3: Rewrite the harness**

In `internal/e2e/harness_test.go`, delete line 97 (`t.Setenv("DARKROUTER_ADMIN_PASSWORD_HASH", passwordHash())`) and the `passwordHash` helper. Add:

```go
// seedAccount claims the console and returns a live session cookie. Tests that
// make authenticated requests call this; tests that assert on the unclaimed
// state do not.
func seedAccount(t *testing.T, db *store.DB) (string, string) {
	t.Helper()
	hash, err := admin.HashPassword("harness-password-1234")
	if err != nil {
		t.Fatal(err)
	}
	const uid = "harness-user"
	if _, err := db.ClaimFirstUser(t.Context(), uid, "harness", hash); err != nil {
		t.Fatal(err)
	}
	cookie := "harness-session"
	if err := db.CreateSession(t.Context(), cookie, uid, time.Hour); err != nil {
		t.Fatal(err)
	}
	return uid, cookie
}
```

Have the harness's authenticated request helpers attach that cookie. In `internal/server/server_test.go`, replace the three `t.Setenv("DARKROUTER_ADMIN_PASSWORD_HASH", ...)` calls: the two that set a hash become `seedAccount`, and the one at `:437` that sets `""` simply drops the line — an empty account table is now the default state.

- [ ] **Step 4: Run the full Go suite**

```bash
export TMPDIR=/var/tmp/dr-build; export PATH=$PATH:/usr/local/go/bin
go build ./... && go vet ./... && go test ./... -count=1
```

Expected: everything PASSES. This is the first point in phase 1 where the whole tree is green.

- [ ] **Step 5: Prove the harness is honest**

Make `seedAccount` a no-op returning empty strings and confirm `TestHarnessRequestsAreRefusedWithoutAnAccount` still passes while a dozen other e2e tests go red. That asymmetry is the point: the guard test proves the harness is not silently authenticating. Restore.

- [ ] **Step 6: Commit**

```bash
git add internal/e2e/harness_test.go internal/server/server_test.go
git commit -m "test: authenticate harnesses as an account"
```

---

### Task 11: The first-run screen claims with a username

**Files:**
- Modify: `web/src/features/shell/first-run.tsx`
- Test: `web/src/features/shell/first-run.test.tsx` (replace its 12 token cases)

**Interfaces:**
- Consumes: `POST /api/auth/setup` with `{username, password, confirm}` (Task 6).
- Produces: nothing other tasks consume.

**Implementer:** dr-superpowers:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 1 = 3

- [ ] **Step 1: Write the failing test**

Replace `web/src/features/shell/first-run.test.tsx`:

```tsx
import { render, screen, waitFor } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, it, vi } from "vitest"
import { FirstRun } from "./first-run"

const GOOD = "correct-horse-battery"

describe("FirstRun", () => {
  it("asks for a username, a password and a confirmation", () => {
    render(<FirstRun onClaimed={() => {}} />)
    expect(screen.getByLabelText(/username/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/^password/i)).toBeInTheDocument()
    expect(screen.getByLabelText(/confirm/i)).toBeInTheDocument()
    expect(screen.queryByLabelText(/token/i)).not.toBeInTheDocument()
  })

  it("refuses to submit when the two passwords differ", async () => {
    const post = vi.fn()
    render(<FirstRun onClaimed={() => {}} />)
    await userEvent.type(screen.getByLabelText(/username/i), "alice")
    await userEvent.type(screen.getByLabelText(/^password/i), GOOD)
    await userEvent.type(screen.getByLabelText(/confirm/i), GOOD + "X")
    await userEvent.click(screen.getByRole("button", { name: /claim/i }))
    expect(post).not.toHaveBeenCalled()
    expect(screen.getByText(/do not match/i)).toBeInTheDocument()
  })

  it("refuses a password under twelve characters before the round trip", async () => {
    render(<FirstRun onClaimed={() => {}} />)
    await userEvent.type(screen.getByLabelText(/username/i), "alice")
    await userEvent.type(screen.getByLabelText(/^password/i), "short")
    await userEvent.type(screen.getByLabelText(/confirm/i), "short")
    await userEvent.click(screen.getByRole("button", { name: /claim/i }))
    expect(screen.getByText(/at least 12/i)).toBeInTheDocument()
  })

  it("calls onClaimed after a successful claim so the operator logs in", async () => {
    const onClaimed = vi.fn()
    render(<FirstRun onClaimed={onClaimed} />)
    await userEvent.type(screen.getByLabelText(/username/i), "alice")
    await userEvent.type(screen.getByLabelText(/^password/i), GOOD)
    await userEvent.type(screen.getByLabelText(/confirm/i), GOOD)
    await userEvent.click(screen.getByRole("button", { name: /claim/i }))
    await waitFor(() => expect(onClaimed).toHaveBeenCalled())
  })

  it("sends the operator to the login form when someone else claimed it first", async () => {
    // A 409 means the console gained an account between this page loading and
    // this submit. What this operator needs is the login form.
    const onClaimed = vi.fn()
    render(<FirstRun onClaimed={onClaimed} />)
    await userEvent.type(screen.getByLabelText(/username/i), "alice")
    await userEvent.type(screen.getByLabelText(/^password/i), GOOD)
    await userEvent.type(screen.getByLabelText(/confirm/i), GOOD)
    await userEvent.click(screen.getByRole("button", { name: /claim/i }))
    await waitFor(() => expect(onClaimed).toHaveBeenCalled())
  })
})
```

Mock `api.post` in this file the way the existing suite does — follow the pattern already in `web/src/features/settings/change-password-dialog.test.tsx`.

- [ ] **Step 2: Run it to make sure it fails**

```bash
cd web && npx vitest run src/features/shell/first-run.test.tsx
```

Expected: FAIL — `Unable to find a label with the text of: /username/i`.

- [ ] **Step 3: Rewrite the screen**

In `web/src/features/shell/first-run.tsx`: replace the `token` state with `username`, add `confirm`, drop the `REJECTED` constant (there is no token to reject), and validate both the length and the match before posting. Keep `MIN_PASSWORD = 12`. Post `{ username, password, confirm }` to `/api/auth/setup`. Keep the existing 409 handling, which already re-reads auth status and puts the operator on the login form. Update the component's doc comment: the claim is now trust-on-first-use, and the reason it mints no session is unchanged and worth restating.

Every label uses `text-sm` or larger. No `text-xs`, no custom size.

- [ ] **Step 4: Run the tests and make sure they pass**

```bash
cd web && npx vitest run src/features/shell/first-run.test.tsx
```

Expected: PASS, all five.

- [ ] **Step 5: Prove the tests can fail**

Remove the confirmation comparison and confirm the mismatch test goes red. Restore.

- [ ] **Step 6: Commit**

```bash
git add web/src/features/shell/first-run.tsx web/src/features/shell/first-run.test.tsx
git commit -m "feat(console): claim the console with a username"
```

---

### Task 12: The login screen takes a username

**Files:**
- Modify: the login form component under `web/src/features/shell/`
- Test: its sibling test file

**Interfaces:**
- Consumes: `POST /api/auth/login` with `{username, password}` (Task 5).
- Produces: nothing other tasks consume.

**Implementer:** dr-superpowers:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 1 = 3

- [ ] **Step 1: Write the failing test**

```tsx
it("sends the username with the password", async () => {
  const post = vi.spyOn(api, "post").mockResolvedValue({ authenticated: true, csrf_token: "t" })
  render(<Login onAuthenticated={() => {}} />)
  await userEvent.type(screen.getByLabelText(/username/i), "alice")
  await userEvent.type(screen.getByLabelText(/password/i), "correct-horse-battery")
  await userEvent.click(screen.getByRole("button", { name: /sign in/i }))
  await waitFor(() =>
    expect(post).toHaveBeenCalledWith(
      "/api/auth/login",
      { username: "alice", password: "correct-horse-battery" },
      expect.anything(),
    ),
  )
})

it("keeps one message for every refusal", async () => {
  vi.spyOn(api, "post").mockRejectedValue(new ApiError(401, "invalid username or password"))
  render(<Login onAuthenticated={() => {}} />)
  await userEvent.type(screen.getByLabelText(/username/i), "nobody")
  await userEvent.type(screen.getByLabelText(/password/i), "correct-horse-battery")
  await userEvent.click(screen.getByRole("button", { name: /sign in/i }))
  // The screen must not add its own wording that distinguishes the two cases.
  expect(await screen.findByText(/invalid username or password/i)).toBeInTheDocument()
})
```

- [ ] **Step 2: Run it to make sure it fails**

```bash
cd web && npx vitest run src/features/shell/
```

Expected: FAIL — no username field.

- [ ] **Step 3: Add the field**

Add a username `Input` above the `PasswordInput`, labelled "Username", with `autoComplete="username"` so password managers fill both. The password field gains `autoComplete="current-password"`. Submit `{ username, password }`. The Sign in button stays disabled until both are non-empty. `text-sm` floor throughout.

- [ ] **Step 4: Run the tests and make sure they pass**

```bash
cd web && npx vitest run src/features/shell/
```

Expected: PASS.

- [ ] **Step 5: Prove the tests can fail**

Post only `{ password }` and confirm the first test goes red. Restore.

- [ ] **Step 6: Commit**

```bash
git add web/src/features/shell/
git commit -m "feat(console): sign in with a username"
```

---

### Task 13: Correct the recovery documentation

**Files:**
- Modify: `docs/operations/deploy.md:40-80` (the password and recovery sections)

**Interfaces:**
- Consumes: nothing.
- Produces: nothing.

**Implementer:** dr-superpowers:impl-sonnet-low
**Evaluation:** files 1 - spec 0 - coupling 0 - risk 0 = 1

This task is in phase 1 rather than the phase 3 sweep because the text it fixes becomes actively dangerous the moment phase 1 ships: it instructs an operator to set a variable that no longer does anything, during the incident where they are already locked out.

- [ ] **Step 1: Replace the password section**

In `docs/operations/deploy.md`, delete the paragraph beginning "The admin password is set in the browser, not here" through the end of the "Recovering a lost password" block, including the `docker run ... hash-password` snippet and the indented note about the adopting restart. Replace with:

```markdown
The first account claims the console. On first run the console shows a claim
screen instead of a login: pick a username and a password, and that account
becomes the administrator. There is no setup token and no environment
variable — whoever reaches the console first claims it.

**Claim it immediately after the first start.** Both ports bind every
interface, so until an account exists anyone who can reach the admin port can
become the administrator. The process logs a warning at every start while a
database that already holds providers has no account.

**There is no password recovery.** No environment variable overrides a stored
password and no subcommand resets one. An administrator who forgets their
password has no route back in through darkrouter itself; the only remedy is to
edit `users` in `data/darkrouter.db` directly, which means stopping the
container and writing a bcrypt hash by hand. Keep the password somewhere you
will still have it.
```

- [ ] **Step 2: Remove the upgrade note's password clause**

In the "Upgrading a deployment made before these changes" block, delete the paragraph about doubled `$` in the bcrypt hash — the variable it describes no longer exists. Add one line to that block:

```markdown
> `DARKROUTER_ADMIN_PASSWORD_HASH` is no longer read. Remove it from `.env`;
> leaving it set has no effect. Every session ends at this upgrade and the
> console falls to its claim screen, so claim it as soon as it restarts.
```

- [ ] **Step 3: Check nothing else in this file still promises the old mechanism**

```bash
grep -n 'ADMIN_PASSWORD_HASH\|setup token\|hash-password' docs/operations/deploy.md
```

Expected: no output.

- [ ] **Step 4: Confirm the prose wraps at the file's width**

```bash
awk 'length>80 {print FILENAME":"NR" ["length"]"}' docs/operations/deploy.md
```

Expected: no new lines over 80 beyond any that already existed.

- [ ] **Step 5: Commit**

```bash
git add docs/operations/deploy.md
git commit -m "docs(ops): describe claiming rather than recovering"
```

---

## Phase 1 gate

Before starting Task 14, the whole tree must be green and the feature must work in a browser.

- [ ] `go build ./... && go vet ./... && go test ./... -count=1` all exit 0
- [ ] `cd web && npm test` passes every file
- [ ] Rebuild and redeploy per `docs/operations/deploy.md` "Local build (UAT)", including the `compose.uat.yml` overlay
- [ ] Open http://localhost:8091, confirm the claim screen appears, claim it, confirm the login form accepts the new account
- [ ] Rewrite `.uat-credentials` with the username and password chosen, keeping the file gitignored
- [ ] Confirm `/healthz` reports no warnings and that the startup log carried the unclaimed warning before the claim

---

### Task 14: Store methods for listing and removing accounts

**Files:**
- Modify: `internal/store/users.go`, `internal/store/users_test.go`

**Interfaces:**
- Consumes: the `users` table (Task 1) and `User` (Task 2).
- Produces:
  - `func (d *DB) Users(ctx context.Context) ([]User, error)` — every account, oldest first
  - `func (d *DB) CreateUser(ctx context.Context, id, username, hash, role string) error`
  - `func (d *DB) DeleteUser(ctx context.Context, id string) (bool, error)`
  - `func (d *DB) AdminCount(ctx context.Context) (int, error)`

**Implementer:** dr-superpowers:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 1 = 3

- [ ] **Step 1: Write the failing test**

Add to `internal/store/users_test.go`:

```go
func TestCreateUserRefusesADuplicateNameRegardlessOfCase(t *testing.T) {
	db := storetest.Migrated(t)
	ctx := context.Background()
	if _, err := db.ClaimFirstUser(ctx, "u1", "Alice", "hash"); err != nil {
		t.Fatal(err)
	}
	err := db.CreateUser(ctx, "u2", "ALICE", "hash", RoleMember)
	if err == nil {
		t.Fatal("a second account took a name differing only by case")
	}
}

func TestUsersListsEveryAccountOldestFirst(t *testing.T) {
	db := storetest.Migrated(t)
	ctx := context.Background()
	if _, err := db.ClaimFirstUser(ctx, "u1", "alice", "hash"); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateUser(ctx, "u2", "bob", "hash", RoleMember); err != nil {
		t.Fatal(err)
	}
	list, err := db.Users(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("len = %d, want 2", len(list))
	}
	if list[0].ID != "u1" {
		t.Errorf("first = %q, want the founding account", list[0].ID)
	}
	for _, u := range list {
		if u.PasswordHash != "" {
			t.Error("Users returned a password hash; a listing has no business carrying one")
		}
	}
}

func TestDeleteUserReportsWhetherARowWent(t *testing.T) {
	db := storetest.Migrated(t)
	ctx := context.Background()
	if _, err := db.ClaimFirstUser(ctx, "u1", "alice", "hash"); err != nil {
		t.Fatal(err)
	}
	gone, err := db.DeleteUser(ctx, "u1")
	if err != nil || !gone {
		t.Fatalf("DeleteUser: %v gone=%v", err, gone)
	}
	gone, err = db.DeleteUser(ctx, "u1")
	if err != nil {
		t.Fatal(err)
	}
	if gone {
		t.Error("deleting an absent account reported a removal")
	}
}

func TestAdminCountSeesOnlyAdmins(t *testing.T) {
	db := storetest.Migrated(t)
	ctx := context.Background()
	if _, err := db.ClaimFirstUser(ctx, "u1", "alice", "hash"); err != nil {
		t.Fatal(err)
	}
	if err := db.CreateUser(ctx, "u2", "bob", "hash", RoleMember); err != nil {
		t.Fatal(err)
	}
	n, err := db.AdminCount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("AdminCount = %d, want 1", n)
	}
}
```

- [ ] **Step 2: Run it to make sure it fails**

```bash
export TMPDIR=/var/tmp/dr-build; export PATH=$PATH:/usr/local/go/bin
go test ./internal/store/ -run 'TestCreateUser|TestUsersLists|TestDeleteUser|TestAdminCount' -count=1 -v
```

Expected: FAIL — `db.CreateUser undefined`.

- [ ] **Step 3: Write the implementation**

Append to `internal/store/users.go`:

```go
// Users lists every account, oldest first. The password hash is deliberately
// not selected: a listing has no use for it, and a field that is never read
// cannot be leaked by a handler that forgets to strip it.
func (d *DB) Users(ctx context.Context) ([]User, error) {
	rows, err := d.Read.QueryContext(ctx,
		`SELECT id, username, username_lc, role, created_at FROM users
		  ORDER BY created_at, id`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer func() { _ = rows.Close() }()

	out := []User{}
	for rows.Next() {
		var (
			u       User
			created int64
		)
		if err := rows.Scan(&u.ID, &u.Username, &u.UsernameLC, &u.Role, &created); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		u.CreatedAt = time.UnixMilli(created).UTC()
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	return out, nil
}

// CreateUser adds an account. The unique index on username_lc is what refuses
// a duplicate name, so the check and the write cannot drift apart.
func (d *DB) CreateUser(ctx context.Context, id, username, hash, role string) error {
	if _, err := d.Write.ExecContext(ctx,
		`INSERT INTO users (id, username, username_lc, password_hash, role, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		id, username, normalizeUsername(username), hash, role, time.Now().UnixMilli()); err != nil {
		return fmt.Errorf("create user: %w", err)
	}
	return nil
}

// DeleteUser removes an account and, through ON DELETE CASCADE, every session
// it holds. It reports whether a row went.
func (d *DB) DeleteUser(ctx context.Context, id string) (bool, error) {
	res, err := d.Write.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	if err != nil {
		return false, fmt.Errorf("delete user: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("delete user: %w", err)
	}
	return n > 0, nil
}

// AdminCount is what stops the last administrator removing or demoting
// themselves, which would leave a console nobody can manage and no recovery
// path to fix it.
func (d *DB) AdminCount(ctx context.Context) (int, error) {
	var n int
	if err := d.Read.QueryRowContext(ctx,
		`SELECT count(*) FROM users WHERE role = ?`, RoleAdmin).Scan(&n); err != nil {
		return 0, fmt.Errorf("count admins: %w", err)
	}
	return n, nil
}
```

- [ ] **Step 4: Run the tests and make sure they pass**

```bash
export TMPDIR=/var/tmp/dr-build; export PATH=$PATH:/usr/local/go/bin
go test ./internal/store/ -count=1
```

Expected: PASS.

- [ ] **Step 5: Prove the tests can fail**

Select `password_hash` in `Users` and assign it, and confirm `TestUsersListsEveryAccountOldestFirst` goes red. Restore.

- [ ] **Step 6: Commit**

```bash
git add internal/store/users.go internal/store/users_test.go
git commit -m "feat(store): list, create and remove accounts"
```

---

### Task 15: Admin-only account endpoints

**Files:**
- Create: `internal/admin/usersapi.go`, `internal/admin/usersapi_test.go`
- Modify: `internal/admin/admin.go` (three rows in `routeTable`)

**Interfaces:**
- Consumes: `Users`, `CreateUser`, `DeleteUser`, `AdminCount`, `UserByID` (Tasks 2 and 14); `userFrom` (Task 4).
- Produces: `GET /api/users` (`routeSession`), `POST /api/users` (`routeCSRF`), `DELETE /api/users/{id}` (`routeCSRF`), all admin-only in-handler.

**Implementer:** dr-superpowers:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 2 = 4

- [ ] **Step 1: Write the failing test**

Create `internal/admin/usersapi_test.go`:

```go
package admin

import "testing"

func TestAMemberCannotListAccounts(t *testing.T) {
	s, cookie := newServerWithMemberSession(t)
	rec := getJSONAs(t, s, "/api/users", cookie)
	if rec.Code != 403 {
		t.Errorf("code = %d, want 403", rec.Code)
	}
}

func TestAMemberCannotCreateAnAccount(t *testing.T) {
	s, cookie := newServerWithMemberSession(t)
	rec := postJSONAs(t, s, "/api/users",
		`{"username":"mallory","password":"correct-horse-battery","role":"admin"}`, cookie)
	if rec.Code != 403 {
		t.Errorf("code = %d, want 403", rec.Code)
	}
	if n, _ := s.deps.DB.UserCount(t.Context()); n != 2 {
		t.Error("a member created an account")
	}
}

func TestAnAdminCreatesAMember(t *testing.T) {
	s, _, cookie := newServerWithSession(t) // founding admin
	rec := postJSONAs(t, s, "/api/users",
		`{"username":"bob","password":"correct-horse-battery","role":"member"}`, cookie)
	if rec.Code != 201 {
		t.Fatalf("code = %d, want 201: %s", rec.Code, rec.Body)
	}
	u, ok, _ := s.deps.DB.UserByUsername(t.Context(), "bob")
	if !ok || u.Role != "member" {
		t.Errorf("account not created as a member: ok=%v role=%q", ok, u.Role)
	}
}

func TestTheLastAdminCannotBeRemoved(t *testing.T) {
	// There is no recovery path. A console whose last administrator deleted
	// themselves cannot be managed again by any route the code provides.
	s, uid, cookie := newServerWithSession(t)
	rec := deleteAs(t, s, "/api/users/"+uid, cookie)
	if rec.Code != 409 {
		t.Fatalf("code = %d, want 409", rec.Code)
	}
	if n, _ := s.deps.DB.AdminCount(t.Context()); n != 1 {
		t.Error("the last administrator was removed")
	}
}

func TestRemovingAnAccountSignsItOut(t *testing.T) {
	s, _, adminCookie := newServerWithSession(t)
	memberID, memberCookie := seedMember(t, s)

	rec := deleteAs(t, s, "/api/users/"+memberID, adminCookie)
	if rec.Code != 204 {
		t.Fatalf("code = %d, want 204: %s", rec.Code, rec.Body)
	}
	if _, ok, _ := s.deps.DB.TouchSession(t.Context(), memberCookie, sessionTTL); ok {
		t.Error("a removed account still has a live session")
	}
}

func TestListingNeverCarriesAHash(t *testing.T) {
	s, _, cookie := newServerWithSession(t)
	rec := getJSONAs(t, s, "/api/users", cookie)
	if strings.Contains(rec.Body.String(), "password_hash") || strings.Contains(rec.Body.String(), "$2a$") {
		t.Errorf("the listing carried a password hash: %s", rec.Body)
	}
}
```

- [ ] **Step 2: Run it to make sure it fails**

```bash
export TMPDIR=/var/tmp/dr-build; export PATH=$PATH:/usr/local/go/bin
go test ./internal/admin/ -run 'TestAMember|TestAnAdmin|TestTheLastAdmin|TestRemovingAnAccount|TestListingNever' -count=1 -v
```

Expected: FAIL — 404, the routes do not exist.

- [ ] **Step 3: Write the implementation**

Create `internal/admin/usersapi.go` with a `requireAdmin` helper checked in-handler:

```go
// requireAdmin answers whether the caller may manage accounts.
//
// Checked in the handler rather than as a fourth routeAuth tier: only three
// endpoints care, and a guard tier implies a permission model the rest of the
// console does not have. Every other route stays reachable by every account.
func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	u, ok, err := s.deps.DB.UserByID(r.Context(), userFrom(r.Context()))
	if err != nil {
		internalError(w, r, err)
		return false
	}
	if !ok || u.Role != store.RoleAdmin {
		writeError(w, http.StatusForbidden, "only an administrator can manage accounts")
		return false
	}
	return true
}
```

`handleListUsers` returns `[]{id, username, role, created_at}` — no hash, because `Users` never selects one. `handleCreateUser` validates the username and the password against the same floors the claim uses, rejects an unknown role, and returns 201. `handleDeleteUser` refuses with 409 when the target is the last admin (`AdminCount() <= 1 && target.Role == RoleAdmin`), and otherwise deletes, letting the cascade take the sessions; it returns 204.

Add three rows to `routeTable` in `internal/admin/admin.go`, beside the existing session routes:

```go
		{"GET", "/api/users", routeSession, s.handleListUsers},
		{"POST", "/api/users", routeCSRF, s.handleCreateUser},
		{"DELETE", "/api/users/{id}", routeCSRF, s.handleDeleteUser},
```

- [ ] **Step 4: Run the tests and make sure they pass**

```bash
export TMPDIR=/var/tmp/dr-build; export PATH=$PATH:/usr/local/go/bin
go test ./internal/admin/ -count=1
```

Expected: PASS.

- [ ] **Step 5: Prove the tests can fail**

Return `true` unconditionally from `requireAdmin` and confirm both member tests go red. Drop the `AdminCount` guard and confirm `TestTheLastAdminCannotBeRemoved` goes red. Restore both.

- [ ] **Step 6: Commit**

```bash
git add internal/admin/usersapi.go internal/admin/usersapi_test.go internal/admin/admin.go
git commit -m "feat(admin): manage accounts as an administrator"
```

---

### Task 16: Accounts section on the Settings screen

**Files:**
- Create: `web/src/features/settings/accounts-card.tsx`, `web/src/features/settings/accounts-card.test.tsx`
- Modify: `web/src/features/settings/settings-screen.tsx` (render the card), `web/src/lib/queries.ts` (add `useUsers`)

**Interfaces:**
- Consumes: `GET/POST/DELETE /api/users` (Task 15).
- Produces: nothing other tasks consume.

**Implementer:** dr-superpowers:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 1 = 3

- [ ] **Step 1: Write the failing test**

Create `web/src/features/settings/accounts-card.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react"
import { describe, expect, it } from "vitest"
import { AccountsCard } from "./accounts-card"

const ADMIN = { id: "u1", username: "alice", role: "admin", created_at: "2026-09-10T00:00:00Z" }
const MEMBER = { id: "u2", username: "bob", role: "member", created_at: "2026-09-10T01:00:00Z" }

describe("AccountsCard", () => {
  it("lists every account with its role", () => {
    render(<AccountsCard users={[ADMIN, MEMBER]} me="u1" />)
    expect(screen.getByText("alice")).toBeInTheDocument()
    expect(screen.getByText("bob")).toBeInTheDocument()
    expect(screen.getByText(/administrator/i)).toBeInTheDocument()
  })

  it("marks which account is mine and offers no remove for it", () => {
    render(<AccountsCard users={[ADMIN, MEMBER]} me="u1" />)
    expect(screen.getByText(/this is you/i)).toBeInTheDocument()
    const removes = screen.getAllByRole("button", { name: /remove/i })
    expect(removes).toHaveLength(1)
  })

  it("says the console is not shared when there is one account", () => {
    render(<AccountsCard users={[ADMIN]} me="u1" />)
    expect(screen.getByText(/only account/i)).toBeInTheDocument()
    expect(screen.queryByRole("button", { name: /remove/i })).not.toBeInTheDocument()
  })

  it("warns that a removed account loses its sessions immediately", () => {
    render(<AccountsCard users={[ADMIN, MEMBER]} me="u1" />)
    expect(screen.getByText(/signed out/i)).toBeInTheDocument()
  })
})
```

- [ ] **Step 2: Run it to make sure it fails**

```bash
cd web && npx vitest run src/features/settings/accounts-card.test.tsx
```

Expected: FAIL — cannot resolve `./accounts-card`.

- [ ] **Step 3: Write the component**

Create `web/src/features/settings/accounts-card.tsx` rendering a heading, a one-line explanation that every account can use every screen and an administrator can additionally manage accounts, then one row per account: username, a role badge, "this is you" on the caller's own row, and a Remove button on every other row. When there is one account, say so and render no Remove. Include the sentence that removing an account signs it out immediately. Add "Add an account" opening a dialog with username, password, confirmation and a role select.

Render it from `settings-screen.tsx` below the existing Password card. Add `useUsers` to `web/src/lib/queries.ts` following the shape of `usePolicy` at `queries.ts:249`.

Typography: `text-sm` floor, no custom sizes, badges by colour and weight.

- [ ] **Step 4: Run the tests and make sure they pass**

```bash
cd web && npx vitest run src/features/settings/
```

Expected: PASS.

- [ ] **Step 5: Prove the tests can fail**

Render a Remove button on the caller's own row and confirm the second test goes red. Restore.

- [ ] **Step 6: Commit**

```bash
git add web/src/features/settings/ web/src/lib/queries.ts
git commit -m "feat(console): manage accounts from Settings"
```

---

### Task 17: Documentation sweep

**Files:**
- Modify: `README.md`, `docs/design/configuration.md`, `docs/design/security.md`, `docs/operations/verification.md`, `CLAUDE.md`, `compose.yml`, `.env.example`

**Interfaces:**
- Consumes: nothing.
- Produces: nothing.

**Implementer:** dr-superpowers:impl-sonnet-high
**Evaluation:** files 3 - spec 0 - coupling 0 - risk 0 = 3

- [ ] **Step 1: Find every surviving reference**

```bash
grep -rn 'ADMIN_PASSWORD_HASH\|setup token\|one password\|hash-password' \
  README.md CLAUDE.md docs/ compose.yml .env.example --include='*.md' --include='*.yml' --include='*.example' \
  | grep -v docs/superpowers/
```

Work the list top to bottom. Every hit is either corrected or, where it describes history deliberately, left with a sentence saying so.

- [ ] **Step 2: Correct each file**

`README.md` — the quick-start describes claiming the console, not setting a hash. Remove the `hash-password` invocation.

`docs/design/configuration.md` — `DARKROUTER_ADMIN_PASSWORD_HASH` leaves the bootstrap table. The remaining bootstrap set is the database path, the master key, the two listen addresses, the proxy token, and the log level and format.

`docs/design/security.md` — the threat model section covering the setup token is rewritten: the claim is now trust-on-first-use, the window between first start and the claim is a stated risk, and the startup warning is the mitigation. Say plainly that this is weaker than requiring host access.

`docs/operations/verification.md` — the step that seeds a hash becomes claiming an account.

`CLAUDE.md` — the "Verifying a change in the running console" section: `.uat-credentials` now holds a username and a password for an account, not a password checked against a hash in `.env`.

`compose.yml` and `.env.example` — remove the variable and its comment block.

- [ ] **Step 3: Confirm the sweep is complete**

```bash
grep -rn 'ADMIN_PASSWORD_HASH\|setup token\|hash-password' \
  README.md CLAUDE.md docs/ compose.yml .env.example | grep -v docs/superpowers/
```

Expected: no output, or only lines that explicitly describe history.

- [ ] **Step 4: Confirm the drift guard still passes**

```bash
export TMPDIR=/var/tmp/dr-build; export PATH=$PATH:/usr/local/go/bin
go test ./internal/config/ -run TestDoc -count=1 -v
```

Expected: PASS. `docs_test.go` anchors on `configuration.md`, which this task edits.

- [ ] **Step 5: Commit**

```bash
git add README.md CLAUDE.md docs/ compose.yml .env.example
git commit -m "docs: describe accounts instead of a shared password"
```

---

### Task 18: Record the reversal and refresh the mockups

**Files:**
- Modify: `docs/plan/decisions.md`, `docs/ux/mockups/fragments/16-login.html`, `docs/ux/mockups/index.html`, `docs/ux/mockups/artifact.html`

**Interfaces:**
- Consumes: nothing.
- Produces: nothing.

**Implementer:** dr-superpowers:impl-sonnet-low
**Evaluation:** files 1 - spec 0 - coupling 0 - risk 0 = 1

- [ ] **Step 1: Add the decision entry**

Append to the appropriate section of `docs/plan/decisions.md`:

```markdown
**The first account to be created claims the console; there is no setup token
and no password recovery.** *Do not re-litigate.*

`mintSetupToken` argued the opposite, in a comment that survived until this
change: a token from the startup log kept the claim to whoever could read the
host rather than whoever could reach the port, and letting the first visitor
claim it "would make an empty hash open the port rather than close it." That
reasoning was correct. It was overridden deliberately, for convenience on a
single-operator deployment, with the operator told what it costs.

What replaces the token is weaker and is meant to be understood as weaker: a
startup warning while a populated database has no account, and a line in
deploy.md. Between a first start and the claim, anyone who can reach the admin
port can become the administrator, and both ports bind every interface.

`DARKROUTER_ADMIN_PASSWORD_HASH` was retired in the same change, so there is no
recovery path at all. An administrator who forgets their password edits the
database by hand or loses the console.
```

- [ ] **Step 2: Update the login mockups**

In `docs/ux/mockups/fragments/16-login.html`, add a username field above the password field and retitle the claim variant to ask for a username, a password and a confirmation rather than a token. Update the surrounding copy in `index.html` and `artifact.html` where it describes the token.

Pixel font sizes are permitted here and only here — the mockups are a standalone document that never loads darkraise-ui. `qa.py`'s 30px ceiling is the only limit that applies.

- [ ] **Step 3: Run the mockup checks**

```bash
python3 docs/ux/mockups/qa.py
```

Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add docs/plan/decisions.md docs/ux/mockups/
git commit -m "docs(decisions): record the console claim reversal"
```

---

## Final gate

- [ ] `go build ./... && go vet ./... && go test ./... -count=1` all exit 0
- [ ] `cd web && npm test` passes every file
- [ ] `grep -rn 'ADMIN_PASSWORD_HASH' --include='*.go' --include='*.md' --include='*.yml' . | grep -v docs/superpowers/` returns nothing
- [ ] Rebuild and redeploy per `docs/operations/deploy.md`, with the `compose.uat.yml` overlay
- [ ] Byte-compare the served bundle against a host build, per that document
- [ ] Log in as the account claimed at the phase 1 gate; add a member account, confirm it can reach the Playground and cannot reach `/api/users`; remove it and confirm its session died
