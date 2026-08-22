FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# CGO_ENABLED=0 keeps the binary static, which is what lets the final image be
# minimal and what modernc.org/sqlite is chosen for in Phase 2.
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/darkrouter ./cmd/darkrouter

FROM alpine:3.21
RUN apk add --no-cache ca-certificates wget && adduser -D -u 10001 darkrouter
COPY --from=build /out/darkrouter /usr/local/bin/darkrouter
USER darkrouter
WORKDIR /data
EXPOSE 8080 8081
ENTRYPOINT ["darkrouter", "-config", "/data/darkrouter.yaml"]
