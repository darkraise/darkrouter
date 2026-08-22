# Darkrouter Progress

Last updated: 2026-08-22

## Phase status

| Phase | Spec | Plan | Status |
|---|---|---|---|
| 1 — Foundation | ✅ | ✅ | **Complete.** Race-clean, `go vet` clean, Docker image verified, all four manual checks passed. |
| 2 — Persistence and health | ✅ | ✅ | **Implemented** on branch `phase-2-persistence-health`. 18 tasks, all done criteria met. |
| 3 — Routing and failover | ✅ | — | Not started |
| 4 — Dialects | ✅ | — | Not started |
| 5 — Auxiliary surfaces | ✅ | — | Not started |
| 6 — Catalog | ✅ | — | Not started |
| 7 — Admin API and UI | ✅ | — | Not started |
| 8 — Signed and OAuth credentials | ✅ | — | Not started |
| 9 — Passthrough fast path | ✅ | — | Not started |

Specs live in `docs/superpowers/specs/`; read its `README.md` first for the
dependency graph. Plans live in `docs/superpowers/plans/`.

## Build environment

The Linux machine needs Go 1.26.1 at `/usr/local/go` (add `/usr/local/go/bin` to
`PATH`) and gcc, which `-race` requires via cgo. `CGO_ENABLED` defaults to 1
there, unlike the original Windows machine.

## Open items

### 1. Race detector — done, clean

Run on Linux with Go 1.26.1 and `CGO_ENABLED=1`:

```bash
go test -race -count=1 ./...
```

**No data races in any package**, including the three tasks scored risk 3
(`internal/config/store.go`, `internal/exec/exec.go`, `internal/server/server.go`).
`internal/server` and `internal/exec` were additionally run at `-count=5` under
`-race` because of the shutdown-lifecycle rework in 8b0b81d and ec207d2.

Caveat worth carrying forward: the detector only observes interleavings the
tests actually schedule. This says the concurrency is clean under current test
coverage, not under load. Phase 2 adds background workers, so re-run it there.

The run did surface two non-race defects, both fixed:

- **`TestWatchDetectsRenameStyleSave` failed deterministically on Linux** (20/20),
  with and without `-race`. The test started the watcher goroutine and
  immediately saved; inotify does not replay events that predate the watch, so
  the save was never seen. `Store.Watch` had no readiness signal, so the test
  could not synchronize. Fixed in 223bde9 by adding an internal
  `watch(ctx, ready)` that closes `ready` once the directory watch is
  established. It passed on Windows only because slower file I/O let the
  goroutine win the race.
- **`cmd/darkrouter/main.go` did not exist.** The repository had no `main`
  package at all, so "binary builds" was never true — `go build ./...` compiles
  only libraries. Root cause: `.gitignore` line 1 was an unanchored
  `darkrouter`, which matched the `cmd/darkrouter/` *directory* as well as the
  built binary, making `git add cmd/` a silent no-op. Fixed in 72887fe
  (anchor to `/darkrouter`) and 71a6a50 (add the entrypoint).

### 2. Docker image — done, verified

Image builds and serves; 28.8 MB.

```bash
docker build -t darkrouter:dev .
```

Verified end to end: `/readyz` 200, `/healthz` reports `config_valid: true`,
`/v1/models` returns the configured provider's models, and SIGTERM shuts the
container down in ~1.3 s rather than hitting Docker's 10 s SIGKILL.

`compose.yml` was also unexercised and is now verified, including its `wget`
healthcheck reaching `healthy`. Note when smoke-testing on a machine that
already serves 8080/8081: compose *appends* to `ports` rather than replacing,
so a port override file needs the `!override` tag.

### 3. Manual checks against a real provider — done

All four verified against Groq on 2026-08-22 with `openai/gpt-oss-120b`.

- ✅ **A vim-style save hot-reloads without a restart.** Write-temp-then-rename
  over the original; a restart-only change appeared in `/healthz` `warnings`
  within the debounce window.
- ✅ **An invalid edit is rejected and the gateway keeps serving** on the
  previous config, with `/healthz` reporting `config_valid: false` and the parse
  error. `/readyz` correctly stays 200, matching the phase 1 spec's "`/readyz`
  fails only when the process cannot serve at all".
- ✅ **Tokens arrive incrementally, with time-to-first-token at or below the
  provider's own.** Three interleaved samples, same prompt and model:

  | Sample | Direct to Groq | Through gateway |
  |---|---|---|
  | 1 | 739 ms | 559 ms |
  | 2 | 742 ms | 551 ms |
  | 3 | 674 ms | 510 ms |

  The gateway is *faster* because `exec`'s shared `http.Transport` keeps a warm
  connection to Groq, while each direct call pays a fresh TLS handshake. Content
  chunks spread over ~170 ms in both cases, so nothing is being buffered.
- ✅ **The streamed response reports token usage, and the injection is what
  causes it.** Verified against a local capture upstream rather than inferred:
  a client request carrying no `stream_options` was sent upstream as
  `{"stream": true, "stream_options": {"include_usage": true}, ...}`. This
  matters because Groq returns usage whether or not it is asked, so a live call
  alone cannot distinguish injection from provider default.

Note the example configuration previously named `llama-3.3-70b-versatile`, which
Groq has decommissioned — any new user following the README got a 404. Updated
to `openai/gpt-oss-120b` in `darkrouter.example.yaml` and `README.md`. The
config fixtures in `internal/config/load_test.go` still use the old name, which
is harmless: they never leave the process.

### 3b. Two wire-format notes for phase 9

Neither is a phase 1 defect — phase 1's spec requires only that the adapter
inject `stream_options` — but phase 9's differential suite compares IR output
against passthrough byte for byte, and both will surface there.

- **The IR path emits a usage chunk the client never asked for.** Master design
  §4.2 requires the injected chunk to be stripped so "the client's view is
  identical to what it would have received directly", but scopes that to the
  passthrough path. On the IR path the gateway currently forwards usage
  unconditionally. OpenAI sends that chunk only when the client sets
  `stream_options.include_usage`.
- **The usage chunk carries a synthesized choice.** `chunk()` in
  `internal/edge/openai/stream.go:14` always builds `choices: [{index, delta,
  finish_reason}]`, so the usage chunk goes out with one empty-delta choice
  where OpenAI emits `choices: []`.

### 4. One design decision still open

The rerank wire shape (findings ledger §2.3). Specs currently adopt Cohere v2
with a preset-declared path. Revisit before Phase 5.

## Phase 1 deviations from spec

Recorded so Phase 2 does not trip over them:

- **`policy.timeout.idle` is parsed but unenforced.** Phase 1 applies `total` to the whole request. Design §8.2 wants committed streams governed by `idle` instead, which needs Phase 3's commit machinery.
- **`connect` and `first_byte` are restart-only.** They configure a shared `http.Transport` built once in `exec.New`, which cannot vary them per request. `max_body_bytes` is *not* restart-only — the executor reads it from a per-request snapshot.
- **The lossy-field warning mechanism does not exist yet.** Master design §5 requires dropped fields to be recorded; `requests.warnings` arrives in Phase 2 and the mechanism in Phase 4. Phase 1's adapter drops silently.
- **No request logging at all.** The done criterion "a client disconnect is distinguishable from a timeout in logs" is unmet: nothing in the request path logs. Phase 2 owns this.

## Review history

| Artifact | Reviewers | Outcome |
|---|---|---|
| All 10 specs | 5 × Fable, read-only | ~150 findings → `docs/superpowers/specs/2026-08-22-spec-review-findings.md`, all specs revised |
| Task 13 (`internal/exec`) | 1 × Fable, read-only | Concurrency core sound; 6 defects fixed in 8b0b81d |
| Task 14 (`internal/server`) | 1 × Fable, read-only | 8 defects fixed in 8b0b81d, including a drain deadline that did nothing |
