# darkraise-ui 6.5.0 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task.

**Goal:** Make Warm Console expressible from `darkraise-ui`'s own axes, so Darkrouter's theme becomes a config block rather than the `!important` cascade `darkrouter-ui.css` is today.

**Architecture:** Four of the five changes are in the theme engine and its palettes — widen one constant, register one accent scale, emit one token, and correct four light-mode values that fail their contrast floor. The fifth extends `DataTable`, which is component work with no theme coupling. Nothing here is Darkrouter-specific: every change is a capability the library was missing, which is why it ships upstream rather than as an override block.

**Spec:** `docs/superpowers/specs/2026-08-24-darkrouter-phase10-operator-console.md` §7. §13 step 1.

**Repository:** `/root/repositories/darkraise-web-template`, branch `feat/6.5.0-console-tokens`, package `packages/ui` (6.4.0 today). This is **not** the Darkrouter repository; only this plan document lives there.

## Global Constraints

- TDD: a failing test precedes the implementation.
- The gate is `cd packages/ui && npx vitest run` plus `pnpm --filter darkraise-ui typecheck`, both clean before any commit.
- **Run the suite with `npx vitest run` from inside `packages/ui`, not `pnpm --filter darkraise-ui test`.** The filtered form fails a dozen unrelated component tests when anything else is loading the machine; the direct form runs 168 files in about fifteen seconds. A scattered failure list across components you did not touch is that contention, not a regression — re-run before believing it.
- **`pnpm -r typecheck` cannot be the gate: `apps/template` fails on `master` at 6.4.0**, on `surfaceIntensity` missing from the config type it imports from `dist`. Pre-existing and out of scope; `packages/ui` typechecks clean and is what this plan holds itself to.
- **`src/styles/build-output.test.ts` reads `dist/styles.css` and fails if it is absent**, so a full green run needs `pnpm --filter darkraise-ui build` to have completed at least once. That build takes upwards of twenty minutes — start it in the background rather than killing it, because a half-written `dist` is what makes this test fail.
- Commit subjects: `<type>(<scope>): <subject>`, imperative, **50 characters or fewer**, no trailing period.
- Comments explain WHY, never WHAT. No comment may reference this plan or task.
- English only.
- **`CHANGELOG.md` is Keep a Changelog.** Every behaviour change adds an entry under `## [Unreleased]` in the same commit that makes it. The release task turns that heading into `## [6.5.0] — 2026-08-26`.
- **This is a minor release, so nothing may break.** Widening a union, adding a scale and emitting a new token are additive. The contrast repairs change rendered colour for every consumer — that is the point of shipping them, but each one is called out in the changelog under `### Fixed` with its measured before and after.

## The baseline is red

`pnpm --filter darkraise-ui test` fails one test of 2479 on `master` at 6.4.0, before this plan touches anything:

    FAIL src/theme/theme-switcher/ThemeSettingsPanel.test.tsx
      > names its axis sliders so they are reachable by role and name

Task 0 fixes it. Do not start Task 1 against a red suite — every later task's "watch it fail" step becomes unreadable if one failure is always present.

---

### Task 0: Make the axis sliders reachable by name

**Files:**
- Investigate: `src/components/slider/Slider.tsx`, `src/theme/theme-switcher/AxisControl.tsx`
- Test: `src/theme/theme-switcher/ThemeSettingsPanel.test.tsx`

**Done — the cause was test pollution, not a labelling bug.** The test passes in isolation and fails when the file runs whole. `ThemeProvider` persists the chosen preset to `localStorage`; the earlier test *"thins a group without hiding its heading"* clicks the sci-fi preset radio; `scifi.ts` lists `radius` in `hiddenCommonAxes`. Every test after that click ran under sci-fi, where the radius row is not rendered at all. Clearing storage in `beforeEach` restores isolation.

The comment claiming the `aria-label` "landed on a wrapper span" was stale and is replaced: eleven `role="slider"` elements each carry their axis name correctly.

- [x] **Step 1: Find the real cause before changing anything**

`getByRole` filters by accessibility, `querySelectorAll` does not — that difference is the likeliest lead. Check whether the Radius thumb is excluded by `aria-hidden` on an ancestor, by `display: none` in the jsdom-computed style, or by a duplicate accessible name colliding with the row's own `<label>`.

Write the finding into the test's comment, replacing the stale one. A future reader must not re-derive this.

- [x] **Step 2: Fix the cause, not the assertion**

Whatever the cause, the fix belongs in `Slider.tsx` or `AxisControl.tsx`. Do not relax the assertion, do not query by `querySelector`, and do not delete the test. If the conclusion is that the test asserts something the panel should not do, stop and report rather than deleting it.

- [x] **Step 3: Run everything**

Run: `cd packages/ui && npx vitest run`
Result: 168 files, 2479 passed, 0 failed.

- [x] **Step 4: Commit**

Committed as `4e082f3`, subject `test(theme): isolate preset state between tests`. The planned subject named the Slider, which is not where the fault was.

---

### Task 1: Expose the twelve neutral surface ramps

**Files:**
- Modify: `src/theme/types.ts`
- Test: `src/theme/palettes/surfaceColors.test.ts` (new)

`src/theme/palettes/surfaceColors.ts` builds and registers twelve neutral `ColorScale`s — slate, gray, cool, zinc, neutral, iron, mauve, graphite, stone, sand, olive, sepia. Exactly one, `slate`, is selectable, because `types.ts` reads:

```ts
export const SURFACE_COLORS = ["slate", ...ACCENT_COLORS] as const
```

Eleven built scales are unreachable, and the eighteen names that *are* reachable are seventeen accent hues plus one neutral.

**Done — but §7's "widening that one constant exposes all twelve" is wrong.** Four sites treat `slate` as the only neutral and route every other surface name to `accentColors`, where the new ramps do not exist, so widening alone crashes on `undefined[500]`:

| Site | What it does |
|---|---|
| `resolveSurfaceScale` | returned slate for slate, tinted an accent for everything else |
| `resolveSfHueTokens` | same branch, for the three gradient hue tokens |
| `--surface-tint` (`generateTokens.ts`) | `surfaceColor === "slate" ? neutral[500] : accentColors[...][500]` |
| the switcher's swatch preview | same, with slate's mid-tone as a magic string |

Each now dispatches on whether the name is registered, via an exported `isAccentSurface` predicate that narrows the accent branches without a cast. `resolveNeutralScale` is also exported, and a chosen neutral now carries its own hue into the text tiers — otherwise a warm ground sits under slate-derived text, which is the override block this release exists to delete.

Committed as `be212cf`. 169 files, 2485 tests, typecheck clean. The 29-swatch grid uses `auto-fill` and wraps to four rows rather than overflowing, so no CSS change was needed.

- [x] **Step 1: Write the failing test**

Assert that every key of `surfaceColors` appears in `SURFACE_COLORS`. Write it as a set comparison over `Object.keys(surfaceColors)`, not a hardcoded list of twelve, so registering a thirteenth ramp without exposing it fails here too.

Assert separately that the seventeen accent names are still present — widening must not drop the accent-as-surface capability consumers already have.

- [x] **Step 2: Run it, watch it fail**

Run: `pnpm --filter darkraise-ui test -- surfaceColors`
Expected: FAIL naming the eleven missing neutrals.

- [x] **Step 3: Widen the constant**

```ts
export const SURFACE_COLORS = [
  ...(Object.keys(surfaceColors) as (keyof typeof surfaceColors)[]),
  ...ACCENT_COLORS,
] as const
```

If deriving it from the registry creates an import cycle (`types.ts` is imported by the palettes), spell the twelve names out literally and let the test be what keeps the two in step. Prefer the literal list if there is any doubt — a cycle here fails at module init, not at build.

Warm Console wants `sepia` (hue 36); `stone` is the workable second. **This supersedes the earlier ask for `graphite`**, which is hue 210 and belongs to the cool language this phase was first drawn in.

- [x] **Step 4: Check the switcher renders twelve more swatches**

The Surface Color control reads `SURFACE_COLORS`. Confirm it does not overflow its grid at twenty-nine entries; if it does, that is a `theme-switcher.css` change in this task, not a reason to narrow the constant.

- [x] **Step 5: Run everything, changelog, commit**

Subject: `feat(theme): expose all twelve surface ramps`

---

### Task 2: Add a coral accent

**Files:**
- Modify: `src/theme/palettes/accentColors.ts`, `src/theme/types.ts`
- Test: `src/theme/palettes/accentColors.test.ts` (new or existing)

`ACCENT_COLORS` is seventeen named hues. Coral — `hsl(12, 75%, 59%)`, the brand this console adopts — sits between `red` at hue 0 and `orange` at hue 25 and matches neither. The accent scale drives `--ring`, `--focus-ring`, `--chart-1..5` and the destructive branch as well as the three `--primary*` tokens, so an override is five declarations before the chart ramp even starts.

**Done — `da646a5`.** Hue 12 at every stop, drifting 17→6 the way `orange` drifts 33→13. Lightness follows red's profile; saturation is red's scaled so 500 lands on the pinned `12 75% 59%`. The midpoint of the two neighbours would have been 89% saturation, which reads as a signal colour rather than a brand one.

Inserting an eighteenth entry into `ACCENT_COLORS` shifts the wrap-around neighbour `resolveSfHueTokens` uses for a gradient's second hue. Decorative only, and in the changelog.

- [x] **Step 1: Write the failing test**

Assert `accentColors.coral` exists, that `ACCENT_COLORS` contains `"coral"`, and that the scale has all eleven stops (50–950) in the `"H S% L%"` string shape the other scales use. Assert `coral[500]` is at hue 12.

- [x] **Step 2: Run it, watch it fail**

- [x] **Step 3: Build the ramp**

Derive the eleven stops the way the neighbouring scales are derived rather than inventing values: read how `red` and `orange` step their saturation and lightness and interpolate at hue 12. `500` is fixed at `12 75% 59%` by the spec; the rest must look like a sibling of the scales around it.

- [x] **Step 4: Prove the ramp is monotonic**

Lightness must decrease from 50 to 950 with no reversal. A reversed stop is invisible in a swatch grid and produces a `--primary-fill` darker than `--primary` at one intensity only.

- [x] **Step 5: Run everything, changelog, commit**

Subject: `feat(theme): add the coral accent`

---

### Task 3: Emit a third text tier

**Files:**
- Modify: `src/theme/engine/generateTokens.ts`
- Test: `src/theme/engine/generateTokens.test.ts`

The engine emits `--foreground` and `--muted-foreground` and stops. Warm Console reads a third, quieter tier for column heads, captions and unit suffixes — `--legend` in `darkrouter-ui.css`. Two tiers force either a caption that competes with body text or one that fails its contrast floor.

**Done — `c7b6452`, and it was not additive.** `--muted-foreground` was ramp step 500, which measures **4.31:1** in light across the twelve ramps — already under AA on the warmer ones. One step quieter is 2.37:1, so no third tier could exist beneath it.

The owner chose to compute the tiers against the background rather than snap them to steps: `--muted-foreground` targets 7:1, `--legend` targets 4.6:1. Muted text renders darker in light mode for every consumer. `--sidebar-foreground-muted` is now exactly `--muted-foreground` instead of a parallel pair of steps.

- [x] **Step 1: Write the failing test**

Assert `generateTokens` emits `--legend` in both modes. Assert it is quieter than `--muted-foreground` and still clears **4.5:1** against `--background` — it carries text, so the 3:1 non-text floor does not apply.

- [x] **Step 2: Run it, watch it fail**

- [x] **Step 3: Emit it**

Follow the polarity `--muted-foreground` already uses (light → `neutral[500]`, dark → `neutral[400]`), one step quieter, and keep the 4.5:1 floor. If one step quieter cannot clear 4.5:1 in light, the tier is the constraint and the step size gives way — say so in a comment on the value.

- [x] **Step 4: Run everything, changelog, commit**

Subject: `feat(theme): emit a third text tier`

---

### Task 4: Repair the light-mode contrast failures

**Files:**
- Modify: `src/theme/engine/generateTokens.ts`
- Test: `src/theme/engine/contrast.test.ts` (new)

These are the kit's own defects, independent of Darkrouter and separate from the nine repairs `darkrouter-ui.css` makes to 9router's palette.

| Token | Light value today | Measured | Required |
|---|---|---|---|
| `--focus-ring` | sky-200/300 | 1.28–1.58:1 | 3:1 |
| `--success` | emerald-500 `#10B77F` | 2.48:1 | 3:1 |
| `--warning` | amber-500 `#F59F0A` | 2.03:1 | 3:1 |
| `--destructive` as text | red-500 | fails | 4.5:1 |

`--primary` is a fifth and what it measures depends on the accent: 2.74:1 at sky, 3.23:1 at the coral Task 2 adds — over the 3:1 a mark is held to, under the 4.5:1 text is. It matters more than it looks, because `dist/styles.css` documents that form controls take their focus indicator from `--primary` rather than `--focus-ring`: at 2.74:1 every text field, select and textarea in every consuming app has a sub-3:1 focus ring in light mode.

**Done — `ea8110d`, four of five.** `--focus-ring`, `--success`, `--warning`, `--destructive` and `--primary` are each moved to the nearest lightness on their own hue that clears their floor, so anything already passing keeps its place on the accent ladder.

**The button-label floor was deliberately left at 3:1.** Raising `FOREGROUND_MIN_RATIO` to 4.5 broke 77 tests, and not value pins: 66 assert the label *stays white* (the `VIBRANCY` notes call out that flipping to ink makes label colour vary by accent) and 11 assert light mode ignores the axis (fixing labels needs fills moved off the pinned OKLCH ladder, and light is documented as axis-independent). Worst case per step, all eighteen accents:

| Step | Light | Dark |
|---|---|---|
| calm | 3.29 | **4.75** |
| balanced | 3.29 | 4.10 |
| vivid | 3.29 | 3.59 |
| intense | 3.29 | 3.18 |

`dark` + `calm` is the only combination clearing AA, which is why Darkrouter pins it. Recorded in the changelog under Known limitations; raising it is a major.

- [x] **Step 1: Write the failing test**

One table-driven test over every accent in `ACCENT_COLORS` and both modes, asserting each token clears its floor: 3:1 for `--focus-ring`, `--success`, `--warning` and `--primary`; 4.5:1 for `--destructive` when used as text. Drive it from the engine's own output, not from hardcoded hex — a repair that only fixes the default accent is not a repair.

- [x] **Step 2: Run it, watch it fail**

Expected: failures across most accents, at the ratios tabulated above.

- [x] **Step 3: Raise `FOREGROUND_MIN_RATIO`**

`pickForeground` enforces only `FOREGROUND_MIN_RATIO = 3`, which is why button labels are not AA-safe at every `accentIntensity` — `calm` emits a fill at 4.96:1 against a white label while `balanced` emits 4.37:1 and fails. Raise the floor to 4.5 and let the picker flip to ink where white no longer clears it.

This changes rendered label colour on some accent/intensity pairs. That is a visible change in a minor release and belongs in the changelog under `### Fixed` with the pairs it moves.

- [x] **Step 4: Repair the four token values**

Move each to the nearest stop on its own scale that clears the floor, rather than to a hand-picked hex — staying on the ramp is what keeps a repair from becoming a new palette.

- [x] **Step 5: Confirm dark mode did not regress**

The floors apply in both modes. Dark already passes; the test covers it so a light-mode repair cannot quietly break it.

- [x] **Step 6: Run everything, changelog, commit**

Subject: `fix(theme): clear the light-mode contrast floors`

---

### Task 5: Extend DataTable with faceted filters and virtualization

**Files:**
- Modify: `src/data-table/components/data-table/DataTable.tsx`, `src/data-table/components/data-table-toolbar/DataTableToolbar.tsx`
- Test: alongside each

`DataTable` offers sorting, column visibility, CSV export and a single-column text filter. A 197-row provider list and a long request log want faceted filters and virtualization, and adding them here keeps Darkrouter's tables on the house component.

**Done — `e68904b`, with no new runtime dependency.** `@tanstack/react-virtual` is not installed; windowing is hand-rolled with padding rows above and below the window, which keeps the header sticky and the column widths shared. Row height is declared rather than measured because every row here is one `density` cell.

Building the facet surfaced a defect in the kit: `DropdownMenuCheckboxItem` skipped `onCheckedChange` whenever `onSelect` prevented default. Preventing default is how a caller keeps the menu open, which is exactly what a multi-select needs, so such an item could never be checked. Fixed here; `ColumnVisibility` benefits too.

- [x] **Step 1: Write the failing tests**

Faceted filter: given a column marked facetable, the toolbar offers its distinct values with counts, selecting two shows the union, and clearing restores every row.

Virtualization: given 5000 rows, the DOM holds a bounded number of row elements, scrolling changes which rows are mounted, and the accessible row count still reports 5000.

- [x] **Step 2: Run them, watch them fail**

- [x] **Step 3: Implement**

Both are opt-in props, defaulting off. A consumer on 6.4.0 must get byte-identical markup after upgrading unless they pass the new props — that is what makes this a minor release.

- [x] **Step 4: Run everything, changelog, commit**

Subject: `feat(data-table): add faceted filters and virtual rows`

---

### Task 6: Release 6.5.0

- [ ] **Step 1: Verify**

Run: `pnpm --filter darkraise-ui test && pnpm -r typecheck && pnpm --filter darkraise-ui build`

All three clean, or the release does not happen.

- [ ] **Step 2: Bump and date the changelog**

`packages/ui/package.json` to `6.5.0`. Turn `## [Unreleased]` into `## [6.5.0] — 2026-08-26` and open a fresh empty `## [Unreleased]` above it.

- [ ] **Step 3: Commit**

Subject: `chore(release): v6.5.0`

- [ ] **Step 4: Point Darkrouter at it**

In the Darkrouter repository, move `web/package.json`'s `darkraise-ui` dependency from `^6.4.0` to `^6.5.0` and install. That commit belongs to Darkrouter, not here, and is the first task of the console plan rather than the last of this one.

---

## Notes for the executor

**Five changes, not four.** §7's prose says "Four changes" and then lists five: the surface ramps, coral, the third tier, the contrast repairs and `DataTable`. The count is stale, the list is not. Task 0 makes six.

**The contrast repairs are the only breaking-feeling change.** Everything else is additive. If a consumer's snapshot tests fail after upgrading, it is Task 4 that did it, and the changelog must make that findable without reading the diff.

**Do not import Darkrouter's `darkrouter-ui.css` into this repository for reference.** It is the override block this release exists to delete; treating it as the specification would bake one consumer's workarounds into the library. §7 and §3 of the spec are the contract.
