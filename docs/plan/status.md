# Status

**As of 2026-09-04**, against `master`.

## What is built

Every requirement in `requirements/02-functional.md` and
`requirements/03-non-functional.md` is met except where listed under *Open* or
*Unverified* below.

The gateway serves three inbound dialects over four dialect implementations
and seven surfaces, routes across five provider kinds with failover and a
circuit breaker, assembles its catalogue from five inputs, and ships an
operator console of nine destinations behind a password.

Delivery ran as fourteen numbered phases followed by three catalogue-sync
phases. Phases A, B1 and B2 of the catalogue sync are merged; **Phase C is not
started**.

## Verification

| Gate | State |
|---|---|
| `gofmt`, `go vet` | Clean |
| `go test -race -count=1 ./...` | 30 packages, no failures |
| Console tests | 972 tests in 98 files |
| Console lint, typecheck, build | Clean |
| Live gate against a real provider | Last run 2026-09-02 against Groq, through the UAT stack |

Suites cover behaviour, not appearance. They establish nothing about whether a
screen looks like its mockup.

## Unverified

- **FR-CON-7, light mode.** The console has been walked against a running
  gateway, but a recorded pass through every screen in *light* mode has not
  been made.
- **FR-CRD-4, signed and subscription credentials.** Nothing in the Bedrock,
  Vertex or OAuth paths has ever been exercised against a real vendor. The
  Converse and `rawPredict` field names come from published documentation.
  Testing needs an AWS, GCP or Claude-subscription credential that does not
  exist in this environment.
- **Cross-kind failover.** Passthrough to one candidate followed by a
  translated attempt against a candidate of a different kind has been
  exercised against fakes only.

## Open

Carried forward, each verified as still true on 2026-09-04.

**Routing and translation**

1. A refusal reaches the client as a hard error. A blocked Gemini prompt is
   HTTP 200 natively and 400 through the translated path, because a content
   filter is classified fatal. The fast path forwards the provider's own 200,
   so the two paths disagree on purpose — but a client renders it as failure.
2. Four Responses fields the IR does not model — `truncation`, `include`,
   `max_tool_calls`, `prompt_cache_key` — are parsed away without a warning.
   Two of them change the answer's shape.
3. The translated path emits a usage chunk the client did not ask for.
4. A post-commit scanner overflow forwards the un-stripped usage chunk, making
   a fourth body mutation reachable where three are permitted.

**Catalogue and providers**

5. Bedrock and Vertex have no discovery; both return "not discoverable".
6. The probe's one-token completion fallback is unimplemented — it returns an
   explanatory error rather than spending money.
7. There is no per-provider discovery interval; every enabled provider is
   probed on every tick.
8. Only one OAuth preset is transcribed. Thirteen upstream entries were
   dropped because none carried a complete authorization block, and shipping
   an incomplete one shows a provider that cannot be connected.
9. Per-call image pricing has no catalogue source, so an image call records no
   cost at all.
10. The embedded metadata snapshot ages; a long-lived offline install runs on
    it indefinitely.

**Accounting and health**

11. A post-commit client cancel under-reports the provider's partial bill,
    because the token fields are non-nullable and cost is priced from them.
    Known and accepted — **do not re-litigate**.
12. The overview's cooling tile counts credential-level cooldowns only, so a
    provider cooling at the triple level does not show as cooling.
13. A dangling alias created by deleting a provider in the console is reported
    once and then invisible, because the warning is computed at parse time
    against the file's provider block, which is ignored once providers are in
    the database.

**Structure**

14. `internal/adapter/openaicompat/quirks.go` and
    `internal/adapter/bedrock/discover.go` import `internal/catalog`, violating
    the downward dependency rule stated in `design/architecture.md`.
15. Native token counting is unreachable for Bedrock and Vertex-published
    Anthropic models: the lookup matches on kind, and neither kind implements
    the counting interface. Such a model is always estimated locally, and the
    estimator's prefix tables cover only OpenAI families.
16. Bedrock has no streaming golden fixture, only a recorded exemption.

**Deferred by choice**

17. A duplicated body helper, waiting for a third caller.
18. A stream guard left uncovered rather than pinned by a test written to fit
    it.
19. Four surviving lint warnings: three deliberate dependency omissions in
    effects whose re-firing is the point, and one stale suppression.

## Recently closed

Fixed 2026-09-04: the Anthropic beta list leaking to non-Anthropic upstreams;
`/v1/messages` answering 400 rather than 413 on an oversized body; Gemini
response media typed as image regardless of MIME type; a Gemini `fileUri`
carried as a public URL; the discovery sweeper's timeout and concurrency
accepted on reload but silently ignored; Bedrock ignoring the catalogue's
thinking traits; and a drifted duplicate of the Gemini budget bands.
