FROM golang:1.26.5-alpine AS builder

WORKDIR /build

# Download dependencies first (cache layer)
COPY go.mod go.sum ./
RUN go mod download

# Build
COPY . .
RUN CGO_ENABLED=0 go build -tags cortex_vectors -ldflags="-s -w" -o cortex ./cmd/cortex

# Runtime image
FROM alpine:3.20

RUN apk add --no-cache ca-certificates curl \
    && addgroup -S cortex && adduser -S cortex -G cortex \
    && mkdir -p /home/cortex/.cortex && chown cortex:cortex /home/cortex/.cortex

COPY --from=builder /build/cortex /usr/local/bin/cortex
COPY --from=builder /build/migrations /migrations
COPY docker/server-entrypoint.sh /usr/local/bin/cortex-server-entrypoint

RUN chmod 755 /usr/local/bin/cortex-server-entrypoint

USER cortex

EXPOSE 7438

ENTRYPOINT ["/usr/local/bin/cortex-server-entrypoint"]

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
  CMD curl -f http://localhost:7438/health || exit 1

CMD ["/usr/local/bin/cortex", "--mode", "server"]
