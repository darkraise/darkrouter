# The Twelve Admin Endpoints — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task.

**Goal:** Add the endpoints §8.2 lists as new, in the order §8.4 gives, so every console screen has a data source and every action it offers has somewhere to send it.

**Architecture:** Almost nothing here is new logic. Route preview is the router's own pure `Resolve` over the executor's own snapshot. Breaker detail is `health.Entry` serialized. Discovery and catalog sync already have workers with a one-shot entry point. What is genuinely new is a per-client proxy token store and a `model_overrides` writer, and those are the two tasks carrying a migration.

**Spec:** `docs/superpowers/specs/2026-08-24-darkrouter-phase10-operator-console.md` §8.2 (the New table), §8.4 (the order), §13 step 3(c).

**Prior plan:** `2026-08-26-phase11b-aliases-policy-config.md` is slice 3(b) and is merged. It already serves aliases and policy through `GET`/`PUT /api/config`; this plan adds the focused `/api/aliases` and `/api/policy` pair §8.2 asks for, over the same store methods.

## Global Constraints

- TDD: a failing test precedes the implementation.
- Race-clean: `go test -race ./...` passes, `go vet ./...` and `gofmt -l internal cmd tools` are clean before any commit.
- `export PATH=/usr/local/go/bin:$PATH` before any Go command.
- Migrations are append-only; next free number is `0009`. Tables are `STRICT`.
- **Every mutating endpoint inherits phase 7 §3 unchanged:** session required, CSRF token bound to the session by HMAC, `Origin` or `Sec-Fetch-Site` check. Register it with `s.requireCSRF`, which wraps `requireSession` itself so a route cannot get one check and not the other. Every task asserts an unauthenticated write is refused.
- **No endpoint returns credential material** (phase 7 §4.1). `PATCH .../keys/{keyId}` accepts a new secret and never echoes one; a proxy token is shown once at creation and stored hashed.
- Comments explain WHY, never WHAT. No comment may reference this plan or task.
- Commit subjects: `<type>(<scope>): <subject>`, imperative, **50 characters or fewer**, no trailing period.
- English only. Stage explicit paths; never `git add -A`.

---

### Task 1: Breaker detail, reset, discovery and sync

**Unblocks:** Providers (§6.5). First because §8.4 puts it first and it needs no new storage.

**Files:**
- Modify: `internal/admin/probe.go` or a new `internal/admin/healthapi.go`
- Modify: `internal/admin/admin.go` — four routes
- Test: `internal/admin/healthapi_test.go`

Four endpoints:

| Method | Path |
|---|---|
| GET | `/api/health/providers` |
| POST | `/api/providers/{id}/breaker/reset` |
| POST | `/api/providers/{id}/discover` |
| POST | `/api/catalog/sync` |

- [ ] **Step 1: Write the failing tests**

- `GET /api/health/providers` reports, per credential, the cooling deadline, backoff level and consecutive failures. `health.Entry` already carries all three and `Breaker.Snapshot()` returns them, so this is a serialization — assert the numbers survive it rather than that the handler exists.
- A breaker with nothing cooling returns an empty list, not null. A JSON `null` where the console expects an array is the defect the phase 11a work already fixed once on the usage series.
- `POST /api/providers/{id}/breaker/reset` clears a cooldown, and the next `GET` shows the credential available. `clearCooldowns` (`internal/admin/probe.go:113`) already does this by recording a success; call it rather than reaching into the breaker.
- Reset does **not** spend a probe. That is the whole point of the endpoint — assert no request reaches the fake upstream.
- `POST /api/providers/{id}/discover` forces a sweep for one provider and `POST /api/catalog/sync` forces a models.dev sync. Both have a one-shot entry point already; a 202 with what was triggered is the answer, not a synchronous wait.
- An unknown provider id is a 404 on both provider-scoped routes.
- Each of the three mutating routes refuses an unauthenticated request.

- [ ] **Step 2: Run them, watch them fail**

- [ ] **Step 3: Implement**

- [ ] **Step 4: Run everything, commit**

Subject: `feat(admin): expose breaker detail and force sweeps`

---

### Task 2: Route preview

**Unblocks:** Routing (§6.7) and the compressed ladder in Models (§6.6).

**Files:**
- Modify: `internal/exec/resolve.go` — export the snapshot builder
- Add: `internal/admin/routeapi.go`
- Test: `internal/admin/routeapi_test.go`, `internal/exec/resolve_test.go`

§12's done criterion is exact: *route preview, given an alias, produces the same ordered candidate list the router produces for a real request against the same snapshot.* The only way to guarantee that is to share the construction, not to reimplement it.

- [ ] **Step 1: Extract the snapshot builder**

`(*Executor).resolve` builds `router.Snapshot` inline (`internal/exec/resolve.go:57`) from the executor's own provider source, catalog, adapter surfaces and fleet health. Lift exactly that into an exported method — `func (e *Executor) RouteSnapshot(ctx context.Context, at time.Time, cfg *config.Config) (router.Snapshot, error)` — and have `resolve` call it. No behaviour change; the existing exec tests are the proof.

`admin.Deps` already carries the same `*exec.Executor` the proxy port uses, so the preview endpoint has it without new wiring.

- [ ] **Step 2: Write the failing tests**

- `POST /api/route/preview` with a model or alias returns the ordered candidates and the skips, each skip carrying its `SkipReason`.
- **The equivalence test is the point:** drive one request through the real executor and the same query through the preview endpoint against the same snapshot instant, and assert the candidate lists are identical in order and content. A preview that agrees only on the set is a preview that lies about failover order.
- A query resolving to nothing returns the skips rather than an empty body — the skips are the only explanation of why nothing routed.
- Preview is read-only: assert it records no request row and spends no probe.

- [ ] **Step 3: Run them, watch them fail**

- [ ] **Step 4: Implement**

- [ ] **Step 5: Run everything, commit**

Subject: `feat(admin): add the route preview endpoint`

---

### Task 3: Aliases, policy and model overrides

**Unblocks:** the rest of Routing, and Models (§6.6).

**Files:**
- Add: `internal/admin/aliasapi.go`
- Add to: `internal/store/configstore.go` — override read/write
- Modify: `internal/admin/admin.go`
- Test: `internal/admin/aliasapi_test.go`, `internal/store/configstore_test.go`

| Method | Path |
|---|---|
| GET/PUT | `/api/aliases` |
| GET/PUT | `/api/policy` |
| GET/PUT/DELETE | `/api/models/{provider}/{model}/override` |

- [ ] **Step 1: Write the failing tests**

- `GET`/`PUT /api/aliases` and `/api/policy` are focused views of what `/api/config` already serves. They share `DB.PutAliases`, `DB.PutPolicy` and `config.ValidateAliases` — assert that a write through either surface is visible through the other, because two write paths that diverge is the failure worth testing for.
- A `PUT /api/policy` naming a restart-only field is refused, exactly as `PUT /api/config` refuses it. Same rule, same message.
- `model_overrides` has existed since migration `0001` and has never had a writer. `PUT` sets surfaces, capabilities and context window for one (provider, model); `DELETE` removes the row and the merged catalog returns to the upstream's own metadata. Assert the merge actually changes — a writer whose rows nothing reads is worse than none.
- An override for an unknown provider is a 404, not a stored orphan. The table has `ON DELETE CASCADE` onto providers, so the row would vanish silently later.

- [ ] **Step 2: Run them, watch them fail**

- [ ] **Step 3: Implement**

- [ ] **Step 4: Run everything, commit**

Subject: `feat(admin): serve aliases, policy and overrides`

---

### Task 4: Per-client proxy tokens

**Unblocks:** Connect (§6.9).

**Files:**
- Add: `internal/store/migrations/0009_proxy_tokens.sql`
- Add: `internal/store/proxytokens.go`
- Add: `internal/admin/tokenapi.go`
- Modify: `internal/edge/edge.go` or `internal/server/server.go` — accept a stored token
- Test: alongside each

`server.proxy_token` is one shared secret compared against three headers (`internal/edge/edge.go:43`). Replacing it with per-client tokens is what lets a client be revoked without rotating every other client's credential.

- [ ] **Step 1: Write the failing tests**

- `POST /api/proxy-tokens` returns the token **once**, and no later `GET` ever returns it again. Assert the stored row holds a hash, not the token: a token readable from the database is one a database backup leaks.
- `GET /api/proxy-tokens` lists name, prefix, created and last-used — enough to identify a token without reproducing it.
- `DELETE` revokes: a request bearing that token is refused afterwards.
- A request bearing a valid stored token is accepted on the proxy port.
- **`server.proxy_token` still works.** An operator upgrading must not have every client stop working the moment they take this build; the shared secret stays valid alongside stored tokens. Assert both.

- [ ] **Step 2: Run them, watch them fail**

- [ ] **Step 3: Implement**

Hash with the same primitive the admin password uses rather than introducing a second one. Compare in constant time.

- [ ] **Step 4: Run everything, commit**

Subject: `feat(admin): add per-client proxy tokens`

---

### Task 5: Sessions and the password change

**Unblocks:** Settings (§6.10).

**Files:**
- Add to: `internal/store/adminstore.go` — `SessionRows`
- Add: `internal/admin/sessionapi.go`
- Modify: `internal/admin/admin.go`
- Test: `internal/admin/sessionapi_test.go`

| Method | Path |
|---|---|
| GET/DELETE | `/api/sessions` |
| POST | `/api/auth/password` |

- [ ] **Step 1: Write the failing tests**

- `GET /api/sessions` lists live sessions with their creation and expiry, and marks which one is the caller's. An operator revoking sessions needs to know which row logs them out.
- `DELETE /api/sessions/{id}` revokes one; the next request bearing that cookie is refused.
- No session id is returned in full. The id *is* the credential — it is what the cookie carries — so the list shows a prefix, exactly as the proxy tokens do.
- `POST /api/auth/password` requires the current password, and refuses when it is wrong. A change endpoint that does not is a session-hijack escalation.
- Changing the password revokes every other session but keeps the caller's. Anything else logs the operator out of the screen they just used.

- [ ] **Step 2: Run them, watch them fail**

- [ ] **Step 3: Implement**

- [ ] **Step 4: Run everything, commit**

Subject: `feat(admin): list sessions and change the password`

---

## Notes for the executor

**Reuse, do not restate.** Four of these endpoints are thin wrappers over machinery that already exists: `router.Resolve`, `health.Breaker.Snapshot`, `clearCooldowns`, the discovery and sync one-shots. A task that finds itself writing routing or breaker logic has taken a wrong turn.

**The equivalence test in Task 2 is the one that matters.** Everything else here can be checked by reading the handler. Whether preview agrees with the real router cannot, and it is the only done criterion in §12 phrased as an equality.

**`server.proxy_token` is not being removed.** §8.2 says proxy tokens *replace* the shared secret, but removing it in the same release that adds them would break every existing client on upgrade. Add the new path, keep the old one working, and leave the removal to a phase that can announce it.
