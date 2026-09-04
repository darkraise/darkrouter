# Product requirements

## Purpose

Darkrouter is a self-hosted gateway that sits between LLM clients and LLM
providers. A client points at Darkrouter once, using whichever API dialect it
already speaks, and reaches any configured provider through it.

It exists to solve three problems a direct connection cannot:

1. **A provider that fails takes the request down with it.** Darkrouter
   retries across other providers and other credentials, and tells the
   operator what it tried.
2. **Clients are written against one vendor's API.** Darkrouter translates
   between the OpenAI, Anthropic and Gemini dialects, so a client speaking one
   can reach a model served by another.
3. **Spend and failure are invisible.** Darkrouter records every request,
   every attempt within it, and what each cost.

## Who it is for

A single operator running a small fleet for themselves or a small team on
their own hardware. Not a multi-tenant service: there is one operator
identity, one admin console, and no per-user accounting.

## Scope

- Three inbound dialects — OpenAI (chat and responses), Anthropic Messages,
  Gemini `generateContent` — over seven surfaces.
- Five provider kinds — `openaicompat`, `anthropic`, `gemini`, `bedrock`,
  `vertex` — with authentication as a separate, composable dimension.
- Priority-ordered routing with failover across providers, credentials and
  models, governed by a per-target circuit breaker.
- A catalogue of models assembled from shipped presets, an upstream metadata
  index, live discovery against each provider, and a free-tier register.
- An admin console and HTTP API for configuration, observability and testing.

## Non-goals

These are deliberate refusals, not gaps.

- **Not a load balancer.** Routing is priority-ordered and deterministic, not
  round-robin or latency-optimising. An operator's ordering is an instruction.
- **Not multi-tenant.** One operator, one console.
- **No prompt or response transformation** beyond dialect translation. The
  gateway does not inject system prompts, rewrite content, or filter output.
- **No client-side caching of completions.** Provider-side prompt caching is
  passed through and accounted for; Darkrouter stores no answers to reuse.
- **No stateful Responses API.** `previous_response_id`, `conversation` and
  `background` are refused rather than emulated. A confident amnesic answer is
  worse than an explicit error.
- **No `/v1/audio/translations`.** Transcribe and translate separately.

## Success criteria

The gateway is doing its job when all of these hold:

1. A client configured for one vendor reaches a model served by another,
   without the client being modified.
2. A provider outage is survived: the request is served by another candidate
   and the trace names every attempt and every skipped candidate.
3. An operator can answer "what did this cost and which provider served it"
   for any request, from the console, without reading a log file.
4. A credential never appears in any API response, log line, or trace.
5. Configuration changes that can be applied without a restart are, and the
   ones that cannot say so rather than silently doing nothing.
