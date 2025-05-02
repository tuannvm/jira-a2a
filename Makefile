.PHONY: build test clean run run-dev release-snapshot run-docker docker-compose-up docker-compose-down lint server client run-client

# Variables
BINARY_NAME=jira-a2a
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_DIR=bin
SERVER_CMD=cmd/infogathering
CLIENT_CMD=cmd/test_a2a
SERVER_BINARY=$(BUILD_DIR)/infogathering
CLIENT_BINARY=$(BUILD_DIR)/test_a2a

# Build both server and client
build: server client

# Build the server binary
server:
	mkdir -p $(BUILD_DIR)
	go build -ldflags "-X main.Version=$(VERSION)" -o $(SERVER_BINARY) ./$(SERVER_CMD)

# Build the client binary
client:
	mkdir -p $(BUILD_DIR)
	go build -ldflags "-X main.Version=$(VERSION)" -o $(CLIENT_BINARY) ./$(CLIENT_CMD)

# Run tests
test:
	go test ./...

# Clean build artifacts
clean:
	rm -rf $(BUILD_DIR)

# Run the server in development mode
run-dev:
	go run $(SERVER_CMD)/main.go

# Create a release snapshot using GoReleaser
release-snapshot:
	goreleaser release --snapshot --clean

# Run the server using the built binary
run: server
	./$(SERVER_BINARY)

# Run the test client
run-client: client
	./$(CLIENT_BINARY)

# Build and run Docker image
run-docker: build
	docker build -t $(BINARY_NAME):$(VERSION) .
	docker run -p 8080:8080 $(BINARY_NAME):$(VERSION)

# Start the application with Docker Compose
docker-compose-up:
	docker-compose up -d

# Stop Docker Compose services
docker-compose-down:
	docker-compose down

# Run linting checks
lint:
	@echo "Running linters..."
	@go mod tidy
	@if ! git diff --quiet go.mod go.sum; then echo "go.mod or go.sum is not tidy, run 'go mod tidy'"; git diff go.mod go.sum; exit 1; fi
	@if ! command -v golangci-lint &> /dev/null; then echo "Installing golangci-lint..." && go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest; fi
	@golangci-lint run --timeout=5m

# Default target - clean and build
all: clean build