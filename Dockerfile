# The SPA is built first and its output copied into the Go stage. Building it
# inside the Go stage would mean installing Node in the Go image; building it
# outside would make the image depend on the host having run npm first, which is
# the "works on my machine" a multi-stage build exists to remove.
# Base images are pinned by tag. A digest would pin harder, but it changes
# with every rebuild of the upstream image and has to be resolved by pulling;
# Dependabot's docker ecosystem tracks these tags instead and opens a pull
# request when one moves, which is the review point a digest bump would need
# anyway. Tags in this file: node:26-alpine, golang:1.27.1-alpine, alpine:3.24.
ARG WITH_AUGGIE=1

FROM node:26-alpine AS web
# Mirrors the repo layout so the bundle lands where vite.config.ts points it,
# at /src/internal/admin/dist.
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
# ci rather than install: the lockfile is committed and a reproducible image is
# the point. install is free to resolve a newer minor and produce a different
# bundle from the one that was tested.
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.27.1-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# go:embed reads the filesystem at compile time, so the bundle has to land
# before the build below rather than being mounted at runtime.
COPY --from=web /src/internal/admin/dist ./internal/admin/dist
# Stamped into the binary so a running container can say which build it is:
# GET /healthz on the admin port reports it, and without this every image
# calls itself "dev".
ARG VERSION=dev
# CGO_ENABLED=0 keeps the binary static, which is what lets the final image be
# minimal and what modernc.org/sqlite is chosen for in Phase 2.
RUN CGO_ENABLED=0 go build -trimpath \
      -ldflags="-s -w -X github.com/darkraise/darkrouter/internal/server.Version=${VERSION}" \
      -o /out/darkrouter ./cmd/darkrouter

# Augment publishes no HTTP endpoint at all: the provider in internal/localcli
# reaches it by running the `auggie` CLI. The image carries it rather than
# expecting it on the host, because a provider that works only where somebody
# remembered to install something is a provider that breaks on the next machine.
#
# WITH_AUGGIE=0 builds without it: no Node runtime in the final image and no
# CLI, for a deployment that never routes to Augment and would rather not carry
# a second language runtime for a provider it does not use. The stage name is
# resolved from the argument, which is how a Dockerfile expresses a conditional
# stage without duplicating the final one. The argument itself is declared
# at the top of the file: an ARG a FROM line reads has to precede every stage.

FROM node:26-alpine AS auggie-1
# Pinned for the reason web/ uses npm ci: an image is meant to be reproducible,
# and an unpinned CLI would change under a rebuild that touched nothing else.
ARG AUGGIE_VERSION=0.36.0
RUN npm install -g --prefix /opt/auggie @augmentcode/auggie@${AUGGIE_VERSION}

# The empty counterpart: the final stage copies /opt/auggie unconditionally,
# so the stage it copies from has to exist either way.
FROM alpine:3.24 AS auggie-0
RUN mkdir -p /opt/auggie

FROM auggie-${WITH_AUGGIE} AS auggie

FROM alpine:3.24
ARG WITH_AUGGIE
# nodejs is here for auggie alone — the gateway itself is a static binary and
# needs no runtime. It is the cost of a vendor who ships a CLI instead of an API.
# The base tag lags Alpine's package fixes by days; upgrading here is what
# keeps the image scan clean between base-image releases.
RUN apk upgrade --no-cache && apk add --no-cache ca-certificates wget \
    $([ "$WITH_AUGGIE" = "1" ] && echo nodejs) \
    && adduser -D -u 10001 darkrouter
COPY --from=build /out/darkrouter /usr/local/bin/darkrouter
COPY --from=auggie /opt/auggie /opt/auggie
# On PATH under its own name, which is how internal/localcli finds it when
# AUGGIE_BIN is unset. The state directory is created here so that a named
# volume mounted over it inherits a usable mode; without it `auggie login`
# writes into a directory it does not own and fails.
#
# Mode rather than ownership, because the uid is the operator's choice: this
# image runs as root so a bind-mounted /data needs nothing done to it on the
# host, and `user: "10001:10001"` opts back into the unprivileged account that
# is still built below. A directory owned by either uid would break the other.
# It is single-tenant state that a volume mount replaces in every real
# deployment, which is what makes the mode acceptable here and nowhere else.
RUN if [ "$WITH_AUGGIE" = "1" ]; then ln -s /opt/auggie/bin/auggie /usr/local/bin/auggie; fi \
    && install -d -m 0777 /home/darkrouter/.augment
# The notices travel with the artifact rather than only with the repository:
# Apache-2.0 asks for them to reach whoever receives the binary, and an image
# is how most people receive this one.
COPY THIRD_PARTY_NOTICES.md /usr/share/doc/darkrouter/THIRD_PARTY_NOTICES.md

# No USER: the container runs as root so a bind-mounted ./data works with
# nothing done to it on the host. Docker creates a missing bind-mount source
# owned by root, and an unprivileged process cannot write into it -- so an
# unprivileged default charges every deployment a chown before it will start.
#
# What makes that acceptable is compose.prod.yml, which drops every capability,
# mounts the root filesystem read-only and sets no-new-privileges. Root with an
# empty capability set cannot chown, mount, load a module, bind a privileged
# port, or override a file mode -- it is uid 0 and almost nothing else. Keep
# those three settings beside any `user:` override.
#
# HOME is explicit because the Augment CLI reads its session from under it and
# the value must not change with the uid: dropping to `user: "10001:10001"`
# would otherwise resolve it to /home/darkrouter by passwd lookup and root to
# /root, silently splitting the volume from the directory that is mounted.
ENV HOME=/home/darkrouter

# 0777 for the same reason as the Augment directory: a named volume mounted
# here inherits this mode, and the operator picks the uid.
RUN install -d -m 0777 /data
WORKDIR /data
EXPOSE 8080 8081
# readyz rather than healthz: readiness fails while the store or the config
# is not usable, which is the state an orchestrator should route away from.
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8081/readyz || exit 1
ENTRYPOINT ["darkrouter", "-config", "/data/darkrouter.yaml"]
