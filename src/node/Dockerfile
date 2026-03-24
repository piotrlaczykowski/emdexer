FROM golang:1.26.1-alpine AS builder

WORKDIR /app

# OPTIMIZE CACHING: Copy only go.mod/go.sum first
COPY src/pkg/go.mod src/pkg/go.sum src/pkg/
COPY src/node/go.mod src/node/go.sum src/node/

WORKDIR /app/src/node
RUN go mod download

# Copy source code and build
WORKDIR /app
COPY src/pkg/ src/pkg/
COPY src/node/ src/node/

WORKDIR /app/src/node
# CGO_ENABLED=0: all dependencies (including github.com/zeebo/xxh3 added in
# Phase 15.6 for delta-indexing) are pure Go — no CGO required.
ARG VERSION=dev
ARG COMMIT=none
RUN CGO_ENABLED=0 go build -ldflags="-s -w -X 'github.com/piotrlaczykowski/emdexer/version.Version=${VERSION:-dev}' -X 'github.com/piotrlaczykowski/emdexer/version.Commit=${COMMIT:-none}'" -o /app/emdexer-node .

FROM alpine:3.23
RUN apk add --no-cache curl
WORKDIR /app
RUN addgroup -S emdexer && adduser -S emdexer -G emdexer
COPY --from=builder /app/emdexer-node .
RUN chown -R emdexer:emdexer /app
USER emdexer
CMD ["./emdexer-node"]
