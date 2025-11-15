# Multi-stage Dockerfile for AI Gateway - Podman Compatible
# This Dockerfile is optimized for Podman with security best practices

# Stage 1: Build stage
FROM golang:1.25.1-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git ca-certificates tzdata

# Set the working directory
WORKDIR /app

# Create non-root user for build
RUN adduser -D -s /bin/sh appuser

# Ensure the working directory is writable by the appuser
RUN chown appuser:appuser /app

# Copy go mod files first for better caching
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download && go mod verify

# Copy source code
COPY --chown=appuser:appuser . .

# Switch to non-root user for build
USER appuser

# Build the application with comprehensive flags
# -s -w: Strip debug symbols for smaller binary
# -buildmode=pie: Position independent executables
# -trimpath: Remove file system paths from binary
RUN GIT_COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "dev-build") && \
    BUILD_TIME=$(date -u +'%Y-%m-%dT%H:%M:%SZ') && \
    go build \
    -ldflags="-s -w -X 'main.version=$GIT_COMMIT' -X 'main.buildTime=$BUILD_TIME'" \
    -buildmode=pie \
    -trimpath \
    -o gateway ./cmd/server

# Stage 2: Runtime stage
FROM alpine:3.19

# Install runtime dependencies
RUN apk add --no-cache \
    ca-certificates \
    tzdata \
    dumb-init \
    && update-ca-certificates

# Set the working directory
WORKDIR /app

# Create non-root user for runtime
# -D: Create a system user without home directory
# -s /bin/sh: Set shell
RUN adduser -D -s /bin/sh -g "Gateway User" gateway

# Copy binary from builder stage
COPY --from=builder /app/gateway /app/gateway

# Copy static content if it exists
COPY --from=builder /app/internal/static /app/internal/static

# Set ownership of all files
RUN chown -R gateway:gateway /app

# Switch to non-root user
USER gateway

# Set environment variables for security
ENV GOMAXPROCS=2
ENV GOGC=100

# Health check configuration
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD /app/gateway --health-check

# Expose the default port on HTTP and HTTPS
EXPOSE 8080

# Use dumb-init for proper signal handling
ENTRYPOINT ["dumb-init", "--"]

# Run the gateway
CMD ["/app/gateway"]

# Labels for Podman/OpenShift compatibility
LABEL name="ai-gateway" \
    maintainer="AI Gateway Team" \
    version="1.0.0" \
    description="AI Gateway Service - Compatibility Layer for LLM Providers" \
    summary="High-performance Go gateway for Anthropic/OpenAI API compatibility" \
    url="https://github.com/anthropics/claude-code" \
    io.k8s.description="AI Gateway for LLM provider compatibility" \
    io.k8s.display-name="AI Gateway" \
    io.openshift.tags="ai,gateway,llm,anthropic,openai,go" \
    io.openshift.min-memory="256Mi" \
    io.openshift.min-cpu="500m"