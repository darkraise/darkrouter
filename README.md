# Darkrouter

A self-hosted LLM gateway. One endpoint, many providers, deterministic failover.

## Status

The gateway and operator console are implemented. Three inbound dialects —
OpenAI, Anthropic, and Gemini — route to any provider kind, including
SigV4-signed Bedrock, service-account-backed Vertex, and OAuth-backed
Anthropic subscriptions, with
deterministic failover, persistence, health tracking, an admin dashboard, and
a catalog that merges shipped presets, models.dev metadata, and live
discovery. A request whose dialect already matches the chosen provider's wire
format takes a fast path that forwards the body rather than re-rendering it —
see "The fast path" below.

**[`docs/`](docs/) is the documentation set**, and
[`docs/README.md`](docs/README.md) states its reading order: what the gateway
must do (`requirements/`), how it does it (`design/`), what is built and why it
was decided that way (`plan/`), and how it is run (`operations/`).

## Run

```bash
mkdir -p data
cp darkrouter.example.yaml data/darkrouter.yaml
export DARKROUTER_MASTER_KEY="$(openssl rand -base64 32)"   # keep it: it unlocks stored credentials
export GROQ_KEY=your-key
docker compose up --build
```

The master key encrypts every credential stored in `data/darkrouter.db`; the
process refuses to start without one, and a database opened under a different
key cannot read what it holds. For a real deployment put it in `.env` and keep
a copy off the host.

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

## Deploy

CI gates every push with `gofmt`, `go vet`, `staticcheck`, `govulncheck`,
`npm audit`, the console's tests, lint and build, then `go test -race`. It
publishes a multi-tag image with an SBOM, build provenance and a vulnerability
scan of the pushed digest.

**[`docs/operations/deploy.md`](docs/operations/deploy.md)** is the procedure:
running the published image, building locally for UAT, verifying a deploy took,
backup and restore, rolling back, exposing it to the internet, reaching a model
runtime on the host, and the `hash-password` and `rotate-key` subcommands.

## Endpoints

| Route | Dialect |
|---|---|
| `POST /v1/chat/completions` | OpenAI |
| `POST /v1/responses` | OpenAI |
| `POST /v1/embeddings` | OpenAI |
| `POST /v1/images/generations` | OpenAI |
| `POST /v1/audio/speech` | OpenAI |
| `POST /v1/audio/transcriptions` | OpenAI |
| `POST /v1/rerank` | OpenAI |
| `POST /v1/moderations` | OpenAI |
| `GET /v1/models` | OpenAI |
| `POST /v1/messages` | Anthropic |
| `POST /v1/messages/count_tokens` | Anthropic |
| `POST /v1beta/models/{model}:generateContent` | Gemini |
| `POST /v1beta/models/{model}:streamGenerateContent` | Gemini |
| `POST /v1beta/models/{model}:countTokens` | Gemini |
| `GET /v1beta/models` | Gemini |

### The auxiliary surfaces

Beyond chat, Darkrouter serves six more surfaces. Each routes through the same
attempt loop as chat, so each inherits failover, the budget gate, credential
rotation and the request log.

| Route | Surface | Notes |
|---|---|---|
| `POST /v1/embeddings` | `embedding` | Batched input; `float` or `base64`; optional `dimensions`. Pre-tokenized input is forwarded as token ids rather than rendered back to text. |
| `POST /v1/responses` | `llm` | Stateless only. `previous_response_id`, `conversation` and `background` are refused, not degraded. |
| `POST /v1/images/generations` | `image` | URLs or base64. `gpt-image-1` reports usage; the dall-e models report none, and their cost stays unrecorded rather than recorded as zero. |
| `POST /v1/audio/speech` | `tts` | Binary or SSE, streamed through and never stored. |
| `POST /v1/audio/transcriptions` | `stt` | Multipart in; JSON, plain text or SSE out, chosen by the response `Content-Type`. |
| `POST /v1/rerank` | `rerank` | Cohere v2 schema, at the path the provider's preset declares. |
| `POST /v1/moderations` | `moderation` | Category flags forwarded whole, including categories added after this was written. |

**These routes speak the OpenAI dialect only.** The Anthropic and Gemini inbound
dialects serve chat and token counting; neither vendor defines a rerank or
moderations endpoint. A client speaking those dialects reaches the auxiliary
surfaces by calling the OpenAI routes with its existing token, which
`server.proxy_token` accepts in any of the three credential forms.

**`/v1/audio/translations` is permanently absent.** Whisper clients do call it.
Darkrouter does not serve it and will not; transcribe and translate separately.

### Embedding aliases must point at the same model

An alias exists so a request can fail over. For chat that is unambiguously
good. For embeddings it is a hazard: two models produce vectors in different
vector spaces, so a failover to a different model returns vectors that are not
comparable to the ones already in your index — and **nothing in the response
says so**. The call succeeds, the vector looks fine, and similarity search
quietly degrades.

So an embedding alias should list the *same* model across *different*
providers, never different models:

```yaml
aliases:
  # Good: one model, two providers. A failover is invisible and harmless.
  embed:
    - openai/text-embedding-3-small
    - azure-openai/text-embedding-3-small
```

An alias entry is a `provider/model` string, which is the shape
`darkrouter.example.yaml` already uses for its chat aliases.

Darkrouter permits the other arrangement rather than refusing it — refusing
would make an alias useless the moment its first provider rate-limits — but it
records a warning on the request row naming both models, and always sets
`X-Darkrouter-Model` to the model that actually served. Check that header if
you index across a failover.

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

## The fast path

When a client's dialect already matches the provider's wire format — Claude Code
against Anthropic, an OpenAI client against Groq — Darkrouter forwards the
request rather than translating it. The model name is rewritten, the credential
is swapped, and everything else the client sent reaches the provider unchanged,
including parameters Darkrouter has never heard of.

Eligibility is decided per attempt, so a request that forwards to its first
provider still translates correctly when it fails over to a different kind. The
trace drawer's Path column says which happened.

Bedrock and Vertex never take the fast path: Bedrock signs a hash of the request
body, and Vertex encodes the model in its URL alongside the publisher.

## The dashboard

The admin port serves an operator dashboard at `/`, and the REST API it runs on
at `/api/*`. Both require a session; `/healthz`, `/readyz` and `/metrics` do
not, so an orchestrator and a Prometheus scrape keep working.
[`docs/design/admin-api.md`](docs/design/admin-api.md) lists every route under
`/api` with its request and response shape: providers and their credentials, model overrides and
discovery, aliases and policy, route preview, requests and traces, usage,
sessions and proxy tokens, the playground, OAuth connection, and the
config view.

Set a password before starting:

```bash
export DARKROUTER_ADMIN_PASSWORD_HASH="$(echo -n 'your-password' | darkrouter hash-password)"
```

Without it the gateway still proxies — that is its job — but every login is
refused and `/healthz` carries a warning saying so.

Nine destinations cover operations and configuration: **Overview**,
**Requests**, **Usage**, **Providers**, **Models**, **Routing**,
**Playground**, **Connect**, and **Settings**. Provider accounts and
credentials are managed under Providers; model overrides, aliases, and policy
are editable in the console, with file-owned and restart-only settings marked
where they are shown. Playground provides persistent Chat, side-by-side
Compare, Token Count, and the six auxiliary request tools.

**Credentials are never returned by the API** — not for editing, not for export.
The dashboard shows a label and a masked suffix. Replacing a key means adding a
new one and deleting the old.

**The proxy port never honors the dashboard's cookie.** Cookies are not
port-scoped, so a browser logged into the admin port sends that cookie to the
proxy port too; only `server.proxy_token` authenticates there.


## Signed and subscription credentials

Beyond a static API key, Darkrouter carries three credential strategies. Each
composes with an adapter kind rather than being one: a Claude subscription
speaks Anthropic Messages and an OpenAI one does not, so OAuth cannot be a
provider kind of its own.

| Style | Used by | What Darkrouter stores |
|---|---|---|
| `sigv4` | `bedrock` | An access key and secret, or nothing — the AWS chain covers environment, shared config and instance role |
| `gcp-sa` | `vertex` | The service-account JSON, encrypted, exchanged for a short-lived token |
| `oauth` | `anthropic`, and any other kind an OAuth preset declares | A refresh token, encrypted, rotated on every refresh |

**Bedrock** speaks Converse, not InvokeModel, so one message shape covers every
model family. Its region is a provider property rather than part of the model
id; what carries a `us.` or `eu.` prefix is the cross-region inference profile,
and discovery catalogues **profile ids** because those are what an invocation
must name. Streaming arrives as AWS binary eventstream framing rather than SSE.

**Vertex** is one kind with two request builders. A `publishers/google` model
goes to `:generateContent` with a Gemini payload; a `publishers/anthropic` one
goes to `:rawPredict` with an Anthropic Messages payload carrying
`anthropic_version`. Vertex has no usable listing API, so its catalog is seeded
from the preset and models.dev and the credential probe confirms reachability.
Llama and Mistral MaaS use a third route and are not served.

**OAuth accounts** connect from Settings. The dashboard shows an authorize link
and a box to paste the redirected URL back into — the paste path always works,
because vendors register `localhost` redirect URIs that a homelab admin origin
cannot satisfy. Where the vendor's registered URI is a localhost callback and
Darkrouter runs on your own machine, a temporary listener receives the redirect
directly instead.

Tokens refresh in the background ahead of expiry. Many vendors rotate the
refresh token on every refresh, so **run one Darkrouter against one account**:
two instances sharing a grant trip rotation-reuse detection, which some vendors
treat as theft and answer by revoking the grant. A refresh the provider refuses
outright disables the credential and shows "needs reconnection" on the overview
— it is not retried, because hammering a refused endpoint is how an account gets
locked rather than recovered.

**No credential material is ever returned by the API**, for any of the three.

**These three have not been verified against a real vendor.** This build has no
AWS account, no GCP service account and no Claude subscription behind it: every
test runs against a known-answer vector, an SDK-encoded frame, or a fake server.
The Converse and `rawPredict` field names in particular come from vendor
documentation rather than from a live call.

### Augment

Augment publishes no HTTP API, so the `auggie` preset is an `openaicompat`
provider whose base URL, `auggie://cli/v1`, is served by a transport that runs
the `auggie` CLI as a subprocess (`internal/localcli`) and returns its output
as an OpenAI-shaped response. The image installs a pinned version and the
compose file keeps its login state in the `auggie-state` volume, so
`docker exec -it darkrouter auggie login` once is enough; setting
`AUGMENT_SESSION_AUTH` in `.env` is the alternative for a host that cannot
complete the interactive login. `AUGGIE_BIN` points at a different binary
when the bundled one is not wanted.

The CLI is proprietary and its licence restricts redistribution — see
`THIRD_PARTY_NOTICES.md`. For that reason the published `darkraise/darkrouter`
image is built with `WITH_AUGGIE=0` and does not carry it: a request routed to
the preset fails with an error naming the missing binary, and everything else
works unchanged. A local build includes it by default (`WITH_AUGGIE=1` is the
Dockerfile's default), which is how an operator who has accepted Augment's
terms gets the provider.

## The model catalog

Darkrouter ships a catalog of provider presets, so adding a known provider is a
name and a key rather than a base URL, an auth style, and a list of quirks.
Five sources merge into one index:

- **Presets** — shipped data: kind, base URL, auth style, surfaces, and known
  quirks per named upstream.
- **models.dev** — pricing, context windows, and capability flags, refreshed
  every twelve hours. A snapshot is embedded in the binary, so Darkrouter starts
  and serves with no outbound access to it.
- **Discovery** — each enabled provider's own model listing, probed every
  fifteen minutes and whenever a provider or credential changes.
- **Free tiers** — which models a provider's free tier covers, curated by hand
  upstream and refreshed daily. It cannot be derived from prices: a free tier is
  a property of your account, not of the model, so a provider that charges per
  token on paper can still serve you for nothing. A free tier whose terms the
  vendor has not sanctioned is not routed unless you opt that provider in.
- **A community price index** — refreshed daily and joined in memory, filling
  prices the other four sources leave unknown.

A provider that times out does not lose its models: after three consecutive
failed probes they are marked stale and stay routable, because the circuit
breaker rather than the catalog is what avoids a broken provider. A model a
*successful* listing omits three times running is marked removed upstream and
stops being routable.

Each syncer is optional. See the `catalog:` block in `darkrouter.example.yaml`,
and [`docs/design/catalog-and-providers.md`](docs/design/catalog-and-providers.md)
for the merge precedence.

## Third-party material

Provider logos, the preset catalogue, the curated free-tier list and the
models.dev snapshot all come from elsewhere, and the libraries in the binary
and the console bundle do too. [`THIRD_PARTY_NOTICES.md`](THIRD_PARTY_NOTICES.md)
names each one, its licence and its upstream. Provider logos are their owners'
trademarks, shown so an operator can find the provider they mean — not as an
endorsement, and removable on request.

## Develop

**[`docs/development.md`](docs/development.md)** covers the layout, running
locally, the test discipline, the generated preset catalogue and the release
process. **[`docs/operations/verification.md`](docs/operations/verification.md)**
lists every gate and says what each one does and does not prove.
