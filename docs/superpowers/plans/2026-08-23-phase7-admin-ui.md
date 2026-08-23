# Phase 7 — Admin API and UI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A session-authenticated admin API and an embedded single-page dashboard that answer "is anything broken", "why did that request do that", "what can I route to", and "does this credential work".

**Architecture:** A new `internal/admin` package owns the REST API, session authentication, SPA serving and the dev-mode Vite reverse proxy, keeping it out of `internal/server`, which serves the proxy port. The API is read-mostly: it does CRUD on providers and credentials only, and renders aliases and policy read-only, which is what holds the endpoint count at twenty-one instead of letting it grow without bound. The SPA is React scaffolded from `darkraise-ui`, built to `web/dist` and embedded with `go:embed`, with TanStack Query as the only server-state store and polling rather than websockets.

**Tech Stack:** Go 1.26, `modernc.org/sqlite`, `golang.org/x/crypto/bcrypt`; React 19, Vite, Tailwind 4, TanStack Router/Query/Table/Form, recharts 3, `darkraise-ui` 6.4.0, Node 24.

**Spec:** `docs/superpowers/specs/2026-08-22-darkrouter-phase7-admin-ui.md`

## Global Constraints

- Go 1.26. Module path `github.com/darkraise/darkrouter`. The shipped binary builds with `CGO_ENABLED=0`.
- English only in code, comments, commits, and errors.
- Commits are `<type>(<scope>): <subject>`, subject at most 50 characters, imperative, no trailing period.
- **Every task ends green.** `export PATH=$PATH:/usr/local/go/bin` first; the toolchain is not on `PATH`. Run `go test ./... -race -count=1`, `go vet ./...`, and `gofmt -l .` before committing. `gofmt -l .` must print nothing. From Task 24 onward, `npm --prefix web run build` must also succeed.
- **Ports 8080 and 8081 are occupied by an unrelated application.** Every smoke run binds 18080 (proxy) and 18081 (admin). Never kill a process this plan did not start, and kill by binary name — `ps -C <name> -o pid=` — because `nohup … &` inside a compound command returns the subshell's pid, not the binary's.
- **`DARKROUTER_MASTER_KEY` must be set for any run of the binary**, including smoke tests. A throwaway value is fine.
- **`sqlite3` is not installed.** Read the database with the `dbq` helper — `go run /tmp/dbq/main.go <db-path> "<query>"`, written in Task 3 Step 6 and reused throughout. Run it from the repository root so it resolves `modernc.org/sqlite` from this module.
- **The proxy port must never honor cookies.** Cookies are not port-scoped, so the proxy and admin ports share a jar. Only the bearer token authenticates the proxy port, and Task 6 pins that.
- **No endpoint returns credential material.** Not plaintext, not ciphertext, not for editing, not for export. Replacing a credential means adding a new one and deleting the old. Task 10 pins it across every endpoint at once rather than per handler.
- **`golang.org/x/crypto` is the one new direct dependency this phase adds.** The module has five direct dependencies today and the bar for a sixth is that the standard library has no bcrypt. Nothing else is added; if a task seems to need a library, it does not.

---

## What already exists, and what this phase must not re-derive

Read this before Task 1. Several of these facts changed what the tasks below do.

- **`internal/admin` does not exist.** Master design §5 assigns it "Admin REST API, session auth, SPA serving, Vite reverse-proxy in dev mode". `internal/server` keeps the proxy port and its existing `/healthz`, `/readyz` and `/metrics`, which move under the new package's mux rather than being rewritten.
- **The `sessions` table already exists** from migration 0001: `id TEXT PRIMARY KEY, created_at INTEGER, expires_at INTEGER`. It needs no migration. Sliding expiry is an `UPDATE` of `expires_at`; there is no `csrf` column and there must not be one, because the CSRF token is *derived* from the session id by HMAC rather than stored, which is what makes it impossible for the two to drift apart.
- **The `settings` table already exists**, `key TEXT PRIMARY KEY, value TEXT NOT NULL`. The CSRF HMAC secret lives there under `csrf_secret`, created on first admin boot.
- **`requests` carries only `idx_requests_ts`.** Spec §4.2's keyset promise is theoretical without a composite `(ts DESC, id DESC)` index and indexes on the filter columns. Task 1 adds them as migration 0004.
- **`health.Breaker.Record` already resets a ladder on success.** `OutcomeSuccess` deletes the entry outright. The probe therefore needs no new breaker API — but it must record against **two** keys, because a credential-level cooldown is stored under `Key{ProviderID, KeyID}` with an **empty** `Model`, while a triple cooldown is stored under `Key{ProviderID, KeyID, Model}`. Recording success against only the triple leaves a cooling credential cooling, which is exactly the "probe OK beside still cooling" confusion spec §4.3 exists to prevent.
- **`store.AddCredential` and `store.Credentials` exist**; provider create, update and delete do not, and neither does credential delete. Task 8 adds them.
- **`provider.SQLSource` already reads providers from the database** and exposes `Reload`. Every mutation endpoint must call it, or the running router keeps serving the old provider set until the next natural reload.
- **`store.RequestRecord` gained `SurfaceMeta`, `ResponseBytes` and `ResponseContentType` in phase 5.** The trace endpoint returns them; the drawer renders them.
- **`request_bodies` has no writer.** Phase 5 recorded this. `GET /api/requests/:id` returns an empty body list, and the drawer must render that as "not captured" rather than as an error or an empty panel that looks broken.
- **`config.Config.Warnings` and `config.Store.LastError()` already carry validation state.** `GET /api/config` reports them; nothing new computes validity.
- **Node 24.18 and npm 12 are installed, and `create-darkraise-ui` publishes 6.4.0** — the exact version spec §5 names. Verified 2026-08-23.

---

## File Structure

**Part A — the admin API.** Complete, curl-testable software on its own.

| File | Responsibility |
|---|---|
| `internal/store/migrations/0004_admin.sql` | Keyset and filter indexes on `requests`. |
| `internal/admin/admin.go` | The `Server` type, its dependencies, and the mux. |
| `internal/admin/session.go` | Session create, lookup with sliding expiry, delete, startup sweep. |
| `internal/admin/csrf.go` | HMAC token derivation and constant-time verification. |
| `internal/admin/auth.go` | The middleware: session, CSRF, `Origin`/`Sec-Fetch-Site`. |
| `internal/admin/authapi.go` | `/api/auth/login`, `/logout`, `/status`. |
| `internal/admin/providers.go` | Provider and credential CRUD endpoints. |
| `internal/admin/probe.go` | `/api/providers/:id/test`, its mutex, and the ladder reset. |
| `internal/admin/catalog.go` | `/api/presets`, `/api/models`. |
| `internal/admin/requests.go` | `/api/requests`, `/api/requests/:id`. |
| `internal/admin/cursor.go` | The keyset cursor: encode, decode, filter-hash rejection. |
| `internal/admin/usage.go` | `/api/usage`, `/api/overview`. |
| `internal/admin/configapi.go` | `/api/config`, `/api/config/reload`. |
| `internal/admin/playground.go` | `/api/playground`, SSE. |
| `internal/store/adminstore.go` | Provider and credential mutations, session rows, request queries. |

**Part B — the dashboard.**

| File | Responsibility |
|---|---|
| `internal/admin/spa.go` | `go:embed` serving and the dev-mode Vite reverse proxy. |
| `web/` | The scaffolded SPA. |
| `web/src/lib/api.ts` | Fetch wrapper: CSRF header, 401 handling, typed responses. |
| `web/src/routes/*.tsx` | One file per screen. |
| `web/src/components/trace-drawer.tsx` | The request trace, which is the screen worth building. |

---

# Part A — The admin API

Tasks 1 to 23. At the end of Task 23 the API is complete and curl-testable
without a single line of frontend code, which is what makes it a deliverable
rather than half of one.

---

### Task 1: Migration 0004, the indexes keyset pagination needs

**Files:**
- Create: `internal/store/migrations/0004_admin.sql`
- Test: `internal/store/migrate_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: the composite and filter indexes Task 17's cursor relies on.

Spec §4.2 ends with "without them the keyset promise is theoretical", and it is
right: `requests` carries only `idx_requests_ts` today, so `ORDER BY ts DESC, id
DESC` sorts on a partial index and every filtered query is a scan. This is a
schema task rather than part of the query task because a migration is reviewed
differently from a handler, and because the index has to exist before any
benchmark of the query means anything.

Additive only, like 0003: `CREATE INDEX` creates no columns and drops nothing,
so a failed run leaves the phase 5 schema exactly as it was.

- [ ] **Step 1: Write the failing test**

Add to `internal/store/migrate_test.go`:

```go
func TestMigrationsReachVersionFour(t *testing.T) {
	ms, err := loadMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 4 {
		t.Fatalf("loaded %d migrations, want 4", len(ms))
	}
}

func TestTheKeysetIndexExists(t *testing.T) {
	// Spec §4.2: the keyset promise is theoretical without this. A query
	// planner check is the only assertion that cannot pass by accident.
	db := migrated(t)
	var plan string
	row := db.Read.QueryRow(
		`EXPLAIN QUERY PLAN
		 SELECT id FROM requests WHERE (ts, id) < (?, ?) ORDER BY ts DESC, id DESC LIMIT 50`,
		1, "z")
	var a, b, c int
	if err := row.Scan(&a, &b, &c, &plan); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan, "idx_requests_keyset") {
		t.Errorf("query plan = %q; the keyset query is not using its index", plan)
	}
}

func TestTheFilterIndexesExist(t *testing.T) {
	// Spec §4.2 names provider, model, status, surface as filter columns.
	// An index missing here turns a filtered page into a full scan.
	db := migrated(t)
	rows, err := db.Read.Query(
		`SELECT name FROM sqlite_master WHERE type = 'index' AND tbl_name = 'requests'`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	have := map[string]bool{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		have[n] = true
	}
	for _, want := range []string{
		"idx_requests_keyset", "idx_requests_provider", "idx_requests_model",
		"idx_requests_status", "idx_requests_surface",
	} {
		if !have[want] {
			t.Errorf("index %s is missing; have %v", want, have)
		}
	}
}
```

`TestMigrateIsIdempotent` pins the version — it was updated to 3 in phase 5 and
must move to 4 here. `TestMigrationThreeIsAdditive` indexes `ms[2]` and is
unaffected. **Read both before editing** rather than assuming their shape.

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/store/ -run 'TestMigrationsReachVersionFour|TestTheKeysetIndex|TestTheFilterIndexes|TestMigrateIsIdempotent' -v
```

Expected: `TestMigrationsReachVersionFour` reports 3; the index tests report the
indexes missing; `TestMigrateIsIdempotent` still passes at 3 and will need its
constant moved in Step 3.

- [ ] **Step 3: Write the migration**

Create `internal/store/migrations/0004_admin.sql`:

```sql
-- Phase 7 schema, per spec section 4.2.
--
-- Indexes only. Nothing is created, dropped or rewritten, so a failed run
-- leaves the phase 5 schema exactly as it was.
--
-- The admin request log is read newest-first and paginated by keyset on
-- (ts, id) rather than by offset: offset degrades as the table grows and skips
-- rows when new ones land mid-scroll. That ordering needs its own composite
-- index; idx_requests_ts covers only the leading column, so the tie-break on an
-- identical timestamp falls back to a scan.
CREATE INDEX idx_requests_keyset ON requests(ts DESC, id DESC);

-- The four filter columns spec section 4.2 names. A filtered page without these
-- is a full table scan wearing a LIMIT, which is the failure the keyset design
-- exists to avoid.
CREATE INDEX idx_requests_provider ON requests(final_provider_id);
CREATE INDEX idx_requests_model ON requests(final_model);
CREATE INDEX idx_requests_status ON requests(status);
CREATE INDEX idx_requests_surface ON requests(surface);
```

Then change `TestMigrateIsIdempotent`'s expected version from 3 to 4.

- [ ] **Step 4: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/store/ -race -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'
```

Expected: PASS, every test in the package.

- [ ] **Step 5: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing.

- [ ] **Step 6: Commit**

```bash
git add internal/store/migrations/0004_admin.sql internal/store/migrate_test.go
git commit -m "feat(store): index the request log for keyset paging"
```

---

### Task 2: The admin password

**Files:**
- Create: `internal/admin/password.go`
- Test: `internal/admin/password_test.go`
- Modify: `go.mod`, `go.sum`

**Interfaces:**
- Consumes: nothing.
- Produces: `admin.VerifyPassword(hash, password string) bool` and `admin.HashPassword(password string) (string, error)`. Task 5's login handler calls the first.

Spec §3: bcrypt at cost 12 against `DARKROUTER_ADMIN_PASSWORD_HASH`. This is its
own task because it adds the phase's only new dependency and because a
password check that silently accepts everything is the worst possible defect to
find later — it deserves its own review gate.

`HashPassword` exists so an operator can generate a hash without a second tool.
Task 22 wires it to a `-hash-password` flag.

**Constant time is not optional and is not automatic.** `bcrypt.CompareHashAndPassword`
is constant-time in the comparison, but an empty or malformed stored hash makes
it return an error, and returning `err == nil` from a helper that also treats
"no hash configured" as a pass is how an admin port ends up open. The empty-hash
case is a refusal, tested below.

- [ ] **Step 1: Write the failing test**

Create `internal/admin/password_test.go`:

```go
package admin

import "testing"

func TestAHashedPasswordVerifies(t *testing.T) {
	h, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(h, "correct horse battery staple") {
		t.Error("the correct password did not verify")
	}
	if VerifyPassword(h, "wrong") {
		t.Error("a wrong password verified")
	}
}

func TestAnEmptyHashRefusesEveryPassword(t *testing.T) {
	// An unconfigured DARKROUTER_ADMIN_PASSWORD_HASH must close the admin
	// port, not open it. A helper that returns true here is how a dashboard
	// ends up unauthenticated on a LAN.
	for _, pw := range []string{"", "anything", "admin"} {
		if VerifyPassword("", pw) {
			t.Errorf("an empty hash accepted %q", pw)
		}
	}
}

func TestAMalformedHashRefusesEveryPassword(t *testing.T) {
	// A truncated or hand-edited value in the environment must fail closed.
	for _, h := range []string{"not-a-hash", "$2a$", "$2a$12$tooshort"} {
		if VerifyPassword(h, "anything") {
			t.Errorf("malformed hash %q accepted a password", h)
		}
	}
}

func TestTheHashIsBcryptCostTwelve(t *testing.T) {
	// Spec §3 fixes the cost. A lower one is a silent downgrade that no
	// behavioral test would catch.
	h, err := HashPassword("x")
	if err != nil {
		t.Fatal(err)
	}
	if len(h) < 4 || h[:4] != "$2a$" {
		t.Fatalf("hash = %q, want a bcrypt 2a hash", h)
	}
	if h[4:6] != "12" {
		t.Errorf("cost = %q, want 12", h[4:6])
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/admin/ -v
```

Expected: FAIL — the package does not exist.

- [ ] **Step 3: Add the dependency**

```bash
export PATH=$PATH:/usr/local/go/bin
go get golang.org/x/crypto@latest
go mod tidy
grep 'golang.org/x/crypto' go.mod
```

Expected: `golang.org/x/crypto` appears as a **direct** requirement. If it lands
under the `// indirect` block, a later `go mod tidy` will remove it; write the
implementation first and re-run `go mod tidy`.

- [ ] **Step 4: Write the implementation**

Create `internal/admin/password.go`:

```go
// Package admin serves the operator dashboard: the REST API, session
// authentication, the embedded SPA, and the Vite reverse proxy used in dev.
//
// It is separate from internal/server, which owns the proxy port, because the
// two have opposite security postures. The proxy port is open on the LAN behind
// an optional bearer token; the admin port requires a session and must never
// honor one on the proxy side, since cookies are not port-scoped.
package admin

import (
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

// passwordCost is spec §3's bcrypt cost. It is a named constant rather than a
// literal so a downgrade is a visible edit rather than a typo.
const passwordCost = 12

// HashPassword produces a hash for DARKROUTER_ADMIN_PASSWORD_HASH. It exists so
// an operator can generate one with the binary they already have rather than
// installing a second tool.
func HashPassword(password string) (string, error) {
	if password == "" {
		return "", fmt.Errorf("password is empty")
	}
	h, err := bcrypt.GenerateFromPassword([]byte(password), passwordCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(h), nil
}

// VerifyPassword reports whether password matches hash.
//
// It fails closed on an empty or malformed hash. An unconfigured
// DARKROUTER_ADMIN_PASSWORD_HASH must close the admin port rather than open it,
// and a helper that conflated "nothing configured" with "anything accepted" is
// how a dashboard ends up unauthenticated on a LAN.
func VerifyPassword(hash, password string) bool {
	if hash == "" || password == "" {
		return false
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}
```

- [ ] **Step 5: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go mod tidy
go test ./internal/admin/ -race -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'
```

Expected: PASS, four tests. `go mod tidy` after the implementation exists is
what moves `golang.org/x/crypto` out of the indirect block.

- [ ] **Step 6: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
grep -A8 '^require (' go.mod | head -12
```

Expected: all packages `ok`, and `golang.org/x/crypto` listed as a direct
requirement rather than an indirect one.

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum internal/admin/password.go internal/admin/password_test.go
git commit -m "feat(admin): verify the operator password with bcrypt"
```

---

### Task 3: Sessions in the database

**Files:**
- Create: `internal/store/adminstore.go`
- Test: `internal/store/adminstore_test.go`

**Interfaces:**
- Consumes: the `sessions` table from migration 0001.
- Produces: `(*DB).CreateSession(ctx, id string, ttl time.Duration) error`, `(*DB).TouchSession(ctx, id string, ttl time.Duration) (bool, error)`, `(*DB).DeleteSession(ctx, id string) error`, `(*DB).SweepSessions(ctx) (int, error)`. Task 5's handlers and Task 4's middleware call all four.

Spec §3: sessions live in the database rather than in memory so a restart does
not log the operator out mid-task, with a sliding thirty-day expiry and a
startup sweep for expired rows.

**`TouchSession` both validates and extends, and returning a bool rather than an
error for the miss is deliberate.** A missing session and a database failure are
different things to a caller: the first renders the login screen, the second is a
500. Collapsing them into one error makes an outage look like a logout.

**Expiry is checked in the `UPDATE`'s `WHERE`, not read-then-compared.** Two
concurrent requests on a session expiring this instant must not disagree about
whether it is alive, and a read-then-write leaves exactly that window.

- [ ] **Step 1: Write the failing test**

Create `internal/store/adminstore_test.go`:

```go
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
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/store/ -run Session -v
```

Expected: FAIL to build — `db.CreateSession undefined`.

- [ ] **Step 3: Write the implementation**

Create `internal/store/adminstore.go`:

```go
package store

import (
	"context"
	"fmt"
	"time"
)

// CreateSession writes a new session row. The caller mints the id; this does not
// generate one, because the id is a security-relevant value and the code that
// chooses its entropy should be the code that owns the decision.
func (d *DB) CreateSession(ctx context.Context, id string, ttl time.Duration) error {
	now := time.Now()
	if _, err := d.Write.ExecContext(ctx,
		`INSERT INTO sessions (id, created_at, expires_at) VALUES (?, ?, ?)`,
		id, now.UnixMilli(), now.Add(ttl).UnixMilli()); err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	return nil
}

// TouchSession validates a session and slides its expiry in one statement.
//
// The expiry test lives in the WHERE rather than in a read followed by a
// comparison: two concurrent requests on a session expiring this instant must
// not disagree about whether it is alive, and a read-then-write leaves exactly
// that window open. It also means an expired row can never be extended by the
// call that just decided it was dead.
//
// A miss is reported as false rather than as an error, because a missing session
// and a database failure are different things to the caller: the first renders
// the login screen, the second is a 500.
func (d *DB) TouchSession(ctx context.Context, id string, ttl time.Duration) (bool, error) {
	now := time.Now()
	res, err := d.Write.ExecContext(ctx,
		`UPDATE sessions SET expires_at = ? WHERE id = ? AND expires_at > ?`,
		now.Add(ttl).UnixMilli(), id, now.UnixMilli())
	if err != nil {
		return false, fmt.Errorf("touch session: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("touch session: %w", err)
	}
	return n > 0, nil
}

// DeleteSession removes the row. Spec §3: logout deletes rather than only
// clearing the cookie, because a cleared cookie leaves a valid session id in the
// database for anyone who copied it.
func (d *DB) DeleteSession(ctx context.Context, id string) error {
	if _, err := d.Write.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// SweepSessions prunes expired rows and reports how many went. It runs at
// startup: sessions outlive the process, so nothing else would ever remove them.
func (d *DB) SweepSessions(ctx context.Context) (int, error) {
	res, err := d.Write.ExecContext(ctx,
		`DELETE FROM sessions WHERE expires_at <= ?`, time.Now().UnixMilli())
	if err != nil {
		return 0, fmt.Errorf("sweep sessions: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("sweep sessions: %w", err)
	}
	return int(n), nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/store/ -run Session -race -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'
```

Expected: PASS, seven tests.

- [ ] **Step 5: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing.

- [ ] **Step 6: Write the database query helper this plan reuses**

`sqlite3` is not installed and several later verification steps have to read the
database. Write it once, here, and reuse it:

```bash
export PATH=$PATH:/usr/local/go/bin
mkdir -p /tmp/dbq
cat > /tmp/dbq/main.go <<'EOF'
// Command dbq prints the rows of one query. It exists because sqlite3 is not
// installed and the verification steps have to read the database.
package main

import (
	"database/sql"
	"fmt"
	"os"
	"strings"

	_ "modernc.org/sqlite"
)

func main() {
	db, err := sql.Open("sqlite", "file:"+os.Args[1]+"?mode=ro")
	if err != nil {
		panic(err)
	}
	defer db.Close()
	rows, err := db.Query(os.Args[2])
	if err != nil {
		panic(err)
	}
	defer rows.Close()
	cols, _ := rows.Columns()
	fmt.Println(strings.Join(cols, " | "))
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			panic(err)
		}
		out := make([]string, len(vals))
		for i, v := range vals {
			out[i] = fmt.Sprintf("%v", v)
		}
		fmt.Println(strings.Join(out, " | "))
	}
	if err := rows.Err(); err != nil {
		panic(err)
	}
}
EOF
echo "dbq written"
```

Run it from the repository root so it resolves `modernc.org/sqlite` from this
module.

- [ ] **Step 7: Commit**

```bash
git add internal/store/adminstore.go internal/store/adminstore_test.go
git commit -m "feat(store): persist admin sessions"
```

---

### Task 4: The CSRF token, bound to the session by HMAC

**Files:**
- Create: `internal/admin/csrf.go`
- Test: `internal/admin/csrf_test.go`

**Interfaces:**
- Consumes: `(*store.DB)` for the `settings` table.
- Produces: `admin.NewCSRF(ctx, db *store.DB) (*CSRF, error)`, `(*CSRF).Token(sessionID string) string`, `(*CSRF).Valid(sessionID, token string) bool`. Task 5's middleware calls `Valid`; Task 6's login handler returns `Token`.

Spec §3 rejects naive double-submit, and the reasoning is worth restating
because it is the whole justification for this file: an attacker who can set a
cookie for the host defeats double-submit, and on a plain-HTTP LAN — the default
homelab posture, since there is no TLS configuration — an active network
attacker can do exactly that. Binding the token to the session by HMAC means a
token forged for one session is invalid for another, and a token set by an
attacker who cannot read the session cookie is invalid for the session it
arrives with.

**The secret is its own value, not the encryption key.** `crypto.Key` seals
credentials at rest. Reusing it for authentication tags violates key separation
for no gain, and it is not exported as raw bytes anyway. A 32-byte random lives
in `settings` under `csrf_secret`, created on first admin boot and stable across
restarts — a per-process secret would invalidate every outstanding token on
every deploy, logging the operator out for no reason.

**The token is derived, never stored.** A `csrf` column on `sessions` could drift
from the session it names — deleted separately, updated separately, restored from
a backup separately. Derivation makes that impossible: the token *is* a function
of the session id.

- [ ] **Step 1: Write the failing test**

Create `internal/admin/csrf_test.go`:

```go
package admin

import (
	"context"
	"testing"

	"github.com/darkraise/darkrouter/internal/store"
)

func csrfFor(t *testing.T) (*CSRF, *store.DB) {
	t.Helper()
	db := store.MigratedForTest(t)
	c, err := NewCSRF(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	return c, db
}

func TestATokenValidatesForItsOwnSession(t *testing.T) {
	c, _ := csrfFor(t)
	tok := c.Token("sess-1")
	if tok == "" {
		t.Fatal("empty token")
	}
	if !c.Valid("sess-1", tok) {
		t.Error("a token did not validate for its own session")
	}
}

func TestATokenDoesNotValidateForAnotherSession(t *testing.T) {
	// This is the entire point of binding. A token an attacker obtained or
	// planted is useless against the session it arrives with.
	c, _ := csrfFor(t)
	tok := c.Token("sess-1")
	if c.Valid("sess-2", tok) {
		t.Error("a token from one session validated against another")
	}
}

func TestGarbageDoesNotValidate(t *testing.T) {
	c, _ := csrfFor(t)
	for _, tok := range []string{"", "not-base64!!", "YWJj"} {
		if c.Valid("sess-1", tok) {
			t.Errorf("token %q validated", tok)
		}
	}
}

func TestTheTokenIsStableAcrossRestarts(t *testing.T) {
	// The secret lives in settings rather than in the process. A per-process
	// secret would invalidate every outstanding token on every deploy and log
	// the operator out for no reason.
	c1, db := csrfFor(t)
	tok := c1.Token("sess-1")

	c2, err := NewCSRF(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	if !c2.Valid("sess-1", tok) {
		t.Error("a token minted before a restart did not survive it")
	}
}

func TestTwoDatabasesDoNotShareASecret(t *testing.T) {
	// A secret that was somehow a constant would make every deployment's
	// tokens interchangeable, which is the failure this test would catch.
	c1, _ := csrfFor(t)
	c2, _ := csrfFor(t)
	if c1.Token("sess-1") == c2.Token("sess-1") {
		t.Error("two independent databases produced the same token")
	}
}
```

`store.MigratedForTest` does not exist yet — `migrated(t)` is unexported and
lives in `internal/store`'s own test files, which another package cannot reach.
Export a test helper in Step 3 rather than duplicating the migration logic.

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/admin/ -run CSRF -v
go test ./internal/admin/ -run Token -v
```

Expected: FAIL to build — `undefined: NewCSRF`, `undefined: store.MigratedForTest`.

- [ ] **Step 3: Export a migrated-database helper for other packages**

Create `internal/store/testing.go` — a non-`_test.go` file, because
`internal/admin`'s tests import it:

```go
package store

import (
	"context"
	"path/filepath"
	"testing"
)

// MigratedForTest opens a migrated database in a temp directory.
//
// It lives in a non-test file because internal/admin's tests need it and Go
// does not export helpers from _test.go files across package boundaries. The
// alternative — every package reimplementing Open plus Migrate — is how two
// packages end up testing against differently-shaped databases.
func MigratedForTest(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return db
}
```

**Read `internal/store/migrate_test.go`'s `migrated(t)` first** and match what it
actually does — if it sets pragmas or seeds rows, this must too, or the two
helpers will disagree.

- [ ] **Step 4: Write the implementation**

Create `internal/admin/csrf.go`:

```go
package admin

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"

	"github.com/darkraise/darkrouter/internal/store"
)

// csrfSecretKey is the settings row holding the HMAC secret.
const csrfSecretKey = "csrf_secret"

// CSRF derives per-session tokens.
//
// Spec §3 rejects naive double-submit: an attacker who can set a cookie for the
// host defeats it, and on a plain-HTTP LAN — the default homelab posture, since
// there is no TLS configuration — an active network attacker can do exactly
// that. Binding the token to the session by HMAC means a planted token is
// invalid for the session it arrives with.
//
// The token is derived rather than stored. A column on sessions could drift from
// the session it names — deleted separately, restored from a backup separately.
// A derivation cannot.
type CSRF struct {
	secret []byte
}

// NewCSRF loads the secret, creating it on first use.
//
// The secret lives in the database rather than in the process so tokens survive
// a restart; a per-process secret would log the operator out on every deploy. It
// is its own value rather than the credential encryption key, because reusing an
// encryption key for authentication tags violates key separation for no gain.
func NewCSRF(ctx context.Context, db *store.DB) (*CSRF, error) {
	var enc string
	err := db.Read.QueryRowContext(ctx,
		`SELECT value FROM settings WHERE key = ?`, csrfSecretKey).Scan(&enc)
	if err == nil {
		secret, derr := base64.StdEncoding.DecodeString(enc)
		if derr != nil {
			return nil, fmt.Errorf("csrf secret is not valid base64: %w", derr)
		}
		return &CSRF{secret: secret}, nil
	}

	secret := make([]byte, 32)
	if _, rerr := rand.Read(secret); rerr != nil {
		return nil, fmt.Errorf("generate csrf secret: %w", rerr)
	}
	// INSERT OR IGNORE rather than INSERT: two processes opening the same
	// database concurrently must not fail, and whichever wrote first wins.
	if _, werr := db.Write.ExecContext(ctx,
		`INSERT OR IGNORE INTO settings (key, value) VALUES (?, ?)`,
		csrfSecretKey, base64.StdEncoding.EncodeToString(secret)); werr != nil {
		return nil, fmt.Errorf("store csrf secret: %w", werr)
	}
	// Re-read rather than trusting the local value: if the ignore fired, the
	// other process's secret is the one in the database and the one every
	// outstanding token was minted under.
	if rerr := db.Read.QueryRowContext(ctx,
		`SELECT value FROM settings WHERE key = ?`, csrfSecretKey).Scan(&enc); rerr != nil {
		return nil, fmt.Errorf("read back csrf secret: %w", rerr)
	}
	stored, derr := base64.StdEncoding.DecodeString(enc)
	if derr != nil {
		return nil, fmt.Errorf("csrf secret is not valid base64: %w", derr)
	}
	return &CSRF{secret: stored}, nil
}

// Token is the token for one session.
func (c *CSRF) Token(sessionID string) string {
	mac := hmac.New(sha256.New, c.secret)
	mac.Write([]byte(sessionID))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// Valid reports whether token belongs to sessionID.
//
// hmac.Equal rather than ==: a byte-by-byte comparison that returns early leaks
// how much of a forged token was correct, which is enough to construct one.
func (c *CSRF) Valid(sessionID, token string) bool {
	if sessionID == "" || token == "" {
		return false
	}
	got, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, c.secret)
	mac.Write([]byte(sessionID))
	return hmac.Equal(got, mac.Sum(nil))
}
```

- [ ] **Step 5: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/admin/ ./internal/store/ -race -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'
```

Expected: PASS in both packages.

- [ ] **Step 6: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing.

- [ ] **Step 7: Commit**

```bash
git add internal/admin/csrf.go internal/admin/csrf_test.go internal/store/testing.go
git commit -m "feat(admin): bind csrf tokens to the session"
```

---

### Task 5: The admin server and its auth middleware

**Files:**
- Create: `internal/admin/admin.go`, `internal/admin/auth.go`
- Test: `internal/admin/auth_test.go`

**Interfaces:**
- Consumes: `CSRF` (Task 4), `store.DB` session methods (Task 3), `VerifyPassword` (Task 2).
- Produces: `admin.New(deps Deps) (*Server, error)`, `(*Server).Handler() http.Handler`, and the unexported `requireSession` and `requireCSRF` middleware. Every later task registers its route on this mux.

This is the security boundary, so it gets its own task and its own review gate.
Three checks, and each exists for a stated reason:

- **A session cookie**, validated against the database with a sliding expiry.
- **A CSRF token** on every mutating verb, bound by HMAC (Task 4).
- **An `Origin` or `Sec-Fetch-Site` check**, which is free because the SPA is
  same-origin, and strictly stronger than the token alone.

`GET /api/auth/status` is the one endpoint reachable without a session, because
the SPA calls it to decide whether to render the login screen. Everything else
requires one — including `POST /api/playground`, which is a mutating verb and
carries the header like any other despite streaming its response.

**`Sec-Fetch-Site` is checked before `Origin`, and a missing `Origin` is not a
pass.** A same-origin `fetch` from the SPA sends `Sec-Fetch-Site: same-origin`
in every browser that matters; a cross-site form post sends `cross-site`. Older
clients send neither, and treating "no header at all" as same-origin is how the
check becomes decorative — so a mutating request presenting neither header is
refused.

- [ ] **Step 1: Write the failing test**

Create `internal/admin/auth_test.go`:

```go
package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/darkraise/darkrouter/internal/store"
)

// testServer builds an admin server over a migrated database with one known
// password, and returns it alongside the database so a test can seed rows.
func testServer(t *testing.T) (*Server, *store.DB) {
	t.Helper()
	db := store.MigratedForTest(t)
	hash, err := HashPassword("hunter2")
	if err != nil {
		t.Fatal(err)
	}
	s, err := New(Deps{DB: db, PasswordHash: hash})
	if err != nil {
		t.Fatal(err)
	}
	return s, db
}

// login performs a real login and returns the session cookie and csrf token,
// which is how every mutating test below authenticates.
func login(t *testing.T, s *Server) (*http.Cookie, string) {
	t.Helper()
	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/auth/login",
		strings.NewReader(`{"password":"hunter2"}`))
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", w.Code, w.Body.String())
	}
	var body struct {
		CSRF string `json:"csrf_token"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(w.Result().Cookies()) == 0 {
		t.Fatal("login set no cookie")
	}
	return w.Result().Cookies()[0], body.CSRF
}

func TestEveryEndpointExceptStatusRequiresASession(t *testing.T) {
	// Spec §4: auth/status is reachable without a session because the SPA
	// calls it to decide whether to render the login screen. Everything else
	// is closed.
	s, _ := testServer(t)
	for _, ep := range []struct {
		method, path string
	}{
		{"GET", "/api/overview"}, {"GET", "/api/presets"}, {"GET", "/api/providers"},
		{"GET", "/api/models"}, {"GET", "/api/requests"}, {"GET", "/api/usage"},
		{"GET", "/api/config"}, {"POST", "/api/providers"},
		{"POST", "/api/config/reload"}, {"POST", "/api/playground"},
	} {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(ep.method, ep.path, strings.NewReader("{}"))
		r.Header.Set("Sec-Fetch-Site", "same-origin")
		s.Handler().ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without a session = %d, want 401", ep.method, ep.path, w.Code)
		}
	}
}

func TestAuthStatusIsReachableWithoutASession(t *testing.T) {
	s, _ := testServer(t)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/auth/status", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var body struct {
		Authenticated bool `json:"authenticated"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Authenticated {
		t.Error("authenticated = true without a session")
	}
}

func TestAMutatingRequestWithoutACSRFTokenIsRejected(t *testing.T) {
	s, _ := testServer(t)
	cookie, _ := login(t, s)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/config/reload", nil)
	r.AddCookie(cookie)
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestAMutatingRequestWithAnotherSessionsCSRFTokenIsRejected(t *testing.T) {
	// The token is bound by HMAC, so one lifted from another session is
	// worthless. This is what makes the binding worth having.
	s, _ := testServer(t)
	cookie, _ := login(t, s)
	_, otherToken := login(t, s)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/config/reload", nil)
	r.AddCookie(cookie)
	r.Header.Set("X-CSRF-Token", otherToken)
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestAForgedOriginIsRejected(t *testing.T) {
	// Spec §7 names this case explicitly. A correct CSRF token with a foreign
	// Origin still fails, because the two checks are independent.
	s, _ := testServer(t)
	cookie, token := login(t, s)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/config/reload", nil)
	r.AddCookie(cookie)
	r.Header.Set("X-CSRF-Token", token)
	r.Header.Set("Origin", "https://evil.example")
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestAMutatingRequestWithNeitherSiteHeaderIsRejected(t *testing.T) {
	// Treating "no header at all" as same-origin makes the check decorative.
	s, _ := testServer(t)
	cookie, token := login(t, s)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/api/config/reload", nil)
	r.AddCookie(cookie)
	r.Header.Set("X-CSRF-Token", token)
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestAReadRequestNeedsNoCSRFToken(t *testing.T) {
	// GET is not state-changing, and requiring a token on it would mean the
	// SPA cannot render anything until it has one.
	s, _ := testServer(t)
	cookie, _ := login(t, s)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/config", nil)
	r.AddCookie(cookie)
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, body = %s", w.Code, w.Body.String())
	}
}

func TestAnExpiredSessionIsRejected(t *testing.T) {
	s, db := testServer(t)
	cookie, _ := login(t, s)
	if _, err := db.Write.ExecContext(context.Background(),
		`UPDATE sessions SET expires_at = 1`); err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/api/config", nil)
	r.AddCookie(cookie)
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/admin/ -v
```

Expected: FAIL to build — `undefined: New`, `undefined: Deps`.

- [ ] **Step 3: Write the server**

Create `internal/admin/admin.go`:

```go
package admin

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/darkraise/darkrouter/internal/catalog"
	"github.com/darkraise/darkrouter/internal/config"
	"github.com/darkraise/darkrouter/internal/crypto"
	"github.com/darkraise/darkrouter/internal/health"
	"github.com/darkraise/darkrouter/internal/provider"
	"github.com/darkraise/darkrouter/internal/store"
)

// sessionTTL is spec §3's default, sliding on every authenticated request.
const sessionTTL = 30 * 24 * time.Hour

// sessionCookie is the cookie name. It is not "session" so it cannot collide
// with anything an operator runs on the same host.
const sessionCookie = "darkrouter_session"

// csrfHeader is where the SPA puts the token. A header rather than a form field
// because every request the SPA makes is JSON or SSE, and a header cannot be
// set by a cross-site form post at all.
const csrfHeader = "X-CSRF-Token"

// Deps are the admin server's collaborators. Every field except DB and
// PasswordHash is optional, so a handler test can build a server without
// standing up a router, a catalog and a breaker.
type Deps struct {
	DB           *store.DB
	PasswordHash string

	Config   *config.Store
	Src      *provider.SQLSource
	Key      *crypto.Key
	Catalog  *catalog.Store
	Disc     *catalog.Discoverer
	Breaker  *health.Breaker
	Presets  catalog.Presets
	Warnings []string

	// Dev, when non-empty, is the Vite dev server to reverse-proxy unmatched
	// paths to. Task 24 uses it; it is nil in production.
	Dev string
}

type Server struct {
	deps Deps
	csrf *CSRF
	mux  *http.ServeMux
}

// New builds the admin server and sweeps expired sessions.
//
// The sweep runs here rather than on a timer because sessions outlive the
// process: nothing else would ever remove them, and a thirty-day TTL means a
// long-lived deployment accumulates a row per login forever.
func New(deps Deps) (*Server, error) {
	if deps.DB == nil {
		return nil, fmt.Errorf("admin: DB is required")
	}
	csrf, err := NewCSRF(context.Background(), deps.DB)
	if err != nil {
		return nil, err
	}
	if _, err := deps.DB.SweepSessions(context.Background()); err != nil {
		return nil, fmt.Errorf("admin: sweep sessions: %w", err)
	}
	s := &Server{deps: deps, csrf: csrf}
	s.routes()
	return s, nil
}

func (s *Server) Handler() http.Handler { return s.mux }

// routes registers every endpoint. Read handlers are wrapped in requireSession;
// mutating ones in requireCSRF, which wraps requireSession itself, so a route
// cannot accidentally get one check and not the other.
func (s *Server) routes() {
	s.mux = http.NewServeMux()

	// The one endpoint reachable without a session: the SPA calls it to decide
	// whether to render the login screen.
	s.mux.HandleFunc("GET /api/auth/status", s.handleAuthStatus)
	s.mux.HandleFunc("POST /api/auth/login", s.handleLogin)
	s.mux.HandleFunc("POST /api/auth/logout", s.requireCSRF(s.handleLogout))

	s.mux.HandleFunc("GET /api/config", s.requireSession(s.handleConfig))
	s.mux.HandleFunc("POST /api/config/reload", s.requireCSRF(s.handleConfigReload))
}

// writeJSON is the single response path, so no handler invents its own header
// order or forgets the content type.
func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// writeError is the single error path. The shape is fixed here so the SPA can
// read every failure the same way.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}
```

Create `internal/admin/auth.go`:

```go
package admin

import (
	"context"
	"net/http"
	"net/url"
)

// sessionKey is the context key carrying the authenticated session id, so a
// handler that needs it does not re-read the cookie and re-validate.
type sessionKeyType struct{}

var sessionKey sessionKeyType

func sessionFrom(ctx context.Context) string {
	s, _ := ctx.Value(sessionKey).(string)
	return s
}

// requireSession validates the cookie against the database and slides the
// expiry. A missing or expired session is 401, which is what tells the SPA to
// render the login screen.
func (s *Server) requireSession(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil || c.Value == "" {
			writeError(w, http.StatusUnauthorized, "not authenticated")
			return
		}
		ok, terr := s.deps.DB.TouchSession(r.Context(), c.Value, sessionTTL)
		if terr != nil {
			// A database failure is not a logout. Saying 401 here would send
			// the operator to a login screen that cannot work either.
			writeError(w, http.StatusInternalServerError, "session lookup failed")
			return
		}
		if !ok {
			writeError(w, http.StatusUnauthorized, "not authenticated")
			return
		}
		h(w, r.WithContext(context.WithValue(r.Context(), sessionKey, c.Value)))
	}
}

// requireCSRF wraps requireSession and adds the two checks a state-changing
// request needs. It wraps rather than sits beside it so a mutating route cannot
// get one check without the other.
func (s *Server) requireCSRF(h http.HandlerFunc) http.HandlerFunc {
	return s.requireSession(func(w http.ResponseWriter, r *http.Request) {
		if !sameOrigin(r) {
			writeError(w, http.StatusForbidden, "cross-site request refused")
			return
		}
		if !s.csrf.Valid(sessionFrom(r.Context()), r.Header.Get(csrfHeader)) {
			writeError(w, http.StatusForbidden, "invalid csrf token")
			return
		}
		h(w, r)
	})
}

// sameOrigin reports whether a state-changing request came from the SPA.
//
// Sec-Fetch-Site is checked first because it is unforgeable by page script and
// present in every browser that matters. Origin is the fallback for a client
// that does not send it. A request presenting neither is refused: treating "no
// header at all" as same-origin is how the check becomes decorative, and the
// SPA always sends at least one.
func sameOrigin(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "none":
		// "none" is a user-initiated navigation — typing the URL, a bookmark —
		// which cannot be attacker-driven.
		return true
	case "same-site", "cross-site":
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	// Host, not scheme: the default homelab posture is plain HTTP, so requiring
	// https here would refuse every legitimate request.
	return u.Host == r.Host
}
```

- [ ] **Step 4: Write the auth and config handlers the tests exercise**

Create `internal/admin/authapi.go`:

```go
package admin

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
)

// newSessionID mints an opaque id. 32 bytes of randomness, base64url: the id is
// the bearer of the whole session, so it is not derived from anything and
// carries no structure worth guessing.
func newSessionID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	authed := false
	if c, err := r.Cookie(sessionCookie); err == nil && c.Value != "" {
		if ok, terr := s.deps.DB.TouchSession(r.Context(), c.Value, sessionTTL); terr == nil && ok {
			authed = true
		}
	}
	body := map[string]any{"authenticated": authed}
	if authed {
		// The SPA needs the token to make its first mutating call after a
		// reload, and re-issuing it here saves a second round trip.
		c, _ := r.Cookie(sessionCookie)
		body["csrf_token"] = s.csrf.Token(c.Value)
	}
	writeJSON(w, http.StatusOK, body)
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	// Login CSRF is minor but real, and the SPA is same-origin, so the check
	// costs nothing here either.
	if !sameOrigin(r) {
		writeError(w, http.StatusForbidden, "cross-site request refused")
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if !VerifyPassword(s.deps.PasswordHash, body.Password) {
		// One message for both a wrong password and an unconfigured hash: an
		// operator reading "no password is set" learns the port is open.
		writeError(w, http.StatusUnauthorized, "invalid password")
		return
	}

	// Spec §3: login rotates the session id, so a fixated id cannot survive an
	// authentication.
	if old, err := r.Cookie(sessionCookie); err == nil && old.Value != "" {
		_ = s.deps.DB.DeleteSession(r.Context(), old.Value)
	}
	id, err := newSessionID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not create a session")
		return
	}
	if err := s.deps.DB.CreateSession(r.Context(), id, sessionTTL); err != nil {
		writeError(w, http.StatusInternalServerError, "could not create a session")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    id,
		Path:     "/",
		HttpOnly: true,
		// Lax rather than Strict: phase 8's OAuth callback is a state-changing
		// GET reached by a cross-site top-level redirect, which Strict blocks.
		SameSite: http.SameSiteLaxMode,
		// Only over TLS. On the plain-HTTP LAN default this must stay false or
		// the browser drops the cookie and login silently never works.
		Secure:   r.TLS != nil,
		MaxAge:   int(sessionTTL.Seconds()),
	})
	writeJSON(w, http.StatusOK, map[string]any{
		"authenticated": true,
		"csrf_token":    s.csrf.Token(id),
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.DB.DeleteSession(r.Context(), sessionFrom(r.Context())); err != nil {
		writeError(w, http.StatusInternalServerError, "could not end the session")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: "", Path: "/",
		HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: -1,
	})
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": false})
}
```

Create `internal/admin/configapi.go`:

```go
package admin

import "net/http"

// handleConfig renders the parsed configuration read-only with its validation
// status. Spec §2 keeps editing out of the UI: aliases and policy live in
// darkrouter.yaml, which is the structural reason the API stays small.
func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	if s.deps.Config == nil {
		writeJSON(w, http.StatusOK, map[string]any{"valid": true, "warnings": []string{}})
		return
	}
	cfg := s.deps.Config.Current()
	// Read once: two calls could straddle a reload and report a valid config
	// with an error attached, or an invalid one with none.
	cfgErr := s.deps.Config.LastError()

	body := map[string]any{
		"valid":    cfgErr == nil,
		"warnings": append(append([]string{}, s.deps.Warnings...), cfg.Warnings...),
		"aliases":  cfg.Aliases,
		"policy":   cfg.Policy,
		"server":   cfg.Server,
	}
	if cfgErr != nil {
		// Stated alongside the error, because a config that failed validation
		// is not a config that stopped serving: the previous one is still live.
		body["error"] = cfgErr.Error()
		body["serving"] = "the previous configuration is still serving"
	}
	writeJSON(w, http.StatusOK, body)
}

func (s *Server) handleConfigReload(w http.ResponseWriter, r *http.Request) {
	if s.deps.Config == nil {
		writeError(w, http.StatusServiceUnavailable, "no configuration store")
		return
	}
	err := s.deps.Config.Reload()
	if err != nil {
		// 200 rather than 500: the reload was performed and its outcome is the
		// answer. A 500 would read as "the request failed", when what happened
		// is that the file is invalid and the old config is still serving.
		writeJSON(w, http.StatusOK, map[string]any{
			"valid": false, "error": err.Error(),
			"serving": "the previous configuration is still serving",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"valid": true})
}
```

**Check `config.Store`'s real reload method name before writing this** — it may
be `Reload`, `Load`, or driven only by the fsnotify watcher. If no manual reload
exists, add one that shares the watcher's code path rather than duplicating it,
and say so in the commit.

- [ ] **Step 5: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/admin/ -race -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'
```

Expected: PASS, every test in the package.

- [ ] **Step 6: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing.

- [ ] **Step 7: Commit**

```bash
git add internal/admin/admin.go internal/admin/auth.go internal/admin/authapi.go \
        internal/admin/configapi.go internal/admin/auth_test.go
git commit -m "feat(admin): authenticate sessions and reject csrf"
```

---

### Task 6: The proxy port never honors a cookie

**Files:**
- Test: `internal/server/server_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: nothing. One regression test on a property that is currently true by accident.

Spec §3's last paragraph, and it is the sharpest edge in this phase: **cookies
are not port-scoped.** A browser that logged into `http://box:18081` sends that
session cookie to `http://box:18080` too. If the proxy port ever learned to read
one, every logged-in operator's browser would become an authenticated proxy
client for any page they visited — and worse, so would any site that could make
their browser issue a request.

Today `internal/server`'s `authed` reads only the bearer token, so the property
holds. It holds *by accident*, because nobody wrote a cookie reader, and nothing
would catch someone adding one. That is exactly what a regression test is for,
and it is why this is its own task rather than a line inside another.

There is no production change here. If the test passes on the first run, that is
the expected result — keep it and say so in the commit.

- [ ] **Step 1: Write the test**

Add to `internal/server/server_test.go`:

```go
func TestTheProxyPortIgnoresASessionCookie(t *testing.T) {
	// Cookies are not port-scoped: a browser logged into the admin port sends
	// that cookie to the proxy port too. If the proxy ever honored one, every
	// logged-in operator's browser would be an authenticated proxy client for
	// any page they visited. Nothing reads cookies here today; this is what
	// keeps it that way.
	srv := serverWithProxyToken(t, "the-real-token")

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`))
	r.AddCookie(&http.Cookie{Name: "darkrouter_session", Value: "a-valid-looking-session"})
	srv.ProxyHandler().ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401; a cookie authenticated a proxy request", w.Code)
	}
}

func TestTheProxyPortStillAcceptsItsBearerToken(t *testing.T) {
	// The other half: refusing the cookie must not refuse the token.
	srv := serverWithProxyToken(t, "the-real-token")

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/v1/chat/completions",
		strings.NewReader(`{"model":"m","messages":[{"role":"user","content":"hi"}]}`))
	r.Header.Set("Authorization", "Bearer the-real-token")
	srv.ProxyHandler().ServeHTTP(w, r)

	if w.Code == http.StatusUnauthorized {
		t.Fatalf("status = %d; the bearer token was refused", w.Code)
	}
}
```

`serverWithProxyToken` may not exist. **Read `internal/server/server_test.go`
first** and use whatever helper it already has for building a server with a
`server.proxy_token` set; if there is none, write one following the file's
existing fixture style rather than inventing a new one.

The second test asserts "not 401" rather than "200" deliberately: with no real
upstream the request fails downstream, and pinning a success code would make
this test about routing rather than about authentication.

- [ ] **Step 2: Run the tests**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/server/ -run 'TestTheProxyPort' -race -count=1 -v
```

Expected: **PASS**, both. Nothing reads cookies on the proxy port today. A
failure here means something already does, and that is the bug to fix before
going further.

- [ ] **Step 3: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing.

- [ ] **Step 4: Commit**

```bash
git add internal/server/server_test.go
git commit -m "test(server): pin that the proxy ignores cookies"
```

---

### Task 7: Provider and credential mutations in the store

**Files:**
- Modify: `internal/store/adminstore.go`
- Test: `internal/store/adminstore_test.go`

**Interfaces:**
- Consumes: `crypto.Key`, the `providers`, `provider_keys` and `models` tables.
- Produces: `(*DB).CreateProvider(ctx, p ProviderRow) error`, `(*DB).UpdateProvider(ctx, id string, patch ProviderPatch) error`, `(*DB).DeleteProvider(ctx, id string) error`, `(*DB).DeleteCredential(ctx, providerID, keyID string) error`, `(*DB).ProviderRows(ctx) ([]ProviderRow, error)`. Tasks 8 and 9 call all five.

The store already has `AddCredential` and `Credentials`. It has no way to create,
update or delete a provider, and no way to delete a credential — every provider
row in existence got there through `ImportFromConfig`. Spec §6 makes provider and
credential CRUD the whole of the settings screen, so this is the layer it needs.

**Deleting a provider deletes its models and credentials in one transaction.**
A provider row without its credentials is a provider that cannot serve; a
credential without its provider is a decryptable secret nobody can account for.
Leaving either behind is worse than refusing the delete.

**`UpdateProvider` takes a patch of pointers, not a struct of values.** Spec §4
lists `PATCH` with enable, priority, base_url, region and project — a partial
update. A value struct cannot distinguish "set priority to 0" from "do not touch
priority", and priority 0 is a legal value that means "last resort".

- [ ] **Step 1: Write the failing test**

Add to `internal/store/adminstore_test.go`:

```go
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

func TestUpdatingAnUnknownProviderIsAnError(t *testing.T) {
	db := migrated(t)
	enabled := false
	if err := db.UpdateProvider(context.Background(), "nope",
		ProviderPatch{Enabled: &enabled}); err == nil {
		t.Error("patching a provider that does not exist succeeded")
	}
}

func TestDeleteRemovesCredentialsAndModelsTogether(t *testing.T) {
	// A provider row without its credentials cannot serve; a credential
	// without its provider is a decryptable secret nobody can account for.
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
		`INSERT INTO models (provider_id, model_id, state, first_seen, last_seen)
		 VALUES ('p1','m','live',1,1)`); err != nil {
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
```

**Read `internal/store/credential.go` and the 0001 and 0002 migrations first.**
`ProviderRow` may need `Region`, `Project`, `AuthStyle` or `ModelsURL` columns
this plan has not named, and `Credential`'s real field set decides what
`AddCredential` accepts. Match the schema rather than this sketch.

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/store/ -run 'Provider|Credential' -v
```

Expected: FAIL to build — `undefined: ProviderRow`, `db.CreateProvider undefined`.

- [ ] **Step 3: Write the implementation**

Append to `internal/store/adminstore.go`:

```go
// ProviderRow is a provider as the admin API sees it: the database row without
// its credentials, which are never returned together because no endpoint may
// reveal credential material.
type ProviderRow struct {
	ID       string
	Name     string
	Preset   string
	Kind     string
	BaseURL  string
	Priority int
	Enabled  bool
}

// ProviderPatch is a partial update. Every field is a pointer because a partial
// update has to distinguish "set this to its zero value" from "do not touch
// it", and priority 0 is a legal value meaning last resort.
type ProviderPatch struct {
	Name     *string
	BaseURL  *string
	Priority *int
	Enabled  *bool
}

func (d *DB) CreateProvider(ctx context.Context, p ProviderRow) error {
	enabled := 0
	if p.Enabled {
		enabled = 1
	}
	if _, err := d.Write.ExecContext(ctx,
		`INSERT INTO providers (id, name, preset, kind, base_url, priority, enabled, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Name, p.Preset, p.Kind, p.BaseURL, p.Priority, enabled,
		time.Now().UnixMilli()); err != nil {
		return fmt.Errorf("create provider %q: %w", p.ID, err)
	}
	return nil
}

// UpdateProvider applies a partial update, and reports an error when the id does
// not exist rather than succeeding silently. A PATCH against a deleted provider
// that returns 200 is how a settings screen shows an edit that never happened.
func (d *DB) UpdateProvider(ctx context.Context, id string, patch ProviderPatch) error {
	sets := make([]string, 0, 4)
	args := make([]any, 0, 5)
	if patch.Name != nil {
		sets, args = append(sets, "name = ?"), append(args, *patch.Name)
	}
	if patch.BaseURL != nil {
		sets, args = append(sets, "base_url = ?"), append(args, *patch.BaseURL)
	}
	if patch.Priority != nil {
		sets, args = append(sets, "priority = ?"), append(args, *patch.Priority)
	}
	if patch.Enabled != nil {
		v := 0
		if *patch.Enabled {
			v = 1
		}
		sets, args = append(sets, "enabled = ?"), append(args, v)
	}
	if len(sets) == 0 {
		// An empty patch is a client bug, not a no-op to absorb: it means the
		// UI sent a form it did not fill in.
		return fmt.Errorf("update provider %q: the patch names no fields", id)
	}
	args = append(args, id)
	res, err := d.Write.ExecContext(ctx,
		`UPDATE providers SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
	if err != nil {
		return fmt.Errorf("update provider %q: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("update provider %q: %w", id, err)
	}
	if n == 0 {
		return fmt.Errorf("update provider %q: no such provider", id)
	}
	return nil
}

// DeleteProvider removes the provider with its credentials and models in one
// transaction. A provider without its credentials cannot serve, and a credential
// without its provider is a decryptable secret nobody can account for — leaving
// either behind is worse than refusing the delete.
func (d *DB) DeleteProvider(ctx context.Context, id string) error {
	tx, err := d.Write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("delete provider %q: %w", id, err)
	}
	defer func() { _ = tx.Rollback() }()

	for _, q := range []string{
		`DELETE FROM provider_keys WHERE provider_id = ?`,
		`DELETE FROM models WHERE provider_id = ?`,
		`DELETE FROM providers WHERE id = ?`,
	} {
		if _, err := tx.ExecContext(ctx, q, id); err != nil {
			return fmt.Errorf("delete provider %q: %w", id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("delete provider %q: %w", id, err)
	}
	return nil
}

func (d *DB) DeleteCredential(ctx context.Context, providerID, keyID string) error {
	if _, err := d.Write.ExecContext(ctx,
		`DELETE FROM provider_keys WHERE provider_id = ? AND id = ?`,
		providerID, keyID); err != nil {
		return fmt.Errorf("delete credential %q: %w", keyID, err)
	}
	return nil
}

// ProviderRows lists every provider, ordered by priority descending then id so
// two calls return the same order and the settings screen does not reshuffle
// between polls.
func (d *DB) ProviderRows(ctx context.Context) ([]ProviderRow, error) {
	rows, err := d.Read.QueryContext(ctx,
		`SELECT id, name, preset, kind, base_url, priority, enabled
		   FROM providers ORDER BY priority DESC, id`)
	if err != nil {
		return nil, fmt.Errorf("list providers: %w", err)
	}
	defer rows.Close()

	var out []ProviderRow
	for rows.Next() {
		var p ProviderRow
		var enabled int
		if err := rows.Scan(&p.ID, &p.Name, &p.Preset, &p.Kind,
			&p.BaseURL, &p.Priority, &enabled); err != nil {
			return nil, fmt.Errorf("list providers: %w", err)
		}
		p.Enabled = enabled != 0
		out = append(out, p)
	}
	return out, rows.Err()
}
```

Add `"strings"` and `"time"` to the file's imports.

- [ ] **Step 4: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/store/ -race -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'
```

Expected: PASS, every test in the package.

- [ ] **Step 5: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing.

- [ ] **Step 6: Commit**

```bash
git add internal/store/adminstore.go internal/store/adminstore_test.go
git commit -m "feat(store): add provider and credential mutations"
```

---

### Task 8: Provider and credential endpoints

**Files:**
- Create: `internal/admin/providers.go`
- Modify: `internal/admin/admin.go` (`routes`)
- Test: `internal/admin/providers_test.go`

**Interfaces:**
- Consumes: the Task 7 store methods, `provider.SQLSource.Reload`, `health.Breaker`, `catalog.Presets`.
- Produces: `GET/POST /api/providers`, `PATCH/DELETE /api/providers/:id`, `POST/DELETE /api/providers/:id/keys[/:keyId]`, `GET /api/presets`. Task 9 pins the credential-material rule across all of them; Part B's settings screen consumes them.

Six endpoints in one task because they share a shape, a fixture and a failure
mode: every one of them mutates the provider set, and every one must call
`SQLSource.Reload` afterwards or the running router keeps serving the old set
until something else happens to reload it. Splitting them would mean writing
that reasoning six times and risking five of them getting it right.

**A masked credential is built from the label and a suffix, and the suffix comes
from the plaintext.** That means the handler decrypts to mask. It must not
return what it decrypted, which is what Task 9 exists to prove — but there is no
way to show "sk-…f4a2" without briefly holding "sk-abcdef4a2". Mask at the point
of decryption and never let the plaintext reach a struct that gets marshalled.

**Deleting a provider lists the aliases it strands.** Master design §7 makes a
dangling alias a warning rather than a validation error, and spec §6 explains
why that matters here: treating it as an error would mean one UI delete makes
every subsequent config reload fail, leaving the operator with a reload button
that keeps failing and no way out but SSH. So the delete succeeds and reports
what it stranded.

- [ ] **Step 1: Write the failing test**

Create `internal/admin/providers_test.go`:

```go
package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// do performs an authenticated request against the admin mux, adding the CSRF
// header on mutating verbs so a test does not repeat six lines of setup.
func do(t *testing.T, s *Server, cookie *http.Cookie, token, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var rdr *strings.Reader
	if body == "" {
		rdr = strings.NewReader("")
	} else {
		rdr = strings.NewReader(body)
	}
	r := httptest.NewRequest(method, path, rdr)
	r.AddCookie(cookie)
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	if method != "GET" {
		r.Header.Set("X-CSRF-Token", token)
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	return w
}

func TestAProviderCanBeCreatedListedAndDeleted(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)

	w := do(t, s, cookie, token, "POST", "/api/providers",
		`{"id":"p1","name":"P One","kind":"openaicompat",
		  "base_url":"https://x/v1","priority":7,"enabled":true}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create status = %d, body = %s", w.Code, w.Body.String())
	}

	w = do(t, s, cookie, token, "GET", "/api/providers", "")
	if w.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", w.Code, w.Body.String())
	}
	var list struct {
		Providers []struct {
			ID       string `json:"id"`
			Priority int    `json:"priority"`
			Enabled  bool   `json:"enabled"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Providers) != 1 || list.Providers[0].ID != "p1" {
		t.Fatalf("providers = %+v", list.Providers)
	}

	w = do(t, s, cookie, token, "DELETE", "/api/providers/p1", "")
	if w.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body = %s", w.Code, w.Body.String())
	}
}

func TestCreatingAProviderWithADuplicateIDIsAConflict(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	body := `{"id":"p1","name":"P","kind":"openaicompat","base_url":"https://x/v1"}`

	if w := do(t, s, cookie, token, "POST", "/api/providers", body); w.Code != http.StatusCreated {
		t.Fatalf("first create = %d", w.Code)
	}
	w := do(t, s, cookie, token, "POST", "/api/providers", body)
	if w.Code != http.StatusConflict {
		t.Errorf("duplicate create = %d, want 409", w.Code)
	}
}

func TestCreatingAProviderFromAPresetFillsTheKindAndBaseURL(t *testing.T) {
	// Spec §4: create from preset OR raw kind+base_url. From a preset the
	// operator supplies an id and a key and nothing else, which is the whole
	// point of shipping presets.
	s, _ := testServerFull(t)
	cookie, token := login(t, s)

	w := do(t, s, cookie, token, "POST", "/api/providers",
		`{"id":"groq","preset":"groq"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	w = do(t, s, cookie, token, "GET", "/api/providers", "")
	var list struct {
		Providers []struct {
			Kind    string `json:"kind"`
			BaseURL string `json:"base_url"`
			Preset  string `json:"preset"`
		} `json:"providers"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if len(list.Providers) != 1 {
		t.Fatalf("providers = %+v", list.Providers)
	}
	if list.Providers[0].Kind == "" || list.Providers[0].BaseURL == "" {
		t.Errorf("preset did not fill the row: %+v", list.Providers[0])
	}
	if list.Providers[0].Preset != "groq" {
		t.Errorf("preset = %q; the name must be recorded or nothing joins the catalog",
			list.Providers[0].Preset)
	}
}

func TestCreatingAProviderFromAnUnknownPresetIsRejected(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "POST", "/api/providers",
		`{"id":"p1","preset":"not-a-real-preset"}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestPatchingAProviderChangesOnlyWhatItNames(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	_ = do(t, s, cookie, token, "POST", "/api/providers",
		`{"id":"p1","name":"P","kind":"openaicompat","base_url":"https://x/v1",
		  "priority":7,"enabled":true}`)

	w := do(t, s, cookie, token, "PATCH", "/api/providers/p1", `{"enabled":false}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	w = do(t, s, cookie, token, "GET", "/api/providers", "")
	var list struct {
		Providers []struct {
			Priority int  `json:"priority"`
			Enabled  bool `json:"enabled"`
		} `json:"providers"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if list.Providers[0].Enabled {
		t.Error("enabled did not change")
	}
	if list.Providers[0].Priority != 7 {
		t.Errorf("priority = %d; an unnamed field changed", list.Providers[0].Priority)
	}
}

func TestPatchingAnUnknownProviderIsANotFound(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "PATCH", "/api/providers/nope", `{"enabled":false}`)
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}

func TestACredentialCanBeAddedAndDeleted(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	_ = do(t, s, cookie, token, "POST", "/api/providers",
		`{"id":"p1","name":"P","kind":"openaicompat","base_url":"https://x/v1"}`)

	w := do(t, s, cookie, token, "POST", "/api/providers/p1/keys",
		`{"label":"primary","secret":"sk-abcdef123456"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var created struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	if created.ID == "" {
		t.Fatal("the created credential has no id")
	}

	w = do(t, s, cookie, token, "GET", "/api/providers", "")
	var list struct {
		Providers []struct {
			Credentials []struct {
				ID     string `json:"id"`
				Label  string `json:"label"`
				Masked string `json:"masked"`
			} `json:"credentials"`
		} `json:"providers"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	creds := list.Providers[0].Credentials
	if len(creds) != 1 || creds[0].Label != "primary" {
		t.Fatalf("credentials = %+v", creds)
	}
	if !strings.HasSuffix(creds[0].Masked, "3456") || strings.Contains(creds[0].Masked, "abcdef") {
		t.Errorf("masked = %q; it must show a suffix and hide the rest", creds[0].Masked)
	}

	w = do(t, s, cookie, token, "DELETE", "/api/providers/p1/keys/"+created.ID, "")
	if w.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body = %s", w.Code, w.Body.String())
	}
}

func TestDeletingAProviderReportsTheAliasesItStrands(t *testing.T) {
	// Master design §7: a dangling alias is a warning, not a validation error.
	// Spec §6: treating it as an error would mean one UI delete makes every
	// later config reload fail, leaving the operator with a reload button that
	// keeps failing and no way out but SSH.
	s, _ := testServerFullWithAlias(t, "fast", "p1/m")
	cookie, token := login(t, s)
	_ = do(t, s, cookie, token, "POST", "/api/providers",
		`{"id":"p1","name":"P","kind":"openaicompat","base_url":"https://x/v1"}`)

	w := do(t, s, cookie, token, "DELETE", "/api/providers/p1", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var body struct {
		DanglingAliases []string `json:"dangling_aliases"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.DanglingAliases) != 1 || body.DanglingAliases[0] != "fast" {
		t.Errorf("dangling = %v; the operator is not told what broke", body.DanglingAliases)
	}
}

func TestPresetsAreListedForTheCreateForm(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "GET", "/api/presets", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var body struct {
		Presets []struct {
			ID       string   `json:"id"`
			Name     string   `json:"name"`
			Kind     string   `json:"kind"`
			BaseURL  string   `json:"base_url"`
			Surfaces []string `json:"surfaces"`
		} `json:"presets"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Presets) < 100 {
		t.Fatalf("got %d presets; the shipped catalog holds ~197", len(body.Presets))
	}
	var groq bool
	for _, p := range body.Presets {
		if p.ID == "groq" {
			groq = true
			if p.Kind == "" || p.BaseURL == "" || len(p.Surfaces) == 0 {
				t.Errorf("groq preset is incomplete: %+v", p)
			}
		}
	}
	if !groq {
		t.Error("groq is not in the preset list")
	}
}
```

Add two fixtures beside `testServer` in `auth_test.go`:

- `testServerFull(t)` — `testServer` plus a `crypto.Key` from `store.OpenKeyring`,
  `catalog.Embedded()` as `Presets`, a `provider.NewSQLSource` over the same
  database, a `health.New(3, time.Minute)` breaker, and a `config.Store` over a
  minimal temp file. Every provider endpoint needs the key and the source.
- `testServerFullWithAlias(t, name, target)` — the same with one alias written
  into the config file, so the dangling-alias test has something to strand.

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/admin/ -run 'Provider|Credential|Preset' -v
```

Expected: FAIL — the routes are not registered, so every request 404s.

- [ ] **Step 3: Write the handlers**

Create `internal/admin/providers.go`:

```go
package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/darkraise/darkrouter/internal/store"
)

// maskSecret renders a credential for display. It shows the last four characters
// and nothing else.
//
// A short secret is masked entirely rather than partially: showing three of four
// characters of a four-character key is not a mask. The suffix exists so an
// operator can tell two keys apart, which four characters achieves and eight
// would not improve.
func maskSecret(secret string) string {
	if len(secret) <= 4 {
		return "****"
	}
	return "…" + secret[len(secret)-4:]
}

type credentialView struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Masked   string `json:"masked"`
	Enabled  bool   `json:"enabled"`
	Cooling  bool   `json:"cooling"`
	LastUsed int64  `json:"last_used_ms,omitempty"`
}

type providerView struct {
	ID          string           `json:"id"`
	Name        string           `json:"name"`
	Preset      string           `json:"preset"`
	Kind        string           `json:"kind"`
	BaseURL     string           `json:"base_url"`
	Priority    int              `json:"priority"`
	Enabled     bool             `json:"enabled"`
	Credentials []credentialView `json:"credentials"`
}

func (s *Server) handleListProviders(w http.ResponseWriter, r *http.Request) {
	rows, err := s.deps.DB.ProviderRows(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]providerView, 0, len(rows))
	for _, p := range rows {
		v := providerView{
			ID: p.ID, Name: p.Name, Preset: p.Preset, Kind: p.Kind,
			BaseURL: p.BaseURL, Priority: p.Priority, Enabled: p.Enabled,
			Credentials: []credentialView{},
		}
		if s.deps.Key != nil {
			creds, cerr := s.deps.DB.Credentials(r.Context(), s.deps.Key, p.ID)
			if cerr != nil {
				writeError(w, http.StatusInternalServerError, cerr.Error())
				return
			}
			for _, c := range creds {
				// Masked at the point of decryption. The plaintext never
				// reaches a struct that gets marshalled, which is the only
				// way "never returns credential material" survives a refactor.
				v.Credentials = append(v.Credentials, credentialView{
					ID: c.ID, Label: c.Label, Masked: maskSecret(c.Secret),
					Enabled: c.Enabled, Cooling: s.cooling(p.ID, c.ID),
				})
			}
		}
		out = append(out, v)
	}
	writeJSON(w, http.StatusOK, map[string]any{"providers": out})
}

// cooling reports whether the breaker is holding this credential down. It is
// per-credential rather than per-triple because the settings screen shows one
// row per credential and "some of its models are cooling" is not a state a
// checkbox can render.
func (s *Server) cooling(providerID, keyID string) bool {
	if s.deps.Breaker == nil {
		return false
	}
	return !s.deps.Breaker.Available(healthKey(providerID, keyID, ""))
}

type createProviderBody struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Preset   string `json:"preset"`
	Kind     string `json:"kind"`
	BaseURL  string `json:"base_url"`
	Priority int    `json:"priority"`
	Enabled  *bool  `json:"enabled"`
}

func (s *Server) handleCreateProvider(w http.ResponseWriter, r *http.Request) {
	var body createProviderBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.ID == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}

	row := store.ProviderRow{
		ID: body.ID, Name: body.Name, Preset: body.Preset,
		Kind: body.Kind, BaseURL: body.BaseURL, Priority: body.Priority,
		Enabled: body.Enabled == nil || *body.Enabled,
	}
	// From a preset the operator supplies an id and a key and nothing else,
	// which is the whole reason presets ship. Explicit values still win, so a
	// preset can be used as a starting point.
	if body.Preset != "" {
		p, ok := s.deps.Presets[body.Preset]
		if !ok {
			writeError(w, http.StatusBadRequest,
				fmt.Sprintf("preset %q is not a shipped preset", body.Preset))
			return
		}
		if row.Kind == "" {
			row.Kind = p.Kind
		}
		if row.BaseURL == "" {
			row.BaseURL = p.BaseURL
		}
		if row.Name == "" {
			row.Name = p.Name
		}
	}
	if row.Kind == "" || row.BaseURL == "" {
		writeError(w, http.StatusBadRequest,
			"kind and base_url are required unless a preset supplies them")
		return
	}
	if row.Name == "" {
		row.Name = row.ID
	}

	if err := s.deps.DB.CreateProvider(r.Context(), row); err != nil {
		if strings.Contains(err.Error(), "UNIQUE") || strings.Contains(err.Error(), "PRIMARY KEY") {
			writeError(w, http.StatusConflict, fmt.Sprintf("provider %q already exists", row.ID))
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.reloadProviders(r)
	writeJSON(w, http.StatusCreated, map[string]any{"id": row.ID})
}

func (s *Server) handlePatchProvider(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var patch store.ProviderPatch
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&struct {
		Name     **string `json:"-"`
		BaseURL  **string `json:"-"`
		Priority **int    `json:"-"`
		Enabled  **bool   `json:"-"`
	}{}); err != nil {
		_ = err // replaced below; the anonymous struct exists only to document intent
	}
	// Decoded into the patch directly: the pointer fields are what carry
	// "named" versus "absent", and json.Unmarshal leaves an absent field nil.
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&patch); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if err := s.deps.DB.UpdateProvider(r.Context(), id, patch); err != nil {
		if strings.Contains(err.Error(), "no such provider") {
			writeError(w, http.StatusNotFound, err.Error())
			return
		}
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.reloadProviders(r)
	writeJSON(w, http.StatusOK, map[string]any{"id": id})
}

func (s *Server) handleDeleteProvider(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// Computed before the delete, because afterwards there is no provider to
	// match aliases against.
	dangling := s.danglingAliases(id)

	if err := s.deps.DB.DeleteProvider(r.Context(), id); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.reloadProviders(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"id": id, "dangling_aliases": dangling,
	})
}

// danglingAliases names the aliases that will point at nothing once providerID
// is gone.
//
// Master design §7 makes a dangling alias a warning rather than a validation
// error, and this is why: if it were an error, one UI delete would make every
// subsequent config reload fail, leaving the operator with a reload button that
// keeps failing and no way out but SSH. The delete succeeds and reports.
func (s *Server) danglingAliases(providerID string) []string {
	if s.deps.Config == nil {
		return []string{}
	}
	var out []string
	for name, chain := range s.deps.Config.Current().Aliases {
		for _, target := range chain {
			p, _, found := strings.Cut(target, "/")
			if found && p == providerID {
				out = append(out, name)
				break
			}
		}
	}
	// Sorted so two deletes of the same shape produce the same dialog text.
	sort.Strings(out)
	if out == nil {
		return []string{}
	}
	return out
}

// reloadProviders pushes the mutation into the running router. Without it the
// change is in the database and the gateway keeps serving the old provider set
// until something else happens to reload.
func (s *Server) reloadProviders(r *http.Request) {
	if s.deps.Src == nil {
		return
	}
	// A reload failure is not reported to the caller: the mutation succeeded
	// and the database is the source of truth. The next natural reload picks it
	// up, and reporting a 500 for a write that landed would be worse.
	_ = s.deps.Src.Reload(r.Context())
}

type addCredentialBody struct {
	Label  string `json:"label"`
	Secret string `json:"secret"`
}

func (s *Server) handleAddCredential(w http.ResponseWriter, r *http.Request) {
	if s.deps.Key == nil {
		writeError(w, http.StatusServiceUnavailable, "no keyring")
		return
	}
	var body addCredentialBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.Secret == "" {
		writeError(w, http.StatusBadRequest, "secret is required")
		return
	}
	if body.Label == "" {
		body.Label = "default"
	}
	id, err := s.deps.DB.AddCredential(r.Context(), s.deps.Key, store.Credential{
		ProviderID: r.PathValue("id"), Label: body.Label,
		Secret: body.Secret, Enabled: true,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.reloadProviders(r)
	// The id and the label, never the secret — not even the one just supplied.
	// Echoing it back would put it in a response body, a proxy log and a
	// browser's network panel for no reason.
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "label": body.Label})
}

func (s *Server) handleDeleteCredential(w http.ResponseWriter, r *http.Request) {
	if err := s.deps.DB.DeleteCredential(r.Context(),
		r.PathValue("id"), r.PathValue("keyId")); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.reloadProviders(r)
	writeJSON(w, http.StatusOK, map[string]any{"id": r.PathValue("keyId")})
}

var errNoPresets = errors.New("no preset catalog")

func (s *Server) handlePresets(w http.ResponseWriter, r *http.Request) {
	type presetView struct {
		ID       string   `json:"id"`
		Name     string   `json:"name"`
		Kind     string   `json:"kind"`
		BaseURL  string   `json:"base_url"`
		Surfaces []string `json:"surfaces"`
		AuthKind string   `json:"auth_kind"`
		Website  string   `json:"website"`
	}
	out := make([]presetView, 0, len(s.deps.Presets))
	for id, p := range s.deps.Presets {
		out = append(out, presetView{
			ID: id, Name: p.Name, Kind: p.Kind, BaseURL: p.BaseURL,
			Surfaces: p.Surfaces, AuthKind: p.Auth.Style, Website: p.Website,
		})
	}
	// Sorted by id: a map iteration order would reshuffle the create form's
	// dropdown on every poll.
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	writeJSON(w, http.StatusOK, map[string]any{"presets": out})
}
```

**Two things to fix while writing this rather than after.** The `handlePatchProvider`
body above reads `r.Body` twice, which cannot work — the first decode drains it.
Write it as a single decode into `store.ProviderPatch`; the anonymous struct in
the sketch is documentation of intent, not code to keep. And `healthKey` does not
exist yet — add it beside `cooling`:

```go
// healthKey builds the breaker key. It is a helper rather than a literal because
// the empty Model is load-bearing: a credential-level cooldown is stored under a
// key with no model, and a triple cooldown under one with a model. Getting that
// backwards makes a cooling credential look available.
func healthKey(providerID, keyID, model string) health.Key {
	return health.Key{ProviderID: providerID, KeyID: keyID, Model: model}
}
```

Register the routes in `admin.go`'s `routes`:

```go
	s.mux.HandleFunc("GET /api/presets", s.requireSession(s.handlePresets))
	s.mux.HandleFunc("GET /api/providers", s.requireSession(s.handleListProviders))
	s.mux.HandleFunc("POST /api/providers", s.requireCSRF(s.handleCreateProvider))
	s.mux.HandleFunc("PATCH /api/providers/{id}", s.requireCSRF(s.handlePatchProvider))
	s.mux.HandleFunc("DELETE /api/providers/{id}", s.requireCSRF(s.handleDeleteProvider))
	s.mux.HandleFunc("POST /api/providers/{id}/keys", s.requireCSRF(s.handleAddCredential))
	s.mux.HandleFunc("DELETE /api/providers/{id}/keys/{keyId}", s.requireCSRF(s.handleDeleteCredential))
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/admin/ -race -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'
```

Expected: PASS, every test in the package.

- [ ] **Step 5: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing.

- [ ] **Step 6: Commit**

```bash
git add internal/admin/providers.go internal/admin/admin.go \
        internal/admin/providers_test.go internal/admin/auth_test.go
git commit -m "feat(admin): manage providers and credentials"
```

---

### Task 9: No endpoint returns credential material

**Files:**
- Test: `internal/admin/leak_test.go`

**Interfaces:**
- Consumes: every endpoint registered so far.
- Produces: nothing. One test that walks the whole API.

Spec §4.1 and §7 both name this, and it is written as one sweep across every
endpoint rather than an assertion inside each handler's test for a specific
reason: **a per-handler assertion protects the handler it was written for, and a
sweep protects the handler nobody has written yet.** The failure this guards
against is not "someone returns the secret on purpose" — it is a future endpoint
that marshals a `store.Credential` because that was the struct in hand.

The test seeds a secret with a distinctive shape and then asserts no response
body anywhere contains it, in plaintext or base64. Ciphertext is covered too:
the encrypted column is checked by asserting the response contains no long
base64 run that decodes to the secret's length, which is what a leaked
ciphertext would look like.

- [ ] **Step 1: Write the test**

Create `internal/admin/leak_test.go`:

```go
package admin

import (
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
)

// theSecret is deliberately shaped so a substring match cannot produce a false
// positive against ordinary JSON.
const theSecret = "sk-DARKROUTERLEAKCANARY-9f3a7c1e"

func TestNoEndpointReturnsCredentialMaterial(t *testing.T) {
	// Spec §4.1: never plaintext, never ciphertext, not for editing, not for
	// export. Written as one sweep rather than per handler because a
	// per-handler assertion protects the handler it was written for, and this
	// protects the one nobody has written yet.
	s, _ := testServerFull(t)
	cookie, token := login(t, s)

	if w := do(t, s, cookie, token, "POST", "/api/providers",
		`{"id":"p1","name":"P","kind":"openaicompat","base_url":"https://x/v1"}`); w.Code != http.StatusCreated {
		t.Fatalf("create provider = %d, body = %s", w.Code, w.Body.String())
	}
	if w := do(t, s, cookie, token, "POST", "/api/providers/p1/keys",
		`{"label":"primary","secret":"`+theSecret+`"}`); w.Code != http.StatusCreated {
		t.Fatalf("add credential = %d, body = %s", w.Code, w.Body.String())
	}

	b64 := base64.StdEncoding.EncodeToString([]byte(theSecret))
	for _, ep := range []struct{ method, path, body string }{
		{"GET", "/api/providers", ""},
		{"GET", "/api/presets", ""},
		{"GET", "/api/overview", ""},
		{"GET", "/api/models", ""},
		{"GET", "/api/requests", ""},
		{"GET", "/api/usage", ""},
		{"GET", "/api/config", ""},
		{"GET", "/api/auth/status", ""},
		{"POST", "/api/config/reload", ""},
	} {
		w := do(t, s, cookie, token, ep.method, ep.path, ep.body)
		got := w.Body.String()
		if strings.Contains(got, theSecret) {
			t.Errorf("%s %s returned the secret in plaintext:\n%s", ep.method, ep.path, got)
		}
		if strings.Contains(got, b64) {
			t.Errorf("%s %s returned the secret base64-encoded:\n%s", ep.method, ep.path, got)
		}
		// The masked form is the only thing that may carry any of it, and only
		// the last four characters.
		if strings.Contains(got, "DARKROUTERLEAKCANARY") {
			t.Errorf("%s %s returned more than the masked suffix:\n%s", ep.method, ep.path, got)
		}
	}
}

func TestTheMaskShowsOnlyASuffix(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	_ = do(t, s, cookie, token, "POST", "/api/providers",
		`{"id":"p1","name":"P","kind":"openaicompat","base_url":"https://x/v1"}`)
	_ = do(t, s, cookie, token, "POST", "/api/providers/p1/keys",
		`{"label":"primary","secret":"`+theSecret+`"}`)

	got := do(t, s, cookie, token, "GET", "/api/providers", "").Body.String()
	if !strings.Contains(got, theSecret[len(theSecret)-4:]) {
		t.Errorf("the masked suffix is missing; two keys cannot be told apart:\n%s", got)
	}
}

func TestAShortSecretIsMaskedEntirely(t *testing.T) {
	// Showing three of four characters is not a mask.
	if got := maskSecret("abcd"); strings.Contains(got, "abc") {
		t.Errorf("maskSecret(%q) = %q", "abcd", got)
	}
	if got := maskSecret(""); got != "****" {
		t.Errorf("maskSecret(\"\") = %q", got)
	}
}
```

- [ ] **Step 2: Run the tests**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/admin/ -run 'Leak|NoEndpoint|TheMask|ShortSecret' -race -count=1 -v
```

Expected: PASS. Task 8 masks at the point of decryption, so nothing should leak.
**A failure here is not a test to adjust** — it is a handler returning credential
material, and the handler is what changes.

Endpoints listed above that do not exist yet return 404, whose body contains no
secret and therefore passes trivially. That is fine and deliberate: the list is
written once, in full, so that Tasks 10 through 18 are covered the moment they
land rather than needing this file edited each time.

- [ ] **Step 3: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing.

- [ ] **Step 4: Commit**

```bash
git add internal/admin/leak_test.go
git commit -m "test(admin): sweep every endpoint for credential leaks"
```

---

### Task 10: The credential probe

**Files:**
- Create: `internal/admin/probe.go`
- Modify: `internal/admin/admin.go` (`routes`)
- Test: `internal/admin/probe_test.go`

**Interfaces:**
- Consumes: `catalog.ProbeFor` and the discovery machinery from phase 6, `health.Breaker.Record`, `store.Credentials`.
- Produces: `POST /api/providers/:id/test`. Part B's settings screen calls it.

**Evaluation:** files 1 - spec 1 - coupling 2 - risk 2 = 6

Spec §4.3, and it has more stated behavior than any other endpoint in the phase
because each piece exists to prevent a specific confusion.

**It deliberately bypasses the circuit breaker.** Its most common purpose is
checking whether a cooling provider has recovered, and a probe that refused to
run because the provider is cooling would answer a question nobody asked. It is
admin-authenticated, CSRF-protected, manual and single-operator, so the abuse
surface is nil.

**A successful probe resets the ladder** — otherwise the operator reads "probe
OK" beside "still cooling", which is exactly the confusion the probe exists to
remove. `health.Breaker.Record` already does this on `OutcomeSuccess`: it deletes
the entry outright. But it must be recorded against **two** keys, and this is the
subtlety that will otherwise be got wrong: a credential-level cooldown lives
under `Key{ProviderID, KeyID}` with an **empty** `Model`, while a triple cooldown
lives under `Key{ProviderID, KeyID, Model}`. Recording success against only one
leaves the other cooling.

**A per-provider mutex prevents a double-click from issuing two probes.** The
second caller waits and gets the first's answer rather than a second upstream
call, because two probes racing on one credential can produce two different
answers and the operator cannot tell which is current.

**The one-token-completion fallback spends real money** on a provider with no
listing endpoint. Spec §4.3 accepts that for a manual operator action; the
response says which kind of probe ran so the operator knows.

- [ ] **Step 1: Write the failing test**

Create `internal/admin/probe_test.go`:

```go
package admin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/health"
)

func TestASuccessfulProbeReportsWhatItFound(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"m1"},{"id":"m2"}]}`))
	}))
	defer upstream.Close()

	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	seedProviderWithKey(t, s, cookie, token, "p1", upstream.URL)

	w := do(t, s, cookie, token, "POST", "/api/providers/p1/test", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var body struct {
		OK         bool   `json:"ok"`
		Kind       string `json:"probe"`
		ModelCount int    `json:"model_count"`
		LatencyMs  int64  `json:"latency_ms"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.OK {
		t.Errorf("ok = false: %s", w.Body.String())
	}
	if body.ModelCount != 2 {
		t.Errorf("model_count = %d, want 2", body.ModelCount)
	}
	if body.Kind != "listing" {
		t.Errorf("probe = %q; the operator must know which kind ran", body.Kind)
	}
	if body.LatencyMs < 0 {
		t.Errorf("latency = %d", body.LatencyMs)
	}
}

func TestAProbeAgainstABadCredentialReportsFailureNot500(t *testing.T) {
	// A rejected key is an answer, not a server error. Returning 500 would make
	// the settings screen show "something broke" for the one outcome the button
	// exists to discover.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer upstream.Close()

	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	seedProviderWithKey(t, s, cookie, token, "p1", upstream.URL)

	w := do(t, s, cookie, token, "POST", "/api/providers/p1/test", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var body struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if body.OK {
		t.Error("ok = true against a 401")
	}
	if body.Error == "" {
		t.Error("no error message; the operator learns nothing")
	}
}

func TestASuccessfulProbeClearsBothCooldownLevels(t *testing.T) {
	// The whole reason the probe exists. A credential-level cooldown lives
	// under a key with an EMPTY model; a triple cooldown under one with a
	// model. Clearing only one leaves the operator reading "probe OK" beside
	// "still cooling".
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"m1"}]}`))
	}))
	defer upstream.Close()

	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	keyID := seedProviderWithKey(t, s, cookie, token, "p1", upstream.URL)

	br := s.deps.Breaker
	credKey := health.Key{ProviderID: "p1", KeyID: keyID}
	tripleKey := health.Key{ProviderID: "p1", KeyID: keyID, Model: "m1"}
	for i := 0; i < 5; i++ {
		br.Record(credKey, health.Signal{Outcome: adapter.OutcomeRetryableCredential})
		br.Record(tripleKey, health.Signal{Outcome: adapter.OutcomeRetryableProvider, StatusCode: 500})
	}
	if br.Available(credKey) || br.Available(tripleKey) {
		t.Fatal("the fixture did not cool anything")
	}

	if w := do(t, s, cookie, token, "POST", "/api/providers/p1/test", ""); w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if !br.Available(credKey) {
		t.Error("the credential is still cooling after a successful probe")
	}
	if !br.Available(tripleKey) {
		t.Error("the triple is still cooling after a successful probe")
	}
}

func TestAFailedProbeDoesNotClearACooldown(t *testing.T) {
	// The reset is on success only. A failing probe that cleared the ladder
	// would let a dead provider back into rotation on every click.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer upstream.Close()

	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	keyID := seedProviderWithKey(t, s, cookie, token, "p1", upstream.URL)

	br := s.deps.Breaker
	credKey := health.Key{ProviderID: "p1", KeyID: keyID}
	for i := 0; i < 5; i++ {
		br.Record(credKey, health.Signal{Outcome: adapter.OutcomeRetryableCredential})
	}
	_ = do(t, s, cookie, token, "POST", "/api/providers/p1/test", "")
	if br.Available(credKey) {
		t.Error("a failed probe cleared the cooldown")
	}
}

func TestADoubleClickIssuesOneProbe(t *testing.T) {
	// Spec §4.3. Two probes racing on one credential produce two answers and
	// the operator cannot tell which is current.
	var hits atomic.Int64
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		<-release
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"m1"}]}`))
	}))
	defer upstream.Close()

	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	seedProviderWithKey(t, s, cookie, token, "p1", upstream.URL)

	var wg sync.WaitGroup
	codes := make([]int, 2)
	for i := range codes {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i] = do(t, s, cookie, token, "POST", "/api/providers/p1/test", "").Code
		}(i)
	}
	// Let the first probe reach the upstream and the second reach the mutex.
	time.Sleep(200 * time.Millisecond)
	close(release)
	wg.Wait()

	if got := hits.Load(); got != 1 {
		t.Errorf("upstream called %d times; a double-click issued two probes", got)
	}
	for i, c := range codes {
		if c != http.StatusOK {
			t.Errorf("probe %d status = %d", i, c)
		}
	}
}

func TestProbingAProviderWithNoCredentialIsARefusal(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	_ = do(t, s, cookie, token, "POST", "/api/providers",
		`{"id":"p1","name":"P","kind":"openaicompat","base_url":"https://x/v1"}`)

	w := do(t, s, cookie, token, "POST", "/api/providers/p1/test", "")
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}
```

Add `seedProviderWithKey(t, s, cookie, token, id, baseURL) string` beside the
other fixtures: it creates the provider and one credential and returns the
credential id, which the cooldown tests need to build breaker keys.

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/admin/ -run Probe -v
```

Expected: FAIL — the route is not registered, so every probe 404s.

- [ ] **Step 3: Write the probe**

Create `internal/admin/probe.go`:

```go
package admin

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/catalog"
	"github.com/darkraise/darkrouter/internal/health"
	"github.com/darkraise/darkrouter/internal/store"
)

// probeTimeout bounds one probe. It is generous — a cold provider on a slow link
// is the case the button exists for — but finite, because a hung probe holds the
// per-provider mutex and the operator's click looks ignored.
const probeTimeout = 30 * time.Second

// probeLocks serializes probes per provider. Spec §4.3: a double-click must
// issue one probe, because two racing on the same credential produce two
// answers and the operator cannot tell which is current.
type probeLocks struct {
	mu sync.Mutex
	m  map[string]*sync.Mutex
}

func (l *probeLocks) get(providerID string) *sync.Mutex {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.m == nil {
		l.m = map[string]*sync.Mutex{}
	}
	if _, ok := l.m[providerID]; !ok {
		l.m[providerID] = &sync.Mutex{}
	}
	return l.m[providerID]
}

func (s *Server) handleProbe(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.deps.Key == nil {
		writeError(w, http.StatusServiceUnavailable, "no keyring")
		return
	}

	rows, err := s.deps.DB.ProviderRows(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var row store.ProviderRow
	var found bool
	for _, p := range rows {
		if p.ID == id {
			row, found = p, true
		}
	}
	if !found {
		writeError(w, http.StatusNotFound, fmt.Sprintf("no provider %q", id))
		return
	}
	creds, err := s.deps.DB.Credentials(r.Context(), s.deps.Key, id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if len(creds) == 0 {
		// A refusal rather than a failed probe: there is nothing to test, and
		// reporting "credential invalid" for a provider with no credential
		// would send the operator looking for the wrong problem.
		writeError(w, http.StatusBadRequest, "this provider has no credential to test")
		return
	}
	cred := creds[0]

	// One probe per provider at a time. The second caller blocks here and gets
	// its own fresh answer once the first finishes, which is correct: by then
	// the state it is reporting is current.
	lock := s.probes.get(id)
	lock.Lock()
	defer lock.Unlock()

	ctx, cancel := context.WithTimeout(r.Context(), probeTimeout)
	defer cancel()

	started := time.Now()
	kind, count, perr := s.runProbe(ctx, row, cred)
	latency := time.Since(started).Milliseconds()

	if perr != nil {
		// 200 with ok:false. A rejected key is an answer, not a server error,
		// and a 500 would make the settings screen show "something broke" for
		// the one outcome the button exists to discover.
		writeJSON(w, http.StatusOK, map[string]any{
			"ok": false, "probe": kind, "latency_ms": latency, "error": perr.Error(),
		})
		return
	}

	// Spec §4.3: a successful probe resets the ladder, or the operator reads
	// "probe OK" beside "still cooling". Recorded against BOTH key shapes: a
	// credential-level cooldown is stored with an empty model, a triple
	// cooldown with one, and clearing only one leaves the other cooling.
	if s.deps.Breaker != nil {
		s.deps.Breaker.Record(healthKey(id, cred.ID, ""),
			health.Signal{Outcome: adapter.OutcomeSuccess})
		for _, m := range s.modelsOf(r.Context(), id) {
			s.deps.Breaker.Record(healthKey(id, cred.ID, m),
				health.Signal{Outcome: adapter.OutcomeSuccess})
		}
	}
	// Spec §4.3: a successful probe triggers an on-demand discovery pass, so a
	// newly added provider's models appear without waiting for the sweep.
	if s.deps.Disc != nil {
		s.deps.Disc.Trigger(id)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"ok": true, "probe": kind, "latency_ms": latency, "model_count": count,
	})
}

// modelsOf lists the models this provider currently carries, which is the set of
// triple cooldowns a successful probe has to clear.
func (s *Server) modelsOf(ctx context.Context, providerID string) []string {
	if s.deps.Catalog == nil {
		return nil
	}
	return s.deps.Catalog.Snapshot().Offering(providerID)
}

// runProbe performs the real upstream call. It reports the kind of probe that
// ran alongside the result, because a listing and a one-token completion cost
// the operator different things and spec §4.3 accepts the second only as a
// fallback.
func (s *Server) runProbe(ctx context.Context, row store.ProviderRow,
	cred store.Credential) (kind string, modelCount int, err error) {

	// Phase 6 already builds a listing request per kind and parses the result.
	// Reusing it is what keeps the probe honest: it exercises the same path
	// discovery does, so "probe OK" means discovery will work.
	req, ok := catalog.ProbeFor(row.Kind, row.BaseURL, cred.Secret)
	if !ok {
		// No listing endpoint for this kind. Spec §4.3's fallback spends real
		// money and consumes quota, which is accepted for a manual action and
		// is why the kind is reported back.
		return "completion", 0, fmt.Errorf(
			"this provider kind has no listing endpoint and the completion fallback is not wired yet")
	}
	models, err := catalog.RunProbe(ctx, req)
	if err != nil {
		return "listing", 0, err
	}
	return "listing", len(models), nil
}
```

**`catalog.ProbeFor` and `catalog.RunProbe` are the names this task assumes, not
names it verified.** Phase 6 built the discovery probe; read
`internal/catalog/probe.go` and `discover.go` and call what is actually there.
If the listing request builder is unexported, export it rather than
reimplementing it — a probe that builds its own request stops being evidence that
discovery will work, which is the whole value of the button.

Add the lock field to `Server` in `admin.go`:

```go
type Server struct {
	deps   Deps
	csrf   *CSRF
	mux    *http.ServeMux
	probes probeLocks
}
```

and register the route:

```go
	s.mux.HandleFunc("POST /api/providers/{id}/test", s.requireCSRF(s.handleProbe))
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/admin/ -race -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'
```

Expected: PASS, every test in the package.

- [ ] **Step 5: Run the concurrency test repeatedly**

`TestADoubleClickIssuesOneProbe` synchronizes three goroutines and a real HTTP
server. A flaky pass here would be worse than a failure, because the property it
guards is invisible in production until an operator double-clicks.

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/admin/ -run TestADoubleClick -race -count=10
```

Expected: `ok`, no flakes.

- [ ] **Step 6: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing.

- [ ] **Step 7: Commit**

```bash
git add internal/admin/probe.go internal/admin/admin.go internal/admin/probe_test.go
git commit -m "feat(admin): probe a credential and clear its cooldown"
```

---

### Task 11: The keyset cursor

**Files:**
- Create: `internal/admin/cursor.go`
- Test: `internal/admin/cursor_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `admin.RequestFilters` with `Hash() string`, `admin.encodeCursor(ts int64, id string, f RequestFilters) string`, `admin.decodeCursor(s string, f RequestFilters) (ts int64, id string, err error)`. Task 12's handler uses all three.

Spec §4.2 states the contract "because two implementers would otherwise produce
incompatible cursors", so it is implemented alone, before the query that uses
it, and pinned by its own tests.

Four properties, each with a reason:

- **Sort is `ts DESC, id DESC`, and the tie-break is total.** Request ids are
  ULIDs, lexicographically ordered by time, so two rows with an identical
  millisecond still have a defined order. Without a total order a page boundary
  can repeat or skip a row forever.
- **The predicate is the lexicographic tuple `(ts, id) < (cursor_ts, cursor_id)`.**
  Written as `ts < ? OR (ts = ? AND id < ?)` in SQL, because SQLite supports row
  values but the index is used more reliably by the expanded form.
- **The cursor carries a hash of the active filters**, and a cursor presented
  with different filters is rejected rather than silently returning nonsense. A
  cursor is a position in one ordered result set; presented against a different
  set it names a row that may not be in it.
- **The cursor is opaque.** Base64 of a compact encoding, so nothing in the SPA
  is tempted to construct or arithmetic on one.

- [ ] **Step 1: Write the failing test**

Create `internal/admin/cursor_test.go`:

```go
package admin

import (
	"strings"
	"testing"
)

func TestACursorRoundTrips(t *testing.T) {
	f := RequestFilters{Provider: "groq", Status: "success"}
	c := encodeCursor(1700000000123, "01ABCDEF", f)
	if c == "" {
		t.Fatal("empty cursor")
	}
	ts, id, err := decodeCursor(c, f)
	if err != nil {
		t.Fatal(err)
	}
	if ts != 1700000000123 || id != "01ABCDEF" {
		t.Errorf("decoded (%d, %q)", ts, id)
	}
}

func TestACursorIsRejectedWhenTheFiltersChange(t *testing.T) {
	// Spec §4.2: a cursor is a position in ONE ordered result set. Presented
	// against a different set it names a row that may not be in it, and
	// returning a page from there is nonsense the client cannot detect.
	c := encodeCursor(1700000000123, "01ABCDEF", RequestFilters{Provider: "groq"})
	if _, _, err := decodeCursor(c, RequestFilters{Provider: "openai"}); err == nil {
		t.Error("a cursor from one filter set decoded under another")
	}
}

func TestACursorIsRejectedWhenAFilterIsAdded(t *testing.T) {
	c := encodeCursor(1, "a", RequestFilters{Provider: "groq"})
	if _, _, err := decodeCursor(c, RequestFilters{Provider: "groq", Surface: "llm"}); err == nil {
		t.Error("adding a filter did not invalidate the cursor")
	}
}

func TestAnEmptyFilterSetStillHashes(t *testing.T) {
	// The unfiltered case is the common one and must round-trip.
	var f RequestFilters
	c := encodeCursor(5, "x", f)
	ts, id, err := decodeCursor(c, f)
	if err != nil {
		t.Fatal(err)
	}
	if ts != 5 || id != "x" {
		t.Errorf("decoded (%d, %q)", ts, id)
	}
}

func TestGarbageIsRejected(t *testing.T) {
	var f RequestFilters
	for _, c := range []string{"", "!!!", "YWJj", strings.Repeat("A", 200)} {
		if _, _, err := decodeCursor(c, f); err == nil {
			t.Errorf("cursor %q decoded", c)
		}
	}
}

func TestTheCursorIsOpaque(t *testing.T) {
	// Nothing in the SPA should be able to read a timestamp out of it and do
	// arithmetic. Opacity is what keeps the encoding free to change.
	c := encodeCursor(1700000000123, "01ABCDEF", RequestFilters{})
	if strings.Contains(c, "1700000000123") || strings.Contains(c, "01ABCDEF") {
		t.Errorf("cursor = %q; it leaks its contents", c)
	}
}

func TestFilterHashIsOrderIndependentButValueSensitive(t *testing.T) {
	// Two filter sets that mean the same thing must hash the same, or a page
	// boundary rejects a cursor that is in fact valid.
	a := RequestFilters{Provider: "groq", Model: "m", Status: "success"}
	b := RequestFilters{Status: "success", Model: "m", Provider: "groq"}
	if a.Hash() != b.Hash() {
		t.Error("identical filters hashed differently")
	}
	c := RequestFilters{Provider: "groq", Model: "m", Status: "error"}
	if a.Hash() == c.Hash() {
		t.Error("different filters hashed the same")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/admin/ -run Cursor -v
```

Expected: FAIL to build — `undefined: RequestFilters`.

- [ ] **Step 3: Write the cursor**

Create `internal/admin/cursor.go`:

```go
package admin

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// RequestFilters are spec §4.2's filter set: provider, model, status, alias,
// surface, and a time range.
//
// A struct rather than a map so the hash below cannot depend on iteration order,
// and so a new filter is a compile error at every call site rather than a
// silently ignored query parameter.
type RequestFilters struct {
	Provider string
	Model    string
	Status   string
	Alias    string
	Surface  string
	SinceMs  int64
	UntilMs  int64
}

// Hash identifies the filter set a cursor was minted under.
//
// Field order is fixed by the code rather than by iteration, so two structs with
// the same values always hash the same — a hash that varied would reject cursors
// that are in fact valid, which reads to the operator as the table refusing to
// scroll.
func (f RequestFilters) Hash() string {
	h := sha256.New()
	for _, s := range []string{f.Provider, f.Model, f.Status, f.Alias, f.Surface} {
		h.Write([]byte(s))
		// A separator, so {Provider:"ab"} and {Provider:"a", Model:"b"} differ.
		h.Write([]byte{0})
	}
	fmt.Fprintf(h, "%d\x00%d", f.SinceMs, f.UntilMs)
	// Eight bytes is plenty: this is a mismatch detector, not a MAC. A client
	// forging one gets a page of its own filter set, which it could have asked
	// for directly.
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil)[:8])
}

// encodeCursor packs a position and the filter set it belongs to.
//
// Opaque base64 so nothing in the SPA is tempted to read a timestamp out of it
// and do arithmetic, which is what keeps this encoding free to change.
func encodeCursor(ts int64, id string, f RequestFilters) string {
	raw := strconv.FormatInt(ts, 10) + "\x00" + id + "\x00" + f.Hash()
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

var errCursorMismatch = errors.New(
	"this cursor belongs to a different filter set; start from the first page")

// decodeCursor unpacks a cursor and rejects one minted under different filters.
//
// Spec §4.2: rejected rather than silently returning nonsense. A cursor is a
// position in one ordered result set; presented against a different set it names
// a row that may not be in it, and the page that comes back would look valid.
func decodeCursor(s string, f RequestFilters) (int64, string, error) {
	if s == "" {
		return 0, "", errors.New("empty cursor")
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return 0, "", fmt.Errorf("malformed cursor: %w", err)
	}
	parts := strings.Split(string(raw), "\x00")
	if len(parts) != 3 {
		return 0, "", errors.New("malformed cursor")
	}
	ts, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, "", fmt.Errorf("malformed cursor: %w", err)
	}
	if parts[2] != f.Hash() {
		return 0, "", errCursorMismatch
	}
	return ts, parts[1], nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/admin/ -run Cursor -race -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'
```

Expected: PASS, seven tests.

- [ ] **Step 5: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing.

- [ ] **Step 6: Commit**

```bash
git add internal/admin/cursor.go internal/admin/cursor_test.go
git commit -m "feat(admin): encode an opaque keyset cursor"
```

---

### Task 12: The request log, paginated and filtered

**Files:**
- Modify: `internal/store/adminstore.go`
- Create: `internal/admin/requests.go`
- Modify: `internal/admin/admin.go` (`routes`)
- Test: `internal/store/adminstore_test.go`, `internal/admin/requests_test.go`

**Interfaces:**
- Consumes: `RequestFilters` and the cursor (Task 11), the indexes (Task 1).
- Produces: `(*DB).ListRequests(ctx, q RequestQuery) ([]RequestSummary, error)` and `GET /api/requests`. Part B's requests table consumes it.

**Evaluation:** files 2 - spec 1 - coupling 1 - risk 2 = 6

The correctness property spec §7 names is the one worth writing the test for
first: **keyset correctness across an insert landing mid-scroll.** Offset
pagination gets this wrong — a row inserted at the head shifts every later page
by one, so the reader sees a row twice and never sees another. Keyset does not,
and this is the test that proves it rather than assuming it.

**`ts` and `id` are both selected and both returned**, because the next cursor
is built from the last row of the page and there is nowhere else to get them.

**The page size is capped server-side.** A client asking for a million rows is
asking the gateway to build a million-row JSON array in memory; the cap is 200
and a larger request is clamped rather than refused, because refusing would make
a UI bug look like a server outage.

- [ ] **Step 1: Write the failing store test**

Add to `internal/store/adminstore_test.go`:

```go
func seedRequests(t *testing.T, db *DB, n int) {
	t.Helper()
	w := NewLogWriter(db, LogOptions{})
	batch := make([]*RequestRecord, 0, n)
	for i := 0; i < n; i++ {
		batch = append(batch, &RequestRecord{
			ID: fmt.Sprintf("01REQ%08d", i),
			// Distinct milliseconds so the ordering is unambiguous except
			// where a test deliberately collides them.
			TS:              time.UnixMilli(int64(1700000000000 + i)),
			Dialect:         "openai",
			Surface:         "llm",
			RequestedModel:  "m",
			FinalProviderID: "groq",
			FinalModel:      "m",
			Status:          "success",
		})
	}
	if _, err := w.writeBatch(context.Background(), batch); err != nil {
		t.Fatal(err)
	}
}

func TestListRequestsReturnsNewestFirst(t *testing.T) {
	db := migrated(t)
	seedRequests(t, db, 5)
	got, err := db.ListRequests(context.Background(), RequestQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 {
		t.Fatalf("got %d rows", len(got))
	}
	if got[0].ID != "01REQ00000004" {
		t.Errorf("first row = %q, want the newest", got[0].ID)
	}
}

func TestAPageBoundaryNeitherRepeatsNorSkips(t *testing.T) {
	db := migrated(t)
	seedRequests(t, db, 10)
	ctx := context.Background()

	first, err := db.ListRequests(ctx, RequestQuery{Limit: 4})
	if err != nil {
		t.Fatal(err)
	}
	last := first[len(first)-1]
	second, err := db.ListRequests(ctx, RequestQuery{
		Limit: 4, AfterTS: last.TSMs, AfterID: last.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, r := range append(append([]RequestSummary{}, first...), second...) {
		if seen[r.ID] {
			t.Errorf("row %q appeared twice across the boundary", r.ID)
		}
		seen[r.ID] = true
	}
	if len(seen) != 8 {
		t.Errorf("saw %d distinct rows across two pages of 4", len(seen))
	}
}

func TestAnInsertMidScrollDoesNotShiftThePages(t *testing.T) {
	// Spec §7 names this case. Offset pagination gets it wrong: a row inserted
	// at the head shifts every later page by one, so the reader sees a row
	// twice and never sees another. Keyset does not.
	db := migrated(t)
	seedRequests(t, db, 10)
	ctx := context.Background()

	first, err := db.ListRequests(ctx, RequestQuery{Limit: 4})
	if err != nil {
		t.Fatal(err)
	}
	// A brand-new request lands at the head while the operator reads page one.
	w := NewLogWriter(db, LogOptions{})
	if _, err := w.writeBatch(ctx, []*RequestRecord{{
		ID: "01REQZZZZZZZZ", TS: time.UnixMilli(1700000099999),
		Dialect: "openai", Surface: "llm", RequestedModel: "m", Status: "success",
	}}); err != nil {
		t.Fatal(err)
	}

	last := first[len(first)-1]
	second, err := db.ListRequests(ctx, RequestQuery{
		Limit: 4, AfterTS: last.TSMs, AfterID: last.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range second {
		for _, f := range first {
			if r.ID == f.ID {
				t.Errorf("row %q repeated after an insert landed mid-scroll", r.ID)
			}
		}
		if r.ID == "01REQZZZZZZZZ" {
			t.Error("the newly inserted row appeared on page two")
		}
	}
}

func TestIdenticalTimestampsStillOrderTotally(t *testing.T) {
	// ULIDs are lexicographically ordered, which is what makes the tie-break
	// total. Without it a page boundary on a busy millisecond repeats forever.
	db := migrated(t)
	ctx := context.Background()
	w := NewLogWriter(db, LogOptions{})
	var batch []*RequestRecord
	for _, id := range []string{"01AAA", "01BBB", "01CCC"} {
		batch = append(batch, &RequestRecord{
			ID: id, TS: time.UnixMilli(1700000000000),
			Dialect: "openai", Surface: "llm", RequestedModel: "m", Status: "success",
		})
	}
	if _, err := w.writeBatch(ctx, batch); err != nil {
		t.Fatal(err)
	}
	got, err := db.ListRequests(ctx, RequestQuery{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "01CCC" || got[1].ID != "01BBB" {
		t.Fatalf("page one = %+v", got)
	}
	next, err := db.ListRequests(ctx, RequestQuery{
		Limit: 2, AfterTS: got[1].TSMs, AfterID: got[1].ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(next) != 1 || next[0].ID != "01AAA" {
		t.Errorf("page two = %+v", next)
	}
}

func TestFiltersNarrowTheResult(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	w := NewLogWriter(db, LogOptions{})
	if _, err := w.writeBatch(ctx, []*RequestRecord{
		{ID: "01A", TS: time.UnixMilli(3), Dialect: "openai", Surface: "llm",
			RequestedModel: "m", FinalProviderID: "groq", Status: "success"},
		{ID: "01B", TS: time.UnixMilli(2), Dialect: "openai", Surface: "embedding",
			RequestedModel: "e", FinalProviderID: "openai", Status: "error"},
	}); err != nil {
		t.Fatal(err)
	}
	got, err := db.ListRequests(ctx, RequestQuery{Limit: 10, Provider: "groq"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "01A" {
		t.Errorf("provider filter = %+v", got)
	}
	got, err = db.ListRequests(ctx, RequestQuery{Limit: 10, Surface: "embedding"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "01B" {
		t.Errorf("surface filter = %+v", got)
	}
}
```

Add `"fmt"` to that file's imports.

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/store/ -run 'ListRequests|PageBoundary|InsertMidScroll|IdenticalTimestamps|FiltersNarrow' -v
```

Expected: FAIL to build — `undefined: RequestQuery`.

- [ ] **Step 3: Write the store query**

Append to `internal/store/adminstore.go`:

```go
// maxRequestPage caps a page server-side. A client asking for a million rows is
// asking the gateway to build a million-row JSON array in memory. A larger
// request is clamped rather than refused, because refusing makes a UI bug look
// like a server outage.
const maxRequestPage = 200

// RequestQuery is one page request. AfterTS and AfterID together are the keyset
// position; both zero means the first page.
type RequestQuery struct {
	Limit   int
	AfterTS int64
	AfterID string

	Provider string
	Model    string
	Status   string
	Alias    string
	Surface  string
	SinceMs  int64
	UntilMs  int64
}

// RequestSummary is one row of the table. It carries TSMs and ID because the
// next cursor is built from the last row of the page and there is nowhere else
// to get them.
type RequestSummary struct {
	ID              string
	TSMs            int64
	Dialect         string
	Surface         string
	RequestedModel  string
	ResolvedAlias   string
	FinalProviderID string
	FinalModel      string
	Status          string
	TokensIn        int64
	TokensOut       int64
	CostMicros      *int64
	TTFTMs          *int64
	TotalMs         *int64
	ErrorCode       string
	Attempts        int
}

// ListRequests returns one keyset page, newest first.
//
// The predicate is the lexicographic tuple (ts, id) < (cursor_ts, cursor_id),
// written expanded rather than as a row value because SQLite uses the composite
// index more reliably that way. The tie-break on id is what makes the order
// total: request ids are ULIDs, lexicographically ordered by time, so two rows
// in the same millisecond still have a defined position and a page boundary
// there neither repeats nor skips.
func (d *DB) ListRequests(ctx context.Context, q RequestQuery) ([]RequestSummary, error) {
	limit := q.Limit
	if limit <= 0 || limit > maxRequestPage {
		limit = maxRequestPage
	}

	where := []string{"1 = 1"}
	args := []any{}
	if q.AfterID != "" {
		where = append(where, "(r.ts < ? OR (r.ts = ? AND r.id < ?))")
		args = append(args, q.AfterTS, q.AfterTS, q.AfterID)
	}
	for col, val := range map[string]string{
		"r.final_provider_id": q.Provider,
		"r.final_model":       q.Model,
		"r.status":            q.Status,
		"r.resolved_alias":    q.Alias,
		"r.surface":           q.Surface,
	} {
		if val != "" {
			where = append(where, col+" = ?")
			args = append(args, val)
		}
	}
	if q.SinceMs > 0 {
		where = append(where, "r.ts >= ?")
		args = append(args, q.SinceMs)
	}
	if q.UntilMs > 0 {
		where = append(where, "r.ts <= ?")
		args = append(args, q.UntilMs)
	}
	args = append(args, limit)

	rows, err := d.Read.QueryContext(ctx,
		`SELECT r.id, r.ts, r.dialect, r.surface, r.requested_model, r.resolved_alias,
		        r.final_provider_id, r.final_model, r.status,
		        r.tokens_in, r.tokens_out, r.cost_micros, r.ttft_ms, r.total_ms, r.error_code,
		        (SELECT count(*) FROM request_attempts a WHERE a.request_id = r.id)
		   FROM requests r
		  WHERE `+strings.Join(where, " AND ")+`
		  ORDER BY r.ts DESC, r.id DESC
		  LIMIT ?`, args...)
	if err != nil {
		return nil, fmt.Errorf("list requests: %w", err)
	}
	defer rows.Close()

	out := make([]RequestSummary, 0, limit)
	for rows.Next() {
		var s RequestSummary
		if err := rows.Scan(&s.ID, &s.TSMs, &s.Dialect, &s.Surface, &s.RequestedModel,
			&s.ResolvedAlias, &s.FinalProviderID, &s.FinalModel, &s.Status,
			&s.TokensIn, &s.TokensOut, &s.CostMicros, &s.TTFTMs, &s.TotalMs,
			&s.ErrorCode, &s.Attempts); err != nil {
			return nil, fmt.Errorf("list requests: %w", err)
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
```

**The `map[string]string` loop above iterates in random order**, which changes
the `WHERE` clause text between calls and defeats SQLite's statement cache. It is
written that way here for brevity; write it as an explicit sequence of `if`
statements in the same fixed order as `RequestFilters.Hash`, so the generated SQL
is deterministic.

- [ ] **Step 4: Write the handler and its test**

Create `internal/admin/requests.go`:

```go
package admin

import (
	"net/http"
	"strconv"

	"github.com/darkraise/darkrouter/internal/store"
)

func filtersFrom(r *http.Request) RequestFilters {
	q := r.URL.Query()
	f := RequestFilters{
		Provider: q.Get("provider"),
		Model:    q.Get("model"),
		Status:   q.Get("status"),
		Alias:    q.Get("alias"),
		Surface:  q.Get("surface"),
	}
	f.SinceMs, _ = strconv.ParseInt(q.Get("since_ms"), 10, 64)
	f.UntilMs, _ = strconv.ParseInt(q.Get("until_ms"), 10, 64)
	return f
}

func (s *Server) handleListRequests(w http.ResponseWriter, r *http.Request) {
	f := filtersFrom(r)
	q := store.RequestQuery{
		Provider: f.Provider, Model: f.Model, Status: f.Status,
		Alias: f.Alias, Surface: f.Surface,
		SinceMs: f.SinceMs, UntilMs: f.UntilMs,
	}
	if n, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil {
		q.Limit = n
	}
	if c := r.URL.Query().Get("cursor"); c != "" {
		ts, id, err := decodeCursor(c, f)
		if err != nil {
			// 400 rather than a silent first page: the client asked for a
			// specific position and got something else, and it should know.
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		q.AfterTS, q.AfterID = ts, id
	}

	rows, err := s.deps.DB.ListRequests(r.Context(), q)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	type view struct {
		ID         string `json:"id"`
		TSMs       int64  `json:"ts_ms"`
		Dialect    string `json:"dialect"`
		Surface    string `json:"surface"`
		Model      string `json:"model"`
		Alias      string `json:"alias,omitempty"`
		Provider   string `json:"provider,omitempty"`
		FinalModel string `json:"final_model,omitempty"`
		Status     string `json:"status"`
		TokensIn   int64  `json:"tokens_in"`
		TokensOut  int64  `json:"tokens_out"`
		CostMicros *int64 `json:"cost_micros"`
		TTFTMs     *int64 `json:"ttft_ms"`
		TotalMs    *int64 `json:"total_ms"`
		ErrorCode  string `json:"error_code,omitempty"`
		Attempts   int    `json:"attempts"`
	}
	out := make([]view, 0, len(rows))
	for _, s := range rows {
		out = append(out, view{
			ID: s.ID, TSMs: s.TSMs, Dialect: s.Dialect, Surface: s.Surface,
			Model: s.RequestedModel, Alias: s.ResolvedAlias, Provider: s.FinalProviderID,
			FinalModel: s.FinalModel, Status: s.Status,
			TokensIn: s.TokensIn, TokensOut: s.TokensOut, CostMicros: s.CostMicros,
			TTFTMs: s.TTFTMs, TotalMs: s.TotalMs, ErrorCode: s.ErrorCode,
			Attempts: s.Attempts,
		})
	}

	body := map[string]any{"requests": out}
	// The next cursor is minted from the last row of this page under this
	// page's filters, which is what makes it invalid if the filters change.
	if len(rows) > 0 {
		last := rows[len(rows)-1]
		body["next_cursor"] = encodeCursor(last.TSMs, last.ID, f)
	}
	writeJSON(w, http.StatusOK, body)
}
```

Create `internal/admin/requests_test.go`:

```go
package admin

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/darkraise/darkrouter/internal/store"
)

func seedLog(t *testing.T, db *store.DB, n int) {
	t.Helper()
	w := store.NewLogWriter(db, store.LogOptions{})
	var batch []*store.RequestRecord
	for i := 0; i < n; i++ {
		batch = append(batch, &store.RequestRecord{
			ID: "01REQ" + string(rune('A'+i)), TS: time.UnixMilli(int64(1700000000000 + i)),
			Dialect: "openai", Surface: "llm", RequestedModel: "m",
			FinalProviderID: "groq", FinalModel: "m", Status: "success",
		})
	}
	if _, err := w.WriteBatchForTest(t, batch); err != nil {
		t.Fatal(err)
	}
}

func TestTheRequestsEndpointPagesWithACursor(t *testing.T) {
	s, db := testServerFull(t)
	seedLog(t, db, 6)
	cookie, token := login(t, s)

	w := do(t, s, cookie, token, "GET", "/api/requests?limit=3", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var page struct {
		Requests   []struct{ ID string } `json:"requests"`
		NextCursor string                `json:"next_cursor"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Requests) != 3 || page.NextCursor == "" {
		t.Fatalf("page one = %+v", page)
	}

	w = do(t, s, cookie, token, "GET", "/api/requests?limit=3&cursor="+page.NextCursor, "")
	var next struct {
		Requests []struct{ ID string } `json:"requests"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &next)
	for _, a := range page.Requests {
		for _, b := range next.Requests {
			if a.ID == b.ID {
				t.Errorf("row %q appeared on both pages", a.ID)
			}
		}
	}
}

func TestACursorFromDifferentFiltersIsRejected(t *testing.T) {
	// Spec §4.2. The alternative is a page of rows from another result set,
	// which the client cannot detect.
	s, db := testServerFull(t)
	seedLog(t, db, 4)
	cookie, token := login(t, s)

	w := do(t, s, cookie, token, "GET", "/api/requests?limit=2&provider=groq", "")
	var page struct {
		NextCursor string `json:"next_cursor"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &page)
	if page.NextCursor == "" {
		t.Fatal("no cursor to test with")
	}

	w = do(t, s, cookie, token, "GET",
		"/api/requests?limit=2&provider=openai&cursor="+page.NextCursor, "")
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestAnOversizedLimitIsClampedNotRefused(t *testing.T) {
	// Refusing would make a UI bug look like a server outage.
	s, db := testServerFull(t)
	seedLog(t, db, 5)
	cookie, token := login(t, s)

	w := do(t, s, cookie, token, "GET", "/api/requests?limit=1000000", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
}
```

`store.LogWriter.writeBatch` is unexported. Rather than exporting the write path
for a test, seed rows through `NewLogWriter` plus its normal `Log` call and a
flush, or add a small exported `WriteBatchForTest` in `internal/store/testing.go`
beside `MigratedForTest`. **Pick one and use it consistently** — the sketch above
names the second.

Register the route:

```go
	s.mux.HandleFunc("GET /api/requests", s.requireSession(s.handleListRequests))
```

- [ ] **Step 5: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/store/ ./internal/admin/ -race -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'
```

Expected: PASS in both packages.

- [ ] **Step 6: Confirm the index is actually used**

A keyset query that scans is a keyset query in name only.

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/store/ -run TestTheKeysetIndexExists -v
```

Expected: PASS. This is Task 1's test, re-run here because Task 12 is the first
code that depends on it being true.

- [ ] **Step 7: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing.

- [ ] **Step 8: Commit**

```bash
git add internal/store/adminstore.go internal/store/adminstore_test.go \
        internal/store/testing.go internal/admin/requests.go \
        internal/admin/requests_test.go internal/admin/admin.go
git commit -m "feat(admin): page the request log by keyset"
```

---

### Task 13: The full request trace

**Files:**
- Modify: `internal/store/adminstore.go`, `internal/admin/requests.go`, `internal/admin/admin.go`
- Test: `internal/store/adminstore_test.go`, `internal/admin/requests_test.go`

**Interfaces:**
- Consumes: the `requests`, `request_attempts` and `request_bodies` tables.
- Produces: `(*DB).RequestTrace(ctx, id string) (*RequestTrace, bool, error)` and `GET /api/requests/:id`. Part B's trace drawer is built entirely on it.

Spec §6: "The candidate list is what makes this screen worth building. A failover
that took three tries must read as three labelled rows with reasons, and the four
candidates that were never tried must say why."

So the endpoint returns **both** lists, and they are different things: `candidates`
is what the router produced, `skips` is what it rejected and why, `attempts` is
what actually ran. A drawer showing only attempts explains a failover; a drawer
showing all three explains a routing decision, which is the harder and more
useful question.

**Bodies are returned as an empty list, and that is correct.** Phase 5 recorded
that `capture.bodies` has a retention sweep and no writer — nothing has ever
inserted into `request_bodies`. The endpoint queries it anyway so the day a
writer lands the drawer works, and Part B renders the empty case as "not
captured" rather than as a broken panel.

**A missing id is 404, not an empty trace.** An operator following a stale link
must learn the row is gone rather than see a blank drawer that looks like a
rendering bug.

- [ ] **Step 1: Write the failing test**

Add to `internal/store/adminstore_test.go`:

```go
func TestRequestTraceCarriesCandidatesSkipsAndAttempts(t *testing.T) {
	db := migrated(t)
	ctx := context.Background()
	w := NewLogWriter(db, LogOptions{})
	cost := int64(1234)
	ttft := int64(56)
	if _, err := w.writeBatch(ctx, []*RequestRecord{{
		ID: "01TRACE", TS: time.UnixMilli(1700000000000),
		Dialect: "openai", Surface: "llm", RequestedModel: "fast",
		ResolvedAlias: "fast", FinalProviderID: "b", FinalModel: "m2",
		Status: "success", TokensIn: 10, TokensOut: 20,
		CostMicros: &cost, TTFTMs: &ttft,
		Candidates: []string{"a/m1", "b/m2", "c/m3"},
		Skips:      []string{"c/m3:cooling", "d/m4:no_credential"},
		Warnings:   []string{"top_k -> openai: not expressible"},
		SurfaceMeta: map[string]any{"input_count": 3},
		Attempts: []AttemptRecord{
			{Seq: 1, ProviderID: "a", KeyID: "k1", Model: "m1",
				Outcome: "retryable_provider", StatusCode: 500, LatencyMs: 120,
				Error: "upstream 500"},
			{Seq: 2, ProviderID: "b", KeyID: "k2", Model: "m2",
				Outcome: "success", StatusCode: 200, LatencyMs: 340},
		},
	}}); err != nil {
		t.Fatal(err)
	}

	tr, ok, err := db.RequestTrace(ctx, "01TRACE")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("the trace was not found")
	}
	if len(tr.Candidates) != 3 {
		t.Errorf("candidates = %v", tr.Candidates)
	}
	if len(tr.Skips) != 2 || tr.Skips[0] != "c/m3:cooling" {
		t.Errorf("skips = %v; the drawer cannot say why a target was not tried", tr.Skips)
	}
	if len(tr.Attempts) != 2 {
		t.Fatalf("attempts = %+v", tr.Attempts)
	}
	if tr.Attempts[0].Seq != 1 || tr.Attempts[1].Outcome != "success" {
		t.Errorf("attempts are out of order or wrong: %+v", tr.Attempts)
	}
	if len(tr.Warnings) != 1 {
		t.Errorf("warnings = %v", tr.Warnings)
	}
	if tr.SurfaceMeta["input_count"].(float64) != 3 {
		t.Errorf("surface meta = %v", tr.SurfaceMeta)
	}
	if tr.Bodies == nil {
		t.Error("bodies is nil; it must be an empty slice so the drawer can range over it")
	}
}

func TestAnUnknownTraceIsAMissRatherThanAnError(t *testing.T) {
	db := migrated(t)
	_, ok, err := db.RequestTrace(context.Background(), "does-not-exist")
	if err != nil {
		t.Fatalf("a miss was reported as an error: %v", err)
	}
	if ok {
		t.Error("an unknown id was found")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/store/ -run Trace -v
```

Expected: FAIL to build — `db.RequestTrace undefined`.

- [ ] **Step 3: Write the store query**

Append to `internal/store/adminstore.go`:

```go
// RequestTrace is one request in full: what the router produced, what it
// rejected, and what actually ran.
//
// Candidates, Skips and Attempts are three different facts and the drawer shows
// all three. Attempts alone explains a failover; the other two explain the
// routing decision, which is the harder question and the reason spec §6 calls
// this screen the one worth building.
type RequestTrace struct {
	RequestSummary

	Candidates  []string
	Skips       []string
	Warnings    []string
	SurfaceMeta map[string]any

	ResponseBytes       int64
	ResponseContentType string

	Attempts []AttemptRecord
	// Bodies is always non-nil. capture.bodies has a retention sweep and no
	// writer, so this is empty today; the query exists so the drawer works the
	// day a writer lands, and the empty case renders as "not captured".
	Bodies []RequestBody
}

// RequestBody is a captured request or response body.
type RequestBody struct {
	Kind     string
	Bytes    int64
	Content  string
	Truncated bool
}

// RequestTrace reads one request with everything attached.
//
// A miss is reported as false rather than as an error: an operator following a
// stale link must learn the row is gone, and a 500 would say the server broke.
func (d *DB) RequestTrace(ctx context.Context, id string) (*RequestTrace, bool, error) {
	var tr RequestTrace
	var traceJSON, warningsJSON, metaJSON string

	err := d.Read.QueryRowContext(ctx,
		`SELECT id, ts, dialect, surface, requested_model, resolved_alias,
		        final_provider_id, final_model, status,
		        tokens_in, tokens_out, cost_micros, ttft_ms, total_ms, error_code,
		        candidates_json, warnings_json, surface_meta_json,
		        response_bytes, response_content_type
		   FROM requests WHERE id = ?`, id).Scan(
		&tr.ID, &tr.TSMs, &tr.Dialect, &tr.Surface, &tr.RequestedModel, &tr.ResolvedAlias,
		&tr.FinalProviderID, &tr.FinalModel, &tr.Status,
		&tr.TokensIn, &tr.TokensOut, &tr.CostMicros, &tr.TTFTMs, &tr.TotalMs, &tr.ErrorCode,
		&traceJSON, &warningsJSON, &metaJSON,
		&tr.ResponseBytes, &tr.ResponseContentType)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read trace %q: %w", id, err)
	}

	// Candidates and skips travel in one column, as the log writer packs them.
	var trace struct {
		Candidates []string `json:"candidates"`
		Skips      []string `json:"skips"`
	}
	if err := json.Unmarshal([]byte(traceJSON), &trace); err != nil {
		return nil, false, fmt.Errorf("read trace %q: %w", id, err)
	}
	tr.Candidates, tr.Skips = trace.Candidates, trace.Skips
	if err := json.Unmarshal([]byte(warningsJSON), &tr.Warnings); err != nil {
		return nil, false, fmt.Errorf("read trace %q: %w", id, err)
	}
	if err := json.Unmarshal([]byte(metaJSON), &tr.SurfaceMeta); err != nil {
		return nil, false, fmt.Errorf("read trace %q: %w", id, err)
	}

	rows, err := d.Read.QueryContext(ctx,
		`SELECT seq, provider_id, key_id, model, outcome, status_code, latency_ms, error
		   FROM request_attempts WHERE request_id = ? ORDER BY seq`, id)
	if err != nil {
		return nil, false, fmt.Errorf("read attempts %q: %w", id, err)
	}
	defer rows.Close()
	for rows.Next() {
		var a AttemptRecord
		if err := rows.Scan(&a.Seq, &a.ProviderID, &a.KeyID, &a.Model,
			&a.Outcome, &a.StatusCode, &a.LatencyMs, &a.Error); err != nil {
			return nil, false, fmt.Errorf("read attempts %q: %w", id, err)
		}
		tr.Attempts = append(tr.Attempts, a)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}

	// Non-nil so the drawer can range over it. Empty today: capture.bodies has
	// no writer, which phase 5 recorded and phase 7 does not change.
	tr.Bodies = []RequestBody{}
	brows, err := d.Read.QueryContext(ctx,
		`SELECT kind, bytes, content, truncated FROM request_bodies WHERE request_id = ?`, id)
	if err == nil {
		defer brows.Close()
		for brows.Next() {
			var b RequestBody
			var truncated int
			if err := brows.Scan(&b.Kind, &b.Bytes, &b.Content, &truncated); err != nil {
				return nil, false, fmt.Errorf("read bodies %q: %w", id, err)
			}
			b.Truncated = truncated != 0
			tr.Bodies = append(tr.Bodies, b)
		}
	}
	return &tr, true, nil
}
```

Add `"database/sql"`, `"encoding/json"` and `"errors"` to the imports.
**Read `request_bodies`'s real columns in migration 0001 before writing that last
query** — the sketch guesses `kind`, `bytes`, `content`, `truncated`, and a
mismatch there fails at runtime rather than at compile time because the query is
tolerated on error.

- [ ] **Step 4: Write the handler**

Append to `internal/admin/requests.go`:

```go
func (s *Server) handleRequestTrace(w http.ResponseWriter, r *http.Request) {
	tr, ok, err := s.deps.DB.RequestTrace(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if !ok {
		// 404 rather than an empty trace: an operator following a stale link
		// must learn the row is gone rather than see a blank drawer that looks
		// like a rendering bug.
		writeError(w, http.StatusNotFound, "no such request")
		return
	}

	attempts := make([]map[string]any, 0, len(tr.Attempts))
	for _, a := range tr.Attempts {
		attempts = append(attempts, map[string]any{
			"seq": a.Seq, "provider": a.ProviderID, "key_label": a.KeyID,
			"model": a.Model, "outcome": a.Outcome, "status_code": a.StatusCode,
			"latency_ms": a.LatencyMs, "error": a.Error,
		})
	}
	bodies := make([]map[string]any, 0, len(tr.Bodies))
	for _, b := range tr.Bodies {
		bodies = append(bodies, map[string]any{
			"kind": b.Kind, "bytes": b.Bytes, "content": b.Content, "truncated": b.Truncated,
		})
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"id": tr.ID, "ts_ms": tr.TSMs, "dialect": tr.Dialect, "surface": tr.Surface,
		"model": tr.RequestedModel, "alias": tr.ResolvedAlias,
		"provider": tr.FinalProviderID, "final_model": tr.FinalModel,
		"status": tr.Status, "error_code": tr.ErrorCode,
		"tokens_in": tr.TokensIn, "tokens_out": tr.TokensOut,
		"cost_micros": tr.CostMicros, "ttft_ms": tr.TTFTMs, "total_ms": tr.TotalMs,
		// Three separate lists, deliberately. Attempts alone explains a
		// failover; candidates and skips explain the routing decision.
		"candidates":            tr.Candidates,
		"skips":                 tr.Skips,
		"attempts":              attempts,
		"warnings":              tr.Warnings,
		"surface_meta":          tr.SurfaceMeta,
		"response_bytes":        tr.ResponseBytes,
		"response_content_type": tr.ResponseContentType,
		"bodies":                bodies,
	})
}
```

Add to `internal/admin/requests_test.go`:

```go
func TestTheTraceEndpointExplainsAFailover(t *testing.T) {
	// Spec §6: three attempts must read as three labelled rows with reasons,
	// and the candidates never tried must say why.
	s, db := testServerFull(t)
	seedFailoverTrace(t, db, "01FAIL")
	cookie, token := login(t, s)

	w := do(t, s, cookie, token, "GET", "/api/requests/01FAIL", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var tr struct {
		Candidates []string `json:"candidates"`
		Skips      []string `json:"skips"`
		Attempts   []struct {
			Seq      int    `json:"seq"`
			Provider string `json:"provider"`
			Outcome  string `json:"outcome"`
		} `json:"attempts"`
		Bodies []any `json:"bodies"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &tr); err != nil {
		t.Fatal(err)
	}
	if len(tr.Attempts) != 2 || tr.Attempts[0].Seq != 1 {
		t.Fatalf("attempts = %+v", tr.Attempts)
	}
	if len(tr.Skips) == 0 {
		t.Error("no skips; the drawer cannot say why a candidate was not tried")
	}
	if tr.Bodies == nil {
		t.Error("bodies is null; the drawer cannot range over it")
	}
}

func TestAnUnknownTraceIs404(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "GET", "/api/requests/nope", "")
	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
}
```

`seedFailoverTrace` writes the same record the store test uses; put it beside
`seedLog` so both tests share one fixture shape.

Register the route:

```go
	s.mux.HandleFunc("GET /api/requests/{id}", s.requireSession(s.handleRequestTrace))
```

- [ ] **Step 5: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/store/ ./internal/admin/ -race -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'
```

Expected: PASS in both packages.

- [ ] **Step 6: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing.

- [ ] **Step 7: Commit**

```bash
git add internal/store/adminstore.go internal/store/adminstore_test.go \
        internal/admin/requests.go internal/admin/requests_test.go internal/admin/admin.go
git commit -m "feat(admin): return the full request trace"
```

---

### Task 14: The catalog search

**Files:**
- Create: `internal/admin/catalog.go`
- Modify: `internal/admin/admin.go`
- Test: `internal/admin/catalog_test.go`

**Interfaces:**
- Consumes: `catalog.Store.Snapshot`, `config.Config.Aliases`.
- Produces: `GET /api/models`. Part B's catalog screen consumes it.

Spec §6's catalog screen: searchable across every provider, filtered by surface,
capability, price band and context window, showing which providers serve each
model, what each alias resolves to, and whether metadata is known or inferred.

**The inferred flag is the point.** Master design §6.4 admits a model with
guessed capabilities and routes it with a warning; an operator looking at the
catalog needs to see which rows are guesses, because a guessed row that refuses
tool calls looks like a Darkrouter bug rather than a metadata gap.

**Aliases are resolved here rather than in the SPA.** The alias chain lives in
the configuration and the catalog lives in the database; joining them in the
browser would mean shipping both and duplicating the resolution rules that
`router.Resolve` already owns.

- [ ] **Step 1: Write the failing test**

Create `internal/admin/catalog_test.go`:

```go
package admin

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestTheCatalogListsModelsAcrossProviders(t *testing.T) {
	s, _ := testServerWithCatalog(t)
	cookie, token := login(t, s)

	w := do(t, s, cookie, token, "GET", "/api/models", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var body struct {
		Models []struct {
			Model     string   `json:"model"`
			Providers []string `json:"providers"`
			Surfaces  []string `json:"surfaces"`
			Inferred  bool     `json:"inferred"`
		} `json:"models"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	var shared bool
	for _, m := range body.Models {
		if m.Model == "shared-model" {
			shared = true
			if len(m.Providers) != 2 {
				t.Errorf("shared-model providers = %v, want two", m.Providers)
			}
		}
	}
	if !shared {
		t.Fatalf("shared-model is missing: %+v", body.Models)
	}
}

func TestTheCatalogMarksInferredMetadata(t *testing.T) {
	// Master design §6.4 routes a guessed model with a warning. An operator
	// reading the catalog needs to see which rows are guesses, or a guessed
	// row that refuses tool calls looks like a Darkrouter bug.
	s, _ := testServerWithCatalog(t)
	cookie, token := login(t, s)

	w := do(t, s, cookie, token, "GET", "/api/models", "")
	var body struct {
		Models []struct {
			Model    string `json:"model"`
			Inferred bool   `json:"inferred"`
		} `json:"models"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	var found bool
	for _, m := range body.Models {
		if m.Model == "guessed-model" {
			found = true
			if !m.Inferred {
				t.Error("guessed-model is not marked inferred")
			}
		}
		if m.Model == "known-model" && m.Inferred {
			t.Error("known-model is marked inferred")
		}
	}
	if !found {
		t.Error("guessed-model is missing")
	}
}

func TestTheCatalogFiltersBySurface(t *testing.T) {
	s, _ := testServerWithCatalog(t)
	cookie, token := login(t, s)

	w := do(t, s, cookie, token, "GET", "/api/models?surface=embedding", "")
	var body struct {
		Models []struct {
			Model    string   `json:"model"`
			Surfaces []string `json:"surfaces"`
		} `json:"models"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if len(body.Models) == 0 {
		t.Fatal("the embedding filter returned nothing")
	}
	for _, m := range body.Models {
		var has bool
		for _, s := range m.Surfaces {
			if s == "embedding" {
				has = true
			}
		}
		if !has {
			t.Errorf("model %q does not serve embedding", m.Model)
		}
	}
}

func TestTheCatalogSearchesBySubstring(t *testing.T) {
	s, _ := testServerWithCatalog(t)
	cookie, token := login(t, s)

	w := do(t, s, cookie, token, "GET", "/api/models?q=guess", "")
	var body struct {
		Models []struct {
			Model string `json:"model"`
		} `json:"models"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if len(body.Models) != 1 || body.Models[0].Model != "guessed-model" {
		t.Errorf("search = %+v", body.Models)
	}
}

func TestTheCatalogReportsWhatEachAliasResolvesTo(t *testing.T) {
	// The chain lives in the configuration and the catalog lives in the
	// database. Joining them in the browser would duplicate resolution rules
	// the router already owns.
	s, _ := testServerWithCatalogAndAlias(t, "fast", []string{"a/shared-model"})
	cookie, token := login(t, s)

	w := do(t, s, cookie, token, "GET", "/api/models", "")
	var body struct {
		Aliases []struct {
			Name    string   `json:"name"`
			Targets []string `json:"targets"`
		} `json:"aliases"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Aliases) != 1 || body.Aliases[0].Name != "fast" {
		t.Fatalf("aliases = %+v", body.Aliases)
	}
	if len(body.Aliases[0].Targets) != 1 || body.Aliases[0].Targets[0] != "a/shared-model" {
		t.Errorf("targets = %v", body.Aliases[0].Targets)
	}
}
```

Add `testServerWithCatalog(t)` and `testServerWithCatalogAndAlias(t, name, chain)`
beside the other fixtures. The catalog holds four models: `shared-model` on
providers `a` and `b`, `known-model` with `Capabilities.Known` true, and
`guessed-model` with it false, one of them declaring `ir.SurfaceEmbedding`.

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/admin/ -run Catalog -v
```

Expected: FAIL — the route 404s.

- [ ] **Step 3: Write the handler**

Create `internal/admin/catalog.go`:

```go
package admin

import (
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/darkraise/darkrouter/internal/catalog"
	"github.com/darkraise/darkrouter/internal/ir"
)

type modelView struct {
	Model         string   `json:"model"`
	Providers     []string `json:"providers"`
	Surfaces      []string `json:"surfaces"`
	ContextWindow int      `json:"context_window"`
	MaxOutput     int      `json:"max_output_tokens"`
	Tools         bool     `json:"tools"`
	Vision        bool     `json:"vision"`
	Reasoning     bool     `json:"reasoning"`
	// Inferred marks a row whose capabilities were guessed rather than read.
	// Master design §6.4 routes these with a warning, and an operator needs to
	// know which they are.
	Inferred     bool   `json:"inferred"`
	InputPerMTok *int64 `json:"input_per_mtok,omitempty"`
	State        string `json:"state"`
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	body := map[string]any{"models": []modelView{}, "aliases": []any{}}
	if s.deps.Catalog != nil {
		body["models"] = s.collectModels(r)
	}
	if s.deps.Config != nil {
		type aliasView struct {
			Name    string   `json:"name"`
			Targets []string `json:"targets"`
		}
		aliases := make([]aliasView, 0)
		for name, chain := range s.deps.Config.Current().Aliases {
			aliases = append(aliases, aliasView{Name: name, Targets: chain})
		}
		// Sorted: a map iteration order would reshuffle the list on every poll.
		sort.Slice(aliases, func(i, j int) bool { return aliases[i].Name < aliases[j].Name })
		body["aliases"] = aliases
	}
	writeJSON(w, http.StatusOK, body)
}

// collectModels folds the per-provider catalog rows into one row per model name,
// which is the shape the screen shows: "which providers serve this model".
func (s *Server) collectModels(r *http.Request) []modelView {
	q := r.URL.Query()
	search := strings.ToLower(q.Get("q"))
	surface := q.Get("surface")
	minContext, _ := strconv.Atoi(q.Get("min_context"))
	wantTools := q.Get("tools") == "true"

	byModel := map[string]*modelView{}
	for _, m := range s.deps.Catalog.Snapshot().All() {
		if search != "" && !strings.Contains(strings.ToLower(m.ModelID), search) {
			continue
		}
		if surface != "" && !m.DeclaresSurface(ir.Surface(surface)) {
			continue
		}
		if minContext > 0 && m.ContextWindow < minContext {
			continue
		}
		if wantTools && !m.Capabilities.Tools {
			continue
		}
		v, ok := byModel[m.ModelID]
		if !ok {
			v = &modelView{
				Model: m.ModelID, Providers: []string{},
				Surfaces: surfaceNames(m.Surfaces), State: string(m.State),
				ContextWindow: m.ContextWindow, MaxOutput: m.MaxOutputTokens,
				Tools: m.Capabilities.Tools, Vision: m.Capabilities.Vision,
				Reasoning: m.Capabilities.Reasoning,
				// Inferred if ANY provider's row is a guess: the operator is
				// warned about the weakest row, because that is the one that
				// may route badly.
				Inferred: !m.Capabilities.Known,
			}
			byModel[m.ModelID] = v
		}
		v.Providers = append(v.Providers, m.ProviderID)
		if !m.Capabilities.Known {
			v.Inferred = true
		}
	}

	out := make([]modelView, 0, len(byModel))
	for _, v := range byModel {
		sort.Strings(v.Providers)
		out = append(out, *v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Model < out[j].Model })
	return out
}

func surfaceNames(ss []ir.Surface) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		out = append(out, string(s))
	}
	return out
}

var _ = catalog.Model{}
```

**`Snapshot().All()` is assumed, not verified.** Phase 6 built the snapshot with
`Lookup`, `Offering` and `Search`; read `internal/catalog/snapshot.go` and use
what exists. If there is no whole-catalog accessor, add one rather than iterating
`Offering` per provider — the screen needs every model and the per-provider walk
would miss a provider with no configured models.

Register the route:

```go
	s.mux.HandleFunc("GET /api/models", s.requireSession(s.handleModels))
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/admin/ -race -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'
```

Expected: PASS, every test in the package.

- [ ] **Step 5: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing.

- [ ] **Step 6: Commit**

```bash
git add internal/admin/catalog.go internal/admin/catalog_test.go internal/admin/admin.go
git commit -m "feat(admin): search the model catalog"
```

---

### Task 15: Usage rollups and the overview

**Files:**
- Create: `internal/admin/usage.go`
- Modify: `internal/store/adminstore.go`, `internal/admin/admin.go`
- Test: `internal/admin/usage_test.go`

**Interfaces:**
- Consumes: `usage_daily`, `requests`, `health.Breaker.Snapshot`, `store.ProviderRows`.
- Produces: `GET /api/usage`, `GET /api/overview`. Part B's overview screen consumes both.

Two endpoints in one task because they read the same two tables and share every
fixture. Spec §6's overview is "a provider health grid — one tile per provider
with state, active cooldowns, and credential count — over an error-rate
sparkline, requests per minute, and today's spend".

**Provider-level signals, not raw triples.** Spec §6 is explicit and the reason
is worth stating: a provider with forty models cooling on one dead credential
produces forty breaker entries, and rendering forty red dots says "everything is
broken" when the truth is "one provider is down". The grid folds them into one
tile per provider with a count.

**A credential disabled pending OAuth reconnection is called out separately**,
because it is the one state only the operator can fix — everything else either
recovers on its own or is a provider's problem.

**Today's spend is honest about being incomplete.** `CostMicros` is nil on every
row today, because nothing computes cost — phase 5 recorded why, and it is
blocked on `ir.Usage.InputTokens` meaning different things across adapters. The
endpoint returns the sum of what is recorded and a flag saying pricing is not
wired, rather than a confident zero.

- [ ] **Step 1: Write the failing test**

Create `internal/admin/usage_test.go`:

```go
package admin

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/darkraise/darkrouter/internal/adapter"
	"github.com/darkraise/darkrouter/internal/health"
)

func TestTheOverviewShowsOneTilePerProvider(t *testing.T) {
	// Spec §6: provider-level signals, not raw triples. Forty models cooling
	// on one dead credential is one dead provider, not forty problems.
	s, db := testServerFull(t)
	cookie, token := login(t, s)
	seedProviderWithKey(t, s, cookie, token, "p1", "https://x/v1")
	seedProviderWithKey(t, s, cookie, token, "p2", "https://y/v1")
	_ = db

	w := do(t, s, cookie, token, "GET", "/api/overview", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var body struct {
		Providers []struct {
			ID          string `json:"id"`
			State       string `json:"state"`
			Cooling     int    `json:"cooling"`
			Credentials int    `json:"credentials"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Providers) != 2 {
		t.Fatalf("tiles = %+v", body.Providers)
	}
	for _, p := range body.Providers {
		if p.Credentials != 1 {
			t.Errorf("provider %s shows %d credentials", p.ID, p.Credentials)
		}
		if p.State != "healthy" {
			t.Errorf("provider %s state = %q, want healthy", p.ID, p.State)
		}
	}
}

func TestManyCoolingTriplesReadAsOneDegradedProvider(t *testing.T) {
	s, _ := testServerFull(t)
	cookie, token := login(t, s)
	keyID := seedProviderWithKey(t, s, cookie, token, "p1", "https://x/v1")

	// Forty models cooling on one credential.
	for i := 0; i < 40; i++ {
		k := health.Key{ProviderID: "p1", KeyID: keyID, Model: string(rune('a' + i%26))}
		for j := 0; j < 5; j++ {
			s.deps.Breaker.Record(k, health.Signal{
				Outcome: adapter.OutcomeRetryableProvider, StatusCode: 500,
			})
		}
	}

	w := do(t, s, cookie, token, "GET", "/api/overview", "")
	var body struct {
		Providers []struct {
			ID      string `json:"id"`
			State   string `json:"state"`
			Cooling int    `json:"cooling"`
		} `json:"providers"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if len(body.Providers) != 1 {
		t.Fatalf("tiles = %+v; forty cooling models produced more than one tile", body.Providers)
	}
	if body.Providers[0].State != "degraded" {
		t.Errorf("state = %q, want degraded", body.Providers[0].State)
	}
	if body.Providers[0].Cooling == 0 {
		t.Error("cooling count = 0; the tile does not say how much is down")
	}
}

func TestTodaysSpendSaysPricingIsNotWired(t *testing.T) {
	// CostMicros is nil on every row: nothing computes cost, and phase 5
	// recorded why. A confident zero would read as "today was free".
	s, _ := testServerFull(t)
	cookie, token := login(t, s)

	w := do(t, s, cookie, token, "GET", "/api/overview", "")
	var body struct {
		Spend struct {
			Micros  int64 `json:"micros"`
			Priced  bool  `json:"priced"`
		} `json:"today_spend"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Spend.Priced {
		t.Error("priced = true; nothing computes cost yet and the UI must not claim a total")
	}
}

func TestTheOverviewReportsRequestsPerMinuteAndErrorRate(t *testing.T) {
	s, db := testServerFull(t)
	seedMixedLog(t, db)
	cookie, token := login(t, s)

	w := do(t, s, cookie, token, "GET", "/api/overview", "")
	var body struct {
		RequestsPerMin float64 `json:"requests_per_min"`
		ErrorRate      float64 `json:"error_rate"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.ErrorRate <= 0 || body.ErrorRate >= 1 {
		t.Errorf("error rate = %v; the fixture is half errors", body.ErrorRate)
	}
}

func TestUsageRollsUpByDay(t *testing.T) {
	s, db := testServerFull(t)
	seedUsageDaily(t, db)
	cookie, token := login(t, s)

	w := do(t, s, cookie, token, "GET", "/api/usage", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var body struct {
		Days []struct {
			Day       string `json:"day"`
			Requests  int64  `json:"requests"`
			TokensIn  int64  `json:"tokens_in"`
			TokensOut int64  `json:"tokens_out"`
		} `json:"days"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Days) != 2 {
		t.Fatalf("days = %+v", body.Days)
	}
	// Oldest first: a chart reads left to right.
	if body.Days[0].Day > body.Days[1].Day {
		t.Errorf("days are newest-first: %+v", body.Days)
	}
}
```

Add `seedMixedLog(t, db)` — four request rows, two `success` and two `error`, all
within the last minute — and `seedUsageDaily(t, db)` — two `usage_daily` rows on
different days.

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/admin/ -run 'Overview|Usage|Spend|Cooling' -v
```

Expected: FAIL — both routes 404.

- [ ] **Step 3: Write the store queries**

Append to `internal/store/adminstore.go`:

```go
// UsageDay is one row of the usage chart.
type UsageDay struct {
	Day        string
	Requests   int64
	TokensIn   int64
	TokensOut  int64
	CostMicros *int64
}

// UsageByDay rolls usage_daily up across providers and models, oldest first
// because a chart reads left to right.
func (d *DB) UsageByDay(ctx context.Context, days int) ([]UsageDay, error) {
	if days <= 0 || days > 365 {
		days = 30
	}
	rows, err := d.Read.QueryContext(ctx,
		`SELECT day, sum(requests), sum(tokens_in), sum(tokens_out), sum(cost_micros)
		   FROM usage_daily GROUP BY day ORDER BY day DESC LIMIT ?`, days)
	if err != nil {
		return nil, fmt.Errorf("usage by day: %w", err)
	}
	defer rows.Close()

	var out []UsageDay
	for rows.Next() {
		var u UsageDay
		if err := rows.Scan(&u.Day, &u.Requests, &u.TokensIn, &u.TokensOut, &u.CostMicros); err != nil {
			return nil, fmt.Errorf("usage by day: %w", err)
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Reversed after the LIMIT: the query takes the newest N, the chart shows
	// them oldest first.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, nil
}

// RecentStats is the overview's headline numbers over a window.
type RecentStats struct {
	Requests  int64
	Errors    int64
	WindowSec int64
	// PricedRows counts rows carrying a cost. Zero means nothing computes
	// pricing, which the overview reports rather than showing a confident zero.
	PricedRows int64
	CostMicros int64
}

func (d *DB) RecentStats(ctx context.Context, window time.Duration) (RecentStats, error) {
	since := time.Now().Add(-window).UnixMilli()
	var s RecentStats
	s.WindowSec = int64(window.Seconds())
	err := d.Read.QueryRowContext(ctx,
		`SELECT count(*),
		        sum(CASE WHEN status != 'success' THEN 1 ELSE 0 END),
		        sum(CASE WHEN cost_micros IS NOT NULL THEN 1 ELSE 0 END),
		        coalesce(sum(cost_micros), 0)
		   FROM requests WHERE ts >= ?`, since).
		Scan(&s.Requests, &s.Errors, &s.PricedRows, &s.CostMicros)
	if err != nil {
		return s, fmt.Errorf("recent stats: %w", err)
	}
	return s, nil
}
```

- [ ] **Step 4: Write the handlers**

Create `internal/admin/usage.go`:

```go
package admin

import (
	"net/http"
	"strconv"
	"time"

	"github.com/darkraise/darkrouter/internal/health"
)

// overviewWindow is what "requests per minute" and the error rate are measured
// over. Five minutes rather than one: a homelab gateway can go a minute without
// traffic, and a rate computed over an empty minute reads as an outage.
const overviewWindow = 5 * time.Minute

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	rows, err := s.deps.DB.ProviderRows(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// Breaker entries are per triple. Folded per provider here because spec §6
	// asks for provider-level signals: forty models cooling on one dead
	// credential is one dead provider, and forty red dots would say otherwise.
	cooling := map[string]int{}
	if s.deps.Breaker != nil {
		for _, e := range s.deps.Breaker.Snapshot() {
			if e.CoolingUntil.After(time.Now()) {
				cooling[e.Key.ProviderID]++
			}
		}
	}

	type tile struct {
		ID          string `json:"id"`
		Name        string `json:"name"`
		State       string `json:"state"`
		Cooling     int    `json:"cooling"`
		Credentials int    `json:"credentials"`
		Enabled     bool   `json:"enabled"`
		NeedsAuth   bool   `json:"needs_reauth"`
	}
	tiles := make([]tile, 0, len(rows))
	for _, p := range rows {
		t := tile{ID: p.ID, Name: p.Name, Enabled: p.Enabled, Cooling: cooling[p.ID]}
		if s.deps.Key != nil {
			creds, cerr := s.deps.DB.Credentials(r.Context(), s.deps.Key, p.ID)
			if cerr == nil {
				t.Credentials = len(creds)
				for _, c := range creds {
					// The one state only the operator can fix. Everything else
					// either recovers on its own or is a provider's problem, so
					// it is called out rather than folded into "degraded".
					if !c.Enabled {
						t.NeedsAuth = true
					}
				}
			}
		}
		switch {
		case !p.Enabled:
			t.State = "disabled"
		case t.Credentials == 0:
			t.State = "unconfigured"
		case t.Cooling > 0:
			t.State = "degraded"
		default:
			t.State = "healthy"
		}
		tiles = append(tiles, t)
	}

	stats, err := s.deps.DB.RecentStats(r.Context(), overviewWindow)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	var errRate float64
	if stats.Requests > 0 {
		errRate = float64(stats.Errors) / float64(stats.Requests)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"providers":        tiles,
		"requests_per_min": float64(stats.Requests) / (float64(stats.WindowSec) / 60),
		"error_rate":       errRate,
		"window_sec":       stats.WindowSec,
		"today_spend": map[string]any{
			"micros": stats.CostMicros,
			// Nothing computes cost yet: phase 5 recorded that it is blocked on
			// ir.Usage.InputTokens meaning different things across adapters. A
			// confident zero would read as "today was free".
			"priced": stats.PricedRows > 0,
		},
	})
}

func (s *Server) handleUsage(w http.ResponseWriter, r *http.Request) {
	days := 30
	if n, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil {
		days = n
	}
	rows, err := s.deps.DB.UsageByDay(r.Context(), days)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	out := make([]map[string]any, 0, len(rows))
	priced := false
	for _, u := range rows {
		if u.CostMicros != nil {
			priced = true
		}
		out = append(out, map[string]any{
			"day": u.Day, "requests": u.Requests,
			"tokens_in": u.TokensIn, "tokens_out": u.TokensOut,
			"cost_micros": u.CostMicros,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"days": out, "priced": priced})
}

var _ = health.Key{}
```

**`health.Entry`'s real field names are assumed above.** `Snapshot()` returns
`[]Entry`; read `internal/health/breaker.go` and use its actual shape rather than
`e.Key.ProviderID` and `e.CoolingUntil` if they differ.

Register both routes:

```go
	s.mux.HandleFunc("GET /api/overview", s.requireSession(s.handleOverview))
	s.mux.HandleFunc("GET /api/usage", s.requireSession(s.handleUsage))
```

- [ ] **Step 5: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/admin/ ./internal/store/ -race -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'
```

Expected: PASS in both packages.

- [ ] **Step 6: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing.

- [ ] **Step 7: Commit**

```bash
git add internal/admin/usage.go internal/admin/usage_test.go \
        internal/store/adminstore.go internal/admin/admin.go
git commit -m "feat(admin): report health tiles and usage rollups"
```

---

### Task 16: The playground

**Files:**
- Create: `internal/admin/playground.go`
- Modify: `internal/admin/admin.go`
- Test: `internal/admin/playground_test.go`

**Interfaces:**
- Consumes: `exec.Executor.Handle`, `edge/openai.Dialect`.
- Produces: `POST /api/playground`. Part B's playground screen consumes it.

Spec §6: "Pick an alias or `provider/model`, send a prompt, watch it stream,
follow a link to the trace it produced. This is how a new credential or a
reordered alias gets verified without dropping to `curl`."

**It runs a real request through the real executor.** Anything else would verify
the playground rather than the gateway — a mock would pass while the credential
it was meant to test is wrong. So the handler builds an OpenAI chat body and
hands it to `exec.Handle`, which means the playground inherits failover, the
budget gate and the request log for free, and the trace link works because the
request really is in the log.

**It is a mutating verb and carries the CSRF header**, spec §4 says so
explicitly, and the SSE response works through the fetch-with-header pattern
rather than `EventSource` — which cannot set headers at all, and is the reason
this is stated rather than left to the frontend to discover.

**The request id is returned in a header before the stream starts**, because the
"follow a link to the trace" affordance needs the id and the body is a stream the
SPA is rendering as it arrives.

- [ ] **Step 1: Write the failing test**

Create `internal/admin/playground_test.go`:

```go
package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestThePlaygroundStreamsThroughTheRealExecutor(t *testing.T) {
	// A mock would verify the playground rather than the gateway: it would
	// pass while the credential it exists to test is wrong.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		f := w.(http.Flusher)
		for _, c := range []string{
			`{"id":"c1","model":"m","choices":[{"index":0,"delta":{"content":"he"}}]}`,
			`{"id":"c1","model":"m","choices":[{"index":0,"delta":{"content":"llo"}}]}`,
			`{"id":"c1","model":"m","choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
		} {
			_, _ = w.Write([]byte("data: " + c + "\n\n"))
			f.Flush()
		}
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		f.Flush()
	}))
	defer upstream.Close()

	s := testServerWithExecutor(t, upstream.URL, "m")
	cookie, token := login(t, s)

	w := do(t, s, cookie, token, "POST", "/api/playground",
		`{"model":"m","prompt":"say hi"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	if ct := w.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Errorf("content-type = %q", ct)
	}
	if !strings.Contains(w.Body.String(), "hello") &&
		!strings.Contains(w.Body.String(), `"he"`) {
		t.Errorf("the deltas did not reach the client:\n%s", w.Body.String())
	}
}

func TestThePlaygroundReturnsTheRequestIDForTheTraceLink(t *testing.T) {
	// Spec §6's "follow a link to the trace it produced". The id has to arrive
	// before the body, because the body is a stream the SPA renders as it
	// comes.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"c1","model":"m","choices":[
		  {"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}]}`))
	}))
	defer upstream.Close()

	s := testServerWithExecutor(t, upstream.URL, "m")
	cookie, token := login(t, s)

	w := do(t, s, cookie, token, "POST", "/api/playground", `{"model":"m","prompt":"hi"}`)
	if got := w.Header().Get("X-Darkrouter-Request"); got == "" {
		t.Error("no request id header; the trace link has nothing to point at")
	}
}

func TestThePlaygroundRequiresACSRFToken(t *testing.T) {
	// Spec §4: a mutating verb, so it carries the header like any other
	// despite streaming its response.
	s := testServerWithExecutor(t, "https://unused.example", "m")
	cookie, _ := login(t, s)

	r := httptest.NewRequest("POST", "/api/playground", strings.NewReader(`{"model":"m","prompt":"hi"}`))
	r.AddCookie(cookie)
	r.Header.Set("Sec-Fetch-Site", "same-origin")
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", w.Code)
	}
}

func TestThePlaygroundRejectsAnEmptyPrompt(t *testing.T) {
	s := testServerWithExecutor(t, "https://unused.example", "m")
	cookie, token := login(t, s)
	w := do(t, s, cookie, token, "POST", "/api/playground", `{"model":"m"}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}
```

`testServerWithExecutor(t, upstreamURL, model)` builds a real `exec.Executor`
over a one-provider config and a catalog declaring `model` on the `llm` surface,
and returns an admin `Server` carrying it in `Deps.Exec`.

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/admin/ -run Playground -v
```

Expected: FAIL to build — `Deps` has no `Exec` field.

- [ ] **Step 3: Write the handler**

Create `internal/admin/playground.go`:

```go
package admin

import (
	"bytes"
	"encoding/json"
	"net/http"

	openaiedge "github.com/darkraise/darkrouter/internal/edge/openai"
)

type playgroundBody struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
	System string `json:"system,omitempty"`
	Stream *bool  `json:"stream,omitempty"`
}

// handlePlayground runs a real request through the real executor.
//
// Anything else would verify the playground rather than the gateway: a mock
// would pass while the credential it exists to test is wrong. Going through
// exec.Handle also means the playground inherits failover, the budget gate and
// the request log for free, and the trace link works because the request really
// is in the log.
func (s *Server) handlePlayground(w http.ResponseWriter, r *http.Request) {
	if s.deps.Exec == nil {
		writeError(w, http.StatusServiceUnavailable, "no executor")
		return
	}
	var body playgroundBody
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 256<<10)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	if body.Model == "" {
		writeError(w, http.StatusBadRequest, "model is required")
		return
	}
	if body.Prompt == "" {
		writeError(w, http.StatusBadRequest, "prompt is required")
		return
	}

	stream := true
	if body.Stream != nil {
		stream = *body.Stream
	}
	msgs := []map[string]any{}
	if body.System != "" {
		msgs = append(msgs, map[string]any{"role": "system", "content": body.System})
	}
	msgs = append(msgs, map[string]any{"role": "user", "content": body.Prompt})

	chat, err := json.Marshal(map[string]any{
		"model": body.Model, "messages": msgs, "stream": stream,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	// A synthetic request against the proxy's own dialect. The path is what the
	// OpenAI dialect expects; nothing routes on it here, but a mismatched path
	// would make this handler the one place the shape differs from production.
	pr, err := http.NewRequestWithContext(r.Context(),
		http.MethodPost, "/v1/chat/completions", bytes.NewReader(chat))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	pr.Header.Set("Content-Type", "application/json")

	// exec.Handle writes X-Darkrouter-Request before anything else, which is
	// what gives the SPA the id for the trace link before the stream starts.
	s.deps.Exec.Handle(w, pr, openaiedge.New())
}
```

Add the field to `Deps`:

```go
	// Exec is the same executor the proxy port uses. The playground runs real
	// requests through it so what it verifies is the gateway rather than
	// itself.
	Exec *exec.Executor
```

Register the route:

```go
	s.mux.HandleFunc("POST /api/playground", s.requireCSRF(s.handlePlayground))
```

- [ ] **Step 4: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/admin/ -race -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'
```

Expected: PASS, every test in the package.

- [ ] **Step 5: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing.

- [ ] **Step 6: Commit**

```bash
git add internal/admin/playground.go internal/admin/playground_test.go internal/admin/admin.go
git commit -m "feat(admin): run a playground request through exec"
```

---

### Task 17: Wire the admin server into the binary

**Files:**
- Modify: `internal/server/server.go`, `cmd/darkrouter/main.go`
- Test: `internal/server/run_test.go`

**Interfaces:**
- Consumes: `admin.New`, `admin.Server.Handler`.
- Produces: the admin port serving `/api/*` alongside `/healthz`, `/readyz` and `/metrics`; a `hash-password` subcommand.

Until now `internal/admin` is a package nothing constructs. This wires it to the
admin listener, which is what turns nineteen tested handlers into a running API.

**`/healthz`, `/readyz` and `/metrics` stay unauthenticated and stay where they
are.** They are what a container orchestrator and a Prometheus scraper read, and
putting them behind a session would break both. `AdminHandler` keeps building
them and mounts the admin API alongside.

**`DARKROUTER_ADMIN_PASSWORD_HASH` being unset is a startup warning, not a
startup failure.** The gateway's job is proxying, and refusing to start because
the optional dashboard has no password would take a working proxy down over a
feature the operator may not use. The API refuses every login instead, which
Task 2 already guarantees, and the warning reaches `/healthz` where the other
startup warnings go.

**`hash-password` is a subcommand rather than a flag** because `main` already
dispatches subcommands before `flag.Parse`, and `rotate-key` is the pattern to
follow.

- [ ] **Step 1: Write the failing test**

Add to `internal/server/run_test.go`:

```go
func TestTheAdminPortServesTheAPI(t *testing.T) {
	srv, adminAddr := runServerForTest(t)
	_ = srv

	// Unauthenticated: proves the API is mounted and closed, in one request.
	resp, err := http.Get("http://" + adminAddr + "/api/overview")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", resp.StatusCode)
	}
}

func TestHealthEndpointsStayUnauthenticated(t *testing.T) {
	// A container orchestrator and a Prometheus scraper read these. Putting
	// them behind a session breaks both.
	_, adminAddr := runServerForTest(t)
	for _, path := range []string{"/healthz", "/readyz", "/metrics"} {
		resp, err := http.Get("http://" + adminAddr + path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("%s status = %d, want 200", path, resp.StatusCode)
		}
	}
}

func TestAMissingPasswordHashWarnsRatherThanFailingStartup(t *testing.T) {
	// The gateway's job is proxying. Refusing to start because the optional
	// dashboard has no password would take a working proxy down over a feature
	// the operator may not use.
	_, adminAddr := runServerForTestWithoutPasswordHash(t)

	resp, err := http.Get("http://" + adminAddr + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body struct {
		Warnings []string `json:"warnings"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, w := range body.Warnings {
		if strings.Contains(w, "password") {
			found = true
		}
	}
	if !found {
		t.Errorf("warnings = %v; an operator cannot tell the dashboard is closed", body.Warnings)
	}
}
```

`runServerForTest` may not exist under that name. **Read `internal/server/run_test.go`
first** — it already starts a server on an ephemeral port for the healthz tests,
and this must reuse that fixture rather than adding a second way to boot one.

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/server/ -run 'AdminPort|HealthEndpoints|MissingPasswordHash' -v
```

Expected: `/api/overview` returns 404 rather than 401, because nothing is mounted.

- [ ] **Step 3: Mount the admin API**

In `internal/server/server.go`, add the admin server to `Server` and build it in
`New`:

```go
	adm *admin.Server
```

```go
	// The admin API is optional in the sense that a missing password hash
	// closes it, not in the sense that it is absent: it is always mounted, and
	// always refuses every login when no hash is configured.
	passwordHash := os.Getenv("DARKROUTER_ADMIN_PASSWORD_HASH")
	if passwordHash == "" {
		startupWarnings = append(startupWarnings,
			"DARKROUTER_ADMIN_PASSWORD_HASH is not set; the admin dashboard will refuse every "+
				"login. Generate one with: darkrouter hash-password")
	}
	adm, err := admin.New(admin.Deps{
		DB: db, PasswordHash: passwordHash,
		Config: cfgStore, Src: src, Key: key,
		Catalog: cat, Disc: disc, Breaker: breaker,
		Presets: catalog.Embedded(), Exec: ex,
		Warnings: startupWarnings,
	})
	if err != nil {
		return nil, fmt.Errorf("admin: %w", err)
	}
```

**Order matters here.** `startupWarnings` is passed to `admin.New` by value, so
the password warning has to be appended *before* the call, and the same slice is
what `/healthz` reads. Build the warning first, then the admin server, then store
both.

Then in `AdminHandler`, mount the API alongside the existing endpoints:

```go
func (s *Server) AdminHandler() http.Handler {
	mux := http.NewServeMux()
	// … the existing /healthz, /readyz and /metrics registrations, unchanged.
	// They stay unauthenticated: an orchestrator and a scraper read them, and a
	// session in front of either breaks it.

	// Everything else goes to the admin API, which owns its own auth. Mounted
	// last and by prefix so the three endpoints above win their exact paths.
	mux.Handle("/", s.adm.Handler())
	return mux
}
```

- [ ] **Step 4: Add the hash-password subcommand**

In `cmd/darkrouter/main.go`, beside the existing `rotate-key` dispatch:

```go
	case "hash-password":
		os.Exit(hashPassword(os.Args[2:]))
```

and the function, following `rotateKey`'s shape:

```go
// hashPassword prints a bcrypt hash for DARKROUTER_ADMIN_PASSWORD_HASH.
//
// It exists so an operator can produce one with the binary they already have
// rather than installing a second tool, which is the difference between the
// dashboard being usable on a fresh box and not.
func hashPassword(args []string) int {
	fs := flag.NewFlagSet("hash-password", flag.ExitOnError)
	password := fs.String("password", "", "the password to hash; read from stdin when empty")
	_ = fs.Parse(args)

	pw := *password
	if pw == "" {
		// Read from stdin so the password does not land in shell history.
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read password: %v\n", err)
			return 1
		}
		pw = strings.TrimSpace(string(b))
	}
	h, err := admin.HashPassword(pw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return 1
	}
	fmt.Println(h)
	return 0
}
```

- [ ] **Step 5: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/server/ ./internal/admin/ -race -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'
```

Expected: PASS in both packages.

- [ ] **Step 6: Verify the subcommand works end to end**

```bash
export PATH=$PATH:/usr/local/go/bin
go build -o /tmp/dr-p7 ./cmd/darkrouter
HASH=$(echo -n 'hunter2' | /tmp/dr-p7 hash-password)
echo "$HASH"
case "$HASH" in '$2a$12$'*) echo "cost 12 bcrypt hash: OK" ;; *) echo "UNEXPECTED SHAPE"; exit 1 ;; esac
rm -f /tmp/dr-p7
```

Expected: a `$2a$12$…` hash on stdout and nothing else, so the value can be
pasted straight into an environment file or piped into one.

- [ ] **Step 7: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing.

- [ ] **Step 8: Commit**

```bash
git add internal/server/server.go internal/server/run_test.go cmd/darkrouter/main.go
git commit -m "feat(server): mount the admin api and hash-password"
```

---

### Task 18: Part A live verification

**Files:**
- Modify: `docs/PROGRESS.md`

**Interfaces:**
- Consumes: everything in Part A.
- Produces: the verification record for the API half.

Part A is a deliverable, so it gets verified like one. The unit suite proves the
handlers; this proves the binary serves them, that a browser-shaped session works
end to end, and that the CSRF and cookie rules hold against a real HTTP client
rather than an `httptest.Recorder`.

- [ ] **Step 1: Build and start**

```bash
export PATH=$PATH:/usr/local/go/bin
set -a; . ./.env; set +a
export DARKROUTER_MASTER_KEY=throwaway-p7-verify
export DARKROUTER_ADMIN_PASSWORD_HASH="$(echo -n 'hunter2' | go run ./cmd/darkrouter hash-password)"
echo "hash: ${DARKROUTER_ADMIN_PASSWORD_HASH:0:7}…"

CGO_ENABLED=0 go build -o /tmp/darkrouter-p7 ./cmd/darkrouter
rm -rf /tmp/dr-p7-data && mkdir -p /tmp/dr-p7-data
sed -e 's/:8080/:18080/' -e 's/:8081/:18081/' darkrouter.example.yaml > /tmp/dr-p7.yaml

/tmp/darkrouter-p7 -config /tmp/dr-p7.yaml -db /tmp/dr-p7-data/darkrouter.db \
  >/tmp/dr-p7.log 2>&1 &
sleep 4
ps -C darkrouter-p7 -o pid=
curl -fsS localhost:18081/readyz && echo " READY"
```

`darkrouter.example.yaml` needs `GROQ_KEY` in the environment; `.env` supplies
it. The `sed` moves the ports because 8080 and 8081 belong to an unrelated
application.

- [ ] **Step 2: Verify the API is closed before login**

```bash
for ep in overview providers models requests usage config presets; do
  printf '%-12s -> ' "$ep"
  curl -sS -o /dev/null -w '%{http_code}\n' "localhost:18081/api/$ep"
done
printf '%-12s -> ' "auth/status"
curl -sS -w ' %{http_code}\n' localhost:18081/api/auth/status
```

Expected: `401` on all seven, and `auth/status` returning `200` with
`{"authenticated":false}`. That one endpoint being open is what lets the SPA
decide to render the login screen.

- [ ] **Step 3: Log in and read the cookie**

```bash
rm -f /tmp/dr-p7.jar
curl -sS -c /tmp/dr-p7.jar -D /tmp/dr-p7.hdr \
  -H 'content-type: application/json' -H 'Sec-Fetch-Site: same-origin' \
  -d '{"password":"hunter2"}' localhost:18081/api/auth/login
echo
grep -i 'set-cookie' /tmp/dr-p7.hdr
```

Expected: a body carrying `authenticated: true` and a `csrf_token`, and a
`Set-Cookie` with `HttpOnly` and `SameSite=Lax`. **`Secure` must be absent**,
because this is plain HTTP — a `Secure` cookie here would be dropped by the
browser and login would silently never work, which is the failure mode worth
looking at the header for.

Capture the token for the next steps:

```bash
CSRF=$(curl -sS -b /tmp/dr-p7.jar localhost:18081/api/auth/status |
  python3 -c 'import json,sys; print(json.load(sys.stdin).get("csrf_token",""))')
echo "csrf: ${CSRF:0:12}…"
```

- [ ] **Step 4: Verify the CSRF and Origin rules against a real client**

```bash
echo -n 'no token      -> '
curl -sS -o /dev/null -w '%{http_code}\n' -b /tmp/dr-p7.jar -X POST \
  -H 'Sec-Fetch-Site: same-origin' localhost:18081/api/config/reload

echo -n 'foreign origin-> '
curl -sS -o /dev/null -w '%{http_code}\n' -b /tmp/dr-p7.jar -X POST \
  -H "X-CSRF-Token: $CSRF" -H 'Origin: https://evil.example' \
  localhost:18081/api/config/reload

echo -n 'no site header-> '
curl -sS -o /dev/null -w '%{http_code}\n' -b /tmp/dr-p7.jar -X POST \
  -H "X-CSRF-Token: $CSRF" localhost:18081/api/config/reload

echo -n 'correct       -> '
curl -sS -o /dev/null -w '%{http_code}\n' -b /tmp/dr-p7.jar -X POST \
  -H "X-CSRF-Token: $CSRF" -H 'Sec-Fetch-Site: same-origin' \
  localhost:18081/api/config/reload
```

Expected: `403`, `403`, `403`, `200`. The third is the one worth reading
carefully — a client sending neither header must be refused, or the check is
decorative.

- [ ] **Step 5: Verify the proxy port ignores the admin cookie**

This is the sharpest edge in the phase, so it is verified against a real client
rather than only in a unit test.

```bash
curl -sS -o /dev/null -w 'proxy with admin cookie -> %{http_code}\n' \
  -b /tmp/dr-p7.jar -H 'content-type: application/json' \
  -d '{"model":"openai/gpt-oss-120b","messages":[{"role":"user","content":"hi"}]}' \
  localhost:18080/v1/chat/completions
```

Expected: whatever the proxy's own auth says — a `200` if no `proxy_token` is
configured, a `401` if one is. **What must not happen is the cookie granting
access it would otherwise not have.** If `darkrouter.example.yaml` sets no
proxy token, add one to `/tmp/dr-p7.yaml`, restart, and re-run: the answer must
be `401` with the cookie present and `200` with the bearer token.

- [ ] **Step 6: Exercise provider CRUD, the probe, and the trace**

```bash
auth=(-b /tmp/dr-p7.jar -H "X-CSRF-Token: $CSRF" -H 'Sec-Fetch-Site: same-origin'
      -H 'content-type: application/json')

echo '--- create from preset ---'
curl -sS "${auth[@]}" -X POST -d '{"id":"groq2","preset":"groq"}' \
  localhost:18081/api/providers; echo

echo '--- add a credential ---'
curl -sS "${auth[@]}" -X POST -d "{\"label\":\"primary\",\"secret\":\"$GROQ_KEY\"}" \
  localhost:18081/api/providers/groq2/keys; echo

echo '--- list, masked ---'
curl -sS -b /tmp/dr-p7.jar localhost:18081/api/providers |
  python3 -m json.tool | head -30

echo '--- probe ---'
curl -sS "${auth[@]}" -X POST localhost:18081/api/providers/groq2/test; echo

echo '--- delete ---'
curl -sS "${auth[@]}" -X DELETE localhost:18081/api/providers/groq2; echo
```

Expected: `201` on create and on the credential; the listing showing
`"masked": "…XXXX"` and **no secret anywhere in the body**; the probe reporting
`ok: true` with a real `model_count` and `latency_ms`; the delete reporting its
`dangling_aliases` array.

**Read the listing output rather than only its status.** The one thing that must
not appear is the key, and a status code cannot tell you it did not.

- [ ] **Step 7: Read the request log through the API**

```bash
curl -sS -b /tmp/dr-p7.jar 'localhost:18081/api/requests?limit=3' | python3 -m json.tool | head -30
ID=$(curl -sS -b /tmp/dr-p7.jar 'localhost:18081/api/requests?limit=1' |
  python3 -c 'import json,sys; rs=json.load(sys.stdin)["requests"]; print(rs[0]["id"] if rs else "")')
echo "trace id: $ID"
[ -n "$ID" ] && curl -sS -b /tmp/dr-p7.jar "localhost:18081/api/requests/$ID" |
  python3 -m json.tool | head -40
```

Expected: a page with a `next_cursor`, and a trace carrying `candidates`,
`skips`, `attempts`, `warnings` and a `bodies` array that is **empty rather than
null** — `capture.bodies` has no writer and the drawer has to range over it.

If the log is empty, send a chat request through the proxy first and re-run.

- [ ] **Step 8: Stop and confirm nothing is left running**

```bash
kill "$(ps -C darkrouter-p7 -o pid= | head -1)" 2>/dev/null
sleep 1
ps -C darkrouter-p7 -o pid= || echo "stopped"
ss -ltnp 2>/dev/null | grep -E ':1808[01]' || echo "ports free"
rm -rf /tmp/dr-p7-data /tmp/dr-p7.yaml /tmp/darkrouter-p7 /tmp/dr-p7.jar /tmp/dr-p7.hdr
```

Expected: no process, no listener on 18080 or 18081.

- [ ] **Step 9: Record the result**

Add a numbered section to `docs/PROGRESS.md`'s "Open items" with the real
numbers: the status codes from Steps 2 and 4, the probe's model count and
latency, and the masked-credential output with the mask shown. State plainly
that the frontend is not built yet and that the API was exercised with `curl`.

- [ ] **Step 10: Commit**

```bash
git add docs/PROGRESS.md
git commit -m "docs: record the phase 7 api verification"
```

---

# Part B — The dashboard

Tasks 19 to 31. Part A is complete and usable before any of this starts.

## What the scaffold actually produces

Verified on 2026-08-23 by running the real generator into a temp directory.
**Three of these contradict spec §5**, and finding them at plan time rather than
at Task 19 is why the probe was worth running.

- The command is non-interactive with flags:
  `npx create-darkraise-ui@6.4.0 web --layout sidebar --preset default --surface slate --yes`.
  Without them it prompts and hangs a scripted run.
- **It initializes a git repository inside `web/`** and makes a commit. That has
  to be removed before anything is added to the outer repository, or `web/`
  becomes an unwanted submodule-shaped hole.
- **It does not install TanStack Router, Query, Table or Form, and it does not
  install recharts.** Spec §5 says the scaffold gives them; it gives
  `react`, `react-dom` and `darkraise-ui` and nothing else. `recharts` is a
  *peer* dependency of `darkraise-ui`, so its chart component fails at runtime
  until it is installed explicitly. Task 19 installs all five.
- **`darkraise-ui/router` is a router adapter interface, not a router.** It
  declares `Link`, `useNavigate`, `usePathname`, `useBack` and `useInvalidate`
  and expects a concrete router plugged into it. TanStack Router is that router.
- `.gitignore` ignores `dist`, which **breaks `go:embed all:web/dist` on a fresh
  clone**: the directory does not exist, and `go:embed` fails at compile time
  rather than at runtime. Task 20 handles it.
- Layout: `index.html`, `vite.config.ts` (dev server on 5173, `@` aliased to
  `./src`), `tsconfig.json`, `src/main.tsx`, `src/app.tsx`, `src/theme.config.ts`,
  `src/styles/globals.css`.
- `npm run build` is `vite build && tsc --noEmit`, so a type error fails the
  build. That is what makes the build test in Task 29 meaningful.
- Every component spec §6 names exists: `table`, `drawer`, `card`, `stat`,
  `chart`, `command`, `json-tree-view`, plus `badge`, `button`, `dialog`,
  `empty-state`, `input`, `select`, `sheet`, `skeleton` and `tabs`.

---

### Task 19: Scaffold the SPA

**Files:**
- Create: `web/` (generated), `web/.gitignore` (modified)
- Modify: `.gitignore` (repository root, if it ignores `web/`)

**Interfaces:**
- Consumes: nothing.
- Produces: a building SPA skeleton at `web/`, with every dependency the later tasks import.

- [ ] **Step 1: Generate**

```bash
cd /root/repositories/darkrouter
npx --yes create-darkraise-ui@6.4.0 web --layout sidebar --preset default --surface slate --yes
```

Expected: `web/` with `index.html`, `package.json`, `vite.config.ts`,
`tsconfig.json` and `src/`.

- [ ] **Step 2: Remove the nested git repository**

The generator runs `git init` and commits inside `web/`. Left in place, the outer
repository sees `web/` as a gitlink and stores a pointer instead of the files.

```bash
rm -rf web/.git
git status --short web/ | head -5
```

Expected: `web/` listed as untracked **files**, not as a single `web/` entry.
A single entry means the nested repository is still there.

- [ ] **Step 3: Install what the scaffold does not**

```bash
npm --prefix web install \
  @tanstack/react-router @tanstack/react-query @tanstack/react-table recharts
npm --prefix web install
node -e "const p=require('./web/package.json'); console.log(Object.keys(p.dependencies).join(' '))"
```

Expected: `darkraise-ui react react-dom @tanstack/react-router @tanstack/react-query @tanstack/react-table recharts`.

`recharts` matters more than it looks: it is a **peer** dependency of
`darkraise-ui`, so `darkraise-ui/components/chart` compiles without it and fails
at runtime. The overview's sparkline is the first thing that would break.

- [ ] **Step 4: Verify the skeleton builds**

```bash
npm --prefix web run build
ls -la web/dist/
```

Expected: a successful build and `web/dist/index.html` plus an `assets/`
directory. `npm run build` is `vite build && tsc --noEmit`, so this also proves
the TypeScript configuration is sound before a single line is written.

- [ ] **Step 5: Commit**

```bash
git add web/ .gitignore
git commit -m "feat(web): scaffold the dashboard"
```

`web/.gitignore` already excludes `node_modules` and `dist`, so neither is
staged. Confirm with `git status --short` before committing — a staged
`node_modules` is thousands of files.

---

### Task 20: Embed and serve the SPA

**Files:**
- Create: `internal/admin/spa.go`, `web/dist/.gitkeep`
- Modify: `web/.gitignore`, `internal/admin/admin.go`
- Test: `internal/admin/spa_test.go`

**Interfaces:**
- Consumes: `Deps.Dev`.
- Produces: SPA serving from the binary and the dev-mode Vite reverse proxy.

Master design §12 and spec §5: built to `web/dist`, embedded with
`go:embed all:web/dist`, served with no working-directory dependency; in dev mode
the admin server reverse-proxies unmatched paths to Vite so hot reload works
against the real API.

**`go:embed` fails at compile time when `web/dist` does not exist**, and
`web/.gitignore` ignores `dist`. So a fresh clone would not build at all — not
"the dashboard is missing", but `go build ./...` failing. A committed
`web/dist/.gitkeep` with a matching un-ignore fixes it, and `all:` is required
in the embed pattern or the dot-file is skipped and the directory is empty,
which is the same compile error again.

**Unmatched paths serve `index.html`, not 404.** A single-page app owns its
routes: a browser reloading on `/requests/01ABC` asks the server for that path,
and a 404 there breaks every deep link and every refresh.

**`/api/*` is never served by the SPA fallback.** An unknown API path must 404 as
an API path, or a typo in the SPA's fetch returns HTML and the client reports a
JSON parse error instead of the missing route.

- [ ] **Step 1: Write the failing test**

Create `internal/admin/spa_test.go`:

```go
package admin

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTheSPAIsServedFromTheBinary(t *testing.T) {
	s, _ := testServer(t)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("content-type = %q", ct)
	}
}

func TestADeepLinkServesTheIndex(t *testing.T) {
	// A single-page app owns its routes. A browser reloading on
	// /requests/01ABC asks the server for that path, and a 404 there breaks
	// every deep link and every refresh.
	s, _ := testServer(t)
	for _, path := range []string{"/requests", "/requests/01ABC", "/settings", "/catalog"} {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != http.StatusOK {
			t.Errorf("%s status = %d, want 200", path, w.Code)
		}
	}
}

func TestAnUnknownAPIPathIsNotTheSPA(t *testing.T) {
	// A typo in the SPA's fetch must return a JSON 404, not HTML. Otherwise
	// the client reports a parse error and the real problem stays hidden.
	s, _ := testServer(t)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/does-not-exist", nil))
	if w.Code == http.StatusOK {
		t.Fatalf("an unknown API path returned 200:\n%s", w.Body.String())
	}
	if strings.Contains(w.Body.String(), "<!doctype") ||
		strings.Contains(w.Body.String(), "<html") {
		t.Errorf("an unknown API path returned HTML:\n%s", w.Body.String())
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./internal/admin/ -run 'SPA|DeepLink|UnknownAPI' -v
```

Expected: 404 on `/` — nothing serves it.

- [ ] **Step 3: Make `web/dist` survive a clean clone**

```bash
mkdir -p web/dist
touch web/dist/.gitkeep
printf 'node_modules\ndist/*\n!dist/.gitkeep\n*.local\n.env\n.env.*\n!.env.example\n' > web/.gitignore
git add -f web/dist/.gitkeep web/.gitignore
git status --short web/ | head
```

Expected: `web/dist/.gitkeep` staged and nothing under `web/dist/assets`.
Without this the embed below fails to compile on a fresh clone, which is a build
failure rather than a missing feature.

- [ ] **Step 4: Write the server**

Create `internal/admin/spa.go`:

```go
package admin

import (
	"embed"
	"io/fs"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
)

// distFS is the built SPA.
//
// "all:" is required, not decorative: without it go:embed skips files beginning
// with a dot, and web/dist holds only .gitkeep on a fresh clone — an empty
// pattern is a compile error, so the repository would not build at all.
//
//go:embed all:web/dist
var distFS embed.FS

// spaHandler serves the built SPA, falling back to index.html.
//
// The fallback is what makes deep links work: a browser reloading on
// /requests/01ABC asks the server for that path, and the router that knows what
// it means only exists once index.html has loaded.
func (s *Server) spaHandler() http.Handler {
	sub, err := fs.Sub(distFS, "web/dist")
	if err != nil {
		// Unreachable: the embed is checked at compile time. Serving a plain
		// message beats a nil handler panicking on the first request.
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "the dashboard was not built into this binary", http.StatusNotFound)
		})
	}
	files := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// A real file wins: assets, the favicon, index.html itself.
		if f, err := sub.Open(strings.TrimPrefix(r.URL.Path, "/")); err == nil {
			_ = f.Close()
			if r.URL.Path != "/" {
				files.ServeHTTP(w, r)
				return
			}
		}
		index, err := sub.Open("index.html")
		if err != nil {
			http.Error(w, "the dashboard was not built into this binary", http.StatusNotFound)
			return
		}
		defer index.Close()
		st, err := index.Stat()
		if err != nil {
			http.Error(w, "the dashboard is unreadable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		http.ServeContent(w, r, "index.html", st.ModTime(), index.(interface {
			Seek(int64, int) (int64, error)
			Read([]byte) (int, error)
		}))
	})
}

// devProxy forwards unmatched paths to the Vite dev server so hot reload works
// against the real API rather than a mock. Spec §5.
func devProxy(target string) (http.Handler, error) {
	u, err := url.Parse(target)
	if err != nil {
		return nil, err
	}
	return httputil.NewSingleHostReverseProxy(u), nil
}
```

`http.ServeContent` needs an `io.ReadSeeker` and `fs.File` is not one. Rather
than the type assertion above, read the index into memory once at construction —
it is a few kilobytes and never changes for the life of the process — and serve
it with `bytes.NewReader`. **Write it that way**; the assertion is shown to make
the requirement visible, not to be kept.

- [ ] **Step 5: Mount it last in `routes`**

At the end of `routes()`:

```go
	// Registered last and at the root so every exact API path above wins.
	// http.ServeMux prefers the longest matching pattern, so "/api/..." never
	// falls through to here — which is what keeps an unknown API path a JSON
	// 404 rather than HTML.
	s.mux.HandleFunc("GET /api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "no such endpoint")
	})
	if s.deps.Dev != "" {
		proxy, err := devProxy(s.deps.Dev)
		if err == nil {
			s.mux.Handle("/", proxy)
			return
		}
	}
	s.mux.Handle("/", s.spaHandler())
```

The `GET /api/` catch-all is what `TestAnUnknownAPIPathIsNotTheSPA` pins.
Register a `POST /api/` twin too, or a mistyped mutating path returns HTML.

- [ ] **Step 6: Run the tests to verify they pass**

```bash
export PATH=$PATH:/usr/local/go/bin
npm --prefix web run build
go test ./internal/admin/ -race -count=1 -v 2>&1 | grep -E '^(--- |ok|FAIL)'
```

Expected: PASS. The `npm run build` is required first — the tests serve the real
embedded files, and an empty `dist` serves a `.gitkeep`.

- [ ] **Step 7: Verify the binary carries the SPA with no working directory**

```bash
export PATH=$PATH:/usr/local/go/bin
CGO_ENABLED=0 go build -o /tmp/dr-spa ./cmd/darkrouter
cd /tmp && DARKROUTER_MASTER_KEY=x /tmp/dr-spa -config /root/repositories/darkrouter/darkrouter.example.yaml -db /tmp/spa.db >/tmp/spa.log 2>&1 &
sleep 3
curl -sS -o /dev/null -w 'index from /tmp: %{http_code} %{content_type}\n' localhost:8081/
kill "$(ps -C dr-spa -o pid= | head -1)" 2>/dev/null; sleep 1
rm -f /tmp/dr-spa /tmp/spa.db /tmp/spa.log
cd /root/repositories/darkrouter
```

Expected: `200 text/html` while running from `/tmp`. That is the whole point of
embedding — the binary must not need its source tree.

**This step binds 8081, which belongs to an unrelated application.** Edit a copy
of the config to 18081 first, exactly as every other smoke step in this plan
does, and use that.

- [ ] **Step 8: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`, vet silent, `gofmt -l .` prints nothing.

- [ ] **Step 9: Commit**

```bash
git add internal/admin/spa.go internal/admin/spa_test.go internal/admin/admin.go \
        web/.gitignore web/dist/.gitkeep
git commit -m "feat(admin): embed and serve the dashboard"
```

---

### Task 21: The app shell — API client, router, query, and the login gate

**Files:**
- Create: `web/src/lib/api.ts`, `web/src/lib/router.tsx`, `web/src/routes/login.tsx`
- Modify: `web/src/app.tsx`, `web/src/main.tsx`

**Interfaces:**
- Consumes: Part A's `/api/auth/*`.
- Produces: `api.get`, `api.post`, `api.del`, `api.patch`, the `useAuth` hook, and a shell that renders the login screen until a session exists. Every screen task builds inside it.

Spec §5: server state is TanStack Query throughout, no client store mirrors
server data. Polling rather than websockets, at stated intervals so "near real
time" is testable.

**The CSRF token lives in one module-level variable, not in React state.** It is
not server data being rendered, it is a header every mutation needs, and putting
it in a context means every mutation hook has to reach for it. `login` and
`auth/status` both return it; the client stores whichever came last.

**A 401 from any call clears the session and shows the login screen.** The
alternative is a dashboard that renders empty tables after the session expires,
which reads as "everything is broken" rather than "log in again".

**`EventSource` is not used for the playground.** It cannot set headers, and
spec §4 requires the CSRF header on `/api/playground`. The fetch-with-header
pattern reading a `ReadableStream` is what the client uses instead.

- [ ] **Step 1: Write the API client**

Create `web/src/lib/api.ts`:

```ts
// The one place a request leaves the SPA.
//
// Every mutation carries the CSRF header spec §3 requires, and every response
// funnels through one 401 handler — a dashboard that renders empty tables after
// a session expires reads as "everything is broken" rather than "log in again".

let csrfToken = ""

export function setCsrfToken(t: string) {
  csrfToken = t
}

export function getCsrfToken() {
  return csrfToken
}

/** Listeners notified when the server says the session is gone. */
const unauthorizedListeners = new Set<() => void>()

export function onUnauthorized(fn: () => void) {
  unauthorizedListeners.add(fn)
  return () => unauthorizedListeners.delete(fn)
}

export class ApiError extends Error {
  constructor(
    readonly status: number,
    message: string,
  ) {
    super(message)
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = {}
  if (body !== undefined) headers["Content-Type"] = "application/json"
  if (method !== "GET") {
    // Spec §3: bound to the session by HMAC, so a token from another session
    // is worthless and one the client never received cannot be guessed.
    headers["X-CSRF-Token"] = csrfToken
  }

  const res = await fetch(path, {
    method,
    headers,
    body: body === undefined ? undefined : JSON.stringify(body),
    // Same-origin: the SPA is served by the API's own port, so the cookie
    // travels without any cross-origin machinery.
    credentials: "same-origin",
  })

  if (res.status === 401) {
    csrfToken = ""
    unauthorizedListeners.forEach((fn) => fn())
    throw new ApiError(401, "not authenticated")
  }
  if (!res.ok) {
    let message = res.statusText
    try {
      const parsed = (await res.json()) as { error?: string }
      if (parsed.error) message = parsed.error
    } catch {
      // A non-JSON error body means something upstream of the API answered.
      // The status line is all there is to report.
    }
    throw new ApiError(res.status, message)
  }
  if (res.status === 204) return undefined as T
  return (await res.json()) as T
}

export const api = {
  get: <T>(path: string) => request<T>("GET", path),
  post: <T>(path: string, body?: unknown) => request<T>("POST", path, body),
  patch: <T>(path: string, body?: unknown) => request<T>("PATCH", path, body),
  del: <T>(path: string) => request<T>("DELETE", path),
}

/**
 * stream sends a mutating request and yields the response body in chunks.
 *
 * EventSource cannot set headers and spec §4 requires the CSRF header on
 * /api/playground, so the playground reads a ReadableStream instead.
 */
export async function* stream(
  path: string,
  body: unknown,
): AsyncGenerator<string, void, unknown> {
  const res = await fetch(path, {
    method: "POST",
    headers: { "Content-Type": "application/json", "X-CSRF-Token": csrfToken },
    body: JSON.stringify(body),
    credentials: "same-origin",
  })
  if (res.status === 401) {
    csrfToken = ""
    unauthorizedListeners.forEach((fn) => fn())
    throw new ApiError(401, "not authenticated")
  }
  if (!res.ok || !res.body) {
    throw new ApiError(res.status, res.statusText)
  }
  const reader = res.body.getReader()
  const decoder = new TextDecoder()
  for (;;) {
    const { done, value } = await reader.read()
    if (done) return
    yield decoder.decode(value, { stream: true })
  }
}

/** Poll intervals, spec §5. Stated here so "near real time" is one edit. */
export const POLL = {
  /** The overview and the requests first page. */
  fast: 3000,
  /** The catalog and usage: they change on a discovery sweep, not per request. */
  slow: 30000,
} as const
```

- [ ] **Step 2: Wire the router, query client and auth gate**

Create `web/src/lib/router.tsx`:

```tsx
// darkraise-ui/router is an adapter interface, not a router: it declares Link,
// useNavigate, usePathname, useBack and useInvalidate and expects a concrete
// router plugged in. This is that plug for TanStack Router.
import { Link, useNavigate, useRouterState } from "@tanstack/react-router"
import { useQueryClient } from "@tanstack/react-query"
import type { ReactNode, MouseEvent, CSSProperties } from "react"

export const routerAdapter = {
  Link: ({
    to,
    children,
    className,
    activeClassName,
    style,
    onClick,
  }: {
    to: string
    children: ReactNode
    className?: string
    activeClassName?: string
    style?: CSSProperties
    onClick?: (e: MouseEvent<HTMLAnchorElement>) => void
  }) => (
    <Link
      to={to}
      className={className}
      activeProps={activeClassName ? { className: activeClassName } : undefined}
      style={style}
      onClick={onClick}
    >
      {children}
    </Link>
  ),
  useNavigate: () => {
    const navigate = useNavigate()
    return (to: string) => navigate({ to })
  },
  usePathname: () => useRouterState({ select: (s) => s.location.pathname }),
  useBack: () => () => window.history.back(),
  useInvalidate: () => {
    const qc = useQueryClient()
    // Invalidate rather than refetch: a screen the operator is not looking at
    // should not fetch just because another one mutated.
    return () => void qc.invalidateQueries()
  },
}
```

Replace `web/src/app.tsx`:

```tsx
import { useEffect, useState } from "react"
import { QueryClient, QueryClientProvider, useQuery } from "@tanstack/react-query"
import { ThemeProvider } from "darkraise-ui/theme"
import { SidebarLayout } from "darkraise-ui/layout"
import { themeConfig } from "./theme.config"
import { api, onUnauthorized, setCsrfToken } from "./lib/api"
import { LoginScreen } from "./routes/login"

const queryClient = new QueryClient({
  defaultOptions: {
    queries: {
      // Spec §5: polling, paused when the tab is hidden. A dashboard nobody is
      // looking at should not keep a gateway busy.
      refetchOnWindowFocus: true,
      refetchIntervalInBackground: false,
      retry: (count, err) =>
        // A 401 is not a transient failure. Retrying it three times delays the
        // login screen for no reason.
        (err as { status?: number }).status !== 401 && count < 2,
    },
  },
})

type AuthStatus = { authenticated: boolean; csrf_token?: string }

function Shell() {
  const [authed, setAuthed] = useState<boolean | null>(null)

  const status = useQuery({
    queryKey: ["auth-status"],
    queryFn: () => api.get<AuthStatus>("/api/auth/status"),
  })

  useEffect(() => {
    if (!status.data) return
    setAuthed(status.data.authenticated)
    if (status.data.csrf_token) setCsrfToken(status.data.csrf_token)
  }, [status.data])

  useEffect(
    () =>
      onUnauthorized(() => {
        // Any call anywhere can discover the session is gone. One listener
        // beats every screen handling it.
        setAuthed(false)
        queryClient.clear()
      }),
    [],
  )

  if (authed === null) return null
  if (!authed) {
    return <LoginScreen onAuthenticated={() => void status.refetch()} />
  }
  return (
    <SidebarLayout
      nav={[
        { label: "Overview", href: "/" },
        { label: "Requests", href: "/requests" },
        { label: "Catalog", href: "/catalog" },
        { label: "Playground", href: "/playground" },
        { label: "Settings", href: "/settings" },
      ]}
      showThemeSwitcher
    >
      {/* Task 22 onward mount the routed screens here. */}
    </SidebarLayout>
  )
}

export function App() {
  return (
    <ThemeProvider config={themeConfig}>
      <QueryClientProvider client={queryClient}>
        <Shell />
      </QueryClientProvider>
    </ThemeProvider>
  )
}
```

- [ ] **Step 3: Write the login screen**

Create `web/src/routes/login.tsx`:

```tsx
import { useState } from "react"
import { Button } from "darkraise-ui/components/button"
import { Card } from "darkraise-ui/components/card"
import { Input } from "darkraise-ui/components/input"
import { api, setCsrfToken } from "../lib/api"

export function LoginScreen({ onAuthenticated }: { onAuthenticated: () => void }) {
  const [password, setPassword] = useState("")
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)

  async function submit(e: React.FormEvent) {
    e.preventDefault()
    setBusy(true)
    setError("")
    try {
      const res = await api.post<{ authenticated: boolean; csrf_token: string }>(
        "/api/auth/login",
        { password },
      )
      setCsrfToken(res.csrf_token)
      onAuthenticated()
    } catch (err) {
      // One message for a wrong password and an unconfigured hash, matching the
      // server: an operator reading "no password is set" learns the port is
      // open.
      setError((err as Error).message || "login failed")
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center p-6">
      <Card className="w-full max-w-sm p-6">
        <form onSubmit={submit} className="flex flex-col gap-4">
          <h1 className="text-xl font-medium">Darkrouter</h1>
          <Input
            type="password"
            autoFocus
            autoComplete="current-password"
            placeholder="Admin password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
          />
          {error ? <p className="text-sm text-destructive">{error}</p> : null}
          <Button type="submit" disabled={busy || password === ""}>
            {busy ? "Signing in…" : "Sign in"}
          </Button>
        </form>
      </Card>
    </div>
  )
}
```

- [ ] **Step 4: Verify it builds and type-checks**

```bash
npm --prefix web run build
```

Expected: a clean build. `npm run build` is `vite build && tsc --noEmit`, so a
wrong prop name or a missing export fails here rather than in a browser.

**`darkraise-ui`'s component import paths and prop names are assumed above.**
`Button`, `Card` and `Input` exist under `darkraise-ui/components/*`; whether
they are default or named exports, and whether `Card` takes `className`, is
what this build tells you. Fix the imports against the real `.d.ts` files in
`web/node_modules/darkraise-ui/dist/components/` rather than guessing twice.

- [ ] **Step 5: Verify the Go suite still passes**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`. The embedded `dist` changed, which is why this runs.

- [ ] **Step 6: Commit**

```bash
git add web/src/lib/api.ts web/src/lib/router.tsx web/src/routes/login.tsx \
        web/src/app.tsx web/package.json web/package-lock.json
git commit -m "feat(web): add the api client and login gate"
```

---

### Task 22: The overview screen

**Files:**
- Create: `web/src/routes/overview.tsx`
- Modify: `web/src/app.tsx`

**Interfaces:**
- Consumes: `GET /api/overview`, `GET /api/usage`.
- Produces: the health grid, the error-rate sparkline, requests per minute, today's spend.

Spec §6: "A provider health grid — one tile per provider with state, active
cooldowns, and credential count — over an error-rate sparkline, requests per
minute, and today's spend."

**The tile renders the state the API computed, it does not compute one.** Task 15
already folds forty cooling triples into one degraded provider; a second
computation in the browser would be a second place for that rule to drift.

**"Needs reconnection" is visually distinct from "degraded".** Spec §6 calls it
out because it is the one state only the operator can fix — everything else
either recovers or is the provider's problem.

**Today's spend renders "not priced" rather than a zero** when the API says
pricing is not wired. A confident `$0.00` on a day that cost money is worse than
an honest gap.

- [ ] **Step 1: Write the screen**

Create `web/src/routes/overview.tsx`:

```tsx
import { useQuery } from "@tanstack/react-query"
import { Badge } from "darkraise-ui/components/badge"
import { Card } from "darkraise-ui/components/card"
import { Stat } from "darkraise-ui/components/stat"
import { api, POLL } from "../lib/api"

type Tile = {
  id: string
  name: string
  state: "healthy" | "degraded" | "disabled" | "unconfigured"
  cooling: number
  credentials: number
  enabled: boolean
  needs_reauth: boolean
}

type Overview = {
  providers: Tile[]
  requests_per_min: number
  error_rate: number
  window_sec: number
  today_spend: { micros: number; priced: boolean }
}

const stateTone: Record<Tile["state"], "success" | "warning" | "danger" | "muted"> = {
  healthy: "success",
  degraded: "warning",
  disabled: "muted",
  unconfigured: "danger",
}

export function OverviewScreen() {
  const overview = useQuery({
    queryKey: ["overview"],
    queryFn: () => api.get<Overview>("/api/overview"),
    // Spec §5: 3s for the overview, so a provider entering or leaving cooldown
    // shows up within one interval.
    refetchInterval: POLL.fast,
  })

  if (!overview.data) return null
  const o = overview.data

  return (
    <div className="flex flex-col gap-6 p-6">
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-3">
        <Stat
          label="Requests / min"
          value={o.requests_per_min.toFixed(1)}
          hint={`over the last ${Math.round(o.window_sec / 60)} min`}
        />
        <Stat
          label="Error rate"
          value={`${(o.error_rate * 100).toFixed(1)}%`}
          tone={o.error_rate > 0.1 ? "danger" : undefined}
        />
        <Stat
          label="Today's spend"
          // An honest gap beats a confident $0.00 on a day that cost money.
          // Nothing computes cost yet, and the API says so rather than
          // reporting a zero.
          value={o.today_spend.priced ? `$${(o.today_spend.micros / 1e6).toFixed(2)}` : "—"}
          hint={o.today_spend.priced ? undefined : "pricing is not wired yet"}
        />
      </div>

      <div>
        <h2 className="mb-3 text-sm font-medium text-muted-foreground">Providers</h2>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2 lg:grid-cols-3">
          {o.providers.map((p) => (
            <Card key={p.id} className="flex flex-col gap-2 p-4">
              <div className="flex items-center justify-between">
                <span className="font-medium">{p.name || p.id}</span>
                {/* The state the API computed. Recomputing it here would be a
                    second place for the fold-forty-triples rule to drift. */}
                <Badge tone={stateTone[p.state]}>{p.state}</Badge>
              </div>
              <div className="text-sm text-muted-foreground">
                {p.credentials} credential{p.credentials === 1 ? "" : "s"}
                {p.cooling > 0 ? ` · ${p.cooling} cooling` : null}
              </div>
              {p.needs_reauth ? (
                // The one state only the operator can fix, so it is called out
                // rather than folded into "degraded".
                <Badge tone="warning">needs reconnection</Badge>
              ) : null}
            </Card>
          ))}
          {o.providers.length === 0 ? (
            <Card className="p-4 text-sm text-muted-foreground">
              No providers configured yet. Add one from Settings.
            </Card>
          ) : null}
        </div>
      </div>
    </div>
  )
}
```

- [ ] **Step 2: Mount it**

In `web/src/app.tsx`, render `<OverviewScreen />` inside the `SidebarLayout`
until Task 23 introduces routing. Import it at the top.

- [ ] **Step 3: Verify it builds**

```bash
npm --prefix web run build
```

Expected: a clean build. `Stat`'s `label`, `value`, `hint` and `tone` props and
`Badge`'s `tone` values are assumed; read
`web/node_modules/darkraise-ui/dist/components/stat.d.ts` and `badge.d.ts` and
correct them rather than guessing a second time.

- [ ] **Step 4: Verify the Go suite still passes**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`.

- [ ] **Step 5: Commit**

```bash
git add web/src/routes/overview.tsx web/src/app.tsx
git commit -m "feat(web): add the overview screen"
```

---

### Task 23: The requests screen and the trace drawer

**Files:**
- Create: `web/src/routes/requests.tsx`, `web/src/components/trace-drawer.tsx`
- Modify: `web/src/app.tsx`

**Interfaces:**
- Consumes: `GET /api/requests`, `GET /api/requests/:id`.
- Produces: the filterable keyset table and the drawer.

Spec §6: "The candidate list is what makes this screen worth building. A failover
that took three tries must read as three labelled rows with reasons, and the four
candidates that were never tried must say why."

That sentence is the acceptance criterion for the whole screen, so the drawer
renders three separate sections and never collapses them: **attempts** in order,
**candidates** the router produced, and **skips** with reasons. A drawer showing
only attempts explains what happened; all three explain why.

**Paging appends rather than replaces.** The operator is scrolling a log; a
"next page" that swaps the table loses their place and makes the keyset cursor
pointless.

**A filter change resets the cursor.** Task 11's cursor is rejected under
different filters, so keeping it would turn every filter change into a 400. The
reset is what makes that rejection invisible in normal use.

- [ ] **Step 1: Write the drawer**

Create `web/src/components/trace-drawer.tsx`:

```tsx
import { useQuery } from "@tanstack/react-query"
import { Badge } from "darkraise-ui/components/badge"
import { Drawer } from "darkraise-ui/components/drawer"
import { JsonTreeView } from "darkraise-ui/components/json-tree-view"
import { Table } from "darkraise-ui/components/table"
import { api } from "../lib/api"

type Attempt = {
  seq: number
  provider: string
  key_label: string
  model: string
  outcome: string
  status_code: number
  latency_ms: number
  error?: string
}

type Trace = {
  id: string
  ts_ms: number
  dialect: string
  surface: string
  model: string
  alias?: string
  provider?: string
  final_model?: string
  status: string
  error_code?: string
  tokens_in: number
  tokens_out: number
  cost_micros: number | null
  ttft_ms: number | null
  total_ms: number | null
  candidates: string[]
  skips: string[]
  attempts: Attempt[]
  warnings: string[]
  surface_meta: Record<string, unknown>
  response_bytes: number
  response_content_type: string
  bodies: { kind: string; bytes: number; content: string; truncated: boolean }[]
}

export function TraceDrawer({ id, onClose }: { id: string | null; onClose: () => void }) {
  const trace = useQuery({
    queryKey: ["trace", id],
    queryFn: () => api.get<Trace>(`/api/requests/${id}`),
    enabled: id !== null,
  })

  const t = trace.data
  return (
    <Drawer open={id !== null} onOpenChange={(open) => (open ? null : onClose())}>
      {!t ? null : (
        <div className="flex flex-col gap-6 p-6">
          <header className="flex flex-col gap-1">
            <div className="flex items-center gap-2">
              <Badge tone={t.status === "success" ? "success" : "danger"}>{t.status}</Badge>
              <span className="font-mono text-xs text-muted-foreground">{t.id}</span>
            </div>
            <div className="text-sm text-muted-foreground">
              {t.dialect} · {t.surface} · {t.alias ? `${t.alias} → ` : ""}
              {t.provider ? `${t.provider}/${t.final_model}` : t.model}
            </div>
          </header>

          <section>
            <h3 className="mb-2 text-sm font-medium">Attempts</h3>
            {/* In order, one row each. A failover that took three tries reads
                as three labelled rows, which is spec §6's criterion. */}
            <Table>
              <thead>
                <tr>
                  <th>#</th><th>Provider</th><th>Key</th><th>Model</th>
                  <th>Outcome</th><th>Status</th><th>Latency</th>
                </tr>
              </thead>
              <tbody>
                {t.attempts.map((a) => (
                  <tr key={a.seq}>
                    <td>{a.seq}</td>
                    <td>{a.provider}</td>
                    <td>{a.key_label || "—"}</td>
                    <td>{a.model}</td>
                    <td>
                      <Badge tone={a.outcome === "success" ? "success" : "warning"}>
                        {a.outcome}
                      </Badge>
                      {a.error ? (
                        <div className="text-xs text-muted-foreground">{a.error}</div>
                      ) : null}
                    </td>
                    <td>{a.status_code || "—"}</td>
                    <td>{a.latency_ms} ms</td>
                  </tr>
                ))}
              </tbody>
            </Table>
          </section>

          <section className="grid grid-cols-1 gap-4 sm:grid-cols-2">
            <div>
              <h3 className="mb-2 text-sm font-medium">Candidates</h3>
              {/* What the router produced, in order. Separate from attempts:
                  a candidate that was never tried is not an attempt. */}
              <ul className="flex flex-col gap-1 text-sm">
                {t.candidates.map((c) => (
                  <li key={c} className="font-mono text-xs">{c}</li>
                ))}
                {t.candidates.length === 0 ? (
                  <li className="text-muted-foreground">none</li>
                ) : null}
              </ul>
            </div>
            <div>
              <h3 className="mb-2 text-sm font-medium">Skipped</h3>
              {/* Spec §6: "the four candidates that were never tried must say
                  why". Each entry is target:reason from the router's trace. */}
              <ul className="flex flex-col gap-1 text-sm">
                {t.skips.map((s) => {
                  const [target, reason] = s.split(":")
                  return (
                    <li key={s} className="flex items-center gap-2">
                      <span className="font-mono text-xs">{target}</span>
                      <Badge tone="muted">{reason ?? "skipped"}</Badge>
                    </li>
                  )
                })}
                {t.skips.length === 0 ? (
                  <li className="text-muted-foreground">none</li>
                ) : null}
              </ul>
            </div>
          </section>

          {t.warnings.length > 0 ? (
            <section>
              <h3 className="mb-2 text-sm font-medium">Warnings</h3>
              {/* Phase 4's dropped-field warnings. This is where a vanished
                  cache_control marker becomes visible. */}
              <ul className="flex flex-col gap-1 text-sm text-muted-foreground">
                {t.warnings.map((wn) => <li key={wn}>{wn}</li>)}
              </ul>
            </section>
          ) : null}

          <section className="grid grid-cols-2 gap-4 text-sm sm:grid-cols-4">
            <div><span className="text-muted-foreground">Tokens in</span><div>{t.tokens_in}</div></div>
            <div><span className="text-muted-foreground">Tokens out</span><div>{t.tokens_out}</div></div>
            <div>
              <span className="text-muted-foreground">Cost</span>
              {/* null, not zero: nothing computes cost yet. */}
              <div>{t.cost_micros === null ? "—" : `$${(t.cost_micros / 1e6).toFixed(4)}`}</div>
            </div>
            <div><span className="text-muted-foreground">TTFT</span><div>{t.ttft_ms ?? "—"} ms</div></div>
          </section>

          {t.response_bytes > 0 ? (
            <section className="text-sm">
              <span className="text-muted-foreground">Response</span>
              <div>
                {t.response_bytes} bytes · {t.response_content_type || "unknown type"}
              </div>
            </section>
          ) : null}

          {Object.keys(t.surface_meta ?? {}).length > 0 ? (
            <section>
              <h3 className="mb-2 text-sm font-medium">Surface detail</h3>
              <JsonTreeView data={t.surface_meta} />
            </section>
          ) : null}

          <section>
            <h3 className="mb-2 text-sm font-medium">Bodies</h3>
            {t.bodies.length === 0 ? (
              // capture.bodies has a retention sweep and no writer, so this is
              // always empty today. Saying so beats an empty panel that looks
              // like a rendering bug.
              <p className="text-sm text-muted-foreground">Not captured.</p>
            ) : (
              t.bodies.map((b) => (
                <div key={b.kind} className="mb-3">
                  <div className="text-xs text-muted-foreground">
                    {b.kind} · {b.bytes} bytes{b.truncated ? " · truncated" : ""}
                  </div>
                  <pre className="overflow-x-auto rounded bg-muted p-2 text-xs">{b.content}</pre>
                </div>
              ))
            )}
          </section>
        </div>
      )}
    </Drawer>
  )
}
```

- [ ] **Step 2: Write the table**

Create `web/src/routes/requests.tsx`:

```tsx
import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { Badge } from "darkraise-ui/components/badge"
import { Button } from "darkraise-ui/components/button"
import { Input } from "darkraise-ui/components/input"
import { Table } from "darkraise-ui/components/table"
import { api, POLL } from "../lib/api"
import { TraceDrawer } from "../components/trace-drawer"

type Row = {
  id: string
  ts_ms: number
  dialect: string
  surface: string
  model: string
  alias?: string
  provider?: string
  status: string
  tokens_in: number
  tokens_out: number
  total_ms: number | null
  attempts: number
}

type Page = { requests: Row[]; next_cursor?: string }

type Filters = { provider: string; model: string; status: string; surface: string }

function queryString(f: Filters, cursor: string | null) {
  const p = new URLSearchParams({ limit: "50" })
  if (f.provider) p.set("provider", f.provider)
  if (f.model) p.set("model", f.model)
  if (f.status) p.set("status", f.status)
  if (f.surface) p.set("surface", f.surface)
  if (cursor) p.set("cursor", cursor)
  return p.toString()
}

export function RequestsScreen() {
  const [filters, setFilters] = useState<Filters>({
    provider: "", model: "", status: "", surface: "",
  })
  // Pages accumulate: the operator is scrolling a log, and a "next page" that
  // swapped the table would lose their place and make the cursor pointless.
  const [pages, setPages] = useState<Row[]>([])
  const [cursor, setCursor] = useState<string | null>(null)
  const [selected, setSelected] = useState<string | null>(null)

  const first = useQuery({
    queryKey: ["requests", filters],
    queryFn: () => api.get<Page>(`/api/requests?${queryString(filters, null)}`),
    // Spec §5: 3s on the first page. Later pages are history and do not move.
    refetchInterval: POLL.fast,
  })

  function setFilter(k: keyof Filters, v: string) {
    // The cursor is rejected under different filters, by design. Resetting it
    // here is what keeps that rejection invisible in normal use.
    setFilters((f) => ({ ...f, [k]: v }))
    setPages([])
    setCursor(null)
  }

  async function loadMore() {
    const from = cursor ?? first.data?.next_cursor
    if (!from) return
    const page = await api.get<Page>(`/api/requests?${queryString(filters, from)}`)
    setPages((p) => [...p, ...page.requests])
    setCursor(page.next_cursor ?? null)
  }

  const rows = [...(first.data?.requests ?? []), ...pages]

  return (
    <div className="flex flex-col gap-4 p-6">
      <div className="flex flex-wrap gap-2">
        <Input placeholder="provider" value={filters.provider}
          onChange={(e) => setFilter("provider", e.target.value)} />
        <Input placeholder="model" value={filters.model}
          onChange={(e) => setFilter("model", e.target.value)} />
        <Input placeholder="status" value={filters.status}
          onChange={(e) => setFilter("status", e.target.value)} />
        <Input placeholder="surface" value={filters.surface}
          onChange={(e) => setFilter("surface", e.target.value)} />
      </div>

      <Table>
        <thead>
          <tr>
            <th>Time</th><th>Surface</th><th>Model</th><th>Provider</th>
            <th>Status</th><th>Attempts</th><th>Tokens</th><th>Latency</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => (
            <tr key={r.id} className="cursor-pointer" onClick={() => setSelected(r.id)}>
              <td className="whitespace-nowrap">{new Date(r.ts_ms).toLocaleTimeString()}</td>
              <td>{r.surface}</td>
              <td>{r.alias ? `${r.alias} → ${r.model}` : r.model}</td>
              <td>{r.provider || "—"}</td>
              <td>
                <Badge tone={r.status === "success" ? "success" : "danger"}>{r.status}</Badge>
              </td>
              {/* More than one attempt means a failover, which is the row an
                  operator is usually looking for. */}
              <td>{r.attempts > 1 ? <Badge tone="warning">{r.attempts}</Badge> : r.attempts}</td>
              <td>{r.tokens_in}/{r.tokens_out}</td>
              <td>{r.total_ms ?? "—"} ms</td>
            </tr>
          ))}
        </tbody>
      </Table>

      {rows.length === 0 ? (
        <p className="text-sm text-muted-foreground">No requests match these filters.</p>
      ) : null}

      {(cursor ?? first.data?.next_cursor) ? (
        <Button variant="secondary" onClick={loadMore}>Load more</Button>
      ) : null}

      <TraceDrawer id={selected} onClose={() => setSelected(null)} />
    </div>
  )
}
```

- [ ] **Step 3: Verify it builds**

```bash
npm --prefix web run build
```

Expected: a clean build. `Drawer`'s `open`/`onOpenChange` props, `Table`'s
composition and `JsonTreeView`'s `data` prop are assumed; read the `.d.ts` files
and correct them.

- [ ] **Step 4: Verify the Go suite still passes**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`.

- [ ] **Step 5: Commit**

```bash
git add web/src/routes/requests.tsx web/src/components/trace-drawer.tsx web/src/app.tsx
git commit -m "feat(web): add the requests table and trace drawer"
```

---

### Task 24: The catalog and playground screens

**Files:**
- Create: `web/src/routes/catalog.tsx`, `web/src/routes/playground.tsx`
- Modify: `web/src/app.tsx`

**Interfaces:**
- Consumes: `GET /api/models`, `POST /api/playground`.
- Produces: both screens.

Two screens in one task: neither is large, both are read-mostly, and the
playground's only interesting behavior — streaming with a header — is already
built in Task 21's `stream`.

**The catalog marks inferred metadata visibly.** Master design §6.4 routes a
guessed model with a warning, and an operator who cannot see which rows are
guesses reads a refused tool call as a Darkrouter bug.

**The playground links to the trace it produced.** That link is the point of the
screen: verifying a credential means seeing which provider actually served, and
the header carries the id before the stream starts.

- [ ] **Step 1: Write the catalog**

Create `web/src/routes/catalog.tsx`:

```tsx
import { useState } from "react"
import { useQuery } from "@tanstack/react-query"
import { Badge } from "darkraise-ui/components/badge"
import { Input } from "darkraise-ui/components/input"
import { Table } from "darkraise-ui/components/table"
import { api, POLL } from "../lib/api"

type Model = {
  model: string
  providers: string[]
  surfaces: string[]
  context_window: number
  max_output_tokens: number
  tools: boolean
  vision: boolean
  reasoning: boolean
  inferred: boolean
  state: string
}

type Catalog = {
  models: Model[]
  aliases: { name: string; targets: string[] }[]
}

export function CatalogScreen() {
  const [q, setQ] = useState("")
  const [surface, setSurface] = useState("")

  const cat = useQuery({
    queryKey: ["models", q, surface],
    queryFn: () => {
      const p = new URLSearchParams()
      if (q) p.set("q", q)
      if (surface) p.set("surface", surface)
      return api.get<Catalog>(`/api/models?${p.toString()}`)
    },
    // Spec §5: 30s. The catalog changes on a discovery sweep, not per request.
    refetchInterval: POLL.slow,
  })

  const data = cat.data
  return (
    <div className="flex flex-col gap-6 p-6">
      <div className="flex flex-wrap gap-2">
        <Input placeholder="Search models" value={q} onChange={(e) => setQ(e.target.value)} />
        <Input placeholder="surface" value={surface} onChange={(e) => setSurface(e.target.value)} />
      </div>

      <Table>
        <thead>
          <tr>
            <th>Model</th><th>Providers</th><th>Surfaces</th>
            <th>Context</th><th>Capabilities</th><th>Metadata</th>
          </tr>
        </thead>
        <tbody>
          {(data?.models ?? []).map((m) => (
            <tr key={m.model}>
              <td className="font-mono text-xs">{m.model}</td>
              <td>{m.providers.join(", ")}</td>
              <td>{m.surfaces.join(", ")}</td>
              <td>{m.context_window || "—"}</td>
              <td className="flex gap-1">
                {m.tools ? <Badge tone="muted">tools</Badge> : null}
                {m.vision ? <Badge tone="muted">vision</Badge> : null}
                {m.reasoning ? <Badge tone="muted">reasoning</Badge> : null}
              </td>
              <td>
                {/* Master design §6.4 routes a guessed model with a warning.
                    An operator who cannot see which rows are guesses reads a
                    refused tool call as a Darkrouter bug. */}
                {m.inferred ? <Badge tone="warning">inferred</Badge> : <Badge tone="success">known</Badge>}
              </td>
            </tr>
          ))}
        </tbody>
      </Table>

      <section>
        <h2 className="mb-2 text-sm font-medium text-muted-foreground">Aliases</h2>
        <Table>
          <thead><tr><th>Alias</th><th>Resolves to</th></tr></thead>
          <tbody>
            {(data?.aliases ?? []).map((a) => (
              <tr key={a.name}>
                <td className="font-mono text-xs">{a.name}</td>
                <td className="font-mono text-xs">{a.targets.join(" → ")}</td>
              </tr>
            ))}
          </tbody>
        </Table>
        <p className="mt-2 text-xs text-muted-foreground">
          Aliases are edited in darkrouter.yaml, not here.
        </p>
      </section>
    </div>
  )
}
```

- [ ] **Step 2: Write the playground**

Create `web/src/routes/playground.tsx`:

```tsx
import { useRef, useState } from "react"
import { Button } from "darkraise-ui/components/button"
import { Card } from "darkraise-ui/components/card"
import { Input } from "darkraise-ui/components/input"
import { stream } from "../lib/api"

/** Reads one SSE frame's data field out of a raw chunk buffer. */
function drainSSE(buffer: string): { text: string; rest: string } {
  let text = ""
  let rest = buffer
  for (;;) {
    const i = rest.indexOf("\n\n")
    if (i < 0) break
    const frame = rest.slice(0, i)
    rest = rest.slice(i + 2)
    for (const line of frame.split("\n")) {
      if (!line.startsWith("data: ")) continue
      const payload = line.slice(6)
      if (payload === "[DONE]") continue
      try {
        const obj = JSON.parse(payload)
        const delta = obj?.choices?.[0]?.delta?.content
        if (typeof delta === "string") text += delta
      } catch {
        // A frame that is not JSON is a provider quirk, not a client error.
        // Skipping it beats aborting a stream that is otherwise fine.
      }
    }
  }
  return { text, rest }
}

export function PlaygroundScreen() {
  const [model, setModel] = useState("")
  const [prompt, setPrompt] = useState("")
  const [output, setOutput] = useState("")
  const [requestID, setRequestID] = useState("")
  const [busy, setBusy] = useState(false)
  const buffer = useRef("")

  async function send() {
    setBusy(true)
    setOutput("")
    setRequestID("")
    buffer.current = ""
    try {
      for await (const chunk of stream("/api/playground", { model, prompt })) {
        buffer.current += chunk
        const { text, rest } = drainSSE(buffer.current)
        buffer.current = rest
        if (text) setOutput((o) => o + text)
      }
    } catch (err) {
      setOutput((o) => o + `\n\n[${(err as Error).message}]`)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex flex-col gap-4 p-6">
      <div className="flex flex-wrap gap-2">
        <Input placeholder="alias or provider/model" value={model}
          onChange={(e) => setModel(e.target.value)} />
      </div>
      <Input placeholder="Prompt" value={prompt} onChange={(e) => setPrompt(e.target.value)} />
      <div>
        <Button onClick={send} disabled={busy || !model || !prompt}>
          {busy ? "Streaming…" : "Send"}
        </Button>
      </div>
      <Card className="min-h-40 whitespace-pre-wrap p-4 font-mono text-sm">{output}</Card>
      {requestID ? (
        // Spec §6: "follow a link to the trace it produced". Verifying a
        // credential means seeing which provider actually served.
        <a className="text-sm underline" href={`/requests/${requestID}`}>
          View the trace for this request
        </a>
      ) : null}
    </div>
  )
}
```

`requestID` is never set above. The id arrives in the `X-Darkrouter-Request`
response header, which `stream` discards — **extend `stream` to surface the
response headers** (return them alongside the generator, or take an `onResponse`
callback) and set it there. Without that the trace link never appears, which is
the affordance the screen exists for.

- [ ] **Step 3: Verify it builds**

```bash
npm --prefix web run build
```

Expected: a clean build.

- [ ] **Step 4: Verify the Go suite still passes**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`.

- [ ] **Step 5: Commit**

```bash
git add web/src/routes/catalog.tsx web/src/routes/playground.tsx web/src/lib/api.ts web/src/app.tsx
git commit -m "feat(web): add the catalog and playground screens"
```

---

### Task 25: The settings screen

**Files:**
- Create: `web/src/routes/settings.tsx`
- Modify: `web/src/app.tsx`

**Interfaces:**
- Consumes: `GET/POST /api/providers`, `PATCH/DELETE /api/providers/:id`, `POST/DELETE /api/providers/:id/keys[/:keyId]`, `POST /api/providers/:id/test`, `GET /api/presets`, `GET /api/config`, `POST /api/config/reload`.
- Produces: the last screen. After this the dashboard is complete.

Spec §6: "Provider and credential CRUD, plus the rendered read-only view of
`darkrouter.yaml` with validation status and a reload button. A config failing
validation is shown prominently with the error and a note that the previous
configuration is still serving."

**Deleting a provider shows the aliases it will strand, in the confirm dialog,
before the delete.** Master design §7 makes a dangling alias a warning rather
than an error precisely so this delete cannot brick the reload button — and spec
§6 asks the screen to link the operator to the file that needs editing, because
that edit cannot be made here.

**A failing config is shown with "the previous configuration is still serving".**
An operator seeing a red validation error and no other information assumes the
gateway is down. It is not, and saying so is the difference between a calm fix
and a panicked restart.

**The credential form never shows an existing secret, because there is nothing
to show.** Spec §4.1: replacing a credential means adding a new one and deleting
the old, and the UI is shaped that way rather than pretending to offer an edit.

- [ ] **Step 1: Write the screen**

Create `web/src/routes/settings.tsx`:

```tsx
import { useState } from "react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { Badge } from "darkraise-ui/components/badge"
import { Button } from "darkraise-ui/components/button"
import { Card } from "darkraise-ui/components/card"
import { Dialog } from "darkraise-ui/components/dialog"
import { Input } from "darkraise-ui/components/input"
import { Select } from "darkraise-ui/components/select"
import { api, POLL } from "../lib/api"

type Credential = {
  id: string
  label: string
  masked: string
  enabled: boolean
  cooling: boolean
}

type Provider = {
  id: string
  name: string
  preset: string
  kind: string
  base_url: string
  priority: number
  enabled: boolean
  credentials: Credential[]
}

type Preset = { id: string; name: string; kind: string; base_url: string }

type Config = {
  valid: boolean
  error?: string
  serving?: string
  warnings: string[]
  aliases: Record<string, string[]>
}

export function SettingsScreen() {
  const qc = useQueryClient()
  const providers = useQuery({
    queryKey: ["providers"],
    queryFn: () => api.get<{ providers: Provider[] }>("/api/providers"),
    refetchInterval: POLL.slow,
  })
  const presets = useQuery({
    queryKey: ["presets"],
    queryFn: () => api.get<{ presets: Preset[] }>("/api/presets"),
  })
  const config = useQuery({
    queryKey: ["config"],
    queryFn: () => api.get<Config>("/api/config"),
    refetchInterval: POLL.slow,
  })

  const invalidate = () => void qc.invalidateQueries()

  const [newID, setNewID] = useState("")
  const [newPreset, setNewPreset] = useState("")
  const [deleting, setDeleting] = useState<Provider | null>(null)
  const [probeResult, setProbeResult] = useState<Record<string, string>>({})

  const create = useMutation({
    mutationFn: () => api.post("/api/providers", { id: newID, preset: newPreset }),
    onSuccess: () => { setNewID(""); setNewPreset(""); invalidate() },
  })
  const remove = useMutation({
    mutationFn: (id: string) => api.del<{ dangling_aliases: string[] }>(`/api/providers/${id}`),
    onSuccess: () => { setDeleting(null); invalidate() },
  })
  const probe = useMutation({
    mutationFn: (id: string) =>
      api.post<{ ok: boolean; model_count?: number; latency_ms: number; error?: string }>(
        `/api/providers/${id}/test`,
      ),
  })
  const reload = useMutation({
    mutationFn: () => api.post("/api/config/reload"),
    onSuccess: invalidate,
  })

  return (
    <div className="flex flex-col gap-6 p-6">
      {config.data && !config.data.valid ? (
        <Card className="border-destructive p-4">
          <h2 className="font-medium text-destructive">Configuration is invalid</h2>
          <p className="mt-1 font-mono text-xs">{config.data.error}</p>
          {/* The difference between a calm fix and a panicked restart. */}
          <p className="mt-2 text-sm text-muted-foreground">
            {config.data.serving ?? "The previous configuration is still serving."}
          </p>
        </Card>
      ) : null}

      <section>
        <h2 className="mb-3 text-sm font-medium text-muted-foreground">Providers</h2>
        <div className="flex flex-col gap-3">
          {(providers.data?.providers ?? []).map((p) => (
            <Card key={p.id} className="flex flex-col gap-3 p-4">
              <div className="flex items-center justify-between">
                <div>
                  <div className="font-medium">{p.name}</div>
                  <div className="font-mono text-xs text-muted-foreground">
                    {p.kind} · {p.base_url}
                    {p.preset ? ` · preset ${p.preset}` : " · no preset"}
                  </div>
                </div>
                <div className="flex items-center gap-2">
                  <Button
                    variant="secondary"
                    onClick={() =>
                      probe.mutate(p.id, {
                        onSuccess: (r) =>
                          setProbeResult((s) => ({
                            ...s,
                            [p.id]: r.ok
                              ? `OK · ${r.model_count} models · ${r.latency_ms} ms`
                              : `Failed · ${r.error}`,
                          })),
                      })
                    }
                  >
                    Test
                  </Button>
                  <Button variant="destructive" onClick={() => setDeleting(p)}>Delete</Button>
                </div>
              </div>
              {probeResult[p.id] ? (
                <p className="text-sm text-muted-foreground">{probeResult[p.id]}</p>
              ) : null}

              <div className="flex flex-col gap-1">
                {p.credentials.map((c) => (
                  <div key={c.id} className="flex items-center gap-2 text-sm">
                    <span>{c.label}</span>
                    {/* Never the secret. Spec §4.1: replacing one means adding
                        a new one and deleting the old, so there is nothing to
                        show and no edit to offer. */}
                    <span className="font-mono text-xs text-muted-foreground">{c.masked}</span>
                    {c.cooling ? <Badge tone="warning">cooling</Badge> : null}
                    {!c.enabled ? <Badge tone="warning">needs reconnection</Badge> : null}
                    <Button
                      variant="ghost"
                      onClick={() =>
                        api.del(`/api/providers/${p.id}/keys/${c.id}`).then(invalidate)
                      }
                    >
                      Remove
                    </Button>
                  </div>
                ))}
                <AddCredential providerID={p.id} onAdded={invalidate} />
              </div>
            </Card>
          ))}
        </div>
      </section>

      <section>
        <h2 className="mb-3 text-sm font-medium text-muted-foreground">Add a provider</h2>
        <Card className="flex flex-wrap items-end gap-2 p-4">
          <Input placeholder="id" value={newID} onChange={(e) => setNewID(e.target.value)} />
          <Select value={newPreset} onValueChange={setNewPreset}>
            {(presets.data?.presets ?? []).map((p) => (
              <option key={p.id} value={p.id}>{p.name} ({p.id})</option>
            ))}
          </Select>
          <Button onClick={() => create.mutate()} disabled={!newID || !newPreset}>
            Create
          </Button>
        </Card>
      </section>

      <section>
        <div className="mb-3 flex items-center justify-between">
          <h2 className="text-sm font-medium text-muted-foreground">Configuration</h2>
          <Button variant="secondary" onClick={() => reload.mutate()}>Reload</Button>
        </div>
        <Card className="p-4">
          {(config.data?.warnings ?? []).map((w) => (
            <p key={w} className="mb-1 text-sm text-muted-foreground">{w}</p>
          ))}
          <h3 className="mt-3 text-sm font-medium">Aliases</h3>
          <pre className="mt-1 overflow-x-auto text-xs">
            {JSON.stringify(config.data?.aliases ?? {}, null, 2)}
          </pre>
          <p className="mt-2 text-xs text-muted-foreground">
            Aliases and policy are edited in darkrouter.yaml. This view is read-only.
          </p>
        </Card>
      </section>

      <Dialog open={deleting !== null} onOpenChange={(o) => (o ? null : setDeleting(null))}>
        {deleting ? (
          <div className="flex flex-col gap-3 p-6">
            <h2 className="font-medium">Delete {deleting.name}?</h2>
            <p className="text-sm text-muted-foreground">
              Its {deleting.credentials.length} credential
              {deleting.credentials.length === 1 ? "" : "s"} and its discovered models go with it.
            </p>
            <DanglingWarning providerID={deleting.id} />
            <div className="flex justify-end gap-2">
              <Button variant="secondary" onClick={() => setDeleting(null)}>Cancel</Button>
              <Button variant="destructive" onClick={() => remove.mutate(deleting.id)}>
                Delete
              </Button>
            </div>
          </div>
        ) : null}
      </Dialog>
    </div>
  )
}

/**
 * DanglingWarning names the aliases a delete will strand.
 *
 * Master design §7 makes a dangling alias a warning rather than a validation
 * error precisely so this delete cannot brick the reload button. The operator
 * still has to edit the file, which is why the path is named: that edit cannot
 * be made here.
 */
function DanglingWarning({ providerID }: { providerID: string }) {
  const config = useQuery({
    queryKey: ["config"],
    queryFn: () => api.get<Config>("/api/config"),
  })
  const stranded = Object.entries(config.data?.aliases ?? {})
    .filter(([, targets]) => targets.some((t) => t.split("/")[0] === providerID))
    .map(([name]) => name)

  if (stranded.length === 0) return null
  return (
    <div className="rounded border border-warning p-3 text-sm">
      <p>
        These aliases will point at nothing: <strong>{stranded.join(", ")}</strong>
      </p>
      <p className="mt-1 text-muted-foreground">
        The gateway keeps serving — a dangling alias is a warning, not an error — but
        edit darkrouter.yaml to remove them.
      </p>
    </div>
  )
}

function AddCredential({ providerID, onAdded }: { providerID: string; onAdded: () => void }) {
  const [label, setLabel] = useState("")
  const [secret, setSecret] = useState("")
  return (
    <div className="mt-2 flex flex-wrap items-end gap-2">
      <Input placeholder="label" value={label} onChange={(e) => setLabel(e.target.value)} />
      <Input
        type="password"
        placeholder="secret"
        value={secret}
        onChange={(e) => setSecret(e.target.value)}
      />
      <Button
        variant="secondary"
        disabled={!secret}
        onClick={() =>
          api
            .post(`/api/providers/${providerID}/keys`, { label, secret })
            // Cleared immediately: the value has left the browser and keeping
            // it in a controlled input serves nothing.
            .then(() => { setLabel(""); setSecret(""); onAdded() })
        }
      >
        Add key
      </Button>
    </div>
  )
}
```

- [ ] **Step 2: Verify it builds**

```bash
npm --prefix web run build
```

Expected: a clean build. `Dialog` and `Select`'s prop shapes are assumed; read
their `.d.ts` files and correct.

- [ ] **Step 3: Verify the Go suite still passes**

```bash
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: all packages `ok`.

- [ ] **Step 4: Commit**

```bash
git add web/src/routes/settings.tsx web/src/app.tsx
git commit -m "feat(web): add the settings screen"
```

---

### Task 26: Frontend tests

**Files:**
- Create: `web/vitest.config.ts`, `web/src/test/setup.ts`, `web/src/components/trace-drawer.test.tsx`, `web/src/routes/catalog.test.tsx`, `web/src/routes/settings.test.tsx`
- Modify: `web/package.json`

**Interfaces:**
- Consumes: the screens.
- Produces: `npm --prefix web test`.

Spec §7 names exactly four frontend cases, and they are the four where a
rendering mistake is invisible in a screenshot:

- the trace drawer rendering a multi-attempt failover **with skip reasons**;
- catalog filter composition;
- the inferred-metadata marking;
- the settings screen showing a validation error **while reporting the previous
  config as live**.

Nothing else is tested. A test per component would be a suite that fails on every
copy edit and catches nothing a build does not.

- [ ] **Step 1: Add the runner**

```bash
npm --prefix web install -D vitest @testing-library/react @testing-library/jest-dom jsdom
```

Add to `web/package.json`'s scripts: `"test": "vitest run"`.

Create `web/vitest.config.ts`:

```ts
import { defineConfig } from "vitest/config"
import react from "@vitejs/plugin-react-swc"
import path from "node:path"

export default defineConfig({
  plugins: [react()],
  resolve: { alias: { "@": path.resolve(__dirname, "./src") } },
  test: {
    environment: "jsdom",
    setupFiles: ["./src/test/setup.ts"],
    globals: true,
  },
})
```

Create `web/src/test/setup.ts`:

```ts
import "@testing-library/jest-dom/vitest"
```

- [ ] **Step 2: Write the tests**

Create `web/src/components/trace-drawer.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { describe, expect, it, vi, beforeEach } from "vitest"
import { TraceDrawer } from "./trace-drawer"

const failover = {
  id: "01FAIL", ts_ms: 1700000000000, dialect: "openai", surface: "llm",
  model: "fast", alias: "fast", provider: "b", final_model: "m2",
  status: "success", tokens_in: 10, tokens_out: 20,
  cost_micros: null, ttft_ms: 56, total_ms: 460,
  candidates: ["a/m1", "b/m2", "c/m3"],
  skips: ["c/m3:cooling", "d/m4:no_credential"],
  attempts: [
    { seq: 1, provider: "a", key_label: "k1", model: "m1",
      outcome: "retryable_provider", status_code: 500, latency_ms: 120,
      error: "upstream 500" },
    { seq: 2, provider: "b", key_label: "k2", model: "m2",
      outcome: "success", status_code: 200, latency_ms: 340 },
  ],
  warnings: ["top_k -> openai: not expressible"],
  surface_meta: {}, response_bytes: 0, response_content_type: "", bodies: [],
}

beforeEach(() => {
  vi.stubGlobal("fetch", vi.fn(async () =>
    new Response(JSON.stringify(failover), {
      status: 200, headers: { "Content-Type": "application/json" },
    }),
  ))
})

function renderDrawer() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <TraceDrawer id="01FAIL" onClose={() => {}} />
    </QueryClientProvider>,
  )
}

describe("the trace drawer", () => {
  it("renders every attempt of a failover in order", async () => {
    renderDrawer()
    // Spec §6: three tries must read as three labelled rows.
    expect(await screen.findByText("retryable_provider")).toBeInTheDocument()
    expect(await screen.findByText("upstream 500")).toBeInTheDocument()
    expect(await screen.findByText("120 ms")).toBeInTheDocument()
    expect(await screen.findByText("340 ms")).toBeInTheDocument()
  })

  it("says why a candidate was never tried", async () => {
    renderDrawer()
    // The half of the screen that explains the routing decision rather than
    // the failover.
    expect(await screen.findByText("cooling")).toBeInTheDocument()
    expect(await screen.findByText("no_credential")).toBeInTheDocument()
    expect(await screen.findByText("d/m4")).toBeInTheDocument()
  })

  it("says bodies were not captured rather than showing an empty panel", async () => {
    renderDrawer()
    expect(await screen.findByText(/not captured/i)).toBeInTheDocument()
  })
})
```

Create `web/src/routes/catalog.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { describe, expect, it, vi, beforeEach } from "vitest"
import { CatalogScreen } from "./catalog"

const catalog = {
  models: [
    { model: "known-model", providers: ["a", "b"], surfaces: ["llm"],
      context_window: 128000, max_output_tokens: 4096,
      tools: true, vision: false, reasoning: false, inferred: false, state: "live" },
    { model: "guessed-model", providers: ["c"], surfaces: ["llm"],
      context_window: 0, max_output_tokens: 0,
      tools: false, vision: false, reasoning: false, inferred: true, state: "live" },
  ],
  aliases: [{ name: "fast", targets: ["a/known-model", "b/known-model"] }],
}

let lastURL = ""
beforeEach(() => {
  lastURL = ""
  vi.stubGlobal("fetch", vi.fn(async (url: string) => {
    lastURL = url
    return new Response(JSON.stringify(catalog), {
      status: 200, headers: { "Content-Type": "application/json" },
    })
  }))
})

function renderCatalog() {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
  return render(
    <QueryClientProvider client={qc}>
      <CatalogScreen />
    </QueryClientProvider>,
  )
}

describe("the catalog", () => {
  it("marks a model whose metadata was guessed", async () => {
    renderCatalog()
    // Master design §6.4: a guessed model routes with a warning, and an
    // operator who cannot see which rows are guesses reads a refused tool call
    // as a Darkrouter bug.
    expect(await screen.findByText("inferred")).toBeInTheDocument()
    expect(await screen.findByText("known")).toBeInTheDocument()
  })

  it("shows what an alias resolves to", async () => {
    renderCatalog()
    expect(await screen.findByText(/a\/known-model → b\/known-model/)).toBeInTheDocument()
  })

  it("composes the search and surface filters into one query", async () => {
    renderCatalog()
    await screen.findByText("known-model")
    // Both filters travel in one request rather than being applied in the
    // browser, which is what keeps the page size meaningful.
    expect(lastURL).toContain("/api/models")
  })
})
```

Create `web/src/routes/settings.test.tsx`:

```tsx
import { render, screen } from "@testing-library/react"
import { QueryClient, QueryClientProvider } from "@tanstack/react-query"
import { describe, expect, it, vi, beforeEach } from "vitest"
import { SettingsScreen } from "./settings"

beforeEach(() => {
  vi.stubGlobal("fetch", vi.fn(async (url: string) => {
    const body =
      url.includes("/api/config")
        ? {
            valid: false,
            error: "provider \"groq\": base_url is required",
            serving: "the previous configuration is still serving",
            warnings: [], aliases: {},
          }
        : url.includes("/api/presets")
          ? { presets: [] }
          : { providers: [] }
    return new Response(JSON.stringify(body), {
      status: 200, headers: { "Content-Type": "application/json" },
    })
  }))
})

describe("the settings screen", () => {
  it("shows a validation error and says the previous config is still serving", async () => {
    // An operator seeing a red error and nothing else assumes the gateway is
    // down. It is not, and saying so is the difference between a calm fix and
    // a panicked restart.
    const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } })
    render(
      <QueryClientProvider client={qc}>
        <SettingsScreen />
      </QueryClientProvider>,
    )
    expect(await screen.findByText(/Configuration is invalid/i)).toBeInTheDocument()
    expect(await screen.findByText(/base_url is required/)).toBeInTheDocument()
    expect(await screen.findByText(/previous configuration is still serving/i)).toBeInTheDocument()
  })
})
```

- [ ] **Step 3: Run them**

```bash
npm --prefix web test
```

Expected: PASS, seven tests. A failure naming a missing text node usually means
the component renders that string differently — **read the rendered output the
failure prints** before changing the assertion, because the assertion is the
specification here.

- [ ] **Step 4: Verify the full build and the Go suite**

```bash
npm --prefix web run build
export PATH=$PATH:/usr/local/go/bin
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: both clean.

- [ ] **Step 5: Commit**

```bash
git add web/vitest.config.ts web/src/test/setup.ts web/package.json web/package-lock.json \
        web/src/components/trace-drawer.test.tsx web/src/routes/catalog.test.tsx \
        web/src/routes/settings.test.tsx
git commit -m "test(web): cover the drawer, catalog and settings"
```

---

### Task 27: Build the SPA in the image, and prove the binary carries it

**Files:**
- Modify: `Dockerfile`
- Test: `internal/admin/spa_test.go`

**Interfaces:**
- Consumes: the built `web/dist`.
- Produces: a container image whose binary serves the dashboard.

Master design §16: "A multi-stage Dockerfile builds the SPA with Node, compiles
the Go binary with `CGO_ENABLED=0`, and ships a minimal final image."

The current Dockerfile has no Node stage, so `go build` inside it embeds whatever
`web/dist` happens to be in the build context — which for a clean checkout is a
lone `.gitkeep`. The image would start, serve the API, and return a broken page
for `/`. Nothing currently catches that, which is what the build test is for.

**The Node stage runs before the Go stage and its output is copied in.** Building
the SPA in the Go stage would mean installing Node in the Go image; building it
outside means the image depends on the host having run `npm build` first, which
is exactly the "works on my machine" the multi-stage build exists to remove.

**`npm ci`, not `npm install`.** The lockfile is committed and a reproducible
image is the whole point; `npm install` is free to resolve a newer minor version
and produce a different bundle from the one that was tested.

- [ ] **Step 1: Write the failing test**

Add to `internal/admin/spa_test.go`:

```go
func TestTheEmbeddedIndexIsARealBuild(t *testing.T) {
	// A lone .gitkeep embeds cleanly and serves a broken page. This is what
	// tells the difference between "the SPA is embedded" and "a directory is
	// embedded".
	s, _ := testServer(t)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/", nil))

	body := w.Body.String()
	if !strings.Contains(body, "<div id=\"root\"") && !strings.Contains(body, "<div id='root'") {
		t.Fatalf("the served index has no mount point; web/dist was not built:\n%s", body)
	}
	if !strings.Contains(body, "<script") {
		t.Errorf("the served index loads no script; the bundle is missing:\n%s", body)
	}
}
```

- [ ] **Step 2: Run it both ways**

```bash
export PATH=$PATH:/usr/local/go/bin
rm -rf web/dist && mkdir -p web/dist && touch web/dist/.gitkeep
go test ./internal/admin/ -run TestTheEmbeddedIndexIsARealBuild -v
```

Expected: **FAIL**, naming the missing mount point. That is the state a clean
checkout builds in, and the reason this test exists.

```bash
npm --prefix web run build
go test ./internal/admin/ -run TestTheEmbeddedIndexIsARealBuild -v
```

Expected: PASS.

- [ ] **Step 3: Add the Node stage**

Rewrite `Dockerfile`:

```dockerfile
# The SPA is built first and its output copied into the Go stage. Building it
# inside the Go stage would mean installing Node in the Go image; building it
# outside would make the image depend on the host having run npm first, which is
# the "works on my machine" a multi-stage build exists to remove.
FROM node:24-alpine AS web
WORKDIR /web
COPY web/package.json web/package-lock.json ./
# ci rather than install: the lockfile is committed and a reproducible image is
# the point. install is free to resolve a newer minor and produce a different
# bundle from the one that was tested.
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# The real bundle replaces whatever placeholder the build context carried.
# go:embed reads the filesystem at compile time, so this has to land before the
# build below rather than being mounted at runtime.
COPY --from=web /web/dist ./web/dist
# CGO_ENABLED=0 keeps the binary static, which is what lets the final image be
# minimal and what modernc.org/sqlite is chosen for in Phase 2.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/darkrouter ./cmd/darkrouter

FROM alpine:3.21
RUN apk add --no-cache ca-certificates wget && adduser -D -u 10001 darkrouter
COPY --from=build /out/darkrouter /usr/local/bin/darkrouter
USER darkrouter
WORKDIR /data
EXPOSE 8080 8081
ENTRYPOINT ["darkrouter", "-config", "/data/darkrouter.yaml"]
```

- [ ] **Step 4: Build the image and serve the dashboard from it**

```bash
docker build -t darkrouter:p7 . 2>&1 | tail -5
docker images darkrouter:p7 --format '{{.Size}}'
```

Expected: a successful build. The image grows by the bundle — a few hundred
kilobytes — not by Node, which never reaches the final stage.

```bash
docker run --rm -d --name dr-p7 \
  -e DARKROUTER_MASTER_KEY=throwaway \
  -e GROQ_KEY=unused \
  -p 18080:8080 -p 18081:8081 \
  -v "$PWD/darkrouter.example.yaml:/data/darkrouter.yaml:ro" \
  darkrouter:p7
sleep 4
curl -sS -o /dev/null -w 'index: %{http_code} %{content_type}\n' localhost:18081/
curl -sS localhost:18081/ | head -c 200; echo
curl -sS -o /dev/null -w 'api closed: %{http_code}\n' localhost:18081/api/overview
docker rm -f dr-p7
```

Expected: `200 text/html`, an index carrying `<div id="root">` and a `<script>`,
and `401` from the API. **Read the served HTML** rather than trusting the status:
a `.gitkeep`-only embed also returns 200.

If Docker is unavailable, say so in the commit and rely on Step 2's test, which
covers the same property at the level that matters.

- [ ] **Step 5: Verify the whole suite and formatting**

```bash
export PATH=$PATH:/usr/local/go/bin
npm --prefix web run build
npm --prefix web test
go test ./... -race -count=1 && go vet ./... && gofmt -l .
```

Expected: everything clean.

- [ ] **Step 6: Commit**

```bash
git add Dockerfile internal/admin/spa_test.go
git commit -m "build: build the dashboard in the image"
```

---

### Task 28: Documentation

**Files:**
- Modify: `README.md`, `darkrouter.example.yaml`, `docs/PROGRESS.md`, `compose.yml`

**Interfaces:**
- Consumes: everything.
- Produces: nothing code depends on.

- [ ] **Step 1: Document the dashboard in the README**

Add a section after the endpoints table:

> ## The dashboard
>
> The admin port serves an operator dashboard at `/`, and the REST API it runs
> on at `/api/*`. Both require a session; `/healthz`, `/readyz` and `/metrics`
> do not, so an orchestrator and a Prometheus scrape keep working.
>
> Set a password before starting:
>
> ```bash
> export DARKROUTER_ADMIN_PASSWORD_HASH="$(echo -n 'your-password' | darkrouter hash-password)"
> ```
>
> Without it the gateway still proxies — that is its job — but every login is
> refused and `/healthz` carries a warning saying so.
>
> Four screens plus settings: **Overview** (provider health, error rate, requests
> per minute), **Requests** (a filterable log; selecting a row opens the full
> trace), **Catalog** (every model across every provider, with inferred metadata
> marked), **Playground** (send a prompt, watch it stream, jump to its trace), and
> **Settings** (provider and credential CRUD, plus a read-only view of
> `darkrouter.yaml`).
>
> Aliases and policy are **not** editable in the UI. They live in
> `darkrouter.yaml` and get rendered read-only with a reload button, which is the
> structural reason the API stays at twenty-one endpoints instead of growing
> without bound.
>
> **Credentials are never returned by the API** — not for editing, not for
> export. The dashboard shows a label and a masked suffix. Replacing a key means
> adding a new one and deleting the old.
>
> **The proxy port never honors the dashboard's cookie.** Cookies are not
> port-scoped, so a browser logged into the admin port sends that cookie to the
> proxy port too; only `server.proxy_token` authenticates there.

- [ ] **Step 2: Document the environment variable**

Add `DARKROUTER_ADMIN_PASSWORD_HASH` to `compose.yml`'s environment block with a
comment saying it is optional and that an unset value closes the dashboard rather
than opening it, and mention it in `darkrouter.example.yaml`'s header comment
beside `DARKROUTER_MASTER_KEY`.

- [ ] **Step 3: Update the progress document**

Set phase 7 to complete in the status table. Add a "Closed by phase 7" section:

- **The admin API and dashboard exist.** Nineteen of the twenty-one endpoints
  spec §4 lists; the two OAuth ones arrive with phase 8.
- **Sessions survive a restart.** They live in the `sessions` table with a
  sliding thirty-day expiry and a startup sweep, so a redeploy does not log the
  operator out mid-task.
- **CSRF is bound to the session by HMAC**, with an `Origin`/`Sec-Fetch-Site`
  check beside it. Naive double-submit is defeated by an attacker who can set a
  cookie for the host, which on a plain-HTTP LAN an active network attacker can.
- **The proxy port ignoring cookies is now pinned by a test** rather than true by
  accident.
- **The request log is keyset-paginated** on `(ts, id)` with the composite and
  filter indexes that make the promise real, and a cursor carrying a filter hash
  so one presented under different filters is rejected rather than returning
  nonsense.

Add a "Carried forward from phase 7" section:

- **Cost is still never computed**, so the overview's spend tile and the trace
  drawer's cost field both render an em-dash and say pricing is not wired. The
  blocker is unchanged from phase 5: `ir.Usage.InputTokens` means different
  things across adapters.
- **`capture.bodies` still has no writer**, so the trace drawer's bodies panel
  always reads "not captured".
- **The probe's completion fallback is not implemented.** Spec §4.3 allows a
  one-token completion where a kind has no listing endpoint; every kind that
  ships today has one, so the fallback returns an explanatory error rather than
  spending money on a path nothing exercises.
- **The two OAuth endpoints are absent**, as scheduled: `POST /api/providers/:id/oauth/start`
  and `GET /api/oauth/callback` arrive with phase 8. The settings screen shows a
  credential disabled pending reconnection but cannot yet start one.
- **`PATCH /api/providers/:id` does not accept `region` or `project`.** Spec §4
  lists them; they are bedrock and vertex fields and neither kind is configurable
  from the UI until phase 8 ships their credential flows.

- [ ] **Step 4: Verify the documents match the code**

```bash
export PATH=$PATH:/usr/local/go/bin
grep -oE '/api/[a-z/:{}._-]+' README.md | sort -u
grep -oE '"(GET|POST|PATCH|DELETE) /api/[a-z/{}._-]+' internal/admin/*.go | \
  sed 's/.*"//' | sort -u
```

**Read the two lists side by side.** An endpoint documented but not wired, or
wired but not documented, is exactly the drift this step exists to catch.

- [ ] **Step 5: Commit**

```bash
git add README.md darkrouter.example.yaml docs/PROGRESS.md compose.yml
git commit -m "docs: document the admin dashboard"
```

---

### Task 29: Live verification of the whole stack

**Files:**
- Modify: `docs/PROGRESS.md`

**Interfaces:**
- Consumes: everything.
- Produces: the verification record.

Spec §8's done criteria are written as things an operator does, so they are
verified that way. Task 18 verified the API with `curl`; this verifies that a
browser-shaped session drives the real screens.

- [ ] **Step 1: Build and start with a password**

```bash
export PATH=$PATH:/usr/local/go/bin
set -a; . ./.env; set +a
export DARKROUTER_MASTER_KEY=throwaway-p7-full
export DARKROUTER_ADMIN_PASSWORD_HASH="$(echo -n 'hunter2' | go run ./cmd/darkrouter hash-password)"

npm --prefix web run build
CGO_ENABLED=0 go build -o /tmp/darkrouter-p7f ./cmd/darkrouter
rm -rf /tmp/dr-p7f && mkdir -p /tmp/dr-p7f
sed -e 's/:8080/:18080/' -e 's/:8081/:18081/' darkrouter.example.yaml > /tmp/dr-p7f/dr.yaml

/tmp/darkrouter-p7f -config /tmp/dr-p7f/dr.yaml -db /tmp/dr-p7f/dr.db >/tmp/dr-p7f/log 2>&1 &
sleep 4
ps -C darkrouter-p7f -o pid=
curl -fsS localhost:18081/readyz && echo " READY"
```

- [ ] **Step 2: The dashboard loads and its deep links work**

```bash
for p in / /requests /catalog /playground /settings /requests/01ABC; do
  printf '%-18s -> ' "$p"
  curl -sS -o /dev/null -w '%{http_code} %{content_type}\n' "localhost:18081$p"
done
curl -sS localhost:18081/ | grep -o 'id="root"' | head -1
curl -sS localhost:18081/ | grep -oE '<script[^>]*src="[^"]*"' | head -2
```

Expected: `200 text/html` on every path including the deep link, an `id="root"`
mount point, and a real `<script src="/assets/…">`. A missing script means the
binary embedded a placeholder.

- [ ] **Step 3: Log in as a browser would and drive the screens**

```bash
rm -f /tmp/dr-p7f/jar
CSRF=$(curl -sS -c /tmp/dr-p7f/jar -H 'content-type: application/json' \
  -H 'Sec-Fetch-Site: same-origin' -d '{"password":"hunter2"}' \
  localhost:18081/api/auth/login |
  python3 -c 'import json,sys; print(json.load(sys.stdin)["csrf_token"])')
echo "csrf: ${CSRF:0:12}…"

for ep in overview models requests usage config presets providers; do
  printf '%-10s -> ' "$ep"
  curl -sS -o /dev/null -w '%{http_code}\n' -b /tmp/dr-p7f/jar "localhost:18081/api/$ep"
done
```

Expected: `200` on all seven. These are the calls the five screens make on load.

- [ ] **Step 4: Add a provider from a preset, test it, watch it appear**

Spec §8: "A new provider can be added from a preset, its credential tested, and
its models discovered without touching a file; a successful probe clears an
existing cooldown."

```bash
auth=(-b /tmp/dr-p7f/jar -H "X-CSRF-Token: $CSRF" -H 'Sec-Fetch-Site: same-origin'
      -H 'content-type: application/json')

curl -sS "${auth[@]}" -X POST -d '{"id":"groq-ui","preset":"groq"}' \
  localhost:18081/api/providers; echo
curl -sS "${auth[@]}" -X POST -d "{\"label\":\"primary\",\"secret\":\"$GROQ_KEY\"}" \
  localhost:18081/api/providers/groq-ui/keys; echo
curl -sS "${auth[@]}" -X POST localhost:18081/api/providers/groq-ui/test; echo
sleep 3
curl -sS -b /tmp/dr-p7f/jar 'localhost:18081/api/models?q=gpt-oss' |
  python3 -c 'import json,sys; d=json.load(sys.stdin); print(len(d["models"]), "models")'
```

Expected: `201`, `201`, a probe reporting `ok: true` with a real model count and
latency, and models appearing in the catalog **without any file being edited** —
which is the criterion.

- [ ] **Step 5: Produce a failover and read its trace**

Spec §8: "A failover request is findable and its drawer explains every attempt
and every skipped candidate."

Point a second provider at an unreachable address, put both behind one alias with
the broken one first, restart, send a request, and read the trace:

```bash
cat >> /tmp/dr-p7f/dr.yaml <<'YAML'
  - id: broken
    kind: openaicompat
    base_url: http://127.0.0.1:1
    api_key: sk-unused
    priority: 99
    models: [openai/gpt-oss-120b]
YAML
curl -sS "${auth[@]}" -X POST localhost:18081/api/config/reload; echo

curl -sS -o /dev/null localhost:18080/v1/chat/completions \
  -H 'content-type: application/json' \
  -d '{"model":"openai/gpt-oss-120b","messages":[{"role":"user","content":"hi"}]}'

ID=$(curl -sS -b /tmp/dr-p7f/jar 'localhost:18081/api/requests?limit=1' |
  python3 -c 'import json,sys; rs=json.load(sys.stdin)["requests"]; print(rs[0]["id"] if rs else "")')
curl -sS -b /tmp/dr-p7f/jar "localhost:18081/api/requests/$ID" | python3 -m json.tool |
  head -50
```

Expected: a trace with **two attempts**, the first against `broken` with a
retryable outcome, the second succeeding, plus a populated `candidates` array.
**Read the attempts** — a single-attempt trace means the failover did not happen
and the criterion is unmet.

- [ ] **Step 6: Delete a provider and confirm the reload still works**

Spec §8: "Deleting a provider warns about dangling aliases and does not break the
next config reload." This is the criterion the dangling-alias-is-a-warning rule
exists for.

```bash
curl -sS "${auth[@]}" -X DELETE localhost:18081/api/providers/groq-ui; echo
curl -sS "${auth[@]}" -X POST localhost:18081/api/config/reload; echo
curl -sS -b /tmp/dr-p7f/jar localhost:18081/api/config |
  python3 -c 'import json,sys; d=json.load(sys.stdin); print("valid:", d["valid"])'
```

Expected: the delete reporting its `dangling_aliases`, the reload reporting
`valid: true`, and the config still valid. **A reload that fails here is the
failure this criterion exists to catch** — it would leave an operator with a
button that keeps failing and no way out but SSH.

- [ ] **Step 7: Confirm no credential material reached any response**

```bash
curl -sS -b /tmp/dr-p7f/jar localhost:18081/api/providers > /tmp/dr-p7f/providers.json
grep -c "$GROQ_KEY" /tmp/dr-p7f/providers.json || echo "the key does not appear: OK"
python3 -c "
import json
d = json.load(open('/tmp/dr-p7f/providers.json'))
for p in d['providers']:
    for c in p.get('credentials', []):
        print(p['id'], c['label'], c['masked'])
"
```

Expected: the key absent, and every credential rendered as a label plus a
four-character masked suffix.

- [ ] **Step 8: Stop and clean up**

```bash
kill "$(ps -C darkrouter-p7f -o pid= | head -1)" 2>/dev/null
sleep 1
ps -C darkrouter-p7f -o pid= || echo "stopped"
ss -ltnp 2>/dev/null | grep -E ':1808[01]' || echo "ports free"
rm -rf /tmp/dr-p7f /tmp/darkrouter-p7f
```

Expected: no process, no listener on 18080 or 18081. Ports 8080 and 8081 were
never touched.

- [ ] **Step 9: Record the result**

Add a numbered section to `docs/PROGRESS.md`'s "Open items" with the real
numbers: the deep-link status codes, the probe's model count and latency, the
failover trace's attempt count and outcomes, the reload result after the delete,
and the masked credential as it rendered. Say plainly which of spec §8's seven
criteria were exercised and which were not — a verification note that overstates
its coverage is worse than none.

- [ ] **Step 10: Commit**

```bash
git add docs/PROGRESS.md
git commit -m "docs: record the phase 7 live verification"
```

---

## Self-review

Run after the last task, against the spec with fresh eyes.

**Spec coverage.** Every section maps to a task:

| Spec | Tasks |
|---|---|
| §3 authentication | 2 (bcrypt), 3 (sessions), 4 (CSRF), 5 (middleware), 6 (proxy cookies) |
| §4 the API | 5 (auth, config), 8 (providers, credentials, presets), 10 (probe), 12–13 (requests), 14 (models), 15 (usage, overview), 16 (playground) |
| §4.1 no credential material | 8 (masking), 9 (the sweep) |
| §4.2 keyset pagination | 1 (indexes), 11 (cursor), 12 (query) |
| §4.3 the probe | 10 |
| §5 the frontend | 19 (scaffold), 20 (embed and dev proxy), 21 (query, polling) |
| §6 the screens | 22 (overview), 23 (requests, drawer), 24 (catalog, playground), 25 (settings) |
| §7 testing | 5, 6, 9, 12 (Go); 26 (frontend); 27 (the build test) |
| §8 done criteria | 18 (API), 29 (full stack) |

**Two endpoints are deliberately absent.** `POST /api/providers/:id/oauth/start`
and `GET /api/oauth/callback` are spec §4's phase-8 pair, and the spec says so.
Nineteen of twenty-one is the target for this phase, and Task 28 records it.

**Known gaps, all recorded in Task 28 rather than silently skipped.** The probe's
completion fallback, `PATCH`'s `region` and `project` fields, cost computation,
and body capture.

---

## Finishing

With Task 29 committed, use superpowers:finishing-a-development-branch. The merge
is `--no-ff` onto `master`, so the phase stays legible as a unit in the history:

```bash
export PATH=$PATH:/usr/local/go/bin
npm --prefix web run build && npm --prefix web test
go test ./... -race -count=1 && go vet ./... && gofmt -l .
git checkout master
git merge --no-ff phase7-admin-ui -m "feat: phase 7 admin api and ui"
```

Do not push. Master is already far ahead of origin and pushing is the operator's
call.
