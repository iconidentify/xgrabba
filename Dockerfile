# Build stage
FROM golang:1.22-alpine AS builder

RUN apk add --no-cache git ca-certificates

WORKDIR /app

# Copy go mod files first for caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build arguments for version info
ARG VERSION=dev
ARG BUILD_TIME=unknown

# Build the server binary
RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags="-s -w -X main.Version=${VERSION} -X main.BuildTime=${BUILD_TIME}" \
    -o xgrabba \
    ./cmd/server

# Build viewer binaries for all platforms (for USB export)
RUN mkdir -p bin && \
    CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-s -w -X main.Version=${VERSION} -X main.BuildTime=${BUILD_TIME}" \
    -o bin/xgrabba-viewer-linux ./cmd/viewer && \
    CGO_ENABLED=0 GOOS=darwin GOARCH=amd64 go build \
    -ldflags="-s -w -X main.Version=${VERSION} -X main.BuildTime=${BUILD_TIME}" \
    -o bin/xgrabba-viewer-mac-amd64 ./cmd/viewer && \
    CGO_ENABLED=0 GOOS=darwin GOARCH=arm64 go build \
    -ldflags="-s -w -X main.Version=${VERSION} -X main.BuildTime=${BUILD_TIME}" \
    -o bin/xgrabba-viewer-mac-arm64 ./cmd/viewer && \
    CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build \
    -ldflags="-s -w -X main.Version=${VERSION} -X main.BuildTime=${BUILD_TIME}" \
    -o bin/xgrabba-viewer.exe ./cmd/viewer

# Runtime stage
FROM alpine:3.19

# Install ca-certificates, tzdata, and ffmpeg for video processing
RUN apk add --no-cache ca-certificates tzdata ffmpeg

# Create non-root user
RUN addgroup -g 1000 xgrabba && \
    adduser -u 1000 -G xgrabba -h /app -D xgrabba

WORKDIR /app

# Copy binary from builder
COPY --from=builder /app/xgrabba .

# Copy viewer binaries for USB export
COPY --from=builder /app/bin ./bin

# Create data directories
RUN mkdir -p /data/videos /data/temp && \
    chown -R xgrabba:xgrabba /data

USER xgrabba

# Default environment variables
ENV SERVER_HOST=0.0.0.0 \
    SERVER_PORT=9847 \
    STORAGE_PATH=/data/videos \
    STORAGE_TEMP_PATH=/data/temp \
    WORKER_COUNT=2

EXPOSE 9847

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD wget -qO- http://localhost:9847/health || exit 1

ENTRYPOINT ["/app/xgrabba"]
