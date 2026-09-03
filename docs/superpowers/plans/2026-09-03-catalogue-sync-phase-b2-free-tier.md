# Catalogue Sync Phase B2 — The Free-Tier Record Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

> **Implementer assignments:** each task names its implementer agent in an
> `**Implementer:**` line. When executing with
> superpowers:subagent-driven-development, REQUIRED SUB-SKILL:
> dcc-superpower-companions:dispatching-tiered-implementers. Under
> superpowers:executing-plans these lines are inert; ignore them.

**Goal:** Carry the whole upstream free-tier record — budget, pool and terms verdict — into the catalogue, and stop routing production traffic through tiers the vendor has not sanctioned unless the operator opts in.

**Architecture:** `FreeCatalog` widens from `map[preset]map[model]string` to a record struct, parsed by the same regex-over-TypeScript reader that already exists and regenerated into the embedded snapshot in one commit. A new `providers.allow_unsanctioned_free` column mirrors `free_models_only` exactly — column, store patch, admin API, wizard control. The `avoid` policy then reads that column at two gates: the discovery import filter and the router's `filterTarget`.

**Tech Stack:** Go 1.26 gateway (`internal/`), React + TypeScript console (`web/`), SQLite (`internal/store/migrations`), Vitest, `go test`.

**Spec:** `docs/superpowers/specs/2026-09-03-catalogue-sync-phase-b-design.md` §6

## Global Constraints

- **Never a hardcoded font size under `web/`.** Only darkraise-ui's scale: `text-sm`, `text-base`, `text-lg`, `text-xl`, `text-2xl`, `text-3xl`. Never `text-xs`, never `text-[11px]`, never `font-size: 13px`. Hierarchy below body text comes from colour (`--legend`, `--muted-foreground`) and weight. In a stylesheet use `font-size: var(--text-sm)`.
- **Fixtures come from upstream verbatim,** checked into `testdata/` and decoded through the production parser. Never a hand-built struct literal for third-party data.
- **Every guard must be proven able to fail.** Break the implementation, watch the test go red, restore. A test that passes against the bug it names is not coverage.
- **A source with nothing to say contributes nothing.** No zero-valued record may overwrite a real one.
- **English only** in code, comments, docs, commits, configs, errors and tests.
- **Commits** `<type>(<scope>): <subject>`, type = feat|fix|docs|style|refactor|test|chore|perf. Subject ≤50 chars, imperative, no trailing period.
- **Terms of art:** upstream calls the terms verdict `tos`; darkrouter calls a row whose verdict is `avoid` **unsanctioned**. Use that word in identifiers and operator-facing copy.

---

## File Structure

| File | Responsibility |
|---|---|
| `internal/catalog/freecatalog.go` | `FreeTier` record, widened parser, `Covers`/`ModelsFor`/`Tier` |
| `internal/catalog/free_models.json` | Regenerated embedded snapshot (generated — never hand-edited) |
| `internal/catalog/testdata/free-catalog-sample.ts` | Verbatim upstream excerpt |
| `internal/store/migrations/0021_allow_unsanctioned_free.sql` | The opt-in column |
| `internal/store/providers_store.go` | Column read, write and patch |
| `internal/admin/providers.go` | Opt-in on the provider API |
| `internal/admin/catalog.go` | Free-tier record on the model API |
| `internal/catalog/discovery.go` | Import filter honours the opt-in |
| `internal/router/filter.go`, `types.go` | Routing skips an unsanctioned model |
| `web/src/features/models/models-screen.tsx` | Budget label and unsanctioned warning |
| `web/src/features/providers/provider-detail.tsx` | The opt-in control |

---

### Task 1: Widen the free-tier record and regenerate the snapshot

The parser reads three fields and discards five. This reads all of them. The
embedded `free_models.json` **must** be regenerated in the same commit: its
shape changes, and `FreeModels()` degrades a parse failure to an empty
catalogue rather than panicking, so a missed regeneration silently switches the
free filter off instead of failing.

**Files:**
- Modify: `internal/catalog/freecatalog.go:18-118`
- Modify: `internal/catalog/free_models.json` (regenerate, do not hand-edit)
- Create: `internal/catalog/testdata/free-catalog-sample.ts`
- Test: `internal/catalog/freecatalog_test.go`, `internal/catalog/freesync_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: `catalog.FreeTier{FreeType, DisplayName string; MonthlyTokens, CreditTokens int64; PoolKey, ToS string}`; `FreeCatalog.Providers map[string]map[string]FreeTier`; `(FreeCatalog).Tier(presetID, modelID string) (FreeTier, bool)`; `(FreeTier).Unsanctioned() bool`; `(FreeTier).Live() bool`. `Covers` and `ModelsFor` keep their existing signatures.

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 2 - spec 0 - coupling 1 - risk 2 = 5
**Approach:** inline - skip 3: spec §6 already chose the full record

- [ ] **Step 1: Capture a verbatim upstream excerpt**

Take the real file's header and twelve real entries, chosen to cover every
`freeType` value, a null `poolKey`, and each `tos` verdict:

```bash
F=/tmp/omniroute/open-sse/config/freeModelCatalog.data.ts
# clone first if absent:
#   git clone --depth 1 https://github.com/diegosouzapw/OmniRoute /tmp/omniroute
{
  grep -m1 'FREE_CATALOG_CURATED_AT' "$F"
  echo 'export const FREE_MODEL_BUDGETS = ['
  grep 'freeType: "recurring-daily"'     "$F" | head -2
  grep 'freeType: "one-time-initial"'    "$F" | head -2
  grep 'freeType: "keyless"'             "$F" | head -2
  grep 'freeType: "recurring-uncapped"'  "$F" | head -2
  grep 'freeType: "recurring-monthly"'   "$F" | head -1
  grep 'freeType: "recurring-credit"'    "$F" | head -1
  grep 'freeType: "discontinued"'        "$F" | head -1
  grep 'poolKey: null'                   "$F" | head -1
  grep 'tos: "avoid"'                    "$F" | head -1
  grep 'tos: "ok"'                       "$F" | head -1
  echo ']'
} > internal/catalog/testdata/free-catalog-sample.ts
wc -l internal/catalog/testdata/free-catalog-sample.ts
```

- [ ] **Step 2: Write the failing test**

In `internal/catalog/freecatalog_test.go`:

```go
//go:embed testdata/free-catalog-sample.ts
var freeCatalogSampleTS []byte

// The record is read from a verbatim upstream excerpt through the production
// parser, so every field this phase adds is exercised against the real shape
// rather than one assumed for it.
func TestParseFreeCatalogReadsTheWholeRecord(t *testing.T) {
	c, err := ParseFreeCatalog(freeCatalogSampleTS)
	if err != nil {
		t.Fatal(err)
	}
	var withBudget, unsanctioned, pooled int
	for _, models := range c.Providers {
		for _, tier := range models {
			if tier.MonthlyTokens > 0 || tier.CreditTokens > 0 {
				withBudget++
			}
			if tier.Unsanctioned() {
				unsanctioned++
			}
			if tier.PoolKey != "" {
				pooled++
			}
		}
	}
	if withBudget == 0 {
		t.Error("no entry carried a budget; monthlyTokens/creditTokens are being dropped")
	}
	if unsanctioned == 0 {
		t.Error("no entry graded avoid; tos is being dropped")
	}
	if pooled == 0 {
		t.Error("no entry carried a pool; poolKey is being dropped")
	}
}

// A null poolKey is a real value upstream — seven rows carry it — and must
// read as absent rather than as the literal string "null".
func TestParseFreeCatalogReadsANullPool(t *testing.T) {
	c, err := ParseFreeCatalog(freeCatalogSampleTS)
	if err != nil {
		t.Fatal(err)
	}
	for _, models := range c.Providers {
		for id, tier := range models {
			if tier.PoolKey == "null" {
				t.Fatalf("%s: poolKey read as the literal string null", id)
			}
		}
	}
}

// The embedded snapshot is generated. If a regeneration is skipped after the
// record widens, FreeModels() degrades to an empty catalogue and the free
// filter silently stops working — this fails loudly instead.
func TestEmbeddedFreeCatalogCarriesTheFullRecord(t *testing.T) {
	c := FreeModels()
	if len(c.Providers) == 0 {
		t.Fatal("embedded free catalogue is empty")
	}
	for _, models := range c.Providers {
		for _, tier := range models {
			if tier.ToS != "" {
				return
			}
		}
	}
	t.Error("no embedded entry carries a tos; free_models.json needs regenerating")
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/catalog/ -run TestParseFreeCatalogReadsTheWholeRecord -v`
Expected: FAIL — `tier.MonthlyTokens undefined` (compile error; the record does not exist yet).

- [ ] **Step 4: Widen the type and the parser**

In `internal/catalog/freecatalog.go`, replace the `Providers` field and add the record:

```go
// FreeTier is one row of the upstream free-model catalogue.
//
// Every field upstream publishes is kept. The three darkrouter decided on
// before — provider, model, freeType — answered "is this model free"; the rest
// answer "free how much, on whose terms, out of which shared bucket", which is
// what an operator actually needs before routing production traffic through it.
type FreeTier struct {
	// FreeType is the shape of the allowance: recurring-daily,
	// recurring-monthly, recurring-uncapped, recurring-credit, one-time-initial,
	// keyless, or discontinued.
	FreeType string `json:"free_type"`
	// DisplayName is upstream's label for the model, kept for the console.
	DisplayName string `json:"display_name,omitempty"`
	// MonthlyTokens is the recurring allowance; CreditTokens a one-time grant.
	// Zero in both means uncapped or unquantified, never "no allowance".
	MonthlyTokens int64 `json:"monthly_tokens,omitempty"`
	CreditTokens  int64 `json:"credit_tokens,omitempty"`
	// PoolKey names a quota shared across models. Empty for the seven rows
	// upstream publishes as null.
	PoolKey string `json:"pool_key,omitempty"`
	// ToS is upstream's verdict on how the vendor regards this access: ok,
	// caution, ambiguous, avoid, unknown.
	ToS string `json:"tos"`
}

// tosAvoid is the verdict that keeps a tier out of automatic use. It largely
// means access the vendor has not sanctioned; a gateway that silently routes
// production traffic through it exposes its operator to a risk they never
// agreed to.
const tosAvoid = "avoid"

// Unsanctioned reports whether the vendor has not sanctioned this access.
func (t FreeTier) Unsanctioned() bool { return t.ToS == tosAvoid }

// Live reports whether the tier still exists. A withdrawn tier stays in the
// catalogue for its history and is not one an import filter may count on.
func (t FreeTier) Live() bool { return t.FreeType != freeTypeDiscontinued }
```

Change the struct field and the three readers:

```go
	// Providers maps preset id -> model id -> the tier's whole record.
	Providers map[string]map[string]FreeTier `json:"providers"`
```

```go
// Covers reports whether the provider's free tier documents this model.
func (c FreeCatalog) Covers(presetID, modelID string) bool {
	tier, ok := c.Providers[presetID][modelID]
	return ok && tier.Live()
}

// Tier returns the whole record for one model, if the catalogue has it.
func (c FreeCatalog) Tier(presetID, modelID string) (FreeTier, bool) {
	tier, ok := c.Providers[presetID][modelID]
	return tier, ok
}

// ModelsFor lists the models documented free for one preset, discontinued
// tiers excluded. Nil for a provider the catalog has never covered, which is a
// different fact from one whose free tier has been withdrawn.
func (c FreeCatalog) ModelsFor(presetID string) []string {
	models := c.Providers[presetID]
	if len(models) == 0 {
		return nil
	}
	out := make([]string, 0, len(models))
	for id, tier := range models {
		if tier.Live() {
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out
}
```

Add `"sort"` and `"strconv"` to the imports. `ModelsFor` gains the sort because
its caller (`seed.go:21`) writes generated output from it, and map order there
is the same nondeterminism the models.dev join was fixed for.

Replace the entry regex and the loop. The field order is uniform across all 437
upstream rows — verified — so one pattern reads them all:

```go
// freeEntry matches one line of OmniRoute's FREE_MODEL_BUDGETS. Every field
// upstream publishes is captured; poolKey alternates because seven rows carry
// a literal null rather than a string.
var freeEntry = regexp.MustCompile(
	`\{ provider: "([^"]+)", modelId: "([^"]+)", displayName: "([^"]*)", ` +
		`monthlyTokens: (\d+), creditTokens: (\d+), freeType: "([^"]+)", ` +
		`poolKey: (?:"([^"]+)"|null), tos: "([^"]+)"`)
```

```go
	for _, m := range freeEntry.FindAllSubmatch(raw, -1) {
		provider, model := string(m[1]), string(m[2])
		if out.Providers[provider] == nil {
			out.Providers[provider] = map[string]FreeTier{}
		}
		monthly, _ := strconv.ParseInt(string(m[4]), 10, 64)
		credit, _ := strconv.ParseInt(string(m[5]), 10, 64)
		out.Providers[provider][model] = FreeTier{
			DisplayName:   string(m[3]),
			MonthlyTokens: monthly,
			CreditTokens:  credit,
			FreeType:      string(m[6]),
			PoolKey:       string(m[7]),
			ToS:           string(m[8]),
		}
	}
```

- [ ] **Step 5: Fix the existing callers**

`internal/catalog/freecatalog_test.go` and `discovery_test.go` build
`FreeCatalog{Providers: map[string]map[string]string{...}}` literals. Change
each to `map[string]map[string]FreeTier{...}` with `{FreeType: "..."}` values.
Do not change what any existing test asserts.

- [ ] **Step 6: Regenerate the embedded snapshot**

```bash
git clone --depth 1 https://github.com/diegosouzapw/OmniRoute /tmp/omniroute
git clone --depth 1 https://github.com/decolua/9router /tmp/ninerouter
curl -sS --fail --max-time 60 -o /tmp/modelsdev.json https://models.dev/api.json
curl -sS --fail --max-time 60 -o /tmp/litellm.json \
  https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json

go run ./tools/presetgen \
  -omniroute /tmp/omniroute -ninerouter /tmp/ninerouter \
  -modelsdev /tmp/modelsdev.json -litellm /tmp/litellm.json \
  -omniroute-sha "$(git -C /tmp/omniroute rev-parse --short HEAD)" \
  -ninerouter-sha "$(git -C /tmp/ninerouter rev-parse --short HEAD)"
```

**Then revert everything except `free_models.json`.** A full regeneration also
carries unrelated upstream drift — as of 2026-09-03 it deletes the `hackclub`
preset, which serves 436 live models, plus `clova-studio` and about thirty
provider logos. That drift is a separate decision and must not ride in on this
change:

```bash
git checkout -- internal/catalog/presets.yaml internal/catalog/provenance.yaml \
  internal/catalog/models_snapshot.json web/public/providers \
  web/src/features/providers/provider-assets.ts
git status --porcelain   # expect only freecatalog.go, free_models.json, tests, testdata
```

- [ ] **Step 7: Run the tests to verify they pass**

Run: `go test ./internal/catalog/... ./tools/presetgen/...`
Expected: PASS.

- [ ] **Step 8: Prove the guards can fail**

```bash
# Drop tos from the regex -> TestParseFreeCatalogReadsTheWholeRecord reddens
# Revert free_models.json to HEAD -> TestEmbeddedFreeCatalogCarriesTheFullRecord reddens
```
Restore after each. Both must go red; record the output in your report.

- [ ] **Step 9: Commit**

```bash
git add internal/catalog/freecatalog.go internal/catalog/free_models.json \
  internal/catalog/testdata/free-catalog-sample.ts \
  internal/catalog/freecatalog_test.go internal/catalog/discovery_test.go
git commit -m "feat(catalog): read the whole free-tier record"
```

---

### Task 2: Store the operator's unsanctioned-tier opt-in

**Files:**
- Create: `internal/store/migrations/0021_allow_unsanctioned_free.sql`
- Modify: `internal/store/providers_store.go:29-48,71-75,115-116`
- Test: `internal/store/providers_store_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: `store.ProviderRow.AllowUnsanctionedFree bool`; `store.ProviderPatch.AllowUnsanctionedFree *bool`; `provider.Provider.AllowUnsanctionedFree bool` (hydrated in `internal/provider/sqlsource.go`). Column `providers.allow_unsanctioned_free INTEGER NOT NULL DEFAULT 0`.
- **Two provider structs.** `store.ProviderRow` is the database row; `provider.Provider` is what the discovery sweep and the router read. Both carry the field. Accessors are `db.ProviderByID`, `db.ProviderRows`, `db.CreateProvider`, `db.UpdateProvider` — there is no `db.Provider` or `db.PatchProvider`.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 2 - spec 0 - coupling 1 - risk 1 = 4
**Approach:** inline - skip 2: mirrors `free_models_only` exactly, column for column

- [ ] **Step 1: Write the migration**

`internal/store/migrations/0021_allow_unsanctioned_free.sql`:

```sql
-- An `avoid` terms verdict means access the vendor has not sanctioned. Those
-- models stay catalogued and visible, but nothing routes to them automatically
-- until the operator says so for that provider. Default 0: the opt-in is the
-- operator's, and a migration must not grant it on their behalf.
ALTER TABLE providers ADD COLUMN allow_unsanctioned_free INTEGER NOT NULL DEFAULT 0;
```

- [ ] **Step 2: Write the failing test**

In `internal/store/providers_store_test.go`:

```go
// The opt-in defaults off and survives a patch round trip. A column that read
// back as its default after being set would silently un-opt the operator.
func TestUnsanctionedOptInDefaultsOffAndPatches(t *testing.T) {
	db := migrated(t)
	p := ProviderRow{ID: "p", Name: "p", Preset: "groq", Kind: "openaicompat", Enabled: true}
	if err := db.CreateProvider(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	got, err := db.ProviderByID(context.Background(), "p")
	if err != nil {
		t.Fatal(err)
	}
	if got.AllowUnsanctionedFree {
		t.Error("the opt-in defaulted on; it is the operator's to grant")
	}
	yes := true
	if err := db.UpdateProvider(context.Background(), "p",
		ProviderPatch{AllowUnsanctionedFree: &yes}); err != nil {
		t.Fatal(err)
	}
	got, err = db.ProviderByID(context.Background(), "p")
	if err != nil {
		t.Fatal(err)
	}
	if !got.AllowUnsanctionedFree {
		t.Error("the patch did not stick")
	}
}
```

- [ ] **Step 3: Run it to verify it fails**

Run: `go test ./internal/store/ -run TestUnsanctionedOptInDefaultsOffAndPatches -v`
Expected: FAIL — `AllowUnsanctionedFree` undefined.

- [ ] **Step 4: Add the field, the column and the patch**

In `internal/store/providers_store.go`, beside `FreeModelsOnly` in each of the
four places it appears — the `Provider` struct, the `ProviderPatch` struct, the
`INSERT` column list and its `boolToInt` argument, and the patch `sets` block:

```go
	// AllowUnsanctionedFree lets this provider's `avoid`-graded free models be
	// imported and routed to. Off by default: `avoid` largely means access the
	// vendor has not sanctioned, and the risk is the operator's to accept.
	AllowUnsanctionedFree bool
```

```go
	AllowUnsanctionedFree *bool `json:"allow_unsanctioned_free"`
```

```go
	if patch.AllowUnsanctionedFree != nil {
		sets = append(sets, "allow_unsanctioned_free = ?")
		args = append(args, boolToInt(*patch.AllowUnsanctionedFree))
	}
```

Add the column to every `SELECT` that builds a `Provider`, and to the `INSERT`
column list and its placeholders. Scan it with the same `intToBool` treatment
`free_models_only` gets.

- [ ] **Step 5: Run it to verify it passes**

Run: `go test ./internal/store/...`
Expected: PASS.

- [ ] **Step 6: Prove the guard can fail**

Drop the `sets` block added in Step 4; the patch half must redden. Restore.

- [ ] **Step 7: Commit**

```bash
git add internal/store/migrations/0021_allow_unsanctioned_free.sql \
  internal/store/providers_store.go internal/store/providers_store_test.go
git commit -m "feat(store): store the unsanctioned-tier opt-in"
```

---

### Task 3: Surface the opt-in on the provider API

**Files:**
- Modify: `internal/admin/providers.go:108-110,139,190-193,216,284`
- Test: `internal/admin/providers_test.go`

**Interfaces:**
- Consumes: `store.ProviderRow.AllowUnsanctionedFree`, `store.ProviderPatch.AllowUnsanctionedFree`.
- Produces: `allow_unsanctioned_free` boolean on the provider GET response, the create body and the patch body.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 2 = 4
**Approach:** inline - skip 2: `free_models_only` is the pattern, four sites over

- [ ] **Step 1: Write the failing test**

```go
// The opt-in has to reach the operator both ways: readable on the provider and
// settable without a second call. A field that serialized but ignored its patch
// would look like it worked.
func TestProviderAPICarriesTheUnsanctionedOptIn(t *testing.T) {
	s, db := testServerFull(t)
	seedProvider(t, db, "p")

	body := doJSON(t, s, "PATCH", "/api/providers/p",
		`{"allow_unsanctioned_free": true}`)
	if code := body.Code; code != 200 {
		t.Fatalf("patch returned %d", code)
	}
	got := doJSON(t, s, "GET", "/api/providers/p", "")
	if !strings.Contains(got.Body.String(), `"allow_unsanctioned_free":true`) {
		t.Errorf("opt-in absent from the provider response: %s", got.Body.String())
	}
}
```

Use whatever request helper `internal/admin/providers_test.go` already uses;
`doJSON` and `seedProvider` above are placeholders for the file's own helpers —
read the file and use its real ones rather than adding new ones.

- [ ] **Step 2: Run it to verify it fails**

Run: `go test ./internal/admin/ -run TestProviderAPICarriesTheUnsanctionedOptIn -v`
Expected: FAIL — the field is absent from the response.

- [ ] **Step 3: Add the field at all four sites**

Beside every `FreeModelsOnly` in `providers.go`:

```go
	// AllowUnsanctionedFree lets this provider's `avoid`-graded free models be
	// imported and routed to. The console explains the risk beside the control.
	AllowUnsanctionedFree bool `json:"allow_unsanctioned_free"`
```

and in the response builder, `AllowUnsanctionedFree: p.AllowUnsanctionedFree`,
and in the create path, and in the "no fields to patch" guard at line 284 add
`patch.AllowUnsanctionedFree == nil &&`.

- [ ] **Step 4: Run it to verify it passes**

Run: `go test ./internal/admin/...`
Expected: PASS.

- [ ] **Step 5: Prove the guard can fail**

Remove the field from the response builder; the test must redden. Restore.

- [ ] **Step 6: Commit**

```bash
git add internal/admin/providers.go internal/admin/providers_test.go
git commit -m "feat(admin): expose the unsanctioned-tier opt-in"
```

---

### Task 4: The import filter skips an unsanctioned tier

**Files:**
- Modify: `internal/catalog/discovery.go:60-70,338-355`
- Test: `internal/catalog/discovery_test.go`

**Interfaces:**
- Consumes: `FreeCatalog.Tier`, `FreeTier.Unsanctioned`, `provider.Provider.AllowUnsanctionedFree`.
- Produces: `FreeRules.Curated` returns false for an unsanctioned tier when the provider has not opted in.

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5
**Approach:** inline - skip 3: spec §6.1 chose the policy and its default

- [ ] **Step 1: Write the failing test**

```go
// avoid largely means access the vendor has not sanctioned. Those models stay
// catalogued — dropping them would hide models an operator may already be
// using — but the free filter does not import them until the operator opts the
// provider in.
func TestTheFreeFilterSkipsAnUnsanctionedTier(t *testing.T) {
	live := FreeCatalog{Providers: map[string]map[string]FreeTier{
		"groq": {
			"sanctioned":   {FreeType: "recurring-daily", ToS: "ok"},
			"unsanctioned": {FreeType: "recurring-daily", ToS: "avoid"},
		},
	}}
	for _, tc := range []struct {
		name    string
		optedIn bool
		want    bool
	}{
		{name: "off by default", optedIn: false, want: false},
		{name: "opted in", optedIn: true, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := &Discoverer{opts: DiscoveryOptions{
				FreeTiers: func() FreeCatalog { return live },
			}}
			rules := d.freeRules(provider.Provider{
				ID: "p", Preset: "groq", AllowUnsanctionedFree: tc.optedIn,
			}, Preset{})
			if got := rules.Curated("unsanctioned"); got != tc.want {
				t.Errorf("unsanctioned curated = %v, want %v", got, tc.want)
			}
			if !rules.Curated("sanctioned") {
				t.Error("a sanctioned tier was skipped; the opt-in is too broad")
			}
		})
	}
}
```

Read `discovery.go` for the real name and signature of the method that builds
`FreeRules` (it is the one containing line 352) and call that, rather than the
`freeRules` name used above.

- [ ] **Step 2: Run it to verify it fails**

Expected: FAIL — the unsanctioned model is curated in both cases.

- [ ] **Step 3: Thread the opt-in into the rule**

Replace the `rules.Curated` assignment:

```go
	if len(free.Providers[key]) > 0 {
		allowUnsanctioned := p.AllowUnsanctionedFree
		rules.Curated = func(modelID string) bool {
			tier, ok := free.Tier(key, modelID)
			if !ok || !tier.Live() {
				return false
			}
			// Catalogued either way; imported only with the operator's consent.
			return allowUnsanctioned || !tier.Unsanctioned()
		}
	}
```

- [ ] **Step 4: Run it to verify it passes**

Run: `go test ./internal/catalog/...`
Expected: PASS.

- [ ] **Step 5: Prove the guard can fail**

Change the return to `return true`; the "off by default" case must redden.
Change it to `return !tier.Unsanctioned()` ignoring the flag; the "opted in"
case must redden. Both matter — one predicate, two directions. Restore.

- [ ] **Step 6: Commit**

```bash
git add internal/catalog/discovery.go internal/catalog/discovery_test.go
git commit -m "feat(catalog): skip unsanctioned tiers on import"
```

---

### Task 5: Routing skips an unsanctioned model

**Files:**
- Modify: `internal/router/types.go:71-84`, `internal/router/filter.go:19-60`
- Test: `internal/router/filter_test.go`

**Interfaces:**
- Consumes: `provider.Provider.AllowUnsanctionedFree`, the catalogue's per-model tier. `catalog.Model` does **not** carry a free tier today — Task 5 adds it.
- Produces: `router.SkipUnsanctioned SkipReason = "unsanctioned"`.

**Implementer:** dcc-superpower-companions:impl-opus-medium
**Evaluation:** files 2 - spec 0 - coupling 1 - risk 2 = 5
**Approach:** inline - skip 2: `SkipRemoved` at filter.go:39 is the same shape

- [ ] **Step 1: Write the failing test**

```go
// An explicit request for the model still routes — the operator named it. What
// this stops is automatic selection putting production traffic through access
// the vendor has not sanctioned, without the operator having said yes.
func TestAnUnsanctionedModelIsNotRoutedByDefault(t *testing.T) {
	for _, tc := range []struct {
		name    string
		optedIn bool
		wantN   int
	}{
		{name: "off by default", optedIn: false, wantN: 0},
		{name: "opted in", optedIn: true, wantN: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Build the snapshot with snapWithModels, marking the model
			// unsanctioned, and the provider with AllowUnsanctionedFree.
			// Assert len(candidates) == tc.wantN, and that a skipped one
			// carries Reason SkipUnsanctioned.
		})
	}
}
```

Fill the body using `snapOf` / `snapWithModels` from `filter_test.go:22,152`.
Do not add a new snapshot helper.

- [ ] **Step 2: Run it to verify it fails**

Expected: FAIL — the model is a candidate in both cases.

- [ ] **Step 3: Add the reason and the filter**

In `types.go`, beside `SkipRemoved`:

```go
	// SkipUnsanctioned is a free tier whose terms the vendor has not
	// sanctioned, on a provider the operator has not opted in. The model stays
	// in the catalogue and stays visible; it is not chosen automatically.
	SkipUnsanctioned SkipReason = "unsanctioned"
```

In `filter.go`, directly after the `!m.Routable()` block:

```go
	if m.FreeTier.Unsanctioned() && !p.AllowUnsanctionedFree {
		return nil, []Skip{{
			ProviderID: t.ProviderID, Model: t.ModelID, Reason: SkipUnsanctioned,
		}}, true
	}
```

This requires `catalog.Model` to carry the tier. If it does not yet, add
`FreeTier FreeTier` to `catalog.Model` and populate it in `mergeOne` from
`FreeModels().Tier(preset id, model id)`, alongside how pricing is merged.
Record in your report which of the two you did.

- [ ] **Step 4: Run it to verify it passes**

Run: `go test ./internal/router/... ./internal/catalog/...`
Expected: PASS.

- [ ] **Step 5: Prove the guard can fail**

Delete the filter block; the "off by default" case must redden. Invert the
`!p.AllowUnsanctionedFree` condition; the "opted in" case must redden. Restore.

- [ ] **Step 6: Commit**

```bash
git add internal/router/types.go internal/router/filter.go \
  internal/router/filter_test.go internal/catalog/
git commit -m "feat(router): skip unsanctioned tiers by default"
```

---

### Task 6: Expose the free-tier record on the model API

**Files:**
- Modify: `internal/admin/catalog.go:18-30,100-120`
- Modify: `docs/API.md` (the `GET /api/models` shape)
- Test: `internal/admin/catalog_test.go`

**Interfaces:**
- Consumes: `catalog.FreeTier`.
- Produces: `free_tier` object on each model row: `{free_type, monthly_tokens, credit_tokens, pool_key, tos}`, or null when the model has no free tier.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 2 - spec 0 - coupling 1 - risk 1 = 4
**Approach:** inline - skip 2: `pricing` on the same row is the pattern

- [ ] **Step 1: Write the failing test**

```go
// The console cannot show a budget or a terms warning it was never sent. A
// model with no free tier sends null rather than a zeroed object, so "free,
// uncapped" and "not free" stay distinguishable.
func TestModelAPICarriesTheFreeTierRecord(t *testing.T) {
	s, _ := testServerFull(t)
	// seed one model with a free tier and one without, via pricedCatalog()'s
	// sibling in this file; assert the first carries free_tier.tos and the
	// second carries "free_tier":null.
}
```

- [ ] **Step 2: Run it to verify it fails**
- [ ] **Step 3: Add the field to the row builder, mirroring `Pricing`**
- [ ] **Step 4: Run it to verify it passes**
- [ ] **Step 5: Prove the guard can fail** — return a zeroed object instead of null for a model with no tier; the second assertion must redden.
- [ ] **Step 6: Update `docs/API.md`** with the new shape, matching the emitted JSON exactly.
- [ ] **Step 7: Commit** — `feat(admin): send the free-tier record`

---

### Task 7: The free badge carries its budget

Per spec §6.3, a free model shows what it gets rather than a bare badge:
`free · ~24M tokens/day`. `tokenLabel` at `models-screen.tsx:60` already
formats a token count.

**Files:**
- Modify: `web/src/features/models/models-screen.tsx:60-92`
- Test: `web/src/features/models/models-screen.test.ts`

**Interfaces:**
- Consumes: the `free_tier` object from Task 6.
- Produces: `export function freeLabel(t: FreeTier | null): string | null`.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 2 - spec 0 - coupling 1 - risk 1 = 4
**Approach:** inline - skip 2: `priceBand`/`priceMarker` are the pattern — exported pure functions tested in `.test.ts`

- [ ] **Step 1: Write the failing test**

```ts
describe("freeLabel", () => {
  it("names the allowance and its period", () => {
    expect(freeLabel({ free_type: "recurring-daily", monthly_tokens: 24_000_000,
      credit_tokens: 0, pool_key: "groq", tos: "ok" })).toBe("free · ~24M tokens/day")
    expect(freeLabel({ free_type: "recurring-monthly", monthly_tokens: 1_000_000,
      credit_tokens: 0, pool_key: "", tos: "ok" })).toBe("free · ~1M tokens/month")
    expect(freeLabel({ free_type: "one-time-initial", monthly_tokens: 0,
      credit_tokens: 200_000_000, pool_key: "", tos: "caution" }))
      .toBe("free · ~200M tokens once")
  })
  it("says free without a figure when the allowance is uncapped", () => {
    expect(freeLabel({ free_type: "recurring-uncapped", monthly_tokens: 0,
      credit_tokens: 0, pool_key: "", tos: "ok" })).toBe("free")
  })
  it("is absent for a withdrawn tier and for no tier at all", () => {
    expect(freeLabel({ free_type: "discontinued", monthly_tokens: 0,
      credit_tokens: 0, pool_key: "", tos: "unknown" })).toBeNull()
    expect(freeLabel(null)).toBeNull()
  })
})
```

Each case differs on exactly one axis from its neighbour, so a function that
ignored `free_type` or ignored the figure fails rather than coincidentally
passing.

- [ ] **Step 2: Run it to verify it fails** — `npm test -- --run models-screen`
- [ ] **Step 3: Implement `freeLabel`**, reusing `tokenLabel` for the figure. Never a hardcoded font size; the label is `text-sm` and its hierarchy comes from `--legend`.
- [ ] **Step 4: Run it to verify it passes**
- [ ] **Step 5: Prove the guard can fail** — return a fixed string; every case but one must redden.
- [ ] **Step 6: Commit** — `feat(console): show a free tier's budget`

---

### Task 8: An unsanctioned tier carries a visible warning

**Files:**
- Modify: `web/src/features/models/models-screen.tsx`
- Test: `web/src/features/models/models-screen.test.ts`, `models-screen.test.tsx`

**Interfaces:**
- Consumes: `free_tier.tos`.
- Produces: `export function tierWarning(t: FreeTier | null): string | null`.

**Implementer:** dcc-superpower-companions:impl-opus-low
**Evaluation:** files 2 - spec 0 - coupling 1 - risk 1 = 4
**Approach:** inline - skip 2: mirrors `priceMarker`'s three-way return

- [ ] **Step 1: Write the failing test** — `avoid` yields a warning naming that nothing routes there automatically; `caution`, `ambiguous`, `ok`, `unknown` and `null` yield null. Assert all six so a function returning a warning for every non-ok verdict fails.
- [ ] **Step 2: Run it to verify it fails**
- [ ] **Step 3: Implement it,** and render the warning in the row. Colour and weight only — no size change, per the project typography rule.
- [ ] **Step 4: Run it to verify it passes**
- [ ] **Step 5: Prove the guard can fail** — widen the predicate to `tos !== "ok"`; the `caution` case must redden.
- [ ] **Step 6: Commit** — `feat(console): warn on an unsanctioned tier`

---

### Task 9: The provider opt-in control

**Files:**
- Modify: `web/src/features/providers/provider-detail.tsx`
- Test: `web/src/features/providers/provider-detail.test.tsx`

**Interfaces:**
- Consumes: `allow_unsanctioned_free` from Task 3.
- Produces: a labelled toggle that PATCHes the provider.

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 1 = 3
**Approach:** inline - skip 2: the `free_models_only` control in the same file is the pattern

- [ ] **Step 1: Write the failing test** — the toggle renders, is off for a provider with `allow_unsanctioned_free: false`, and PATCHes `{"allow_unsanctioned_free": true}` when switched on. Assert the request body, not just that a request happened.
- [ ] **Step 2: Run it to verify it fails**
- [ ] **Step 3: Add the control.** Copy says what happens, not how it is stored: label **"Use models the vendor hasn't sanctioned"**, help text **"Off by default. These free tiers may breach the provider's terms, so nothing routes to them automatically until you allow it."** No hardcoded font size.
- [ ] **Step 4: Run it to verify it passes**
- [ ] **Step 5: Prove the guard can fail** — send an empty PATCH body; the body assertion must redden.
- [ ] **Step 6: Commit** — `feat(console): add the unsanctioned-tier opt-in`

---

### Task 10: Deploy and verify

**Files:** none — this task changes no source.

**Interfaces:**
- Consumes: everything above.
- Produces: a verified running container and a report of the observed numbers.

**Implementer:** dcc-superpower-companions:impl-sonnet-high
**Evaluation:** files 0 - spec 0 - coupling 1 - risk 2 = 3
**Approach:** inline - skip 2: `docs/DEPLOY.md` prescribes the procedure

- [ ] **Step 1: Full suite**

```bash
go test ./... && (cd web && npm test -- --run)
```

- [ ] **Step 2: Build and deploy**

```bash
docker build -t darkraise/darkrouter:latest .
docker compose -f compose.prod.yml -f compose.uat.yml up -d darkrouter
```

The `compose.uat.yml` overlay is required or the published image is pulled over
the local build. The admin port on this machine is **8091**.

- [ ] **Step 3: Verify the deploy took**

```bash
curl -s http://localhost:8091/healthz
(cd web && npm run build)
asset=$(curl -s http://localhost:8091/ | grep -o 'assets/index-[A-Za-z0-9_-]*\.js')
curl -s "http://localhost:8091/$asset" > /tmp/served.js
cmp /tmp/served.js internal/admin/dist/assets/index-*.js && echo "deploy matches source"
```

- [ ] **Step 4: Log in and look**

The password is in `.uat-credentials` at the repository root — gitignored, and
it stays that way. Never copy it into a tracked file, a commit message, or a
report. `POST /api/auth/login` needs the CSRF token from `GET /api/auth/status`
and an `Origin` header. If it returns 401 the file has drifted: regenerate with
`go run ./cmd/darkrouter hash-password -password X`, put the hash in `.env` with
`$` doubled and the plaintext in `.uat-credentials`, and redeploy.

Confirm on the running console, by looking:
- a free model shows its budget rather than a bare badge
- an `avoid`-graded model carries a visible warning
- the provider opt-in renders and is off

- [ ] **Step 5: Record the numbers**

Report how many catalogued models carry a free tier, the `tos` split, and how
many rows the import filter now skips. Expected from upstream as of
2026-09-03: 437 rows, `caution` 266, `avoid` 78, `ok` 50, `ambiguous` 38,
`unknown` 5. A material difference from these means upstream moved — say so
rather than adjusting the expectation.

- [ ] **Step 6: Commit** — only if Step 4 required a fix.

---

## Self-Review

**Spec coverage.** §6's record widening is Task 1; §6.1's `tos` policy is Tasks
2–5 and 8–9; §6.2's display-only pools ride in Task 1's record and Task 6's API
with no routing input, as specified; §6.3's console treatment is Tasks 7–9.

**Known gap, deliberate.** §6.2 says pools are stored and shown. `pool_key`
reaches the API in Task 6 and no task renders it — one of 76 pools is shared by
more than one provider (`zhipu-flash-free`, by `glm` and `glm-cn`), so a
per-pool display would be a column that reads the same as the provider name on
75 of 76 rows. Rendering it is deferred until a second shared pool exists.
Recorded here so it is a decision rather than an omission.

**Numbers refreshed.** The spec's counts (451 rows; `caution` 271, `avoid` 87)
were a 2026-09-03 morning snapshot. Upstream now publishes 437 rows: `caution`
266, `avoid` 78, `ok` 50, `ambiguous` 38, `unknown` 5. Verified against
`/tmp/omniroute` the same day. The policy is unchanged — roughly one row in
nine is still unambiguously fine.

**Type consistency.** `FreeTier` is produced by Task 1 and consumed under that
name by Tasks 4, 5, 6, 7 and 8. `AllowUnsanctionedFree` is produced by Task 2
and consumed by Tasks 3, 4, 5 and 9. `SkipUnsanctioned` is produced and
consumed only within Task 5.

**Rule S.** Every task's `files + spec + coupling` is 3 or less and no task
scores 3 on spec completeness.

**One coupling worth naming at dispatch.** Task 1 changes the shape of a
generated file whose parse failure degrades silently to an empty catalogue.
Its regeneration step and its embedded-record guard are in the same task for
that reason, and Task 1 must land before any other task is dispatched.
