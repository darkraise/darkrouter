# Darkrouter specs

Read `2026-08-22-darkrouter-design.md` first. It is the master design and the authority on anything
the phase specs leave unstated. Phase specs reference it rather than restating it, and where a phase
spec appears to contradict it, the master design wins and the phase spec is wrong.

| Phase | Spec | Covers | Depends on |
|---|---|---|---|
| — | `2026-08-22-darkrouter-design.md` | Master design | — |
| 1 | `2026-08-22-darkrouter-phase1-foundation.md` | Repo layout, config, chat IR, OpenAI dialect, `openaicompat` adapter, Docker | — |
| 2 | `2026-08-22-darkrouter-phase2-persistence-health.md` | SQLite, encrypted keys, request log, circuit breaker, background workers | 1 |
| 3 | `2026-08-22-darkrouter-phase3-routing-failover.md` | Pure router, attempt loop, outcome classification, streaming commit | 2 |
| 4 | `2026-08-22-darkrouter-phase4-dialects.md` | Anthropic and Gemini, in and out, golden-file suite | 3 |
| 5 | `2026-08-22-darkrouter-phase5-auxiliary-surfaces.md` | Embeddings, responses, images, audio, rerank, moderations | 3 |
| 6 | `2026-08-22-darkrouter-phase6-catalog.md` | Presets, models.dev sync, live discovery, merge precedence | 3 |
| 7 | `2026-08-22-darkrouter-phase7-admin-ui.md` | Session auth, eighteen endpoints, four screens plus settings | 6 |
| 8 | `2026-08-22-darkrouter-phase8-signed-oauth-providers.md` | Bedrock, Vertex, OAuth subscriptions | 3 |
| 9 | `2026-08-22-darkrouter-phase9-passthrough.md` | Passthrough eligibility, body rewriting, usage tee, differential tests | 4 |

## Ordering

Phases 1, 2, and 3 are strictly sequential — they build the request path, its state, and its decision
logic in that order.

Phases 4, 5, 6, and 8 depend only on 3, so they are genuinely parallel and can be reordered by
whatever you want working soonest. Phase 7 needs 6 for the catalog screen. Phase 9 needs 4, because
its differential tests validate the fast path against the IR path.

Docker packaging lands in phase 1 rather than at the end, so the gateway is deployable from the first
phase and every later phase is a redeploy rather than a big-bang integration.
