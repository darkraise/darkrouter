# User accounts and first-run onboarding

Darkrouter authenticates against one shared password. This design replaces it
with named accounts: a `users` table, a session that knows whose it is, and a
console that is claimed by whoever creates the first account rather than by
whoever holds a token from the startup log.

The purpose is narrow and worth stating plainly, because it bounds everything
below: **the shared password is the wrong shape**, not the permission model.
One person operates this gateway today. They want a named account, a real
password-change flow, and room to add people later without redesigning it.
Nobody has asked for restricted roles, per-user quotas, or audit trails, and
this design deliberately builds none of them.

## Decisions taken before this design

These were settled with the operator and are not re-opened here.

| Decision | Choice |
|---|---|
| Identity | A case-insensitive unique **username**. No email, no verification. |
| First account | Becomes admin, claimed **trust on first use**. No setup token. |
| `DARKROUTER_ADMIN_PASSWORD_HASH` | **Retired entirely.** |
| Password recovery | **None.** No CLI, no environment escape hatch. |
| Roles | A column, not a matrix. Every account may call every endpoint. |

**Approach:** best-of-3 — session owner column vs anonymous sessions vs
stateless signed token. The owner column won 2 of 2 comparisons and 6 of 6
criteria. Grafts from the losing candidates are folded in and marked where they
land.

### This reverses a documented decision

`mintSetupToken` carries a comment (`internal/admin/setupapi.go:12-18`) arguing
that the setup token exists precisely to prevent the design below: letting
whoever reaches the port first set the password "would make an empty hash open
the port rather than close it, which is the inverse of what `VerifyPassword`
promises."

That reasoning was correct and is now overridden deliberately, for
convenience on a single-operator homelab deployment. What replaces the
protection is weaker and should be understood as weaker: a startup warning, a
line in `deploy.md`, and claiming the console promptly. An entry goes in
`docs/plan/decisions.md` recording the reversal, so it is not silently flipped
back — or silently flipped forward again by someone reading only the comment.

## 1. Data model

One migration, `internal/store/migrations/0023_users.sql`. The head is
`0022_credential_account_id.sql`.

```sql
CREATE TABLE users (
  id            TEXT    PRIMARY KEY,
  username      TEXT    NOT NULL,
  username_lc   TEXT    NOT NULL,
  password_hash TEXT    NOT NULL,
  role          TEXT    NOT NULL DEFAULT 'member',
  created_at    INTEGER NOT NULL
) STRICT;

CREATE UNIQUE INDEX idx_users_username_lc ON users(username_lc);

DROP TABLE sessions;

CREATE TABLE sessions (
  id         TEXT    PRIMARY KEY,
  user_id    TEXT    NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL
) STRICT;

CREATE INDEX idx_sessions_user ON sessions(user_id);

DELETE FROM settings
 WHERE key IN ('admin.password_hash', 'admin.password_env_fingerprint');
```

`username_lc` is lowercased **in Go**, at insert and at rename, not by SQLite's
`lower()` and not by `COLLATE NOCASE`: both fold ASCII only, and a rule the
next reader can see in one place beats one hidden in a collation. (Graft from
candidate B.)

Drop-and-recreate rather than `ALTER TABLE ADD COLUMN`, because SQLite cannot
add a `NOT NULL` column without a constant default to a non-empty table, and a
nullable `user_id` would invent a second "session with nobody behind it" state
to guard at every read. `ON DELETE CASCADE` follows the convention already at
`0001_init.sql:29`, and genuinely fires: `foreign_keys(on)` is in the DSN for
every connection (`internal/store/db.go:50`), asserted per-connection at
`internal/store/db_test.go:42`.

`role` holds `admin` or `member`. Nothing reads it in phase 1.

## 2. Migration

Every live session ends. The old hash and its environment fingerprint are
deleted. The console lands in first-run onboarding — the same state a fresh
install is in, with no separate migrated-deployment path, because a second path
is a second thing to test and explain.

Nothing carries the old password forward. There is no username to attach it to;
synthesising `admin` would pre-claim a name the real operator may want and would
keep the shared secret alive as an account.

Forcing a re-login on a session schema change is established practice here, not
a novelty: `0017_session_hashes_and_filter_indexes.sql:1-4` did exactly this —
"they are dropped rather than rewritten: operators log in again once."

**Startup warning.** When the database has data but no users, the process logs a
warning at startup naming the risk, in the shape `mintSetupToken` used
(`setupapi.go:35-36`). It goes to `slog` only, never to `startupWarnings`, which
reaches unauthenticated `/healthz`. Without it an unattended upgrade hands admin
to whoever loads the console first — a health check, a left-open bookmark, a
stranger. (Graft from candidate C.)

**The window is real and untestable.** Between that restart and the claim,
anyone who can reach the admin port can become admin. Both ports bind every
interface. The mitigations are the warning, the documentation, and claiming
promptly. No test asserts this away.

## 3. Auth path

**The route table does not change.** All 54 routes keep their existing guard —
3 `routePublic`, 19 `routeSession`, 34 `routeCSRF`. No new guard tier, and the
guard never consults `role`. Identity is added underneath the guards, not
beside them, which is what keeps this tractable.

**One signature widens.** `TouchSession(ctx, id, ttl) (bool, error)` becomes
`(userID string, ok bool, err error)`. It already reads the row on every
request (`sessions.go:59-62`); it now selects `user_id` as well, and joins
`users` so a session whose owner has vanished is a 401 rather than a 500. The
cascade should make that unreachable; a restored backup that de-syncs the tables
is where "unreachable" stops being true, and failing closed there is free.
(Graft from candidate C.)

**`requireSession` stashes two context values.** Alongside `sessionKey`
(`auth.go:41`) it puts the owner under a parallel `userKey`. `requireCSRF` needs
no change: it wraps `requireSession` and inherits both, which is the reason
`auth.go:45-47` gives for wrapping rather than sitting beside.

Other store signatures gain the owner so that per-account operations cannot be
written wrong: `CreateSession`, `SessionRows`, `RevokeSession` and
`DeleteSessionsExcept` all take a `userID`. `SweepSessions` does not — expiry
is global by nature.

**CSRF is untouched.** The token is `HMAC(secret, sessionID)`
(`csrf.go:71-92`), bound to the cookie value; login already rotates the id and
reissues the token in one response (`authapi.go:107-134`). Adding an owner
disturbs none of it.

### Login

`handleLogin` gains a `username` field and verifies against that user's own
hash. Three existing properties are kept deliberately:

- The 72-byte cap, because bcrypt reads no further and a longer password would
  verify against a truncation of itself.
- Both rate limiters — the per-IP bucket taken before the body is read, and
  the bcrypt concurrency semaphore — stay keyed by **IP, not by account**. An
  attacker rotating usernames must not get a fresh budget per name.
- One error message for every failure.

**No username enumeration.** An unknown username must be indistinguishable from
a wrong password in both the message and the response time. When the lookup
misses, the handler still runs a bcrypt comparison against a fixed dummy hash,
so timing does not answer the question the error message refuses to.

## 4. Endpoints

Paths barely move.

| Endpoint | Change |
|---|---|
| `POST /api/auth/setup` | Body `{token, password}` → `{username, password, confirm}`. Stays `routePublic`. Mints the session on success. |
| `POST /api/auth/login` | Gains `username`. |
| `GET /api/auth/status` | `configured` becomes "a user exists" instead of "a hash exists". |
| `POST /api/auth/password` | Operates on the caller's row. |
| `GET /api/sessions`, `DELETE /api/sessions/{id}` | Scoped to the caller. |

**The founding claim is atomic.** A single write transaction with the emptiness
check re-run inside it; the loser of a race gets 409. Today's handler achieves
this with insert-or-ignore, deciding the winner by whether its own salted hash
came back (`setupapi.go:101-104`); the users table needs an equivalent or two
browsers racing at first boot could both believe they won. (Graft from
candidate B; this was the one real defect the ring found in the winning
approach.)

**A confirmation field.** The claim takes the password twice. With no recovery
path of any kind, a typo in the founding password is unrecoverable — the
cheapest possible guard against the most expensive possible mistake. The server
requires the two to match rather than trusting the console to have checked, so
an API client cannot skip the guard the screen enforces. The minimum stays at 12
characters, matching `minPasswordChars` (`sessionapi.go:25`).

**Deleted outright:** `mintSetupToken`, `currentSetupToken`, `spendSetupToken`,
`PasswordConfigured`, the `setupToken`/`setupMu` fields, and the whole
`fingerprint` / `reconcilePasswordHash` / `recordPasswordEnv` /
`currentPasswordHash` cluster (`sessionapi.go:14-93`). The `hash-password`
subcommand goes with them: it exists solely to generate the retired variable.

**Account management is specified, not built.** Once `users` is non-empty, the
claim handler becomes admin-only account creation, checked in-handler against
`role` rather than by adding a guard tier. Writing this down now gives the
`role` column a named future rather than a speculative one. (Graft from
candidate C.)

## 5. Console

`web/src/features/shell/first-run.tsx` becomes username, password and
confirmation instead of token and password. The login screen gains a username
field. `change-password-dialog.tsx` retargets to the caller's row. The
"Signed-in browsers" card keeps its shape and now lists only the caller's own
sessions.

Typography follows the repo rule: `text-sm` floor, no custom sizes.

## 6. Testing

Behavioural, and each proven able to fail by breaking the code and watching it
go red.

**The scaffolding is the risk.** `internal/e2e/harness_test.go:97` seeds the
environment hash for every end-to-end test, and `server_test.go` keys three more
on it. That harness line becomes "create a user, then log in." A harness that
authenticates when it should not leaves the whole e2e suite green while proving
nothing. Change it first, alone, and verify it by asserting that a request
without the new setup fails.

Forty-three existing tests sit on machinery being deleted: 11 in
`setupapi_test.go`, 7 in `sessionapi_test.go`, 4 each in `password_test.go` and
`sessions_test.go`, 12 console cases in `first-run.test.tsx`, 5 in
`change-password-dialog.test.tsx`. The setup-token tests are not updated; the
thing they test ceases to exist.

Behaviours to pin:

- Migration from a database with a stored hash, and from one with only the
  environment variable. Both reach zero users, zero sessions, no stale rows.
- Two simultaneous founding claims: exactly one winner, the loser 409.
- A claim attempted once a user exists: 409, not a second admin.
- An unknown username is indistinguishable from a wrong password. Assert the
  dummy comparison runs rather than measuring the clock; a timing assertion is
  flaky by nature.
- `TouchSession` with a missing owner returns 401, not 500.
- Deleting a user removes their sessions through the cascade.
- One account cannot revoke another account's session.
- The startup warning fires on a populated database with no users, and stays
  silent on a fresh one. Both halves, because that warning is the only thing
  between an unattended upgrade and a stranger claiming admin.

## 7. Breaking changes

`DARKROUTER_ADMIN_PASSWORD_HASH` stops being read. A deployment setting it gets
no error and no effect; the console falls to onboarding on the next start. The
setup token stops being minted. Every live session ends. `hash-password` is
removed.

Documentation carrying the retired model: `deploy.md` (5 mentions),
`README.md` (4), `configuration.md` (2), `security.md`, `verification.md`,
`CLAUDE.md`, plus `compose.yml`, `.env.example`, and the three login mockups
under `docs/ux/mockups/`. `security.md` needs its threat model revised, since
the setup token is part of it.

## 8. Sequence

**Phase 1 — flag day.** Migration, the widened `TouchSession` and guard, login
and claim by username, the atomic claim, deletion of the retired machinery, the
startup warning, the e2e harness, and the login and first-run screens. These
cannot be split: any intermediate state has two authorities deciding who may log
in. The console ships in this phase because the binary embeds it at compile
time, so changing the login API without the screen that feeds it produces a
build nobody can log into.

Phase 1 also carries the `deploy.md` recovery corrections. It cannot wait for
phase 3: the moment phase 1 ships, that section instructs an operator to set a
variable that does nothing and tells them a missing log line means the change
was absorbed — advice that sends someone chasing deleted machinery during the
exact incident where they are already locked out.

**Phase 2 — account management.** Admin-only create, list and remove. The
`role` column's first consumer and the cascade's first caller.

**Phase 3 — documentation sweep.** The remainder, in the shape phase 4 of the
configuration work took.

## Open question

Whether `role` should be a constrained column (`CHECK (role IN ('admin',
'member'))`) or a free string. This design leaves it free, matching how `kind`
and `source` are handled elsewhere, and because a CHECK constraint on a STRICT
table is a migration to change. If phase 2 finds itself defending against
typo'd roles, constrain it then.
