# Darkrouter Progress

Last updated: 2026-08-22

## Phase status

| Phase | Spec | Plan | Status |
|---|---|---|---|
| 1 — Foundation | ✅ | ✅ | **Implemented.** 86 tests, `go vet` clean, binary builds. Race verification outstanding — see below. |
| 2 — Persistence and health | ✅ | — | Not started |
| 3 — Routing and failover | ✅ | — | Not started |
| 4 — Dialects | ✅ | — | Not started |
| 5 — Auxiliary surfaces | ✅ | — | Not started |
| 6 — Catalog | ✅ | — | Not started |
| 7 — Admin API and UI | ✅ | — | Not started |
| 8 — Signed and OAuth credentials | ✅ | — | Not started |
| 9 — Passthrough fast path | ✅ | — | Not started |

Specs live in `docs/superpowers/specs/`; read its `README.md` first for the
dependency graph. Plans live in `docs/superpowers/plans/`.

## Open items

### 1. Race detector has never run — highest priority

`go test -race` requires cgo and a C toolchain. The development machine is
Windows with `CGO_ENABLED=0` and no gcc, so **no code in this repository has
ever been checked for data races.** That gap covers exactly the three tasks
scored risk 3:

- `internal/config/store.go` — atomic config swap and the fsnotify watcher goroutine
- `internal/exec/exec.go` — context cancellation across the streaming path
- `internal/server/server.go` — shutdown ordering, the lifecycle context, three goroutines

On a Linux machine this is one command:

```bash
go test -race ./...
```

Run it before starting Phase 2. If it reports anything, fix it there — the
server's lifecycle-context change (commit 8b0b81d) added goroutine interactions
that no automated check has seen.

### 2. Docker image never built

`Dockerfile` and `compose.yml` are written but unexercised; the Docker daemon was
not running. Verify with:

```bash
docker build -t darkrouter:dev .
mkdir -p data && cp darkrouter.example.yaml data/darkrouter.yaml
docker run --rm -d --name dr-smoke -p 8081:8081 -e GROQ_KEY=placeholder \
  -v "$PWD/data:/data" darkrouter:dev
curl -sf http://localhost:8081/readyz && echo OK
docker rm -f dr-smoke
```

### 3. Manual checks against a real provider

The plan's done criteria that no test covers:

- A streaming `curl` returns tokens incrementally, with time-to-first-token close to the provider's own.
- The streamed response reports token usage (proves the `stream_options` injection works against a real upstream).
- Editing `darkrouter.yaml` from vim changes behavior without a restart. `TestWatchDetectsRenameStyleSave` covers the mechanism; this confirms it end to end.
- An invalid edit is rejected, the gateway keeps serving, and `/healthz` shows `config_valid: false` with the error.

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
