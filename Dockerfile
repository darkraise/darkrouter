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

FROM alpine:3.21
RUN apk add --no-cache ca-certificates wget && adduser -D -u 10001 darkrouter
COPY --from=build /out/darkrouter /usr/local/bin/darkrouter
USER darkrouter
WORKDIR /data
EXPOSE 8080 8081
ENTRYPOINT ["darkrouter", "-config", "/data/darkrouter.yaml"]
