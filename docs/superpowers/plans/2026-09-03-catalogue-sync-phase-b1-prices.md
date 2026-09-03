# Catalogue Sync Phase B1 — Price Federation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

> **Implementer assignments:** each task names its implementer agent in an
> `**Implementer:**` line. When executing with
> superpowers:subagent-driven-development, REQUIRED SUB-SKILL:
> dcc-superpower-companions:dispatching-tiered-implementers. Under
> superpowers:executing-plans these lines are inert; ignore them.

**Goal:** Price the 441 of 459 catalogue rows that currently have no price, by federating a LiteLLM index and the gateway's own discovery sweep alongside models.dev, each carrying the provenance grade Phase A landed.

**Architecture:** Price resolution moves out of `mergeOne`'s binary branch into `resolvePrice`, an ordered-candidate function modelled on the existing `surfaces()`. The row keeps one price slot whose values are gated on its stamp; LiteLLM and registry prices are joined in memory at merge time so they stay re-resolvable without storage.

**Tech Stack:** Go, SQLite (`internal/store/migrations`), React + TypeScript (`web/`).

**Spec:** `docs/superpowers/specs/2026-09-03-catalogue-sync-phase-b-design.md`

**Scope note:** this plan implements the price half of the Phase B spec — §§1-5 and the price parts of §7-8. The free-tier record (§6) is a separate plan, B2, and can be built independently.

One source named in spec §3 is deliberately **not** in this plan: prices declared inline in the upstream provider registries. It is the smallest of the three by a wide margin — a handful of entries against LiteLLM's 2,352 — and it comes from the same registry files B2 already parses for the free-tier record, so it lands there rather than duplicating that parse here. `SourceRegistry` therefore keeps zero members until B2, exactly as `SourceLiteLLM` did until this plan.

## Global Constraints

- **Never hardcode a font size.** No `text-xs`, no `text-[11px]`, no `text-[length:var(…)]`, no `font-size: <n>px`. Only `text-sm`, `text-base`, `text-lg`, `text-xl`, `text-2xl`, `text-3xl`. `text-sm` (14px) is the floor; hierarchy below body text comes from colour and weight. Enforced by `web/src/design-system.test.ts`.
- **English only** in code, comments, docs, commits, configs, errors and tests.
- **Commit format:** `<type>(<scope>): <subject>`, type one of `feat|fix|docs|style|refactor|test|chore|perf`, subject **≤50 characters**, imperative, no trailing period. Count it before committing.
- **Comments explain WHY, never WHAT.** Default to none. Never reference a task or plan step in a comment.
- **`catalog.Quirks` and `catalog.AuthStyles` are CLOSED vocabularies** enforced by `internal/catalog/preset_test.go`.
- **A source with nothing to say contributes no candidate**, never a zeroed `Pricing`. This rule is why a silent listing cannot outrank a real price.
- **Test fixtures for third-party data come from verbatim real files**, checked into `testdata/` and decoded through the production parser — never hand-built struct literals. Phase A shipped two Critical defects because fixtures encoded the plan's assumptions rather than the upstream's schema.
- **Prove a test discriminates** by breaking the implementation and watching it go red, then restoring. Quote both runs.
- Give every test command an explicit timeout. `go` lives at `/usr/local/go/bin/go`; add it to PATH if `go` is not found.
- `-run TestFoo` is a regex that does not substring-match a differently-prefixed name. Run each new test by its own name.

---

### Task 1: Add Source.Authoritative

**Files:**
- Modify: `internal/catalog/catalog.go` (beside `Grade()`)
- Test: `internal/catalog/catalog_test.go`

**Interfaces:**
- Consumes: `catalog.Source` and its six constants.
- Produces: `func (s Source) Authoritative() bool` — true for `SourceOverride`, `SourceDiscovered`, `SourceModelsDev`; false for `SourceLiteLLM`, `SourceRegistry`, `SourceInferred` and any unknown value.

**Implementer:** dcc-superpower-companions:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 0 = 2
**Approach:** inline - skip 3: the spec's §4.2 fixed the predicate and its membership

- [ ] **Step 1: Write the failing test**

```go
func TestSourceAuthoritative(t *testing.T) {
	cases := map[Source]bool{
		SourceOverride:   true,
		SourceDiscovered: true,
		SourceModelsDev:  true,
		SourceLiteLLM:    false,
		SourceRegistry:   false,
		SourceInferred:   false,
	}
	for src, want := range cases {
		if got := src.Authoritative(); got != want {
			t.Errorf("Source(%q).Authoritative() = %v, want %v", src, got, want)
		}
	}
}

// An unrecognised source is not authoritative: a stored stamp we cannot read
// must not displace a directory price we can.
func TestUnknownSourceIsNotAuthoritative(t *testing.T) {
	if Source("something-new").Authoritative() {
		t.Error("unknown source reported authoritative")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `timeout 120 go test ./internal/catalog/ -run 'TestSourceAuthoritative|TestUnknownSourceIsNotAuthoritative' -v`
Expected: FAIL — `src.Authoritative undefined`.

- [ ] **Step 3: Write the implementation**

In `internal/catalog/catalog.go`, below `Grade()`:

```go
// Authoritative reports whether a stored stamp outranks a third-party index.
// It is a different question from Grade: an operator's own correction is the
// least "official" grade and still the most authoritative value, because they
// entered it deliberately to replace what a directory said.
func (s Source) Authoritative() bool {
	switch s {
	case SourceOverride, SourceDiscovered, SourceModelsDev:
		return true
	default:
		return false
	}
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `timeout 120 go test ./internal/catalog/ -run 'TestSourceAuthoritative|TestUnknownSourceIsNotAuthoritative' -v`
Expected: PASS, both.

- [ ] **Step 5: Run the package suite**

Run: `timeout 300 go test ./internal/catalog/`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/catalog/catalog.go internal/catalog/catalog_test.go
git commit -m "feat(catalog): rank a stored price stamp"
```

---

### Task 2: Keep a stored price's values with its stamp

**Files:**
- Modify: `internal/catalog/sync.go` (the `MetadataRow` construction in `SyncOnce`, around the `priceSourceAfterSync` call)
- Test: `internal/catalog/sync_test.go`

**Interfaces:**
- Consumes: `priceSourceAfterSync` (already present from Phase A).
- Produces: nothing new; corrects existing behaviour.

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 3 = 5
**Approach:** inline - skip 2: mirrors the capabilities gate three lines above it in the same function

- [ ] **Step 1: Write the failing test**

Append to `internal/catalog/sync_test.go`. This package's sync tests use `syncFixture(t)` — which returns `(db, src, cat)` — and drive one sync through an `httptest.Server` serving a document string. Read `TestSyncWritesPricesAndLimits` (sync_test.go:49) and follow its setup exactly; do NOT use `migrated(t)` or `catalogDB(t)`, which live in package `store` and are unreachable from here.

```go
// A discovered price keeps BOTH its stamp and its numbers across a sync. The
// capabilities half of SyncOnce has always done this; the price half did not,
// so a row stamped discovered took models.dev's figures and kept the label —
// the console would then render "measured" over an indexed price.
func TestSyncKeepsADiscoveredPriceNotJustItsStamp(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(syncDoc))
	}))
	defer srv.Close()

	db, src, cat := syncFixture(t)
	ctx := context.Background()

	// Stamp one row as discovered with its own numbers before the sync runs.
	// Use a model id that syncDoc also prices, so the sync has something to
	// overwrite it with; read syncDoc and testPresets() to pick one.
	if err := db.UpsertMetadata(ctx, []store.MetadataRow{{
		ProviderID: "groq", ModelID: "big",
		InputMicrosPerMTok: 111, OutputMicrosPerMTok: 222,
		PriceSource: string(SourceDiscovered),
	}}); err != nil {
		t.Fatal(err)
	}

	s := NewSyncer(db, src, cat, SyncOptions{URL: srv.URL, Presets: testPresets()})
	if err := s.SyncOnce(ctx); err != nil {
		t.Fatal(err)
	}

	rows, err := db.Models(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := rowFor(t, rows, "groq", "big")
	if got.PriceSource != string(SourceDiscovered) {
		t.Errorf("PriceSource = %q, want discovered", got.PriceSource)
	}
	if got.InputMicrosPerMTok != 111 || got.OutputMicrosPerMTok != 222 {
		t.Errorf("prices = %d/%d, want the discovered 111/222 kept",
			got.InputMicrosPerMTok, got.OutputMicrosPerMTok)
	}
}
```

`rowFor` is a small local helper you write: scan `rows` for the matching provider and model id and `t.Fatalf` if absent — do not index `rows[0]`, whose order is not guaranteed. Confirm the provider and model ids against `syncDoc` and `testPresets()` in this file before using them; the ids above are the shape to follow, not values to trust.

- [ ] **Step 2: Run it to verify it fails**

Run: `timeout 120 go test ./internal/catalog/ -run TestSyncKeepsADiscoveredPriceNotJustItsStamp -v`
Expected: FAIL — prices read 999/888, models.dev's numbers, while the stamp stayed `discovered`.

- [ ] **Step 3: Gate the values on the stamp**

In `internal/catalog/sync.go`, where the `MetadataRow` is built, compute the source first and use it to choose the numbers — exactly as the capabilities block three lines above already does:

```go
priceSrc := priceSourceAfterSync(r.PriceSource)
in, out := meta.InputMicrosPerMTok, meta.OutputMicrosPerMTok
cacheRead, cacheWrite := meta.CacheReadMicrosPerMTok, meta.CacheWriteMicrosPerMTok
if priceSrc != string(SourceModelsDev) {
	// The row's stamp outranks this sync, so its numbers do too. Keeping the
	// label without the values would report a figure as measured that
	// models.dev supplied.
	in, out = r.InputMicrosPerMTok, r.OutputMicrosPerMTok
	cacheRead, cacheWrite = r.CacheReadMicrosPerMTok, r.CacheWriteMicrosPerMTok
}
```

Then pass `in`, `out`, `cacheRead`, `cacheWrite` and `priceSrc` into the row instead of the `meta.*` values directly.

- [ ] **Step 4: Run it to verify it passes**

Run: `timeout 120 go test ./internal/catalog/ -run TestSyncKeepsADiscoveredPriceNotJustItsStamp -v`
Expected: PASS.

- [ ] **Step 5: Prove the gate discriminates**

Temporarily revert the `if priceSrc != string(SourceModelsDev)` block so the `meta.*` values are always used. Re-run the test, confirm it goes RED, restore, confirm GREEN. Quote both runs.

- [ ] **Step 6: Run the catalog and store suites**

Run: `timeout 600 go test ./internal/catalog/ ./internal/store/`
Expected: PASS. The existing sync tests assert models.dev's numbers on rows stamped `models_dev` or `inferred`, which this change leaves alone.

- [ ] **Step 7: Commit**

```bash
git add internal/catalog/sync.go internal/catalog/sync_test.go
git commit -m "fix(catalog): keep a stored price with its stamp"
```

---

### Task 3: Store PriceKnown instead of deriving it

**Files:**
- Create: `internal/store/migrations/0019_price_known.sql`
- Modify: `internal/store/catalog.go` (`modelColumns`, the `Models` scan, `MetadataRow`, the metadata `UPDATE`)
- Test: `internal/store/catalog_test.go`, `internal/store/migrate_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `store.MetadataRow.PriceKnown bool`, persisted in `models.price_known`; `ModelRow.PriceKnown` now read from the column rather than derived.

**Implementer:** dcc-superpower-companions:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6
**Approach:** inline - skip 2: mirrors the `price_source` column added by migration 0018

- [ ] **Step 1: Write the failing test**

Append to `internal/store/catalog_test.go`:

```go
// A model whose price is genuinely zero is priced, not unpriced. nullableInt64
// writes NULL for zero, so deriving PriceKnown from column nullability read a
// free model back as "we never found out" — and the providers that publish
// prices in their listing are disproportionately the free-tier ones.
func TestAFreeModelRoundTripsAsPriced(t *testing.T) {
	db := catalogDB(t)
	ctx := context.Background()
	if err := db.UpsertMetadata(ctx, []MetadataRow{{
		ProviderID: "groq", ModelID: "m",
		InputMicrosPerMTok: 0, OutputMicrosPerMTok: 0,
		PriceKnown: true, PriceSource: "discovered",
	}}); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Models(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !rows[0].PriceKnown {
		t.Error("a zero-priced model read back as unpriced")
	}
}

func TestAnUnpricedModelStaysUnpriced(t *testing.T) {
	db := catalogDB(t)
	ctx := context.Background()
	if err := db.UpsertMetadata(ctx, []MetadataRow{{
		ProviderID: "groq", ModelID: "m", PriceKnown: false,
	}}); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Models(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].PriceKnown {
		t.Error("a model with no price read back as priced")
	}
}
```

Use whichever migrated-database helper this file's existing tests call — `catalogDB` in the current tree. Do not add a second helper.

- [ ] **Step 2: Run them to verify they fail**

Run: `timeout 120 go test ./internal/store/ -run 'TestAFreeModelRoundTripsAsPriced|TestAnUnpricedModelStaysUnpriced' -v`
Expected: FAIL — `unknown field PriceKnown in struct literal of type MetadataRow`.

- [ ] **Step 3: Write the migration**

Create `internal/store/migrations/0019_price_known.sql`:

```sql
-- A price of zero and no price at all are different facts, and the column
-- layout could not tell them apart.
--
-- nullableInt64 writes NULL for a zero rate, so a genuinely free model stored
-- four NULLs and read back through the old `inPrice.Valid || outPrice.Valid`
-- derivation as unpriced. Backfilled from that same derivation, which is
-- correct for every row written before this column existed: nothing could
-- store a known zero, so a NULL rate really did mean "never found out".
ALTER TABLE models ADD COLUMN price_known INTEGER NOT NULL DEFAULT 0;

UPDATE models SET price_known = 1
 WHERE input_price_micros_per_mtok IS NOT NULL
    OR output_price_micros_per_mtok IS NOT NULL;
```

- [ ] **Step 4: Write the migration test**

Append to `internal/store/migrate_test.go`:

```go
func TestPriceKnownBackfillsFromExistingPrices(t *testing.T) {
	db := migrated(t)
	if _, err := db.Write.Exec(
		`INSERT INTO providers (id) VALUES ('p')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write.Exec(
		`INSERT INTO models (provider_id, model_id, capabilities_source,
		    input_price_micros_per_mtok) VALUES ('p', 'priced', 'inferred', 150000)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Write.Exec(
		`INSERT INTO models (provider_id, model_id, capabilities_source)
		 VALUES ('p', 'bare', 'inferred')`); err != nil {
		t.Fatal(err)
	}
	var priced, bare int
	if err := db.Read.QueryRow(
		`SELECT price_known FROM models WHERE model_id = 'priced'`).Scan(&priced); err != nil {
		t.Fatal(err)
	}
	if err := db.Read.QueryRow(
		`SELECT price_known FROM models WHERE model_id = 'bare'`).Scan(&bare); err != nil {
		t.Fatal(err)
	}
	if priced != 1 || bare != 0 {
		t.Errorf("price_known priced=%d bare=%d, want 1 and 0", priced, bare)
	}
}
```

Note: rows inserted *after* the migration take the column default of 0, so this test asserts the default rather than the backfill. To assert the backfill itself, follow the pattern of `TestMigration0002RewritesLegacyRows`, which applies migrations up to N-1, inserts, then applies the rest. Use that pattern if this file provides the helper for it; if it does not, keep the default-assertion above and say so in your report.

- [ ] **Step 5: Wire the column through the store**

In `internal/store/catalog.go`: add `PriceKnown bool` to `MetadataRow`; add `price_known` to `modelColumns` immediately after `price_source`; add `&r.PriceKnown` to the `Models` scan in the matching position; add `price_known = ?` to the metadata `UPDATE` and its argument in the matching position; and **delete** the derivation `r.PriceKnown = inPrice.Valid || outPrice.Valid`, since the column now carries it.

- [ ] **Step 6: Run the tests to verify they pass**

Run: `timeout 300 go test ./internal/store/`
Expected: PASS. Any existing test asserting `PriceKnown` on a row written through `UpsertMetadata` must now set it explicitly; fix those call sites rather than restoring the derivation.

- [ ] **Step 7: Set PriceKnown where metadata is written**

`internal/catalog/sync.go` builds `MetadataRow` from a models.dev join; set `PriceKnown: meta.PriceKnown` there, gated on the stamp exactly like the values in Task 2.

- [ ] **Step 8: Run the full suite**

Run: `timeout 900 go test ./...`
Expected: PASS.

- [ ] **Step 9: Commit**

```bash
git add internal/store/migrations/0019_price_known.sql internal/store/catalog.go internal/store/catalog_test.go internal/store/migrate_test.go internal/catalog/sync.go
git commit -m "fix(store): store whether a price is known"
```

---

### Task 4: Resolve a price from ordered candidates

**Files:**
- Modify: `internal/catalog/merge.go` (add `resolvePrice`, rewrite the pricing half of `mergeOne`)
- Test: `internal/catalog/merge_test.go`

**Interfaces:**
- Consumes: `Source.Authoritative()` from Task 1; `Pricing` with its `Source` field.
- Produces: `func resolvePrice(candidates ...Pricing) Pricing` — returns the first candidate whose `Known` is true, or a zero `Pricing` with `Source: SourceInferred` when none is.

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5
**Approach:** advisor - the advisor pass confirmed the shape and corrected two details: model it on `surfaces()` positionally rather than a rank table, and drop the `priceCandidate` wrapper since `Pricing` already carries `Source`

- [ ] **Step 1: Write the failing test**

Append to `internal/catalog/merge_test.go`:

```go
func TestResolvePriceTakesTheFirstKnownCandidate(t *testing.T) {
	md := Pricing{InputMicrosPerMTok: 500, Known: true, Source: SourceModelsDev}
	ll := Pricing{InputMicrosPerMTok: 700, Known: true, Source: SourceLiteLLM}
	if got := resolvePrice(md, ll); got.Source != SourceModelsDev || got.InputMicrosPerMTok != 500 {
		t.Errorf("got %+v, want models.dev's 500", got)
	}
}

// An unknown candidate is skipped, not returned. A source that had nothing to
// say must not shadow one that did.
func TestResolvePriceSkipsUnknownCandidates(t *testing.T) {
	empty := Pricing{Source: SourceDiscovered}
	ll := Pricing{InputMicrosPerMTok: 700, Known: true, Source: SourceLiteLLM}
	if got := resolvePrice(empty, ll); got.Source != SourceLiteLLM {
		t.Errorf("got %+v, want the litellm price", got)
	}
}

// A known price of zero is a price. This is the free-model case.
func TestResolvePriceTakesAKnownZero(t *testing.T) {
	free := Pricing{Known: true, Source: SourceDiscovered}
	ll := Pricing{InputMicrosPerMTok: 700, Known: true, Source: SourceLiteLLM}
	if got := resolvePrice(free, ll); got.Source != SourceDiscovered {
		t.Errorf("got %+v, want the discovered free price", got)
	}
}

func TestResolvePriceWithNoCandidatesIsInferred(t *testing.T) {
	got := resolvePrice()
	if got.Known || got.Source != SourceInferred {
		t.Errorf("got %+v, want an unknown inferred price", got)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `timeout 120 go test ./internal/catalog/ -run TestResolvePrice -v`
Expected: FAIL — `undefined: resolvePrice`.

- [ ] **Step 3: Write the resolver**

In `internal/catalog/merge.go`, beside `surfaces`:

```go
// resolvePrice returns the first candidate whose price is known. Callers pass
// candidates in precedence order; a source with nothing to say passes nothing
// rather than a zeroed Pricing, because a known price of zero is a real price
// and must not be indistinguishable from an absent one.
func resolvePrice(candidates ...Pricing) Pricing {
	for _, c := range candidates {
		if c.Known {
			return c
		}
	}
	return Pricing{Source: SourceInferred}
}
```

- [ ] **Step 4: Run them to verify they pass**

Run: `timeout 120 go test ./internal/catalog/ -run TestResolvePrice -v`
Expected: PASS, all four.

- [ ] **Step 5: Write the failing ordering test**

```go
// The row holds one slot whose stamp may outrank a directory or not, so the
// stored candidate moves rather than sitting at a fixed position.
func TestAStoredModelsDevPriceBeatsLiteLLM(t *testing.T) {
	row := store.ModelRow{
		ProviderID: "p", ModelID: "m",
		InputMicrosPerMTok: 500, PriceKnown: true,
		PriceSource: string(SourceModelsDev),
	}
	got := mergeOne(row, Preset{}, Doc{}, store.ModelOverride{})
	if got.Pricing.Source != SourceModelsDev || got.Pricing.InputMicrosPerMTok != 500 {
		t.Errorf("got %+v, want the stored models.dev price", got.Pricing)
	}
}

func TestAStoredInferredPriceLosesToLiteLLM(t *testing.T) {
	row := store.ModelRow{
		ProviderID: "p", ModelID: "m",
		InputMicrosPerMTok: 1, PriceKnown: true,
		PriceSource: string(SourceInferred),
	}
	got := mergeOne(row, Preset{}, Doc{}, store.ModelOverride{})
	// With no LiteLLM candidate wired yet this still returns the stored price;
	// the assertion that matters is the ordering predicate, exercised directly.
	if got.Pricing.Source != SourceInferred {
		t.Errorf("got %+v, want the stored inferred price", got.Pricing)
	}
}
```

- [ ] **Step 6: Rewrite mergeOne's pricing half**

Replace the `if joined { … } else { … }` pricing assignments with candidate construction. Build each candidate only when its source has a price, then order by whether the stored stamp is authoritative:

```go
stored := Pricing{
	InputMicrosPerMTok:      row.InputMicrosPerMTok,
	OutputMicrosPerMTok:     row.OutputMicrosPerMTok,
	CacheReadMicrosPerMTok:  row.CacheReadMicrosPerMTok,
	CacheWriteMicrosPerMTok: row.CacheWriteMicrosPerMTok,
	Known:                   row.PriceKnown,
	Source:                  priceSource(row.PriceSource),
}
var fromDoc Pricing
if joined && meta.PriceKnown {
	fromDoc = Pricing{
		InputMicrosPerMTok:      meta.InputMicrosPerMTok,
		OutputMicrosPerMTok:     meta.OutputMicrosPerMTok,
		CacheReadMicrosPerMTok:  meta.CacheReadMicrosPerMTok,
		CacheWriteMicrosPerMTok: meta.CacheWriteMicrosPerMTok,
		Known:                   true,
		Source:                  SourceModelsDev,
	}
}
if stored.Source.Authoritative() {
	m.Pricing = resolvePrice(stored, fromDoc)
} else {
	m.Pricing = resolvePrice(fromDoc, stored)
}
```

Keep the rest of `mergeOne` — capabilities, context window, surfaces, traits, state — exactly as it is.

- [ ] **Step 7: Run the catalog suite**

Run: `timeout 300 go test ./internal/catalog/`
Expected: PASS. The existing pricing tests assert models.dev winning on a join and the stored row otherwise, which this preserves.

- [ ] **Step 8: Prove the ordering discriminates**

Temporarily invert the `Authoritative()` branch so the stored candidate always trails. Confirm `TestAStoredModelsDevPriceBeatsLiteLLM` goes RED, restore, confirm GREEN. Quote both runs.

- [ ] **Step 9: Commit**

```bash
git add internal/catalog/merge.go internal/catalog/merge_test.go
git commit -m "refactor(catalog): resolve a price by precedence"
```

---

### Task 5: Add the LiteLLM join key to Preset

**Files:**
- Modify: `internal/catalog/preset.go` (the `Preset` struct)
- Test: `internal/catalog/preset_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `Preset.LiteLLMID string` (`yaml:"litellm_id,omitempty"`) and `Preset.NoLiteLLM bool` (`yaml:"no_litellm,omitempty"`).

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4
**Approach:** inline - skip 2: mirrors `ModelsDevID`/`NoModelsDev` and the assertion at preset_test.go:40

- [ ] **Step 1: Write the failing test**

Append to `internal/catalog/preset_test.go`, mirroring the `ModelsDevID` assertion at line 40:

```go
// A preset must declare a LiteLLM join key or an explicit exemption. Without
// the exemption a forgotten key is indistinguishable from a provider the index
// genuinely does not cover, which is the failure the models.dev key already
// guards against.
func TestEveryPresetDeclaresALiteLLMKeyOrExemption(t *testing.T) {
	for id, p := range Embedded() {
		if p.LiteLLMID == "" && !p.NoLiteLLM {
			t.Errorf("%s: needs litellm_id or no_litellm", id)
		}
	}
}
```

- [ ] **Step 2: Run it to verify it fails**

Run: `timeout 120 go test ./internal/catalog/ -run TestEveryPresetDeclaresALiteLLMKeyOrExemption -v`
Expected: FAIL — `p.LiteLLMID undefined`, then once the field exists, 208 failures for presets declaring neither.

- [ ] **Step 3: Add the fields**

In `internal/catalog/preset.go`, beside `ModelsDevID` and `NoModelsDev`:

```go
	// LiteLLMID is the join key into the LiteLLM price index, whose records
	// carry a litellm_provider field. It is a separate key from ModelsDevID
	// because the two indexes name the same vendor differently: fireworks is
	// fireworks_ai there, together is together_ai.
	LiteLLMID string `yaml:"litellm_id,omitempty"`
	// NoLiteLLM is the explicit exemption. A provider the index does not cover
	// must say so, or a forgotten key reads as a deliberate absence.
	NoLiteLLM bool `yaml:"no_litellm,omitempty"`
```

- [ ] **Step 4: Populate the keys via the overrides file**

Add `litellm_id` for the presets whose id matches a `litellm_provider` after normalising dashes and underscores, and for the ten aliases measured in the spec:

```yaml
fireworks:      { litellm_id: fireworks_ai }
together:       { litellm_id: together_ai }
vertex:         { litellm_id: vertex_ai-language-models }
cloudflare-ai:  { litellm_id: cloudflare }
ollama-cloud:   { litellm_id: ollama }
anthropic-oauth: { litellm_id: anthropic }
aimlapi:        { litellm_id: aiml }
volcengine-ark: { litellm_id: volcengine }
nvidia:         { litellm_id: nvidia_nim }
featherless:    { litellm_id: featherless_ai }
```

Merge these into the existing entries in `internal/catalog/presets.overrides.yaml` rather than duplicating ids. Every other preset gets `no_litellm: true` — set it in the generator, not by hand, so a regeneration does not wipe it. **Confirm the direct-match list against the live index before writing it**; do not copy the spec's list on faith.

- [ ] **Step 5: Run the preset suite**

Run: `timeout 300 go test ./internal/catalog/ -run TestEveryPreset -v`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/catalog/preset.go internal/catalog/preset_test.go internal/catalog/presets.overrides.yaml internal/catalog/presets.yaml
git commit -m "feat(catalog): add the litellm join key"
```

---

### Task 6: Parse the LiteLLM index

**Files:**
- Create: `internal/catalog/litellm.go`, `internal/catalog/litellm_test.go`, `internal/catalog/testdata/litellm-sample.json`
- Test: as above

**Interfaces:**
- Consumes: nothing.
- Produces: `type LiteLLMDoc map[string]map[string]Pricing` — `litellm_provider` then model id; `func ParseLiteLLM(raw []byte) (LiteLLMDoc, error)`.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4
**Approach:** inline - skip 3: the spec fixed the shape and the join key in §3 and §4.3

- [ ] **Step 1: Build the golden sample from real data**

Download the live index and extract a handful of entries **verbatim**, preserving their exact field names and values:

```bash
curl -sS --fail --max-time 60 -o /tmp/litellm-full.json \
  https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json
python3 - <<'PY'
import json
d=json.load(open('/tmp/litellm-full.json')); d.pop('sample_spec',None)
want=['gpt-4o','claude-3-5-sonnet-20240620','groq/llama-3.3-70b-versatile',
      'fireworks_ai/accounts/fireworks/models/llama-v3p1-8b-instruct']
out={k:d[k] for k in want if k in d}
# add one free model and one with no input cost, whatever the index actually has
for k,v in d.items():
    if isinstance(v,dict) and v.get('input_cost_per_token')==0 and len(out)<6:
        out[k]=v; break
json.dump(out, open('internal/catalog/testdata/litellm-sample.json','w'), indent=2)
print('sampled:', list(out))
PY
```

Do not hand-edit the result. If a chosen key is absent from the live index, pick a real neighbour rather than inventing one.

- [ ] **Step 2: Write the failing test**

Create `internal/catalog/litellm_test.go`:

```go
package catalog

import (
	"os"
	"testing"
)

// Decoded through the production parser from verbatim upstream records, so a
// schema change upstream fails here rather than silently producing zero prices.
func TestParseLiteLLMReadsTheRealSchema(t *testing.T) {
	raw, err := os.ReadFile("testdata/litellm-sample.json")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := ParseLiteLLM(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc) == 0 {
		t.Fatal("no providers parsed")
	}
	var priced int
	for _, models := range doc {
		for _, p := range models {
			if p.Known && p.Source != SourceLiteLLM {
				t.Errorf("price stamped %q, want litellm", p.Source)
			}
			if p.Known {
				priced++
			}
		}
	}
	if priced == 0 {
		t.Error("every sampled entry parsed as unpriced")
	}
}

// An entry with no cost fields is unpriced, not zero-priced: contributing a
// zeroed candidate would let a silent record outrank a real price.
func TestParseLiteLLMLeavesACostlessEntryUnknown(t *testing.T) {
	doc, err := ParseLiteLLM([]byte(`{"x":{"litellm_provider":"acme"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if doc["acme"]["x"].Known {
		t.Error("an entry with no cost fields parsed as priced")
	}
}
```

- [ ] **Step 3: Run them to verify they fail**

Run: `timeout 120 go test ./internal/catalog/ -run TestParseLiteLLM -v`
Expected: FAIL — `undefined: ParseLiteLLM`.

- [ ] **Step 4: Write the parser**

Create `internal/catalog/litellm.go`. LiteLLM states costs per token as floats; darkrouter stores micro-dollars per million tokens, so the conversion is `cost_per_token * 1e6 * 1e6` rounded. Read the sample to confirm the exact field names before writing — they are `input_cost_per_token`, `output_cost_per_token` and `litellm_provider` in the current index, but verify rather than assume.

```go
package catalog

import "encoding/json"

// LiteLLMDoc is the price index keyed by litellm_provider then model id. The
// key in the upstream file is a display string that varies in shape, so the
// provider field is the join, not the key.
type LiteLLMDoc map[string]map[string]Pricing

type litellmEntry struct {
	Provider       string   `json:"litellm_provider"`
	InputPerToken  *float64 `json:"input_cost_per_token"`
	OutputPerToken *float64 `json:"output_cost_per_token"`
}

// perMTok converts a per-token dollar rate to micro-dollars per million
// tokens, which is how every other price in the catalog is stored.
func perMTok(rate float64) int64 { return int64(rate*1e12 + 0.5) }

func ParseLiteLLM(raw []byte) (LiteLLMDoc, error) {
	var entries map[string]litellmEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, err
	}
	out := LiteLLMDoc{}
	for key, e := range entries {
		if e.Provider == "" {
			continue
		}
		if out[e.Provider] == nil {
			out[e.Provider] = map[string]Pricing{}
		}
		p := Pricing{Source: SourceLiteLLM}
		if e.InputPerToken != nil || e.OutputPerToken != nil {
			p.Known = true
			if e.InputPerToken != nil {
				p.InputMicrosPerMTok = perMTok(*e.InputPerToken)
			}
			if e.OutputPerToken != nil {
				p.OutputMicrosPerMTok = perMTok(*e.OutputPerToken)
			}
		}
		out[e.Provider][modelIDFromKey(key)] = p
	}
	return out, nil
}

// modelIDFromKey strips the provider prefix the upstream key sometimes carries,
// so "groq/llama-3.3-70b" and "llama-3.3-70b" join the same catalog model.
func modelIDFromKey(key string) string {
	if i := strings.LastIndex(key, "/"); i >= 0 {
		return key[i+1:]
	}
	return key
}
```

Add `"strings"` to the imports.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `timeout 120 go test ./internal/catalog/ -run TestParseLiteLLM -v`
Expected: PASS, both.

- [ ] **Step 6: Prove the schema test discriminates**

Temporarily rename the `litellm_provider` tag to `provider`. Confirm `TestParseLiteLLMReadsTheRealSchema` goes RED (no providers parsed), restore, confirm GREEN. Quote both runs.

- [ ] **Step 7: Commit**

```bash
git add internal/catalog/litellm.go internal/catalog/litellm_test.go internal/catalog/testdata/litellm-sample.json
git commit -m "feat(catalog): parse the litellm price index"
```

---

### Task 7: Sync the LiteLLM index at runtime

**Files:**
- Create: `internal/catalog/litellmsync.go`, `internal/catalog/litellmsync_test.go`
- Modify: `internal/config/config.go`, `internal/config/load.go` (URL and interval defaults)

**Interfaces:**
- Consumes: `ParseLiteLLM` and `LiteLLMDoc` from Task 6.
- Produces: `type LiteLLMSyncer struct`, `NewLiteLLMSyncer(LiteLLMSyncOptions) *LiteLLMSyncer`, `(*LiteLLMSyncer).Doc() LiteLLMDoc`, `(*LiteLLMSyncer).Run(ctx) error`, `(*LiteLLMSyncer).SyncOnce(ctx) error`.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4
**Approach:** inline - skip 2: mirrors `catalog.Syncer` and `catalog.FreeSyncer`, both already in this package

- [ ] **Step 1: Read the two existing syncers**

Read `internal/catalog/sync.go` and `internal/catalog/freesync.go` in full before writing. `FreeSyncer` is the closer model: it fetches one document, parses it, and serves the newest successfully-parsed copy from an `atomic.Pointer`, so a failed fetch is a no-op rather than a window where the document is empty. Mirror its structure, its options shape, and its degradation behaviour.

- [ ] **Step 2: Write the failing test**

Create `internal/catalog/litellmsync_test.go` with a test that serves a small index from an `httptest.Server`, runs `SyncOnce`, and asserts `Doc()` returns the parsed content; plus one asserting a failed fetch leaves the previous document in place. Follow `internal/catalog/freesync_test.go`'s setup exactly — read it first and reuse its idioms.

- [ ] **Step 3: Run it to verify it fails**

Run: `timeout 120 go test ./internal/catalog/ -run TestLiteLLMSync -v`
Expected: FAIL — `undefined: NewLiteLLMSyncer`.

- [ ] **Step 4: Write the syncer**

Mirror `FreeSyncer`. Defaults: URL `https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json`, interval 24h, timeout from the existing sync timeout. Cap the fetch as `maxFreeCatalogBytes` does — the index is about 2 MB, so use the same 16 MB ceiling and say why in a comment.

Unlike `Syncer`, this one holds no `*store.DB`: LiteLLM prices are joined in memory at merge time and never written to a row, which is what keeps them re-resolvable.

- [ ] **Step 5: Add the config keys**

`catalog.litellm_url` and `catalog.litellm_interval`, plus a `LiteLLMSync *bool` for turning it off, mirroring `FreeCatalogURL`/`FreeCatalogInterval`/`FreeCatalogSync` in `internal/config/config.go` and their defaults in `load.go`. Add them to the restart-only list if the existing free-catalogue keys are on it.

- [ ] **Step 6: Run the tests and the config suite**

Run: `timeout 600 go test ./internal/catalog/ ./internal/config/`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/catalog/litellmsync.go internal/catalog/litellmsync_test.go internal/config/
git commit -m "feat(catalog): sync the litellm index"
```

---

### Task 8: Join LiteLLM into the merge

**Files:**
- Modify: `internal/catalog/merge.go` (`MergeInput`, `Merge`, `mergeOne`), `internal/server/server.go` (wire the syncer)
- Test: `internal/catalog/merge_test.go`

**Interfaces:**
- Consumes: `LiteLLMDoc` (Task 6), `LiteLLMSyncer.Doc` (Task 7), `resolvePrice` and `Authoritative` (Tasks 1, 4), `Preset.LiteLLMID` (Task 5).
- Produces: `MergeInput.LiteLLM LiteLLMDoc`; `mergeOne` gains a `litellm LiteLLMDoc` parameter.

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5
**Approach:** inline - skip 3: §4.2 fixed the candidate order and §4.3 the join key

- [ ] **Step 1: Write the failing test**

```go
// A model models.dev has never heard of takes the LiteLLM price rather than
// reading as unpriced, which is the whole point of the phase.
func TestLiteLLMPricesAModelModelsDevMisses(t *testing.T) {
	row := store.ModelRow{ProviderID: "p", ModelID: "llama-3.3-70b"}
	ll := LiteLLMDoc{"groq": {"llama-3.3-70b": {
		InputMicrosPerMTok: 590, OutputMicrosPerMTok: 790,
		Known: true, Source: SourceLiteLLM,
	}}}
	got := mergeOne(row, Preset{LiteLLMID: "groq"}, Doc{}, ll, store.ModelOverride{})
	if got.Pricing.Source != SourceLiteLLM || got.Pricing.InputMicrosPerMTok != 590 {
		t.Errorf("got %+v, want the litellm price", got.Pricing)
	}
	if got.Pricing.Source.Grade() != GradeIndexed {
		t.Errorf("grade = %q, want indexed", got.Pricing.Source.Grade())
	}
}

// models.dev outranks the index where both know the model.
func TestModelsDevBeatsLiteLLM(t *testing.T) {
	row := store.ModelRow{ProviderID: "p", ModelID: "big"}
	doc := Doc{"p": {"big": Metadata{InputMicrosPerMTok: 500, PriceKnown: true}}}
	ll := LiteLLMDoc{"groq": {"big": {InputMicrosPerMTok: 700, Known: true, Source: SourceLiteLLM}}}
	got := mergeOne(row, Preset{ModelsDevID: "p", LiteLLMID: "groq"}, doc, ll, store.ModelOverride{})
	if got.Pricing.Source != SourceModelsDev {
		t.Errorf("got %+v, want models.dev", got.Pricing)
	}
}

// A preset with no LiteLLM key joins nothing rather than matching by accident.
func TestNoLiteLLMKeyJoinsNothing(t *testing.T) {
	row := store.ModelRow{ProviderID: "p", ModelID: "m"}
	ll := LiteLLMDoc{"groq": {"m": {InputMicrosPerMTok: 700, Known: true, Source: SourceLiteLLM}}}
	got := mergeOne(row, Preset{NoLiteLLM: true}, Doc{}, ll, store.ModelOverride{})
	if got.Pricing.Known {
		t.Errorf("got %+v, want no price", got.Pricing)
	}
}
```

- [ ] **Step 2: Run them to verify they fail**

Run: `timeout 120 go test ./internal/catalog/ -run 'TestLiteLLM|TestModelsDevBeatsLiteLLM|TestNoLiteLLMKey' -v`
Expected: FAIL — `too many arguments in call to mergeOne`.

- [ ] **Step 3: Thread the document through**

Add `LiteLLM LiteLLMDoc` to `MergeInput`; pass `in.LiteLLM` at the `mergeOne` call site in `Merge`; add the parameter to `mergeOne`'s signature after `doc`. Build the candidate inside `mergeOne`:

```go
var fromLiteLLM Pricing
if preset.LiteLLMID != "" {
	if p, ok := litellm[preset.LiteLLMID][row.ModelID]; ok && p.Known {
		fromLiteLLM = p
	}
}
```

and place it after `fromDoc` in both orderings:

```go
if stored.Source.Authoritative() {
	m.Pricing = resolvePrice(stored, fromDoc, fromLiteLLM)
} else {
	m.Pricing = resolvePrice(fromDoc, fromLiteLLM, stored)
}
```

Update every existing `mergeOne` call in the tests to pass a `LiteLLMDoc{}`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `timeout 300 go test ./internal/catalog/`
Expected: PASS.

- [ ] **Step 5: Wire the syncer in the server**

In `internal/server/server.go`, construct the `LiteLLMSyncer` beside the existing `freeSync`, run its worker when enabled the same way, and supply `syncer.Doc` equivalents into whatever builds `MergeInput`. Follow how `FreeTiers` is threaded into `DiscoveryOptions` — the same shape, a function returning the newest document.

- [ ] **Step 6: Run the full suite**

Run: `timeout 900 go test ./...`
Expected: PASS.

- [ ] **Step 7: Measure the coverage gain and report it**

Rebuild and inspect how many rows became priced. Report the before and after counts — this is the phase's success metric, and a number below roughly 200 newly-priced rows means the join is not matching and should be investigated before the task is called done.

- [ ] **Step 8: Commit**

```bash
git add internal/catalog/merge.go internal/catalog/merge_test.go internal/server/server.go
git commit -m "feat(catalog): price models from the litellm index"
```

---

### Task 9: Harvest prices from the discovery sweep

**Files:**
- Modify: `internal/catalog/probe.go` (the OpenAI-compatible listing parse), `internal/store/catalog_lifecycle.go` (`DiscoveredModel`, `RecordDiscoverySuccess`)
- Test: `internal/catalog/probe_test.go`, `internal/store/catalog_lifecycle_test.go`

**Interfaces:**
- Consumes: `store.MetadataRow.PriceKnown` (Task 3).
- Produces: `store.DiscoveredModel.Pricing *ModelPricing` — nil when the listing carried no price.

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5
**Approach:** inline - skip 3: §4.2's "no candidate rather than a zeroed one" rule fixes the shape

- [ ] **Step 1: Confirm the upstream shape against a live endpoint**

Two of five sampled providers publish prices, and their field names differ. Fetch both and read them before writing a struct:

```bash
curl -sS --max-time 20 https://llm.chutes.ai/v1/models | python3 -m json.tool | head -40
```

Record what you find in your report. Build the parse from what the endpoint actually returns, not from this plan's guess. If the shapes are irreconcilable, parse only the one you can verify and say so.

- [ ] **Step 2: Write the failing test**

Add a probe test feeding a listing body captured verbatim from that fetch, asserting the parsed model carries a price; and one feeding a listing with no price fields, asserting `Pricing` is **nil** rather than a zeroed struct.

- [ ] **Step 3: Run them to verify they fail**

Run: `timeout 120 go test ./internal/catalog/ -run TestProbe -v`
Expected: FAIL — the field does not exist.

- [ ] **Step 4: Parse the price and carry it**

Extend the OpenAI-compatible listing struct in `probe.go` with the price fields you verified, add `Pricing *ModelPricing` to `store.DiscoveredModel`, and set it only when the listing carried a price. A listing without one leaves it nil.

- [ ] **Step 5: Write the discovered price with its stamp**

`RecordDiscoverySuccess` names no price columns today. Extend it to write the four price columns, `price_known` and `price_source = 'discovered'` **only for models whose `Pricing` is non-nil**, leaving every other row's price untouched. A probe reporting a bare id must not overwrite models.dev's numbers with zeroes — the same care the existing upsert already documents for capabilities.

- [ ] **Step 6: Run the suites**

Run: `timeout 600 go test ./internal/catalog/ ./internal/store/`
Expected: PASS.

- [ ] **Step 7: Prove the nil case discriminates**

Temporarily make the parser return a zeroed `ModelPricing` instead of nil when no price is present. Confirm the no-price test goes RED, restore, confirm GREEN. Quote both runs.

- [ ] **Step 8: Commit**

```bash
git add internal/catalog/probe.go internal/catalog/probe_test.go internal/store/catalog_lifecycle.go internal/store/catalog_lifecycle_test.go
git commit -m "feat(catalog): harvest prices from discovery"
```

---

### Task 10: Mark a partly estimated spend total

**Files:**
- Modify: the spend aggregation in `internal/admin/` and the tile in `web/src/features/`
- Test: the corresponding `_test.go` and `.test.ts(x)`

**Interfaces:**
- Consumes: `catalog.Pricing.Source` and `Grade()`.
- Produces: an `estimated bool` on the spend response, true when any contributing price was `indexed` or `guessed`.

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5
**Approach:** inline - skip 3: §5 fixed the semantics — count everything, mark the total

- [ ] **Step 1: Find the spend aggregation**

Read how the spend figure is produced today — start from `internal/store/rollup*.go` and the admin handler that serves it. Requests store `CostMicros`, which is already nil for an unpriced model, so the aggregate currently sums only priced rows. Establish where a grade could be carried alongside before writing anything, and record what you find.

- [ ] **Step 2: Write the failing test**

Assert that a spend total whose contributing prices include an `indexed` one reports `estimated: true`, and one whose prices are all `measured` or `declared` reports `estimated: false`. Follow the existing rollup tests' fixture style.

- [ ] **Step 3: Run it to verify it fails**

Expected: FAIL — the field does not exist.

- [ ] **Step 4: Carry the grade into the aggregate**

Implement the smallest change that makes the assertion pass. Prefer recording the grade at log time beside `CostMicros` over re-deriving it at query time from a catalogue that may have changed since the request.

- [ ] **Step 5: Render the marker**

Show the spend tile's total with an "estimated" qualifier when the flag is set. Per the typography rule, the qualifier uses `text-sm` or larger and takes its de-emphasis from colour — `--muted-foreground` — never from a smaller size.

- [ ] **Step 6: Run both suites and lint**

Run: `timeout 900 go test ./...` then `cd web && timeout 600 npm test && timeout 300 npm run lint`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/ web/
git commit -m "feat(console): mark a partly estimated spend"
```

---

### Task 11: Deploy and verify

**Files:** none — verification only.

**Interfaces:**
- Consumes: everything above.
- Produces: nothing.

**Implementer:** dcc-superpower-companions:impl-sonnet-medium
**Evaluation:** files 0 - spec 0 - coupling 1 - risk 1 = 2
**Approach:** inline - skip 3: the deploy procedure is fixed by docs/DEPLOY.md

- [ ] **Step 1: Run the full suite**

Run: `timeout 900 go test ./...` and `cd web && timeout 600 npm test && timeout 300 npm run lint`
Expected: PASS. Quote the tails.

- [ ] **Step 2: Rebuild and redeploy**

Follow `docs/DEPLOY.md`, "Local build (UAT)". The `compose.uat.yml` overlay is **required** or the published image is pulled over the local build, and this machine publishes the admin port on **8091** (8081 is a different container).

- [ ] **Step 3: Verify the coverage gain**

The console password is in `.uat-credentials` at the repository root — gitignored, and it stays that way; never copy it into a tracked file, a commit message, or your report. If login returns 403, wait rather than retrying repeatedly; these credentials have drifted before.

Report, from `GET /api/models`:
- how many rows carry a price now, against the 18 of 459 before this phase;
- the distribution of `price_source` and `price_grade`.

The phase's goal is coverage. A large `litellm`/`indexed` population is success; a total near 18 means the join is not matching and the phase has not delivered, whatever the tests say.

- [ ] **Step 4: Report**

State the before and after counts plainly. If coverage did not improve, say so rather than reporting the deploy as a success.

---

## Verification

After Task 11:

1. `internal/catalog/presets.yaml` — every preset declares `litellm_id` or `no_litellm`.
2. A row stamped `discovered` keeps its numbers across a models.dev sync.
3. A zero-priced model reads back as priced.
4. Priced rows are substantially more numerous than the 18 this phase started from.
