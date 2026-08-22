# Darkrouter

A self-hosted LLM gateway. One endpoint, many providers, deterministic failover.

## Status

Phases 1–4: three inbound dialects — OpenAI, Anthropic, and Gemini — routed to
any provider kind with deterministic failover, persistence, and health tracking.
See `docs/superpowers/specs/README.md` for the full design and the phase
roadmap.

## Run

```bash
mkdir -p data
cp darkrouter.example.yaml data/darkrouter.yaml
export GROQ_KEY=your-key
docker compose up --build
```

Then:

```bash
curl -N http://localhost:8080/v1/chat/completions \
  -H 'content-type: application/json' \
  -d '{"model":"openai/gpt-oss-120b","stream":true,
       "messages":[{"role":"user","content":"say hi"}]}'
```

The response carries `X-Darkrouter-Provider`, `X-Darkrouter-Model`,
`X-Darkrouter-Attempts`, and `X-Darkrouter-Request` so routing is visible from a
terminal.

## Endpoints

| Route | Dialect |
|---|---|
| `POST /v1/chat/completions` | OpenAI |
| `GET /v1/models` | OpenAI |
| `POST /v1/messages` | Anthropic |
| `POST /v1/messages/count_tokens` | Anthropic |
| `POST /v1beta/models/{model}:generateContent` | Gemini |
| `POST /v1beta/models/{model}:streamGenerateContent` | Gemini |
| `POST /v1beta/models/{model}:countTokens` | Gemini |
| `GET /v1beta/models` | Gemini |

**Any inbound dialect routes to any provider kind.** An Anthropic client can be
served by a Groq model and a Gemini client by an Anthropic one; the response
comes back in the dialect the client speaks, including errors and streamed
events. Point Claude Code at `ANTHROPIC_BASE_URL=http://<host>:<port>` and
Gemini CLI at the same host's `/v1beta`. Each sends its own credential form —
`x-api-key`, `Authorization: Bearer`, `x-goog-api-key`, or `?key=` — and all are
compared against `server.proxy_token`.

**A field the target cannot express is recorded, never dropped silently.** The
warning names the field, the target kind, and the reason, and lands on the
request row. That is how a vanished `cache_control` marker or a dropped
`top_k` becomes visible instead of showing up as a surprise on a bill.

**`X-Darkrouter-Estimated: true`** marks a token count Darkrouter computed
locally rather than forwarding to the provider's own counting endpoint. The
estimate uses a bundled BPE for OpenAI-family models and
characters-divided-by-four otherwise, and it does not count images.

## The model catalog

Darkrouter ships a catalog of provider presets, so adding a known provider is a
name and a key rather than a base URL, an auth style, and a list of quirks.
Three sources merge into one index:

- **Presets** — shipped data: kind, base URL, auth style, surfaces, and known
  quirks per named upstream.
- **models.dev** — pricing, context windows, and capability flags, refreshed
  every twelve hours. A snapshot is embedded in the binary, so Darkrouter starts
  and serves with no outbound access to it.
- **Discovery** — each enabled provider's own model listing, probed every
  fifteen minutes and whenever a provider or credential changes.

A provider that times out does not lose its models: after three consecutive
failed probes they are marked stale and stay routable, because the circuit
breaker rather than the catalog is what avoids a broken provider. A model a
*successful* listing omits three times running is marked removed upstream and
stops being routable.

Both workers are optional. See the `catalog:` block in `darkrouter.example.yaml`.

## Develop

```bash
go test ./...
go vet ./...
go build ./cmd/darkrouter
```

The race detector needs cgo and a C toolchain, which a stock Windows checkout
does not have. Run it in the build image instead:

```bash
docker run --rm -v "$PWD":/src -w /src golang:1.26-alpine \
  sh -c 'apk add --no-cache gcc musl-dev >/dev/null && go test -race ./...'
```
