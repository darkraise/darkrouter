# Darkrouter Progress

Last updated: 2026-08-24

## Phase status

| Phase | Spec | Plan | Status |
|---|---|---|---|
| 1 — Foundation | ✅ | ✅ | **Complete.** Race-clean, `go vet` clean, Docker image verified, all four manual checks passed. |
| 2 — Persistence and health | ✅ | ✅ | **Merged to master.** 18 tasks, all done criteria met. |
| 3 — Routing and failover | ✅ | ✅ | **Merged to master.** 20 tasks, all done criteria met. |
| 4 — Dialects | ✅ | ✅ | **Complete.** 37 tasks; race-clean, verified live against Groq. |
| 5 — Auxiliary surfaces | ✅ | ✅ | **Complete.** 34 tasks; race-clean, all seven surfaces served. |
| 6 — Catalog | ✅ | ✅ | **Complete.** 26 tasks; race-clean, verified live against Groq. |
| 7 — Admin API and UI | ✅ | ✅ | **Complete.** 29 tasks; race-clean, dashboard served from the image. |
| 8 — Signed and OAuth credentials | ✅ | ✅ | **Complete.** 20 tasks; race-clean, verified against fakes only. |
| 9 — Passthrough fast path | ✅ | ✅ | **Complete.** 17 tasks; race-clean, verified against fakes only — no `GROQ_KEY` in this environment for the live check. |

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

## Closed by phase 9

- **A request forwards instead of re-rendering when the client's dialect
  already matches the chosen provider's wire format.** `edge.Passthrough`
  (`internal/edge/edge.go:15`) carries the raw body alongside the parsed IR;
  `forwardKinds` (`internal/exec/passthrough.go:25`) maps each of the three
  inbound dialects onto the one adapter kind whose wire format matches it, and
  eligibility is re-decided on every attempt, so a request that forwards to its
  first provider still translates correctly on failover to a different kind.
- **Bedrock and Vertex never take the fast path.** Bedrock signs a hash of the
  materialized body, which a forwarded body would invalidate the moment a
  header changed it; Vertex encodes the model in its URL alongside the
  publisher, which a forwarded body cannot carry. Both are excluded by
  `adapter.Forwarder` (`internal/adapter/adapter.go:240`) simply not being
  implemented for either kind.
- **Streaming forwards event-by-event, not buffered whole.** The forwarded SSE
  stream is split at event boundaries, scanned inline for the commit and usage
  signals every path needs for accounting, and flushed through without being
  held for a full re-encode.
- **The unary fast path is measurably cheaper, against a local upstream.**
  `internal/exec/bench_test.go`, three runs of `-count=3 -benchmem`: unary
  passthrough averaged ~66µs and 227 allocations per request against the IR
  path's ~72µs and 284; streaming time-to-first-token averaged ~86.8µs against
  ~90.7µs, with 448 total allocations against 825. The benchmark's upstream is
  a local `httptest` server, so none of these numbers include real network
  latency.
- **`DisableCompression` is set on the executor's shared transport (spec §8),
  which costs the IR path bandwidth.** Go's `http.Transport` otherwise
  negotiates gzip and decompresses it transparently; turning that off so a
  forwarded body always arrives byte-identical to what the provider sent means
  every request on the shared transport, IR path included, now pays for
  uncompressed bytes over the wire.
- **A differential suite compares both paths over the phase 4 golden corpus.**
  `internal/golden/differential_test.go` drives 10 request fixtures, 7 response
  fixtures and 11 usage fixtures through the fast and IR paths against the same
  fake upstream, asserting the forwarded body is byte-identical to what the
  client sent and that extracted usage agrees exactly.
- **Which path an attempt took is recorded end to end.**
  `request_attempts.path` (migration 0005) is written by the executor, read
  back by the trace endpoint, and shown as a `Path` column in the dashboard's
  trace drawer — the only way to confirm from outside the process that a 200
  came from the fast path rather than the IR path producing the same bytes by
  coincidence.
- **An end-to-end suite exercises the fast path through an assembled
  `server.Server`**, not just through `internal/exec` in isolation:
  `internal/e2e/phase9_test.go` seeds a fake upstream, runs the real HTTP
  handlers and worker goroutines, and reads the request row back through
  `GET /api/requests/{id}` to confirm the recorded path, forwarded bytes and
  extracted usage all agree with what actually crossed the wire.

## Carried forward from phase 9

- **Two IR-path fidelity gaps phase 4 recorded in prose are now load-bearing
  in the differential suite, not just noted here.** The IR path emits a usage
  chunk the client never asked for — master design §4.2 requires that chunk
  stripped, but scopes the requirement to the passthrough path only, so the IR
  path still forwards it unconditionally — and that chunk carries a
  synthesized choice (`chunk()` in `internal/edge/openai/stream.go:14` always
  builds one `choices` entry) where OpenAI itself emits `choices: []`.
  `internal/golden/differential_test.go` now encodes both explicitly: its
  request-body comparison allows the IR path's top-level keys to exceed the
  passthrough body's by exactly `stream_options`, and its source comments
  cross-reference this document. A widening of either gap beyond what is
  described here fails a test instead of only being true in prose.
- **Gemini's blocked-prompt behaviour diverges between the two paths, and the
  divergence is deliberate.** Google returns a blocked prompt as HTTP 200 with
  `promptFeedback.blockReason`; the IR path (`internal/adapter/gemini/parse.go:119`)
  still synthesizes a 400 and withholds the health signal, matching master
  design §8.1's rule that a content filter is fatal rather than retryable. The
  fast path forwards Google's 200 verbatim, which is strictly closer to what a
  direct call to Google returns, and matches this document's own earlier note
  (above, under "Carried forward into phase 5 and beyond") that the IR
  behaviour makes Gemini CLI and Claude Code show a failure where the model
  merely declined. A real defect was found and fixed along the
  way: the streaming recognizer used to classify a blocked prompt as a
  retryable provider error, which failed the request over to another provider
  and recorded a health failure against a provider that had answered correctly.
- **Live verification against Groq did not happen.** Unlike phase 8, this
  phase is fully verifiable live — Groq is an OpenAI-compatible provider and
  the OpenAI dialect is one of the three inbound ones — but there is no
  `GROQ_KEY` in this environment and no local `darkrouter.yaml` carrying a
  credential, so no live call was made. The plan's verification steps
  (`docs/superpowers/plans/2026-08-24-phase9-passthrough.md`, task 17) are
  written to be followed by hand and remain available to whoever has a key.
- **`Discoverer.SweepOnce` ends with a rebuild that is not serialized against
  concurrent admin catalog writes.** An operator adding a model while a sweep
  runs could have it vanish from routing until the next rebuild. The mechanism
  is inferred from the code; the phase's own e2e work only proved the
  manifestation — a 404 flake in the strict-400 case, traced to the sweep's
  trailing rebuild publishing a stale snapshot over a test's seeding — and
  worked around it by disabling discovery in that suite rather than fixing the
  race. Out of phase 9's scope.
- **Array-form Gemini streaming lost its only integration coverage.** Gemini
  clients may ask for a chunked JSON array instead of SSE, and the whole-branch
  review found that form was passthrough-eligible but unservable — the fast
  path found no SSE event boundary, so a response over the pre-commit cap failed
  the whole chain and cooled providers that had answered correctly. Eligibility
  now requires `alt=sse`, which is the right fix, but it also means no test
  drives an array-form streaming request end to end through the executor. The
  predicate and the array writer are each unit-tested; the path between them is
  not.
- **A post-commit scanner error forwards the injected usage chunk.** When the
  event splitter overflows after commit, the remainder is copied through raw so
  the client keeps every byte the provider sent — which bypasses the strip that
  normally removes a `stream_options` chunk Darkrouter asked for and the client
  did not. Never corrupting bytes is the right trade on an already-degraded
  path, but it is the one route by which a fourth body mutation reaches a
  client, and master design §4.2 permits three.
- **That same remainder copy is untested.** Its covering test delivers its whole
  body in one read, so the carry is flushed but the `io.Copy` leg copies zero
  bytes. A two-chunk body would close it.

## Closed by phase 8

- **Three credential strategies exist**, composing with adapter kinds rather than being kinds. `internal/auth` resolves a credential into an authorizer applied after the body is materialized, which is the only point at which SigV4 can hash a payload that will not change. A non-static style leaves `Target.APIKey` empty, so no adapter can write a token document into its own header by forgetting a step.
- **SigV4 is pinned by known-answer vectors** generated from the real signer rather than transcribed. `SignedHeaders` includes `content-length`, so a refactor that signed before materializing the body fails the vector instead of producing an opaque 403.
- **`url.PathEscape` is not usable for a Bedrock model id.** It leaves the colon alone, which is legal in a path segment and is not what AWS signs — smithy-go's own `EscapePath` escapes everything outside RFC 3986's unreserved set. Every inference-profile id contains a colon, so the difference is a 403 on every request.
- **Bedrock discovery catalogues inference profile ids.** `ListFoundationModels` alone would store precisely the identifiers that fail; `PROVISIONED`-only and `LEGACY` models are left out for the same reason.
- **Eventstream framing is decoded with the SDK's decoder**, including a frame split across single-byte reads and a mid-stream exception. The tests build their frames with the SDK's own encoder rather than checking in a binary blob a reader cannot inspect.
- **Vertex dispatches per publisher**, reusing phase 4's Gemini and Anthropic renderers rather than growing a third. Parsing dispatches on payload shape, because neither parser is handed a target.
- **`router.Candidate.Publisher` is populated.** It was declared in phase 3 and never read; without it every Vertex request would take the Google builder and every Claude call would 400.
- **OAuth state is single-use, expiring and session-bound**, and a session mismatch deliberately does not consume it — letting a blocked attack invalidate the operator's own callback would turn the block into a denial of service.
- **Rotation is persisted before the in-memory pair is replaced**, so a crash mid-refresh loses a refresh rather than the account. The refresh worker drives the same authorizer a request does, under the same per-account mutex, so the two cannot drift or race.
- **The probe names what failed** — signature, permission, region, expiry or reachability — for all three strategies.
- **`provider_keys` needed no migration.** `kind`, `expires_at` and `scope`, and `providers.region/project/location`, have all been columns since migration 0001: master design §11 wrote the column list for the whole product, not for phase 2.
- **Two gaps the phase found in existing code.** `POST /api/providers` ignored `auth_style`, so a provider created for a signed kind was never signed; and the Bedrock builder rendered an inbound `developer` turn as a `user` message, silently stripping its status on a payload whose `messages` array admits only user and assistant. The golden regeneration is what exposed the second.

## Carried forward from phase 8

- **Nothing in this phase was verified against a real vendor.** There is no AWS account, no GCP service account and no Claude subscription in this environment, and the user chose fake-backed verification knowing that. Every test runs against a known-answer vector, an SDK-encoded frame, or an `httptest` server. **The Converse and `rawPredict` field names are the specific risk**: they come from vendor documentation and are pinned by golden files, so a correction later is a visible diff rather than a silent behavior change.
- **Bedrock serves `llm` only.** Its embedding API is a different shape, and claiming the surface would route embeddings to a Converse endpoint that answers 400. The `vertex` preset declares `embedding` and the adapter does not serve it, for the same reason.
- **Llama and Mistral MaaS on Vertex are not served**, per spec §4.1: they use a third, OpenAI-compatible route that is out of scope for v1.
- **Bedrock has no `.sse` golden fixture**, and a recorded exemption instead: its stream is binary framing a text file cannot hold. `TestEveryKindHasStreamFixtures` fails for any other kind that goes uncovered.
- **The refresh worker is per-process.** Darkrouter is single-instance by design and nothing here makes two instances safe to run against one OAuth account; two sharing a grant trip rotation-reuse detection.
- **`presets.yaml` carries two lines by hand.** `presetgen` needs an OmniRoute checkout this environment does not have, so the `publisher:` field was added to the generated file directly. The override file and the generator both carry it, so a regeneration reproduces it, and a guard test fails if either vertex preset loses one.
- **The API has twenty-two endpoints, not master design §10's twenty-one.** The extra is `POST /api/providers/:id/oauth/complete`, which the design's list does not name: spec §5.1 requires the manual-paste path and a paste needs a POST of its own, since `GET /api/oauth/callback` exists for the listener. Recorded as a deliberate addition rather than left as an off-by-one against the design.
- **`providers.location` is set at creation and not patchable.** Changing it moves every catalogued model to a different endpoint, which is a new provider rather than an edit. `region` and `project` are patchable, closing the phase 7 carry-forward.

## Closed by phase 7

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
- **A provider's cooling credentials and cooling triples are both cleared by the
  test button.** The triple half was written against `Snapshot.Offering`, which
  answers "which providers serve this model" rather than "which models does this
  provider serve"; passing it a provider id returned nothing and the triple
  cooldowns were never cleared. Fixed with a regression test that fails against
  the old code.
- **The image builds the dashboard.** A Node stage runs before the Go stage, so
  a clean checkout produces a binary carrying the real bundle rather than an
  embedded placeholder — a `go:embed` of a `.gitkeep`-only directory compiles
  and serves a broken page, which is what the build test now catches.

## Carried forward from phase 7

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
- **The per-provider discovery interval phase 6 filed against the settings screen
  is still absent.** The screen edits providers, credentials and nothing else;
  scheduling stays a config concern.
- **Gemini media inlining still has no UI switch**, which phase 4 filed against
  this screen. Spec §2 keeps policy in `darkrouter.yaml` and the screen renders
  it read-only, so the toggle belongs in the file rather than here; the item
  moves from "phase 7 will add it" to "it is a config key, and one nothing has
  written yet".

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

### 3b. Two wire-format notes for phase 9 — moved

These were phase 1/4 observations held here for phase 9 to pick up. Phase 9
did: they now live under "Carried forward from phase 9" as load-bearing
assertions in `internal/golden/differential_test.go`, not just prose.

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

### 8. Phase 7 full-stack verification against Groq — done

Run on 2026-08-24 with a static `CGO_ENABLED=0` binary on ports 18080/18081,
`DARKROUTER_ADMIN_PASSWORD_HASH` set from `darkrouter hash-password`. Section 7
exercised the API with `curl`; this drives the built frontend and the real
screens' request sequence.

- **The dashboard is served from the binary and every deep link resolves.** `/`,
  `/requests`, `/catalog`, `/playground`, `/settings` and `/requests/01ABC` all
  returned `200 text/html`, with an `id="root"` mount point and
  `<script type="module" crossorigin src="/assets/index-kCLcAtgC.js">` — a real
  hashed bundle, not the placeholder a `.gitkeep`-only embed would serve.
- **A browser-shaped login drives all five screens.** One `POST /api/auth/login`
  with `Sec-Fetch-Site: same-origin` returned a CSRF token and a cookie; the
  seven endpoints the screens fetch on load — `overview`, `models`, `requests`,
  `usage`, `config`, `presets`, `providers` — all returned 200 on that session.
- **A provider was added from a preset and its models discovered without editing
  a file.** `POST /api/providers {"id":"groq-ui","preset":"groq"}` → 201, a
  credential → 201, `POST .../test` → `ok: true, model_count: 13,
  latency_ms: 247`, and `GET /api/models?q=gpt-oss` then listed three models
  carrying both `groq` and `groq-ui` as providers.
- **The failover trace explains every attempt.** A second provider created from
  the same preset, then `PATCH`ed to `http://127.0.0.1:1`, was tried first on
  priority 99. The trace carries two attempts — attempt 0 `broken` →
  `retryable_provider` with `dial tcp 127.0.0.1:1: connect: connection refused`,
  attempt 1 `groq` → `success` `http=200` `739ms` — and a three-entry
  `candidates` array.
- **A skipped candidate says why.** Ten concurrent requests tripped the breaker
  and ten of the twelve resulting traces carry
  `broken/<key>/openai/gpt-oss-120b:cooling` in `skips` with a single attempt.
  Worth stating why the first sequential attempt did not produce one: the
  cooldown ladder starts at one second and a Groq round trip is longer than
  that, so each sequential request found the triple already reopened as the
  half-open probe. That is the ladder working, not a missed trip.
- **Deleting a provider names the stranded aliases and the reload still works.**
  `DELETE /api/providers/groq` returned `{"dangling_aliases":["fast"]}`, and the
  immediately following `POST /api/config/reload` returned `{"valid":true}`,
  with `GET /api/config` still reporting `valid: true`. This is the criterion
  the dangling-alias-is-a-warning rule exists for: a delete that could invalidate
  the config would leave the operator with a reload button that keeps failing.
- **No credential material reached any response.** The key does not appear in
  any of the seven screen endpoints' bodies; the surviving credential renders as
  `primary` plus `…Q1um`.

Six of spec §8's seven criteria were exercised here; the seventh, `go test ./...`
and the frontend build, is the standing gate every task ran. **One criterion was
verified only in part:** "the overview shows a provider entering and leaving
cooldown within one poll interval" was confirmed as data — the breaker trips and
the router skips — but the overview's `cooling` count stayed at 0 throughout,
because it counts credential-level cooldowns and a `retryable_provider` outcome
cools the `(provider, key, model)` triple instead. The tile is not wrong, but a
provider failing this way does not show as cooling on the overview. Filed as an
observation rather than a fix: which cooldowns the tile should count is a phase 2
question about breaker key shapes, not a rendering bug.

One further observation, not a phase 7 defect: **a dangling alias created by a UI
delete is reported once and then invisible.** The delete response names it, but
neither `GET /api/config` nor `/healthz` carries a standing warning afterwards,
because the loader computes that warning at parse time against the yaml
`providers:` block — which is ignored once providers have been imported into the
database.

Both test ports were released and no process was left running. Ports 8080 and
8081 were never touched.

### 9. Phase 8 verification against fakes — done

Run on 2026-08-24. **No vendor was contacted.** This environment has no AWS
account, no GCP service account and no Claude subscription, and that limitation
was accepted deliberately before the phase was planned. Every upstream below is
an `httptest` server; what these results prove is that the wiring holds end to
end, not that a real vendor accepts the payload.

Eight cases in `internal/e2e`, driving an assembled `server.Server` over a
temporary database through both its handlers:

- **A Bedrock request leaves signed.** The fake saw
  `AWS4-HMAC-SHA256 Credential=…/us-east-1/bedrock/aws4_request`, a path ending
  `…-v2%3A0/converse` — the escaped form the signature covers — and a
  Converse-shaped body. The completion reached the client.
- **A Bedrock stream decodes.** Six SDK-encoded eventstream frames became SSE
  carrying the reassembled text.
- **A signed provider fails over like any other.** A bedrock provider at
  `127.0.0.1:1` on priority 99 with an openaicompat provider behind it:
  `X-Darkrouter-Attempts: 2`, `X-Darkrouter-Provider: back`.
- **Vertex routes each publisher to its own URL.** Two models on one provider:
  `…:generateContent` with a `contents` body, and `…:rawPredict` with
  `anthropic_version: vertex-2023-10-16` and no `model` key.
- **An OAuth account serves.** The upstream saw `Authorization: Bearer at-0`
  and no `x-api-key` beside it.
- **A rotated refresh survives a restart.** After one refresh, the server was
  torn down and rebuilt over the same database file; the stored credential named
  `rt-1`, the token the vendor now expects.
- **An `invalid_grant` shows as needing reconnection.** The probe reported
  `ok: false` naming reconnection, and `GET /api/overview` reported
  `needs_reauth: true` for the provider.
- **No credential material leaves the process.** Six admin endpoints and a proxy
  error response swept for the AWS secret and access key id.

The image builds and stays static: **57.1 MB**, against phase 7's 53.3 MB. The
3.8 MB is `aws-sdk-go-v2` and `golang.org/x/oauth2`; Node never reaches the
final stage and `CGO_ENABLED=0` still produces one binary.

Of spec §8's seven criteria, **six were exercised against fakes** and the
seventh — `go test ./...` with golden files — is the standing gate every task
ran. **None was exercised live.** The two that a fake can least stand in for are
the first and the third: whether real Bedrock accepts this Converse body, and
whether real Vertex accepts this `rawPredict` body. Those field names come from
vendor documentation and are pinned by golden files, so a correction is a
visible diff rather than a silent behavior change — but they are unverified.

Two defects were found by this task rather than by a unit test, both fixed:
**the Vertex adapter ignored `Target.BaseURL` entirely**, so it could only ever
address googleapis.com — no private service endpoint, and no test above the unit
level; and **a credential written directly to the database never reached the
provider source**, which is why the harness forces a reload. The second is a
test-harness fact rather than a product defect: every path that writes a
credential through the API already reloads.

No process was left running and no port was held.

### 10. Phase 9 verification against fakes — done

Task 17's brief calls for a live check against Groq. This environment has no
`GROQ_KEY` and no local config carrying one — a missing secret, not a network
problem — and spending the user's paid vendor quota to work around that was
ruled out. **What follows ran against a locally built binary and a local fake
upstream. No vendor was contacted, and this section proves the wiring, not
that Groq accepts the payload.** That gap is unlike phase 8's: phase 9 is
otherwise fully verifiable live, and remains unrun live for want of a
credential rather than for want of a reachable protocol.

Run on 2026-08-24. `CGO_ENABLED=0 go build ./cmd/darkrouter` produced a
**35 MB** static binary, started for real — a background OS process bound to
real TCP listeners, config loaded from a real YAML file, the admin API driven
over real HTTP with a real login and session cookie — rather than through
`internal/e2e`'s in-process `httptest` harness. The fake upstream was a small
standalone `net/http` server (not `httptest`, which cannot run outside a test
binary) on port 19090, answering `/v1/chat/completions` (unary and SSE) and
`/v1/messages` (Anthropic-shaped), and exposing `/_seen` to return the last
request's headers and body verbatim.

Three providers were configured: `fake-oai` (`openaicompat`, serving
`fake-model`, kept healthy throughout so the passthrough checks below are
unaffected by the failover check), `broken` (`openaicompat`, base URL
`127.0.0.1:1`, unreachable by construction) and `fake-fallback` (`anthropic`),
the latter two both serving a separate `fallback-model` so the failover check
cannot accidentally land back on `fake-oai` and take the fast path a second
time.

- **The fast path is confirmed by trace, not by status code.** An
  OpenAI-shaped request to `fake-oai` returned 200 with
  `X-Darkrouter-Attempts: 1`, and `GET /api/requests/{id}` read back
  `attempts[0].path == "passthrough"`.
- **An unmodelled parameter reached the provider intact.** `seed: 424242`,
  which Darkrouter's IR does not model anywhere in `internal/ir` or
  `internal/edge/openai`, appeared verbatim in the fake's recorded body
  (`"seed":424242`) and in its reply (`x_fake_saw_seed: 424242`, a value the
  fake can only have produced by reading the field back out of what it
  received).
- **The inbound proxy credential never reached the upstream on the two calls
  its headers were inspected; the target's own key did.** `/_seen` after the
  unmodelled-parameter call recorded `Authorization: Bearer
  sk-fake-upstream-key` (the provider's configured credential), never the
  client's `Bearer proxy-throwaway-token`; `/_seen` after the failover call
  showed the anthropic-kind fallback sending `X-Api-Key:
  sk-fake-fallback-key` and no `Authorization` header at all. Headers were
  not inspected on the plain fast-path call or on either streaming call —
  only their bodies and the request trace were.
- **Streamed usage and the stripped-versus-kept pair both hold.** A stream
  request with no `stream_options` recorded `tokens_in: 12, tokens_out: 5` on
  the request row, and the client's own SSE body (`stream1.txt`) carried zero
  chunks with a `usage` key — while the fake's `/_seen` confirmed Darkrouter
  had injected `"stream_options":{"include_usage":true}` upstream regardless.
  The same request repeated with `"stream_options":{"include_usage":true}` in
  the client's own body produced an SSE stream (`stream2.txt`) carrying
  exactly one `usage`-bearing chunk before `[DONE]`. That pair — absent by
  default, present on request — is what distinguishes stripping from never
  receiving; either result alone would have proved nothing.
- **Failover still translates.** With `broken` at priority 99 in front of
  `fake-fallback` at priority 1, the trace read `attempts[0]` as
  `broken/passthrough/retryable_provider` (`dial tcp 127.0.0.1:1: connect:
  connection refused`) and `attempts[1]` as `fake-fallback/ir/success/200`.
  The client received a well-formed OpenAI-shaped completion
  (`"hi from the fallback"`) even though the second attempt answered in
  Anthropic's wire shape — the IR path translated it back correctly.

**What did not run, and why.** Brief step 6 (timing ten unary requests through
the gateway against ten direct calls) was not attempted: it exists to catch a
network-latency confound against a real vendor, and a local fake on loopback
has no TLS handshake or WAN latency to confound anything, so the measurement
would say nothing this task's scope needs. Nothing else in the brief's
substance was skipped, and nothing observed here failed — every check above
passed on the first run, including the failover case, which is itself worth
noting as absence of a finding rather than proof one could not exist.

Both processes were tracked by PID from the moment they started. Both were
killed at the end (`pkill -P` followed by `kill`), and `ss -ltnp` afterward
showed no listener on 18080, 18081 or 19090.

## Review history

| Artifact | Reviewers | Outcome |
|---|---|---|
| All 10 specs | 5 × Fable, read-only | ~150 findings → `docs/superpowers/specs/2026-08-22-spec-review-findings.md`, all specs revised |
| Task 13 (`internal/exec`) | 1 × Fable, read-only | Concurrency core sound; 6 defects fixed in 8b0b81d |
| Task 14 (`internal/server`) | 1 × Fable, read-only | 8 defects fixed in 8b0b81d, including a drain deadline that did nothing |
