# AI Gateway - Podman Makefile
# Comprehensive shortcuts for building, running, and managing the AI Gateway with Podman

# Variables
IMAGE_NAME=ai-gateway
IMAGE_TAG=latest
CONTAINER_NAME=ai-gateway
PODMAN=podman
PORT=8081
LOG_LEVEL=info
OPENROUTER_BASE_URL=https://openrouter.ai/api/v1
# Local Development
GO_BIN := bin/gateway
GO_MAIN := ./cmd/server

# Default target
.PHONY: help run-local run-dev-local build-local build-dev-local test lint clean-local dev-setup-local dev-local

# Show help information
help:
	@echo "🚀 AI Gateway - Podman Commands"
	@echo "================================="
	@echo ""
	@echo "📦 Build Commands:"
	@echo "  make build              - Build the container image"
	@echo "  make build-dev          - Build with debug symbols"
	@echo "  make build-clean        - Clean build cache and rebuild"
	@echo ""
	@echo "🚀 Run Commands:"
	@echo "  make run                - Run container in background"
	@echo "  make run-foreground     - Run container in foreground"
	@echo "  make run-dev            - Run with development settings"
	@echo "  make run-prod           - Run with production settings"
	@echo ""
	@echo "🔧 Management Commands:"
	@echo "  make stop               - Stop the container"
	@echo "  make restart            - Restart the container"
	@echo "  make logs               - Show container logs"
	@echo "  make logs-follow        - Follow container logs"
	@echo "  make shell              - Open shell in running container"
	@echo "  make status             - Show container status"
	@echo "  make clean              - Remove container and image"
	@echo ""
	@echo "📊 Monitoring Commands:"
	@echo "  make health             - Check health endpoint"
	@echo "  make metrics            - Show metrics endpoint"
	@echo "  make test-api           - Test basic API functionality"
	@echo "  make stats              - Show container statistics"
	@echo ""
	@echo "🔧 Development Commands:"
	@echo "  make dev-setup          - Setup development environment"
	@echo "  make dev-clean          - Clean development artifacts"
	@echo ""
	@echo "💻 Local Development Commands:"
	@echo "  make run-local          - Run with go run"
	@echo "  make run-dev-local      - Run with hot reload (air)"
	@echo "  make build-local        - Build optimized binary"
	@echo "  make build-dev-local    - Build with debug symbols"
	@echo "  make test               - Run tests with race detector"
	@echo "  make lint               - Format, vet, and staticcheck"
	@echo "  make clean-local        - Clean local binaries"
	@echo ""
	@echo "  make pod-create         - Create a pod for the service"
	@echo "  make pod-run            - Run in a pod"
	@echo "  make pod-stop           - Stop and remove pod"
	@echo ""
	@echo "🔒 Security Commands:"
	@echo "  make security-scan      - Scan image for vulnerabilities"
	@echo "  make security-hardened  - Run with security hardening"

# Build the container image
build:
	@echo "📦 Building $(IMAGE_NAME):$(IMAGE_TAG)..."
	$(PODMAN) build -t $(IMAGE_NAME):$(IMAGE_TAG) .
	@echo "✅ Build completed successfully!"

# Build with debug symbols for development
build-dev:
	@echo "📦 Building $(IMAGE_NAME):$(IMAGE_TAG) with debug symbols..."
	$(PODMAN) build --build-arg DEBUG=true -t $(IMAGE_NAME):dev .
	@echo "✅ Development build completed!"

# Clean build (remove cache and rebuild)
build-clean:
	@echo "🧹 Cleaning build cache and rebuilding..."
	$(PODMAN) build --no-cache -t $(IMAGE_NAME):$(IMAGE_TAG) .
	@echo "✅ Clean build completed!"

# Run container in background
run:
	@echo "🚀 Starting $(CONTAINER_NAME)..."
	$(PODMAN) run -d --name $(CONTAINER_NAME) \
		-p $(PORT):$(PORT) \
		-e PORT=$(PORT) \
		-e LOG_LEVEL=$(LOG_LEVEL) \
		-e OPENROUTER_BASE_URL=$(OPENROUTER_BASE_URL) \
		-e STREAMING_CONNECTION_TIMEOUT=120s \
		--health-interval=30s \
		--health-retries=3 \
		--health-timeout=10s \
		--restart=unless-stopped \
		$(IMAGE_NAME):$(IMAGE_TAG)
	@echo "✅ Container started! Access the service at http://localhost:$(PORT)"

# Run container in foreground
run-foreground:
	@echo "🚀 Starting $(CONTAINER_NAME) in foreground..."
	$(PODMAN) run --rm --name $(CONTAINER_NAME) \
		-p $(PORT):$(PORT) \
		-e PORT=$(PORT) \
		-e LOG_LEVEL=$(LOG_LEVEL) \
		-e OPENROUTER_BASE_URL=$(OPENROUTER_BASE_URL) \
		$(IMAGE_NAME):$(IMAGE_TAG)

# Run with development settings
run-dev:
	@echo "🚀 Starting $(CONTAINER_NAME) in development mode..."
	$(PODMAN) run -d --name $(CONTAINER_NAME) \
		-p $(PORT):$(PORT) \
		-p 6060:6060 \
		-e PORT=$(PORT) \
		-e LOG_LEVEL=debug \
		-e OPENROUTER_BASE_URL=$(OPENROUTER_BASE_URL) \
		-e ENABLE_PPROF=true \
		-e STREAMING_ENABLE_TRACING=true \
		-v /tmp/ai-gateway-dev:/app/debug \
		--rm \
		$(IMAGE_NAME):dev

# Run with production settings
run-prod:
	@echo "🚀 Starting $(CONTAINER_NAME) in production mode..."
	$(PODMAN) run -d --name $(CONTAINER_NAME) \
		-p $(PORT):$(PORT) \
		-e PORT=$(PORT) \
		-e LOG_LEVEL=$(LOG_LEVEL) \
		-e OPENROUTER_BASE_URL=$(OPENROUTER_BASE_URL) \
		--memory=512m \
		--cpus=1.0 \
		--health-interval=30s \
		--health-retries=3 \
		--health-timeout=10s \
		--restart=always \
		--security-opt=no-new-privileges \
		--cap-drop=ALL \
		--cap-add=CHOWN \
		--cap-add=SETGID \
		--cap-add=SETUID \
		$(IMAGE_NAME):$(IMAGE_TAG)

# Stop the container
stop:
	@echo "🛑 Stopping $(CONTAINER_NAME)..."
	$(PODMAN) stop $(CONTAINER_NAME) 2>/dev/null || true
	$(PODMAN) rm $(CONTAINER_NAME) 2>/dev/null || true
	@echo "✅ Container stopped and removed"

# Restart the container
restart:
	@echo "🔄 Restarting $(CONTAINER_NAME)..."
	$(PODMAN) restart $(CONTAINER_NAME)
	@echo "✅ Container restarted"

# Show container logs
logs:
	@echo "📋 Showing logs for $(CONTAINER_NAME)..."
	$(PODMAN) logs $(CONTAINER_NAME) --tail=100

# Follow container logs
logs-follow:
	@echo "📋 Following logs for $(CONTAINER_NAME)..."
	$(PODMAN) logs -f $(CONTAINER_NAME)

# Open shell in running container
shell:
	@echo "🐚 Opening shell in $(CONTAINER_NAME)..."
	$(PODMAN) exec -it $(CONTAINER_NAME) /bin/sh

# Show container status
status:
	@echo "📊 Container status:"
	$(PODMAN) ps --format "table {{.Names}}\t{{.Status}}\t{{.Ports}}\t{{.Image}}" | grep $(CONTAINER_NAME) || echo "Container not running"
	@echo ""
	@echo "🏥 Health check:"
	$(PODMAN) inspect $(CONTAINER_NAME) --format "{{.State.Health.Status}}" 2>/dev/null || echo "Container not running"

# Remove container and image
clean:
	@echo "🧹 Cleaning up..."
	$(PODMAN) stop $(CONTAINER_NAME) 2>/dev/null || true
	$(PODMAN) rm $(CONTAINER_NAME) 2>/dev/null || true
	$(PODMAN) rmi $(IMAGE_NAME):$(IMAGE_TAG) 2>/dev/null || true
	$(PODMAN) rmi $(IMAGE_NAME):dev 2>/dev/null || true
	@echo "✅ Cleanup completed"

# Check health endpoint
health:
	@echo "🏥 Checking health..."
	@sleep 3
	@curl -f http://localhost:$(PORT)/health || echo "❌ Health check failed"

# Show metrics endpoint
metrics:
	@echo "📊 Fetching metrics..."
	@curl -s http://localhost:$(PORT)/metrics/summary | jq . || echo "❌ Metrics endpoint not available"

# Test basic API functionality
test-api:
	@echo "🧪 Testing API..."
	@curl -X POST http://localhost:$(PORT)/v1/messages \
		-H "Content-Type: application/json" \
		-H "x-api-key: test-key" \
		-d '{
			"model": "claude-3-haiku-20240307",
			"max_tokens": 50,
			"messages": [{"role": "user", "content": "Hello from Podman Makefile!"}]
		}' || echo "❌ API test failed"

# Show container statistics
stats:
	@echo "📈 Container statistics:"
	$(PODMAN) stats $(CONTAINER_NAME) --no-stream

# Setup development environment
dev-setup: dev-setup-local
	@if ! command -v podman-compose &> /dev/null; then \
		echo "Installing podman-compose..."; \
		python3 -m pip install podman-compose; \
	fi
	@echo "✅ Full development environment ready (local + podman)"
	@if ! command -v podman-compose &> /dev/null; then \
		echo "Installing podman-compose..."; \
		python3 -m pip install podman-compose; \
	fi
	@echo "✅ Development environment ready"

# Clean development artifacts
dev-clean:
	@echo "🧹 Cleaning development artifacts..."
	rm -rf bin/
	rm -rf coverage/
	rm -rf .air.toml.bak
	find . -name "*.test" -delete
	find . -name "*.out" -delete
	@echo "✅ Development cleanup completed"

# Pod commands for multi-container setups
POD_NAME=ai-gateway-pod

pod-create:
	@echo "🏠 Creating pod $(POD_NAME)..."
	$(PODMAN) pod create --name $(POD_NAME) -p $(PORT):$(PORT)
	@echo "✅ Pod created"

pod-run:
	@echo "🚀 Running $(CONTAINER_NAME) in pod $(POD_NAME)..."
	$(PODMAN) pod start $(POD_NAME)
	$(PODMAN) run -d --pod $(POD_NAME) --name $(CONTAINER_NAME) \
		-e PORT=$(PORT) \
		-e LOG_LEVEL=$(LOG_LEVEL) \
		-e OPENROUTER_BASE_URL=$(OPENROUTER_BASE_URL) \
		$(IMAGE_NAME):$(IMAGE_TAG)
	@echo "✅ Container running in pod"

pod-stop:
	@echo "🛑 Stopping and removing pod $(POD_NAME)..."
	$(PODMAN) pod stop $(POD_NAME) 2>/dev/null || true
	$(PODMAN) pod rm $(POD_NAME) 2>/dev/null || true
	@echo "✅ Pod stopped and removed"

# Run locally with go run (restored)
run-local:
	@echo "🚀 Starting $(GO_MAIN) locally..."
	@mkdir -p bin
	@PORT=$(PORT) LOG_LEVEL=$(LOG_LEVEL) OPENROUTER_BASE_URL=$(OPENROUTER_BASE_URL) go run $(GO_MAIN)
	@echo "✅ Local server ready at http://localhost:$(PORT)"

# Build optimized local binary
build-local:
	@echo "📦 Building optimized binary $(GO_BIN)..."
	@mkdir -p bin
	@go build -ldflags="-s -w" -o $(GO_BIN) $(GO_MAIN)
	@echo "✅ Local build completed! Run with ./$(GO_BIN)"

# Build local binary with debug symbols
build-dev-local:
	@echo "📦 Building debug binary $(GO_BIN)..."
	@mkdir -p bin
	@go build -o $(GO_BIN) $(GO_MAIN)
	# Run tests with race detector
test:
	@echo "🧪 Running tests with race detector..."
	@go test -v -race ./...
	@echo "✅ Tests passed!"

# Lint code (format + vet + staticcheck)
lint:
	@echo "🔍 Linting code..."
	@gofmt -s -w -l .
	@go vet ./...
	@staticcheck ./...
	@echo "✅ Linting completed!"

# Clean local build artifacts
clean-local:
	@echo "🧹 Cleaning local binaries..."
	@rm -rf bin/
	@echo "✅ Local cleanup completed!"

# Local development workflow
dev-local: dev-setup-local build-dev-local run-dev-local
	@echo "🔒 Scanning image for vulnerabilities..."
	$(PODMAN) scan $(IMAGE_NAME):$(IMAGE_TAG) || echo "❌ Security scan failed"

security-hardened:
	@echo "🔒 Running hardened container..."
	$(PODMAN) run -d --name $(CONTAINER_NAME)-hardened \
		-p $(PORT):$(PORT) \
		-e PORT=$(PORT) \
		-e LOG_LEVEL=$(LOG_LEVEL) \
		-e OPENROUTER_BASE_URL=$(OPENROUTER_BASE_URL) \
		--security-opt=no-new-privileges \
		--cap-drop=ALL \
		--cap-add=CHOWN \
		--cap-add=SETGID \
		--cap-add=SETUID \
		--selinux-label=type:container_t \
		--read-only \
		--tmpfs /tmp \
		--tmpfs /run \
		$(IMAGE_NAME):$(IMAGE_TAG)
	@echo "✅ Hardened container started"

# Kubernetes YAML generation
k8s-yaml:
	@echo "☸️ Generating Kubernetes YAML..."
	$(PODMAN) generate kube $(CONTAINER_NAME) > ai-gateway-k8s.yaml
	@echo "✅ Kubernetes YAML generated: ai-gateway-k8s.yaml"

# Docker Compose compatibility
docker-compose:
	@echo "🐳 Converting to docker-compose format..."
	$(PODMAN) generate systemd --name $(CONTAINER_NAME) --files --new
	@echo "✅ Systemd files generated"

# Quick start - build and run everything
quick-start: build run health
	@echo "🎉 Quick start completed! Service is running at http://localhost:$(PORT)"

# Production deployment
deploy-prod: build-clean run-prod health
	@echo "🚀 Production deployment completed!"

# Development workflow (local + podman)
dev: dev-setup build-dev run-dev
dev-local: dev-setup-local build-dev-local run-dev-local

# Health check with retries
health-retry:
	@echo "🏥 Checking health with retries..."
	@for i in {1..30}; do \
		if curl -f http://localhost:$(PORT)/health >/dev/null 2>&1; then \
			echo "✅ Service is healthy"; \
			exit 0; \
		fi; \
		echo "⏳ Waiting for service... ($$i/30)"; \
		sleep 2; \
	done; \
	echo "❌ Service failed to become healthy"; \
	exit 1