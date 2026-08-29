# The SPA is built first and its output copied into the Go stage. Building it
# inside the Go stage would mean installing Node in the Go image; building it
# outside would make the image depend on the host having run npm first, which is
# the "works on my machine" a multi-stage build exists to remove.
FROM node:24-alpine AS web
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

FROM golang:1.26-alpine AS build
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
FROM node:24-alpine AS auggie
# Pinned for the reason web/ uses npm ci: an image is meant to be reproducible,
# and an unpinned CLI would change under a rebuild that touched nothing else.
ARG AUGGIE_VERSION=0.36.0
RUN npm install -g --prefix /opt/auggie @augmentcode/auggie@${AUGGIE_VERSION}

FROM alpine:3.21
# nodejs is here for auggie alone — the gateway itself is a static binary and
# needs no runtime. It is the cost of a vendor who ships a CLI instead of an API.
RUN apk add --no-cache ca-certificates wget nodejs && adduser -D -u 10001 darkrouter
COPY --from=build /out/darkrouter /usr/local/bin/darkrouter
COPY --from=auggie /opt/auggie /opt/auggie
# On PATH under its own name, which is how internal/localcli finds it when
# AUGGIE_BIN is unset. The state directory is created here so that a volume
# mounted over it inherits the unprivileged user's ownership instead of arriving
# owned by root and unwritable — which is where `auggie login` would fail.
RUN ln -s /opt/auggie/bin/auggie /usr/local/bin/auggie \
    && install -d -o darkrouter -g darkrouter /home/darkrouter/.augment
# The notices travel with the artifact rather than only with the repository:
# Apache-2.0 asks for them to reach whoever receives the binary, and an image
# is how most people receive this one.
COPY THIRD_PARTY_NOTICES.md /usr/share/doc/darkrouter/THIRD_PARTY_NOTICES.md
USER darkrouter
WORKDIR /data
EXPOSE 8080 8081
ENTRYPOINT ["darkrouter", "-config", "/data/darkrouter.yaml"]
