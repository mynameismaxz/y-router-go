# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is an **AI Gateway** service written in Go that acts as a compatibility layer between Anthropic Claude and OpenAI/OpenRouter API formats. It enables seamless integration with multiple LLM providers through a unified API format with advanced streaming, monitoring, and resilience features.

**Main Entry Point**: `cmd/server/main.go:24`

## Build Commands

```bash
# Build optimized binary
go build -ldflags="-s -w" -o bin/gateway ./cmd/server

# Build with debug symbols
go build -o bin/gateway ./cmd/server

# Download dependencies
go mod download

# Run directly without building
go run ./cmd/server
```

**Go Version Required**: 1.25.1 or later

## Development Commands

- **Linting** (using `go vet` and `staticcheck`):
  ```bash
  go vet ./...
  staticcheck ./...
  ```
- **Format code**:
  ```bash
  gofmt -s -w .
  ```
- **Run all tests** (including race detector):
  ```bash
  go test -v -race ./...
  ```
- **Run a single test** (replace `TestName` with the desired test):
  ```bash
  go test -v ./path/to/package -run ^TestName$
  ```
- **Build the binary** (debug symbols):
  ```bash
  go build -o bin/gateway ./cmd/server
  ```
- **Build optimized binary**:
  ```bash
  go build -ldflags="-s -w" -o bin/gateway ./cmd/server
  ```
- **Run the server without building**:
  ```bash
  go run ./cmd/server
  ```

These commands cover the most common development workflow steps for this Go codebase.

## High-Level Architecture

The gateway is organized as a set of loosely‑coupled components leveraging dependency injection:
- **Entry point** (`cmd/server/main.go`) wires configuration, logger, and the streaming component manager.
- **Configuration** (`internal/config`) loads env vars and validates them.
- **HTTP layer** (`internal/handlers`, `internal/middleware`) handles routing, validation, CORS and recovery.
- **Core services** (`internal/services`) implement conversion, streaming, buffering, circuit breaking, retries and monitoring.
- **Models** (`internal/models`) define the Anthropic and OpenAI request/response schemas.
- **Static assets** (`internal/static`) serve UI pages.

This structure follows clean‑architecture principles: the outer layers (HTTP, CLI) depend on inner service interfaces, enabling easy testing and future extensions.

## Running the Application

```bash
# Run with default configuration
./bin/gateway

# Run with custom port and log level
PORT=3000 LOG_LEVEL=debug ./bin/gateway

# Use .env file for configuration
cp .env.example .env
# Edit .env with your settings
./bin/gateway
```

**Health Check**: The binary supports a health check flag for Docker: `./bin/gateway --health-check`

## Testing

**Note**: No test files found in this repository. Testing infrastructure needs to be established.

```bash
# Run all tests (when tests are added)
go test -v ./...

# Run tests with race detection
go test -race ./...

# Run specific package tests
go test ./internal/services
go test ./internal/handlers
```

## Core Architecture

The application follows a **component-based architecture** with dependency injection and follows clean architecture principles.

### Directory Structure

- **`cmd/server/main.go`**: Application entry point with component initialization
- **`internal/config/`**: Configuration management (environment-based)
- **`internal/handlers/`**: HTTP handlers (API, health, static)
- **`internal/middleware/`**: HTTP middleware (CORS, logging, recovery)
- **`internal/models/`**: Data models for Anthropic/OpenAI API formats
- **`internal/services/`**: Core business logic services
- **`internal/static/`**: Static content (HTML pages, install script)

### Key Services (in `internal/services/`)

1. **Converter** (`converter.go`): Bidirectional conversion between Anthropic and OpenAI API formats
2. **StreamManager** (`stream_manager.go`): Lifecycle management for streaming components
3. **BufferManager** (`buffer_manager.go`): High-performance buffer management with overflow protection
4. **CircuitBreaker** (`circuit_breaker.go`): Resilience pattern for upstream services
5. **RetryManager** (`retry_manager.go`): Exponential backoff retry logic
6. **MonitoringService** (`monitoring_service.go`): Real-time metrics and health monitoring
7. **MetricsCollector** (`metrics_collector.go`): Performance metrics collection
8. **ErrorHandler** (`error_handler.go`): Error classification and recovery

### Request Flow

1. Client sends Anthropic-format request to `/v1/messages`
2. Request flows through middleware stack (logging, CORS, recovery)
3. API Handler validates and authenticates request (`internal/handlers/api.go:13`)
4. Format Converter transforms request to OpenAI format (`internal/services/converter.go:32`)
5. Stream Manager handles streaming with buffer management
6. Upstream service is called via HTTP client (`internal/services/client.go`)
7. Response is converted back to Anthropic format
8. Response is returned to client with monitoring metadata

## Configuration

**Environment Variables** (see `.env.example:1` for full list):

- `PORT`: Server port (default: 8080)
- `OPENROUTER_BASE_URL`: Upstream API base URL (default: https://openrouter.ai/api/v1)
- `LOG_LEVEL`: Log level - debug, info, warn, error (default: info)
- `SHUTDOWN_TIMEOUT`: Graceful shutdown timeout (default: 30s)

**Streaming Configuration** (all prefixed with `STREAMING_`):
- Connection timeouts, buffer sizes, retry settings, circuit breaker thresholds
- See `.env.example:17-122` for comprehensive list

Configuration is validated at startup in `internal/config/config.go:174`

## API Endpoints

- `POST /v1/messages`: Main API endpoint for LLM requests (Anthropic format)
- `GET /health`: Basic application health check
- `GET /healthz`: Kubernetes-style health probe
- `GET /ready`: Readiness probe
- `GET /metrics/summary`: Performance metrics summary (when enabled)
- `GET /`: Static index page
- `GET /terms`: Terms of service
- `GET /privacy`: Privacy policy
- `GET /install.sh`: Installation script for Claude Code setup

**Required Headers** for API requests:
- `Content-Type: application/json`
- `x-api-key: <your-api-key>`

**Optional Headers**:
- `X-Correlation-ID: <request-id>` - Custom correlation ID for tracking

## Dependencies

**Main Dependencies** (see `go.mod:1`):
- `github.com/gofiber/fiber/v2 v2.52.9`: Web framework
- `github.com/google/uuid v1.6.0`: UUID generation
- `github.com/joho/godotenv v1.5.1`: Environment file loading

Minimal dependencies - only 3 direct dependencies, all well-established Go packages.

## Key Design Patterns

1. **Component Manager Pattern**: `StreamingComponentManager` (`internal/services/stream_manager.go:19`) manages lifecycle of all streaming components with dependency injection
2. **Circuit Breaker Pattern**: Automatic failover for upstream service issues (`internal/services/circuit_breaker.go:11`)
3. **Buffer Management Pattern**: Overflow protection and flow control for streaming (`internal/services/buffer_manager.go:11`)
4. **Retry with Exponential Backoff**: Intelligent retry with circuit breaker integration (`internal/services/retry_manager.go:11`)
5. **Structured Logging**: JSON-formatted logs with correlation IDs (`internal/services/structured_logger.go:11`)

## Model Mapping

Automatic mapping from Anthropic models to OpenRouter models:

- `claude-3-sonnet-20240229` → `anthropic/claude-3-sonnet`
- `claude-3-haiku-20240307` → `anthropic/claude-3-haiku`
- `claude-3-opus-20240229` → `anthropic/claude-3-opus`
- `claude-3-5-sonnet-20241022` → `anthropic/claude-3.5-sonnet`

See `internal/models/common.go:11` for the `ModelMapper` implementation.

## Development Notes

- **Graceful Shutdown**: Properly handles SIGINT/SIGTERM with configurable timeout (`cmd/server/main.go:150`)
- **No Tests**: Currently no test suite - testing infrastructure needs to be established
- **Pre-built Binary**: A pre-built binary exists at `bin/server` but should rebuild after changes
- **Static Content**: Static HTML pages are embedded in Go code via `internal/static/content.go`
- **Middleware Stack**: Request ID → Logging → Recovery → CORS (in that order)

## Troubleshooting

**Port already in use**:
```bash
lsof -ti:8080 | xargs kill  # Kill process on port 8080
```

**Build fails**:
```bash
go mod download  # Ensure dependencies are downloaded
go version       # Verify Go 1.25.1+
```

**Configuration errors**: Check `.env` file format and validate environment variables using `internal/config/config.go:49`

## Important Files for Reference

- **Configuration Schema**: `internal/config/config.go`
- **Request Models**: `internal/models/anthropic.go` and `internal/models/openai.go`
- **API Handler**: `internal/handlers/api.go`
- **Format Conversion**: `internal/services/converter.go`
- **Stream Management**: `internal/services/stream_manager.go`
- **Environment Template**: `.env.example`
