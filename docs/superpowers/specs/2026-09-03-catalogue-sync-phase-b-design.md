# Catalogue Sync — Phase B: price federation and the free-tier record

**Status:** Approved design, 2026-09-03.
**Master design:** `2026-08-22-darkrouter-design.md`
**Builds on:** Phase A (`2026-09-03-catalogue-sync-design.md`), merged as `4520b20`.
**Followed by:** Phase C (non-LLM surfaces).

---

## 1. Goal

Price the models that have no price.

The deployed catalogue holds 459 model rows and **441 of them are unpriced** — 96%.
An unpriced row costs nothing in the spend tile, so today darkrouter silently
reports most traffic as free. Phase A landed the provenance vocabulary; this
phase supplies the prices it was built to label.

Coverage is the goal, and it is what success is measured against: how many rows
go from unpriced to priced. The provenance grade exists to be honest about the
fill being third-party, not as the headline.

## 2. Scope boundary

**In:** the LiteLLM price index and its per-preset join, prices harvested from
the existing discovery sweep, prices declared inline in the upstream registries,
one price resolver, the enriched free-tier record with its `tos` policy, and the
console treatment of both.

**Out:** non-LLM surfaces and their per-query or per-character price units,
which `catalog.Pricing` cannot express — Phase C. Loosening Phase A's
`format`/`category` ingestion gate, which needs dialect and credential-flow
support first.

## 3. What the sources are worth

Measured 2026-09-03.

| Source | Reach | Grade |
|---|---|---|
| LiteLLM index | 3,517 entries, 130 providers | `indexed` |
| Discovery sweep | rare — see below | `measured` |
| Upstream registry | sparse, a handful of entries | `indexed` |

**Prices in `/models` are real but rare.** Sampling live endpoints: Chutes
publishes `pricing` and `price`, OpenRouter publishes `pricing`; DeepInfra (190
models) and AI Horde (27) publish none. The OpenAI `/models` schema has no price
field, so `measured` is structurally a minority grade and always will be. That
is the correct outcome for a marker whose job is to call out the exception — but
it means discovery is not the coverage source.

**The LiteLLM join is the coverage source.** 47 of 208 presets match a
`litellm_provider` value directly; ten aliases (`fireworks → fireworks_ai`,
`together → together_ai`, `vertex → vertex_ai-language-models`, and seven more)
lift that to **57 presets reaching 2,352 entries**. LiteLLM carries
`litellm_provider` as a field on each record, so this is a per-preset join key,
not string matching — structurally identical to `models_dev_id`.

**As shipped, the join prices zero rows on this deployed catalogue.** The 435
priced rows come from discovery (424) and models.dev (11), none from LiteLLM.
Where a provider the index covers offers a model, the discovery sweep has
already priced it and the stored figure outranks a directory; the 24 rows still
unpriced sit at hackclub, aihorde, ollama and uncloseai — aggregator, proxy and
local-runtime presets the index has no `litellm_provider` for, whose model names
appear nowhere in it either. The join is coverage for a fleet the index knows,
and this fleet is not it.

## 4. Price resolution

### 4.1 One slot, values gated on the stamp

`sync.go` today protects the price *stamp* but not the price *values*. A row
stamped `discovered` receives models.dev's numbers on every sync and keeps the
`discovered` stamp, so the console would render **measured** over an indexed
figure. The capabilities half of the same function already does this correctly,
and its comment says why: "the row then keeps both its label and its values —
keeping only the label would be worse than either."

The price half is corrected to match:

```go
source := priceSourceAfterSync(r.PriceSource)
in, out := meta.InputMicrosPerMTok, meta.OutputMicrosPerMTok
if source != string(SourceModelsDev) {
    in, out = r.InputMicrosPerMTok, r.OutputMicrosPerMTok
}
```

This is latent today — nothing writes a non-default `price_source`, so
`priceSourceAfterSync` guards a value nothing produces. Phase B's discovery
harvest is what makes it live, which is why it is fixed here rather than later.

**Storage decision:** one price slot, not a per-source column set or a
`model_prices` table. LiteLLM and the registry are joined in memory at merge
time the way `Doc` already is, so they stay re-resolvable without storage. Only
models.dev and discovery contend for the row's columns, and the gate above
arbitrates them. A per-source table would buy re-resolution across a precedence
change we have no evidence of wanting, at the cost of a migration and a second
arbitration point.

### 4.2 The resolver

Modelled on `surfaces()` (`merge.go:165`), which is already a precedence
resolver extracted out of `mergeOne`: an ordered candidate slice, first match
wins, its own function, its own comment justifying the order.

```go
// resolvePrice returns the first candidate whose price is Known. The order is
// precedence: an operator's correction beats a seller's own quote, which beats
// a directory, which beats a guess.
func resolvePrice(candidates ...Pricing) Pricing
```

Precedence: `override > discovered > models_dev > litellm > registry > inferred`.

That order cannot be expressed by a fixed argument list, because §4.1 gives the
row a **single** slot whose stamp may be any of `override`, `discovered`,
`models_dev` or `inferred`. The stored candidate therefore moves: it leads when
its stamp is authoritative and trails when it is not.

```go
stored := Pricing{ /* row values */, Source: priceSource(row.PriceSource)}
if stored.Source.Authoritative() {          // override, discovered, models_dev
    return resolvePrice(stored, litellm, registry)
}
return resolvePrice(litellm, registry, stored)
```

This keeps precedence positional — one predicate, two orderings — rather than
introducing a `map[Source]int` rank table as a second encoding of an idea the
file already expresses positionally in `surfaces` and sequentially in the
capability branches. `Authoritative()` sits beside `Grade()` on `Source` and is
the same kind of derivation.

`Pricing` already carries `Source`, so there is no `priceCandidate` wrapper and
no second return value that could disagree with the first. Grade is derived from
the winning source at the view layer, which is already the status quo
(`internal/admin/catalog.go:111`).

**A source with nothing to say contributes no element**, never a zeroed
`Pricing`. This is the same care the discovery upsert already documents — a
probe reporting a bare id must not overwrite real numbers with zeroes — and it
is what stops a silent listing outranking a real price.

### 4.3 The join key

`Preset` gains:

```go
LiteLLMID string `yaml:"litellm_id,omitempty"`
NoLiteLLM bool   `yaml:"no_litellm,omitempty"`
```

mirroring `ModelsDevID`/`NoModelsDev`, including the `preset_test.go` assertion
that every preset declares one or the other. Without that exemption a forgotten
join key is indistinguishable from a provider LiteLLM genuinely does not cover —
the failure mode Phase A's spec §10 already names for models.dev.

The index is fetched at runtime by a syncer mirroring `catalog.Syncer`: same
locus, same reasoning, because prices are volatile data and the alternative is
shipping a stale index in the binary.

`mergeOne`'s inputs grow to carry the LiteLLM document and the registry prices.

### 4.4 A known-free model must survive the round trip

`nullableInt64` (`internal/store/catalog.go:260`) writes NULL for zero, and
`Models` derives `PriceKnown` from `inPrice.Valid || outPrice.Valid`. A model
whose price is genuinely **zero** therefore reads back as *unpriced*.

This is invisible while the models.dev join keeps succeeding, and it lands
directly on this phase: the providers that publish prices in `/models` are
disproportionately the free-tier ones, so the first real discovered price is
likely to be zero. `PriceKnown` must be stored rather than derived from column
nullability.

## 5. Spend

Estimated prices count toward spend, and any total whose inputs were not all
`measured` or `declared` is marked as partly estimated.

The reasoning: an estimated cost is closer to the truth than treating a request
as free, which is what nil does today for 96% of rows. The operator gets a
usable number with an honest marker rather than a precise number that is wrong.
The marker is on the aggregate, not on every row — per-row grade already lives
in the catalogue.

## 6. The free-tier record

`FreeCatalog.Providers` widens from `map[preset]map[model]string` to the full
upstream record: `freeType`, `monthlyTokens`, `creditTokens`, `poolKey`, `tos`.
The parser already reads this file and discards four fields on the way through;
this stops discarding them.

### 6.1 The `tos` policy

Of 451 free-tier rows: `caution` 271, `avoid` 87, `ambiguous` 38, `ok` 50,
`unknown` 5. Only one row in nine is unambiguously fine.

Every row is catalogued with its grade visible. **`avoid` is excluded from the
free-model filter and from automatic routing unless the operator opts that
provider in explicitly.** `avoid` largely means access the vendor has not
sanctioned; a gateway that silently routes production traffic through it exposes
its operator to a risk they never agreed to. Excluding the rows entirely would
be worse — darkrouter would drop 87 models an operator may already be using,
with no indication they exist.

### 6.2 Pools are display-only

There are 80 distinct pools and exactly **one** is shared by more than one
provider (`zhipu-flash-free`, by `glm` and `glm-cn`). `poolKey` is stored and
shown, but it is not a routing input. Pool-aware routing for a single pair would
be over-engineering.

### 6.3 Console

A free model shows its budget rather than a bare badge — `free · ~24M
tokens/day` — and `avoid` carries a visible warning wherever it appears.
Per the project typography rule, hierarchy comes from colour and weight, never
from a smaller size.


### 6.4 Owed forward: the routing gate reads a frozen catalogue

`mergeOne` populates `Model.FreeTier` from the **embedded** `FreeModels()`
snapshot, while `FreeSyncer` holds a live one that only the discovery sweep
consults through `opts.FreeTiers`. `MergeInput` has no equivalent field.

So the two gates age differently. The import gate honours an upstream regrade
within a day; the routing gate honours it at the next release. `FreeSyncer`'s
own comment says the embed "freezes it at the release" — the routing gate
reintroduces that freeze, in the one place the phase exists to protect.

Both directions cost something. A tier regraded to `avoid` after a release keeps
routing until a new binary ships. A tier upgraded from `avoid` to `ok` keeps
being vetoed, and the operator's only symptom is a routing failure.

The fix is shaped like `Store.SetLiteLLM`: a `FreeTiers` field on `MergeInput`,
an atomic pointer and setter on `Store`, threading through `Rebuild`, one wiring
line in `internal/server/server.go`, and a test. Roughly four files. Deferred
from B2 rather than skipped.


### 6.5 Owed forward: the opt-in is unreachable at creation

`POST /api/providers` accepts `allow_unsanctioned_free`, and the handler's own
comment says why creation is the moment that matters: a keyless provider's first
discovery sweep is triggered during creation, before a second call could land,
so an opt-in arriving afterwards misses the import it was meant to widen.

The console never sends it. Both create paths — the keyless add on the provider
detail page and the accounts dialog — send `free_models_only` and nothing else,
so an operator adding a keyless provider cannot opt in until after the sweep
that decision was supposed to govern. The toggle added in B2 sets the flag only
on an existing provider.

Closing this is a wizard control on the two create paths, matching the API the
server already exposes. Deferred from B2 rather than skipped.
## 7. Testing

- `resolvePrice` is table-tested across every combination of present and absent
  candidates, including: a zero-valued known price winning over an absent one,
  and an absent candidate never outranking a present lower-precedence one.
- `Authoritative()` places the stored candidate on both sides: a row stamped
  `models_dev` beats a LiteLLM price, and a row stamped `inferred` loses to one.
  Both orderings are asserted, or the predicate can be inverted undetected.
- A row stamped `discovered` keeps **both** its stamp and its values across a
  models.dev sync — the defect §4.1 fixes, asserted directly.
- A genuinely free (zero-priced) model round-trips through the store as priced,
  not unpriced.
- The LiteLLM join is exercised against a checked-in golden sample of real index
  entries, decoded through the production parser — not a hand-built literal. A
  preset with neither `litellm_id` nor `no_litellm` fails `preset_test.go`.
- An `avoid`-graded model is absent from the free filter by default and present
  once its provider is opted in.
- Spend marks a total as estimated when any input was `indexed` or `guessed`,
  and does not when every input was `measured` or `declared`.

## 8. Risks

**The alias table is a judgment recorded in code.** Ten entries map a preset to
a `litellm_provider`; a wrong one silently prices a model from the wrong
vendor's index. The join must be asserted against real entries, and a
disagreement between LiteLLM and models.dev on a model both cover is worth
surfacing rather than resolving silently.

**An aggregator's headline rate is not its effective rate.** OpenRouter is
exactly this shape, which is part of why it was rejected as a source. A
discovered price from an aggregator may be a list price rather than what the
operator is billed; the `measured` grade asserts the seller quoted it, not that
it is what appears on the invoice.

Resale is therefore a property of the preset, and it is recorded two ways.
Paid aggregators — openrouter, requesty, fastrouter and thirteen more — carry a
hand-declared `resells_prices` in `presets.overrides.yaml`, because nothing in
the generated data separates a paid router from a paid vendor: both charge, and
both publish a rate per model. Free proxies are derived instead, from charging
nothing while appearing in neither price directory as a vendor of record.

`free_tier` alone is not the test. 113 of 208 presets carry it, and 27 of those
join the LiteLLM index — gemini, groq, mistral, cohere, cerebras, nvidia and
vertex among them. Those vendors set their own prices, and grading their own
quotes as third-party would leave the spend tile's estimate marker lit for
traffic priced exactly. A marker that is always on is one an operator stops
reading, which is the same failure as an unmarked estimate, inverted.

**`measured` will remain rare.** Two of five sampled providers publish prices.
The verified marker will render on few rows even after this phase, and that is
the honest outcome rather than a defect to engineer around.
