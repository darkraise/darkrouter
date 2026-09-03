# Catalogue Sync Phase A Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

> **Implementer assignments:** each task names its implementer agent in an
> `**Implementer:**` line. When executing with
> superpowers:subagent-driven-development, REQUIRED SUB-SKILL:
> dcc-superpower-companions:dispatching-tiered-implementers. Under
> superpowers:executing-plans these lines are inert; ignore them.

**Goal:** Ingest a second upstream provider registry (9router) beside OmniRoute, record where every generated fact came from, and put the transcription on a weekly schedule that opens a pull request when upstream drifts.

**Architecture:** `tools/presetgen` stays one program and one pass. It gains a second scraper that evaluates 9router's JavaScript with Node rather than parsing it, then a merge step applying a recorded field precedence before the existing quirk-carry, override and emit stages run unchanged. Provenance splits into two planes: structural origin in an embedded manifest, price origin in a new `price_source` column mirroring the existing `capabilities_source`.

**Tech Stack:** Go 1.x, SQLite (`internal/store/migrations`), Node 24 (for evaluating 9router's ES modules), React + TypeScript (`web/`), GitHub Actions.

**Spec:** `docs/superpowers/specs/2026-09-03-catalogue-sync-design.md`

## Global Constraints

- **Never hardcode a font size.** No `text-xs`, no `text-[11px]`, no custom sizes. Only `text-sm`, `text-base`, `text-lg`, `text-xl`, `text-2xl`, `text-3xl`. In stylesheets use `font-size: var(--text-sm)`. `text-sm` (14px) is the floor. Applies to everything under `web/`.
- **English only** in code, comments, docs, commits, configs, errors and tests.
- **Commit format:** `<type>(<scope>): <subject>`, type one of `feat|fix|docs|style|refactor|test|chore|perf`, subject ≤50 chars, imperative, no trailing period.
- **Comments explain WHY, never WHAT.** Default to none. Never reference the current task or issue number.
- **Phase A ingests only `llm` and `embedding` surfaces.** Non-LLM providers (`tts`, `webSearch`, `webFetch`, `image`) are Phase C and must be skipped, not half-ingested.
- **Do not run `npm run build` or redeploy** as part of these tasks. The deploy step happens once at the end of the phase, per `CLAUDE.md`.
- Tests are run with `go test` and `npm test`; give every command an explicit timeout.

---

### Task 1: Extend the Source vocabulary with Grade

**Files:**
- Modify: `internal/catalog/catalog.go:13-21`
- Test: `internal/catalog/catalog_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `catalog.Grade` (string type) with constants `GradeMeasured`, `GradeDeclared`, `GradeIndexed`, `GradeGuessed`; `catalog.SourceLiteLLM`, `catalog.SourceRegistry`; method `func (s Source) Grade() Grade`.

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 0 = 3
**Approach:** inline - skip 3: the spec settled the four-grade vocabulary in §4.2

- [ ] **Step 1: Write the failing test**

Append to `internal/catalog/catalog_test.go`:

```go
func TestSourceGrade(t *testing.T) {
	cases := map[Source]Grade{
		SourceDiscovered: GradeMeasured,
		SourceOverride:   GradeDeclared,
		SourceModelsDev:  GradeIndexed,
		SourceLiteLLM:    GradeIndexed,
		SourceRegistry:   GradeIndexed,
		SourceInferred:   GradeGuessed,
	}
	for src, want := range cases {
		if got := src.Grade(); got != want {
			t.Errorf("Source(%q).Grade() = %q, want %q", src, got, want)
		}
	}
}

// An unrecognised source must read as a guess rather than as a measurement:
// defaulting the other way would badge an unknown value as vendor-confirmed.
func TestUnknownSourceGradesAsGuessed(t *testing.T) {
	if got := Source("something-new").Grade(); got != GradeGuessed {
		t.Errorf("unknown source graded %q, want %q", got, GradeGuessed)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/catalog/ -run TestSourceGrade -v`
Expected: FAIL — `undefined: GradeMeasured`, `undefined: SourceLiteLLM`.

- [ ] **Step 3: Write the implementation**

In `internal/catalog/catalog.go`, extend the existing const block and add the grade type below it:

```go
const (
	SourceModelsDev  Source = "models_dev"
	SourceDiscovered Source = "discovered"
	SourceInferred   Source = "inferred"
	SourceOverride   Source = "override"
	SourceLiteLLM    Source = "litellm"
	SourceRegistry   Source = "registry"
)

// Grade is how far a value may be trusted, which is not the same question as
// which value wins. An operator's own figure outranks every other source and
// is still not something a vendor confirmed.
type Grade string

const (
	// GradeMeasured is the seller's own endpoint quoting itself.
	GradeMeasured Grade = "measured"
	// GradeDeclared is an operator's hand-entered value.
	GradeDeclared Grade = "declared"
	// GradeIndexed is a curated third party's figure.
	GradeIndexed Grade = "indexed"
	// GradeGuessed is derived from nothing better.
	GradeGuessed Grade = "guessed"
)

func (s Source) Grade() Grade {
	switch s {
	case SourceDiscovered:
		return GradeMeasured
	case SourceOverride:
		return GradeDeclared
	case SourceModelsDev, SourceLiteLLM, SourceRegistry:
		return GradeIndexed
	default:
		return GradeGuessed
	}
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `go test ./internal/catalog/ -run TestSourceGrade -v`
Expected: PASS, both tests.

- [ ] **Step 5: Run the package suite for regressions**

Run: `timeout 300 go test ./internal/catalog/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/catalog/catalog.go internal/catalog/catalog_test.go
git commit -m "feat(catalog): grade a metadata source"
```

---

### Task 2: Add the price_source column

**Files:**
- Create: `internal/store/migrations/0018_price_source.sql`
- Modify: `internal/store/catalog.go:19-41` (ModelRow), `:73` (modelColumns), the `Models` scan, and the metadata `UPDATE` at `:190-217`
- Test: `internal/store/catalog_test.go`, `internal/store/migrate_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `store.ModelRow.PriceSource string`, persisted in `models.price_source`, defaulting to `"inferred"`.

**Implementer:** dcc-superpower-companions:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6
**Approach:** inline - skip 2: mirrors the existing `capabilities_source` column exactly

- [ ] **Step 1: Write the failing migration test**

Append to `internal/store/migrate_test.go`:

```go
func TestPriceSourceDefaultsToInferred(t *testing.T) {
	db := openMigrated(t)
	if _, err := db.Write.Exec(
		`INSERT INTO models (provider_id, model_id, capabilities_source) VALUES ('p', 'legacy', 'inferred')`); err != nil {
		t.Fatal(err)
	}
	var got string
	if err := db.Read.QueryRow(
		`SELECT price_source FROM models WHERE model_id = 'legacy'`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != "inferred" {
		t.Errorf("price_source = %q, want %q", got, "inferred")
	}
}
```

If the helper that opens a migrated database in this package is not named `openMigrated`, use whatever `TestMigrate`-family tests in this file already call; do not add a second helper.

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/store/ -run TestPriceSourceDefaultsToInferred -v`
Expected: FAIL — `no such column: price_source`.

- [ ] **Step 3: Write the migration**

Create `internal/store/migrations/0018_price_source.sql`:

```sql
-- A price and the authority behind it are different facts, and the models row
-- recorded only the number.
--
-- capabilities_source has separated a read capability from a guessed one since
-- 0002; prices had no equivalent, so a figure a provider quoted itself and one
-- a third-party directory estimated read identically. Defaulting to 'inferred'
-- keeps an existing row honest: nothing yet knows where its price came from.
ALTER TABLE models ADD COLUMN price_source TEXT NOT NULL DEFAULT 'inferred';
```

- [ ] **Step 4: Run the migration test to verify it passes**

Run: `go test ./internal/store/ -run TestPriceSourceDefaultsToInferred -v`
Expected: PASS.

- [ ] **Step 5: Write the failing round-trip test**

Append to `internal/store/catalog_test.go`:

```go
func TestModelRowRoundTripsPriceSource(t *testing.T) {
	db := openMigrated(t)
	ctx := context.Background()
	if _, err := db.Write.ExecContext(ctx,
		`INSERT INTO models (provider_id, model_id, capabilities_source) VALUES ('p', 'm', 'inferred')`); err != nil {
		t.Fatal(err)
	}
	if err := db.WriteModelMetadata(ctx, []ModelRow{{
		ProviderID: "p", ModelID: "m",
		InputMicrosPerMTok: 500, OutputMicrosPerMTok: 1500,
		PriceSource: "models_dev",
	}}); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Models(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if rows[0].PriceSource != "models_dev" {
		t.Errorf("PriceSource = %q, want %q", rows[0].PriceSource, "models_dev")
	}
}
```

If the metadata writer is not named `WriteModelMetadata`, use the name the `UPDATE models SET` statement at `internal/store/catalog.go:190` lives inside.

- [ ] **Step 6: Run it to verify it fails**

Run: `go test ./internal/store/ -run TestModelRowRoundTripsPriceSource -v`
Expected: FAIL — `unknown field PriceSource`.

- [ ] **Step 7: Wire the column through the store**

In `internal/store/catalog.go`, add the field to `ModelRow` directly below `PriceKnown`:

```go
	// PriceSource records which authority the price came from, so the console
	// can separate a figure the seller quoted from one a directory estimated.
	PriceSource string
```

Extend the `modelColumns` const to include the new column, keeping the existing order and adding it after `cache_write_price_micros_per_mtok`:

```go
const modelColumns = `provider_id, model_id, publisher, surfaces, capabilities,
	capabilities_source, context_window, max_output_tokens,
	input_price_micros_per_mtok, output_price_micros_per_mtok,
	cache_read_price_micros_per_mtok, cache_write_price_micros_per_mtok,
	price_source,
	discovered_at, state, missing_streak, last_seen_at`
```

Add `&r.PriceSource` to the `Scan` call in `Models` in the same position the column occupies in `modelColumns` — immediately after the cache-write price and before `discovered_at`. Then extend the metadata `UPDATE`:

```go
	stmt, err := tx.PrepareContext(ctx,
		`UPDATE models SET
		    publisher = ?, surfaces = ?, capabilities = ?, capabilities_source = ?,
		    context_window = ?, max_output_tokens = ?,
		    input_price_micros_per_mtok = ?, output_price_micros_per_mtok = ?,
		    cache_read_price_micros_per_mtok = ?,
		    cache_write_price_micros_per_mtok = ?,
		    price_source = ?
		  WHERE provider_id = ? AND model_id = ?`)
```

and add the argument in the matching position of the `ExecContext` call, after `nullableInt64(r.CacheWriteMicrosPerMTok)`:

```go
			priceSourceOr(r.PriceSource),
```

with the helper beside `nonEmptySurfaces`:

```go
// priceSourceOr keeps the column's NOT NULL invariant when a caller leaves the
// field empty, matching the migration's default rather than writing "".
func priceSourceOr(s string) string {
	if s == "" {
		return "inferred"
	}
	return s
}
```

- [ ] **Step 8: Run both tests to verify they pass**

Run: `go test ./internal/store/ -run 'TestPriceSource|TestModelRowRoundTrips' -v`
Expected: PASS, both.

- [ ] **Step 9: Run the store suite for regressions**

Run: `timeout 300 go test ./internal/store/`
Expected: PASS. Any test constructing a `ModelRow` literal still compiles because the new field is additive.

- [ ] **Step 10: Commit**

```bash
git add internal/store/migrations/0018_price_source.sql internal/store/catalog.go internal/store/catalog_test.go internal/store/migrate_test.go
git commit -m "feat(store): record where a price came from"
```

---

### Task 3: Stamp the price source in mergeOne

**Files:**
- Modify: `internal/catalog/catalog.go:89-95` (Pricing), `internal/catalog/merge.go:60-95` (mergeOne)
- Test: `internal/catalog/merge_test.go`

**Interfaces:**
- Consumes: `catalog.Source` and `Grade` from Task 1; `store.ModelRow.PriceSource` from Task 2.
- Produces: `catalog.Pricing.Source Source`, set on every `Model` that `mergeOne` returns.

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5
**Approach:** inline - skip 3: precedence is fixed by spec §4.4

- [ ] **Step 1: Write the failing test**

Append to `internal/catalog/merge_test.go`:

```go
// A models.dev join is a directory's figure, not the seller's. Stamping it
// keeps "a directory priced this" distinct from "nobody did", which the
// Known bool alone cannot express.
func TestMergeStampsModelsDevPriceSource(t *testing.T) {
	row := store.ModelRow{ProviderID: "p", ModelID: "big"}
	doc := Doc{"p": {Models: map[string]DocModel{
		"big": {InputMicrosPerMTok: 500, OutputMicrosPerMTok: 1500, PriceKnown: true},
	}}}
	m := mergeOne(row, Preset{ModelsDevID: "p"}, doc, store.ModelOverride{})
	if m.Pricing.Source != SourceModelsDev {
		t.Errorf("Pricing.Source = %q, want %q", m.Pricing.Source, SourceModelsDev)
	}
	if m.Pricing.Source.Grade() != GradeIndexed {
		t.Errorf("grade = %q, want %q", m.Pricing.Source.Grade(), GradeIndexed)
	}
}

func TestMergeStampsRowPriceSourceWhenModelsDevMisses(t *testing.T) {
	row := store.ModelRow{
		ProviderID: "p", ModelID: "unknown",
		InputMicrosPerMTok: 100, PriceKnown: true,
		PriceSource: string(SourceDiscovered),
	}
	m := mergeOne(row, Preset{}, Doc{}, store.ModelOverride{})
	if m.Pricing.Source != SourceDiscovered {
		t.Errorf("Pricing.Source = %q, want %q", m.Pricing.Source, SourceDiscovered)
	}
}

// An empty stored source is a guess, not a measurement.
func TestMergeDefaultsAbsentPriceSourceToInferred(t *testing.T) {
	m := mergeOne(store.ModelRow{ProviderID: "p", ModelID: "x"}, Preset{}, Doc{}, store.ModelOverride{})
	if m.Pricing.Source != SourceInferred {
		t.Errorf("Pricing.Source = %q, want %q", m.Pricing.Source, SourceInferred)
	}
}
```

Adjust the `Doc` and `DocModel` literals to whatever shapes `internal/catalog/modelsdev.go` actually declares — read that file first and mirror an existing test in `merge_test.go` rather than inventing field names.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/catalog/ -run TestMerge.*PriceSource -v`
Expected: FAIL — `m.Pricing.Source undefined`.

- [ ] **Step 3: Add the field to Pricing**

In `internal/catalog/catalog.go`:

```go
type Pricing struct {
	InputMicrosPerMTok      int64
	OutputMicrosPerMTok     int64
	CacheReadMicrosPerMTok  int64
	CacheWriteMicrosPerMTok int64
	Known                   bool
	// Source is the authority behind these rates. Known says whether a price
	// exists; this says whether to believe it.
	Source Source
}
```

- [ ] **Step 4: Stamp it in mergeOne**

In `internal/catalog/merge.go`, inside the `if joined` branch add `Source: SourceModelsDev` to the `Pricing` literal, and in the `else` branch add `Source: priceSource(row.PriceSource)`. Add the helper at the foot of the file:

```go
// priceSource reads the stored authority, defaulting an empty column to a
// guess. A row written before 0018 carries "" and must not read as measured.
func priceSource(stored string) Source {
	switch Source(stored) {
	case SourceDiscovered, SourceOverride, SourceModelsDev, SourceLiteLLM, SourceRegistry:
		return Source(stored)
	default:
		return SourceInferred
	}
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./internal/catalog/ -run TestMerge.*PriceSource -v`
Expected: PASS, all three.

- [ ] **Step 6: Run the full catalog and store suites**

Run: `timeout 600 go test ./internal/catalog/ ./internal/store/ ./internal/admin/`
Expected: PASS. `Pricing` is compared by field in existing tests, not by struct equality, so the added field does not break them.

- [ ] **Step 7: Commit**

```bash
git add internal/catalog/catalog.go internal/catalog/merge.go internal/catalog/merge_test.go
git commit -m "feat(catalog): stamp the authority behind a price"
```

---

### Task 4: Surface the price source in the admin API

**Files:**
- Modify: `internal/admin/catalog.go:30-45` (modelView and pricingView), and the view builder around `:99`
- Test: `internal/admin/catalog_test.go`

**Interfaces:**
- Consumes: `catalog.Pricing.Source` from Task 3.
- Produces: JSON field `price_source` on the pricing view, and `price_grade` alongside it.

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 1 = 3
**Approach:** inline - skip 2: `merge_source` already establishes the pattern on this view

- [ ] **Step 1: Write the failing test**

Append to `internal/admin/catalog_test.go`:

```go
func TestModelViewCarriesPriceProvenance(t *testing.T) {
	got := modelViews(t, seededServer(t))["priced-model"]
	if got.Pricing == nil {
		t.Fatal("pricing view is nil")
	}
	if got.Pricing.Source != "models_dev" {
		t.Errorf("price source = %q, want %q", got.Pricing.Source, "models_dev")
	}
	if got.Pricing.Grade != "indexed" {
		t.Errorf("price grade = %q, want %q", got.Pricing.Grade, "indexed")
	}
}
```

Use whatever fixture helpers this file already provides in place of `seededServer` and `"priced-model"` — read the neighbouring tests around `internal/admin/catalog_test.go:202` and reuse their setup rather than adding a new one.

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/admin/ -run TestModelViewCarriesPriceProvenance -v`
Expected: FAIL — `got.Pricing.Source undefined`.

- [ ] **Step 3: Extend the view**

Add to `pricingView` in `internal/admin/catalog.go`:

```go
	// Source and Grade let the console mark a price the seller quoted itself,
	// and caution one that was guessed. Grade is derived rather than stored so
	// the console never has to know the source vocabulary.
	Source string `json:"price_source"`
	Grade  string `json:"price_grade"`
```

and populate both where the pricing view is built:

```go
		Source: string(m.Pricing.Source),
		Grade:  string(m.Pricing.Source.Grade()),
```

- [ ] **Step 4: Run it to verify it passes**

Run: `go test ./internal/admin/ -run TestModelViewCarriesPriceProvenance -v`
Expected: PASS.

- [ ] **Step 5: Run the admin suite**

Run: `timeout 300 go test ./internal/admin/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/admin/catalog.go internal/admin/catalog_test.go
git commit -m "feat(admin): expose price provenance on models"
```

---

### Task 5: Mark measured prices in the console

**Files:**
- Modify: `web/src/features/models/models-screen.tsx`
- Test: `web/src/features/models/models-screen.test.ts`

**Interfaces:**
- Consumes: `price_source` and `price_grade` from Task 4's JSON.
- Produces: nothing other tasks read.

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 1 = 3
**Approach:** inline - skip 3: spec §4.3 fixed the treatment — mark measured, caution guessed, leave indexed unmarked

- [ ] **Step 1: Write the failing test**

Add to `web/src/features/models/models-screen.test.ts`, following the shape of the existing fixtures there (which already carry `merge_source`):

```ts
it("marks a measured price and cautions a guessed one", () => {
  const rows = [
    { ...baseModel, id: "measured", pricing: { ...basePricing, price_source: "discovered", price_grade: "measured" } },
    { ...baseModel, id: "indexed", pricing: { ...basePricing, price_source: "models_dev", price_grade: "indexed" } },
    { ...baseModel, id: "guessed", pricing: { ...basePricing, price_source: "inferred", price_grade: "guessed" } },
  ];
  const view = render(rows);
  expect(view.marker("measured")).toBe("verified");
  expect(view.marker("indexed")).toBe(null);
  expect(view.marker("guessed")).toBe("caution");
});
```

Replace `baseModel`, `basePricing`, `render` and `marker` with the fixtures and query helpers this file already uses; do not introduce a new test harness.

- [ ] **Step 2: Run it to verify it fails**

Run: `cd web && timeout 300 npm test -- models-screen`
Expected: FAIL — no marker is rendered.

- [ ] **Step 3: Render the marker**

In the models screen, derive the marker from `price_grade` only:

```tsx
{model.pricing?.price_grade === "measured" && (
  <span className="text-sm text-[--legend]" title="Price quoted by the provider">✓</span>
)}
{model.pricing?.price_grade === "guessed" && (
  <span className="text-sm text-[--muted-foreground]" title="No published price; this is an estimate">?</span>
)}
```

`indexed` renders nothing in the list. Surface the full `price_source` in the row detail panel where the model's other metadata already appears, as plain text.

Both spans use `text-sm`, which is the floor. Do not reach for `text-xs` to make the marker smaller; use colour, as the surrounding legend text does.

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd web && timeout 300 npm test -- models-screen`
Expected: PASS.

- [ ] **Step 5: Lint and run the web suite**

Run: `cd web && timeout 600 npm test && timeout 300 npm run lint`
Expected: PASS both.

- [ ] **Step 6: Commit**

```bash
git add web/src/features/models/
git commit -m "feat(console): mark prices a provider quoted"
```

---

### Task 6: Read the 9router registry with Node

**Files:**
- Create: `tools/presetgen/ninerouter.go`, `tools/presetgen/ninerouter_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `type nineEntry struct` and `func scrapeNineRouter(dir string) ([]nineEntry, error)`, returning entries sorted by `ID`.

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 0 = 3
**Approach:** inline - skip 3: spec §5 settled on evaluating with Node rather than parsing

- [ ] **Step 1: Write the failing test**

Create `tools/presetgen/ninerouter_test.go`:

```go
package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScrapeNineRouterReadsAPlainEntry(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "cerebras.js", `export default {
  id: "cerebras",
  display: { name: "Cerebras", website: "https://www.cerebras.ai",
             notice: { apiKeyUrl: "https://cloud.cerebras.ai/platform" } },
  category: "apikey",
  authType: "apikey",
  transport: { baseUrl: "https://api.cerebras.ai/v1/chat/completions",
               quirks: { dropClientMetadata: true } },
  models: [{ id: "gpt-oss-120b", name: "GPT OSS 120B" }],
};`)

	got, err := scrapeNineRouter(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d entries, want 1", len(got))
	}
	e := got[0]
	if e.ID != "cerebras" {
		t.Errorf("ID = %q", e.ID)
	}
	if e.Transport.BaseURL != "https://api.cerebras.ai/v1/chat/completions" {
		t.Errorf("BaseURL = %q", e.Transport.BaseURL)
	}
	if e.Display.Notice.APIKeyURL != "https://cloud.cerebras.ai/platform" {
		t.Errorf("APIKeyURL = %q", e.Display.Notice.APIKeyURL)
	}
	if len(e.Transport.Quirks) != 1 || !e.Transport.Quirks["dropClientMetadata"] {
		t.Errorf("Quirks = %v", e.Transport.Quirks)
	}
}

// 14 of the 120 upstream files import from elsewhere in the repository. A
// static parser reads those wrong; evaluating them is the whole point.
func TestScrapeNineRouterResolvesImports(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "shared.js", `export const BASE = "https://api.example.com/v1/messages";`)
	write(t, dir, "claude.js", `import { BASE } from "./shared.js";
export default { id: "claude", transport: { baseUrl: BASE }, models: [] };`)

	got, err := scrapeNineRouter(dir)
	if err != nil {
		t.Fatal(err)
	}
	var claude *nineEntry
	for i := range got {
		if got[i].ID == "claude" {
			claude = &got[i]
		}
	}
	if claude == nil {
		t.Fatal("claude entry not found")
	}
	if claude.Transport.BaseURL != "https://api.example.com/v1/messages" {
		t.Errorf("BaseURL = %q, want the imported constant", claude.Transport.BaseURL)
	}
}

// index.js is a barrel of imports with no default export of its own.
func TestScrapeNineRouterSkipsTheBarrel(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "index.js", `import a from "./a.js"; export default [a];`)
	write(t, dir, "a.js", `export default { id: "a", transport: { baseUrl: "https://a.example/v1" }, models: [] };`)

	got, err := scrapeNineRouter(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "a" {
		t.Fatalf("got %v, want only the a entry", got)
	}
}

func write(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./tools/presetgen/ -run TestScrapeNineRouter -v`
Expected: FAIL — `undefined: scrapeNineRouter`.

- [ ] **Step 3: Write the implementation**

Create `tools/presetgen/ninerouter.go`:

```go
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// nineEntry is one 9router registry module. Only the fields darkrouter reads
// are declared; the upstream object carries more.
type nineEntry struct {
	ID       string       `json:"id"`
	Alias    string       `json:"alias"`
	Category string       `json:"category"`
	AuthType string       `json:"authType"`
	Display  nineDisplay  `json:"display"`
	Transport nineTransport `json:"transport"`
	// ServiceKinds names the non-LLM surfaces an entry serves. Absent means
	// chat, which is the only kind phase A ingests.
	ServiceKinds []string   `json:"serviceKinds"`
	Models       []nineModel `json:"models"`
}

type nineDisplay struct {
	Name     string     `json:"name"`
	Color    string     `json:"color"`
	TextIcon string     `json:"textIcon"`
	Website  string     `json:"website"`
	Notice   nineNotice `json:"notice"`
}

type nineNotice struct {
	APIKeyURL string `json:"apiKeyUrl"`
}

type nineTransport struct {
	BaseURL     string          `json:"baseUrl"`
	ValidateURL string          `json:"validateUrl"`
	AuthHeader  string          `json:"authHeader"`
	Quirks      map[string]bool `json:"quirks"`
}

type nineModel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// dumpScript evaluates every module and prints one JSON array. Node resolves
// the imports 14 of the upstream files rely on, which is why this shells out
// rather than parsing: those files are programs, not data.
const dumpScript = `
const fs = require("node:fs");
const path = require("node:path");
const dir = process.argv[1];
const out = [];
for (const f of fs.readdirSync(dir).filter(f => f.endsWith(".js") && f !== "index.js").sort()) {
  const m = await import(path.resolve(dir, f));
  if (m.default && typeof m.default === "object" && !Array.isArray(m.default)) out.push(m.default);
}
console.log(JSON.stringify(out));
`

func scrapeNineRouter(dir string) ([]nineEntry, error) {
	if _, err := os.Stat(dir); err != nil {
		return nil, fmt.Errorf("9router registry: %w", err)
	}
	cmd := exec.Command("node", "--input-type=module", "-e", dumpScript, dir)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("evaluate 9router registry with node (is node installed?): %w: %s", err, stderr.String())
	}
	var entries []nineEntry
	if err := json.Unmarshal(out, &entries); err != nil {
		return nil, fmt.Errorf("decode 9router dump: %w", err)
	}
	kept := entries[:0]
	for _, e := range entries {
		if e.ID != "" {
			kept = append(kept, e)
		}
	}
	sort.Slice(kept, func(i, j int) bool { return kept[i].ID < kept[j].ID })
	return kept, nil
}

// routable reports whether phase A ingests this entry. serviceKinds names the
// non-LLM surfaces; an entry that serves only those belongs to phase C.
func (e nineEntry) routable() bool {
	if len(e.ServiceKinds) == 0 {
		return true
	}
	for _, k := range e.ServiceKinds {
		if k == "embedding" {
			return true
		}
	}
	return false
}

var _ = filepath.Join
```

Remove the `var _ = filepath.Join` line if `filepath` ends up used; it is there only so the import list compiles as written.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `go test ./tools/presetgen/ -run TestScrapeNineRouter -v`
Expected: PASS, all three. If Node is absent the error message names it explicitly.

- [ ] **Step 5: Verify against the real registry**

Run: `go test ./tools/presetgen/ && go run ./tools/presetgen -help 2>&1 | head -3`
Expected: tests pass; the help output confirms the package still builds.

- [ ] **Step 6: Commit**

```bash
git add tools/presetgen/ninerouter.go tools/presetgen/ninerouter_test.go
git commit -m "feat(presetgen): read the 9router registry"
```

---

### Task 7: Trim the /embeddings suffix

**Files:**
- Modify: `tools/presetgen/main.go:248` (chatSuffixes)
- Test: `tools/presetgen/main_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: nothing other tasks import; changes the `base_url` derived for embedding providers.

**Implementer:** dcc-superpower-companions:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 0 - risk 1 = 2
**Approach:** inline - skip 3: spec §6 fixed the scope at `/embeddings` only

- [ ] **Step 1: Write the failing test**

Append to `tools/presetgen/main_test.go`:

```go
func TestEmbeddingSuffixIsTrimmed(t *testing.T) {
	e := entry{id: "voyage", baseURL: "https://api.voyageai.com/v1/embeddings"}
	if got := e.toPreset(displayEntry{}).BaseURL; got != "https://api.voyageai.com/v1" {
		t.Errorf("BaseURL = %q, want the API root", got)
	}
}

// Longest first: /v1/chat/completions must not be trimmed to /v1/chat.
func TestLongerSuffixWinsOverShorter(t *testing.T) {
	e := entry{id: "x", baseURL: "https://api.example.com/v1/chat/completions"}
	if got := e.toPreset(displayEntry{}).BaseURL; got != "https://api.example.com/v1" {
		t.Errorf("BaseURL = %q", got)
	}
}
```

Match the field names of the real `entry` struct at `tools/presetgen/main.go:133`; read it first.

- [ ] **Step 2: Run the tests to verify the first fails**

Run: `go test ./tools/presetgen/ -run 'Suffix' -v`
Expected: `TestEmbeddingSuffixIsTrimmed` FAILs with the untrimmed URL; `TestLongerSuffixWinsOverShorter` passes already.

- [ ] **Step 3: Extend the suffix list**

In `tools/presetgen/main.go`, add `/embeddings` to `chatSuffixes`, keeping the longest-first ordering the existing comment requires:

```go
var chatSuffixes = []string{"/chat/completions", "/embeddings", "/messages", "/responses", "/models"}
```

Only `/embeddings` is added. The other uncovered endings — `/tts`, `/listen`, `/generations`, `/transcriptions`, `/speech`, `/generate`, `/voice`, `/search` — belong to surfaces phase A does not ingest, and adding them now would be entries no test defends.

- [ ] **Step 4: Run the tests to verify both pass**

Run: `go test ./tools/presetgen/ -run 'Suffix' -v`
Expected: PASS, both.

- [ ] **Step 5: Commit**

```bash
git add tools/presetgen/main.go tools/presetgen/main_test.go
git commit -m "fix(presetgen): trim the embeddings suffix"
```

---

### Task 8: Merge the two upstreams by precedence

**Files:**
- Create: `tools/presetgen/merge.go`, `tools/presetgen/merge_test.go`
- Modify: `internal/catalog/preset.go` (adds the optional `APIKeyURL` field, Step 3)

**Interfaces:**
- Consumes: `nineEntry` and `scrapeNineRouter` from Task 6; the existing `entry`, `displayEntry` and `toPreset` in `main.go`.
- Produces: `type fieldOrigin struct{ Field, Source string }`, `type merged struct{ Presets catalog.Presets; Origins map[string][]fieldOrigin }`, and `func mergeSources(omni []entry, display map[string]displayEntry, nine []nineEntry) merged`.

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5
**Approach:** best-of-3 - in-place multi-scraper beat normalize-then-merge and a policy-driven engine; grafts folded in are the two-axis provenance, the SHA-stamped manifest and raw-value conflict detection

- [ ] **Step 1: Write the failing test**

Create `tools/presetgen/merge_test.go`:

```go
package main

import (
	"testing"

	"github.com/darkraise/darkrouter/internal/catalog"
)

// OmniRoute wins a contested structural field: its transcription has been
// reviewed across nine phases, 9router's has not.
func TestOmniRouteWinsAContestedField(t *testing.T) {
	omni := []entry{{id: "groq", baseURL: "https://api.groq.com/openai/v1"}}
	nine := []nineEntry{{ID: "groq", Transport: nineTransport{BaseURL: "https://groq.example/v1"}}}

	got := mergeSources(omni, map[string]displayEntry{}, nine)
	if got.Presets["groq"].BaseURL != "https://api.groq.com/openai/v1" {
		t.Errorf("BaseURL = %q, want OmniRoute's", got.Presets["groq"].BaseURL)
	}
	if !hasOrigin(got, "groq", "base_url", "omniroute") {
		t.Errorf("origins = %v, want base_url from omniroute", got.Origins["groq"])
	}
}

// A field OmniRoute does not carry is taken from 9router rather than dropped.
func TestNineRouterFillsAFieldOmniRouteLacks(t *testing.T) {
	omni := []entry{{id: "groq", baseURL: "https://api.groq.com/openai/v1"}}
	nine := []nineEntry{{ID: "groq", Display: nineDisplay{
		Notice: nineNotice{APIKeyURL: "https://console.groq.com/keys"}}}}

	got := mergeSources(omni, map[string]displayEntry{}, nine)
	if got.Presets["groq"].APIKeyURL != "https://console.groq.com/keys" {
		t.Errorf("APIKeyURL = %q, want 9router's", got.Presets["groq"].APIKeyURL)
	}
	if !hasOrigin(got, "groq", "api_key_url", "9router") {
		t.Errorf("origins = %v, want api_key_url from 9router", got.Origins["groq"])
	}
}

// A provider only 9router knows is ingested outright.
func TestNineRouterOnlyProviderIsAdded(t *testing.T) {
	nine := []nineEntry{{
		ID:        "kimchi",
		Display:   nineDisplay{Name: "Kimchi", Website: "https://kimchi.example"},
		AuthType:  "apikey",
		Transport: nineTransport{BaseURL: "https://api.kimchi.example/v1/chat/completions"},
	}}
	got := mergeSources(nil, map[string]displayEntry{}, nine)
	p, ok := got.Presets["kimchi"]
	if !ok {
		t.Fatal("kimchi absent from the merge")
	}
	if p.BaseURL != "https://api.kimchi.example/v1" {
		t.Errorf("BaseURL = %q, want the trimmed root", p.BaseURL)
	}
	if p.Name != "Kimchi" {
		t.Errorf("Name = %q", p.Name)
	}
}

// Phase A ingests llm and embedding only.
func TestNonLLMProviderIsSkipped(t *testing.T) {
	nine := []nineEntry{{
		ID:           "elevenlabs",
		ServiceKinds: []string{"tts"},
		Transport:    nineTransport{BaseURL: "https://api.elevenlabs.io/v1/text-to-speech"},
	}}
	got := mergeSources(nil, map[string]displayEntry{}, nine)
	if _, ok := got.Presets["elevenlabs"]; ok {
		t.Error("a tts-only provider was ingested; phase C owns those")
	}
}

func TestEmbeddingProviderIsIngested(t *testing.T) {
	nine := []nineEntry{{
		ID:           "voyage-ai",
		ServiceKinds: []string{"embedding"},
		Transport:    nineTransport{BaseURL: "https://api.voyageai.com/v1/embeddings"},
	}}
	got := mergeSources(nil, map[string]displayEntry{}, nine)
	if _, ok := got.Presets["voyage-ai"]; !ok {
		t.Error("an embedding provider was skipped")
	}
}

func hasOrigin(m merged, id, field, source string) bool {
	for _, o := range m.Origins[id] {
		if o.Field == field && o.Source == source {
			return true
		}
	}
	return false
}

var _ = catalog.Preset{}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./tools/presetgen/ -run 'Merge|OmniRouteWins|NineRouter|NonLLM|Embedding' -v`
Expected: FAIL — `undefined: mergeSources`.

- [ ] **Step 3: Add the APIKeyURL field to Preset**

In `internal/catalog/preset.go`, beside `Website`:

```go
	// APIKeyURL is where an operator gets a key. Neither models.dev nor
	// OmniRoute publishes it; 9router does, and an operator adding a provider
	// wants it more than anything else on the page.
	APIKeyURL string `yaml:"api_key_url,omitempty"`
```

- [ ] **Step 4: Write the merge**

Create `tools/presetgen/merge.go`:

```go
package main

import (
	"sort"
	"strings"

	"github.com/darkraise/darkrouter/internal/catalog"
)

// fieldOrigin records which upstream supplied one field of one preset.
type fieldOrigin struct {
	Field  string
	Source string
}

type merged struct {
	Presets catalog.Presets
	Origins map[string][]fieldOrigin
}

const (
	srcOmni = "omniroute"
	srcNine = "9router"
)

// mergeSources folds both upstreams into one preset set.
//
// OmniRoute wins every contested structural field: its transcription has been
// reviewed across nine phases and 9router's has not. 9router fills what
// OmniRoute does not carry, and supplies providers OmniRoute never listed.
func mergeSources(omni []entry, display map[string]displayEntry, nine []nineEntry) merged {
	out := merged{Presets: catalog.Presets{}, Origins: map[string][]fieldOrigin{}}

	for _, e := range omni {
		p := e.toPreset(display[e.id])
		out.Presets[e.id] = p
		out.Origins[e.id] = originsOf(p, srcOmni)
	}

	for _, n := range nine {
		if !n.routable() {
			continue
		}
		p, ok := out.Presets[n.ID]
		if !ok {
			out.Presets[n.ID] = n.toPreset()
			out.Origins[n.ID] = originsOf(out.Presets[n.ID], srcNine)
			continue
		}
		filled := fillGaps(&p, n)
		out.Presets[n.ID] = p
		out.Origins[n.ID] = append(out.Origins[n.ID], filled...)
	}

	for id := range out.Origins {
		sort.Slice(out.Origins[id], func(i, j int) bool {
			return out.Origins[id][i].Field < out.Origins[id][j].Field
		})
	}
	return out
}

// fillGaps takes from 9router only what the winning source left empty, and
// reports which fields it filled.
func fillGaps(p *catalog.Preset, n nineEntry) []fieldOrigin {
	var filled []fieldOrigin
	set := func(field string, dst *string, val string) {
		if *dst == "" && val != "" {
			*dst = val
			filled = append(filled, fieldOrigin{Field: field, Source: srcNine})
		}
	}
	set("name", &p.Name, n.Display.Name)
	set("website", &p.Website, n.Display.Website)
	set("api_key_url", &p.APIKeyURL, n.Display.Notice.APIKeyURL)
	set("base_url", &p.BaseURL, trimAPISuffix(n.Transport.BaseURL))
	return filled
}

// toPreset builds a preset from a 9router entry alone, for a provider
// OmniRoute never listed.
func (e nineEntry) toPreset() catalog.Preset {
	p := catalog.Preset{
		Name:      e.Display.Name,
		Kind:      "openaicompat",
		BaseURL:   trimAPISuffix(e.Transport.BaseURL),
		Surfaces:  []string{"llm"},
		Website:   e.Display.Website,
		APIKeyURL: e.Display.Notice.APIKeyURL,
		Auth:      catalog.Auth{Style: "bearer"},
	}
	if e.Name() != "" {
		p.Name = e.Name()
	}
	for _, k := range e.ServiceKinds {
		if k == "embedding" {
			p.Surfaces = []string{"embedding"}
		}
	}
	// A preset with no models.dev counterpart must say so explicitly; a
	// missing join key and a forgotten one look identical otherwise.
	p.NoModelsDev = true
	return p
}

// Name falls back to the id when upstream carries no display name, so a preset
// never renders as an empty string in the picker.
func (e nineEntry) Name() string {
	if e.Display.Name != "" {
		return e.Display.Name
	}
	return e.ID
}

// trimAPISuffix strips the endpoint path so what remains is the API root the
// adapter appends its own path to. Longest first, for the reason chatSuffixes
// documents.
func trimAPISuffix(u string) string {
	base := strings.TrimRight(u, "/")
	for _, s := range chatSuffixes {
		if strings.HasSuffix(base, s) {
			return strings.TrimSuffix(base, s)
		}
	}
	return base
}

func originsOf(p catalog.Preset, source string) []fieldOrigin {
	var out []fieldOrigin
	for field, val := range map[string]string{
		"name":        p.Name,
		"base_url":    p.BaseURL,
		"website":     p.Website,
		"api_key_url": p.APIKeyURL,
	} {
		if val != "" {
			out = append(out, fieldOrigin{Field: field, Source: source})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Field < out[j].Field })
	return out
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./tools/presetgen/ -run 'Merge|OmniRouteWins|NineRouter|NonLLM|Embedding' -v`
Expected: PASS, all five.

- [ ] **Step 6: Run the presetgen and catalog suites**

Run: `timeout 300 go test ./tools/presetgen/ ./internal/catalog/`
Expected: PASS. The new `APIKeyURL` field is optional, so `preset_test.go`'s `KnownFields(true)` decode still accepts every existing entry.

- [ ] **Step 7: Commit**

```bash
git add tools/presetgen/merge.go tools/presetgen/merge_test.go internal/catalog/preset.go
git commit -m "feat(presetgen): merge two upstream registries"
```

---

### Task 9: Report conflicts on raw values

**Files:**
- Modify: `tools/presetgen/merge.go`
- Create: `tools/presetgen/conflicts.go`
- Test: `tools/presetgen/merge_test.go`

**Interfaces:**
- Consumes: `merged` and `fieldOrigin` from Task 8.
- Produces: `type conflict struct{ ID, Field, Winner, WinnerValue, Loser, LoserValue string }` on `merged.Conflicts`, and `func writeConflicts(path string, c []conflict) error`.

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 1 = 3
**Approach:** inline - skip 3: spec §6 requires comparison on raw pre-trim values

- [ ] **Step 1: Write the failing test**

Append to `tools/presetgen/merge_test.go`:

```go
// The hazard the spec names: two sources whose base URLs differ only in the
// endpoint path agree after trimming. Comparing trimmed values would call that
// resolved and ship a wrong root silently.
func TestConflictIsDetectedOnRawValues(t *testing.T) {
	omni := []entry{{id: "p", baseURL: "https://api.example.com/v1/chat/completions"}}
	nine := []nineEntry{{ID: "p", Transport: nineTransport{BaseURL: "https://api.example.com/v2/messages"}}}

	got := mergeSources(omni, map[string]displayEntry{}, nine)
	if len(got.Conflicts) != 1 {
		t.Fatalf("got %d conflicts, want 1: %v", len(got.Conflicts), got.Conflicts)
	}
	c := got.Conflicts[0]
	if c.Field != "base_url" || c.Winner != "omniroute" {
		t.Errorf("conflict = %+v", c)
	}
	if c.LoserValue != "https://api.example.com/v2/messages" {
		t.Errorf("LoserValue = %q, want the raw upstream value", c.LoserValue)
	}
}

func TestAgreeingSourcesProduceNoConflict(t *testing.T) {
	omni := []entry{{id: "p", baseURL: "https://api.example.com/v1/chat/completions"}}
	nine := []nineEntry{{ID: "p", Transport: nineTransport{BaseURL: "https://api.example.com/v1/chat/completions"}}}

	if got := mergeSources(omni, map[string]displayEntry{}, nine); len(got.Conflicts) != 0 {
		t.Errorf("got %v, want no conflicts", got.Conflicts)
	}
}

// A quirk 9router declares that darkrouter's closed vocabulary has no name for
// must reach the reviewer and never reach Preset.Quirks.
func TestUnmappedQuirkIsReportedNotApplied(t *testing.T) {
	omni := []entry{{id: "p", baseURL: "https://api.example.com/v1"}}
	nine := []nineEntry{{ID: "p", Transport: nineTransport{
		BaseURL: "https://api.example.com/v1",
		Quirks:  map[string]bool{"dropClientMetadata": true},
	}}}

	got := mergeSources(omni, map[string]displayEntry{}, nine)
	if len(got.Presets["p"].Quirks) != 0 {
		t.Errorf("Quirks = %v, want none applied", got.Presets["p"].Quirks)
	}
	var found bool
	for _, c := range got.Conflicts {
		if c.Field == "quirk:dropClientMetadata" {
			found = true
		}
	}
	if !found {
		t.Errorf("conflicts = %v, want the unmapped quirk reported", got.Conflicts)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./tools/presetgen/ -run 'Conflict|Quirk' -v`
Expected: FAIL — `got.Conflicts undefined`.

- [ ] **Step 3: Add conflicts to the merge**

In `tools/presetgen/merge.go`, add the type and the field:

```go
// conflict is one disagreement between the two upstreams, recorded on raw
// pre-trim values so two URLs that agree only after transformation still
// surface. Precedence still resolves the merge; this is the review trail.
type conflict struct {
	ID, Field           string
	Winner, WinnerValue string
	Loser, LoserValue   string
}
```

Add `Conflicts []conflict` to `merged`. In `mergeSources`, in the branch where a 9router entry meets an existing preset, before `fillGaps`:

```go
		out.Conflicts = append(out.Conflicts, contestedFields(n.ID, rawBaseURL(omni, n.ID), n)...)
```

and add:

```go
// contestedFields compares the two upstreams on the values they actually
// published, not on what trimming made of them.
func contestedFields(id, omniRaw string, n nineEntry) []conflict {
	var out []conflict
	if omniRaw != "" && n.Transport.BaseURL != "" && omniRaw != n.Transport.BaseURL {
		out = append(out, conflict{
			ID: id, Field: "base_url",
			Winner: srcOmni, WinnerValue: omniRaw,
			Loser: srcNine, LoserValue: n.Transport.BaseURL,
		})
	}
	// A quirk darkrouter has no name for is reported, never applied: the
	// vocabulary is closed and a guessed mapping silently changes request shape.
	names := make([]string, 0, len(n.Transport.Quirks))
	for q, on := range n.Transport.Quirks {
		if on {
			names = append(names, q)
		}
	}
	sort.Strings(names)
	for _, q := range names {
		out = append(out, conflict{
			ID: id, Field: "quirk:" + q,
			Winner: srcOmni, WinnerValue: "(not applied)",
			Loser: srcNine, LoserValue: "declared upstream",
		})
	}
	return out
}

func rawBaseURL(omni []entry, id string) string {
	for _, e := range omni {
		if e.id == id {
			return e.baseURL
		}
	}
	return ""
}
```

Sort `out.Conflicts` by `ID` then `Field` before returning, so the weekly diff is stable.

- [ ] **Step 4: Write the conflicts artifact**

Create `tools/presetgen/conflicts.go`:

```go
package main

import (
	"fmt"
	"os"
	"strings"
)

// writeConflicts renders the review table. It is regenerated wholesale each
// run, so a disagreement somebody resolved drops off by itself.
func writeConflicts(path string, cs []conflict) error {
	var b strings.Builder
	b.WriteString("# presetgen conflicts\n\n")
	b.WriteString("Generated by tools/presetgen. Do not hand-edit.\n\n")
	if len(cs) == 0 {
		b.WriteString("The two upstream registries agree on every field.\n")
		return os.WriteFile(path, []byte(b.String()), 0o644)
	}
	b.WriteString("| Preset | Field | Winner | Winning value | Loser | Losing value |\n")
	b.WriteString("|---|---|---|---|---|---|\n")
	for _, c := range cs {
		fmt.Fprintf(&b, "| %s | %s | %s | `%s` | %s | `%s` |\n",
			c.ID, c.Field, c.Winner, c.WinnerValue, c.Loser, c.LoserValue)
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go test ./tools/presetgen/ -run 'Conflict|Quirk' -v`
Expected: PASS, all three.

- [ ] **Step 6: Commit**

```bash
git add tools/presetgen/merge.go tools/presetgen/conflicts.go tools/presetgen/merge_test.go
git commit -m "feat(presetgen): report upstream disagreements"
```

---

### Task 10: Emit the provenance manifest

**Files:**
- Create: `tools/presetgen/provenance.go`, `internal/catalog/provenance.go`, `internal/catalog/provenance_test.go`
- Test: `tools/presetgen/provenance_test.go`

**Interfaces:**
- Consumes: `merged.Origins` from Task 8.
- Produces: `func writeProvenance(path string, m merged, meta manifestMeta) error`; `catalog.Provenance` with `//go:embed provenance.yaml` and `func FieldOrigin(presetID, field string) (string, bool)`.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4
**Approach:** inline - skip 3: spec §4.1 fixed the manifest as the structural-plane carrier

- [ ] **Step 1: Write the failing generator test**

Create `tools/presetgen/provenance_test.go`:

```go
package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProvenanceRecordsSourceAndUpstreamSHAs(t *testing.T) {
	m := merged{Origins: map[string][]fieldOrigin{
		"groq": {{Field: "api_key_url", Source: "9router"}, {Field: "base_url", Source: "omniroute"}},
	}}
	path := filepath.Join(t.TempDir(), "provenance.yaml")
	meta := manifestMeta{OmniRouteSHA: "a1b2c3d", NineRouterSHA: "e4f5a6b", GeneratedAt: "2026-09-03"}
	if err := writeProvenance(path, m, meta); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"a1b2c3d", "e4f5a6b", "api_key_url: 9router", "base_url: omniroute"} {
		if !strings.Contains(string(got), want) {
			t.Errorf("manifest missing %q:\n%s", want, got)
		}
	}
}

// Two runs on identical input must produce identical bytes, or the weekly PR
// diffs for nothing.
func TestProvenanceIsByteStable(t *testing.T) {
	m := merged{Origins: map[string][]fieldOrigin{
		"b": {{Field: "base_url", Source: "omniroute"}},
		"a": {{Field: "website", Source: "9router"}, {Field: "base_url", Source: "omniroute"}},
	}}
	meta := manifestMeta{OmniRouteSHA: "x", NineRouterSHA: "y", GeneratedAt: "2026-09-03"}
	dir := t.TempDir()
	one, two := filepath.Join(dir, "1.yaml"), filepath.Join(dir, "2.yaml")
	if err := writeProvenance(one, m, meta); err != nil {
		t.Fatal(err)
	}
	if err := writeProvenance(two, m, meta); err != nil {
		t.Fatal(err)
	}
	a, _ := os.ReadFile(one)
	b, _ := os.ReadFile(two)
	if string(a) != string(b) {
		t.Errorf("two runs differ:\n%s\n---\n%s", a, b)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./tools/presetgen/ -run TestProvenance -v`
Expected: FAIL — `undefined: writeProvenance`.

- [ ] **Step 3: Write the generator**

Create `tools/presetgen/provenance.go`:

```go
package main

import (
	"fmt"
	"os"
	"sort"
	"strings"
)

// manifestMeta stamps the run. Without the upstream SHAs nothing answers
// whether a field changed because upstream moved or because a scraper did.
type manifestMeta struct {
	OmniRouteSHA  string
	NineRouterSHA string
	GeneratedAt   string
}

func writeProvenance(path string, m merged, meta manifestMeta) error {
	var b strings.Builder
	b.WriteString("# Generated by tools/presetgen. Do not hand-edit.\n")
	b.WriteString("#\n")
	b.WriteString("# Which upstream supplied each structural field. Price provenance is a\n")
	b.WriteString("# runtime fact and lives in the models.price_source column instead.\n")
	fmt.Fprintf(&b, "omniroute_sha: %s\n", meta.OmniRouteSHA)
	fmt.Fprintf(&b, "ninerouter_sha: %s\n", meta.NineRouterSHA)
	fmt.Fprintf(&b, "generated_at: %s\n", meta.GeneratedAt)
	b.WriteString("presets:\n")

	ids := make([]string, 0, len(m.Origins))
	for id := range m.Origins {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		fmt.Fprintf(&b, "  %s:\n", id)
		origins := append([]fieldOrigin(nil), m.Origins[id]...)
		sort.Slice(origins, func(i, j int) bool { return origins[i].Field < origins[j].Field })
		for _, o := range origins {
			fmt.Fprintf(&b, "    %s: %s\n", o.Field, o.Source)
		}
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}
```

- [ ] **Step 4: Run the generator tests to verify they pass**

Run: `go test ./tools/presetgen/ -run TestProvenance -v`
Expected: PASS, both.

- [ ] **Step 5: Write the failing reader test**

Create `internal/catalog/provenance_test.go`:

```go
package catalog

import "testing"

func TestFieldOriginReadsTheManifest(t *testing.T) {
	// Every preset the manifest names must exist, or the two files have drifted.
	for id := range Provenance().Presets {
		if _, ok := Embedded()[id]; !ok {
			t.Errorf("manifest names preset %q, which presets.yaml does not carry", id)
		}
	}
}

// A parse failure degrades to an empty manifest rather than panicking, for the
// reason FallbackDoc does: a gateway that refuses to boot over a provenance
// label is a worse outcome than one that shows none.
func TestProvenanceDegradesToEmpty(t *testing.T) {
	if Provenance().Presets == nil {
		t.Error("Provenance() returned a nil map; want an empty one")
	}
}
```

Use whatever accessor `internal/catalog` already exposes for the embedded presets in place of `Embedded()`; read `preset.go` first.

- [ ] **Step 6: Run it to verify it fails**

Run: `go test ./internal/catalog/ -run TestProvenance -v` — expected FAIL, `undefined: Provenance`.

- [ ] **Step 7: Write the reader**

Create `internal/catalog/provenance.go`:

```go
package catalog

import (
	_ "embed"
	"sync"

	"gopkg.in/yaml.v3"
)

//go:embed provenance.yaml
var provenanceYAML []byte

// ProvenanceDoc is which upstream supplied each structural field, plus the
// commits it was read from.
type ProvenanceDoc struct {
	OmniRouteSHA  string                       `yaml:"omniroute_sha"`
	NineRouterSHA string                       `yaml:"ninerouter_sha"`
	GeneratedAt   string                       `yaml:"generated_at"`
	Presets       map[string]map[string]string `yaml:"presets"`
}

var (
	provOnce sync.Once
	provDoc  ProvenanceDoc
)

func Provenance() ProvenanceDoc {
	provOnce.Do(func() {
		if err := yaml.Unmarshal(provenanceYAML, &provDoc); err != nil || provDoc.Presets == nil {
			provDoc = ProvenanceDoc{Presets: map[string]map[string]string{}}
		}
	})
	return provDoc
}

// FieldOrigin names the upstream a preset's field came from.
func FieldOrigin(presetID, field string) (string, bool) {
	src, ok := Provenance().Presets[presetID][field]
	return src, ok
}
```

Create a placeholder `internal/catalog/provenance.yaml` so the embed compiles before the first generator run:

```yaml
# Generated by tools/presetgen. Do not hand-edit.
omniroute_sha: ""
ninerouter_sha: ""
generated_at: ""
presets: {}
```

- [ ] **Step 8: Run both suites to verify they pass**

Run: `timeout 300 go test ./internal/catalog/ ./tools/presetgen/`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add tools/presetgen/provenance.go tools/presetgen/provenance_test.go internal/catalog/provenance.go internal/catalog/provenance.yaml internal/catalog/provenance_test.go
git commit -m "feat(catalog): record which upstream supplied a field"
```

---

### Task 11: Wire the second upstream into presetgen

**Files:**
- Modify: `tools/presetgen/main.go:39-120` (flags and the main flow), `tools/presetgen/merge.go` (adds `markOverridden`, Step 7)
- Test: `tools/presetgen/main_test.go`

**Interfaces:**
- Consumes: `scrapeNineRouter` (Task 6), `mergeSources` (Task 8), `writeConflicts` (Task 9), `writeProvenance` (Task 10).
- Produces: the `-ninerouter`, `-out-conflicts` and `-out-provenance` flags.

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 1 = 3
**Approach:** inline - skip 2: follows the existing flag-and-emit pattern in this file

- [ ] **Step 1: Write the failing test**

Append to `tools/presetgen/main_test.go`:

```go
// carryQuirks and applyOverrides run after the merge, so a hand-reviewed
// correction still beats both upstreams.
func TestOverridesStillWinOverBothUpstreams(t *testing.T) {
	omni := []entry{{id: "p", baseURL: "https://omni.example/v1"}}
	nine := []nineEntry{{ID: "p", Transport: nineTransport{BaseURL: "https://nine.example/v1"}}}
	m := mergeSources(omni, map[string]displayEntry{}, nine)

	dir := t.TempDir()
	overrides := filepath.Join(dir, "overrides.yaml")
	if err := os.WriteFile(overrides, []byte("p:\n  base_url: https://override.example/v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := applyOverrides(m.Presets, overrides); err != nil {
		t.Fatal(err)
	}
	if got := m.Presets["p"].BaseURL; got != "https://override.example/v1" {
		t.Errorf("BaseURL = %q, want the override", got)
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./tools/presetgen/ -run TestOverridesStillWin -v`
Expected: FAIL until the merge is wired; if it passes already, the merge is correct and this test is a regression guard — proceed.

- [ ] **Step 3: Add the flags**

In `main()`, beside the existing flags:

```go
	nineRouter := flag.String("ninerouter", "", "path to the 9router checkout")
	outConflicts := flag.String("out-conflicts", "internal/catalog/presetgen-conflicts.md", "generated review table")
	outProvenance := flag.String("out-provenance", "internal/catalog/provenance.yaml", "generated field-origin manifest")
	omniSHA := flag.String("omniroute-sha", "", "OmniRoute commit the registry was read from")
	nineSHA := flag.String("ninerouter-sha", "", "9router commit the registry was read from")
```

`-ninerouter` stays optional: an empty value means OmniRoute alone, which keeps a local run working for anyone without the second checkout.

- [ ] **Step 4: Replace the preset construction with the merge**

Where `main` currently builds `presets` by looping over `entries` and calling `toPreset`, substitute:

```go
	var nine []nineEntry
	if *nineRouter != "" {
		nine, err = scrapeNineRouter(filepath.Join(*nineRouter, "open-sse/providers/registry"))
		if err != nil {
			log.Fatal(err)
		}
	}
	m := mergeSources(entries, display, nine)
	presets := m.Presets

	joined := 0
	for id, p := range presets {
		if _, ok := doc[id]; ok {
			p.ModelsDevID, p.NoModelsDev = id, false
			joined++
		} else {
			p.NoModelsDev = true
		}
		presets[id] = p
	}
```

The `carryQuirks`, `applyOverrides` and every `write*` call that follows stay exactly as they are. After `writePresets`, add:

```go
	if err := writeConflicts(*outConflicts, m.Conflicts); err != nil {
		log.Fatal(err)
	}
	if err := writeProvenance(*outProvenance, m, manifestMeta{
		OmniRouteSHA:  *omniSHA,
		NineRouterSHA: *nineSHA,
		GeneratedAt:   time.Now().UTC().Format("2006-01-02"),
	}); err != nil {
		log.Fatal(err)
	}
	log.Printf("presetgen: %d presets from 9router only, %d conflicts recorded",
		len(nine), len(m.Conflicts))
```

- [ ] **Step 5: Write the failing override-provenance test**

A hand-corrected field must read as `override` in the manifest, not as whichever
scraper happened to win the merge. Append to `tools/presetgen/main_test.go`:

```go
func TestOverriddenFieldIsAttributedToTheOverride(t *testing.T) {
	omni := []entry{{id: "p", baseURL: "https://omni.example/v1"}}
	m := mergeSources(omni, map[string]displayEntry{}, nil)

	dir := t.TempDir()
	overrides := filepath.Join(dir, "overrides.yaml")
	if err := os.WriteFile(overrides, []byte("p:\n  base_url: https://override.example/v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := applyOverrides(m.Presets, overrides); err != nil {
		t.Fatal(err)
	}
	markOverridden(&m, overrides)

	if !hasOrigin(m, "p", "base_url", "override") {
		t.Errorf("origins = %v, want base_url attributed to override", m.Origins["p"])
	}
}
```

- [ ] **Step 6: Run it to verify it fails**

Run: `go test ./tools/presetgen/ -run TestOverriddenFieldIsAttributed -v`
Expected: FAIL — `undefined: markOverridden`.

- [ ] **Step 7: Record override origins**

Add to `tools/presetgen/merge.go`:

```go
// markOverridden re-attributes every field the overrides file declares. An
// override outranks both upstreams, so the manifest must not keep crediting
// the scraper whose value was replaced.
func markOverridden(m *merged, overridesPath string) {
	raw, err := os.ReadFile(overridesPath)
	if err != nil {
		return
	}
	var declared map[string]map[string]any
	if err := yaml.Unmarshal(raw, &declared); err != nil {
		return
	}
	for id, fields := range declared {
		for field := range fields {
			replaced := false
			for i := range m.Origins[id] {
				if m.Origins[id][i].Field == field {
					m.Origins[id][i].Source = "override"
					replaced = true
				}
			}
			if !replaced {
				m.Origins[id] = append(m.Origins[id], fieldOrigin{Field: field, Source: "override"})
			}
		}
		sort.Slice(m.Origins[id], func(i, j int) bool {
			return m.Origins[id][i].Field < m.Origins[id][j].Field
		})
	}
}
```

Add `"os"` and `"gopkg.in/yaml.v3"` to that file's imports. Call it in `main` immediately after `applyOverrides`:

```go
	markOverridden(&m, *overrides)
```

- [ ] **Step 8: Run the tests to verify they pass**

Run: `timeout 300 go test ./tools/presetgen/`
Expected: PASS.

- [ ] **Step 9: Regenerate against the real checkouts and inspect**

```bash
curl -s -o /tmp/modelsdev.json https://models.dev/api.json
go run ./tools/presetgen \
  -omniroute /root/repositories-community/OmniRoute \
  -ninerouter /root/repositories-community/9router \
  -modelsdev /tmp/modelsdev.json \
  -omniroute-sha "$(git -C /root/repositories-community/OmniRoute rev-parse --short HEAD)" \
  -ninerouter-sha "$(git -C /root/repositories-community/9router rev-parse --short HEAD)"
git diff --stat
```

Expected: `presets.yaml` gains roughly 25 providers, `provenance.yaml` and `presetgen-conflicts.md` are written. Read the conflicts table before continuing — a base-URL disagreement on a provider that already worked is a signal to add an override, not to proceed.

- [ ] **Step 10: Run the whole Go suite**

Run: `timeout 900 go test ./...`
Expected: PASS.

- [ ] **Step 11: Commit**

```bash
git add tools/presetgen/main.go tools/presetgen/main_test.go internal/catalog/presets.yaml internal/catalog/provenance.yaml internal/catalog/presetgen-conflicts.md
git commit -m "feat(presetgen): ingest the second upstream"
```

---

### Task 12: Schedule the refresh workflow

**Files:**
- Create: `.github/workflows/catalog-refresh.yml`

**Interfaces:**
- Consumes: the `presetgen` CLI as wired in Task 11.
- Produces: nothing other tasks read.

**Implementer:** dcc-superpower-companions:impl-sonnet-medium
**Evaluation:** files 0 - spec 0 - coupling 1 - risk 1 = 2
**Approach:** inline - skip 3: spec §8 fixed the workflow's shape

- [ ] **Step 1: Write the workflow**

Create `.github/workflows/catalog-refresh.yml`:

```yaml
name: Refresh provider catalogue

# Weekly. The upstream registries change when somebody does a research pass,
# which is days apart at best — polling harder would be traffic that cannot
# find news.
on:
  schedule:
    - cron: "0 6 * * 1"
  workflow_dispatch:

permissions:
  contents: write
  pull-requests: write

concurrency:
  group: catalog-refresh
  cancel-in-progress: true

jobs:
  refresh:
    runs-on: ubuntu-latest
    timeout-minutes: 20
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
          cache: true

      # Node evaluates the 9router registry: 14 of its files import from
      # elsewhere in that repository and cannot be read statically.
      - uses: actions/setup-node@v4
        with:
          node-version: 24

      - name: Clone the upstream registries
        run: |
          git clone --depth 1 https://github.com/diegosouzapw/OmniRoute /tmp/omniroute
          git clone --depth 1 https://github.com/decolua/9router /tmp/ninerouter
          echo "OMNI_SHA=$(git -C /tmp/omniroute rev-parse --short HEAD)" >> "$GITHUB_ENV"
          echo "NINE_SHA=$(git -C /tmp/ninerouter rev-parse --short HEAD)" >> "$GITHUB_ENV"

      - name: Fetch the models.dev index
        run: curl -sS --fail --max-time 60 -o /tmp/modelsdev.json https://models.dev/api.json

      - name: Regenerate the catalogue
        run: |
          go run ./tools/presetgen \
            -omniroute /tmp/omniroute \
            -ninerouter /tmp/ninerouter \
            -modelsdev /tmp/modelsdev.json \
            -omniroute-sha "$OMNI_SHA" \
            -ninerouter-sha "$NINE_SHA"

      # The closed-vocabulary and preset-shape tests are the gate. A malformed
      # upstream commit fails here rather than reaching a reviewer as a green PR.
      - name: Test
        run: go test ./internal/catalog/... ./tools/presetgen/...

      - name: Summarise the change
        id: summary
        run: |
          {
            echo 'body<<PRBODY'
            echo "OmniRoute \`$OMNI_SHA\`, 9router \`$NINE_SHA\`."
            echo
            echo '### Fields changed, by source'
            git diff -U0 internal/catalog/provenance.yaml \
              | grep -E '^\+ +[a-z_]+: ' \
              | awk -F': ' '{print $2}' | sort | uniq -c | sort -rn \
              | awk '{print "- " $2 ": " $1 " field(s)"}' || echo '- none'
            echo
            echo '### Conflicts'
            sed -n '/^|/p' internal/catalog/presetgen-conflicts.md || echo 'None.'
            echo PRBODY
          } >> "$GITHUB_OUTPUT"

      - uses: peter-evans/create-pull-request@v6
        with:
          branch: chore/catalogue-refresh
          title: "chore(catalog): refresh the provider catalogue"
          body: ${{ steps.summary.outputs.body }}
          commit-message: "chore(catalog): refresh the provider catalogue"
          labels: catalogue
```

The workflow never auto-merges. A hostile or malformed upstream commit reaches a human, which is the whole reason structure goes through a PR while volatile data goes through the runtime path.

- [ ] **Step 2: Validate the YAML parses**

Run: `python3 -c "import yaml,sys; yaml.safe_load(open('.github/workflows/catalog-refresh.yml')); print('ok')"`
Expected: `ok`.

- [ ] **Step 3: Dry-run the generator step locally**

Run the same `go run ./tools/presetgen …` command from Task 11 Step 6 and confirm it still exits 0 with both checkouts present.
Expected: exit 0, no diff beyond what Task 11 already committed.

- [ ] **Step 4: Commit**

```bash
git add .github/workflows/catalog-refresh.yml
git commit -m "ci(catalog): refresh the catalogue weekly"
```

- [ ] **Step 5: Deploy**

Per `CLAUDE.md`, redeploy once now that the phase is complete, following `docs/DEPLOY.md` "Local build (UAT)". The `compose.uat.yml` overlay is required or the published image is pulled over the local build, and this machine publishes the admin port on **8091**. Then log in with the password from `.uat-credentials` and confirm the models screen renders the price markers from Task 5.

---

## Verification

After Task 12, confirm the whole phase:

```bash
timeout 900 go test ./...
cd web && timeout 600 npm test && timeout 300 npm run lint
```

Then check the three claims this phase makes:

1. `internal/catalog/presets.yaml` carries providers that were not there before — `git log -p --stat internal/catalog/presets.yaml | head -40`.
2. `internal/catalog/provenance.yaml` attributes at least one field to `9router`.
3. `internal/catalog/presetgen-conflicts.md` exists and every row in it has been read.
