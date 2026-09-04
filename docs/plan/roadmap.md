# Roadmap

## Next: catalogue sync, Phase C — non-LLM surfaces

Phases A (multi-source ingestion) and B (price federation, then the free-tier
record) are merged. Phase C is the remaining one and is **not started**.

**The problem.** `tts`, `webSearch`, `webFetch` and `image` are catalogued but
unroutable, and roughly 35 of 62 candidate providers are deferred to this
phase.

**Why it is not a small change.** These surfaces price per query or per
character. The pricing type expresses per-million-tokens only, so the work is
a data-shape change before it is a routing change — which is why it was
separated from the price federation phase rather than folded into it.

**Scope when it starts:** a pricing representation that can express per-call
and per-unit costs; catalogue ingestion for the deferred providers; routing
and surface filtering for the four surfaces; console rendering of a cost that
is not per-token.

## Standing work, unscheduled

Each of these is an entry in [`status.md`](status.md) with a known cause. None
is scheduled; all are worth doing.

- **Give Bedrock and Vertex a discovery path.** Bedrock needs two signed
  control-plane calls rather than one listing GET, and must catalogue
  inference-profile identifiers rather than foundation-model ones — the latter
  are frequently not invocable on demand.
- **Warn on dropped Responses fields**, rather than parsing them away.
- **Repair the dependency-direction violation**, or restate the rule to admit
  it deliberately. Two adapter subpackages import the catalogue.
- **Native token counting for Bedrock and Vertex-Anthropic**, which speak a
  counting dialect the gateway already implements for another kind.
- **A standing dangling-alias warning**, computed against the effective
  provider set rather than the file's.

## Not planned

- Multi-tenancy, per-user accounting, or a second operator identity.
- Latency-based or round-robin routing.
- Stateful Responses support.
- A completion-spending probe fallback.
