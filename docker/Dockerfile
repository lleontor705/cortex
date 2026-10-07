# Web builder: produce the Next.js static export. Git tracks only the
# internal/web/dist/index.html placeholder (the rest of dist is gitignored), so
# a release checkout embeds an asset-less UI unless the export is rebuilt here.
# This mirrors `make web-build` inside the image build.
FROM node:26-alpine AS webbuilder

WORKDIR /web

COPY web/package.json web/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm \
    npm ci

COPY web/ ./
ENV NEXT_TELEMETRY_DISABLED=1
RUN npm run build

FROM golang:1.27.1-alpine AS builder

WORKDIR /build

# Download dependencies first with BuildKit module cache
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# Build with Go native zero-CGO
COPY . .

COPY --from=webbuilder /web/out /web/out

# Sync the freshly built export into the embedded dist (make web-build parity:
# replace the placeholder tree wholesale, then precompress text assets for gzip
# content negotiation). The //go:embed all:dist directive picks up every file,
# including the underscore-prefixed _next/ tree.
RUN rm -rf internal/web/dist \
    && cp -R /web/out internal/web/dist \
    && find internal/web/dist -type f ! -name '*.gz' \( -name '*.html' -o -name '*.js' -o -name '*.mjs' -o -name '*.css' -o -name '*.json' -o -name '*.txt' -o -name '*.svg' -o -name '*.map' -o -name '*.webmanifest' -o -name '*.xml' \) -exec gzip -k9 {} +

RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 go build -tags cortex_vectors -ldflags="-s -w" -o cortex ./cmd/cortex

# Runtime image
FROM alpine:3.24

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
