# Catalogue, providers and credentials

## Five kinds, and auth as a separate dimension

The kinds are `openaicompat`, `anthropic`, `gemini`, `bedrock` and `vertex`.
A kind is defined by payload shape.

Authentication is orthogonal. A preset declares both, and they compose. There
is no `oauthsub` kind, deliberately: a Claude subscription speaks Anthropic
Messages and an OpenAI one does not, so such a kind could not say what it
emits. Credentials resolve into an authorizer applied *after* the body is
materialised — which is also the only point at which AWS SigV4 can hash a
payload that will not change afterwards.

Ten authentication styles exist: `bearer`, `x-api-key`, `api-key`,
`query-param`, `none`, `sigv4`, `gcp-sa`, `oauth`, plus two easily missed —
**`optional`** (serves unauthenticated, serves better with a key) and
**`anonymous`** (demands a credential and ships a published one, such as AI
Horde's). Both count as keyless for routing.

A non-static style leaves the target's API key empty, so no adapter can write
a token document into its own header by forgetting a step.

### Vertex dispatches per publisher

`publishers/google` takes the Gemini payload at `:generateContent`.
`publishers/anthropic` takes the Anthropic Messages payload at `rawPredict` /
`streamRawPredict`, with the model moved from the body into the URL and a
mandatory `anthropic_version: "vertex-2023-10-16"` body field. Llama and
Mistral MaaS are out of scope: they are a third, OpenAI-compatible route.

This is why a candidate carries a publisher at all. Without it every Vertex
request would take the Google builder and every Claude call would fail.

Vertex declares `llm` and `embedding`, but serves embedding only under the
Google publisher — the Anthropic publisher refuses at build time. This is the
one place where "an unimplemented surface is a routing filter, not a runtime
error" does not hold end to end.

Vertex is skipped by discovery and seeded from presets and the metadata index
by publisher.

## The catalogue

Five inputs merge into one index:

| Input | Refresh | Contributes |
|---|---|---|
| Shipped presets | Compile time | Endpoints, kinds, auth styles, quirks, surfaces |
| models.dev index | 12h, with an embedded snapshot for cold start | Context windows, capabilities, prices |
| Live discovery | 15m, and on provider or credential change | The provider's real model list, and often its prices |
| Free-tier register | 24h | Free-tier terms per model |
| LiteLLM price index | 24h | Community prices, joined in memory, never written to a row |

`presets.yaml` is **generated**. Corrections belong in
`presets.overrides.yaml`; a regeneration reproduces them. `presetgen-conflicts.md`
is regenerated on every run and records how each conflict resolved.

### Lifecycle

Three failed probes mark a model `stale` — still routable. Three successful
listings that omit it mark it `removed_upstream` — not routable. A model whose
capabilities are inferred rather than known is admitted with a warning naming
the missing capability, and only when the request actually needed it.

Discovery writes `["llm"]` into every row it inserts and never widens it;
surface breadth comes from the preset at merge time. **The preset therefore
outranks the discovery row for surfaces** — a row-first merge means widening a
preset has no effect on any discovered model. An operator override still wins.

### Quirks

A closed vocabulary of twelve, enforced by a guard test: ten bare tags plus
two that take a value, `rerank-path` and `context-override`. A quirk resolves
by preset id, or — for a hand-configured provider with no preset — by base
URL, unioning every `openaicompat` preset that normalises to the same address.

## Pricing

Prices are stored as integer micro-dollars **per million tokens**. The
upstream index prices in dollars per million as floats; micro-dollars per
*token* would truncate $0.14/M to zero.

Precedence is `override > discovered > models_dev > litellm > registry >
inferred`, resolved by taking the first candidate whose price is known.
`registry` currently has no producer.

Each price carries a grade: `measured` (the vendor of record quoted itself),
`declared` (an operator override), `indexed` (a third-party index) or
`guessed`. A reseller's self-quote is capped down from `measured` to
`indexed` — an aggregator quoting its own markup is not a measurement.

Whether a price is known is a **stored boolean**, not an inference from column
nullability, because a genuinely-zero-priced model must survive the round trip
without becoming "unpriced". An unpriced model records no cost at all rather
than a zero one, which would report as free.

## Free tiers

Each model row can carry the vendor's free-tier terms: type, monthly token
allowance, credit allowance, pool key, and a terms-of-service judgement drawn
from a five-value vocabulary — `ok`, `caution`, `ambiguous`, `avoid`,
`unknown`.

A **live** tier marked `avoid` is vetoed at two gates: the catalogue import,
and the router's filter, where it produces the `unsanctioned` skip reason and
the `ErrUnsanctionedFree` terminal error. The operator opts a provider in with
`allow_unsanctioned_free`, which is per provider, not global.

A `discontinued` tier is exempt at both gates — a tier that no longer exists
cannot be abused.

Pool keys are display-only: several providers can share one upstream
allowance, and knowing that is useful to an operator, but nothing routes on it.

## Credentials

A provider holds several credentials and fails over between them. Never-used
credentials sort ahead of ever-used ones, ties breaking on key id, so the
candidate sequence is reproducible from the snapshot alone.

Credentials are sealed with AES-256-GCM under a key derived from the
operator's master key; the credential row id is the additional authenticated
data, so a sealed value cannot be moved between rows. See
[`security.md`](security.md).

OAuth refresh is per-process, and rotation is persisted before the in-memory
pair is replaced — a crash mid-refresh then loses a refresh rather than the
account. Two instances pointed at one account will trip the vendor's
rotation-reuse detection; this is architectural, not a bug to be fixed in the
worker.
