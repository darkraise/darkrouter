# Playground Review Fixes Design

**Date:** 2026-09-01

**Status:** Approved

## Goal

Resolve every concrete finding from the post-redesign implementation review without adding new subsystems, dependencies, or persistence formats.

## Scope

- Settle displayed reasoning on every terminal request path.
- Make trace-to-playground links select Chat explicitly.
- Prevent conversation selection and delayed-creation races.
- Preserve each playground surface's session state while aborting work that is no longer visible.
- Keep Compare run labels immutable for the duration of a run.
- Account for all attempt cost and distinguish complete, partial, unknown, and known-zero readings.
- Show reasoning tokens in trace details.
- Restore token counting as an Auxiliary tool through the existing endpoint.
- Validate Auxiliary inputs before enabling Run and report clipboard outcomes truthfully.
- Put conversation history in a sheet at narrow widths while retaining the desktop rail.
- Run frontend tests and lint in CI, and refresh operator-facing status documentation.

## Approach

Use the smallest extension of existing component boundaries. `useChatRun` remains the request lifecycle owner. Conversation guards stay in `ChatMode`; no global store or general state machine is introduced. Playground tabs remain mounted with the component library's supported `forceMount` option, and each surface receives an `active` flag so it can abort live network work when hidden.

Cost is derived from existing trace attempts in the browser. The representation carries whether token and price data are present independently, preventing missing prices from silently becoming zero. Token Count reuses `/api/playground/count` and the existing Auxiliary rail/result patterns.

## Constraints

- No new runtime dependencies.
- No database schema or API compatibility break.
- No global client state store.
- No optional roadmap work such as Responses support, encrypted conversation retention, body capture, or observability expansion.
- Every behavioral fix starts with a focused failing test.
- Preserve `providers.png` as an untracked local file.

## Verification

- Focused Vitest tests for each corrected behavior.
- Full `npm test`, `npm run lint`, `npm run typecheck`, and `npm run build` under `web/`.
- `go vet ./...` and `go test -race -count=1 ./...`.
- Documentation records automated evidence honestly and does not claim live-vendor UAT that was not performed.
