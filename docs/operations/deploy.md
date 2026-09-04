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

**Double every `$` in the bcrypt password hash in `.env`.** Compose reads a
single `$` as a variable, and the value arrives silently truncated — a correct
password then fails to log in.

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

Both ports bind every interface, so the LAN reaches them directly. For the
internet, `--profile edge` adds Caddy, which terminates TLS for `ADMIN_DOMAIN`
and `PROXY_DOMAIN` and sets HSTS, `nosniff`, a referrer policy and frame denial
on the console.

**Each surface must be a whole origin** — no subpath, no split between the
console host and the API host — or the console's same-origin `/api` calls
break. See [`../design/security.md`](../design/security.md) for why no CORS
configuration exists.

Login rate limiting is Darkrouter's own, because Caddy's standard build ships
no rate limiter.

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
