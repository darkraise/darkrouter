# Phase 13 — §12's done criteria, walked

Assessed 2026-08-26 against `master` plus `feat/operator-console`.

Each criterion is marked by what actually verifies it. **"Tests" means unit
tests over pure functions; it does not mean the screen was looked at.** Nothing
in this table has been rendered against a running gateway, which is what the
Docker-compose UAT is for — so several rows are honestly *unverified* rather
than met.

| # | Criterion | State | What backs it |
|---|---|---|---|
| 1 | Every screen in §6 renders against a real gateway in both modes | **Still unverified** | Phase 14's gap-closure pass (`docs/ux/GAP-CLOSURE-DOD.md`) ran every static gate but could not perform its own UAT step either — no provider credential exists in that environment. So this remains exactly what it was after phase 13: nine-plus destinations exist and build, but none has been rendered against a live gateway, and light mode has not been looked at once |
| 2 | `qa.py` and the frontend build pass | **Partly met** | Frontend build and 56 tests pass; `qa.py` gates the mockups, not the built console |
| 3 | A failover is findable in three clicks, and its ladder explains every attempt and skipped candidate, with no colour needed | **Partly met** | Requests → row → trace is three clicks, and the ladder renders attempts and skips. Colour-independence is a design property of the marks, not something tested |
| 4 | Route preview produces the same ordered candidate list the router produces | **Met** | `TestPreviewAgreesWithTheRouterExactly` compares position by position against `router.Resolve` over the same snapshot; the screen renders the response unsorted |
| 5 | A provider can be renamed, reprioritised, disabled, probed, breaker reset, discovery forced, without touching a file | **Met** | All six are wired; rename and reprioritise were added on this pass, which is what found them missing |
| 6 | An alias chain can be created, reordered and validated in the browser, and takes effect on the next request | **Met** | Create, edit, reorder and remove all save through `PUT /api/aliases`, which validates server-side and republishes so the next request sees it |
| 7 | Spend shows a real number for a priced model, on the overview and in the trace | **Met** | The overview tile reads `today_spend`; the trace shows the request's cost and each attempt's on its ladder row, which is the only place a failover's spend is attributable to a provider |
| 8 | A fresh install with no providers and no password hash explains itself | **Met** | `FirstRun` renders when `/api/auth/status` reports no password; two tests cover both branches |
| 9 | `darkraise-ui` 6.5.0 ships graphite, the five contrast repairs, and a faceted virtualized `DataTable`; the theme is a config block | **Met, with one deviation** | 6.5.0 published. **`sepia`, not `graphite`** — §7 supersedes the graphite ask, since graphite is hue 210 and belongs to the cool language this phase was first drawn in. Four of five contrast repairs landed; the button-label floor was left at 3:1 by explicit decision and is recorded under Known limitations |

## The three gaps this pass found, and closed

Walking the criteria one at a time surfaced three things a green suite had
nothing to say about:

- **Provider rename and reprioritise.** `PATCH /api/providers/{id}` existed and
  no screen called it. A missing form, not a missing capability.
- **Creating an alias chain.** The editor edited what already existed, so an
  operator with no aliases could not make their first one from the console.
- **Cost in the trace.** `cost_micros` is on the request and on every attempt;
  the drawer showed tokens and not money.

All three are now wired. None was hard, and none would have been found by
running the tests — which is the argument for walking the list before UAT
rather than after.

## Still open

**Criterion 1 is the one that matters and is not met.** No screen has been
rendered against a running gateway, in either mode. Everything above is
reasoning about code plus unit tests over pure functions.

## What the tests do and do not establish

Fifty-six frontend tests cover pure functions: ladder row derivation, provider
state, usage summarisation, filter parsing, config field reading, the
information architecture. They caught real defects — a filled mark outside a
trace, an unpriced total rendering as free, a screen that unmounted the whole
console.

They establish nothing about whether the screens look like the approved
mockups, whether light mode is legible, or whether any of it works against a
gateway that is actually routing. Those are UAT's job.
