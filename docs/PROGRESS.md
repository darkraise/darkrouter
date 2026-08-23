# Darkrouter Progress

Last updated: 2026-08-23

## Phase status

| Phase | Spec | Plan | Status |
|---|---|---|---|
| 1 — Foundation | ✅ | ✅ | **Complete.** Race-clean, `go vet` clean, Docker image verified, all four manual checks passed. |
| 2 — Persistence and health | ✅ | ✅ | **Merged to master.** 18 tasks, all done criteria met. |
| 3 — Routing and failover | ✅ | ✅ | **Merged to master.** 20 tasks, all done criteria met. |
| 4 — Dialects | ✅ | ✅ | **Complete.** 37 tasks; race-clean, verified live against Groq. |
| 5 — Auxiliary surfaces | ✅ | ✅ | **Complete.** 34 tasks; race-clean, all seven surfaces served. |
| 6 — Catalog | ✅ | ✅ | **Complete.** 26 tasks; race-clean, verified live against Groq. |
| 7 — Admin API and UI | ✅ | — | Not started |
| 8 — Signed and OAuth credentials | ✅ | — | Not started |
| 9 — Passthrough fast path | ✅ | — | Not started |

Specs live in `docs/superpowers/specs/`; read its `README.md` first for the
dependency graph. Plans live in `docs/superpowers/plans/`.

## Build environment

The Linux machine needs Go 1.26.1 at `/usr/local/go` (add `/usr/local/go/bin` to
`PATH`) and gcc, which `-race` requires via cgo. `CGO_ENABLED` defaults to 1
there, unlike the original Windows machine.

## Closed by phase 4

Two items phase 3 carried forward are done:

- **`edge.Passthrough.Surface` is now `ir.Surface`**, typed before either new
  dialect wrote a producer of that struct.
- **The per-dialect in-stream error shape is defined and tested.** OpenAI sends
  `data: {"error":…}` then `[DONE]`; Anthropic sends a real `error` event and no
  `message_stop`; Gemini, whose SSE has no error event type at all, sends a
  terminal chunk carrying a `promptFeedback`-shaped object. One golden fixture
  drives all three.

## Carried forward into phase 5 and beyond

- **Failed attempts burn tokens invisibly.** `request_attempts` carries no usage
  columns, so tokens spent by failed pre-commit attempts never reach
  `usage_daily`. Spend figures understate reality whenever failover fires.
- **A refusal reaches the client as a hard error, not a refusal.** A Gemini
  blocked prompt is HTTP 200 with `promptFeedback.blockReason` natively and 400
  `INVALID_ARGUMENT` through Darkrouter; an Anthropic `refusal` is a 200 with
  `stop_reason: "refusal"` natively and a 400 `invalid_request_error` through
  Darkrouter on the unary path. This follows from master design §8.1 classifying
  content filter as `Fatal` and §14 normalizing into the inbound dialect, so it
  is deliberate — but Gemini CLI and Claude Code surface it as a failure rather
  than as the model declining. Worth knowing before someone files it as a bug.
- **The token estimate ignores media and is not the provider's tokenizer.**
  `X-Darkrouter-Estimated: true` says so, but a client budgeting a context
  window around a large image will be wrong.
- **Gemini media inlining fetches client-supplied URLs.** Bounded to http and
  https, no redirects, 20 MB, ten-second timeout — but it is outbound traffic
  the gateway initiates on a client's behalf. Phase 7's settings screen should
  be able to turn it off.
- **`Adapter.Surfaces()` from master design §5.1 still does not exist.** The
  interface has no way to say which surfaces a kind implements, so routing
  cannot exclude a provider on that basis. Phase 5 is where it becomes
  load-bearing.
- **Phase 8 extends the golden suite rather than replacing it.** Add `bedrock`
  and both `vertex` publisher variants to `adapters()` in
  `internal/golden/golden_test.go`, regenerate, and read the new files.

Four smaller items are listed at the end of
`docs/superpowers/plans/2026-08-22-phase3-routing-failover.md`.

## Closed by phase 6

Four items phase 4 carried forward are done:

- **Capability filtering is selective.** models.dev supplies real capability
  data and Ollama's `/api/show` supplies discovered data, so a model known to
  lack tools is no longer a candidate for a tool request. Inferred models still
  route, per master design §6.4, and now carry a `capabilities` warning on the
  request row so the trace explains why.
- **The Anthropic model-generation table is deleted.** `var generations` and
  `func traitsFor` are gone from `internal/adapter/anthropic/build.go`. Traits
  are preset-declared data reaching the adapter on `adapter.Target.Info`, and
  `internal/catalog/traits_test.go` asserts the shipped preset produces phase
  4's live-verified traits for fourteen real model names, dotted proxy
  spellings included.
- **The Anthropic `max_tokens` substitution reads the catalog.** It is the
  model's real `max_output_tokens`, and a request asking for more than the model
  can produce is clamped with a warning rather than forwarded to a 400.
- **`xlate.EffortBudget`'s clamp is live.** Callers pass
  `t.Info.MaxOutputTokens`.

**The two stale phase 4 spec assumptions are corrected in place.** Spec §4.6 now
states that structured output is GA under `output_config.format` and that
extended thinking has split into two mutually exclusive per-generation shapes,
with the narrower sampling rule spelled out; §4.7 records that `max_tokens` is
still required and now substitutes the catalog's value. The findings ledger's
"could not verify" note is corrected too.

## Carried forward from phase 6

- **`presets.yaml` is generated, and regenerating it needs the OmniRoute
  checkout.** `tools/presetgen` reads `/root/repositories-community/OmniRoute`
  and a models.dev snapshot. Corrections belong in
  `internal/catalog/presets.overrides.yaml`, which is re-applied on every run;
  editing `presets.yaml` directly is discarded by the next run.
- **OAuth presets are not transcribed.** Spec §2 expected them with their
  `oauth:` blocks defined. Of OmniRoute's 21 `authType: oauth` entries only 7
  carry any literal URL and none carries a complete block — `claude`, the best
  case, has a token URL but no authorize URL, and its client id sits behind a
  `resolvePublicCred()` call. Shipping an entry missing them would show a
  provider in the UI that cannot be connected, so the 13 that survived the other
  filters were dropped and only `anthropic-oauth`, whose values are verifiable,
  is hand-written. Sourcing the rest is phase 8 work.
- **The embedded metadata snapshot ages.**
  `internal/catalog/models_snapshot.json` was taken on 2026-08-22. It is only
  the cold-start fallback — the sync overwrites it within twelve hours of a
  networked start — but a long-lived offline install runs on those numbers
  indefinitely. Regenerate it when the binary ships.
- **Vertex and Bedrock have no discovery.** `ProbeFor` returns
  `ErrKindNotDiscoverable` for both, so their models come from presets and
  models.dev alone. Bedrock's two control-plane calls arrive with its adapter in
  phase 8; Vertex has no practical listing API and never will.
- **The models.dev join is best-effort.** 49 of the 196 shipped presets join a
  models.dev provider key by exact id and 9 more by hand-written override; the
  rest carry `no_models_dev: true` and their models fall back to inferred
  capabilities. Adding a `models_dev_id` to `presets.overrides.yaml` is the fix,
  one provider at a time.
- **Discovery probes every enabled provider on every tick regardless of how
  static it is.** An `anthropic` provider's model list changes a few times a
  year and is probed ninety-six times a day. Harmless, but phase 7's settings
  screen is where a per-provider interval would belong.
- **Failed attempts still burn tokens invisibly.** `request_attempts` carries no
  usage columns, so tokens spent by failed pre-commit attempts never reach
  `usage_daily`. Untouched by phase 6.

## Closed by phase 5

- **`Adapter.Surfaces()` exists**, closing the item phase 3 carried and phases 4
  and 6 both deferred. Routing now excludes a provider whose kind Darkrouter
  cannot speak the surface to, as a filter rather than a runtime error. An
  adapter that declares nothing serves `llm` only, which is the honest default.
- **The surface vocabulary matches master design §6.** Seven values, with `tts`
  and `stt` separate rather than collapsed into one `audio`, and every shipped
  preset declares them in the corrected spelling.
- **A YAML-configured provider can reach its preset.** `providers.preset` has
  existed since migration 0001 and nothing had ever written it: the loader
  rejected the key, `YAMLSource` dropped it, and `ImportFromConfig` omitted the
  column. Phase 6 recorded the symptom and filed it as a phase 7 UI concern; it
  was three lines of plumbing, and rerank could not be served by any configured
  provider without it. Fixing it also activated `catalog.OrphanedPresets`, which
  could never fire for a YAML provider before.
- **A phase 6 merge defect was fixed on the branch** (`f6ae00c`):
  `merge.surfaces` resolved override → row → preset, but discovery hardcodes
  `'["llm"]'` into every row it inserts and the sync echoes it, so the row always
  shadowed the preset and widening a preset had no effect on any discovered
  model. The preset now outranks the row; an operator override still wins.
- **A reasoning-block indexing defect in shipped code was fixed.**
  `internal/adapter/openaicompat/parse.go` emitted reasoning deltas with no
  block index — the zero value, which is the text block's — and no open or close
  events, while tool blocks were carefully offset by 1000 to avoid exactly that.
  It was invisible because every consumer until the Responses stream writer
  switched on the delta's type and ignored the index.

## Carried forward from phase 5

- **Cost is still never computed.** `applyUsage` leaves `CostMicros` nil on
  every surface including chat, although phase 6 shipped `catalog.Pricing` with
  real per-MTok numbers. Nothing in phase 5 changed that, and the item below is
  why it was not simply switched on.
- **`ir.Usage.InputTokens` does not mean the same thing across adapters.**
  Anthropic's `input_tokens` **excludes** cache read and write tokens; OpenAI's
  `prompt_tokens` and Gemini's `promptTokenCount` **include** them. Each adapter
  copies its provider's own convention into the same field. Any cost formula
  written today is therefore wrong for at least one family — it either
  double-charges cached input or under-charges it — so the IR has to normalize
  before pricing can be turned on. This is the blocker for the item above, and a
  real defect in the existing usage plumbing rather than a phase 5 omission.
- **`capture.bodies` has no writer.** The `request_bodies` table exists from
  phase 2 and the retention sweep prunes it, but nothing has ever inserted a
  row. The setting, its `max_bytes` and its `retention` are all inert. Spec §5's
  "a speech response is never captured even when `capture.bodies` is on" is
  therefore satisfied by construction rather than by enforcement — what phase 5
  does enforce is that the body is never held whole, which is the property that
  matters.
- **Per-call image pricing has no catalog source.** Spec §9 says cost should
  come from per-call or per-unit pricing where no usage arrives, but
  `catalog.Pricing` carries only per-MTok rates and models.dev supplies nothing
  else. A dall-e call therefore records no cost at all, which is correct but
  incomplete.
- **Responses fields the IR does not model are dropped rather than re-emitted.**
  Spec §5 says they "ride in `Extra` and are re-emitted"; `truncation`,
  `include`, `service_tier`, `top_logprobs`, `max_tool_calls` and
  `prompt_cache_key` are instead parsed away without a warning. The response
  echoes the fields the OpenAI SDK's model requires — tools, tool choice,
  sampling, instructions, metadata — which is what keeps a client working; the
  rest are a documented deviation, not an oversight. `truncation` and
  `max_tool_calls` change the answer's shape, so a client setting them gets
  behavior it did not ask for with nothing in the trace saying so.
- **Responses ids are not resolvable, by design.** Returned ids carry a
  `resp_dr_` prefix and `store: false`; any request echoing one back is refused.
  A client built around server-side conversation state will not work against
  Darkrouter and is told so explicitly rather than served an amnesic answer.

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

### 5. Phase 6 verification against Groq — done

Run on 2026-08-23 with a static `CGO_ENABLED=0` binary (29 MB, unchanged from
phase 4 — the embedded metadata snapshot costs 667 KB) on ports 18080/18081.

- **Discovery found 13 models** against the one the configuration named, with
  zero consecutive failures and every row `live` at `missing_streak` 0. The
  configured `models:` list is no longer what the gateway can serve.
- **The sync priced 6 of them**, the rest being audio and guard models
  models.dev carries no `cost` for. `openai/gpt-oss-120b` came back at
  `input 150000` and `output 600000` micro-dollars per million — $0.15 and
  $0.60 — with `context_window 131072`, `max_output_tokens 65536`, and
  capabilities `tools:true, reasoning:true, vision:false`. All 13 rows ended
  `capabilities_source = models_dev`.
- **The join needs the provider row's `preset`.** A provider imported from the
  YAML `providers:` block has an empty one, so nothing joins until it is set.
  That is correct — an uncatalogued provider is a base URL and a key — but it
  means a first-run import gets no metadata until phase 7's UI sets a preset, or
  the operator does it by hand.
- **`warnings_json` was checked in both directions, which is the point.** With
  the preset set, models.dev vouches for tools and a tool request records **no**
  capability warning. With the preset cleared so the join fails, the same
  request records
  `capabilities -> groq/openai/gpt-oss-120b: the request needs tools and this
  model's capabilities are unverified; routed anyway` — and still succeeds, per
  master design §6.4. The mechanism reaches the request row in production, not
  only in tests.
- **A cold start with no models.dev access serves.** Fresh database,
  `models_dev_url` pointed at a closed port: `/readyz` 200, both listings serve,
  a live completion returns, and the log carries one warning ending
  "(serving embedded metadata)". With a preset set, `/v1beta/models` reported
  `inputTokenLimit 131072` and `outputTokenLimit 65536` from the **embedded
  snapshot alone** — the fallback supplies real numbers rather than only letting
  the process boot.

One process-hygiene note for the next session: `nohup … &` inside a compound
command returns the subshell's pid, not the binary's, so `kill "$!"` leaves the
gateway holding its port and the next start fails to bind. Kill by binary name
(`ps -C darkrouter -o pid=`) instead.

### 4. One design decision, now settled

The rerank wire shape (findings ledger §2.3). Settled in phase 5: exactly one
shipped preset declares a `rerank` surface, `cohere`, and neither Jina nor
Voyage is a preset at all. Cohere v2 is therefore not merely the recommendation
but the only shape any shipped provider serves, at the path its preset
declares. No revisit is planned.

## Phase 1 deviations from spec

Recorded so Phase 2 does not trip over them:

- **`policy.timeout.idle` is parsed but unenforced.** Phase 1 applies `total` to the whole request. Design §8.2 wants committed streams governed by `idle` instead, which needs Phase 3's commit machinery.
- **`connect` and `first_byte` are restart-only.** They configure a shared `http.Transport` built once in `exec.New`, which cannot vary them per request. `max_body_bytes` is *not* restart-only — the executor reads it from a per-request snapshot.
- **The lossy-field warning mechanism does not exist yet.** Master design §5 requires dropped fields to be recorded; `requests.warnings` arrives in Phase 2 and the mechanism in Phase 4. Phase 1's adapter drops silently.
- **No request logging at all.** The done criterion "a client disconnect is distinguishable from a timeout in logs" is unmet: nothing in the request path logs. Phase 2 owns this.

### 6. Phase 5 verification against Groq — done

Run on 2026-08-23 with a static `CGO_ENABLED=0` binary (30 MB) on ports
18080/18081, one `groq` provider carrying `preset: groq`.

**Groq serves three of the seven surfaces.** It offers chat and
`/v1/audio/transcriptions` on its OpenAI-compatible base URL, and no
embeddings, images, rerank or moderations endpoint. Those four were verified
only as the no-provider error, which is itself a done criterion rather than a
skipped check. Verifying them live needs an OpenAI key (embeddings, images,
moderations) and a Cohere key (rerank).

- **Chat is unmoved by the `runAttempts` refactor.** A completion came back with
  `X-Darkrouter-Attempts: 1`, `-Provider: groq`, `-Model: openai/gpt-oss-120b`
  and a request id. 73 input tokens, 66 output.
- **The Responses API serves unary and streamed.** The unary body carried
  `"object":"response"`, `"status":"completed"`, `"store":false`, one `message`
  item and an `output_text`, with a `resp_dr_`-prefixed id. The stateful case
  returned 400 naming `previous_response_id`.
- **The streamed Responses events are well-formed against a real provider.**
  Thirteen frames, sequence numbers contiguous 0..12, every `event:` name
  matching its `data.type`, `response.output_item.done` before
  `response.completed`, and no `[DONE]`. The terminal event carried
  `input_tokens: 74, output_tokens: 66` — non-zero, which is the check that
  matters: Groq sends its usage chunk after the finish chunk, so completing on
  `message_stop` would have reported zero here and passed every unit test.
- **Transcriptions serve both response shapes.** `response_format=json` returned
  `application/json` with a `text` field; `response_format=text` returned
  `text/plain; charset=UTF-8`. The second call sent `model` after the file part,
  which is the placement spec §6 calls out, and the in-form rewrite handled it.
  Both request rows carry `file_name: tone.mp3` and a real `response_bytes`.
- **Speech could not be verified end to end, for a provider reason.** Groq has
  decommissioned `playai-tts` and its models list now offers no TTS model at
  all, so the surface has no live target. What was verified is that the route
  routes: the request reached the provider (one attempt, status 400, outcome
  `fatal`), and the upstream error was normalized into the inbound dialect. A
  direct call to Groq with the same body returns the same 400
  (`model_decommissioned`), so this is not a Darkrouter fault. The streaming
  behavior remains covered by `TestSpeechIsNeverHeldWhole`, which deadlocks
  rather than passes if the body is buffered.
- **The four unserved surfaces return 404 and attempt nothing.** Each body reads
  "no configured provider offers this model on this surface", and the request
  rows show `not_found` with **zero attempts** — the filter runs before any
  provider is contacted, which is what spec §4 exists to guarantee.
- **The request rows were read, not assumed.** `openai-responses` appears as a
  dialect distinct from `openai`; the `stt` rows carry `response_bytes` and
  their surface meta; `warnings_json` is `[]` on every row, and `request_bodies`
  is empty. Phase 6's verification caught a mechanism that passed every test and
  did nothing in production by reading this column, which is why it is read here
  rather than trusted.

Both test ports were released and no process was left running. Ports 8080 and
8081 were never touched.

### 7. Phase 7 API verification against Groq — done

Run on 2026-08-23 with a static `CGO_ENABLED=0` binary on ports
18080/18081. The frontend is not built yet; the API was exercised with `curl`.

- **The API is closed before login.** `overview`, `providers`, `models`,
  `requests`, `usage`, `config` and `presets` all returned 401.
  `auth/status` returned 200 with `{"authenticated":false}`, which is the one
  endpoint the SPA needs open to decide whether to render the login screen.
- **The session cookie is shaped correctly.** `Set-Cookie:
  darkrouter_session=…; Path=/; Max-Age=2592000; HttpOnly; SameSite=Lax`, with
  `Secure` **absent** — this is plain HTTP, and a Secure cookie here would be
  dropped by the browser and login would silently never work.
- **CSRF and Origin both hold against a real client.** No token → 403; correct
  token with `Origin: https://evil.example` → 403; correct token with neither
  `Origin` nor `Sec-Fetch-Site` → 403; correct token with
  `Sec-Fetch-Site: same-origin` → 200. The third is the one worth stating: a
  client sending neither header is refused, so the check is not decorative.
- **The proxy port ignores the admin cookie.** With `server.proxy_token` set,
  a chat request carrying the admin session cookie and no bearer token returned
  401; the same request with the bearer token returned 200. Cookies are not
  port-scoped, so this is the property that keeps a logged-in operator's browser
  from being an authenticated proxy client for any page they visit.
- **Provider CRUD and the probe work end to end.** Created `groq2` from the
  `groq` preset (201), added a credential (201), probed it: `ok: true`,
  `model_count: 13`, `latency_ms: 182` against the real Groq listing endpoint.
- **No credential material reached any response.** The key does not appear in
  `GET /api/providers` in any form; both credentials render as a label plus
  `…Q1um`, which is exactly the key's last four characters and nothing more.
- **The request log and trace read correctly.** A page carried a `next_cursor`;
  the trace carried `candidates`, `skips`, `attempts`, `warnings` and a `bodies`
  array that is `[]` rather than `null` — `capture.bodies` has no writer, and
  the drawer has to be able to range over it.

One observation, not a phase 7 defect: `AttemptRecord.Seq` is 0-indexed at the
source (`Seq: len(rec.Attempts)` in `exec.recordAttempt`, phase 3), so the first
attempt of a request is attempt 0. The trace endpoint reports what is stored; the
drawer renders it 1-based, because "Attempt 0" reads as a bug to an operator.

Both test ports were released and no process was left running. Ports 8080 and
8081 were never touched.

## Review history

| Artifact | Reviewers | Outcome |
|---|---|---|
| All 10 specs | 5 × Fable, read-only | ~150 findings → `docs/superpowers/specs/2026-08-22-spec-review-findings.md`, all specs revised |
| Task 13 (`internal/exec`) | 1 × Fable, read-only | Concurrency core sound; 6 defects fixed in 8b0b81d |
| Task 14 (`internal/server`) | 1 × Fable, read-only | 8 defects fixed in 8b0b81d, including a drain deadline that did nothing |
