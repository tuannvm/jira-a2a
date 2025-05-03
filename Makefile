.PHONY: build test clean run run-dev release-snapshot run-docker docker-compose-up docker-compose-down lint server jira-agent run-jira run-jira-dev run-both stop-both logs-both

# Variables
BINARY_NAME=jira-a2a
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
BUILD_DIR=bin
SERVER_CMD=cmd/infogathering
JIRA_CMD=cmd/jiraretrieval
SERVER_BINARY=$(BUILD_DIR)/infogathering
# CLIENT_BINARY removed
JIRA_BINARY=$(BUILD_DIR)/jiraretrieval

# Build all binaries
build: server jira-agent

# Build the server binary
server:
	mkdir -p $(BUILD_DIR)
	go build -ldflags "-X main.Version=$(VERSION)" -o $(SERVER_BINARY) ./$(SERVER_CMD)

# Client target removed

# Build the Jira Retrieval Agent binary
jira-agent:
	mkdir -p $(BUILD_DIR)
	go build -ldflags "-X main.Version=$(VERSION)" -o $(JIRA_BINARY) ./$(JIRA_CMD)

# Run tests
test:
	go test ./...

# Clean build artifacts
clean:
	rm -rf $(BUILD_DIR)

# Run the InfoGathering agent in development mode
run-dev:
	go run $(SERVER_CMD)/main.go

# Run the JiraRetrieval agent in development mode
run-jira-dev:
	go run $(JIRA_CMD)/main.go

# Run both agents together (in background)
run-both:
	@echo "Starting both agents..."
	@mkdir -p logs
	@echo "Starting InformationGatheringAgent..." 
	@go run $(SERVER_CMD)/main.go > logs/infogathering.log 2>&1 & echo $$! > .infogathering.pid
	@echo "Starting JiraRetrievalAgent..."
	@go run $(JIRA_CMD)/main.go > logs/jiraretrieval.log 2>&1 & echo $$! > .jiraretrieval.pid
	@echo "Both agents are running!"
	@echo "InformationGatheringAgent logs: logs/infogathering.log"
	@echo "JiraRetrievalAgent logs: logs/jiraretrieval.log"
	@echo "Use 'make stop-both' to stop both agents"

# Stop both agents
stop-both:
	@echo "Stopping both agents..."
	@if [ -f .infogathering.pid ]; then \
		echo "Stopping InformationGatheringAgent..."; \
		kill `cat .infogathering.pid` 2>/dev/null || true; \
		rm .infogathering.pid; \
	fi
	@if [ -f .jiraretrieval.pid ]; then \
		echo "Stopping JiraRetrievalAgent..."; \
		kill `cat .jiraretrieval.pid` 2>/dev/null || true; \
		rm .jiraretrieval.pid; \
	fi
	@echo "Both agents stopped!"

# Show logs from both agents in real-time
logs-both:
	@tail -f logs/infogathering.log logs/jiraretrieval.log

# Create a release snapshot using GoReleaser
release-snapshot:
	goreleaser release --snapshot --clean

# Run the InfoGathering server using the built binary
run: server
	./$(SERVER_BINARY)

# Run the JiraRetrieval server using the built binary
run-jira: jira-agent
	./$(JIRA_BINARY)

# Run client target removed

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
