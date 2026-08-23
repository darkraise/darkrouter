# Darkrouter specs

Read `2026-08-22-darkrouter-design.md` first. It is the master design and the authority on anything
the phase specs leave unstated. Phase specs reference it rather than restating it, and where a phase
spec appears to contradict it, the master design wins and the phase spec is wrong.

All ten documents were revised on 2026-08-22 against `2026-08-22-spec-review-findings.md`, which
records the review findings, the resolutions applied, and the decisions taken. Read it when a design
choice looks arbitrary — it usually says why.

| Phase | Spec | Covers | Depends on |
|---|---|---|---|
| — | `2026-08-22-darkrouter-design.md` | Master design | — |
| — | `2026-08-22-spec-review-findings.md` | Review findings, resolutions, decision log | — |
| 1 | `2026-08-22-darkrouter-phase1-foundation.md` | Repo layout, config, chat IR, SSE contract, OpenAI dialect, `openaicompat` adapter, Docker | — |
| 2 | `2026-08-22-darkrouter-phase2-persistence-health.md` | SQLite, encrypted credentials, request log, circuit breaker, background workers | 1 |
| 3 | `2026-08-22-darkrouter-phase3-routing-failover.md` | Pure router, attempt loop, outcome classification, commit semantics | 2 |
| 4 | `2026-08-22-darkrouter-phase4-dialects.md` | Anthropic and Gemini, in and out, mapping tables, golden-file suite | 3 |
| 5 | `2026-08-22-darkrouter-phase5-auxiliary-surfaces.md` | Embeddings, responses, images, audio, rerank, moderations | 3, soft on 4 and 6 |
| 6 | `2026-08-22-darkrouter-phase6-catalog.md` | Presets, models.dev sync, discovery, merge precedence, capabilities | 3 |
| 7 | `2026-08-22-darkrouter-phase7-admin-ui.md` | Session auth, twenty-one endpoints, four screens plus settings | 6 |
| 8 | `2026-08-22-darkrouter-phase8-signed-oauth-providers.md` | Bedrock, Vertex, OAuth credentials | 3; OAuth flow also needs 7 |
| 9 | `2026-08-22-darkrouter-phase9-passthrough.md` | Passthrough eligibility, rewriting, raw-stream recognizer, differential tests | 3, 4, 6 |

## Ordering

Phases 1, 2, and 3 are strictly sequential — they build the request path, its state, and its decision
logic in that order.

Phases 4, 5, 6, and 8 depend only on 3 and are largely parallel, with two qualifications. Phase 5
works without 4 and 6 but its warning mechanism comes from 4 and its surface metadata from 6, so
running it first means those arrive as preset declarations only. Phase 8's Bedrock and Vertex halves
are parallel with 3, but its OAuth connect flow needs phase 7's session and admin endpoints.

Phase 7 needs 6 for the catalog screen. Phase 9 needs 3 for the exec loop, 4 for the fixtures its
differential suite compares against, and 6 for the preset quirk declarations its eligibility test
reads.

Docker packaging lands in phase 1 rather than at the end, so the gateway is deployable from the first
phase and every later phase is a redeploy rather than a big-bang integration.

## Open decisions

None. The last one — the rerank wire shape, findings ledger §2.3 — was settled in phase 5: exactly
one shipped preset declares a `rerank` surface, `cohere`, and neither Jina nor Voyage is a preset at
all. Cohere v2 is therefore not merely the recommendation but the only shape any shipped provider
serves, at the path its preset declares. No revisit is planned.
