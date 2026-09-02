# Phase 14 — console gap closure, gated

Assessed 2026-08-27 on `feat/console-gap-closure` and statically reverified
2026-09-01 after the Playground review fixes, against the Definition of Done
table in `docs/superpowers/plans/2026-08-27-phase14-console-gap-closure.md`.

**Update 2026-09-02.** The live half this document records as unperformed
was run: the console was exercised against this machine's UAT instance and a
shadow container, with Chat and Compare completed end to end against Groq.
The rows below are left as the static gate found them on 2026-08-27; the
per-row UAT outcomes were not recorded row by row, so "UAT not performed"
here means "not performed by this gate", and `docs/PROGRESS.md` carries the
2026-09-02 result.

**What this document can and cannot say.** Every command half of every row
below was run and its output checked. Every UAT half — D1 through D18a — was
**not performed**, because no provider credential exists in this environment
and none may be invented. `compose.uat.yml` is present and Docker is usable;
what is missing is one real provider API key, `DARKROUTER_ADMIN_PASSWORD_HASH`
set to its hash, and someone to drive a browser in both light and dark mode.
A row marked "Command verified; UAT not performed" means exactly that — not
that the criterion is met, and not that it failed. It means the static gate
passed and the live gate has yet to run at all.

The two states no automated suite can reach are also unperformed: pointing a
fresh data directory at the gateway to confirm the zero-providers teaching
state renders instead of empty grids, and unsetting the password hash to
confirm `FirstRun` still explains itself. Both need the same live stack.

To finish this gate, a human needs: `compose.uat.yml`, one real provider
credential (any of the three dialects), `DARKROUTER_ADMIN_PASSWORD_HASH` set,
a browser, and both themes exercised — light and dark are structurally
different screens here, per §10, not a palette swap, so each needs its own
pass.

## Static verification

- `go vet ./...` — clean.
- `go build ./...` — clean.
- `go test -race -count=1 ./...` — **26 packages ok, 0 failures**, including
  `internal/admin` (462s), `internal/e2e`, `internal/golden`, `internal/server`,
  `internal/store`.
- `cd web && npm test` — **749 passed in 77 files**.
- `cd web && npm run lint` — clean.
- `cd web && npm run typecheck` — clean.
- `cd web && npm run build` — clean, apart from a pre-existing chunk-size
  advisory that predates this branch.

One regression surfaced by the full suite and fixed on this pass: see
Findings, below.

## Results

| # | Criterion | State | What was run or clicked | What was seen |
|---|---|---|---|---|
| D1 | Requests filter on error code, and the row says which path served | Command verified; UAT not performed | `go test ./internal/admin/ -run 'TestFilteringByErrorCode\|TestACursorMintedUnderOneErrorCode\|TestARequestRowNamesTheServingPath' -v` | 3 tests ran, all passed |
| D2 | Playground sends multi-turn messages, temperature, max tokens and tools, and speaks all three dialects | Command verified; UAT not performed | `go test ./internal/admin/ -run 'TestPlaygroundRequestBuild\|TestThePlaygroundSpeaksAnthropic' -v` | 3 tests ran, all passed. UAT half (one prompt through `openai` and `anthropic` both returning completions) not performed |
| D3 | Token count shows native versus estimated | Command verified; UAT not performed | `go test ./internal/admin/ -run 'TestCountRequestBuild\|TestTheCountEndpoint' -v`; `cd web && npm test -- surfaces results` | Backend request/endpoint tests pass; frontend tests verify both native and estimated labels. Live UAT not performed |
| D4 | The six auxiliary surfaces are runnable from the Playground | Command verified; UAT not performed | `go test ./internal/admin/ -run 'TestAuxRequestBuild\|TestTheAuxEndpoint' -v` | 4 tests ran, all passed. UAT half (embeddings vector preview, dropped-file transcription) not performed |
| D5 | Discovery health is visible per provider | Command verified; UAT not performed | `go test ./internal/admin/ -run 'TestDiscoveryHealthRollsUp\|TestDiscoveryHealth' -v` | 3 tests ran, all passed. UAT half (a degraded discovery line for `missing_streak > 0`) not performed |
| D6 | OAuth credential detail — kind, expiry, scope — is shown | Command verified (corrected pattern); UAT not performed | Plan's original pattern `'TestACredentialViewCarriesOAuth\|TestLeak'` ran **1** test — `TestLeak` matches nothing. Corrected pattern `go test ./internal/admin/ -run 'TestACredentialViewCarriesOAuth\|TestAStaticKeyOmits\|TestNoCredentialMaterial\|TestNoEndpointReturnsCredentialMaterial' -v` | Original: 1 test, half-vacuous (see Findings). Corrected: 6 tests ran, all passed. UAT half (an oauth credential row showing its expiry date) not performed |
| D7 | Catalog rows carry pricing, publisher and provenance | Command verified | `go test ./internal/admin/ -run 'TestAModelViewCarriesPricing\|TestAModelViewNamesItsSource' -v` | 2 tests ran, all passed. This row has no UAT half in the plan |
| D8 | Media inlining has a config switch | Command verified; UAT not performed | `go test ./internal/config/ ./internal/adapter/gemini/ -run 'TestMediaInline\|TestADisabledFetcher' -v` | 4 tests ran, all passed. UAT half (switch off ⇒ a Gemini image-URL request warns and drops the block) not performed |
| D9 | Preset browser adds a provider and a credential without touching a file | Command verified; UAT not performed | `cd web && npm test -- add-provider` (covered within the full 749-test run; not isolated separately) | `add-provider-dialog.test.tsx` passes as part of the full suite. UAT full flow (browse → filter by surface → create → add credential → probe ok) not performed |
| D10 | Model overrides are editable; facets and provenance columns render | Command verified; UAT not performed | `cd web && npm test -- models override-editor` | `override-editor.test.tsx` and `models-screen.test.*` pass as part of the full suite. UAT (override written, catalog reflects it, price/publisher/source columns render) not performed |
| D11 | Policy is editable with hot versus restart marked; alias chains drag-reorder with browser validation | Command verified; UAT not performed | `cd web && npm test -- routing policy-editor` | `policy-editor.test.tsx`, `alias-editor.test.tsx`, `routing-screen.test.ts` pass as part of the full suite. UAT (hot `total` change takes effect after save, `connect` refused with an explanation) not performed |
| D12 | Requests screen: DataTable sorting, column visibility and CSV, real filter controls, time range, saved views, newer pill | Command verified; UAT not performed | `cd web && npm test -- requests saved-views search-filters` | `requests-table.test.tsx`, `saved-views.test.ts`, `search-filters.test.ts` pass as part of the full suite. UAT (each interaction exercised once) not performed |
| D13 | Overview: config banner, sparklines, recent-failovers strip, ops footer | Command verified; UAT not performed | `cd web && npm test -- overview ops-footer failovers` | `overview-screen.test.tsx` and `flow-graph.test.tsx` pass as part of the full suite; `ops-footer` and `failovers` are not separate test files, so those two filter terms select nothing on their own — see Findings. UAT (footer showing version, uptime, dropped-record counter) not performed |
| D14 | Trace: waterfall, a Bodies panel that explains itself, surface metadata, Open-in-playground round-trips | Command verified; UAT not performed | `cd web && npm test -- trace-drawer` | Tests cover explicit Chat round-tripping and reasoning-token detail. UAT (playground run → trace → seed back into playground) not performed |
| D15 | Usage: time series stacked by provider, range picker, cost line, row click-through | Command verified; UAT not performed | `cd web && npm test -- usage` | `usage-screen.test.ts` passes as part of the full suite. UAT (clicking a provider row lands in Requests already filtered) not performed |
| D16 | Connect: copyable base URLs, client snippets, live surfaces | Command verified; UAT not performed | `cd web && npm test -- connect snippets` | `connect-screen.test.ts` and `snippets.test.ts` pass as part of the full suite. UAT (copy button writes the clipboard, snippet matches served routes) not performed |
| D17 | Settings: password change, reload and sync work; file-owned blocks stay read-only with source labels | Command verified; UAT not performed | `cd web && npm test -- settings` | `settings-screen.test.tsx` and `account-card.test.tsx` pass as part of the full suite. UAT (password change revokes other sessions, invalid YAML reload reports invalid) not performed |
| D18 | Login shows the identity mark; a fresh install with zero providers teaches rather than showing empty grids | Command verified; UAT not performed | `cd web && npm test -- identity-mark first-run` | `identity-mark.test.tsx` and `first-run.test.tsx` pass as part of the full suite. UAT (brand-new data directory explains itself, empty screens carry legends) not performed |
| D18a | The ladder's four states are distinguishable with colour stripped | Command verified; UAT not performed | `cd web && npm test -- ladder` | `ladder.test.tsx` passes as part of the full suite. UAT (view a failover trace with the browser forced to greyscale) not performed |
| D19 | All of §12's phase-10 criteria still hold | Re-walked, no regressions found | Re-read `docs/ux/DONE-CRITERIA.md` row by row against the current tree | Rows 2–9 unchanged and still backed by what they cite. Row 1 ("every screen renders against a real gateway in both modes") was already Unverified before this plan and remains so — this plan did not add a live-gateway pass, so it is not newly verifiable either. See the update to that document |
| D20 | Suites green | **Met** | `go vet ./... && go build ./...`; `go test -race -count=1 ./...`; `cd web && npm test && npm run lint && npm run typecheck && npm run build` | All clean. See Static verification above. This row carries no UAT half in the plan |

## Deviations

Made deliberately during the plan; recorded here per Task 23's instruction
that a silent deviation is the failure mode to avoid.

- **The Requests trace opens from a column button, not a row click.**
  `DataTable` exposes no row-click prop.
- **The trace waterfall is request-level, not per-attempt.** No connect
  timing is recorded and `TraceAttempt` carries no TTFT.
- **The latency tile has no sparkline.** `UsageRow` carries no per-day
  latency.
- **The usage range picker's widest option is labelled `365d`, not "All".**
  The endpoint serves 365 days.
- **Playground seeding carries the model and dialect but not the prompt.**
  `capture.bodies` has no writer.
- **Tools are refused on the Gemini dialect.** `functionDeclarations` is a
  different shape from the OpenAI tools box.
- **A Usage click-through from a 30/90/365-day range lands on Requests with
  no time pill lit**, because Requests offers only 1h/24h/7d. No pill is a
  "no answer" state; lighting any of the three alternatives would light a
  wrong one.

## Findings

**A regression the whole-suite gate caught that no per-task gate could.**
The `media.inline` work gave `gemini.Fetcher` an `Inline bool`, and
`internal/golden/golden_test.go` builds its `offlineFetcher` as a struct
literal, so it took the zero value `false` — silently disabling media
inlining and making the fixture stop exercising the SSRF-refusal path its
recorded warning describes. `TestGoldenRequests/openai/multipart-two-images`
failed under the full suite. No individual task caught it: the media task's
own gate named only four packages, while the plan's Global Constraints say
the gate is `go test -race -count=1 ./internal/...`. Fixed in commit
`4aa7d53` (`fix(golden): set Inline on offlineFetcher literal`), by setting
`Inline: true` on the literal. This is exactly the kind of thing the
whole-suite gate exists to catch, and exactly why Task 23 does not accept a
task's own narrower gate as sufficient.

**D6's own verification command was half-vacuous.** The plan's D6 row read
`go test ./internal/admin/ -run 'TestACredentialViewCarriesOAuth|TestLeak'`.
`TestLeak` matches no function in the tree — the real names are
`TestNoCredentialMaterialInAnyResponse` and its siblings in
`leak_strategies_test.go`, plus `TestNoEndpointReturnsCredentialMaterial` in
`leak_test.go`. So the row's own gate ran only the OAuth-metadata half of its
criterion and asserted the credential-leak half against zero tests — the same
"green because nothing ran" failure D6 itself exists to guard against. The
plan's DoD table has been corrected to:

```
go test ./internal/admin/ -run 'TestACredentialViewCarriesOAuth|TestAStaticKeyOmits|TestNoCredentialMaterial|TestNoEndpointReturnsCredentialMaterial'
```

which runs 6 tests, all passing (verified above). A gate that silently
repairs its own misses is not a gate — this is recorded rather than quietly
fixed and forgotten.

**`store.RequestRecord.ResolvedAlias` has no production writer.** It is a
field on every request row and trace, read in several places — the Requests
alias filter and column, Usage's alias dimension, Overview's routing flow
graph, and the Playground's `seedFromTrace` alias branch — but the only
assignment anywhere in the tree is in `internal/store/testing.go`, a test
fixture (confirmed by search: every other occurrence of `ResolvedAlias:` is
in a `_test.go` file). The production handler path that builds a
`RequestRecord` and calls `insertOne` never sets it. Consequences:

- The Requests screen's alias filter and column are always empty against a
  real gateway.
- Usage's alias dimension rolls every request up under an empty key.
- Overview's failover labels always fall back to the final model, never the
  alias that was requested.
- The routing flow graph's left-hand column, which reads this same
  dimension, is affected the same way.
- The Playground's `seedFromTrace` alias branch is inert — there is never a
  resolved alias on a trace to seed from.

This is a pre-existing gateway gap. It predates this plan, is outside this
plan's scope, and none of the tests above would have caught it because they
all seed `ResolvedAlias` through the same test fixture that masks the gap.

**Closed since.** `exec.resolve` now records the alias from the same snapshot
the router resolves against, via `router.MatchedAlias`, covered by
`TestMatchedAliasNamesTheAliasARequestCameInUnder`.
Recorded here for follow-up, not fixed on this pass.

**Closed since.** `web/eslint.config.js` now backs the package's lint script,
the full lint passes, and CI runs it before the production build.
