# Phase 6 — Catalog

**Status:** Approved design, revised 2026-08-22 against the review findings ledger.
**Master design:** `2026-08-22-darkrouter-design.md`
**Depends on:** phase 3. Independent of phases 4, 5, and 8.

---

## 1. Goal

Give Darkrouter breadth as data. Ship presets for the providers OmniRoute knows about, enrich models
with capability and pricing metadata, discover what is actually reachable, and merge the three into
one index the router consults.

## 2. Scope boundary

**In:** the presets schema and its transcription, the models.dev sync, live discovery, merge
precedence, capability handling, the catalog snapshot, and rewiring the client-facing model-listing
routes.

**Out:** the UI that browses it (phase 7). Presets for `bedrock`, `vertex`, and OAuth providers are
transcribed here and their `oauth:` blocks defined, but their adapters arrive in phase 8.

## 3. Presets

`presets.yaml` is embedded with `go:embed`. Each entry:

```yaml
groq:
  name: Groq
  kind: openaicompat
  base_url: https://api.groq.com/openai/v1
  auth: { style: bearer }
  surfaces: [llm]
  models_dev_id: groq          # join key into models.dev
  free_tier: true
  website: https://console.groq.com
  quirks: []
```

`auth.style` is one of `bearer`, `x-api-key`, `api-key`, `query-param`, `none`, `sigv4`, `gcp-sa`,
`oauth`. OAuth entries carry an additional block:

```yaml
  oauth:
    authorize_url: ...
    token_url: ...
    client_id: ...
    scopes: [...]
    redirect: { style: localhost, port: 54545 }   # or style: manual
```

Without that block, phase 8 would hardcode per-vendor flows, contradicting "breadth costs data, not
code".

### 3.1 Quirks

`quirks` is a **closed vocabulary of tag-plus-optional-value entries**, not free text. Known members:
`rejects-stream-options`, `requires-max-tokens`, `max-completion-tokens-name`, `no-system-role`,
`no-parallel-tool-calls`, `temperature-top-p-exclusive`, `strict-unknown-fields`,
`no-tool-streaming`, `usage-final-chunk-only`, `rerank-path=<path>`, `context-override=<tokens>`.

Tag-plus-value is chosen deliberately over bare tags: retrofitting values into a tag set later is the
dumping ground with extra steps.

Growth is a two-part change by design — a vocabulary entry plus an adapter branch — and the preset
test enumerates the vocabulary in one place, so an unknown quirk in a preset fails the test rather
than being ignored at runtime.

### 3.2 Transcription

Source: OmniRoute's `src/shared/constants/providers/` — roughly 4,600 lines of pure data across its
`apikey`, `local`, `noauth`, and `oauth` families.

The transcription is **scripted and then reviewed**, not hand-typed. A one-off tool under `tools/`
reads the TypeScript literals and emits candidate YAML; a human pass fixes names, drops entries, and
fills `auth.style` and `models_dev_id` where OmniRoute encoded them in prose rather than structure.
The tool is build-time, not shipped code.

Dropped wholesale: `web-cookie`, `cloud-agent`, `upstream-proxy`, `system`, and any entry whose only
surfaces are video, music, OCR, or web search. What remains serves at least one supported surface.

This is a data transcription. No OmniRoute code, structure, or abstraction crosses over.

### 3.3 Preset upgrades

A binary upgrade can rename or remove a preset that `providers.preset` rows reference, or change a
base URL a provider row overrides. On startup, a provider whose preset no longer exists degrades to
its stored `kind` and `base_url` with a warning naming the orphaned reference, rather than failing to
load. Silent removal of a working provider on upgrade is the failure mode to avoid.

## 4. Metadata sync

A worker fetches `https://models.dev/api.json` every 12 hours, jittered, caching into `models`. The
document is keyed by provider, each model carrying `cost.input` / `cost.output` (USD per **million**
tokens, floats), `limit.context` / `limit.output`, `tool_call`, `reasoning`, `attachment`, and
`modalities`.

Field mapping is stated here rather than left to be reverse-engineered:

| models.dev | Darkrouter |
|---|---|
| `cost.input` × 1,000,000 | `input_price_micros_per_mtok` |
| `cost.output` × 1,000,000 | `output_price_micros_per_mtok` |
| `limit.context` | `context_window` |
| `limit.output` | `max_output_tokens` |
| `tool_call` | capability `tools` |
| `reasoning` | capability `reasoning` |
| `modalities.input` contains `image` | capability `vision` |

There is no `vision` flag in models.dev; it is expressed through `modalities.input`. And prices are
per million, which is why master design §11 stores micro-dollars per million — storing micro-dollars
per token would truncate a $0.14/M model to integer zero.

The sync is **failure-tolerant**: a fetch error leaves the cache and logs a warning. Darkrouter must
start and serve with no access to models.dev at all, falling back to preset-declared metadata. A
gateway that will not start because a metadata CDN is down is a worse gateway. The data's licence is
recorded in `THIRD_PARTY_NOTICES.md`, since preset fallback metadata effectively embeds a snapshot.

### 4.1 Model-ID normalization

Discovery reports `llama3.3:70b` from Ollama and `accounts/fireworks/models/llama-v3p3-70b` from
Fireworks; models.dev calls the same family `llama-3.3-70b` under its own provider key. Without a join
rule the merge silently fails and every model falls back to inference.

The join is `(preset.models_dev_id, normalized_model_id)`, where normalization lowercases, strips a
known provider path prefix, and replaces `:` with `-`. A preset may additionally declare explicit
`model_aliases` for cases the rule misses. A model that fails to join is not an error — it simply
carries inferred capabilities, handled in §6.

## 5. Live discovery

A worker probes each enabled provider's listing endpoint at startup and every 15 minutes, jittered,
under a **global** concurrency cap across the discovery fleet. A per-provider cap would not stop forty
providers opening forty simultaneous connections on boot, which was the stated goal.

Discovery uses the provider's least-recently-used non-cooling credential. A 401 on a probe cools that
credential exactly as a request would, since it is the same evidence.

Per kind: `/v1/models` for `openaicompat`; the models endpoint for `anthropic` and `gemini`;
`ListFoundationModels` **and** `ListInferenceProfiles` for `bedrock`, cataloguing profile IDs as the
routable identifiers per phase 8 §3.3. **Vertex is skipped** — it offers no practical API for listing
what a project may call, so its entries come from presets and models.dev filtered by declared
publisher.

Discovery also runs on demand when a provider is created, its credential changes, or its probe
succeeds, so the UI shows models immediately rather than after the next tick.

### 5.1 Model lifecycle

Each `models` row carries a `state`: `live`, `stale`, or `removed_upstream`.

**A discovery failure never changes state.** A provider that times out once must not empty its half of
the catalog and break every alias pointing at it. After three consecutive failures its models are
marked `stale` — still routable, since the breaker rather than the catalog is what avoids a broken
provider.

**A successful listing that omits a previously seen model** is different evidence and does change
state. After three consecutive successful listings without it, the model becomes `removed_upstream`:
**not routable**, still displayed with provenance, and purgeable from the UI.

That asymmetry matters. Union-forever would leave retired models routable indefinitely, and because a
404 classifies as `RetryableModel` and never penalizes the provider, nothing would ever stop the
wasted attempt on every request. Replace-on-success would break aliases on a single flaky listing.
Three successful confirmations is the middle.

## 6. Capabilities

Every model records `capabilities_source`: `models_dev`, `discovered`, `inferred`, or `override`.

**Discovered** capabilities come from runtimes that report them — Ollama's `/api/show` exposes whether
a model's template advertises tools, which covers the common local case properly rather than guessing.

**Inferred** applies when nothing else is available: text in and out, unknown context window. Per
master design §6.4, models with inferred capabilities **pass the router's capability filter with a
warning** rather than being excluded.

That rule is what keeps the local-model story honest. Hard-filtering on guessed metadata would mean
every discovered Ollama model refuses tool requests — and since Claude Code always sends tools, "your
local models appear automatically" would mean "appear and never serve anything". A provider's own
error is clearer than Darkrouter silently declining to route, and the trace explains it.

**Override** comes from `model_overrides`, keyed per `(provider, model)` — not per provider. One
Ollama instance serves models with wildly different tool support, so a provider-level override is the
wrong granularity.

On-demand discovery re-runs the merge, so a capability upgrade does not wait up to 12 hours for the
next sync.

## 7. Merge precedence

Resolved per field, not per record:

| Field | Winner |
|---|---|
| Model exists / is routable | Live discovery, then models.dev for kinds without discovery (Vertex) |
| Capabilities | Override, then models.dev, then discovered, then inferred |
| Context window, max output, pricing | models.dev, then preset, then unknown |
| Base URL, auth style, quirks, surfaces | Provider row override, then preset |

Presets declare no model lists, so they are not an existence source — an earlier draft's precedence
table cited them for exactly that and could not be implemented.

## 8. The catalog snapshot

An immutable snapshot, swapped atomically when any source updates:

```go
type Snapshot struct { /* opaque */ }

func (s Snapshot) Lookup(provider, model string) (Model, bool)
func (s Snapshot) Offering(model string) []Offer
func (s Snapshot) Search(Filter) []Model
```

It satisfies the `catalog.Reader` interface phase 3 defined. The router takes one snapshot per
request, which is what lets `Resolve` stay pure.

## 9. Client-facing listings

This phase rewires `GET /v1/models` and `GET /v1beta/models` from phase 1's provider-declared union to
the catalog, preserving phase 1's behavior of listing configured aliases first. `removed_upstream`
models are excluded; `stale` ones are included.

## 10. Testing

Preset tests validate the shipped file: every entry parses, every `kind` and `auth.style` is real,
every quirk is in the closed vocabulary, no id repeats, every OAuth entry has a complete `oauth:`
block, and every entry has a `models_dev_id` or an explicit exemption.

Sync tests use a fixture server covering success, 500, timeout, and malformed JSON — asserting the
cache survives all three failures, that a cold start with no network yields a usable catalog from
presets alone, and that a $0.14/M price round-trips to 140,000 rather than 0.

Normalization tests cover the Ollama, Fireworks, and OpenRouter identifier forms joining correctly, and
a deliberate non-match falling back to inferred.

Discovery tests assert three failures mark stale rather than remove, three successful omissions mark
`removed_upstream`, one omission does not, recovery clears both, and the global concurrency cap holds
on a cold start with forty providers.

Merge tests are a table over §7 including the cases where all sources disagree.

A capability test asserts an Ollama model whose `/api/show` advertises tools is `discovered` rather
than `inferred`, and that an inferred model still routes for a tool request with a warning.

## 11. Done criteria

- A fresh install lists hundreds of known providers; adding one is a name plus a key.
- A locally running Ollama's models appear within one discovery tick, with tool support read from the runtime rather than guessed, and serve a Claude Code request.
- Pricing and context windows are populated for mainstream models, correct to the cent, and marked unknown rather than guessed for the rest.
- Darkrouter starts and serves with no outbound access to models.dev.
- A provider timing out does not empty its models; a model genuinely removed upstream stops being routable after three confirmations.
- `go test ./...` passes.
