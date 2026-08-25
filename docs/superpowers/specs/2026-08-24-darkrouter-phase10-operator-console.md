# Phase 10 — The operator console

**Status:** Approved design, 2026-08-24.
**Master design:** `2026-08-22-darkrouter-design.md`
**Supersedes:** phase 7 §5–§6 (the frontend and its five screens). Phase 7 §3 (authentication) and §4.2 (keyset pagination) stand unchanged.
**Depends on:** phase 9, and on a `darkraise-ui` 6.5.0 release described in §7.

---

## 1. Goal

The dashboard renders roughly a fifth of what the gateway already knows. Twenty-two admin
endpoints serve providers, catalog, traces, usage, config and presets; the SPA fully consumes
eight of them, uses ten partially, and never calls four at all. `PATCH /api/providers/{id}` is
implemented, wired, and has no caller anywhere, so a provider cannot be renamed, reprioritised,
disabled or given a Bedrock region from the browser.
`GET /api/usage` serves 365 days of daily requests, tokens and cost against no screen at all.
`GET /api/config` returns the whole policy and server blocks and the SPA's `Config` type does not
declare them.

Phase 10 closes that gap in both directions: an operator console designed rather than scaffolded,
and the admin API surface it needs. It also reopens, deliberately, the boundary phase 7 §2 drew
around editing aliases and policy — that boundary is the reason the API stopped at twenty-one
endpoints, and it is now the single largest source of "edit the file and restart".

## 2. Scope boundary

**In:** a bespoke design language; nine destinations plus login and first-run; the routing ladder
as a shared component; admin API additions for cost, usage detail, health, discovery, aliases,
policy, model overrides, credentials, proxy tokens, sessions and route preview; a `darkraise-ui`
6.5.0 release; a mockup phase that gates implementation.

**Out:** multi-user anything. No teams, no roles, no SSO, no per-user attribution, no audit trail
of who changed what — there is one operator and the answer is always the same person. No prompt
management, no evaluation framework, no dataset curation. No semantic caching or semantic routing.
These are named because the reference survey is full of them and each is a product of its own.

**Out for now:** request/response body capture. `capture.bodies` and its retention sweep exist,
nothing in the codebase ever writes `request_bodies`, and the trace drawer's Bodies panel is
therefore permanently empty. Phase 10 makes the panel say so plainly rather than rendering an
empty box that reads as a bug. Wiring the writer is its own decision about storing prompt content
on disk.
## 3. Design language — Warm Console

The identity is adopted from **9router** (`github.com/decolua/9router`), whose dashboard is the
reference the owner named. A warm near-black chassis, coral brand, generously rounded surfaces
that stack upward from the ground, Inter throughout the chrome, and a faint accent graticule
behind the page. The console reads as a piece of software someone maintains rather than an
instrument someone calibrated.

Values are taken from `src/app/globals.css` verbatim wherever they measured clean, and repaired
where they did not. Nine did not: 9router's light mode ships a success green at 2.54:1 and an
amber at 2.15:1 — both under the 3:1 floor that applies to a shape and not merely to text — and
its brand at text size reaches 3.23:1. `fragments/17-light-proof.html` shows all nine beside the
values that replace them, and the repair is part of the adoption, not a deviation from it.

The structural idea is **elevation**. The ground is the darkest surface in dark mode and the
palest in light; a card sits on the ground, an inset region sits on the card, and every step is
lighter than the one beneath it. Cards are separated by a gutter and carry a border, a radius and
a soft shadow. This replaces the previous language's recessed **well**, and the consequence is
deliberate and stated: light mode here is a palette swap rather than a polarity inversion, so the
light proof measures whether the palette holds its floors rather than whether the ordering flips.

### 3.1 Colour

Colour answers three different questions and never blurs them.

**Coral is BRAND.** `--accent` `#E56A4A` marks where you are in the navigation and which control
is the primary action on a screen. It says nothing about the state of the system. It is kept out
of the ladder gutter and off every status pip on purpose: coral sits between amber and red in hue,
so a coral routing mark beside an amber cooling mark and a red failure would put three warm hues
in one column doing three unrelated jobs.

**`--trace` is the router's VERDICT.** A cool blue — `#60A5FA` dark, `#2563EB` light — for what
the router *decided* among viable alternatives: the served row, the gutter mark, the failover
chip.

**State colours are provider IDENTITY.** Green, amber and red for what a provider *is*: healthy,
degraded, failed. A provider pip carries one of the four states `GET /api/overview` emits —
healthy, degraded, disabled, unconfigured — and `degraded` is not a synonym for `cooling`: a
credential cools, a provider degrades.

Three carve-outs exist and only three, each named on the design-language screen: the **destructive
affordance** (`--state-failed` on a control that removes something), the **request outcome**
(`--state-healthy` / `--state-failed` on a finished request or probe), and **attention**
(`--state-cooling` on a condition that is neither a failure nor a provider state — a stale sweep,
a warning chip, a dangling alias, a value not yet set). A state colour used for anything not on
that list is a bug.

Brand gets one further allowance: `--shadow-warm`, the coral-tinted shadow, is spent only on the
brand mark in the sidebar. Nothing else in the product may use it.

### 3.2 Typography

**Inter** carries the console and **IBM Plex Mono** carries the data. This is the one rule kept
from the previous language, and it is kept because a gateway console is mostly machine output:
mono means *this came off the wire*, Inter means *the console is telling you something*.

| Role | Spec |
|---|---|
| Legend | 10px / Inter 600 / +0.09em / uppercase |
| Micro data | 11px / Mono 400 / tabular |
| Nav item | 13px / Inter 500 |
| Table and body data | 13px / Mono 400 / 1.5 / tabular |
| Emphasis data | 13px / Mono 500 |
| Prose | 14px / Inter 400 / 1.5 |
| Section title | 16px / Inter 600 / −0.01em / sentence case |
| Page title | 20px / Inter 600 / −0.015em |
| Primary readout | 30px / Mono 600 / tabular / −0.015em |

Nothing exceeds 30px anywhere. Numerals are tabular always. Units set at 10px in `--legend`
immediately after the value with no space. Numeric columns right-align; identifier columns
truncate from the middle. The uppercase small-caps role survives only as `.legend-caps`, on nav
groups and column heads, where the label really is a legend and not a title.

### 3.3 Surface

Radius runs **4 / 8 / 10 / 14px** by element size. A mark under 10px keeps a 2px corner so its
silhouette still reads — a 10px radius on an 8px mark is a circle, and the ladder's square-versus-
round distinction is load-bearing.

Elevation is real and is spent in five places, and nowhere else: `--shadow-soft` on a card,
`--shadow-elevated` under a menu or dialog, `--shadow-elev` for the one lifted panel that also
carries an inset highlight, `--shadow-warm` on the brand mark, and `--shadow-focus` — a 3px coral
ring at 18% alpha — on a focused control. The ring is a wash and carries no contrast; the solid
1px coral border underneath it is what distinguishes focused from resting.

Density is roomy: 36px table rows, 36px controls, a 64px header, a 288px sidebar with a
`blur(20px)` vibrancy backdrop, 40px page padding, and a 1280px measure so tables do not run the
full width of a wide monitor. Cards tile with a gutter, never a shared seam. A 40px accent
graticule at 4% (dark) / 8% (light) sits behind each screen — scoped to the screen rather than the
viewport, so it ends where the content ends.

### 3.4 Motion

Four movements only: **150ms** opacity/colour on hover, **200ms** height on disclosure, **90ms**
cross-fade on a live value swap, and a **press-scale** of 0.97 on a control being clicked. No page
transitions, no entrances, no shimmer, no spinners. Loading is a 2px determinate bar in `--trace`
at the top of a card.

### 3.5 Identity mark

A graticule with one branch taken, buildable from rects and lines with no curves and no gradients.

On a 24×24 grid: a 1px hairline square from (2,2) to (22,22) in the Bezel neutral — the
instrument's screen bezel. A 1px vertical rule down the centre at x=12 from y=2 to y=22 — the
spine. Three 1px horizontal stubs enter from the left at y=7, y=12 and y=17. The top and bottom
stubs run from x=2 and stop at x=12. The middle stub crosses the spine, continues to x=19, and
terminates in a filled 4×4 square at (20,12) in the accent. Two hollow 3×3 squares sit on the
spine at (12,7) and (12,17), echoing the ladder's skip mark.

Two colours only: one neutral hairline, one accent pip. At 16px the hairlines merge into a
bracket-and-crosshair silhouette with a single bright dot pushed right — distinctive in a tab
strip and unmistakably not a rounded square with a letter in it. At 512px it is legibly the
routing ladder, so the logo and the product's central component are the same drawing.

Monochrome fallback: the served path becomes a 2px solid rule, the skipped stubs become 1px dashed
rules, the pip stays a filled square. Wordmark is the mark, a 12px gap, then `darkrouter` in Inter
600 lowercase at −0.01em in Ink — never in the accent, because the accent is a reading and a
product name is not a reading.

In the console the mark is presented the way 9router presents its own: centred in a 36px tile at
`--radius`, filled with a coral gradient and carrying `--shadow-warm`, the mark itself drawn in
white. The tile is the adopted treatment; the drawing inside it stays Darkrouter's, because a
routing ladder is the one thing a generic hub glyph cannot say. The spine is drawn in three
segments — a single rule from top to bottom fills the 1px cores of both hollow squares, and the
two skip marks would render solid and read as served.

## 4. The routing ladder

One component, three modes: **retrospective** in a request trace, **predictive** in the Routing
dry-run, **compressed** in the Models catalog. The geometry never changes between them; only fill
and columns change. Learn it once, read it everywhere.

Geometry left to right: a 28px rank gutter, a 1px vertical spine, a 12px stub lane, then the
candidate row — target id, reason, latency.

**Rank gutter.** Two-digit zero-padded ordinals in 11px mono at Legend, right-aligned.
Zero-padding is not decoration: it keeps the column optically stable past nine candidates and
makes the gutter read as a counter rather than a bullet list. Every fifth rank draws a 5px tick
into the spine at Bezel; the others draw 3px — the graticule's tick convention applied to a list.

**The spine.** A continuous 1px vertical rule running the full ladder height. It is the signal
path, and it exists for every row whether or not that row was touched. That is the component's
whole argument: the candidates you skipped are as much a part of the decision as the one that
served.

**The four marks.** A 9×9px cell centred on the spine. Silhouette alone carries meaning, so the
ladder survives greyscale and every colour-vision deficiency; colour is a second channel, never
the only one.

| State | Mark | Stub | Row |
|---|---|---|---|
| Skipped | hollow 7px square, spine passes through | none | id at muted, no background |
| Skipped, cooling | hollow square stroked in cooling amber | none | reason chip carries a live countdown |
| Attempted, failed | filled 7px square in failure red | 12px **dashed** rule, 3px on 3px | latency shows time-to-failure |
| Served | filled 7px square in accent | 12px **solid** rule | 1px accent left border, served wash |

Solid versus dashed carries the entire attempted-versus-failed distinction and stays legible at
50% zoom. Four independent channels separate a skipped row from an attempted one — connector
dashed versus solid, latency bar absent versus present, identifier struck versus intact, row
dimmed versus full — so no single channel is load-bearing.

**The termination rule** is the most important mark in the component. Below the served row the
spine drops from the default hairline to the subtle one, and every remaining rank goes hollow at
5px in the subtle hairline with its text at 45% opacity. The ladder visibly runs out of ink. From
across a room you can see where the request stopped without reading a word.

**Reason column.** Right-aligned, always the same two-part shape: a machine code, a middot, then a
plain sentence. `model_not_offered · target does not serve claude-sonnet-4-6`. `cooling · 12s
remaining`. `no_capability · vision demanded, model has none`. `context · 210k requested, 200k
available`. The code is what you grep for; the sentence is what you read. The fixed rhythm means
an unfamiliar rejection reason still parses, which matters for the long tail an operator has not
memorised.

**Fill versus outline carries actual versus predicted.** In a trace the marks are filled because
the attempts happened. In the Routing dry-run they are outlined because nothing has been sent.
That distinction needs no legend.

**Latency micro-bar.** A 2px bar in the row's right margin, scaled across the ladder's own maximum,
turning the ladder into a waterfall without introducing a second axis.

## 5. Information architecture

Nine destinations: eight in three rail groups, plus Settings pinned at the bottom. A command
palette sits over all of them.

| Group | Destinations | The question |
|---|---|---|
| Operate | Overview, Requests, Usage | Is it working, and what did it just do |
| Configure | Providers, Models, Routing | What can it route to, and how |
| Use | Playground, Connect | Does it work, and how do I point a client at this |

Eight items read as three because the rail groups them, and Settings sits apart because it holds
knobs rather than decisions. Breaker state, discovery health, preset
browsing, aliases and model overrides deliberately do **not** get their own destinations: each is
one panel's worth of content that belongs beside the subject it describes. Giving them pages means
navigating away from context to answer a question about the thing already on screen, and it is how
a console ends up with twenty-three sections of which six are stubs.

Reachability is the palette's job, not the rail's. ⌘K jumps to a provider, a model, a request id
or an alias directly, so nav depth only matters to someone browsing rather than aiming.

Every filtered view is a URL. Filters live in router search params rather than component state, so
a filtered Requests view survives a reload and can be pasted to yourself.

## 6. Screens

### 6.1 Overview

The screen you leave open. A config-invalid banner at the top when it applies, carrying the error
and the reassurance that the previous configuration is still serving. Below it a live strip:
requests per minute, error rate, latency percentiles and spend today, each over a short sparkline
rather than a bare number, because a rate of 12 says nothing about whether it was 40 a minute ago.
Then the provider health grid, with tiles that are links.

Two additions. A **recent-failovers strip** listing the last handful of requests where attempts
exceeded one — a fleet-wide error rate hides one provider quietly degrading, and this is the early
warning that rate cannot give. And an **ops footer** carrying version, uptime and the dropped-log-record
counter from `/healthz`; that counter is the honest signal that usage figures are a lower bound,
and nothing surfaces it today.

### 6.2 Requests

The log on `darkraise-ui/data-table`, which brings sorting, column visibility and CSV export.
Filters become real controls: comboboxes populated from live data for provider, model, alias,
surface and error code, plus a time-range picker, since `since_ms` and `until_ms` are already
accepted by the API and unreachable from the UI.

Rows carry chips that make routing legible without opening anything — `failover ×3`, `passthrough`
versus `translated`, the estimated-token marker. Saved views in local storage for the three that
get used: failovers only, errors today, passthrough misses.

Paging stays keyset per phase 7 §4.2. New rows arriving from the poll queue behind a "3 newer"
pill rather than shifting the scroll position out from under a reader.

### 6.3 Request trace

The ladder retrospectively, per §4, above a small waterfall showing connect, time-to-first-token
and total per attempt, so a three-attempt failover reads as three bars rather than three numbers.
Then tokens, cost, the phase 4 dropped-field warnings, and surface metadata.

The Bodies panel says "not captured" and why, rather than rendering an empty box.

An **Open in playground** button replays the request with its parameters. Portkey and Langfuse
arrived at that interaction independently, which reads as a requirement rather than a flourish.

### 6.4 Usage

A screen that does not exist against an endpoint that already serves 365 days. Range picker;
requests and tokens over time stacked by provider; cost over time. Two ranked tables — by provider
and by model — where clicking a row lands in Requests already filtered. That interaction is what
turns a static chart into an investigation and it costs nothing.

It carries an honest footnote: tokens burned by failed pre-commit attempts never reach
`usage_daily`, so every figure understates reality exactly when failover fires. §8.3 fixes the
underlying gap; until it lands the footnote is the truth.

### 6.5 Providers

Where `PATCH /api/providers/{id}` finally gets a caller: rename, reprioritise, disable without
deleting, set a Bedrock region or a Vertex project.

Each provider opens to credentials with add, remove, enable, disable and replace; an OAuth account
showing expiry, last refresh and account identifier rather than only "needs reconnection"; the
probe with its result; **breaker detail** — cooling until, backoff level, consecutive failures,
which model triples are cooling — with a manual reset that is not a side effect of clicking Test;
and **discovery health**, which today makes a provider whose listing has failed for six hours look
identical to a healthy one.

Adding a provider becomes a real browser over all 197 presets — searchable, filterable by surface,
auth kind and free tier, linking to each provider's website — with a raw form behind it for
providers that are not presets. The API accepts `kind`, `base_url`, `auth_style`, `priority`,
`enabled`, `region`, `project` and `location`; today's two-field form can express none of them.

### 6.6 Models

The catalog with facets rather than two text boxes: surface, capability, context window, price
band, provider, and lifecycle state — the API already returns `live`, `stale` and
`removed_upstream` and the table never renders it. Columns gain pricing, max output tokens,
publisher and merge source, so a number's provenance is visible: models.dev, discovery, inference
or an override.

Opening a model shows every provider serving it in route order — the ladder again, compressed —
and an **override editor**, because `model_overrides` sits at the top of the merge precedence for
capabilities, context window and surfaces and has no writer anywhere in the product.

### 6.7 Routing

The new destination, and the one that earns the spine.

Its centrepiece is **route preview**: type an alias or a model string and see what the router would
do right now against the live snapshot, before sending anything. The resolver is already a pure
function of a frozen snapshot, so this is a thin endpoint over machinery that exists.

Below it, aliases as editable ordered chains with drag-to-reorder and validation for dangling
targets, and the policy knobs — retry attempts, cooldown trip and ceiling, the four timeouts —
with hot-reloadable and restart-only marked distinctly. `policy.timeout.connect` and
`first_byte` configure the one shared transport built at startup and cannot take effect on a
reload; `total` and `idle` are read per request and can. That difference is real and currently
invisible.

### 6.8 Playground

Multi-turn chat with a system prompt, temperature, max tokens and tools; a stream toggle; and a
dialect selector, because the gateway serves three inbound dialects and none of them are testable
from the dashboard. Then compare mode: two models side by side on the same prompt, which the
reference survey identifies as the most-wanted feature in a router playground precisely because
choosing between providers is the operator's recurring decision.

Then the six auxiliary surfaces the gateway serves and the console cannot touch: embeddings with a
vector preview and dimension control, rerank, moderation, images, speech with a player, and
transcription with a file drop. Plus token counting across both count endpoints, showing the
native-versus-local-estimate marker.

Every run links to its trace; every trace can seed a run.

### 6.9 Connect

Base URLs per dialect, copyable. Proxy token management — today a single shared secret in a file
with no rotation and no per-client keys, which means one leaked token cannot be revoked without
reconfiguring every client. Ready-made config snippets for Claude Code, Codex, Cursor, and the
OpenAI and Anthropic SDKs. Which surfaces are live.

### 6.10 Settings

Server knobs, log retention and capture, catalog sync and discovery — including the master switch
for outbound traffic the gateway initiates on the operator's behalf, currently a file edit and a
restart. Account with password change and session revocation. Appearance: mode and density only.
The raw config view with validation status and reload.

### 6.11 Login and first-run

Login gains the identity mark and nothing else; it is already correct. First-run handles two
states the console currently fails at: no providers configured, which should teach rather than
show an empty grid; and **no admin password hash set**, which today closes the dashboard silently
with only a `/healthz` warning, so an operator sees a login screen that refuses every password
with no explanation.

Empty states are first-class throughout. A fresh install with zero requests must not render as
flat rectangles with faint grids, which is indistinguishable from broken equipment. Each empty
well carries a legend explaining what it will show and a dimmed example.

## 7. Upstream — darkraise-ui 6.5.0

Three changes, all in `/root/repositories/darkraise-web-template/packages/ui`.

**Expose the neutral surface ramps.** `graphite` is a complete `ColorScale` at
`src/theme/palettes/surfaceColors.ts:101` — hue 210 at 3–20% saturation, exactly this language's
ground — and is registered in the palette map. It is unreachable only because `src/theme/types.ts:24`
reads `SURFACE_COLORS = ["slate", ...ACCENT_COLORS]`. So are `stone`, `sand`, `olive` and `sepia`:
five neutral ramps built, shipped and impossible to select. Widening that one constant exposes all
five, and collapses Darkrouter's surface work from a twenty-two-token override block per mode into
`surfaceColor: "graphite"`.

Without it, the identity's foundation is roughly forty-four `!important` declarations that a
future library upgrade can silently strip, taking the design with it. That was named as this
direction's fatal flaw and this change is what removes it.

**Repair five light-mode contrast failures.** These are library defects independent of Darkrouter:

| Token | Light value | Measured | Required |
|---|---|---|---|
| `--primary` as a UI mark | sky-500 `#0DA2E7` | 2.74:1 | 3:1 |
| `--focus-ring` | sky-200/300 | 1.28–1.58:1 | 3:1 |
| `--success` | emerald-500 `#10B77F` | 2.48:1 | 3:1 |
| `--warning` | amber-500 `#F59F0A` | 2.03:1 | 3:1 |
| `--destructive` as text | red-500 | fails | 4.5:1 |

`--primary` matters more than it looks: `dist/styles.css:10749` documents that form controls take
their focus indicator from `--primary` rather than `--focus-ring`, so at 2.74:1 every text field,
select and textarea in every consuming app has a sub-3:1 focus ring in light mode. `pickForeground`
enforces only `FOREGROUND_MIN_RATIO = 3`, which is why button labels are not AA-safe at every
accentIntensity — `calm` emits a fill at 4.96:1 against a white label while `balanced` emits
4.37:1 and fails. Darkrouter pins `calm` for that reason, but the floor should be raised at source.

**Extend `DataTable`.** It currently offers sorting, column visibility, CSV export and a
single-column text filter — no faceted filters, no virtualization. A 197-row provider list and a
long request log want both. Adding them here keeps Darkrouter's tables on the house component and
gives every other frontend the same capability.

## 8. Admin API additions

### 8.1 Where aliases and policy live

Making aliases and policy editable forces a decision the current design has never had to make:
`darkrouter.yaml` is hand-edited and authoritative for both.

**Decision: aliases and policy move into SQLite, with the YAML block imported once on first run.**
This is not a new pattern — it is exactly what `providers:` already does, and having two config
concerns follow two different rules would be worse than either rule. Round-tripping YAML would
destroy comments, and an overlay where the file is the base and the database wins would give the
operator two sources of truth for one value and no way to see which is in effect.

The consequences are stated rather than discovered: after first run, editing `aliases:` or
`policy:` in the file has no effect, exactly as editing `providers:` already has none. The config
view must say so at the point of display, the import must log what it took, and `darkrouter.yaml`
needs a comment block recording it. A `GET /api/config` response must distinguish "this value came
from the file" from "this value is live", because today it cannot.

### 8.2 Endpoints

Extended:

- `GET /api/overview` — add latency percentiles, a short series for sparklines, recent failovers.
- `GET /api/usage` — add `group_by=provider|model`; `usage_daily` already carries the detail and
  the endpoint currently aggregates it away before anyone could show it.
- `GET /api/providers` — add discovery health and OAuth account detail per credential.
- `GET /api/config` — mark each value's source and whether it is hot-reloadable.

New:

| Method | Path | Purpose |
|---|---|---|
| POST | `/api/route/preview` | The dry-run ladder against the live snapshot |
| GET | `/api/health/providers` | Breaker detail: cooling until, backoff level, consecutive failures, cooling triples |
| POST | `/api/providers/{id}/breaker/reset` | Clear a cooldown without spending a probe |
| POST | `/api/providers/{id}/discover` | Force a discovery sweep for one provider |
| POST | `/api/catalog/sync` | Force a models.dev sync |
| PATCH | `/api/providers/{id}/keys/{keyId}` | Enable, disable, or replace a credential secret |
| GET/PUT | `/api/aliases` | Alias chains with validation |
| GET/PUT | `/api/policy` | Retry, cooldown, timeouts |
| GET/PUT/DELETE | `/api/models/{provider}/{model}/override` | The `model_overrides` writer that has never existed |
| GET/POST/DELETE | `/api/proxy-tokens` | Per-client proxy credentials replacing the shared secret |
| GET/DELETE | `/api/sessions` | List and revoke admin sessions |
| POST | `/api/auth/password` | Change the admin password |

Every new mutating endpoint inherits phase 7 §3 unchanged: session required, CSRF token bound to
the session by HMAC, `Origin` or `Sec-Fetch-Site` check. No endpoint returns credential material,
per phase 7 §4.1 — `PATCH .../keys/{keyId}` accepts a new secret and never echoes one.

### 8.3 Two backend capabilities, not endpoints

**Compute cost.** The catalog already carries per-MTok input, output and cache-read pricing from
models.dev, and `CostMicros` is never computed for any surface, so the overview spend tile and
every trace render an em-dash. Cost is computed at commit time from the catalog price of the model
that actually served. Per-call image pricing has no catalog field and stays unpriced and marked
so — an honest gap beats a confident zero, which is the rule the current spend tile already
follows.

**Account for failed attempts.** `request_attempts` carries no usage columns, so tokens burned by
failed pre-commit attempts never reach `usage_daily`. Spend understates reality exactly when
failover fires, which is when an operator most wants the number. Adding usage columns to the
attempt row and rolling them up is what lets §6.4 drop its footnote.

### 8.4 Sequencing

The API work gates screens unevenly, so it is ordered by what unblocks the most:

1. Cost computation and usage detail — unblocks Usage (§6.4) and every cost figure elsewhere.
2. Health, discovery and breaker reset — unblocks Providers (§6.5).
3. Route preview — unblocks Routing (§6.7) and the compressed ladder in Models (§6.6).
4. Aliases, policy and overrides — the §8.1 migration, unblocking the rest of Routing and Models.
5. Proxy tokens, sessions, password — unblocks Connect (§6.9) and Settings (§6.10).

Overview, Requests, the trace and the Playground need no *new* endpoints. Overview and the
requests list want the extensions listed at the top of §8.2, but both degrade honestly without
them — a missing sparkline series renders as the bare number it renders today — so all four can be
built against today's API and enriched as the extensions land.

## 9. Frontend architecture

The current shape is five route files, a 484-line `settings.tsx` doing five jobs, and response
types hand-copied from Go structs into whichever file happens to need them — `Trace` in the trace
drawer, `Provider` in settings, neither with any relationship to the Go source it mirrors.

- **Feature folders.** `src/features/<destination>/` owns its route, queries, components and
  types. The 484-line grab bag becomes five files that each do one thing.
- **One API type module** mirroring the json tags in a single place, so a Go field rename has one
  place to land instead of five.
- **Typed query hooks behind a key factory**, replacing inline `useQuery` with raw path strings.
  Poll intervals stay as phase 7 §5 sets them: 3s for the overview and the requests first page,
  30s for catalog and usage, paused when the tab is hidden.
- **Filters in router search params**, not component state.
- **Toasts on mutations.** `Toaster` ships and is unused; errors currently set a string that
  renders in a corner.
- **Theme config drops from seventeen axes to two** — mode and density. This also shrinks the
  forty-line pre-hydration script in `index.html`, which presently hand-mirrors twelve
  axis-to-localStorage mappings and a preset-neutralisation table, and is a standing maintenance
  hazard.
- **Charts** come from `darkraise-ui/components/chart`, a subpath import because recharts is an
  optional peer. The engine's generated ramp **must be overridden**: with a sky accent it emits
  `--chart-4` as orange-400 and `--chart-5` as lime-400, which read as the reserved cooling amber
  and healthy green on the Usage charts specifically. The replacement is a monochrome accent ramp
  differentiated by fill — solid, 60% tint, 45° hatch, dot, outline — scoped to a `.chart-scope`
  class rather than `:root`, since custom properties inherit and a class on an ancestor wins.

## 10. The mockup phase

Implementation does not start until the mockups are approved.

`docs/ux/mockups/` — one annotated fragment per screen plus canonical chrome and a shell,
assembled by `build.py` into a self-contained `index.html` and an `artifact.html`, published as a
Claude artifact. Eighteen screens: the design-language screen; the ten screens of §6.1–§6.10; login and
first-run from §6.11; a ladder specimen showing all three modes and all four row states together;
provider detail, which is substantial enough to stand alone; the preset browser; the compare
playground; and a light proof.

Press `A` for the annotation layer: each pin maps a region to its component, its tokens, its data
source down to the endpoint and field, and its behaviour. Press `T` for light mode — with both
modes first-class this is a real proof, and it is where the well-polarity inversion gets checked,
since light and dark are structurally different screens rather than a palette swap.

`qa.py` gates the set: no raw hex in fragments (tokens and rgba only), no external URLs, pins and
a legend on every screen, no duplicate ids, balanced tags. Fonts are self-hosted, so "no external
URLs" is enforceable rather than aspirational.

## 11. Testing

- `qa.py` passes on the mockup set.
- Go handler tests on every new endpoint: rejection without a session, rejection with a bad CSRF
  token or foreign `Origin`, and no credential material in any response.
- Route preview agrees with what the router actually resolves — the same frozen snapshot through
  both paths must produce the same candidate list, skips and order. This is the test that keeps
  the dry-run honest.
- The ladder renders all four row states, the termination rule, and both fill modes; it is
  asserted legible in greyscale, meaning every state is distinguishable with colour stripped.
- Contrast assertions over the token pairs in both modes, computed rather than eyeballed, since
  that is where five live defects were found.
- Cost computation: a priced model produces a figure, an unpriced one produces null and not zero.
- The §8.1 migration imports aliases and policy once and does not re-import on a later start.
- Phase 7 §8's done criteria still hold.

## 12. Done criteria

- Every screen in §6 renders against a real gateway in both modes, and `qa.py` and the frontend
  build pass.
- A failover request is findable in three clicks and its ladder explains every attempt and every
  skipped candidate, with no colour needed to tell the four states apart.
- Route preview, given an alias, produces the same ordered candidate list the router produces for
  a real request against the same snapshot.
- A provider can be renamed, reprioritised, disabled, probed, its breaker reset, and its discovery
  forced, without touching a file.
- An alias chain can be created, reordered and validated in the browser, and takes effect on the
  next request.
- Spend shows a real number for a priced model, on the overview and in the trace.
- A fresh install with no providers and no password hash explains itself rather than presenting a
  login that refuses every password.
- `darkraise-ui` 6.5.0 ships graphite, the five contrast repairs, and a faceted virtualized
  `DataTable`, and Darkrouter's theme is a config block rather than an `!important` cascade.

## 13. Decomposition

This is too large for one implementation plan and splits into four, in dependency order:

1. **`darkraise-ui` 6.5.0** (§7) — a separate repository, and the only work that blocks on nothing
   else. It can start immediately and must land before the console's theme can stop being an
   `!important` cascade.
2. **The mockup phase** (§10) — depends on §3 and §4 being settled, which they are. It does not
   depend on 6.5.0 shipping: the mockups are authored against the intended tokens, and 6.5.0 is
   what makes those tokens reachable from the app rather than from hand-written CSS.
3. **The API additions** (§8) — sequenced internally by §8.4, and the §8.1 migration is its own
   reviewable change because it alters where two config concerns live.
4. **The console** (§6, §9) — gated on approved mockups. Overview, Requests, the trace and the
   Playground need no new endpoint from step 3 and can proceed in parallel with it, picking up the
   §8.2 extensions as they land; every other screen waits on its slice of §8.4.

Steps 1 and 2 can run concurrently. Step 4 is the only one that must not start early, because its
whole point is that the design was approved before the code was written.
