# Playground Review Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Correct all concrete implementation-review findings from the Playground redesign with focused, conventional changes.

**Architecture:** Extend the existing local component and hook boundaries rather than adding a global store or lifecycle framework. Preserve inactive surfaces with `forceMount`, use explicit `active` props for cancellation, and derive accurate usage readings from the trace data already returned by the API.

**Tech Stack:** React 19, TypeScript, Vitest, Testing Library, darkraise-ui 6.5, Go, GitHub Actions

**Spec:** `docs/superpowers/specs/2026-09-01-playground-review-fixes.md`

## Global Constraints

- No new runtime dependencies.
- No database schema or API compatibility break.
- No global client state store.
- Every behavioral fix starts with a focused failing test.
- Do not stage or commit `providers.png`.

---

### Task 1: Request lifecycle and trace navigation

**Files:**
- Modify: `web/src/features/playground/lib/use-chat-run.ts`
- Test: `web/src/features/playground/lib/use-chat-run.test.tsx`
- Modify: `web/src/features/requests/trace-drawer.tsx`
- Test: `web/src/features/requests/trace-drawer.test.tsx`

**Interfaces:**
- Produces: terminal reasoning always has a numeric `TurnThinking.ms`.
- Produces: Open in Playground search state includes `mode: "chat"` and the existing seed.

- [ ] **Step 1: Write failing lifecycle tests** for unary reasoning, stream error after reasoning, and abort after reasoning; assert each final `ms` is numeric.
- [ ] **Step 2: Run the focused hook tests** with `npm test -- use-chat-run` and confirm failures are caused by unsettled reasoning.
- [ ] **Step 3: Move reasoning settlement into the terminal cleanup path** so success, error, and abort share the same minimal close operation; mark unary reasoning as seen.
- [ ] **Step 4: Run the focused hook tests** and confirm they pass.
- [ ] **Step 5: Write a failing trace-drawer navigation test** asserting `{ mode: "chat", seed: <request id> }`.
- [ ] **Step 6: Run the trace test, add the explicit mode, and rerun it green.**

### Task 2: Conversation race guards and responsive history

**Files:**
- Modify: `web/src/features/playground/chat/chat-mode.tsx`
- Test: `web/src/features/playground/chat/chat-mode.test.tsx`
- Modify: `web/src/features/playground/chat/history-rail.tsx`
- Test: `web/src/features/playground/chat/history-rail.test.tsx`

**Interfaces:**
- Produces: a selected conversation cannot send until its detail is loaded.
- Produces: an older create response cannot replace the conversation selected after that request began.
- Produces: desktop history remains a resizable rail and narrow history opens in a sheet.

- [ ] **Step 1: Add a failing selection-race test** that delays detail loading and proves the composer is disabled until the selected transcript replaces the old one.
- [ ] **Step 2: Run the focused test, then minimally derive and pass a loading guard to the composer; rerun green.**
- [ ] **Step 3: Add a failing delayed-create test** that selects another conversation before create resolves and asserts the late result does not steal focus.
- [ ] **Step 4: Run it red, add a monotonically increasing selection generation guard, and rerun green.**
- [ ] **Step 5: Add a failing responsive-history test** for a narrow-history trigger and sheet content.
- [ ] **Step 6: Reuse the existing darkraise-ui Sheet pattern for mobile while retaining the desktop resizable panel; rerun focused tests green.**

### Task 3: Preserve surface state and keep Compare labels stable

**Files:**
- Modify: `web/src/features/playground/playground-screen.tsx`
- Test: `web/src/features/playground/playground-screen.test.tsx`
- Modify: `web/src/features/playground/chat/chat-mode.tsx`
- Modify: `web/src/features/playground/compare.tsx`
- Modify: `web/src/features/playground/compare-column.tsx`
- Test: `web/src/features/playground/compare-abort.test.tsx`
- Modify: `web/src/features/playground/aux/aux-mode.tsx`

**Interfaces:**
- Consumes: darkraise-ui `TabsContent.forceMount`.
- Produces: `active: boolean` surface props that abort live work on a true-to-false transition without clearing local state.
- Produces: Compare model, prompt, column count, and request settings cannot change while streaming.

- [ ] **Step 1: Add a failing screen test** that enters state on each surface, changes tabs, and observes the state when returning.
- [ ] **Step 2: Run it red, apply `forceMount` to all three panels, and rerun green.**
- [ ] **Step 3: Add failing active-transition tests** proving Chat, Compare, and Auxiliary abort in-flight requests when hidden.
- [ ] **Step 4: Pass `active` to the surfaces and add narrow effects that call their existing abort operations; rerun green.**
- [ ] **Step 5: Add failing Compare tests** that attempt to change a model, prompt, column count, and shared configuration while running.
- [ ] **Step 6: Disable those existing controls while busy; preserve the model snapshot used for each column result; rerun green.**

### Task 4: Accurate usage and trace detail

**Files:**
- Modify: `web/src/features/playground/message.tsx`
- Test: `web/src/features/playground/message.test.tsx`
- Modify: `web/src/features/playground/token-panel.tsx`
- Test: `web/src/features/playground/token-panel.test.ts`
- Modify: `web/src/features/requests/trace-drawer.tsx`
- Test: `web/src/features/requests/trace-drawer.test.tsx`

**Interfaces:**
- Produces: `TurnRoute` carries summed attempt cost plus independent price-coverage state.
- Produces: conversation consumption distinguishes token coverage from price coverage and renders known zero as a value.
- Produces: trace usage renders reasoning tokens as a subset of output tokens.

- [ ] **Step 1: Add failing route tests** with a failed priced attempt plus a served priced attempt and with missing attempt prices.
- [ ] **Step 2: Run red, sum `attempts[].cost_micros`, and retain whether all contributing attempts were priced; rerun green.**
- [ ] **Step 3: Add failing consumption tests** for all-priced, partially priced, unpriced, and known-zero cases.
- [ ] **Step 4: Separate counted-token and priced-turn coverage in `Consumption`, render cost zero as `$0`, and show an explicit partial/unknown price caveat; rerun green.**
- [ ] **Step 5: Add a failing trace test for `reasoning_tokens`, render it under output tokens as “of which reasoning,” and rerun green.**

### Task 5: Auxiliary validation, Token Count, and clipboard feedback

**Files:**
- Modify: `web/src/features/playground/aux/surfaces.ts`
- Test: `web/src/features/playground/aux/surfaces.test.ts`
- Modify: `web/src/features/playground/aux/tool-inputs.tsx`
- Modify: `web/src/features/playground/aux/aux-mode.tsx`
- Test: `web/src/features/playground/aux/results.test.tsx`
- Modify: `web/src/features/playground/aux/results.tsx`
- Modify: `web/src/lib/api-types.ts`

**Interfaces:**
- Produces: a Token Count surface using the existing `POST /api/playground/count` contract and `CountResult` type.
- Produces: surface-specific readiness requires a model and required primary input.
- Produces: copy feedback only reports success after `navigator.clipboard.writeText` resolves.

- [ ] **Step 1: Add failing surface/readiness tests** for missing model and required text/file inputs.
- [ ] **Step 2: Run red, centralize only the existing surfaces' minimal readiness predicates, and rerun green.**
- [ ] **Step 3: Add failing Token Count tests** for request mapping and native-versus-estimated result rendering.
- [ ] **Step 4: Add Token Count to the rail, inputs, request dispatcher, and results by reusing the existing endpoint/type; rerun green.**
- [ ] **Step 5: Add failing clipboard tests** for unavailable, rejected, and successful writes.
- [ ] **Step 6: Await the write and set success/error feedback from the actual outcome; rerun green.**

### Task 6: CI, documentation, verification, and commits

**Files:**
- Modify: `.github/workflows/ci.yml`
- Modify: `README.md`
- Modify: `docs/PROGRESS.md`
- Modify: `docs/ux/GAP-CLOSURE-DOD.md`

**Interfaces:**
- Produces: pull requests run frontend unit tests, lint, type checking/build, Go vet, and race tests.
- Produces: documentation matches the implemented surfaces and records verification without claiming unperformed UAT.

- [ ] **Step 1: Add `npm test` and `npm run lint` to the existing SPA CI step** before the build.
- [ ] **Step 2: Update README feature descriptions and screen inventory** to match the current console and editable config behavior.
- [ ] **Step 3: Update progress and DoD evidence** with current automated counts/results and retain explicit live-UAT limitations.
- [ ] **Step 4: Run `npm test`, `npm run lint`, `npm run typecheck`, and `npm run build` in `web/`.**
- [ ] **Step 5: Run `go vet ./...` and `go test -race -count=1 ./...`.**
- [ ] **Step 6: Run `git diff --check`, inspect the scoped diff, and create small conventional commits that exclude `providers.png`.**
