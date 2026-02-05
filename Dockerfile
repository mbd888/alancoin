# Build stage
FROM golang:1.22-alpine AS builder

WORKDIR /app

# Install build dependencies
RUN apk add --no-cache git ca-certificates

# Copy go mod files first for better caching
COPY go.mod go.sum* ./
RUN go mod download

# Copy source code
COPY . .

# Build binary
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-w -s -X main.Version=$(git describe --tags --always 2>/dev/null || echo 'dev') -X main.Commit=$(git rev-parse --short HEAD 2>/dev/null || echo 'unknown') -X main.BuildTime=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
    -o /app/bin/alancoin \
    ./cmd/server

# Runtime stage
FROM alpine:3.19

WORKDIR /app

# Install runtime dependencies
RUN apk add --no-cache ca-certificates tzdata

# Create non-root user
RUN addgroup -g 1000 alancoin && \
    adduser -u 1000 -G alancoin -s /bin/sh -D alancoin

# Copy binary from builder
COPY --from=builder /app/bin/alancoin /app/alancoin

# Use non-root user
USER alancoin

# Expose port
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8080/health/live || exit 1

# Run
ENTRYPOINT ["/app/alancoin"]
