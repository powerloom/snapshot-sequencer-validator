.PHONY: all build clean test aggregator monitor-api unified p2p-gateway dequeuer event-monitor

# Build output directory
BIN_DIR := bin

# Build all binaries
all: build

build: aggregator monitor-api unified p2p-gateway

# Individual component builds
aggregator:
	@echo "Building aggregator..."
	@mkdir -p $(BIN_DIR)
	@go build -o $(BIN_DIR)/aggregator ./cmd/aggregator

monitor-api:
	@echo "Building monitor-api..."
	@mkdir -p $(BIN_DIR)
	@go build -o $(BIN_DIR)/monitor-api ./cmd/monitor-api

unified:
	@echo "Building unified sequencer..."
	@mkdir -p $(BIN_DIR)
	@go build -o $(BIN_DIR)/unified ./cmd/unified

p2p-gateway:
	@echo "Building p2p-gateway..."
	@mkdir -p $(BIN_DIR)
	@go build -o $(BIN_DIR)/p2p-gateway ./cmd/p2p-gateway

# Aliases for unified sequencer components
dequeuer: unified
event-monitor: unified

# Clean build artifacts
clean:
	@echo "Cleaning build artifacts..."
	@rm -rf $(BIN_DIR)
	@rm -f aggregator monitor-api unified p2p-gateway

# Run tests
test:
	@echo "Running tests..."
	@go test -v ./...

# Run tests with race detection
test-race:
	@echo "Running tests with race detection..."
	@go test -race -v ./...

# Build with optimizations for production
build-prod:
	@echo "Building for production..."
	@mkdir -p $(BIN_DIR)
	@go build -ldflags="-s -w" -o $(BIN_DIR)/aggregator ./cmd/aggregator
	@go build -ldflags="-s -w" -o $(BIN_DIR)/monitor-api ./cmd/monitor-api
	@go build -ldflags="-s -w" -o $(BIN_DIR)/unified ./cmd/unified
	@go build -ldflags="-s -w" -o $(BIN_DIR)/p2p-gateway ./cmd/p2p-gateway
	@echo "Production builds complete in $(BIN_DIR)/"

# Install dependencies
deps:
	@echo "Installing dependencies..."
	@go mod download
	@go mod tidy

# Format code
fmt:
	@echo "Formatting code..."
	@go fmt ./...

# Run linter
lint:
	@echo "Running linter..."
	@golangci-lint run

# Display help
help:
	@echo "Available targets:"
	@echo "  make build         - Build all components"
	@echo "  make build-prod    - Build with production optimizations"
	@echo "  make aggregator    - Build aggregator component"
	@echo "  make monitor-api   - Build monitoring API"
	@echo "  make unified       - Build unified sequencer"
	@echo "  make p2p-gateway   - Build P2P gateway"
	@echo "  make clean         - Remove build artifacts"
	@echo "  make test          - Run tests"
	@echo "  make test-race     - Run tests with race detection"
	@echo "  make deps          - Install/update dependencies"
	@echo "  make fmt           - Format code"
	@echo "  make lint          - Run linter"
	@echo "  make help          - Show this help"