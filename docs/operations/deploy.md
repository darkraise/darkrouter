# Deploying and running Darkrouter

One image carries the gateway and the embedded console. Two files sit beside it
on the host: `compose.prod.yml` and `.env`. `data/` holds the database, which
stores every provider credential encrypted under `DARKROUTER_MASTER_KEY` —
treat the whole directory as a secret.

There is no configuration file. Settings — body and streaming limits, log and
capture retention, the catalogue sync — live in the database's `settings`
table; every key has a working default, and a key nothing has set stays on it.
Providers and aliases are database-owned too, each with its own tables. A
small bootstrap set comes from `.env` instead, because the process needs it
before the database is open.

## Production

```bash
cp .env.example .env   # fill it in
docker compose -f compose.prod.yml pull
docker compose -f compose.prod.yml up -d
```

Settings are changed in the console. The Settings screen edits every stored
setting, says where each value came from — the database, the environment, or a
compiled default — offers a reset to the default, and names the keys whose
change waits for a restart. A `darkrouter.yaml` left over from an older
deployment is ignored: the process warns about it at startup, in the log and on
the Settings screen, and reads nothing from it.

`.env` needs one value to start: `DARKROUTER_MASTER_KEY`. Everything else in
`.env.example` is commented out and has a working default, and providers are
added in the console rather than here — nothing in the file is interpolated,
so values are pasted exactly as they were printed.

The first account claims the console. On first run the console shows a claim
screen instead of a login: pick a username and a password, and that account
becomes the administrator. There is no setup token and no environment
variable — whoever reaches the console first claims it.

**Claim it immediately after the first start.** Both ports bind every
interface, so until an account exists anyone who can reach the admin port can
become the administrator. The process logs a warning at every start while a
database that already holds providers has no account.

**There is no password recovery.** No environment variable overrides a stored
password and no subcommand resets one. An administrator who forgets their
password has no route back in through darkrouter itself. The working remedy
is to stop the container and clear the `users` table in `data/darkrouter.db`:

```bash
sqlite3 data/darkrouter.db "PRAGMA foreign_keys=ON; DELETE FROM users;"
```

`sessions` cascades from `users` and is cleared with it, so this also signs
out anyone still logged in — the pragma has to come first because a bare
`sqlite3` connection starts with foreign keys off. Nothing else is touched —
providers, credentials, aliases and settings all live outside `users` and
survive untouched. On restart the console falls back to its claim screen, so
**be ready to claim it immediately**: the exposure window from before the
first account existed reopens until someone does.

An alternative is to write a new `password_hash` column value by hand instead
of deleting the account, which skips reopening the claim screen. There is no
`hash-password` subcommand to do this anymore; on a host with Apache's
`htpasswd` available, `htpasswd -nbBC 12 '' 'yours' | cut -d: -f2` produces a
cost-12 bcrypt hash to write into that column:

```bash
sqlite3 data/darkrouter.db \
  "UPDATE users SET password_hash='<hash>' WHERE username_lc='<name>';"
```

The hash contains `$` characters, so keep it single-quoted. Treat this as one
option, not the default — `htpasswd` is not installed everywhere, and clearing
`users` is the one guaranteed to work.

> **Upgrading a deployment made before these changes**, three one-time fixes:
>
> `DARKROUTER_ADMIN_PASSWORD_HASH` is no longer read. Remove it from `.env`;
> leaving it set has no effect. Every session ends at this upgrade and the
> console falls to its claim screen, so claim it as soon as it restarts.
>
> Settings that lived only in `data/darkrouter.yaml` are gone. The `server`,
> `log`, `capture`, `catalog`, `media` and `playground` blocks were file-only,
> so those revert to their defaults and are re-entered later; providers,
> aliases and policy were already in the database and are unaffected.
>
> The container used to run as uid 10001 and `data/` was chowned to match. It
> now runs as root, and root with every capability dropped cannot write a
> directory owned by someone else, so an existing `data/` locks the database
> read-only — `sudo chown -R 0:0 data`. A deployment created after this change
> needs neither.

The whole of `.env` is passed to the container, so a bootstrap variable is set
there under its own name without touching `compose.prod.yml`. Nothing is
interpolated: values are read exactly as written.

The container runs as root, read-only, with all capabilities dropped and a 1 GB
memory and 512 pid ceiling. It needs nothing writable beyond `/data` and a
tmpfs `/tmp`.

Root is what lets a bind-mounted `data/` work with nothing done to it on the
host: Docker creates that directory owned by root, and an unprivileged process
cannot write into it. What makes it defensible is the empty capability set —
uid 0 with no capabilities cannot chown, mount, load a module, bind a
privileged port, or override a file mode. Keep `cap_drop`, `read_only` and
`no-new-privileges` together; dropping any one of them is what would make the
uid matter.

To run unprivileged instead, add `user: "10001:10001"` to the service and
`sudo chown -R 10001:10001 data`. The image still builds that account.

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

The console needs a username and a password. On this machine both are in
`.uat-credentials` at the repository root, which is gitignored and stays that
way: a plaintext password does not belong in the repository, whatever the
database stores.

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

Settings are read from the database and most keys apply without a restart. The
keys that need one are listed in [`../design/configuration.md`](../design/configuration.md).
The Settings screen marks them and, once a save changes one, shows a "Waiting
for a restart" banner. What backs that banner is `pending_restart`, measured
against the snapshot the process booted on and served by both `GET /api/config`
and `/healthz`; it survives later saves, so it is the field to script against.
The `warnings` array names the key too, but only at the save that changed it:
it is the diff between consecutive snapshots, and the next save or reload
clears it while the stale value is still in force.

Providers, aliases and policy are owned by the database and edited in the
console.

`server.shutdown_grace` applies on reload, but the container's
`stop_grace_period` does not: both compose files set it to 30s, after which
Docker sends SIGKILL. Shutdown needs a few seconds past the grace to close
streams and flush the request log, so raising `shutdown_grace` above 25s adds a
warning to `warnings`. Raise `stop_grace_period` in the compose file to at
least the new grace plus 5s and recreate the container, or the extra grace is
cut short by the kill.

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

`rotate-key` re-encrypts every stored credential under a new master key. **Stop
the gateway first.** A running gateway keeps the key it started with and seals
every credential it writes afterwards — a refreshed OAuth token, a key added in
the console — under that key, so a rotation beside it leaves rows the next start
cannot decrypt. The command refuses rather than letting that happen: the
gateway holds an exclusive lock on `data/darkrouter.db.lock` for as long as it
runs, and `rotate-key` exits with "stop the gateway before rotating the key"
while it is held. A second gateway started on the same `data/` is refused the
same way.

Some network mounts (NFS or CIFS without lock support) cannot take the lock at
all. The gateway still starts there and logs "running without the database
lock", but nothing can then tell whether it is running, so `rotate-key` refuses
unless you add `-gateway-stopped` to confirm you stopped it. That flag never
overrides a lock another process actually holds.

```bash
docker compose -f compose.prod.yml stop darkrouter
docker run --rm -i -v ./data:/data --env-file .env \
  --entrypoint darkrouter darkraise/darkrouter:latest rotate-key -db /data/darkrouter.db
# set DARKROUTER_MASTER_KEY in .env to the new key, then
docker compose -f compose.prod.yml up -d darkrouter
```

The entrypoint is the gateway itself, which is why the override is needed.
`rotate-key` reads the current key from `DARKROUTER_MASTER_KEY` and the new one
from stdin, hence `-i`. The lock file stays in `data/` after either process
exits; it is not a sign that anything is still running, and it must not be
deleted while the gateway is up.
