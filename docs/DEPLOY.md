# Deploying Darkrouter

One image carries the gateway, the embedded console and (by default) the
Augment CLI. Three files sit beside it on the host: `compose.prod.yml`, `.env`
and `data/darkrouter.yaml`. `data/` also holds `darkrouter.db`, which stores
every provider credential encrypted under `DARKROUTER_MASTER_KEY`, so treat the
directory as a secret.

## Production: run the published image

```bash
mkdir -p data && sudo chown -R 10001:10001 data   # the image's unprivileged uid
cp darkrouter.example.yaml data/darkrouter.yaml
cp .env.example .env                              # fill it in; double every $ in the bcrypt hash
docker compose -f compose.prod.yml pull
docker compose -f compose.prod.yml up -d
```

`compose.prod.yml` sets `pull_policy: always`, so `up` fetches the tag named by
`DARKROUTER_TAG` (default `latest`) every time. The container runs read-only
with all capabilities dropped and a 1 GB memory and 512 pid ceiling; it needs
nothing writable beyond `/data`, the Augment session volume and a tmpfs `/tmp`.

Roll back by pinning `DARKROUTER_TAG` to the immutable `sha-<short>` tag CI
published for the build you want, then `up -d` again. `latest` has already
moved by the time a rollback is wanted, which is why the sha tag exists.

Internet exposure goes through the `edge` profile: `--profile edge` adds Caddy,
which terminates TLS for `ADMIN_DOMAIN` and `PROXY_DOMAIN` and adds HSTS,
`nosniff`, a referrer policy and frame denial on the dashboard. Each surface
must be a whole origin (no subpath, no split between UI host and API host) or
the console's same-origin `/api` calls break.

## Local build (UAT): run what is in the working tree

The Go binary embeds the console bundle at compile time, so neither `npm run
build` nor `go build` changes what a running container serves. A change is
deployed only once the image is rebuilt and the container recreated from it.

```bash
docker build -t darkraise/darkrouter:latest .
docker compose -f compose.prod.yml -f compose.uat.yml up -d darkrouter
```

The `compose.uat.yml` overlay is required. `compose.prod.yml` alone would pull
the published image from Docker Hub and discard the local build; the overlay
sets `pull_policy: never`. The Dockerfile runs `npm ci && npm run build`
itself, so the image is built from source and does not depend on a local
`web/dist`.

Pass `--build-arg VERSION=...` when the build is meant to identify itself;
without it the binary and `/healthz` report `dev`. `--build-arg WITH_AUGGIE=0`
leaves the Augment CLI and its Node runtime out of the image.

### Verify the deploy took

The machine this project is developed on publishes the proxy on **8090** and
the admin port on **8091** (`PROXY_PORT` and `ADMIN_PORT` in `.env`). 8080 and
8081 belong to other containers there, and querying them returns a different
service's response that looks like a passing check.

```bash
docker ps --filter name=darkrouter --format '{{.Names}}\t{{.Status}}'
curl -s http://localhost:8091/healthz
```

Then confirm the served console is the build you just made by comparing
bytes, not filenames. Vite's asset hash is not stable across build
environments: the image builds at `/src/web` and a host build runs at the repo
path, and the two produce the same bundle under different `index-<hash>.js`
names, so a filename comparison reports a false mismatch on a good deploy.

```bash
(cd web && npm run build)
asset=$(curl -s http://localhost:8091/ | grep -o 'assets/index-[A-Za-z0-9_-]*\.js')
curl -s "http://localhost:8091/$asset" > /tmp/served.js
cmp /tmp/served.js internal/admin/dist/assets/index-*.js && echo "deploy matches source"
```

The console needs a password. On the development machine the UAT password is
in `.uat-credentials` at the repository root, which is gitignored and stays
that way: the console checks a password against the bcrypt hash in `.env`, and
the hash is what is committed so the plaintext never is.

## Backup, restore and downgrade

A backup is `data/` plus the master key. The database is useless without the
key and the key is useless without the database, so store them apart and
restore them together. See the README's "Backup and restore" section for the
procedure, including how `rotate-key` interacts with an older backup.

Downgrading is restoring: stop the container, replace `data/` with the backup
taken before the upgrade, pin `DARKROUTER_TAG` to the earlier build, `up -d`.
Migrations only run forward, so an older binary is not started against a newer
database.

## Configuration changes without a redeploy

`data/darkrouter.yaml` is watched and most keys reload live. The keys that
need a restart are marked as such in the console's Settings screen and in the
startup log; providers, aliases and policy are imported from the file once on
first run and owned by the database (edit them in the console) from then on.
`docs/ARCHITECTURE.md` describes the precedence in full.
