# Phase 2 — Persistence and Health

**Status:** Approved design.
**Date:** 2026-08-22
**Master design:** `2026-08-22-darkrouter-design.md`
**Depends on:** phase 1.

---

## 1. Goal

Make state durable. SQLite becomes the home for provider connections, encrypted API keys, the request
and attempt log, health and cooldown state, and usage rollups.

## 2. Scope boundary

**In:** `internal/store`, `internal/crypto`, the SQLite provider source, the async log writer, the
circuit breaker and its persistence, the rollup and retention workers.

**Out:** routing decisions and the attempt loop (phase 3 — this phase records health, it does not yet
act on it beyond the single-candidate path), the admin API that exposes any of this (phase 7).

## 3. Storage

`modernc.org/sqlite` — pure Go, so `CGO_ENABLED=0` still produces a static binary.

Schema per master design §11, created by embedded, versioned migration files applied in order at
startup. A `schema_version` row gates them. Migrations are forward-only; there is no down path,
because the recovery story for a single-operator homelab is a file copy, not a reversible migration.

### 3.1 Connection settings

WAL journal mode, `busy_timeout` at 5 seconds, `foreign_keys` on, `synchronous=NORMAL`. WAL plus
NORMAL is the right durability trade for a request log: a crash can lose the last few log rows, which
costs nothing, and the alternative costs an fsync on the hot path.

### 3.2 One writer

All writes go through a single owning goroutine. SQLite permits only one writer, and letting many
request goroutines contend produces `SQLITE_BUSY` under exactly the load where you least want it.
Readers use the pool directly; WAL makes concurrent reads safe against the writer.

### 3.3 Money is integers

Costs are stored as `cost_micros int64` — millionths of a unit. No floating point touches money at
any layer, including the API and the UI.

## 4. Key encryption

The chain is `DARKROUTER_MASTER_KEY` → PBKDF2-HMAC-SHA256 with a per-database random salt → a 32-byte
AES key → AES-GCM per stored key with a fresh random nonce.

The salt is generated once on first run and stored in `settings`. A KDF verifier — a known plaintext
encrypted under the derived key, also in `settings` — lets startup detect a wrong or changed master
key and fail loudly rather than emitting garbled keys at request time.

Rotating the master key re-encrypts every stored key in one transaction and rewrites the verifier. If
the process dies mid-rotation the transaction rolls back, so keys are never left half-rotated.

The admin API never returns a decrypted key. Phase 7 exposes only a label and a masked suffix.

## 5. Provider source moves to SQLite

The `provider.Source` interface from phase 1 gains a SQLite implementation. On first run, if the
database has no providers and `darkrouter.yaml` still carries a `providers:` block, the YAML entries
are imported once and a marker is written to `settings` so the import never repeats. After that the
YAML block is ignored, and the file's remaining job is aliases and policy.

This is the one place two sources of truth touch, and it is bounded to first run by design.

## 6. Request logging

Logging must never slow or block a request. The path is: the request handler builds a complete log
record in memory, sends it on a buffered channel, and returns. A background writer batches records
into one transaction on a short timer or when the batch fills.

If the channel is full the record is **dropped and a counter incremented**, surfaced on the health
endpoint. Blocking the request path to guarantee a log line is the wrong trade — the gateway's job is
to proxy, and the log is diagnostic.

Each request writes one `requests` row and one `request_attempts` row per attempt, inserted together
so a trace is never partially visible.

## 7. Health and the circuit breaker

State is keyed on `(provider_id, key_id, model)`.

**In-memory is authoritative on the hot path.** A mutex-guarded map answers "is this candidate
available" without touching SQLite, because the router consults it on every request. Changes are
persisted asynchronously — debounced, so a provider failing fast does not generate a write per
failure — and the map is rehydrated from `health` at startup.

The consequence is bounded and acceptable: a crash can lose the last few seconds of health updates. A
restart that forgets a cooldown costs one wasted attempt, which the breaker immediately re-trips.

### 7.1 Trip and recovery rules

| Signal | Effect |
|---|---|
| 429 with `Retry-After` | Cool the triple for exactly that duration. |
| 429 without `Retry-After` | Cool by the backoff ladder. |
| 5xx, 408, timeout, connection reset | Increment consecutive failures; trip once `policy.cooldown.trip_after` is reached, then cool by the ladder. |
| Any response proving reachability, including 400 | Reset the ladder for that key. |

The ladder runs 1s, 2s, 4s, 8s, 15s, 30s, 60s, 120s and then clamps at `policy.cooldown.max`. On
expiry the triple enters half-open: one probe request is admitted, and success closes the breaker
while failure re-trips at the next ladder level.

Resetting on reachability rather than on success is deliberate. A 400 proves the provider is up and
answering; it says something about the request, not the provider. Treating it as a failure would keep
a healthy provider cooling because a client sent bad input.

## 8. Background workers

Three, all with jittered intervals so they never align, and all shut down cleanly on SIGTERM:

- **Log writer** — batches request and attempt rows.
- **Rollup** — aggregates completed days into `usage_daily` so dashboard charts never scan the raw log. Runs hourly, recomputing today and finalizing yesterday.
- **Retention** — prunes `requests` and `request_attempts` past the retention window and `request_bodies` past `expires_at`. Deletes in bounded batches with a pause between them, so pruning a large backlog cannot lock out the writer.

## 9. Testing

Store tests run against a temporary database file and cover migration application from empty,
migration idempotency on restart, and the first-run YAML import running exactly once.

Crypto tests cover round-trip encryption, that two encryptions of the same plaintext differ (fresh
nonces), that a wrong master key is detected by the verifier rather than producing garbage, and that
an interrupted rotation rolls back.

Breaker tests are table-driven over the trip and recovery rules, including `Retry-After` parsing in
both delta-seconds and HTTP-date forms, ladder escalation, ladder reset on a 400, and half-open
behavior in both directions.

A restart test asserts that cooldowns written before shutdown are honored after startup.

Retention tests assert that batched deletion of a large backlog does not starve the writer.

## 10. Done criteria

- Provider connections and encrypted keys survive a restart; keys are unreadable in the raw database file.
- A wrong `DARKROUTER_MASTER_KEY` fails startup with a clear message.
- Every request produces one `requests` row and one `request_attempts` row per attempt.
- A provider returning 429 is recorded as cooling, and the cooldown survives a restart.
- Log writing under sustained load does not increase request latency; a saturated channel drops records and reports the count instead of blocking.
- `go test ./...` passes.
