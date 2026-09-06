# Deploying and running Darkrouter

One image carries the gateway and the embedded console. Three files sit beside
it on the host: `compose.prod.yml`, `.env`, and `data/darkrouter.yaml`. `data/`
also holds the database, which stores every provider credential encrypted under
`DARKROUTER_MASTER_KEY` — treat the whole directory as a secret.

## Production

```bash
mkdir -p data && sudo chown -R 10001:10001 data   # the image's unprivileged uid
cp darkrouter.example.yaml data/darkrouter.yaml
cp .env.example .env                              # fill it in
docker compose -f compose.prod.yml pull
docker compose -f compose.prod.yml up -d
```

`.env` needs one value to start: `DARKROUTER_MASTER_KEY`. Everything else in
`.env.example` is commented out and has a working default, and providers are
added in the console rather than here — nothing in the file is interpolated,
so values are pasted exactly as they were printed.

The admin password is set in the browser, not here. On first run the process
prints a one-time setup token; open the console and it asks for that token and
a password:

```bash
docker compose -f compose.prod.yml logs | grep 'setup token'
```

Setting a password closes setup for good. `DARKROUTER_ADMIN_PASSWORD_HASH`
still works and still overrides the stored password on the next restart, which
is how a lost password is recovered — see below.

> **Upgrading a deployment made before this change:** the bcrypt hash used to
> need every `$` doubled. It no longer does, and a doubled hash now refuses a
> correct password. Undo it once with `sed -i 's/\$\$/$/g' .env`.

**Recovering a lost password.** Put a fresh hash in `DARKROUTER_ADMIN_PASSWORD_HASH`
and restart. A hash that differs from the one in force when the password was
last set reads as newer, and the stored password is dropped in its favour:

```bash
docker run --rm --entrypoint darkrouter darkraise/darkrouter:latest \
  hash-password -password 'yours'
```

The whole of `.env` is passed to the container, so a `${SOME_KEY}` written
into `data/darkrouter.yaml` resolves from it under any name, without touching
`compose.prod.yml`.

The container runs read-only, with all capabilities dropped and a 1 GB memory
and 512 pid ceiling. It needs nothing writable beyond `/data` and a tmpfs
`/tmp`.

`compose.prod.yml` sets `pull_policy: always`, so `up` fetches the tag named
by `DARKROUTER_TAG` (default `latest`) every time.

**Rolling back** means pinning `DARKROUTER_TAG` to the immutable `sha-<short>`
tag CI published for the build you want, then `up -d` again. `latest` has
already moved by the time a rollback is wanted, which is why the sha tag
exists.

## Local build (UAT)

The console is embedded at compile time, so neither `npm run build` nor
`go build` changes what a running container serves. A change is deployed only
once the image is rebuilt and the container recreated from it.

```bash
docker build -t darkraise/darkrouter:latest .
docker compose -f compose.prod.yml -f compose.uat.yml up -d darkrouter
```

**The `compose.uat.yml` overlay is required.** `compose.prod.yml` alone pulls
the published image over your local build; the overlay sets
`pull_policy: never`.

Pass `--build-arg VERSION=…` when the build should identify itself; without it
the binary and `/healthz` report `dev`. `--build-arg WITH_AUGGIE=0` leaves the
optional local CLI and its Node runtime out.

### Verifying a deploy took

This development machine publishes the proxy on **8090** and the admin port on
**8091**. **8080 and 8081 belong to other containers**, and querying them
returns a different service's response that looks like a passing check.

```bash
docker ps --filter name=darkrouter --format '{{.Names}}\t{{.Status}}'
curl -s http://localhost:8091/healthz
```

Then confirm the served console is the build you just made **by comparing
bytes, not filenames**. The asset hash is not stable across build environments
— the image builds at one path and a host build at another — so a filename
comparison reports a false mismatch on a good deploy.

```bash
(cd web && npm run build)
asset=$(curl -s http://localhost:8091/ | grep -o 'assets/index-[A-Za-z0-9_-]*\.js')
curl -s "http://localhost:8091/$asset" > /tmp/served.js
cmp /tmp/served.js internal/admin/dist/assets/index-*.js && echo "deploy matches source"
```

The console needs a password. On this machine it is in `.uat-credentials` at
the repository root, which is gitignored and stays that way: the console checks
a password against the bcrypt hash in `.env`, and the hash is what is committed
so the plaintext never is.

## Backup and restore

A backup is two things that are useless apart: **`data/`** and **the master
key**. Store them separately.

Take the database copy with the container stopped, or online so the WAL folds
in consistently:

```bash
sqlite3 data/darkrouter.db ".backup 'darkrouter-$(date -u +%F).db'"
```

If `rotate-key` has run since a backup was taken, **that backup still needs the
key that was current when it was taken.** Keep the old key with the old backup
until the backup is retired.

**Restoring and downgrading are the same operation**, because migrations run
forward only: stop the container, put the backup's database back under `data/`,
set `DARKROUTER_MASTER_KEY` to the key that matches it, pin `DARKROUTER_TAG` to
the build you want, and start. An older binary refuses a newer database rather
than half-applying it.

## Configuration changes without a redeploy

`data/darkrouter.yaml` is watched and most keys reload live. The keys that need
a restart are listed in [`../design/configuration.md`](../design/configuration.md),
and are marked in the console's Settings screen and in the startup log.

Providers, aliases and policy are imported from the file once on first run and
owned by the database from then on — edit them in the console.

## Exposure

Both ports bind every interface, so the LAN reaches them directly. Neither
speaks TLS, so anything reachable from the internet needs a reverse proxy in
front — Caddy, nginx, Traefik, a Cloudflare tunnel; the stack ships none and
takes no view on which. Four requirements it has to meet:

- **Terminate TLS and add HSTS.** Darkrouter already sets `nosniff`, frame
  denial, a referrer policy and a CSP on every console response, but
  `Strict-Transport-Security` belongs to whatever holds the certificate.
- **Give each surface a whole origin** — the console on one name, the gateway
  on another, each owning the root of its name. No subpath, no split between
  the console host and the API host, or the console's same-origin `/api` calls
  break. See [`../design/security.md`](../design/security.md) for why no CORS
  configuration exists.
- **Forward `X-Forwarded-Proto`.** The console reads it to mark the session
  cookie `Secure`; without it an HTTPS deployment issues cookies that are not.
  It is honoured only from a loopback or private peer, so the proxy has to
  reach Darkrouter over the container network rather than a public address.
- **Do not buffer the gateway's responses** — Caddy's `flush_interval -1`,
  nginx's `proxy_buffering off`. Buffered streaming delivers a completion in
  one lump instead of token by token.

Login rate limiting is Darkrouter's own, per IP, so it holds whatever sits in
front.

## Reaching a model runtime on the host

Local runtimes run on the host, not in the container, so `localhost` — which
every local preset ships as its base URL — is the wrong address: inside the
container it is the container. Both compose files map `host.docker.internal`
to the host gateway, and that is what the console's **Add local runtime** form
offers by default.

An existing deployment does not pick up a new `extra_hosts` entry on a
restart:

```sh
docker compose up -d --force-recreate darkrouter
```

The runtime must also listen on more than loopback or it refuses a connection
from the container's network namespace — for Ollama, `OLLAMA_HOST=0.0.0.0`.

Running outside a container instead? Use `localhost` in the form; nothing else
changes.

## Command line

```bash
docker run --rm --entrypoint darkrouter darkraise/darkrouter:latest hash-password
docker run --rm --entrypoint darkrouter darkraise/darkrouter:latest rotate-key -db …
```

The entrypoint is the gateway itself, which is why the override is needed.
`hash-password` with no flag reads the password from stdin; `rotate-key` reads
the new key from stdin.
