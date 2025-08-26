# Multi-stage Dockerfile for Envoy Prometheus Exporter
# Builds with comprehensive build information

# Build stage
FROM golang:1.21-alpine AS builder

# Install git for version information
RUN apk add --no-cache git make

# Set working directory
WORKDIR /app

# Build arguments for version information
ARG VERSION=dev
ARG GIT_COMMIT=unknown
ARG GIT_BRANCH=unknown
ARG BUILD_TIME=unknown
ARG BUILD_USER=docker
ARG BUILD_HOST=docker

# Copy go mod files first for better caching
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Get Go and platform information
RUN GO_VERSION=$(go version) && \
    PLATFORM=$(go env GOOS)/$(go env GOARCH) && \
    MODULE_NAME=$(go list -m) && \
    GO_MOD_VERSION=$(go list -m -f '{{.GoVersion}}') && \
    \
    # Build with comprehensive version information
    CGO_ENABLED=0 GOOS=linux go build \
    -ldflags "-s -w \
        -X 'main.Version=${VERSION}' \
        -X 'main.GitCommit=${GIT_COMMIT}' \
        -X 'main.GitBranch=${GIT_BRANCH}' \
        -X 'main.BuildTime=${BUILD_TIME}' \
        -X 'main.BuildUser=${BUILD_USER}' \
        -X 'main.BuildHost=${BUILD_HOST}' \
        -X 'main.GoVersion=${GO_VERSION}' \
        -X 'main.Platform=${PLATFORM}' \
        -X 'main.ModuleName=${MODULE_NAME}' \
        -X 'main.GoModVersion=${GO_MOD_VERSION}'" \
    -o envoy-prometheus-exporter .

# Create version info for web interface
RUN echo "{\
    \"version\": \"${VERSION}\",\
    \"git_commit\": \"${GIT_COMMIT}\",\
    \"git_branch\": \"${GIT_BRANCH}\",\
    \"build_time\": \"${BUILD_TIME}\",\
    \"build_user\": \"${BUILD_USER}\",\
    \"build_host\": \"${BUILD_HOST}\",\
    \"go_version\": \"$(go version)\",\
    \"platform\": \"$(go env GOOS)/$(go env GOARCH)\",\
    \"module_name\": \"$(go list -m)\"\
}" > web/version.json

# Final stage
FROM alpine:latest

# Install ca-certificates for HTTPS requests
RUN apk --no-cache add ca-certificates tzdata

# Create non-root user
RUN addgroup -g 1000 envoy && \
    adduser -u 1000 -G envoy -s /bin/sh -D envoy

# Set working directory
WORKDIR /app

# Copy binary and web files from builder
COPY --from=builder /app/envoy-prometheus-exporter .
COPY --from=builder /app/web ./web
COPY --from=builder /app/web/version.json ./web/

# Create config directory
RUN mkdir -p /app/config && chown -R envoy:envoy /app

# Switch to non-root user
USER envoy

# Expose port
EXPOSE 8080

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8080/health || exit 1

# Default command
ENTRYPOINT ["./envoy-prometheus-exporter"]
CMD ["/app/config/envoy_config.xml"]

# Labels for metadata
LABEL org.opencontainers.image.title="Envoy Prometheus Exporter"
LABEL org.opencontainers.image.description="Enhanced configuration-driven Envoy solar inverter Prometheus exporter with MQTT publishing"
LABEL org.opencontainers.image.version="${VERSION}"
LABEL org.opencontainers.image.source="https://github.com/your-repo/envoy-prometheus-exporter"
LABEL org.opencontainers.image.created="${BUILD_TIME}"
LABEL org.opencontainers.image.revision="${GIT_COMMIT}"
