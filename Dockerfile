FROM golang:1.26-alpine AS builder

WORKDIR /build

# Download dependencies first (cache layer)
COPY go.mod go.sum ./
RUN go mod download

# Build
COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o cortex ./cmd/cortex

# Runtime image
FROM alpine:3.20

RUN apk add --no-cache ca-certificates

COPY --from=builder /build/cortex /usr/local/bin/cortex
COPY --from=builder /build/migrations /migrations

EXPOSE 7438

ENTRYPOINT ["cortex"]
CMD ["mcp"]
