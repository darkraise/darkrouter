# The Operator Console — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task.

**Goal:** Build the fifteen approved screens against the API that now exists, replacing the phase 7 dashboard, and meet §12's done criteria.

**Architecture:** §9's shape — feature folders, one API type module, typed query hooks behind a key factory, filters in router search params. The theme becomes a `darkraise-ui` config block rather than the `!important` cascade `darkrouter-ui.css` is today, which is what 6.5.0 was released to make possible.

**Spec:** `docs/superpowers/specs/2026-08-24-darkrouter-phase10-operator-console.md` — §5 (information architecture), §6 (the screens), §9 (frontend architecture), §12 (done criteria). §13 step 4.

**The design is already approved.** `docs/ux/mockups/fragments/*.html` is the contract, not a suggestion. Fragments `00`, `01` and `17` are the design language, the ladder specimen and the contrast proof — reference material rather than screens. The fifteen screens are `02`–`16`.

## Global Constraints

- TDD: a failing test precedes the implementation.
- `npm test`, `npm run build` and `npx tsc --noEmit` clean in `web/` before any commit; `go build ./...` clean, since the build writes into `internal/admin/dist`.
- **The ladder is defined once and copied, never reimplemented.** Fragment `01` is the contract; screens 04, 09, 10 and 17 embed it byte-identically. Three modes — retrospective (filled marks, the attempts happened), predictive and compressed (hollow, nothing has been sent). Fill versus outline is the only thing separating them, so a filled mark outside a trace is a bug.
- **State colour has exactly three carve-outs**, named on screen 00: destructive affordance, request outcome, attention. No state colour in the ladder gutter, and no `--trace` on a provider pip.
- **Coral is brand only** — position and primary action, never state — so it never joins amber and red in the ladder gutter.
- **Poll intervals are §5's**, unchanged: 3s for the overview and the requests first page, 30s for catalog and usage, paused when the tab is hidden.
- Comments explain WHY, never WHAT. No comment may reference this plan or task.
- Commit subjects: `<type>(<scope>): <subject>`, imperative, **50 characters or fewer**, no trailing period.
- English only. Stage explicit paths; never `git add -A`.

---

### Task 1: The architecture, before any screen

**Files:** `web/src/lib/`, `web/src/features/`, `web/src/theme.config.ts`, `web/index.html`

Doing this first is the whole point of §9: fifteen screens built on the current shape would multiply the grab bag rather than replace it.

- [ ] **Step 1: One API type module**

Response types are hand-copied into whichever file needs them — `Trace` in the trace drawer, `Provider` in settings, neither with any relationship to the Go source. Put every wire type in `src/lib/api-types.ts`, mirroring the json tags in one place, so a Go field rename has one place to land instead of five.

Write a test that fails when a type drifts from the handler, or say plainly in the module header that nothing enforces the mirror and it is maintained by hand — an unenforced claim of safety is worse than an admitted gap.

- [ ] **Step 2: A key factory and typed query hooks**

Replace inline `useQuery` with raw path strings. Poll intervals live on the hook, not at each call site, so §5's cadence cannot drift per screen.

- [ ] **Step 3: Filters in router search params**

Every filtered view is a URL: a filtered Requests view must survive a reload and be pasteable. Component state cannot do that, which is why §5 states it as a rule.

- [ ] **Step 4: The theme becomes a config block**

`theme.config.ts` drops from seventeen axes to two — mode and density. Warm Console is now reachable from the library: `surfaceColor: "sepia"`, `accentColor: "coral"`, `density: "comfortable"`, `radius: "rounded"`, `elevation: "low"`, `accentIntensity: "calm"`. Everything `darkrouter-ui.css` overrode with `!important` should now be an axis setting; whatever genuinely cannot be expressed stays, and gets a comment saying why.

`accentIntensity: "calm"` is pinned deliberately: it is the only step whose button labels clear AA, which 6.5.0's changelog records under Known limitations.

- [ ] **Step 5: Shrink the pre-hydration script**

`index.html` hand-mirrors twelve axis-to-localStorage mappings and a preset-neutralisation table across forty lines. With two axes it needs two. It is a standing maintenance hazard precisely because it duplicates logic that lives in the library.

- [ ] **Step 6: Toasts on mutations**

`Toaster` ships and is unused; errors set a string that renders in a corner. Every mutation reports through it.

- [ ] **Step 7: Verify and commit**

Subject: `refactor(web): adopt the feature-folder architecture`

---

### Task 2: The shell and the palette

**Spec:** §5.

- [ ] **Step 1: Three rail groups plus Settings**

Operate (Overview, Requests, Usage), Configure (Providers, Models, Routing), Use (Playground, Connect), with Settings pinned at the bottom. Eight items read as three because the rail groups them.

Breaker state, discovery health, preset browsing, aliases and model overrides get **no destination of their own**. Each is one panel beside the subject it describes. A task that adds a ninth rail item has misread §5.

- [ ] **Step 2: The command palette**

⌘K jumps to a provider, a model, a request id or an alias directly. Reachability is the palette's job, not the rail's.

- [ ] **Step 3: Verify and commit**

Subject: `feat(web): build the console shell and palette`

---

### Task 3: Operate — Overview, Requests, the trace, Usage

**Fragments:** `02`, `03`, `04`, `05`. **Spec:** §6.1–§6.4.

These need no endpoint that did not already exist, so they land first and prove the architecture before the harder screens depend on it.

- [ ] **Step 1: Overview**

The routing flow graph replaces a health grid: aliases left, router centre, providers right in priority order, edge thickness as share, dashed returns for traffic that arrived somewhere because somewhere else refused it. A provider that is not a candidate has no edge at all. Its left-hand column reads entirely from `usage` grouped by alias.

- [ ] **Step 2: Requests, with filters in the URL**

- [ ] **Step 3: The request trace**

The ladder in retrospective mode — filled marks, because the attempts happened. Every attempt and every skipped candidate, with no colour needed to tell the four states apart. §12: a failover must be findable in three clicks.

- [ ] **Step 4: Usage**

Charts come from `darkraise-ui/components/chart`, a subpath import because recharts is an optional peer. **The engine's chart ramp must be overridden here.** With the accent's generated ramp, `--chart-4` and `--chart-5` read as the reserved cooling amber and healthy green, which on the Usage charts specifically means a series looks like a state. The replacement is a monochrome accent ramp differentiated by fill — solid, 60% tint, 45° hatch, dot, outline — scoped to a `.chart-scope` class rather than `:root`, because custom properties inherit and a class on an ancestor wins.

- [ ] **Step 5: Verify and commit**

Subject: `feat(web): build the three Operate screens`

---

### Task 4: Configure — Providers, Models, Routing

**Fragments:** `06`, `07`, `08`, `09`, `10`. **Spec:** §6.5–§6.7.

- [ ] **Step 1: Providers, with breaker and discovery panels**

Provider pips take the four states `GET /api/overview` actually emits — healthy, degraded, disabled, unconfigured. **`degraded` is not a synonym for `cooling`**: a credential cools, a provider degrades. Breaker detail comes from `GET /api/health/providers`; reset, probe and forced discovery are the three actions.

- [ ] **Step 2: Provider detail and the preset browser**

- [ ] **Step 3: Models, with the compressed ladder and the override editor**

Hollow marks: nothing has been sent. The override editor writes through `PUT /api/models/{provider}/{model}/override`.

- [ ] **Step 4: Routing — alias chains, validated in the browser**

Predictive ladder, hollow. Route preview drives it: §12 requires that, given an alias, the preview shows the same ordered candidate list a real request would produce. `POST /api/route/preview` already guarantees that by sharing the executor's snapshot — the screen must not re-sort what it returns.

§12: an alias chain can be created, reordered and validated in the browser, and takes effect on the next request.

- [ ] **Step 5: Verify and commit**

Subject: `feat(web): build the three Configure screens`

---

### Task 5: Use — Playground and Connect

**Fragments:** `11`, `12`, `13`. **Spec:** §6.8–§6.9.

- [ ] **Step 1: Playground and its compare mode**

- [ ] **Step 2: Connect**

Per-client proxy tokens: `GET/POST/DELETE /api/proxy-tokens`. **The secret is shown once, at creation, and never again** — the API cannot reproduce it, so a screen that implies otherwise is lying about what it can do.

- [ ] **Step 3: Verify and commit**

Subject: `feat(web): build the Playground and Connect screens`

---

### Task 6: Settings, login and first run

**Fragments:** `14`, `15`, `16`. **Spec:** §6.10–§6.11.

- [ ] **Step 1: Settings**

Every config block, each value showing its source and whether it is hot-reloadable — `GET /api/config` returns both. A restart-only field is rendered as such rather than offered and refused. Sessions and the password change live here.

**Aliases and policy say where they live.** After the first run they are database-owned and editing the YAML has no effect; §8.1 requires the config view to say so at the point of display.

- [ ] **Step 2: Login and first run**

§12: a fresh install with no providers and no password hash explains itself rather than presenting a login that refuses every password.

- [ ] **Step 3: Verify and commit**

Subject: `feat(web): build settings, login and first run`

---

### Task 7: The gate

- [ ] **Step 1: Walk §12's done criteria one at a time**

Every screen renders against a real gateway in both modes. A failover is findable in three clicks. Route preview agrees with the router. A provider can be renamed, reprioritised, disabled, probed, its breaker reset and its discovery forced without touching a file. An alias chain can be created, reordered and validated. Spend shows a real number for a priced model. A fresh install explains itself.

Record each as met or not. A criterion that cannot be met is a finding to report, not one to quietly drop.

- [ ] **Step 2: Both modes, every screen**

Light is not a palette swap in this language — surfaces stack upward in both modes. Screen 17 is the contrast proof and re-measures the nine repairs.

- [ ] **Step 3: Commit**

Subject: `test(web): gate the console against its criteria`

---

## Notes for the executor

**The mockups are the contract.** When the built screen and the fragment disagree, the fragment wins unless the fragment is wrong — and if it is, say so rather than diverging silently.

**Do not port `darkrouter-ui.css` wholesale.** It is the override block 6.5.0 was released to delete. Task 1 converts what it encodes into axis settings; what survives should be a short file of genuine exceptions, each with a comment saying why the library cannot express it.

**Four screens embed the ladder and one defines it.** If the ladder markup is being written a second time, stop: fragment `01` is the source, copied byte-identically, indentation included.

**The API is complete but only unit-verified.** Phase 9's history records that live verification against Groq found two defects the suite had passed over. Expect the same here, and treat UAT findings as the point of the exercise rather than as surprises.
