# Phase 6 — Catalog

**Status:** Approved design.
**Date:** 2026-08-22
**Master design:** `2026-08-22-darkrouter-design.md`
**Depends on:** phase 3. Independent of phases 4, 5, and 8.

---

## 1. Goal

Give Darkrouter breadth as data. Ship presets for the providers OmniRoute knows about, enrich models
with capability and pricing metadata, discover what is actually reachable, and merge the three into
one index the router consults.

## 2. Scope boundary

**In:** the presets schema and its transcription, the models.dev sync worker, the live discovery
worker, merge precedence, capability inference, and the immutable catalog snapshot.

**Out:** the UI that browses it (phase 7), signed and OAuth provider kinds (phase 8 — presets for
them are transcribed here, but their adapters do not exist yet and their entries stay inert).

## 3. Presets

`presets.yaml` is embedded with `go:embed`. Each entry:

```yaml
groq:
  name: Groq
  kind: openaicompat
  base_url: https://api.groq.com/openai/v1
  auth: { style: bearer }
  surfaces: [llm]
  free_tier: true
  website: https://console.groq.com
  quirks: []
```

`auth.style` covers the header shapes actually in use: `bearer`, `x-api-key`, `api-key`,
`query-param`, and `none`. `quirks` is a small closed vocabulary — not free text — for behaviors the
adapter must adjust for, such as a provider that rejects `stream_options`, requires `max_tokens` on
every call, or returns usage only on the final chunk.

Keeping quirks a closed set matters. An open-ended field becomes a dumping ground, and the adapter
ends up with per-provider branches that nobody can enumerate.

### 3.1 Transcription

Source: OmniRoute's `src/shared/constants/providers/` — roughly 4,600 lines of pure data across its
`apikey`, `local`, `noauth`, and `oauth` families.

The transcription is **scripted and then reviewed**, not hand-typed. A one-off script reads the
TypeScript literals and emits candidate YAML; a human pass then fixes names, drops entries, and fills
`auth.style` where OmniRoute encoded it in prose rather than structure. The script is a build-time
tool, not shipped code, and lives under `tools/`.

Dropped wholesale: `web-cookie` (browser-scraped), `cloud-agent`, `upstream-proxy`, `system`, and any
entry whose only surfaces are video, music, OCR, or web search. What remains is every provider serving
at least one supported surface.

This is a data transcription. No OmniRoute code, structure, or abstraction crosses over.

## 4. Metadata sync

A background worker fetches models.dev on a long interval (12 hours, jittered) and caches the result
in `models`. It supplies capabilities, context window, maximum output tokens, and per-token pricing.

The sync is **failure-tolerant by design**: a fetch error leaves the previous cache in place and logs
a warning. Darkrouter must start and serve with no network access to models.dev at all, falling back
to preset-declared metadata. A gateway that will not start because a metadata CDN is down is a worse
gateway.

Prices are converted to `micros` integers at ingest. No float touches pricing.

## 5. Live discovery

A worker probes each enabled provider's model-listing endpoint — `/v1/models` for `openaicompat`, the
kind-appropriate equivalent otherwise — at startup and every 15 minutes, jittered, with a
per-provider concurrency cap so a fleet of forty providers does not open forty simultaneous
connections on boot.

Discovery is what makes a locally running Ollama's models appear without configuration.

**A discovery failure never removes models.** A provider that times out once must not empty its half
of the catalog and silently break every alias pointing at it. Instead the entry is marked stale after
three consecutive failures, and stale entries are still routable — the breaker, not the catalog, is
the mechanism for avoiding a broken provider. Staleness is a display concern.

Discovery also runs on demand when a provider is created or its key changes, so the UI shows models
immediately rather than after the next tick.

## 6. Merge precedence

Three sources, resolved per field rather than per record:

| Field | Winner |
|---|---|
| Model exists / is available | Live discovery, then presets |
| Capabilities, context window, pricing | models.dev, then presets, then inference |
| Base URL, auth style, quirks | Provider row override, then preset |

Live discovery wins on availability because it is ground truth about reachability. models.dev wins on
metadata because a provider's own listing rarely reports pricing or context windows accurately.

### 6.1 Inference for unknown models

A model present in discovery but absent from models.dev — common for local runtimes and new releases —
gets conservative inferred capabilities: text in and out, no tools, no vision, and an unknown context
window, adjustable by an override on the provider row.

Conservative inference is deliberate. Guessing that an unknown model supports tools sends a tool call
to a model that cannot handle it and produces a confusing failure; guessing it does not means the
router skips it for tool requests, which is visible and correctable.

## 7. The catalog snapshot

The catalog exposes an immutable snapshot, swapped atomically when any source updates:

```go
type Snapshot struct { /* opaque */ }

func (s Snapshot) Lookup(provider, model string) (Model, bool)
func (s Snapshot) Offering(model string) []Offer   // every provider serving this name
func (s Snapshot) Search(Filter) []Model
```

The router takes one snapshot per request, which is what lets `Resolve` stay a pure function — the
snapshot is an input, not a live query.

## 8. Testing

Preset tests validate the shipped file: every entry parses, every `kind` is real, every `auth.style`
is in the closed set, every `quirk` is in the closed vocabulary, and no `id` repeats.

Sync tests use a fixture server covering a successful fetch, a 500, a timeout, and malformed JSON —
asserting the cache survives all three failures and that a cold start with no network still yields a
usable catalog from presets alone.

Discovery tests assert that three consecutive failures mark stale rather than remove, that a recovery
clears staleness, and that the per-provider concurrency cap is respected on a cold start with many
providers.

Merge tests are a table over the precedence rules, including the conflict cases where all three
sources disagree.

## 9. Done criteria

- A fresh install lists hundreds of known providers without configuration, and adding one is a name plus a key.
- A locally running Ollama's models appear in the catalog within one discovery tick, with no manual entry.
- Pricing and context windows are populated for mainstream models and marked unknown rather than guessed for the rest.
- Darkrouter starts and serves correctly with no outbound internet access to models.dev.
- A provider timing out during discovery does not empty its models or break aliases pointing at it.
- `go test ./...` passes.
