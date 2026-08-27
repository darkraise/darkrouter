# darkrouter

## Typography: never hardcode a font size

Two rules, in the console (`web/`) and anywhere else that renders UI:

1. **Never `text-xs`.** 14px (`text-sm`) is the floor. Hierarchy below body
   text comes from colour (`--legend`, `--muted-foreground`) and weight, not
   from a smaller size.
2. **Never a custom size.** No `text-[11px]`, no `text-[length:var(--…)]`, no
   `font-size: 13px` in a stylesheet. Only darkraise-ui's predefined scale:
   `text-sm`, `text-base`, `text-lg`, `text-xl`, `text-2xl`, `text-3xl`.

The reason is the font-size axis. Its four steps rebind `--text-*`, so a
`text-sm` label moves 14 → 18px when an operator picks extra-large while a
`text-[11px]` one stays at 11px. A hardcoded size is not a slightly-off size;
it is a size that silently opts out of a setting the operator changed.

In a stylesheet, where a utility class cannot be applied, use the same scale
through its token — `font-size: var(--text-sm)` — never a pixel value.

## Always redeploy after a feature or bug fix

When a change is finished and verified, redeploy without being asked. A change
that only exists in the working tree is not done: the Go binary embeds the
console bundle at compile time, so neither `npm run build` nor `go build`
changes what the running container serves.

```bash
docker build -t darkraise/darkrouter:latest .
docker compose -f compose.prod.yml -f compose.uat.yml up -d darkrouter
```

The `compose.uat.yml` overlay is required. `compose.prod.yml` alone sets
`pull_policy: always` and would pull the published image from Docker Hub,
discarding the local build; the overlay sets `pull_policy: never`. The
Dockerfile runs `npm ci && npm run build` itself, so the image is built from
source and does not depend on a local `web/dist`.

Then verify, rather than assuming the deploy took:

```bash
docker ps --filter name=darkrouter --format '{{.Names}}\t{{.Status}}'
curl -s http://localhost:8091/healthz
```

The host publishes **8090** (proxy) and **8091** (admin) — 8080 and 8081 belong
to other containers on this machine, and querying them returns a different
service's response that looks like a passing check.

Confirm the deployed console is the build you just made by comparing bytes,
not filenames. Vite's asset hash is **not** stable across build environments:
the image builds at `/src/web` and you build at the repo path, and the two
produce the same bundle under different `index-<hash>.js` names. A filename
comparison reports a false mismatch on a perfectly good deploy.

```bash
(cd web && npm run build)
asset=$(curl -s http://localhost:8091/ | grep -o 'assets/index-[A-Za-z0-9_-]*\.js')
curl -s "http://localhost:8091/$asset" > /tmp/served.js
cmp /tmp/served.js internal/admin/dist/assets/index-*.js && echo "deploy matches source"
```

Pass `--build-arg VERSION=...` when the build is meant to identify itself;
without it the binary and `/healthz` report `dev`.
