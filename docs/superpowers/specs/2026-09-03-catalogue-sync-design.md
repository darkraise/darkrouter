# Catalogue Sync — Phase A: multi-source provider ingestion

**Status:** Approved design, 2026-09-03.
**Master design:** `2026-08-22-darkrouter-design.md`
**Builds on:** phase 6 (catalog), phase 10 (operator console).
**Followed by:** Phase B (price federation and free-tier enrichment), Phase C (non-LLM surfaces).

---

## 1. Goal

Keep darkrouter's supported-provider set current without a human remembering to run a
generator. Today `tools/presetgen` transcribes a single upstream — OmniRoute — from a
local checkout, and a provider it adds tomorrow reaches operators only when somebody
re-runs the tool and ships a release.

This phase adds a second upstream, gives every generated fact a recorded origin, and puts
the whole transcription on a schedule that opens a pull request when upstream drifts.

## 2. Scope boundary

**In:** the 9router scraper, the two-source merge and its precedence, the provenance
vocabulary and its two carriers, the `price_source` column, the review artifacts, and the
scheduled workflow.

**Out:** harvesting prices from the discovery sweep, the LiteLLM index, and the enriched
free-tier record — all Phase B. Non-LLM surfaces (`tts`, `webSearch`, `webFetch`, `image`)
are Phase C; this phase ingests only providers whose surfaces darkrouter already serves,
`llm` and `embedding`.

The runtime workers are unchanged. `FreeSyncer` keeps its 24-hour fetch and `Discoverer`
its 15-minute sweep; this phase touches only what happens at build time, plus one column
those workers will write in Phase B.

## 3. What the second upstream is worth

Measured 2026-09-03:

| | Providers |
|---|---|
| OmniRoute (`open-sse/config/providers/registry/<id>/index.ts`) | 250 |
| 9router (`open-sse/providers/registry/*.js`) | 120 |
| darkrouter today | 198 |
| Ids present in **both** upstreams | 57 |
| In 9router, absent from darkrouter | 62 |

Of those 62, roughly 35 are non-LLM and deferred to Phase C. The remaining ~27 are
routable today. The 57 overlapping ids matter as much as the new ones: two independent
transcriptions of the same provider disagreeing on a base URL or an auth style is a
review signal darkrouter has never had.

9router also carries facts OmniRoute does not: a `display` block with brand colour, a
two-letter text mark, and `notice.apiKeyUrl` — where an operator actually gets a key.

## 4. Provenance

### 4.1 Two planes

Provenance is two different questions with two different answers, and conflating them
produces a manifest nothing can write to.

| Plane | Answers | Decided | Carrier |
|---|---|---|---|
| **Structural** | "`base_url` came from 9router" | Build time; identical for every operator | `internal/catalog/provenance.yaml`, embedded |
| **Value** | "this price was measured, not indexed" | Runtime; differs per installation | `models.price_source` column |

Structural provenance is a fact about the release. Value provenance is a fact about *this
operator's* catalogue, because their discovery sweep talks to their providers.

### 4.2 Grade

`internal/catalog` already owns a provenance vocabulary — `catalog.Source`, stamped
per-model in `mergeOne`. A second enum would give the console two answers to one
question, so the existing one is extended rather than duplicated:

```go
const (
    SourceDiscovered Source = "discovered"  // grade: measured
    SourceOverride   Source = "override"    // grade: declared
    SourceModelsDev  Source = "models_dev"  // grade: indexed
    SourceLiteLLM    Source = "litellm"     // grade: indexed   (new; no members until Phase B)
    SourceRegistry   Source = "registry"    // grade: indexed   (new; no members until Phase B)
    SourceInferred   Source = "inferred"    // grade: guessed
)

func (s Source) Grade() Grade // measured | declared | indexed | guessed
```

- **measured** — the seller's own endpoint quoted this. The only grade that is "official".
- **declared** — an operator typed it. Neither official nor approximate; it is their number.
- **indexed** — a curated third party said so. This is the "roughly this value" case.
- **guessed** — inferred from nothing better.

A boolean `official` was rejected: it forces `override` into "official", which
misrepresents a hand-entered figure as vendor-confirmed.

### 4.3 Console treatment: mark the exception

models.dev covers most of the catalogue, so grading it `indexed` would put a warning on
the majority of rows, and a warning on everything is read as nothing.

The console therefore marks **measured** positively — a verified marker on the few prices
a provider quoted itself — leaves `indexed` unmarked in the list while showing the grade
in row detail, and gives `guessed` a visible caution. Official is the exception, so
official is what gets called out.

### 4.4 Precedence is a different order from grade

Grade says how far to trust a value. Precedence says which value wins. They are not the
same order: `override` is the least official and the highest precedence, because an
operator correcting a price did so deliberately.

```
override > discovered > models_dev > litellm > registry > inferred
```

models.dev outranks LiteLLM because it is provider-scoped (212 providers) and joins
through the `models_dev_id` key already maintained per preset, while LiteLLM is a flat
3,518-model map needing a fuzzier per-preset join. Broader coverage, weaker join: it
fills gaps rather than displacing a clean match.

For structural fields the order is `override > omniroute > 9router`, with OmniRoute first
because presetgen already trusts it and its transcription has been reviewed across nine
phases. This is a judgment recorded in code, not a derived fact; §7 is how a wrong call
gets caught.

### 4.5 Storage

`catalog.Pricing` gains `Source Source` beside `Known bool`. Safe: it is passed by value
with `Cost`/`CostMicros` hanging off it, and carries no struct-equality comparisons
outside a nil check in `internal/admin/catalog_test.go`.

`store.ModelRow` gains `PriceSource string`, backed by a new migration:

```sql
-- 0018_price_source.sql
ALTER TABLE models ADD COLUMN price_source TEXT NOT NULL DEFAULT 'inferred';
```

This mirrors `capabilities_source TEXT NOT NULL DEFAULT 'inferred'` from
`0002_catalog.sql:23` exactly, and is written by the same `UPDATE models SET …` statement
that already writes the four price columns (`internal/store/catalog.go:190`). The admin
view gains `price_source` alongside the `merge_source` it already returns
(`internal/admin/catalog.go:42`).

**No price is `measured` when this phase ships.** Nothing in the discovery sweep records a
price today. Phase A lands the vocabulary, the column and the precedence; Phase B gives
`measured` its first members. The verified marker rendering on zero rows is correct
behaviour, not a defect.

The column is not dead scaffolding in the meantime. `mergeOne` already knows whether a
price came from a models.dev join or from the stored row, and stamping that distinction as
`models_dev` versus `inferred` is immediately useful: it separates "a directory priced
this" from "nobody did" for every model in the catalogue, which today reads as an
undifferentiated `Known` bool.

## 5. Parsing the second upstream

**14 of 120** 9router registry files carry `import` statements — `deepseek.js`,
`codex.js`, `gemini.js`, `claude.js`, `glm.js`, `xiaomi-tokenplan.js` among them. They are
not JSON and not statically parseable object literals, so a regex scraper or a literal
tokenizer produces silently wrong values for some of the most important providers in the
set.

JavaScript evaluates JavaScript. `presetgen` shells out once per file:

```
node --input-type=module -e 'import("<file>").then(m => console.log(JSON.stringify(m.default)))'
```

Imports resolve naturally, nesting survives, and there is no parser to drift as upstream
reshapes its files. CI already installs Node 24 for `web/`. A local run without Node fails
with an explicit message rather than mis-scraping.

OmniRoute keeps its existing regex scrape: it works, it is tested, and TypeScript types
make evaluation non-trivial. Two techniques for two upstreams, each matched to its format.

## 6. The suffix-trim hazard

`chatSuffixes` (`tools/presetgen/main.go:248`) has four entries. 9router's base URLs use
fourteen distinct endings:

| Covered | Not covered |
|---|---|
| `/completions` (62), `/messages` (13), `/models` (6), `/responses` (3) | `/embeddings` (14), `/generations` (8), `/search` (5), `/transcriptions` (4), `/chat` (4), `/tts` (3), `/speech` (3), `/generate` (3), `/listen` (2), `/voice` (1) |

`/embeddings` lands in **this phase** — darkrouter already serves an `embedding` surface,
so 14 providers would otherwise get `/embeddings` welded onto their `base_url` and ship as
a resolved, conflict-free row.

Two mitigations, both here:

1. Extend `chatSuffixes` with `/embeddings` — and only `/embeddings`. The remaining
   uncovered endings all belong to surfaces this phase does not ingest, so adding them now
   would be dead entries defended by no test. They arrive in Phase C alongside the
   providers that need them.
2. **Detect conflicts on raw, pre-trim values.** Two sources that agree only *after*
   transformation must still surface as a disagreement. Comparing post-trim values is the
   failure mode that hides a wrong base URL behind a clean diff.

## 7. Pipeline

One step is inserted into the existing straight line. Nothing downstream of it changes.

```
scrapeRegistry + scrapeDisplay (omniroute, regex)  ─┐
scrapeNineRouter (9router, node dump)              ─┼─→ merge() ─→ presets + conflicts + provenance
                     [models.dev join, as today] ───┘
                              ↓
                    carryQuirks → applyOverrides   (both stamp provenance)
                              ↓
   writePresets · writeSnapshot · writeFreeCatalog · copyIcons · writeIconManifest
                              + writeProvenance
```

New files under `tools/presetgen/`:

| File | Contents |
|---|---|
| `ninerouter.go` | the Node dump, its decoding, and the mapping to darkrouter's preset shape |
| `merge.go` | `merge(...) (catalog.Presets, []conflict, Provenance)` and the precedence table |
| `provenance.go` | the manifest type and its emission |

`main.go` gains one flag, `-ninerouter`.

Three behaviours survive untouched because they are load-bearing and already correct:
`carryQuirks` still reads the previous `presets.yaml` so a regeneration never wipes a
quirk; `applyOverrides` still runs last with non-zero-wins; and both now also record their
origin in the manifest, so a hand-corrected field reads as `override` rather than being
misattributed to whichever scraper won.

Every emitted artifact sorts by id before marshalling — `presetgen` already does this for
registry entries (`main.go:189`) — or the weekly diff re-flows for nothing.

### 7.1 Conflicts

Precedence always resolves, so a scheduled run never blocks. Disagreements are collected
and written to `internal/catalog/presetgen-conflicts.md`, regenerated each run so a
resolved disagreement drops off by itself. A reviewer promotes one into
`presets.overrides.yaml` by hand.

### 7.2 9router quirks arrive as suggestions

`catalog.Quirks` is a closed vocabulary enforced by `preset_test.go`. 9router's
`transport.quirks` uses its own names (`dropClientMetadata` and friends). Mapping them
automatically would either fail that test or, worse, guess a mapping that silently changes
request shape. They land in the conflicts artifact as raw upstream text. Nothing
auto-applies.

## 8. The scheduled workflow

`.github/workflows/catalog-refresh.yml`, weekly plus `workflow_dispatch`:

Two jobs. The split is the whole point: the generator evaluates upstream JavaScript,
so the job that runs it must not be able to write anything.

```yaml
generate:                        # permissions: contents: read
- checkout darkrouter
- shallow-clone OmniRoute and 9router; record both HEAD SHAs
- setup-go, setup-node
- fetch https://models.dev/api.json
- go run ./tools/presetgen -omniroute … -ninerouter … -modelsdev … \
      -out-provenance internal/catalog/provenance.yaml
- go test ./internal/catalog/... ./tools/presetgen/...
- upload-artifact: the generated files

propose:                         # permissions: contents+PRs write; needs: generate
- checkout darkrouter
- download-artifact over the clean tree
- git diff --quiet && stop
- peter-evans/create-pull-request
```

Five properties:

- **The workflow runs the identical command a developer runs locally.** There is no
  CI-only mode to keep in sync. This is the main reason the pipeline stays one program.
- **The PR body is generated from the provenance diff**, grouped by source, so it reads
  "12 fields changed via 9router, 3 via models.dev" instead of making a reviewer infer
  causation from a 198-entry YAML diff. Conflicts and unmapped 9router quirks follow as a
  table.
- **Upstream SHAs go in the manifest header.** Without them nothing answers "did this
  change because upstream moved, or because our scraper did". A run timestamp is
  deliberately not recorded: it would differ on every run and open a pull request weekly
  even when neither upstream moved, which the byte-stability requirement below rules out.
- **Upstream code never runs with a write token.** `go run ./tools/presetgen` shells out
  to node, which evaluates every file of the freshly cloned 9router registry, top-level
  module code included. That happens only in `generate`, whose token is read-only and
  which can neither push a branch nor open a pull request. `propose` holds the write
  token and does nothing but restore an artifact and read the diff.
- **Tests gate the PR and it never auto-merges.** A malformed upstream commit fails the
  closed-vocabulary and preset-shape tests in `generate`, so it reaches a human as a red
  run rather than a green PR. That asymmetry is the reason structure goes through a PR
  while volatile data goes through the runtime path.

Deliberately not built: a guard against hand-edits to generated files. The repository
already lives with a "Do not hand-edit" header across five generated artifacts
(`main.go:499`); a sixth convention is cheaper than a sixth mechanism.

## 9. Testing

- `merge` picks the precedence winner per field, and records the origin it picked.
- Two sources agreeing only after suffix trimming still produce a conflict row.
- Every `chatSuffixes` entry is exercised, including the newly added `/embeddings`.
- A provider whose only surface is non-LLM is skipped, not half-ingested with a mangled
  base URL.
- The Node dump decodes all 120 files, and specifically the 14 carrying imports.
- `carryQuirks` and `applyOverrides` still win over both upstreams, and stamp `override`.
- A 9router quirk with no darkrouter vocabulary entry reaches the conflicts artifact and
  never reaches `Preset.Quirks`.
- `Grade()` maps every `Source` value, and the migration defaults an existing row to
  `inferred`.
- Emitted artifacts are byte-stable across two runs on unchanged input.

## 10. Risks

**The precedence table is a judgment, not a fact.** OmniRoute-first for structural fields
is a position taken once. If 9router becomes the more accurate transcription, nothing
detects the drift — conflicts surface active disagreement, not a stale rule that no longer
disagrees because one source followed the other. Mitigation is the review artifact and a
human reading it; there is no automated answer.

**The Node dependency is new to a Go tool.** It is present in CI and on any machine with
`web/` set up, but a contributor without Node cannot regenerate presets. The failure is
loud, not silent.

**Shelling out 120 times is slow.** Acceptable for a weekly job; if it becomes annoying
locally, one Node process reading a file list replaces 120, with no design change.
