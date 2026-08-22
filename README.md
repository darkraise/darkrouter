# Darkrouter

A self-hosted LLM gateway. One endpoint, many providers, deterministic failover.

## Status

Phase 1: OpenAI chat completions proxied to one OpenAI-compatible provider, with
config hot-reload. See `docs/superpowers/specs/README.md` for the full design and
the phase roadmap.

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
