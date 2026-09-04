# Verification

What each gate proves, and what it does not.

## The gates

| Gate | Command |
|---|---|
| Format | `gofmt -l internal/ cmd/ tools/` |
| Vet | `go vet ./...` |
| Go tests | `go test -race -count=1 ./...` |
| Console tests | `cd web && npm test` |
| Console lint | `cd web && npm run lint` |
| Console types | `cd web && npm run typecheck` |
| Console build | `cd web && npm run build` |

CI runs these in order, adding staticcheck, a vulnerability check and a
dependency audit, then builds and scans the image.

**Always run the repository-wide Go gate, never a narrower one.** A change
once gave a media fetcher an enabled flag, and a struct literal in an
unrelated golden test took the zero value — silently disabling inlining and
making the fixture stop exercising the refusal path it was recorded to cover.
Only the full gate caught it.

## What the suites cover

Unit tests over pure functions and component behaviour: candidate resolution,
outcome classification, the cooldown ladder, dialect translation, ladder row
derivation, usage summarisation, filter parsing.

Golden files pin the translated request and response shapes per dialect. A
differential suite compares the fast path against the translated path for the
same input.

## What they do not

- **Appearance.** No test establishes that a screen looks like its mockup, or
  that light mode is legible.
- **Vendor behaviour.** The Bedrock, Vertex and OAuth paths have never been
  exercised against a real vendor; their field names come from published
  documentation.
- **Load.** The race detector observes the interleavings the tests schedule,
  not the ones production produces.

## The live gate

Some criteria can only be checked against a running gateway with a real
provider credential. That pass needs: the UAT stack up, one real provider
credential, `DARKROUTER_ADMIN_PASSWORD_HASH` set, and someone to drive a
browser in **both** themes.

Last run 2026-09-02 against Groq, with the playground's chat and compare modes
completed end to end.

Two states no automated suite can reach, both needing the same live stack:
pointing a fresh data directory at the gateway to confirm the zero-provider
teaching state renders instead of empty grids, and unsetting the password hash
to confirm the first-run screen still explains itself.

## Writing a test that can fail

A test that passes against deliberately broken code proves nothing. Five such
tests once shipped here.

Before writing an assertion, name the production change that would make it
fail. Then make that change, watch the test go red, and put the code back.
Assert on real behaviour, never on a mock's.

`web/src/lib/api-types.ts` mirrors Go json tags **by hand and is not
enforced**. Its first version had every request-row field wrong and
typechecked cleanly for two commits. Changing a response shape means changing
it there too, and nothing will catch you.
