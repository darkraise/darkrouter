# Phase 2 — Persistence and Health

**Status:** Approved design, revised 2026-08-22 against the review findings ledger.
**Master design:** `2026-08-22-darkrouter-design.md`
**Depends on:** phase 1.

---

## 1. Goal

Make state durable. SQLite becomes the home for provider connections, encrypted credentials, the
request and attempt log, health and cooldown state, and usage rollups.

## 2. Scope boundary

**In:** `internal/store`, `internal/crypto`, the SQLite provider source, the async log writer, the
circuit breaker and its persistence, the rollup and retention workers.

**Out:** routing decisions and the attempt loop (phase 3), the admin API (phase 7).

Phase 2 records health but does not yet route on it. On the single-candidate path a cooling sole
candidate is still attempted — there is nowhere to fail over to, and refusing to serve would make the
gateway less useful than phase 1 was.

## 3. Storage

`modernc.org/sqlite` — pure Go, so `CGO_ENABLED=0` still produces a static binary. Schema per master
design §11.

### 3.1 Connection settings

WAL journal mode, `busy_timeout=5000`, `foreign_keys=on`, `synchronous=NORMAL`,
`journal_size_limit`, and `wal_autocheckpoint` at their defaults unless measurement says otherwise.

These are **per-connection** pragmas, applied through the DSN so every pooled connection carries
them. Setting them once after opening leaves other pooled connections with foreign keys silently off
— a bug that surfaces as missing constraint enforcement rather than an error.

WAL plus NORMAL is the right durability trade for a request log: an OS crash can lose the last few
log rows, which costs nothing. It is *not* the right trade for credentials, so credential inserts and
master-key rotation run with `synchronous=FULL` on their connection. Without that, a power loss after
a rotation commits but before the WAL syncs rolls the rotation back while the operator has already
changed the environment variable — and the next startup fails the verifier with no way to tell why.

Long-running read transactions pin the WAL and block checkpointing, so the request-log queries phase 7
adds must be short-lived. Retention deletes do not shrink the file; `auto_vacuum=incremental` with a
periodic step keeps growth bounded.

### 3.2 One write path

All writes go through a single write handle configured with `MaxOpenConns(1)`, shared by the log
writer, the rollup worker, the retention worker, the health persister, and the first-run import.
SQLite permits one writer; letting many goroutines contend produces `SQLITE_BUSY` under exactly the
load where you least want it. Readers use a separate pooled handle, which WAL makes safe against the
writer.

Retention therefore deletes in bounded batches with a pause between them not to avoid locking out
"the writer" — it *is* the writer — but to yield the handle so log batches are not starved behind a
long prune.

### 3.3 Migrations

Embedded, versioned, forward-only, each in its own transaction, gated by a `schema_version` row. The
recovery story for a single-operator homelab is a file copy, not a reversible migration.

A database whose version is **newer** than the binary fails startup loudly. That happens on a
rollback deploy, and running an old binary against a new schema corrupts quietly.

### 3.4 Money and time

Costs are `int64` micro-dollars per million tokens for prices, and `int64` micro-dollars for
realized cost. No floating point touches money at any layer. `cost_micros` is NULL — not zero — until
pricing for that model exists, which is phase 6.

`usage_daily.day` is UTC, keyed on request start. Finalization is idempotent recomputation, so a
request spanning midnight lands once in the day it began.

## 4. Credential encryption

`DARKROUTER_MASTER_KEY` → PBKDF2-HMAC-SHA256 → a 32-byte AES key → AES-GCM per stored credential with
a fresh random nonce and the credential row id as **additional authenticated data**, so a ciphertext
cannot be swapped between rows undetected.

The salt and the iteration count are generated on first run and stored in `settings`. Storing the
count is what allows raising it later without breaking existing databases. The default is 600,000
iterations, which runs once at startup and costs nothing there, and matters because the master key may
be a human passphrase — nothing forces it to be 32 random bytes, and a low count makes offline attack
of a stolen database file cheap.

A KDF verifier — a known plaintext encrypted under the derived key, also in `settings` — lets startup
detect a wrong or changed master key and fail loudly rather than emitting garbled credentials at
request time. GCM authentication makes that detection reliable.

Credentials are decrypted once when the provider set is loaded and held in memory, not per request. A
ciphertext that fails authentication surfaces at load time as a provider-level error rather than as a
mysterious request failure, and the startup verifier only proves the master key is right, not that
every row is intact.

### 4.1 Rotation

`darkrouter rotate-key` is a CLI subcommand, not an API endpoint — rotation needs both the old and
new keys simultaneously, which an endpoint authenticated by the running process cannot supply. It
reads the old key from `DARKROUTER_MASTER_KEY` and the new one from stdin, generates a fresh salt,
re-encrypts every credential, and rewrites the verifier in one `synchronous=FULL` transaction. A
crash mid-rotation rolls back; credentials are never half-rotated.

## 5. Provider source moves to SQLite

The `provider.Source` interface from phase 1 gains a SQLite implementation.

The first-run import runs when **all** of: no marker row exists in `settings`, the `providers` table
is empty, and `darkrouter.yaml` still carries a `providers:` block. All inserts and the marker are one
transaction — otherwise a crash mid-import leaves some providers present, falsifies the empty-table
guard, and silently strands the rest.

Import resolves `${ENV}` references and encrypts the resulting keys, so `DARKROUTER_MASTER_KEY`
becomes mandatory at the phase 1 → 2 upgrade boundary. An unresolvable reference aborts the whole
import with a clear message rather than importing a provider with no credential.

After import, a `providers:` block left in the file is still schema-validated (so a stale broken block
is caught) but semantically ignored, with a startup warning naming the date of the import. Editing it
and expecting effect is the obvious mistake, and silence would be the wrong response.

## 6. Request logging

Logging never slows or blocks a request. The handler builds a complete record in memory, sends it on
a buffered channel, and returns. A background writer batches records into one transaction on a short
timer or when the batch fills.

A full channel **drops the record** and increments a counter exposed on `/healthz` and `/metrics`.
Blocking the request path to guarantee a log line is the wrong trade.

The consequence must be stated rather than left implicit: the log is the sole input to `usage_daily`,
so dropped records mean **spend figures are a lower bound**. The drop counter reports how many
records vanished, not how many tokens or dollars.

The done criterion in §11 is qualified accordingly. Request ids are application-generated (ULID) so
that `X-Darkrouter-Request` can be returned even for a record that is later dropped.

On shutdown the writer drains the channel before exiting. Without that, every graceful restart loses
a channel's worth of records and the drop counter lies by omission.

## 7. Health and the circuit breaker

State is keyed on `(provider_id, key_id, model)`, plus a credential-level entry with a NULL model for
cooldowns that apply across every model a credential serves.

**In-memory is authoritative on the hot path.** A mutex-guarded map answers availability without
touching SQLite, because the router consults it on every request. Changes persist asynchronously,
debounced so a provider failing fast does not generate a write per failure, are flushed on shutdown,
and are rehydrated at startup.

Rehydration filters entries whose `cooling_until` has passed but **retains** `consecutive_failures`,
so a provider that was flapping before a restart does not get a clean slate.

### 7.1 Trip and recovery rules

| Signal | Effect |
|---|---|
| 429 with `Retry-After` | Cool the triple for that duration, clamped to `policy.cooldown.max`. |
| 429 without `Retry-After` | Cool the triple by the ladder. |
| Other retryable provider failures — 5xx, 408, timeout, connection reset or refused, DNS, TLS, HTTP/2 resets | Increment consecutive failures; cool by the ladder once `trip_after` is reached. **A single failure does not cool.** |
| 401, 402, 403 | Cool the credential-level entry across all models. Never resets any ladder. |
| `Success` or `Fatal` | Reset the ladder for that exact triple. |
| `ClientCancelled` | No effect on any health state. |

The ladder runs 1s, 2s, 4s, 8s, 15s, 30s, 60s, 120s and continues doubling until clamped at
`policy.cooldown.max`.

Resetting on `Fatal` as well as `Success` is deliberate: a 400 proves the provider is reachable and
functioning, and says something about the request rather than the provider.

Equally deliberate is that `RetryableCredential` never resets. An earlier draft said "any response
proving reachability, including 400, resets the ladder" while also cooling on 401/402/403 — two rules
firing oppositely on the same event. A billing-exhausted key cooled by a 402 would then be resurrected
by any client's malformed request and retried indefinitely.

Clamping `Retry-After` matters too: a provider sending `Retry-After: 86400` would otherwise remove
itself for a day while the ladder path clamps at fifteen minutes.

### 7.2 Half-open

On expiry a triple becomes half-open. Exactly one probe is admitted through an atomic
compare-and-swap on a `probing` flag; concurrent requests at expiry see the candidate as unavailable
rather than all becoming probes. A probe classified `Success` or `Fatal` closes the breaker; any
retryable outcome re-trips at the next ladder level and clears the flag.

A `Retry-After` cooldown never tripped a ladder, so it closes on expiry without a probe.

### 7.3 Cost of a fully dead provider

Triple-keying means a provider with N models and K credentials needs up to `trip_after × N × K`
failures to cool completely. At homelab scale this is accepted rather than solved; the overview
surfaces "many triples cooling on one provider" as a provider-level signal so the operator sees the
real cause.

## 8. Background workers

Four, all with jittered intervals, all draining cleanly on shutdown in the order given in master
design §16:

- **Log writer** — batches request and attempt rows; drains the channel on shutdown.
- **Health persister** — debounced writes of the in-memory map; flushes on shutdown.
- **Rollup** — hourly, recomputing today and idempotently finalizing yesterday in UTC.
- **Retention** — prunes `requests` and `request_attempts` past `log.retention` and `request_bodies` past `expires_at`, in bounded batches, followed by an incremental vacuum step.

## 9. Client cancellation

The upstream request context derives from the inbound one, so a client hanging up surfaces as
`context.Canceled` — indistinguishable at the transport layer from a deadline the executor itself
imposed. Contexts are therefore built with `context.WithCancelCause` and `context.WithTimeoutCause`,
and classification checks the cause: an inbound-originated cancel is `ClientCancelled` and touches no
health state; a Darkrouter-imposed deadline is a provider timeout and does.

Without this, pressing Ctrl-C in Claude Code trips breakers on perfectly healthy providers.

## 10. Testing

Store tests run against a temporary database and cover migration application from empty, idempotency
on restart, refusal to start against a newer schema, and per-connection pragma application across the
pool.

Crypto tests cover round-trip encryption, fresh nonces, AAD binding rejecting a swapped ciphertext,
wrong-master-key detection via the verifier, and an interrupted rotation rolling back.

Import tests cover the three-part predicate, single-transaction atomicity under an injected
mid-import failure, and abort on an unresolvable environment reference.

Breaker tests are table-driven over §7.1, including `Retry-After` in both delta-seconds and HTTP-date
forms, the clamp, ladder escalation past 120s, that a single 5xx does not cool, that a 402 followed by
a 400 leaves the credential cooling, and half-open admitting exactly one probe under concurrent
access.

A restart test asserts cooldowns and failure counters survive a graceful shutdown. A cancellation test
asserts a client disconnect leaves health untouched while a deadline does not.

Retention tests assert batched deletion of a large backlog does not starve the log writer.

## 11. Done criteria

- Provider connections and credentials survive a restart; credentials are unreadable in the raw database file and a swapped ciphertext fails to decrypt.
- A wrong `DARKROUTER_MASTER_KEY` fails startup with a clear message; `darkrouter rotate-key` re-encrypts everything atomically.
- Every request produces one `requests` row and one `request_attempts` row per attempt, **except under log-channel saturation**, where the drop counter reports the shortfall and spend is a documented lower bound.
- A provider returning 429 is recorded as cooling and the cooldown survives a restart; three consecutive 5xx are required before cooling, and one is not.
- A client disconnect leaves every provider healthy.
- Log writing under sustained load does not increase request latency.
- `go test ./...` passes.
